// Package engine holds the types shared between the game actor and the
// transport and admin layers: the operating mode, the health and status
// views, and the sentinel errors. It has no behaviour of its own; it exists
// so that internal/net and internal/admin can describe the engine without
// importing internal/game (which imports internal/net).
package engine

import (
	"errors"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
)

// Mode is the engine's operating state (migration plan 8.7, Phase 1 subset).
type Mode int32

// Engine modes. Transitions are made only by the actor goroutine (and by
// Stop, which sets the draining flag).
const (
	// ModeStarting: the store is being loaded; nothing is served.
	ModeStarting Mode = iota
	// ModeUninitialized: the store holds no universe. Admin operations
	// (status, universe init) work; player commands are refused.
	ModeUninitialized
	// ModeServing: commands and scheduled turns run normally.
	ModeServing
	// ModeTurnInProgress: a turn is running on the actor goroutine.
	ModeTurnInProgress
	// ModeQuiesced: an operator paused the engine (or shutdown has begun):
	// mutating player commands and scheduled turns are refused; status works;
	// Resume returns to ModeServing.
	ModeQuiesced
	// ModeHalted: a panic may have left in-memory state inconsistent. All
	// mutating operations are refused, nothing is flushed, and only a process
	// restart (which reloads from the store) leaves this mode. Phase 3a
	// replaces this with reload-on-failure.
	ModeHalted
	// ModeStopped: the actor has exited after an orderly shutdown.
	ModeStopped
)

var modeNames = [...]string{
	ModeStarting:       "STARTING",
	ModeUninitialized:  "UNINITIALIZED",
	ModeServing:        "SERVING",
	ModeTurnInProgress: "TURN_IN_PROGRESS",
	ModeQuiesced:       "QUIESCED",
	ModeHalted:         "HALTED",
	ModeStopped:        "STOPPED",
}

// AllModes lists every mode (for the mode gauge and tests).
var AllModes = []Mode{ModeStarting, ModeUninitialized, ModeServing, ModeTurnInProgress, ModeQuiesced, ModeHalted, ModeStopped}

func (m Mode) String() string {
	if int(m) < len(modeNames) {
		return modeNames[m]
	}
	return "UNKNOWN"
}

// Ready reports whether the engine should advertise readiness (plan 11.5):
// SERVING and TURN_IN_PROGRESS serve players; UNINITIALIZED is ready for
// administrative initialisation only (Health.GameReady is false there).
func (m Mode) Ready() bool {
	switch m {
	case ModeServing, ModeTurnInProgress, ModeUninitialized:
		return true
	}
	return false
}

// AcceptsCommands reports whether ordinary player commands are dispatched.
func (m Mode) AcceptsCommands() bool { return m == ModeServing }

// Health is the engine's view for /livez and /readyz. It is assembled from
// atomics and a snapshot, never through the actor queue.
type Health struct {
	Mode       Mode
	Alive      bool   // actor heartbeated recently, or a turn is making progress
	Ready      bool   // Mode.Ready() and not draining
	GameReady  bool   // players can be served (SERVING or TURN_IN_PROGRESS)
	HaltReason string // non-empty in ModeHalted
	// Diagnostics.
	LastHeartbeat time.Time
	TurnRunning   bool
	TurnStarted   time.Time
	LastProgress  time.Time
	Progress      uint64
}

// Status is the engine status returned to operators. It contains no
// secrets and no per-player data.
type Status struct {
	Mode           string             `json:"mode"`
	Draining       bool               `json:"draining"`
	HaltReason     string             `json:"halt_reason,omitempty"`
	NextUpdate     time.Time          `json:"next_update"`
	NextSegment    time.Time          `json:"next_segment"`
	NumUpdates     int                `json:"num_updates"`
	NumSegments    int                `json:"num_segments"`
	UpdateMinutes  int                `json:"update_minutes"`
	MovesPerUpdate int                `json:"moves_per_update"`
	Stars          int                `json:"stars"`
	Races          int                `json:"races"`
	Ships          int                `json:"ships"`
	Universe       *db.UniverseRecord `json:"universe,omitempty"`
	TurnRunning    bool               `json:"turn_running"`
	TurnStarted    *time.Time         `json:"turn_started,omitempty"`
	QueueDepth     int                `json:"queue_depth"`
	Persistence    string             `json:"persistence"`
}

// PersistenceNote is repeated in every status so operators are never
// misled about crash consistency.
const PersistenceNote = "per-record flush (not crash-atomic; CR-18, Phase 3a): orderly shutdown preserves state, a kill during a flush may not"

// Sentinel errors shared by the engine, the transport and the admin layer.
var (
	// ErrHalted: the engine is HALTED; mutating operations are refused and a
	// restart is required.
	ErrHalted = errors.New("engine halted; restart required")
	// ErrNotServing: the operation needs ModeServing (or ModeQuiesced for
	// Resume) and the engine is in another mode.
	ErrNotServing = errors.New("engine is not serving")
	// ErrEngineBusy: the actor did not accept or finish the request within
	// the caller's deadline (a long turn is running or the queue is full).
	// Every timeout error wraps it, together with exactly one of
	// ErrNotExecuted or ErrStillRunning, which say what happened to the work.
	ErrEngineBusy = errors.New("engine busy")
	// ErrNotExecuted: the request was refused at admission or canceled while
	// it was still queued. The actor will never run it, so nothing it would
	// have changed was changed (GB1-01).
	ErrNotExecuted = errors.New("request was not executed and will not run")
	// ErrStillRunning: the actor had already started the request when the
	// caller stopped waiting. The operation runs to completion; the timeout
	// does not cancel it and nothing is rolled back. Its outcome is visible
	// in status, metrics and logs (GB1-01).
	ErrStillRunning = errors.New("request had already started; it continues to completion and is not rolled back")
	// ErrEngineStopped: the actor has shut down.
	ErrEngineStopped = errors.New("engine stopped")
	// ErrShutdownTimeout: the actor did not finish within the shutdown
	// budget (a turn was still running). The store may hold a partially
	// flushed turn (CR-18).
	ErrShutdownTimeout = errors.New("shutdown timed out while the actor was busy; the store may hold a partially flushed turn (CR-18)")
	// ErrHaltedNotFlushed: shutdown completed while HALTED; in-memory state
	// was discarded, not flushed. The store is NOT guaranteed to hold a
	// complete turn: a panic or failed write during a turn can leave it
	// partially applied (CR-18, Phase 3a) (GB1-09).
	ErrHaltedNotFlushed = errors.New("engine was halted: in-memory state was not flushed; the store may already contain partially applied turn changes (CR-18)")
	// ErrEnrollmentDisabled is returned when the enrollment preview is off
	// (the default: web enrollment produces unplaced, unplayable races until
	// Phase 3c, CR-07; Decision 9).
	ErrEnrollmentDisabled = errors.New("enrollment is disabled on this server: web enrollment creates races without a home planet (CR-07) and stays off until Phase 3c")
)
