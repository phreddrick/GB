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
	// Resume returns to ModeServing. The process is healthy and Ready for
	// the orchestrator (CR-43: a quiesced Pod must stay Ready so a
	// StatefulSet rolling update can replace it) while the game is not
	// (Health.GameReady is false). An operator quiesce is recorded durably
	// (db.MaintenanceRecord) so that a restart starts QUIESCED again.
	ModeQuiesced
	// ModeHalted: a panic, a failed commit or a failed startup validation
	// stopped mutating work. All mutating operations are refused, nothing
	// is flushed, and only a process restart leaves this mode. After a
	// failed turn the in-memory world has been reloaded from the last commit
	// (the halt reason says so); after a command panic it may hold the
	// handler's partial effects, which are never flushed.
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

// Ready reports whether the engine should advertise process readiness to
// the orchestrator (plan 11.5, CR-43): SERVING and TURN_IN_PROGRESS serve
// players; UNINITIALIZED is ready for administrative initialisation only;
// QUIESCED is a healthy, initialised server deliberately paused by an
// operator (Health.GameReady is false in both). STARTING (store not yet
// loaded or validated), HALTED (including CORRUPT_STORE) and STOPPED are
// not ready: the process cannot operate safely.
func (m Mode) Ready() bool {
	switch m {
	case ModeServing, ModeTurnInProgress, ModeUninitialized, ModeQuiesced:
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
	Ready      bool   // Mode.Ready() and not draining (process readiness for the orchestrator)
	GameReady  bool   // players can be served (SERVING or TURN_IN_PROGRESS)
	HaltReason string // non-empty in ModeHalted
	// Maintenance is true while an operator quiesce is in effect (ModeQuiesced
	// entered through Quiesce or restored from the store); MaintenanceDurable
	// says whether the store holds the maintenance record, i.e. whether a
	// restart would start QUIESCED again.
	Maintenance        bool
	MaintenanceDurable bool
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
	LastCommitSeq  uint64             `json:"commit_seq,omitempty"`
	// GameReady is true only while players are served (SERVING or
	// TURN_IN_PROGRESS, not draining); it is false while QUIESCED even
	// though /readyz then answers 200 (CR-43).
	GameReady bool `json:"game_ready"`
	// Maintenance is present while an operator quiesce is in effect.
	Maintenance *MaintenanceStatus `json:"maintenance,omitempty"`
	Persistence string             `json:"persistence"`
}

// MaintenanceStatus describes an operator quiesce (CR-43, ADR-0010).
type MaintenanceStatus struct {
	// Durable is true when the store holds the maintenance record, so a
	// restart starts QUIESCED. False means the engine is quiesced in memory
	// only (the record could not be written; Error says why) and a restart
	// would start SERVING; re-running `gbctl quiesce` retries the write.
	Durable bool       `json:"durable"`
	SetAt   *time.Time `json:"set_at,omitempty"`
	Reason  string     `json:"reason,omitempty"`
	Error   string     `json:"error,omitempty"`
}

// PersistenceNote is repeated in every status so operators are never
// misled about crash consistency.
const PersistenceNote = "turns, segments and the shutdown flush commit the whole world, the schedule, the turn's news, stored telegrams and allocated numbers in one bbolt transaction with a validated commit record (crash-atomic; CR-18 implemented, independent acceptance pending); a turn whose transaction is confirmed not committed wrote nothing and memory is reloaded from the last commit, a confirmed committed turn stands even if notification delivery then fails, and an outcome the engine cannot confirm is reported as uncertain; live notifications are best-effort after the commit; player commands between turns still write per record and are not atomic (CR-11, CR-17, Phase 3a)"

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
	// ErrMaintenanceNotDurable: Quiesce paused the engine in memory but the
	// maintenance record write reported an error; scheduled turns and
	// mutating commands are refused as usual, but the quiesce is not
	// confirmed durable (the record may be absent, or visible but with its
	// durability on disk unconfirmed; the status error text says which). The
	// operator re-runs `gbctl quiesce` (which retries the write) or treats
	// the store as failing. Never reported as success (CR-43).
	ErrMaintenanceNotDurable = errors.New("engine is QUIESCED in memory, but the maintenance record write did not succeed: a restart is not guaranteed to start QUIESCED (gbctl status says what the store shows); re-run quiesce to retry")
	// ErrShutdownTimeout: the actor did not finish within the shutdown
	// budget; a turn may still be running and may still commit after the
	// process is told to exit. The commit is atomic, so the store will hold
	// either the previous commit or the completed one, never a mixture;
	// which one is shown by the durable commit record (commit_seq in
	// gbctl status) at the next start.
	ErrShutdownTimeout = errors.New("shutdown timed out while the actor was busy; a turn may still be running and may still commit; the store will hold either the previous or the completed commit (see commit_seq at the next start)")
	// ErrHaltedNotFlushed: shutdown completed while HALTED; in-memory state
	// was discarded, not flushed. The store holds a complete commit: the
	// one before a turn whose transaction is known not to have committed,
	// the new one when a turn committed and only its notification delivery
	// failed, and whichever the halt reason named when the outcome was
	// uncertain. In-memory changes made by player commands since that
	// commit, other than what those commands wrote per record themselves,
	// are lost (GB1-09).
	ErrHaltedNotFlushed = errors.New("engine was halted: in-memory state was not flushed; the store holds the last complete commit")
	// ErrShutdownCommitFailed: the shutdown commit failed and the store
	// still holds the previous commit (nothing of the shutdown flush was
	// written).
	ErrShutdownCommitFailed = errors.New("shutdown commit failed; the store holds the previous commit")
	// ErrShutdownCommitUncertain: the shutdown commit's outcome could not be
	// established (the commit record could not be read back, or it changed
	// unexpectedly); the operator checks commit_seq at the next start.
	ErrShutdownCommitUncertain = errors.New("shutdown commit outcome uncertain; check commit_seq at the next start")
	// ErrEnrollmentDisabled is returned when the enrollment preview is off
	// (the default: web enrollment produces unplaced, unplayable races until
	// Phase 3c, CR-07; Decision 9).
	ErrEnrollmentDisabled = errors.New("enrollment is disabled on this server: web enrollment creates races without a home planet (CR-07) and stays off until Phase 3c")
)
