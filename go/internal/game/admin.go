package game

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/enroll"
	"github.com/phreddrick/GB/go/internal/universe"
)

// adminOp names a typed request processed by the actor (CR-04: admin
// operations are never text commands and never carry a nil responder).
type adminOp string

const (
	opStatus       adminOp = "status"
	opUpdate       adminOp = "update"
	opSegment      adminOp = "segment"
	opQuiesce      adminOp = "quiesce"
	opResume       adminOp = "resume"
	opInitUniverse adminOp = "universe.init"
	opEnroll       adminOp = "enroll"
	opAuthenticate adminOp = "authenticate"
	opWelcome      adminOp = "welcome"
	opTest         adminOp = "test"
)

type adminRequest struct {
	op    adminOp
	fn    func() (any, error)
	state atomic.Int32     // reqQueued -> reqStarted (actor) or reqCanceled (caller); lifecycle.go
	reply chan adminResult // buffered(1): exactly one send, by the actor, never blocks
}

// adminResult is the only channel through which a request's outcome reaches
// its caller (GB1-01).
type adminResult struct {
	val any
	err error
}

// handleAdmin runs one typed request on the actor goroutine. A request whose
// caller canceled it while it was queued is skipped: it was reported as not
// executed and must never run later. The request's function runs under
// panic isolation; a panic halts the engine and is reported as ErrHalted.
func (s *State) handleAdmin(req *adminRequest) {
	if !req.state.CompareAndSwap(reqQueued, reqStarted) {
		s.logger.Info("admin request skipped: canceled by its caller before it started", slog.String("op", "admin."+string(req.op)))
		return
	}
	var val any
	var err error
	panicked := s.guard(string(req.op), func() { val, err = req.fn() }, nil)
	if panicked {
		val, err = nil, ErrHalted
	}
	// Buffered channel with exactly one send: never blocks, even if the
	// caller has gone away. val and err are actor-local until this send.
	req.reply <- adminResult{val: val, err: err}
}

// statusData is the snapshot HTTP handlers read under statusMu.
type statusData struct {
	sched     schedule
	universe  *db.UniverseRecord
	stars     int
	races     int
	ships     int
	lastTurn  time.Time
	commitSeq uint64 // last committed sequence (0 when the store has no commit record yet)
}

// Status returns the current status without going through the actor queue,
// so it works during long turns and while halted.
func (s *State) Status() Status {
	s.statusMu.Lock()
	d := s.status
	halt := s.haltReason
	maint := s.maint.snapshot()
	s.statusMu.Unlock()
	mode := s.Mode()
	st := Status{
		Mode:           mode.String(),
		Draining:       s.draining.Load(),
		HaltReason:     halt,
		NextUpdate:     d.sched.nextUpdate,
		NextSegment:    d.sched.nextSegment,
		NumUpdates:     d.sched.numUpdates,
		NumSegments:    d.sched.numSegments,
		UpdateMinutes:  s.cfg.DefaultUpdateMinutes,
		MovesPerUpdate: s.cfg.MovesPerUpdate,
		Stars:          d.stars,
		Races:          d.races,
		Ships:          d.ships,
		Universe:       d.universe,
		QueueDepth:     len(s.cmdCh),
		LastCommitSeq:  d.commitSeq,
		GameReady:      (mode == ModeServing || mode == ModeTurnInProgress) && !s.draining.Load(),
		Maintenance:    maint,
		Persistence:    PersistenceNote,
	}
	if ts := s.turnStartNs.Load(); ts != 0 {
		t := time.Unix(0, ts)
		st.TurnRunning = true
		st.TurnStarted = &t
	}
	return st
}

// adminResultLabel maps an error to a bounded metrics label.
func adminResultLabel(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrHalted):
		return "halted"
	case errors.Is(err, ErrNotServing):
		return "refused"
	case errors.Is(err, universe.ErrAlreadyInitialized):
		return "refused"
	case errors.Is(err, ErrNotExecuted):
		return "not_executed"
	case errors.Is(err, ErrStillRunning):
		return "still_running"
	case errors.Is(err, ErrEngineBusy):
		return "busy"
	case errors.Is(err, ErrEngineStopped):
		return "stopped"
	}
	return "error"
}

func (s *State) adminDone(op adminOp, start time.Time, err error) {
	s.metrics.AdminOps.Inc(string(op), adminResultLabel(err))
	attrs := []any{slog.String("op", "admin."+string(op)), slog.Int64("duration_ms", s.now().Sub(start).Milliseconds()), slog.String("mode", s.Mode().String())}
	if err != nil {
		s.logger.Warn("admin operation failed", append(attrs, slog.String("err", err.Error()))...)
		return
	}
	s.logger.Info("admin operation", attrs...)
}

// TriggerUpdate runs a full update now (replaces the @@update text command).
// It waits for the turn to finish or for ctx to end; a turn accepted by the
// actor runs to completion even if the caller stops waiting.
func (s *State) TriggerUpdate(ctx context.Context) error {
	start := s.now()
	err := s.request(ctx, opUpdate, func() error { return s.runTurn(turnUpdate, "admin") })
	s.adminDone(opUpdate, start, err)
	return err
}

// TriggerSegment runs a movement segment now (replaces @@segment).
func (s *State) TriggerSegment(ctx context.Context) error {
	start := s.now()
	err := s.request(ctx, opSegment, func() error { return s.runTurn(turnSegment, "admin") })
	s.adminDone(opSegment, start, err)
	return err
}

// Quiesce pauses the engine: scheduled turns and mutating player commands
// are refused until Resume. It takes effect at the next operation boundary
// (after a running turn: admin requests are serialized on the actor).
//
// Durability (CR-43, ADR-0010): the maintenance record is written to the
// store BEFORE the mode changes, so a crash at any point leaves either no
// record and a SERVING restart (the caller received no success) or a durable
// record and a QUIESCED restart. If the write fails the engine still stops
// turns and mutating commands (quiescing is a safety action and must not be
// refused), but the request returns ErrMaintenanceNotDurable, status shows
// maintenance.durable=false with the error, and nothing ever reports that
// a restart would preserve the quiesce. Quiescing an already durably
// quiesced engine is a no-op; quiescing one that is quiesced in memory
// only retries the write.
func (s *State) Quiesce(ctx context.Context) error {
	start := s.now()
	err := s.request(ctx, opQuiesce, func() error {
		switch s.Mode() {
		case ModeServing, ModeQuiesced:
			if s.Mode() == ModeQuiesced && s.maintenanceDurable() {
				return nil
			}
			rec := &db.MaintenanceRecord{
				SchemaVersion: db.MaintenanceSchemaVersion,
				Quiesced:      true,
				SetAt:         s.now().UTC(),
				Reason:        "operator quiesce (admin API)",
				EngineVersion: engineVersion(),
			}
			intended, encErr := db.EncodeMaintenanceRecord(rec)
			if encErr != nil {
				return encErr
			}
			before := s.snapshotMetaForWrite()
			if err := s.store.PutMaintenance(rec); err != nil {
				// Classify by reading back (a bbolt error after the meta page
				// was written leaves the record visible): never report
				// "not written" from the error path alone, and never promise
				// QUIESCED on the strength of a record this process has not
				// confirmed.
				detail := s.classifyMaintenanceWriteFailure(intended, before)
				s.setMaintenance(rec, false, errors.New(detail))
				s.setMode(ModeQuiesced)
				s.logger.Error("quiesce: maintenance record write failed; QUIESCED in memory, durability not confirmed",
					slog.String("op", "admin.quiesce"), slog.String("err", err.Error()), slog.String("store", detail))
				return fmt.Errorf("%w: %s: %w", ErrMaintenanceNotDurable, detail, err)
			}
			s.setMaintenance(rec, true, nil)
			s.setMode(ModeQuiesced)
			return nil
		case ModeHalted:
			return ErrHalted
		}
		return fmt.Errorf("%w: cannot quiesce in mode %s", ErrNotServing, s.Mode())
	})
	s.adminDone(opQuiesce, start, err)
	return err
}

// Resume returns a quiesced engine to serving. A halted engine cannot be
// resumed: in-memory state may be inconsistent and only a restart (which
// reloads the store) is safe. Resume is refused while shutdown is draining.
//
// Ordering (GB1-03, CR-43): the rescheduled times are computed on a copy
// and written to the store first, in the same transaction that deletes the
// maintenance record (db.Store.ClearMaintenanceWithSchedule), so the end of
// maintenance and the resumed schedule are durable together. Only after
// that write succeeds does the actor adopt the new schedule, publish it and
// switch to SERVING (which is what makes /readyz report game_ready). If the
// write reports an error the store is failing: the engine is HALTED (fail
// closed, like a failed turn flush), game readiness stays false, mutating
// work is refused, and shutdown will not flush memory. What the next start
// will do is then established by reading the store back, never assumed
// (classifyResumeFailure): a confirmed record still present means QUIESCED;
// a visible resume means SERVING if it persisted; anything else is reported
// as not guaranteed.
func (s *State) Resume(ctx context.Context) error {
	start := s.now()
	err := s.request(ctx, opResume, func() error {
		switch s.Mode() {
		case ModeQuiesced:
			if s.draining.Load() {
				return fmt.Errorf("%w: shutdown in progress", ErrNotServing)
			}
			// Scheduled times that passed while quiesced are not caught up:
			// move them forward from now so the actor does not fire at once.
			now := s.now()
			next := s.sched
			overdue := false
			if !now.Before(next.nextUpdate) {
				next.nextUpdate = now.Add(s.updateInterval())
				next.nextSegment = now.Add(s.segmentInterval())
				next.numSegments = 0
				overdue = true
			} else if !now.Before(next.nextSegment) {
				next.nextSegment = now.Add(s.segmentInterval())
				if next.nextSegment.After(next.nextUpdate) {
					next.nextSegment = next.nextUpdate
				}
			}
			resumeRec := s.scheduleRecord(next, now)
			intended, encErr := db.EncodeScheduleRecord(resumeRec)
			if encErr != nil {
				return encErr
			}
			before := s.snapshotMetaForWrite()
			if err := s.store.ClearMaintenanceWithSchedule(resumeRec); err != nil {
				// The transaction reported an error, which does not establish
				// a rollback: read the store back and say what the next
				// start will do (CR-43 review P2).
				detail := s.classifyResumeFailure(intended, before)
				s.halt("resume: persist schedule and clear maintenance: "+err.Error()+"; "+detail, "")
				return fmt.Errorf("resume failed and the engine is now HALTED (restart required): %w; %s", err, detail)
			}
			s.sched = next
			s.publishSchedule()
			s.clearMaintenance()
			s.setMode(ModeServing)
			if overdue {
				s.logger.Warn("resume: overdue update rescheduled, not run (no-catch-up policy)", slog.String("op", "admin.resume"), slog.Time("next_update", s.sched.nextUpdate))
			}
			return nil
		case ModeServing:
			return nil
		case ModeHalted:
			return ErrHalted
		}
		return fmt.Errorf("%w: cannot resume in mode %s", ErrNotServing, s.Mode())
	})
	s.adminDone(opResume, start, err)
	return err
}

// InitUniverse generates and stores a universe through the running server's
// single store handle (CR-05: no second writer), then loads it and starts
// the schedule. It is refused unless the engine is ModeUninitialized; the
// store-level check inside universe.Init refuses an initialised store even
// if the mode were wrong, and two concurrent requests are serialized by the
// actor so the second one is refused.
func (s *State) InitUniverse(ctx context.Context, params universe.Params, seed int64) (*db.UniverseRecord, error) {
	start := s.now()
	rec, err := call(s, ctx, opInitUniverse, func() (*db.UniverseRecord, error) {
		switch s.Mode() {
		case ModeUninitialized:
		case ModeHalted:
			return nil, ErrHalted
		default:
			return nil, fmt.Errorf("%w: universe init requires mode %s (current %s)", universe.ErrAlreadyInitialized, ModeUninitialized, s.Mode())
		}
		params.RatioXY = s.cfg.RatioXY
		r, err := universe.Init(s.store, params, seed, "gb-server admin", s.now())
		if err != nil {
			return nil, err
		}
		if err := s.loadAll(); err != nil {
			// The universe is on disk; memory could not be loaded. Fail closed:
			// the operator restarts the process, which reloads from the store.
			s.halt("reload after universe init: "+err.Error(), "")
			return nil, fmt.Errorf("universe written but reload failed: %w", err)
		}
		if err := s.restoreSchedule(s.now()); err != nil {
			s.halt("schedule after universe init: "+err.Error(), "")
			return nil, fmt.Errorf("%w: universe written but schedule could not be persisted: %w", ErrHalted, err)
		}
		s.setMode(ModeServing)
		s.logger.Info("universe initialised", slog.String("op", "admin.universe.init"), slog.Int64("seed", r.Seed),
			slog.Int("stars", r.Stars), slog.Int("planets", r.Planets))
		return r, nil
	})
	s.adminDone(opInitUniverse, start, err)
	return rec, err
}

// Enroll retires the incomplete preview writer. Even EnrollPreview=true cannot
// create a race. The trusted CR-07D workflow is separate and never called here.
func (s *State) Enroll(ctx context.Context, req *enroll.Request) (enroll.Response, error) {
	s.metrics.Enrollments.Inc("disabled")
	return enroll.Response{Success: false, Message: ErrEnrollmentDisabled.Error()}, ErrEnrollmentDisabled
}
