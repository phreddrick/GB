package game

import (
	"encoding/json"
	"errors"
	"reflect"

	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// commandWorkspace is private to one actor operation. It is deliberately typed
// and has no database/Tx callback or arbitrary write interface. The first user
// is one landed factory activation; other handlers are NOT automatically staged.
type commandWorkspace struct {
	s       *State
	ships   map[uint32]workspaceShip
	planets map[[2]uint8]workspacePlanet
	stars   map[uint8]workspaceStar
	effects commandEffects
}
type workspaceShip struct{ actor, before, next *model.Ship }
type workspacePlanet struct{ actor, before, next *model.Planet }
type workspaceStar struct{ actor, before, next *model.Star }

func newCommandWorkspace(s *State) *commandWorkspace {
	return &commandWorkspace{s: s, ships: map[uint32]workspaceShip{}, planets: map[[2]uint8]workspacePlanet{}, stars: map[uint8]workspaceStar{}}
}

// JSON is the model's existing full representation and deeply owns all nine
// optional ShipSpecial pointers. Unencodable/nonfinite inputs fail staging.
func workspaceCopyShip(p *model.Ship) (*model.Ship, error) {
	if p == nil {
		return nil, errors.New("missing ship")
	}
	b, e := json.Marshal(p)
	if e != nil {
		return nil, errors.New("ship cannot be staged")
	}
	var q model.Ship
	if e := json.Unmarshal(b, &q); e != nil {
		return nil, errors.New("ship cannot be staged")
	}
	return &q, nil
}
func (w *commandWorkspace) ship(n uint32) (*model.Ship, error) {
	if e, ok := w.ships[n]; ok {
		return e.next, nil
	}
	actor := w.s.ships[n]
	if actor == nil || actor.Number != n {
		return nil, errors.New("missing or inconsistent ship")
	}
	before, e := workspaceCopyShip(actor)
	if e != nil {
		return nil, e
	}
	next, e := workspaceCopyShip(actor)
	if e != nil {
		return nil, e
	}
	w.ships[n] = workspaceShip{actor, before, next}
	return next, nil
}
func (w *commandWorkspace) planet(st, n uint8) (*model.Planet, error) {
	key := [2]uint8{st, n}
	if e, ok := w.planets[key]; ok {
		return e.next, nil
	}
	actor := w.s.planets[st][n]
	if actor == nil || actor.StarNum != st || actor.PlanetNum != n {
		return nil, errors.New("missing or inconsistent planet")
	}
	before, next := planetEdit(actor), planetEdit(actor)
	w.planets[key] = workspacePlanet{actor, before, next}
	return next, nil
}
func (w *commandWorkspace) star(n uint8) (*model.Star, error) {
	if e, ok := w.stars[n]; ok {
		return e.next, nil
	}
	if int(n) >= len(w.s.stars) || w.s.stars[n] == nil || w.s.stars[n].Number != n {
		return nil, errors.New("missing or inconsistent star")
	}
	actor := w.s.stars[n]
	before, next := *actor, *actor
	w.stars[n] = workspaceStar{actor, &before, &next}
	return &next, nil
}
func (w *commandWorkspace) owns() bool {
	for n, e := range w.ships {
		if w.s.ships[n] != e.actor || !reflect.DeepEqual(e.actor, e.before) {
			return false
		}
	}
	for k, e := range w.planets {
		if w.s.planets[k[0]][k[1]] != e.actor || !reflect.DeepEqual(e.actor, e.before) {
			return false
		}
	}
	for n, e := range w.stars {
		if int(n) >= len(w.s.stars) || w.s.stars[n] != e.actor || !reflect.DeepEqual(e.actor, e.before) {
			return false
		}
	}
	return true
}

// commandCompletion is internal evidence, not a new text/WebSocket contract.
// Queue cancellation before execution still uses the existing ErrNotExecuted.
type commandCompletion string

const (
	commandRejected         commandCompletion = "REJECTED"
	commandValidationFailed commandCompletion = "VALIDATION_FAILED"
	commandNotCommitted     commandCompletion = "NOT_COMMITTED"
	commandCommitted        commandCompletion = "COMMITTED"
	commandCommittedError   commandCompletion = "COMMITTED_ERROR"
	commandUncertain        commandCompletion = "UNCERTAIN"
)

const maxCommandEffectBytes = 4096
const maxCommandEffectLines = 16

// commandEffects holds local response text, not a durable outbox. The renderer
// may ignore Send errors, so overflow is sticky and checked before persistence.
type commandEffects struct {
	lines  []string
	size   int
	failed bool
}

func (e *commandEffects) Send(text string) error {
	if e.failed || len(e.lines) >= maxCommandEffectLines || len(text) > maxCommandEffectBytes-e.size {
		e.failed = true
		return errors.New("command response exceeds staging limit")
	}
	e.lines = append(e.lines, text)
	e.size += len(text)
	return nil
}
func (e *commandEffects) SendJSON(any) error {
	e.failed = true
	return errors.New("JSON effects unsupported")
}
func (e *commandEffects) release(r net.Responder) error {
	if e.failed {
		return errors.New("invalid staged effects")
	}
	for _, text := range e.lines {
		if err := r.Send(text); err != nil {
			return errors.New("command response delivery failed")
		}
	}
	return nil
}
