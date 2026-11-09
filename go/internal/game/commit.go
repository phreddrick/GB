package game

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"

	"github.com/phreddrick/GB/go/internal/db"
)

// Atomic turn commits and reload-on-failure (CR-18; migration plan 8.2,
// 8.5, first slice of Phase 3a).
//
// While a turn body runs, nothing reaches the store: the store's write
// fence refuses every ordinary write, number allocations are staged, news
// and stored telegrams are buffered, and player-visible notifications are
// held back. When the body finishes, the whole world, the schedule, the
// buffered effects, the staged allocations and a commit record are written
// in ONE bbolt transaction (db.CommitWorld). Only after that transaction
// has committed are the held notifications released.
//
// Failure handling distinguishes four outcomes:
//   - the body panicked, or turn code hit the write fence: nothing was
//     attempted; memory is discarded and reloaded from the last commit;
//   - the commit transaction returned an error or panicked and the commit
//     record read back unchanged: nothing was written; same recovery;
//   - the commit's outcome cannot be established (the record cannot be
//     read back, or it changed in an unexpected way, or it advanced although
//     an error was reported): the engine halts with an explicit uncertain
//     reason and never claims a rollback;
//   - the commit succeeded but delivering the held notifications panicked:
//     the commit stands, store and memory are consistent, the engine halts
//     with a reason that says so.
//
// A crash at any point leaves the store at a complete commit by bbolt's
// guarantees: the previous one if the transaction had not committed, the
// new one if it had. Which one is shown by the durable commit record at the
// next start; a turn whose commit is absent is simply overdue (the
// no-catch-up policy applies). Player commands between turns still write
// per record outside this mechanism (CR-11, CR-17).

// commit kinds (db.CommitKind*), aliased for readability.
const (
	commitUpdate   = db.CommitKindUpdate
	commitSegment  = db.CommitKindSegment
	commitShutdown = db.CommitKindShutdown
)

// noticeKind distinguishes the three registry delivery paths.
type noticeKind uint8

const (
	noticeGovernor noticeKind = iota // Notify(player, governor)
	noticeRace                       // NotifyRace(player)
	noticeShout                      // Shout
)

type pendingNotice struct {
	kind     noticeKind
	player   int
	governor int
	text     string
}

// turnEffects holds everything a running turn produced that must not be
// visible, or durable, until the turn has committed.
type turnEffects struct {
	active    bool // a turn body is running; effects are buffered
	notices   []pendingNotice
	news      []db.NewsEntry
	telegrams []db.TelegramEntry // destined for the store (recipient offline)
}

// turnAllocator stages ship and commodity number allocation while a turn
// runs. Free-list entries are consumed from an in-memory copy and deleted
// by the commit (db.WorldCommit.ShipFreePops); numbers beyond the free list
// are issued above the highest number known (store and memory), so several
// allocations in one turn stay unique and a failed turn changes nothing in
// the store. Between turns allocation goes straight to the store, as before.
type turnAllocator struct {
	ship   numberAllocator
	commod numberAllocator
}

// numberAllocator is the staged state of one number space for one turn.
// issued is the per-turn reservation set: every number handed out is
// reserved whether or not the caller has inserted the object yet, so two
// allocations in one turn can never coincide (a freed highest-numbered
// object followed by two allocations is the case that needs it).
type numberAllocator struct {
	loaded bool
	free   []uint32 // remaining free entries, pop order (LIFO, as getdeadship in files_shl.c)
	pops   []uint32 // consumed this turn, pop order; deleted by the commit
	max    uint32   // highest number known: store, memory and numbers issued this turn
	issued map[uint32]struct{}
}

// load primes the allocator from the store's free list and highest number,
// raised by the highest number already in memory.
func (a *numberAllocator) load(free []uint32, max, inMemoryMax uint32) {
	if inMemoryMax > max {
		max = inMemoryMax
	}
	a.free, a.max, a.loaded = free, max, true
	a.issued = make(map[uint32]struct{})
}

// next issues one number: the most recently freed entry first, then the
// next number above everything known or issued. known reports numbers that
// exist in memory but may not be in the store yet.
func (a *numberAllocator) next(known func(uint32) bool) uint32 {
	if len(a.free) > 0 {
		n := a.free[0]
		a.free = a.free[1:]
		a.pops = append(a.pops, n)
		a.issued[n] = struct{}{}
		if n > a.max {
			a.max = n
		}
		return n
	}
	for {
		a.max++
		if _, dup := a.issued[a.max]; dup {
			continue
		}
		if known != nil && known(a.max) {
			continue
		}
		a.issued[a.max] = struct{}{}
		return a.max
	}
}

// beginTurnBody captures the fence baseline, resets the staged allocator,
// starts buffering effects and raises the write fence. Called on the actor
// immediately before the turn body.
func (s *State) beginTurnBody() {
	s.fenceBase = s.store.FencedWrites()
	s.turnAlloc = turnAllocator{}
	s.effects = turnEffects{active: true}
	s.store.SetWriteFence(true)
}

// endTurnBody lowers the write fence. The buffers and the staged
// allocations stay until they are committed and released, or discarded.
func (s *State) endTurnBody() {
	s.store.SetWriteFence(false)
	s.effects.active = false
}

// turnFenceViolations reports how many store writes the fence refused
// since beginTurnBody, and the last refused method. Only refusals of the
// current turn count: the baseline is captured at every turn start.
func (s *State) turnFenceViolations() (uint64, string) {
	n := s.store.FencedWrites() - s.fenceBase
	if n == 0 {
		return 0, ""
	}
	return n, s.store.LastFencedWrite()
}

// discardTurnEffects drops every buffered effect and staged allocation of a
// turn that did not commit.
func (s *State) discardTurnEffects() {
	s.effects = turnEffects{}
	s.turnAlloc = turnAllocator{}
}

// releaseTurnEffects delivers the held notifications of a committed turn in
// the order they were produced. Delivery is best-effort: a panic in the
// registry is recovered and returned so that the caller can report it
// without ever claiming the commit was undone.
func (s *State) releaseTurnEffects() (err error) {
	notices := s.effects.notices
	s.effects = turnEffects{}
	s.turnAlloc = turnAllocator{}
	if s.registry == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("notification delivery panicked: %v", r)
		}
	}()
	s.revokeSessions()
	for _, n := range notices {
		switch n.kind {
		case noticeGovernor:
			s.registry.Notify(n.player, n.governor, n.text)
		case noticeRace:
			s.registry.NotifyRace(n.player, n.text)
		case noticeShout:
			s.registry.Shout(n.text)
		}
	}
	return nil
}

// allocShipNum returns a ship number. During a turn body the allocation is
// staged (see turnAllocator); otherwise it is the store's immediate
// allocation, unchanged from before (free list first, then max+1).
func (s *State) allocShipNum() (uint32, error) {
	if !s.effects.active {
		return s.store.AllocShipNum()
	}
	a := &s.turnAlloc.ship
	known := func(n uint32) bool { _, ok := s.ships[n]; return ok }
	if !a.loaded {
		free, err := s.store.FreeShipNums()
		if err != nil {
			return 0, fmt.Errorf("staged ship allocation: %w", err)
		}
		max, err := s.store.MaxShipNum()
		if err != nil {
			return 0, fmt.Errorf("staged ship allocation: %w", err)
		}
		var memMax uint32
		for n := range s.ships {
			if n > memMax {
				memMax = n
			}
		}
		a.load(free, max, memMax)
	}
	return a.next(known), nil
}

// allocCommodNum is allocShipNum for commodity lots.
func (s *State) allocCommodNum() (uint32, error) {
	if !s.effects.active {
		return s.store.AllocCommodNum()
	}
	a := &s.turnAlloc.commod
	if !a.loaded {
		free, err := s.store.FreeCommodNums()
		if err != nil {
			return 0, fmt.Errorf("staged commodity allocation: %w", err)
		}
		max, err := s.store.MaxCommodNum()
		if err != nil {
			return 0, fmt.Errorf("staged commodity allocation: %w", err)
		}
		a.load(free, max, 0)
	}
	return a.next(nil), nil
}

// persistOutcome classifies what persistTurn established.
type persistOutcome int

const (
	persistCommitted      persistOutcome = iota // the commit record advanced; the turn is durable
	persistNothingWritten                       // the store still holds the previous commit
	persistUncertain                            // the outcome could not be established
)

type persistResult struct {
	outcome persistOutcome
	rec     *db.CommitRecord // the new record when committed (or visible after an uncertain error)
	reason  string           // human-readable, used as the halt reason on failure
}

// persistTurn writes the in-memory world, the actor's schedule, the
// buffered effects and the staged allocations in one transaction and
// classifies the outcome. A panic inside the store is recovered here. On
// persistCommitted the status snapshot and gauges are refreshed.
func (s *State) persistTurn(kind string) persistResult {
	prev, err := s.store.GetCommitRecord()
	if err != nil {
		// A corrupt record would make CommitWorld refuse; an I/O error means
		// the store is unreadable. Either way nothing was attempted.
		return persistResult{outcome: persistNothingWritten, reason: fmt.Sprintf("commit not attempted: commit record unreadable before commit: %v", err)}
	}
	var prevSeq uint64
	if prev != nil {
		prevSeq = prev.CommitSeq
	}
	now := s.now()
	c := &db.WorldCommit{
		Kind:           kind,
		EngineVersion:  engineVersion(),
		WallTime:       now,
		Ships:          s.ships,
		Stars:          s.stars,
		Planets:        s.planets,
		Races:          s.races,
		StarData:       s.sdata,
		Power:          &s.power,
		Blocks:         &s.blocks,
		PowerBlocks:    &s.powerBlocks,
		Schedule:       s.scheduleRecord(s.sched, now),
		News:           s.effects.news,
		Telegrams:      s.effects.telegrams,
		ShipFreePops:   s.turnAlloc.ship.pops,
		CommodFreePops: s.turnAlloc.commod.pops,
		Progress:       s.Progress,
	}
	var rec *db.CommitRecord
	var cerr error
	var panicVal any
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicVal = r
				s.logger.Error("panic inside the commit transaction", slog.String("op", "commit"), slog.String("kind", kind),
					slog.String("panic", fmt.Sprint(r)), slog.String("stack", string(debug.Stack())))
			}
		}()
		rec, cerr = s.store.CommitWorld(c)
		if s.testHooks != nil && s.testHooks.afterCommit != nil {
			s.testHooks.afterCommit(kind)
		}
	}()
	d := s.now().Sub(now)
	s.metrics.CommitDuration.Observe(d.Seconds(), kind)
	if cerr == nil && panicVal == nil && rec != nil {
		s.metrics.Commits.Inc(kind, "ok")
		s.setCommitSeq(rec.CommitSeq)
		s.publishSchedule()
		s.publishWorld(s.status.universe)
		return persistResult{outcome: persistCommitted, rec: rec}
	}
	failure := "error: " + fmt.Sprint(cerr)
	label := "error"
	if panicVal != nil {
		failure = fmt.Sprintf("panic: %v", panicVal)
		label = "panic"
	}
	// Classify by the durable record, never by assumption. The whole
	// record identity is compared, not only the sequence, so an altered
	// record with the previous sequence is not mistaken for "unchanged".
	back, rerr := s.store.GetCommitRecord()
	uncertain := func(format string, a ...any) persistResult {
		s.metrics.Commits.Inc(kind, "uncertain")
		return persistResult{outcome: persistUncertain, rec: back, reason: fmt.Sprintf("commit %s failed (%s) and %s: outcome uncertain", kind, failure, fmt.Sprintf(format, a...))}
	}
	expected := &db.CommitRecord{SchemaVersion: db.CommitSchemaVersion, CommitSeq: prevSeq + 1, Turn: c.Schedule.NumUpdates, Segment: c.Schedule.NumSegments,
		Kind: kind, WallTime: now.UTC(), EngineVersion: c.EngineVersion}
	switch {
	case rerr != nil:
		return uncertain("the commit record could not be read back (%v)", rerr)
	case back == nil && prev == nil:
		// Legacy store without a record: the commit would have created
		// sequence 1; its absence means the transaction did not commit.
		s.metrics.Commits.Inc(kind, label)
		return persistResult{outcome: persistNothingWritten, reason: fmt.Sprintf("commit %s failed (%s); the store still holds no commit record, nothing of the turn was written", kind, failure)}
	case back == nil:
		return uncertain("the commit record disappeared (was commit_seq %d)", prevSeq)
	case prev != nil && sameCommitRecord(back, prev):
		s.metrics.Commits.Inc(kind, label)
		return persistResult{outcome: persistNothingWritten, reason: fmt.Sprintf("commit %s failed (%s); the store still holds commit_seq %d, nothing of the turn was written", kind, failure, prevSeq)}
	case sameCommitRecord(back, expected):
		s.metrics.Commits.Inc(kind, "uncertain")
		return persistResult{outcome: persistUncertain, rec: back, reason: fmt.Sprintf("commit %s reported a %s after its record (commit_seq %d) became visible; durability on disk is uncertain", kind, failure, back.CommitSeq)}
	default:
		return uncertain("the commit record changed unexpectedly (commit_seq %d -> %d, kind %q, turn %d/%d)", prevSeq, back.CommitSeq, back.Kind, back.Turn, back.Segment)
	}
}

// sameCommitRecord compares the complete identity of two records.
func sameCommitRecord(a, b *db.CommitRecord) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.SchemaVersion == b.SchemaVersion && a.CommitSeq == b.CommitSeq && a.Turn == b.Turn && a.Segment == b.Segment &&
		a.Kind == b.Kind && a.WallTime.Equal(b.WallTime) && a.EngineVersion == b.EngineVersion
}

// setCommitSeq records the last committed sequence for status and metrics.
func (s *State) setCommitSeq(seq uint64) {
	s.statusMu.Lock()
	s.status.commitSeq = seq
	s.statusMu.Unlock()
	s.metrics.CommitSeq.Set(float64(seq))
}

// reloadFromStore discards the in-memory world and schedule and reloads
// both from the store as it is now visible (the last commit). It is the
// recovery boundary after a failed turn body or a failed commit (plan
// 8.2). The schedule is taken verbatim from the stored record (no overdue
// policy and no write): the engine is halting, so nothing fires until a
// restart, and restoreSchedule applies the policy then. The commit sequence
// shown in status is refreshed from the store when its record is valid.
func (s *State) reloadFromStore(reason string) error {
	if err := s.loadAll(); err != nil {
		return fmt.Errorf("reload world: %w", err)
	}
	rec, err := s.store.GetSchedule()
	if err != nil {
		return fmt.Errorf("reload schedule: %w", err)
	}
	if rec != nil {
		s.sched = schedule{nextUpdate: rec.NextUpdate, nextSegment: rec.NextSegment, numUpdates: rec.NumUpdates, numSegments: rec.NumSegments}
	}
	s.publishSchedule()
	cr, err := s.store.GetCommitRecord()
	if err != nil {
		// A reload that cannot establish which commit it loaded is not a
		// successful reload: the caller reports memory as untrusted.
		return fmt.Errorf("reload commit record: %w", err)
	}
	if cr != nil {
		s.setCommitSeq(cr.CommitSeq)
	}
	s.logger.Info("in-memory state reloaded from the store", slog.String("op", "reload"), slog.String("reason", reason),
		slog.Int("num_updates", s.sched.numUpdates), slog.Int("num_segments", s.sched.numSegments))
	return nil
}

// failTurn is the common path after a turn did not commit: drop the
// buffered effects and staged allocations, reload the store's state, and
// halt with a reason that states what is known. nothingWritten is true
// only when the store was shown to hold the previous commit; otherwise the
// reason says the outcome is uncertain and never claims a rollback.
func (s *State) failTurn(kind turnKind, reason string, nothingWritten bool) {
	s.discardTurnEffects()
	rerr := s.reloadFromStore(reason)
	var detail, shout string
	switch {
	case rerr != nil:
		detail = "reload from the store failed, in-memory state is untrusted: " + rerr.Error()
		shout = "failed; the server could not reload its state and is HALTED until an operator restarts it"
	case nothingWritten:
		detail = "in-memory state reloaded from the last commit"
		shout = "failed and was rolled back; the server is HALTED until an operator restarts it"
	default:
		detail = fmt.Sprintf("commit outcome uncertain; in-memory state reloaded from the store as now visible (commit_seq %d)", s.Status().LastCommitSeq)
		shout = "ended with an uncertain outcome; the server is HALTED until an operator restarts it"
	}
	s.haltWithDetail(reason, detail)
	s.logger.Error("turn failed", slog.String("op", "turn."+string(kind)), slog.String("reason", reason),
		slog.String("detail", detail), slog.Bool("nothing_written", nothingWritten && rerr == nil))
	s.shout("\n*** The " + string(kind) + " " + shout + ". ***\n")
}

// haltWithDetail halts (or keeps the existing halt) and appends detail to
// the recorded reason, so that status says what is known about memory and
// the store.
func (s *State) haltWithDetail(reason, detail string) {
	s.halt(reason, "")
	s.statusMu.Lock()
	s.haltReason = s.haltReason + "; " + detail
	s.statusMu.Unlock()
}

// validateStoreCommit checks the commit record and the schedule record at
// startup (plan 8.3). It returns a non-empty corrupt reason when the engine
// must refuse to serve (HALTED, CORRUPT_STORE, nothing written) and an
// error only for I/O failures. Rules:
//   - no commit record (store older than this change): accepted, logged;
//   - a present record must decode and validate (db.CommitRecord.Validate:
//     schema version, kind, non-zero and non-overflowing sequence, counters);
//   - the schedule record, when present, must decode with non-negative
//     counters;
//   - a non-init commit record without a schedule record is corrupt (both
//     are written in the same transaction); an init record without one is
//     the supported fresh-universe case;
//   - a commit record whose turn is ahead of the schedule's can only come
//     from a writer that bypassed the commit path: corrupt;
//   - a schedule ahead of the commit record means turns were persisted by a
//     pre-commit binary: accepted with a warning, resynchronised at the
//     next commit; a segment-counter difference at the same turn is logged
//     (the overdue policy legitimately resets the segment counter).
func (s *State) validateStoreCommit() (corrupt string, err error) {
	rec, err := s.store.GetCommitRecord()
	if err != nil {
		if errors.Is(err, db.ErrCorruptCommitRecord) {
			return "CORRUPT_STORE: " + err.Error(), nil
		}
		return "", err
	}
	sched, err := s.store.GetSchedule()
	if err != nil {
		if errors.Is(err, db.ErrCorruptScheduleRecord) {
			return "CORRUPT_STORE: " + err.Error(), nil
		}
		return "", err
	}
	if rec == nil {
		s.logger.Info("store has no commit record yet (written before atomic commits); the next commit creates it", slog.String("op", "startup"))
		return "", nil
	}
	s.setCommitSeq(rec.CommitSeq)
	if sched == nil {
		if rec.Kind == db.CommitKindInit {
			return "", nil
		}
		return fmt.Sprintf("CORRUPT_STORE: commit record kind %q (seq %d, turn %d) is present but the schedule record is missing; commits write both together", rec.Kind, rec.CommitSeq, rec.Turn), nil
	}
	switch {
	case rec.Turn > sched.NumUpdates:
		return fmt.Sprintf("CORRUPT_STORE: commit record is at turn %d (seq %d, kind %s) but the schedule record is at turn %d; the store was written outside the commit path",
			rec.Turn, rec.CommitSeq, rec.Kind, sched.NumUpdates), nil
	case rec.Turn < sched.NumUpdates:
		s.logger.Warn("schedule record is ahead of the commit record: turns were persisted by a pre-atomic-commit binary; counters resynchronise at the next commit",
			slog.String("op", "startup"), slog.Int("commit_turn", rec.Turn), slog.Int("schedule_turn", sched.NumUpdates), slog.Uint64("commit_seq", rec.CommitSeq))
	case rec.Segment != sched.NumSegments:
		s.logger.Info("segment counter differs between the commit record and the schedule record (the overdue policy resets it); resynchronises at the next commit",
			slog.String("op", "startup"), slog.Int("commit_segment", rec.Segment), slog.Int("schedule_segment", sched.NumSegments))
	}
	return "", nil
}

var (
	engineVersionOnce sync.Once
	engineVersionVal  string
)

// engineVersion returns the binary's module version and VCS revision as
// recorded by the Go toolchain, for the commit record.
func engineVersion() string {
	engineVersionOnce.Do(func() {
		info, ok := debug.ReadBuildInfo()
		if !ok {
			engineVersionVal = "unknown"
			return
		}
		v := info.Main.Version
		rev, modified := "", ""
		for _, kv := range info.Settings {
			switch kv.Key {
			case "vcs.revision":
				rev = kv.Value
			case "vcs.modified":
				if kv.Value == "true" {
					modified = "+dirty"
				}
			}
		}
		if rev != "" {
			if len(rev) > 12 {
				rev = rev[:12]
			}
			v += "@" + rev + modified
		}
		engineVersionVal = v
	})
	return engineVersionVal
}
