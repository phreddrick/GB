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
// fence refuses every ordinary write, news and stored telegrams are
// buffered, and player-visible notifications are held back. When the body
// finishes, the whole world, the schedule, the buffered effects and a
// commit record are written in ONE bbolt transaction (db.CommitWorld). Only
// after that transaction has committed are the held notifications released.
//
// If the body panics, or the commit transaction fails, nothing of the turn
// is on disk; the in-memory world (which the body may have half advanced)
// is discarded and reloaded from the store, which holds the last commit,
// and the engine halts for operator attention. A crash at any point leaves
// the store at the previous commit by bbolt's guarantees; on restart the
// schedule says the turn has not happened (no-catch-up policy applies).

// commitKind labels a commit in the commit record and the metrics.
const (
	commitUpdate   = "update"
	commitSegment  = "segment"
	commitShutdown = "shutdown"
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

// beginTurnBody raises the write fence and starts buffering effects. Called
// on the actor immediately before the turn body.
func (s *State) beginTurnBody() {
	s.effects = turnEffects{active: true}
	s.store.SetWriteFence(true)
}

// endTurnBody lowers the write fence. The buffers stay until they are
// committed and released, or discarded.
func (s *State) endTurnBody() {
	s.store.SetWriteFence(false)
	s.effects.active = false
}

// discardTurnEffects drops every buffered effect of a failed turn.
func (s *State) discardTurnEffects() {
	s.effects = turnEffects{}
}

// releaseTurnEffects delivers the held notifications of a committed turn in
// the order they were produced. Store writes were part of the commit.
func (s *State) releaseTurnEffects() {
	notices := s.effects.notices
	s.effects = turnEffects{}
	if s.registry == nil {
		return
	}
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
}

// commitWorld writes the in-memory world, the actor's schedule and the
// buffered effects in one transaction, then publishes the schedule and
// world counters. On error nothing was written and the caller must treat
// memory as diverged from the store.
func (s *State) commitWorld(kind string) (*db.CommitRecord, error) {
	now := s.now()
	c := &db.WorldCommit{
		Kind:          kind,
		EngineVersion: engineVersion(),
		WallTime:      now,
		Ships:         s.ships,
		Stars:         s.stars,
		Planets:       s.planets,
		Races:         s.races,
		StarData:      s.sdata,
		Power:         &s.power,
		Blocks:        &s.blocks,
		PowerBlocks:   &s.powerBlocks,
		Schedule:      s.scheduleRecord(s.sched, now),
		News:          s.effects.news,
		Telegrams:     s.effects.telegrams,
		Progress:      s.Progress,
	}
	start := now
	rec, err := s.store.CommitWorld(c)
	d := s.now().Sub(start)
	s.metrics.CommitDuration.Observe(d.Seconds(), kind)
	if err != nil {
		s.metrics.Commits.Inc(kind, "error")
		return nil, err
	}
	s.metrics.Commits.Inc(kind, "ok")
	s.setCommitSeq(rec.CommitSeq)
	s.publishSchedule()
	s.publishWorld(s.status.universe)
	return rec, nil
}

// setCommitSeq records the last committed sequence for status and metrics.
func (s *State) setCommitSeq(seq uint64) {
	s.statusMu.Lock()
	s.status.commitSeq = seq
	s.statusMu.Unlock()
	s.metrics.CommitSeq.Set(float64(seq))
}

// reloadFromStore discards the in-memory world and schedule and reloads
// both from the store, which holds the last commit. It is the recovery
// boundary after a failed turn body or a failed commit (plan 8.2). The
// schedule is taken verbatim from the stored record (no overdue policy and
// no write): the engine is halting, so nothing fires until a restart, and
// restoreSchedule applies the policy then.
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
	s.logger.Info("in-memory state reloaded from the last commit", slog.String("op", "reload"), slog.String("reason", reason),
		slog.Int("num_updates", s.sched.numUpdates), slog.Int("num_segments", s.sched.numSegments))
	return nil
}

// failTurn is the common path after a turn body panicked or its commit
// failed: drop the buffered effects, reload the last commit, and make sure
// the engine is HALTED with a reason that says whether memory is trusted.
func (s *State) failTurn(kind turnKind, reason string) {
	s.discardTurnEffects()
	fenced := s.store.FencedWrites()
	if err := s.reloadFromStore(reason); err != nil {
		// Memory may be half advanced and could not be replaced: say so.
		s.haltWithDetail(reason, "reload from the store failed, in-memory state is untrusted: "+err.Error())
		s.logger.Error("reload after failed turn failed", slog.String("op", "reload"), slog.String("err", err.Error()))
	} else {
		s.haltWithDetail(reason, "in-memory state reloaded from the last commit")
	}
	s.logger.Error("turn failed; nothing of it was written", slog.String("op", "turn."+string(kind)),
		slog.String("reason", reason), slog.Uint64("fenced_writes_total", fenced))
	s.shout("\n*** The " + string(kind) + " failed and was rolled back; the server is HALTED until an operator restarts it. ***\n")
}

// haltWithDetail halts (or keeps the existing halt) and appends detail to
// the recorded reason, so that status says whether memory was reloaded.
func (s *State) haltWithDetail(reason, detail string) {
	s.halt(reason, "")
	s.statusMu.Lock()
	s.haltReason = s.haltReason + "; " + detail
	s.statusMu.Unlock()
}

// validateStoreCommit checks the commit record against the schedule record
// at startup (plan 8.3, counters only). The commit record and the schedule
// are written together, so a commit record that is AHEAD of the schedule
// can only come from a writer that bypassed the commit path; the engine
// then refuses to serve (HALTED, CORRUPT_STORE) and the operator decides.
// A schedule ahead of the commit record means turns were persisted by a
// binary from before atomic commits; that is accepted with a warning and
// the counters resynchronise at the next commit. A missing commit record
// (store older than this change) is accepted.
func (s *State) validateStoreCommit() (corrupt string, err error) {
	rec, err := s.store.GetCommitRecord()
	if err != nil {
		return "", err
	}
	if rec == nil {
		s.logger.Info("store has no commit record yet (written before atomic commits); the next commit creates it", slog.String("op", "startup"))
		return "", nil
	}
	s.setCommitSeq(rec.CommitSeq)
	sched, err := s.store.GetSchedule()
	if err != nil {
		return "", err
	}
	if sched == nil {
		return "", nil
	}
	switch {
	case rec.Turn > sched.NumUpdates:
		return fmt.Sprintf("CORRUPT_STORE: commit record is at turn %d (seq %d, kind %s) but the schedule record is at turn %d; the store was written outside the commit path",
			rec.Turn, rec.CommitSeq, rec.Kind, sched.NumUpdates), nil
	case rec.Turn < sched.NumUpdates:
		s.logger.Warn("schedule record is ahead of the commit record: turns were persisted by a pre-atomic-commit binary; counters resynchronise at the next commit",
			slog.String("op", "startup"), slog.Int("commit_turn", rec.Turn), slog.Int("schedule_turn", sched.NumUpdates), slog.Uint64("commit_seq", rec.CommitSeq))
	}
	return "", nil
}

// ErrCorruptStore is the halt reason prefix when startup validation fails.
var ErrCorruptStore = errors.New("store failed commit validation")

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
