package game

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// handleFactoryOrder runs before cmdOrder emits output or iterates ships.
// Only an explicit single ship ID can enter a migrated operation: a landed
// factory (planet stockpile) or a factory docked aboard a habitat (carrier
// resources and hanger). Numeric IDs are range-checked before the legacy
// selector can truncate them to uint32.
func (s *State) handleFactoryOrder(p, g int, args []string, sess net.Responder) bool {
	if len(args) < 2 || !matchCmd(strings.ToLower(args[1]), "on") {
		return false
	}
	if p < 1 || p > model.MaxPlayers || g < 0 || g > model.MaxGovernors {
		_ = sess.Send("Invalid command authority.\n")
		return true
	}
	spec := args[0]
	numeric := strings.HasPrefix(spec, "#") || (len(spec) > 0 && spec[0] >= '0' && spec[0] <= '9')
	if numeric {
		n, e := strconv.ParseUint(strings.TrimPrefix(spec, "#"), 10, 32)
		if e != nil || n == 0 {
			_ = sess.Send("Invalid ship number.\n")
			return true
		}
		sh := s.ships[uint32(n)]
		if sh == nil || !sh.Alive || int(sh.Owner) != p {
			return false
		}
		if sh.Type != model.OtypeFactory || (!landed(sh) && !habitatFactory(sh)) {
			return false
		}
		if len(args) != 2 {
			_ = sess.Send("Usage: order #<factory> on\n")
			return true
		}
		if landed(sh) {
			s.runFactoryActivation(p, g, uint32(n), sess)
		} else {
			s.runHabitatActivation(p, g, uint32(n), sess)
		}
		return true
	}
	// The on selector is inspected without mutation. A mixed/ambiguous request
	// cannot run a nonfactory prefix then reach a staged factory later in the loop.
	staged, err := s.onSelectionHasStagedFactory(p, g, spec)
	if err != nil {
		s.halt("factory selection has inconsistent orbital records; operator reconciliation required", "")
		_ = sess.Send("Factory selection refused; the engine has halted for operator review.\n")
		return true
	}
	if staged {
		_ = sess.Send("Factory activation requires one explicit ship number; no orders changed.\n")
		return true
	}
	return false
}

// onSelectionHasStagedFactory reports whether a wildcard/type selector would
// reach a landed or habitat-docked factory of this player in the current scope.
func (s *State) onSelectionHasStagedFactory(p, g int, spec string) (bool, error) {
	dir := s.dirs[p-1][g]
	var next uint32
	switch dir.Level {
	case model.LevelUniv:
		if s.sdata == nil {
			return false, errors.New("missing scope")
		}
		next = s.sdata.Ships
	case model.LevelStar:
		if int(dir.StarNum) >= len(s.stars) || s.stars[dir.StarNum] == nil {
			return false, errors.New("missing scope")
		}
		next = s.stars[dir.StarNum].Ships
	case model.LevelPlan:
		pl := s.planets[dir.StarNum][dir.PlanetNum]
		if pl == nil {
			return false, errors.New("missing scope")
		}
		next = pl.Ships
	case model.LevelShip:
		ship := s.ships[dir.ShipNo]
		if ship == nil {
			return false, errors.New("missing scope")
		}
		next = ship.Ships
	}
	seen := map[uint32]bool{}
	for next != 0 {
		sh := s.ships[next]
		if sh == nil || sh.Number != next || seen[next] {
			return false, errors.New("invalid scope chain")
		}
		seen[next] = true
		if sh.Alive && int(sh.Owner) == p && sh.Type == model.OtypeFactory && (landed(sh) || habitatFactory(sh)) && matchShipSpec(sh, spec) {
			return true, nil
		}
		next = sh.NextShip
	}
	return false, nil
}

// factoryStage builds private changes before checking the final budget/effects.
// Validation failures discard the workspace; no actor object is ever mutated.
func (s *State) factoryStage(p, g int, id uint32) (*commandWorkspace, db.FactoryActivation, string, error) {
	w := newCommandWorkspace(s)
	var input db.FactoryActivation
	f, e := w.ship(id)
	if e != nil {
		return nil, input, "", e
	}
	if p < 1 || p > model.MaxPlayers || g < 0 || g > model.MaxGovernors || int(f.Owner) != p || !authorized(g, f) {
		return nil, input, "You are not authorized to activate this factory.\n", nil
	}
	if f.Type != model.OtypeFactory || !f.Alive || !f.Active {
		return nil, input, "This factory cannot be activated.\n", nil
	}
	if f.Popn == 0 && shipMaxCrew(f) > 0 {
		return nil, input, "This factory has no crew.\n", nil
	}
	if f.On {
		return nil, input, "This ship is already activated.\n", nil
	}
	if !landed(f) {
		return nil, input, "Only landed factories are supported by this staged operation.\n", nil
	}
	pl, e := w.planet(f.StOrbits, f.PNumOrbits)
	if e != nil {
		return nil, input, "", e
	}
	star, e := w.star(f.StOrbits)
	if e != nil {
		return nil, input, "", e
	}
	if f.WhatDest != model.LevelPlan || f.DestStar != f.StOrbits || f.DestPNum != f.PNumOrbits || f.LandX >= pl.MaxX || f.LandY >= pl.MaxY || pl.MaxX == 0 || pl.MaxY == 0 || len(pl.Sectors) != int(pl.MaxX)*int(pl.MaxY) || pl.PlanetNum >= star.NumPlanets {
		return nil, input, "", errors.New("inconsistent landing identity")
	}
	chain := []*model.Ship{}
	next := pl.Ships
	seen := map[uint32]bool{}
	for next != 0 {
		if seen[next] {
			return nil, input, "", errors.New("cyclic planet orbit")
		}
		seen[next] = true
		sh, e := w.ship(next)
		if e != nil {
			return nil, input, "", e
		}
		if !sh.Alive || sh.WhatOrbits != model.LevelPlan || sh.StOrbits != pl.StarNum || sh.PNumOrbits != pl.PlanetNum {
			return nil, input, "", errors.New("inconsistent planet orbit")
		}
		chain = append(chain, w.ships[next].before)
		next = sh.NextShip
	}
	if !seen[id] {
		return nil, input, "", errors.New("factory absent from planet orbit")
	}
	f.On = true
	f.Notified = false // proposed only; late budget failure discards it
	cost := uint32(f.BuildCost) * 2
	if cost > uint32(pl.Info[p-1].Resource) {
		return nil, input, fmt.Sprintf("You don't have %d resources on the planet to activate this factory.\n", cost), nil
	}
	pl.Info[p-1].Resource -= uint16(cost) //nolint:gosec // bounded by uint16 stockpile above
	input = db.FactoryActivation{Player: p, Governor: g, Factory: db.CommandShip{Expected: w.ships[id].before, Proposed: f}, Planet: db.CommandPlanet{Expected: w.planets[[2]uint8{pl.StarNum, pl.PlanetNum}].before, Proposed: pl}, Star: w.stars[star.Number].before, Orbit: chain}
	s.dispOrdersHeader(&w.effects)
	_ = w.effects.Send(fmt.Sprintf("Factory activated at a cost of %d resources.\n", cost))
	s.dispOrders(p, g, f, &w.effects)
	if w.effects.failed {
		return nil, input, "Factory response exceeds the supported staging limit; no changes made.\n", nil
	}
	return w, input, "", nil
}

func (s *State) runFactoryActivation(p, g int, id uint32, sess net.Responder) (result commandCompletion) {
	outcome := db.CommandNotCommitted
	result = commandNotCommitted
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("factory activation %s; command panic; operator reconciliation required; no shutdown flush", outcome), "")
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
	w, input, validation, e := s.factoryStage(p, g, id)
	if e != nil {
		s.halt("factory activation has inconsistent actor records; no write attempted; operator reconciliation required", "")
		_ = sess.Send("Factory activation refused: inconsistent world; the engine has halted.\n")
		return commandNotCommitted
	}
	if validation != "" {
		_ = sess.Send(validation)
		return commandValidationFailed
	}
	if !w.owns() || !w.factoryReadOnly(id) {
		s.halt("factory actor identity changed before persistence; operator reconciliation required", "")
		_ = sess.Send("Factory activation refused; the engine has halted.\n")
		return commandNotCommitted
	}
	outcome, e = s.store.CommitFactoryActivation(input)
	if outcome == db.CommandCommitted {
		if s.testHooks != nil && s.testHooks.beforeCommandInstall != nil {
			s.testHooks.beforeCommandInstall()
		}
		if !w.owns() {
			s.halt("factory activation COMMITTED; installation identities changed; operator reconciliation required; no shutdown flush", "")
			_ = sess.Send("Factory activation records committed; reconciliation required; the engine has halted.\n")
			return commandCommittedError
		}
		// All checks finish before these two non-callback assignments. Do not
		// install read-only witnesses from the workspace.
		*w.ships[id].actor = *input.Factory.Proposed
		key := [2]uint8{input.Planet.Expected.StarNum, input.Planet.Expected.PlanetNum}
		*w.planets[key].actor = *input.Planet.Proposed
		if e == nil {
			if e = w.effects.release(sess); e == nil {
				return commandCommitted
			}
		}
	}
	// Confirmed commits remain installed. Unknown writes never install and
	// retain the shared Store guard; neither case is a rollback claim.
	s.halt(fmt.Sprintf("factory activation %s; persistence or response failed; operator reconciliation required; no shutdown flush", outcome), "")
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

// This compile-time assertion also prevents a future responder change from
// bypassing bounded effect staging.
var _ net.Responder = (*commandEffects)(nil)

// workspaceReadOnly verifies the operation did not silently stage unsupported
// changes to read-only witnesses. It is separate from owns (actor preimages).
func (w *commandWorkspace) factoryReadOnly(id uint32) bool {
	if len(w.planets) != 1 || len(w.stars) != 1 {
		return false
	}
	for n, e := range w.ships {
		if n != id && !reflect.DeepEqual(e.before, e.next) {
			return false
		}
	}
	for _, e := range w.stars {
		if !reflect.DeepEqual(e.before, e.next) {
			return false
		}
	}
	return true
}
