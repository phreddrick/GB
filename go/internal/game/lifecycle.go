package game

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"
)

const (
	// heartbeatInterval is how often the idle actor refreshes its heartbeat.
	heartbeatInterval = 10 * time.Second
	// HeartbeatStale is the liveness threshold for an actor that is not in a
	// turn (plan 11.5: 60 s).
	HeartbeatStale = 60 * time.Second
)

// Mode returns the current engine mode (safe from any goroutine).
func (s *State) Mode() Mode { return Mode(s.modeVal.Load()) }

func (s *State) setMode(m Mode) {
	prev := Mode(s.modeVal.Swap(int32(m)))
	s.metrics.setMode(m)
	if prev != m && s.logger != nil {
		s.logger.Info("engine mode", slog.String("op", "mode"), slog.String("mode", m.String()), slog.String("previous", prev.String()))
	}
}

// beat records actor liveness.
func (s *State) beat() { s.heartbeatNs.Store(s.now().UnixNano()) }

// Progress advances the turn progress counter (liveness watchdog). Called by
// turn code per entity processed and per record flushed.
func (s *State) Progress() {
	s.progress.Add(1)
	s.progressNs.Store(s.now().UnixNano())
	s.metrics.TurnProgress.Inc()
}

// Health evaluates liveness and readiness at time now.
func (s *State) Health(now time.Time) Health {
	mode := s.Mode()
	h := Health{
		Mode:          mode,
		LastHeartbeat: time.Unix(0, s.heartbeatNs.Load()),
		Progress:      s.progress.Load(),
	}
	if ts := s.turnStartNs.Load(); ts != 0 {
		h.TurnRunning = true
		h.TurnStarted = time.Unix(0, ts)
	}
	if ps := s.progressNs.Load(); ps != 0 {
		h.LastProgress = time.Unix(0, ps)
	}
	stall := time.Duration(s.cfg.TurnStallTimeoutSecs) * time.Second
	switch {
	case mode == ModeStopped:
		h.Alive = false
	case now.Sub(h.LastHeartbeat) <= HeartbeatStale:
		h.Alive = true
	case h.TurnRunning:
		// A long valid turn keeps advancing the progress counter; a stuck one
		// does not. Before the first progress tick, measure from turn start.
		ref := h.TurnStarted
		if h.LastProgress.After(ref) {
			ref = h.LastProgress
		}
		h.Alive = now.Sub(ref) <= stall
	}
	h.Ready = mode.Ready() && !s.draining.Load()
	h.GameReady = (mode == ModeServing || mode == ModeTurnInProgress) && !s.draining.Load()
	if mode == ModeHalted {
		s.statusMu.Lock()
		h.HaltReason = s.haltReason
		s.statusMu.Unlock()
	}
	return h
}

// guard runs fn with panic isolation. A panic is logged with its stack,
// reported to onPanic, and halts the engine; guard returns true when fn
// panicked. The panic value never propagates: the actor goroutine survives
// so that status, health and an orderly process exit remain possible, but
// nothing mutating runs again until a restart (Phase 1 interim policy; Phase
// 3a replaces it with reload-on-failure).
func (s *State) guard(kind string, fn func(), onPanic func(r any)) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			stack := string(debug.Stack())
			s.halt(fmt.Sprintf("panic during %s: %v", kind, r), stack)
			if onPanic != nil {
				func() {
					defer func() { _ = recover() }()
					onPanic(r)
				}()
			}
		}
	}()
	fn()
	return false
}

// halt moves the engine to ModeHalted and records why. It is also used for
// store write failures detected on the actor (fail closed).
func (s *State) halt(reason, stack string) {
	s.statusMu.Lock()
	if s.haltReason == "" {
		s.haltReason = reason
		s.haltStack = stack
	}
	s.statusMu.Unlock()
	s.turnStartNs.Store(0)
	s.setMode(ModeHalted)
	s.logger.Error("engine halted", slog.String("op", "halt"), slog.String("mode", ModeHalted.String()),
		slog.String("reason", reason), slog.String("stack", stack))
	if s.registry != nil {
		s.registry.Shout("\n*** SERVER HALTED after an internal error. Commands are suspended until an operator restarts the server. ***\n")
	}
}

// HaltDetails returns the halt reason and stack (empty when not halted).
func (s *State) HaltDetails() (reason, stack string) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	return s.haltReason, s.haltStack
}

type turnKind string

const (
	turnUpdate  turnKind = "update"
	turnSegment turnKind = "segment"
)

// runTurn executes one update or segment on the actor goroutine with panic
// isolation, mode transitions, metrics and schedule persistence. It returns
// ErrNotServing when the engine is not in ModeServing.
func (s *State) runTurn(kind turnKind, trigger string) error {
	switch s.Mode() {
	case ModeServing:
	case ModeHalted:
		// Halted is also "not serving"; say which.
		return fmt.Errorf("%w (%w)", ErrHalted, ErrNotServing)
	default:
		return ErrNotServing
	}
	start := s.now()
	s.setMode(ModeTurnInProgress)
	s.turnStartNs.Store(start.UnixNano())
	s.Progress()
	s.logger.Info("turn starting", slog.String("op", "turn."+string(kind)), slog.String("trigger", trigger),
		slog.Int("turn", s.sched.numUpdates+1), slog.Int("segment", s.sched.numSegments+1))

	panicked := s.guard("turn "+string(kind), func() {
		if s.testHooks != nil && s.testHooks.beforeTurn != nil {
			s.testHooks.beforeTurn(kind)
		}
		if kind == turnUpdate {
			s.doUpdate()
		} else {
			s.doSegment()
		}
	}, func(any) { s.metrics.TurnPanics.Inc() })
	d := s.now().Sub(start)
	s.turnStartNs.Store(0)
	s.metrics.TurnDuration.Observe(d.Seconds(), string(kind))
	if panicked {
		s.metrics.TurnsTotal.Inc(string(kind), "panic")
		return ErrHalted
	}
	s.metrics.TurnsTotal.Inc(string(kind), "ok")
	if err := s.persistSchedule(s.now()); err != nil {
		// A schedule that cannot be written means the store is failing;
		// the turn's flush most likely failed too. Fail closed.
		s.halt("persist schedule after turn: "+err.Error(), "")
		return ErrHalted
	}
	s.logger.Info("turn complete", slog.String("op", "turn."+string(kind)), slog.String("trigger", trigger),
		slog.Int("turn", s.sched.numUpdates), slog.Int("segment", s.sched.numSegments), slog.Int64("duration_ms", d.Milliseconds()))
	if s.draining.Load() {
		s.setMode(ModeQuiesced)
	} else {
		s.setMode(ModeServing)
	}
	return nil
}

// Stop begins an orderly shutdown and waits for the actor to finish or for
// ctx to end. Sequence on the actor: stop accepting player commands (the
// draining flag refuses them immediately, even before the actor reaches the
// stop request), let a running turn complete, flush in-memory state exactly
// as a segment boundary would (unless HALTED, when nothing is flushed),
// persist the schedule, exit the loop. The caller closes the store only
// after Stop returns nil or ErrHaltedNotFlushed; on ErrShutdownTimeout the
// actor may still be writing.
func (s *State) Stop(ctx context.Context) error {
	s.draining.Store(true)
	s.stopOnce.Do(func() { close(s.stopCh) })
	if !s.loopStarted {
		return nil
	}
	select {
	case <-s.stopped:
		return s.shutdownErr
	case <-ctx.Done():
		return ErrShutdownTimeout
	}
}

// Stopped is closed when the actor goroutine has exited.
func (s *State) Stopped() <-chan struct{} { return s.stopped }

// orderlyShutdown runs on the actor goroutine when it observes stopCh.
func (s *State) orderlyShutdown() error {
	mode := s.Mode()
	s.logger.Info("shutdown: actor draining", slog.String("op", "shutdown"), slog.String("mode", mode.String()))
	// Answer anything still queued so no caller hangs on a done channel.
	s.drainQueues()
	switch mode {
	case ModeHalted:
		s.logger.Warn("shutdown: engine halted; in-memory state is NOT flushed", slog.String("op", "shutdown"))
		s.setMode(ModeStopped)
		return ErrHaltedNotFlushed
	case ModeUninitialized, ModeStarting:
		s.setMode(ModeStopped)
		return nil
	}
	err := s.flushForShutdown()
	if err == nil {
		err = s.persistSchedule(s.now())
	}
	if err != nil {
		s.logger.Error("shutdown: flush failed", slog.String("op", "shutdown"), slog.String("err", err.Error()))
	} else {
		s.logger.Info("shutdown: flushed", slog.String("op", "shutdown"))
	}
	s.setMode(ModeStopped)
	return err
}

// drainQueues answers queued player commands and admin requests with a
// shutdown refusal (they arrived after Stop began).
func (s *State) drainQueues() {
	for {
		select {
		case cmd := <-s.cmdCh:
			if cmd.state.CompareAndSwap(reqQueued, reqStarted) {
				_ = cmd.sess.Send(refusalMessage(ModeQuiesced))
			}
			close(cmd.done)
		case req := <-s.adminCh:
			if req.state.CompareAndSwap(reqQueued, reqStarted) {
				req.reply <- adminResult{err: ErrEngineStopped}
			}
		default:
			return
		}
	}
}

// flushForShutdown persists in-memory state with the same rules a turn
// boundary uses (turn.go): ships are reloaded from the store first (the
// store is authoritative for ships between turns, CR-11), then stars,
// planets and races are written from memory (memory is authoritative for
// them). It therefore preserves exactly what the next turn would have
// preserved, and carries the same CR-17 caveat for the six planet records
// that handlers read and write through the store directly.
func (s *State) flushForShutdown() error {
	ships, err := s.store.AllShips()
	if err != nil {
		return fmt.Errorf("reload ships: %w", err)
	}
	s.ships = ships
	return s.flushAll()
}

// Request lifecycle (GB1-01). Every typed request and every player command
// carries an atomic state that starts as reqQueued. Exactly one transition
// out of reqQueued ever succeeds:
//
//   - the actor moves it to reqStarted immediately before running it, or
//   - the waiting caller moves it to reqCanceled when its context ends.
//
// The compare-and-swap is the deterministic tie-break between cancellation
// and the start of execution: a request the caller canceled is never run
// (the actor skips it), and a request the actor started is never reported
// as canceled (the caller learns it is still running and is not rolled
// back). There is no mid-operation cancellation: a started turn, flush or
// store write always runs to completion.
const (
	reqQueued int32 = iota
	reqStarted
	reqCanceled
)

// submit runs fn on the actor goroutine and waits for its result, bounded
// by ctx. The result travels only through the request's reply channel
// (buffered, exactly one send, made by the actor), so the caller never reads
// memory the actor writes (GB1-01: no captured result variables).
//
// Errors:
//   - ctx already ended, or ended before the actor accepted the request into
//     its queue: ErrEngineBusy + ErrNotExecuted ("not accepted");
//   - ctx ended while the request was queued: ErrEngineBusy + ErrNotExecuted
//     ("canceled while queued"); the actor will skip it;
//   - ctx ended after the actor started it: ErrEngineBusy + ErrStillRunning;
//   - the actor stopped first: ErrEngineStopped (not executed).
func (s *State) submit(ctx context.Context, op adminOp, fn func() (any, error)) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %s not accepted: %w (%w)", ErrEngineBusy, op, ErrNotExecuted, err)
	}
	select {
	case <-s.stopped:
		return nil, ErrEngineStopped
	default:
	}
	req := &adminRequest{op: op, fn: fn, reply: make(chan adminResult, 1)}
	select {
	case s.adminCh <- req:
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %s not accepted (queue full): %w (%w)", ErrEngineBusy, op, ErrNotExecuted, ctx.Err())
	case <-s.stopped:
		return nil, ErrEngineStopped
	}
	select {
	case res := <-req.reply:
		return res.val, res.err
	case <-ctx.Done():
		if req.state.CompareAndSwap(reqQueued, reqCanceled) {
			s.metrics.Canceled.Inc(string(op))
			return nil, fmt.Errorf("%w: %s canceled while queued: %w (%w)", ErrEngineBusy, op, ErrNotExecuted, ctx.Err())
		}
		// The actor started it first. If it has also finished, report the
		// real outcome; otherwise say plainly that it is still running.
		select {
		case res := <-req.reply:
			return res.val, res.err
		default:
		}
		return nil, fmt.Errorf("%w: %s %w (%w)", ErrEngineBusy, op, ErrStillRunning, ctx.Err())
	case <-s.stopped:
		if req.state.CompareAndSwap(reqQueued, reqCanceled) {
			return nil, ErrEngineStopped
		}
		// Started (or answered by drainQueues) before the actor exited: the
		// reply was sent before s.stopped was closed.
		res := <-req.reply
		return res.val, res.err
	}
}

// call is submit with a typed result.
func call[T any](s *State, ctx context.Context, op adminOp, fn func() (T, error)) (T, error) {
	v, err := s.submit(ctx, op, func() (any, error) {
		r, err := fn()
		return r, err
	})
	r, _ := v.(T)
	return r, err
}

// request is submit for operations without a result value.
func (s *State) request(ctx context.Context, op adminOp, fn func() error) error {
	_, err := s.submit(ctx, op, func() (any, error) { return nil, fn() })
	return err
}

// testHooks are set only by tests in this package.
type testHooks struct {
	beforeTurn func(kind turnKind)
	panicVerb  string // a command verb that panics inside dispatch
}
