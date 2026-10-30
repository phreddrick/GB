package game

import "github.com/phreddrick/GB/go/internal/engine"

// The engine's mode, health and status types and sentinel errors live in
// internal/engine so that the transport and admin layers can use them
// without importing this package. They are aliased here for readability.
type (
	Mode   = engine.Mode
	Health = engine.Health
	Status = engine.Status
)

// Mode constants (see internal/engine for the semantics).
const (
	ModeStarting       = engine.ModeStarting
	ModeUninitialized  = engine.ModeUninitialized
	ModeServing        = engine.ModeServing
	ModeTurnInProgress = engine.ModeTurnInProgress
	ModeQuiesced       = engine.ModeQuiesced
	ModeHalted         = engine.ModeHalted
	ModeStopped        = engine.ModeStopped
)

// AllModes lists every mode.
var AllModes = engine.AllModes

// Sentinel errors (see internal/engine).
var (
	ErrHalted             = engine.ErrHalted
	ErrNotServing         = engine.ErrNotServing
	ErrEngineBusy         = engine.ErrEngineBusy
	ErrNotExecuted        = engine.ErrNotExecuted
	ErrStillRunning       = engine.ErrStillRunning
	ErrEngineStopped      = engine.ErrEngineStopped
	ErrShutdownTimeout    = engine.ErrShutdownTimeout
	ErrHaltedNotFlushed   = engine.ErrHaltedNotFlushed
	ErrEnrollmentDisabled = engine.ErrEnrollmentDisabled
)

// PersistenceNote is repeated in every status.
const PersistenceNote = engine.PersistenceNote
