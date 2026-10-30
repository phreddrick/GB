package game

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/enroll"
	"github.com/phreddrick/GB/go/internal/model"
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
	sched    schedule
	universe *db.UniverseRecord
	stars    int
	races    int
	ships    int
	lastTurn time.Time
}

// Status returns the current status without going through the actor queue,
// so it works during long turns and while halted.
func (s *State) Status() Status {
	s.statusMu.Lock()
	d := s.status
	halt := s.haltReason
	s.statusMu.Unlock()
	st := Status{
		Mode:           s.Mode().String(),
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
// (after a running turn). Quiescing an already quiesced engine is a no-op.
func (s *State) Quiesce(ctx context.Context) error {
	start := s.now()
	err := s.request(ctx, opQuiesce, func() error {
		switch s.Mode() {
		case ModeServing, ModeQuiesced:
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
// Ordering (GB1-03): the rescheduled times are computed on a copy and
// written to the store first. Only after that write succeeds does the actor
// adopt the new schedule, publish it and switch to SERVING (which is what
// makes /readyz report game_ready). If the write fails the store is failing:
// the engine is HALTED (fail closed, like a failed turn flush), readiness
// stays false, mutating work is refused, and shutdown will not flush memory.
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
			if err := s.writeSchedule(next, now); err != nil {
				s.halt("resume: "+err.Error(), "")
				return fmt.Errorf("resume failed and the engine is now HALTED (restart required): %w", err)
			}
			s.sched = next
			s.publishSchedule()
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

// EnrollPreviewNotice is attached to every successful preview enrollment.
const EnrollPreviewNotice = "PREVIEW ONLY: the race was stored without a home planet, sectors or ships and is not playable (CR-07, Phase 3c)"

// Enroll validates and stores a new race through the actor and the shared
// store handle (CR-03: no second database open). With the preview disabled
// nothing is read or written. The new race is also added to the in-memory
// race list so that it can log in without a restart.
func (s *State) Enroll(ctx context.Context, req *enroll.Request) (enroll.Response, error) {
	if !s.cfg.EnrollPreview {
		s.metrics.Enrollments.Inc("disabled")
		return enroll.Response{Success: false, Message: ErrEnrollmentDisabled.Error()}, ErrEnrollmentDisabled
	}
	start := s.now()
	resp, err := call(s, ctx, opEnroll, func() (enroll.Response, error) {
		if s.Mode() != ModeServing {
			return enroll.Response{Success: false, Message: "server is not serving (" + s.Mode().String() + ")"}, ErrNotServing
		}
		playerNum := 1
		for playerNum <= model.MaxPlayers && s.races[playerNum-1] != nil {
			playerNum++
		}
		if playerNum > model.MaxPlayers {
			return enroll.Response{Success: false, Message: "No available player slots"}, nil
		}
		for _, r := range s.races {
			if r != nil && strings.EqualFold(r.Name, req.RaceName) {
				return enroll.Response{Success: false, Message: "Validation failed", Errors: []string{"a race with that name already exists"}}, nil
			}
		}
		race, errs, err := enroll.Enroll(req, playerNum)
		if err != nil {
			return enroll.Response{Success: false, Message: "Enrollment error", Errors: []string{err.Error()}}, nil
		}
		if len(errs) > 0 {
			return enroll.Response{Success: false, Message: "Validation failed", Errors: errs}, nil
		}
		if err := s.store.PutRace(race); err != nil {
			return enroll.Response{Success: false, Message: "Could not save race", Errors: []string{err.Error()}}, err
		}
		s.races[playerNum-1] = race
		s.publishWorld(s.status.universe)
		return enroll.Response{
			Success:   true,
			PlayerNum: playerNum,
			Message:   fmt.Sprintf("Race %q enrolled as player %d. %s", race.Name, playerNum, EnrollPreviewNotice),
		}, nil
	})
	if err != nil && resp.Message == "" {
		// Not answered by the actor (busy, canceled, stopped): report what is
		// known about the request without implying it ran or was undone.
		resp = enroll.Response{Success: false, Message: err.Error()}
	}
	label := "error"
	switch {
	case err == nil && resp.Success:
		label = "ok"
	case err == nil:
		label = "rejected"
	}
	s.metrics.Enrollments.Inc(label)
	s.adminDone(opEnroll, start, err)
	return resp, err
}
