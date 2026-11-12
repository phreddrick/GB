package game

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// habitatFactory reports a factory that claims to be docked aboard another
// ship (order.c's `whatorbits == LEVEL_SHIP` branch). Whether that ship is a
// live habitat with valid docking links is decided by the staged operation.
// C permits the authorized factory to use another player's habitat.
func habitatFactory(sh *model.Ship) bool {
	return sh != nil && sh.Type == model.OtypeFactory && sh.WhatOrbits == model.LevelShip && sh.Docked
}

func (s *State) sizeFactors() commandrules.SizeFactors {
	return commandrules.SizeFactors{Guns: s.cfg.SizeGuns, Crew: s.cfg.SizeCrew, Resource: s.cfg.SizeResource, Fuel: s.cfg.SizeFuel, Destruct: s.cfg.SizeDestruct}
}

// habitatStage builds the complete private proposal for one habitat factory
// activation: factory On/Notified/Size and carrier Resource/Hanger, with the
// race, universe root, the carrier's docked chain, the carrier's own location
// chain and its root star/planet captured as read-only witnesses. Validation
// failures discard the workspace; no actor object is mutated.
func (s *State) habitatStage(p, g int, id uint32) (*commandWorkspace, db.HabitatActivation, string, error) {
	var in db.HabitatActivation
	if p < 1 || p > model.MaxPlayers || g < 0 || g > model.MaxGovernors {
		return nil, in, "Invalid command authority.\n", nil
	}
	w := newCommandWorkspace(s)
	w.scope = &workspaceScope{p, g, s.dirs[p-1][g]}
	race, e := w.race(p)
	if e != nil {
		return nil, in, "", e
	}
	if !race.Governor[g].Active || race.Dissolved {
		return nil, in, "You are not authorized to activate this factory.\n", nil
	}
	f, e := w.ship(id)
	if e != nil {
		return nil, in, "", e
	}
	if !f.Alive || f.Type != model.OtypeFactory || int(f.Owner) != p || !authorized(g, f) {
		return nil, in, "You are not authorized to activate this factory.\n", nil
	}
	if !habitatFactory(f) || f.WhatDest != model.LevelShip {
		return nil, in, "", errors.New("inconsistent carrier identity")
	}
	if !f.Active {
		return nil, in, fmt.Sprintf("%s is irradiated (%d); it cannot be given orders.\n", shipDisplayName(f), f.Rad), nil
	}
	if f.Popn == 0 && shipMaxCrew(f) > 0 {
		return nil, in, fmt.Sprintf("%s has no crew and is not a robotic ship.\n", shipDisplayName(f)), nil
	}
	if f.On {
		return nil, in, "This ship is already activated.\n", nil
	}
	carrier, e := w.ship(f.DestShipNo)
	if e != nil {
		return nil, in, "", e
	}
	if carrier.Number == f.Number || !carrier.Alive {
		return nil, in, "", errors.New("inconsistent carrier identity")
	}
	if carrier.Type != model.StypeHabitat {
		return nil, in, "The factory is currently being transported.\n", nil
	}
	if f.StOrbits != carrier.StOrbits || f.PNumOrbits != carrier.PNumOrbits {
		return nil, in, "", errors.New("inconsistent carrier location")
	}
	// Complete docked chain of the carrier and the carrier's own location,
	// recursively through any parent carriers to the root star/planet/universe.
	if e := w.telescopeLocation(id, map[uint32]bool{}); e != nil {
		return nil, in, "", e
	}
	change, e := commandrules.PlanHabitatActivation(f, carrier, s.sizeFactors())
	switch {
	case errors.Is(e, commandrules.ErrHabitatResources):
		return nil, in, fmt.Sprintf("You don't have %d resources on Habitat #%d to activate this factory.\n", change.Cost, carrier.Number), nil
	case errors.Is(e, commandrules.ErrHabitatHangar):
		return nil, in, fmt.Sprintf("Not enough hanger space free on Habitat #%d. Need %d more.\n", carrier.Number, change.HangerNeeded), nil
	case errors.Is(e, commandrules.ErrHabitatHangarState):
		return nil, in, fmt.Sprintf("Habitat #%d hanger accounting is inconsistent; activation refused.\n", carrier.Number), nil
	case e != nil:
		return nil, in, "", e
	}
	f.On = true
	f.Notified = false
	f.Size = change.Size
	carrier.Resource = change.Resource
	carrier.Hanger = change.Hanger
	in = db.HabitatActivation{Player: p, Governor: g, Factory: db.CommandShip{Expected: w.ships[id].before, Proposed: f}, Carrier: db.CommandShip{Expected: w.ships[carrier.Number].before, Proposed: carrier}, Context: db.CommandContext{Race: race}, Size: s.sizeFactors()}
	for _, r := range w.stars {
		in.Context.Stars = append(in.Context.Stars, r.before)
	}
	for _, r := range w.planets {
		in.Context.Planets = append(in.Context.Planets, r.before)
	}
	for _, r := range w.ships {
		in.Context.Ships = append(in.Context.Ships, r.before)
	}
	if w.data != nil {
		in.Context.StarData = w.data.before
	}
	s.dispOrdersHeader(&w.effects)
	_ = w.effects.Send(fmt.Sprintf("Factory activated at a cost of %d resources.\n", change.Cost))
	s.dispOrders(p, g, f, &w.effects)
	if w.effects.failed {
		return nil, in, "Factory response exceeds the supported staging limit; no changes made.\n", nil
	}
	return w, in, "", nil
}

// habitatReadOnly verifies that nothing but the factory and carrier proposals
// changed in the workspace (owns covers the actor preimages separately).
func (w *commandWorkspace) habitatReadOnly(in db.HabitatActivation) bool {
	for n, e := range w.ships {
		if n != in.Factory.Expected.Number && n != in.Carrier.Expected.Number && !reflect.DeepEqual(e.before, e.next) {
			return false
		}
	}
	for _, e := range w.stars {
		if !reflect.DeepEqual(e.before, e.next) {
			return false
		}
	}
	for _, e := range w.planets {
		if !reflect.DeepEqual(e.before, e.next) {
			return false
		}
	}
	return true
}

func (s *State) runHabitatActivation(p, g int, id uint32, sess net.Responder) (result commandCompletion) {
	outcome := db.CommandNotCommitted
	result = commandNotCommitted
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("habitat factory activation %s; command panic; operator reconciliation required; no shutdown flush", outcome), "")
			func() {
				defer func() { _ = recover() }()
				_ = sess.Send(fmt.Sprintf("Factory activation %s; operator reconciliation required; the engine has halted.\n", outcome))
			}()
			switch outcome {
			case db.CommandCommitted:
				result = commandCommittedError
			case db.CommandUncertain:
				result = commandUncertain
			default:
				result = commandNotCommitted
			}
		}
	}()
	if s.Mode() != ModeServing {
		_ = sess.Send(refusalMessage(s.Mode()))
		return commandRejected
	}
	w, input, validation, e := s.habitatStage(p, g, id)
	if e != nil {
		s.halt("habitat factory activation has inconsistent actor records; no write attempted; operator reconciliation required", "")
		_ = sess.Send("Factory activation refused: inconsistent world; the engine has halted.\n")
		return commandNotCommitted
	}
	if validation != "" {
		_ = sess.Send(validation)
		return commandValidationFailed
	}
	if !w.owns() || !w.habitatReadOnly(input) {
		s.halt("habitat factory actor identity changed before persistence; operator reconciliation required", "")
		_ = sess.Send("Factory activation refused; the engine has halted.\n")
		return commandNotCommitted
	}
	outcome, e = s.store.CommitHabitatActivation(input)
	if outcome == db.CommandCommitted {
		if s.testHooks != nil && s.testHooks.beforeCommandInstall != nil {
			s.testHooks.beforeCommandInstall()
		}
		if !w.owns() {
			s.halt("habitat factory activation COMMITTED; installation identities changed; operator reconciliation required; no shutdown flush", "")
			_ = sess.Send("Factory activation records committed; reconciliation required; the engine has halted.\n")
			return commandCommittedError
		}
		// All checks finish before these two non-callback assignments. Do not
		// install read-only witnesses from the workspace.
		*w.ships[id].actor = *input.Factory.Proposed
		*w.ships[input.Carrier.Expected.Number].actor = *input.Carrier.Proposed
		if s.testHooks != nil && s.testHooks.afterCommandInstall != nil {
			s.testHooks.afterCommandInstall()
		}
		if e == nil {
			if e = w.effects.release(sess); e == nil {
				return commandCommitted
			}
		}
	}
	// Confirmed commits remain installed. Unknown writes never install and
	// retain the shared Store guard; neither case is a rollback claim.
	s.halt(fmt.Sprintf("habitat factory activation %s; persistence or response failed; operator reconciliation required; no shutdown flush", outcome), "")
	_ = sess.Send(fmt.Sprintf("Factory activation %s; operator reconciliation required; the engine has halted.\n", outcome))
	switch outcome {
	case db.CommandCommitted:
		return commandCommittedError
	case db.CommandUncertain:
		return commandUncertain
	default:
		return commandNotCommitted
	}
}
