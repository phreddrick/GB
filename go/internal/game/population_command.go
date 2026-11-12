package game

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// All legacy tokens starting with c/m are intercepted: malformed aliases,
// unsupported locations and mixed selectors cannot fall back to a partial writer.
// Other commodities retain their legacy implementation and CR-17 limitations.
func (s *State) handlePopulation(p, g int, args []string, sess net.Responder, unload bool) bool {
	if len(args) < 2 || len(args[1]) == 0 || !strings.ContainsRune("cm", rune(args[1][0])) {
		return false
	}
	if len(args) != 2 && len(args) != 3 {
		_ = sess.Send("Usage: load/unload #<ship> c|m [amount]; no changes made.\n")
		return true
	}
	if len(args[1]) != 1 {
		_ = sess.Send("Unsupported population commodity; no changes made.\n")
		return true
	}
	id, e := strconv.ParseUint(strings.TrimPrefix(args[0], "#"), 10, 32)
	if e != nil || id == 0 {
		_ = sess.Send("Population transfer requires one explicit ship number; no changes made.\n")
		return true
	}
	var amount int64
	if len(args) == 3 {
		amount, e = strconv.ParseInt(args[2], 10, 32)
		if e != nil {
			_ = sess.Send("Invalid population amount; no changes made.\n")
			return true
		}
	}
	s.runPopulation(p, g, uint32(id), commandrules.PopulationRequest{Commodity: args[1][0], Amount: amount, Unload: unload}, sess)
	return true
}
func (s *State) populationStage(p, g int, id uint32, request commandrules.PopulationRequest) (*commandWorkspace, db.PopulationTransfer, string, error) {
	var in db.PopulationTransfer
	if p < 1 || p > model.MaxPlayers || g < 0 || g > model.MaxGovernors {
		return nil, in, "Invalid population authority.\n", nil
	}
	w := newCommandWorkspace(s)
	w.scope = &workspaceScope{p, g, s.dirs[p-1][g]}
	race, e := w.race(p)
	if e != nil {
		return nil, in, "", e
	}
	if !race.Governor[g].Active || race.Dissolved {
		return nil, in, "Unauthorized population operation.\n", nil
	}
	if s.ships[id] == nil {
		return nil, in, "No matching ship.\n", nil
	}
	f, e := w.ship(id)
	if e != nil {
		return nil, in, "", e
	}
	if int(f.Owner) != p || !authorized(g, f) || !f.Alive {
		return nil, in, "Unauthorized population operation.\n", nil
	}
	if !f.Active || !f.Docked {
		return nil, in, "Ship is inactive or not landed/docked; no changes made.\n", nil
	}
	if int(f.Type) >= len(model.Shipdata) {
		return nil, in, "", errors.New("invalid selected ship type")
	}
	if f.Type == model.OtypeTransDev && f.On && (f.Special.Transport == nil || f.Special.Transport.Target != 0) {
		return nil, in, "Population transfer with an active targeted transporter is deferred (issue #42); no changes made.\n", nil
	}
	var other *model.Ship
	var planet *model.Planet
	if f.WhatDest == model.LevelPlan {
		if f.StOrbits != w.scope.before.StarNum || f.PNumOrbits != w.scope.before.PlanetNum {
			return nil, in, "Change scope to the planet this ship is landed on.\n", nil
		}
		planet, e = w.planet(f.StOrbits, f.PNumOrbits)
		if e != nil {
			return nil, in, "", e
		}
		if f.WhatOrbits != model.LevelPlan || f.DestStar != f.StOrbits || f.DestPNum != f.PNumOrbits || f.LandX >= planet.MaxX || f.LandY >= planet.MaxY {
			return nil, in, "", errors.New("inconsistent landing")
		}
	} else {
		if f.WhatDest != model.LevelShip || f.DestShipNo == 0 {
			return nil, in, "", errors.New("inconsistent dock destination")
		}
		other, e = w.ship(f.DestShipNo)
		if e != nil {
			return nil, in, "", e
		}
		if !other.Alive || other.Number == f.Number || int(other.Type) >= len(model.Shipdata) {
			return nil, in, "", errors.New("invalid dock partner")
		}
		if other.WhatOrbits == model.LevelShip && commandrules.CargoOverloaded(other) {
			return nil, in, "Docked partner is overloaded; no changes made.\n", nil
		}
		if f.WhatOrbits != model.LevelShip && (!other.Docked || other.WhatDest != model.LevelShip || other.DestShipNo != f.Number || other.WhatOrbits != f.WhatOrbits) {
			return nil, in, "", errors.New("nonreciprocal docking")
		}
	}
	if e = w.telescopeLocation(id, map[uint32]bool{}); e != nil {
		return nil, in, "", e
	}
	if other != nil {
		if e = w.telescopeLocation(other.Number, map[uint32]bool{}); e != nil {
			return nil, in, "", e
		}
	}
	change, e := commandrules.PlanPopulation(f, other, planet, race, request)
	if errors.Is(e, commandrules.ErrPopulationBounds) {
		return nil, in, fmt.Sprintf("you can only transfer between %d and %d.\n", change.Lower, change.Upper), nil
	}
	if errors.Is(e, commandrules.ErrPopulationDeferred) {
		return nil, in, "Population transfer requires deferred assault/transporter effects (issues #41/#51/#42); no changes made.\n", nil
	}
	if e != nil {
		return nil, in, "Invalid or unsupported population state/amount; no changes made.\n", nil
	}
	f.Mass = change.SelectedMass
	if request.Commodity == 'c' {
		f.Popn = change.Selected
	} else {
		f.Troops = change.Selected
	}
	in = db.PopulationTransfer{Player: p, Governor: g, Selected: db.CommandShip{Expected: w.ships[id].before, Proposed: f}, Context: db.CommandContext{Race: race}, Scope: w.scope.before, Request: request}
	if other != nil {
		other.Mass = change.OtherMass
		if request.Commodity == 'c' {
			other.Popn = change.Other
		} else {
			other.Troops = change.Other
		}
		in.Partner = &db.CommandShip{Expected: w.ships[other.Number].before, Proposed: other}
		_ = w.effects.Send(fmt.Sprintf("%s docked with %s\n", shipDisplayName(f), shipDisplayName(other)))
		if int(other.Owner) != p {
			_ = w.effects.Send(fmt.Sprintf("Player %d owns that ship.\n", other.Owner))
		}
	} else {
		planet.Info[p-1] = change.Info
		planet.Popn = change.PlanetPopn
		planet.Troops = change.PlanetTroops
		*planet.SectorAt(int(f.LandX), int(f.LandY)) = change.Sector
		in.Planet = &db.CommandPlanet{Expected: w.planets[[2]uint8{f.StOrbits, f.PNumOrbits}].before, Proposed: planet}
		_ = w.effects.Send(fmt.Sprintf("%s at %d,%d\n", shipDisplayName(f), f.LandX, f.LandY))
	}
	if change.Colonized {
		word := "COLONIZED"
		if request.Commodity == 'm' {
			word = "OCCUPIED"
		}
		_ = w.effects.Send(fmt.Sprintf("sector %d,%d %s.\n", f.LandX, f.LandY, word))
	}
	if change.Evacuated {
		_ = w.effects.Send(fmt.Sprintf("sector %d,%d evacuated.\n", f.LandX, f.LandY))
	}
	unit := "crew"
	quantity := f.Popn
	if request.Commodity == 'm' {
		unit = "troop"
		quantity = f.Troops
	}
	_ = w.effects.Send(fmt.Sprintf("%s complement of %s is now %d.\n", unit, shipDisplayName(f), quantity))
	if w.effects.failed {
		return nil, in, "Population response exceeds staging limit; no changes made.\n", nil
	}
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
	return w, in, "", nil
}
func (w *commandWorkspace) populationReadOnly(in db.PopulationTransfer) bool {
	for n, r := range w.ships {
		if n != in.Selected.Expected.Number && (in.Partner == nil || n != in.Partner.Expected.Number) && !reflect.DeepEqual(r.before, r.next) {
			return false
		}
	}
	for n, r := range w.planets {
		if (in.Planet == nil || n != [2]uint8{in.Planet.Expected.StarNum, in.Planet.Expected.PlanetNum}) && !reflect.DeepEqual(r.before, r.next) {
			return false
		}
	}
	for _, r := range w.stars {
		if !reflect.DeepEqual(r.before, r.next) {
			return false
		}
	}
	return true
}

func (s *State) runPopulation(p, g int, id uint32, request commandrules.PopulationRequest, sess net.Responder) (result commandCompletion) {
	outcome := db.CommandNotCommitted
	result = commandNotCommitted
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("population transfer %s; command panic; operator reconciliation required; no shutdown flush", outcome), "")
			func() {
				defer func() { _ = recover() }()
				_ = sess.Send(fmt.Sprintf("Population transfer %s; operator reconciliation required; the engine has halted.\n", outcome))
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
	w, input, validation, e := s.populationStage(p, g, id, request)
	if e != nil {
		s.halt("population transfer has inconsistent actor records; no write attempted; operator reconciliation required", "")
		_ = sess.Send("Population transfer refused: inconsistent world; the engine has halted.\n")
		return commandNotCommitted
	}
	if validation != "" {
		_ = sess.Send(validation)
		return commandValidationFailed
	}
	if !w.owns() || !w.populationReadOnly(input) {
		s.halt("population actor identity changed before persistence; operator reconciliation required", "")
		_ = sess.Send("Population transfer refused; the engine has halted.\n")
		return commandNotCommitted
	}
	outcome, e = s.store.CommitPopulationTransfer(input)
	if outcome == db.CommandCommitted {
		if s.testHooks != nil && s.testHooks.beforeCommandInstall != nil {
			s.testHooks.beforeCommandInstall()
		}
		if !w.owns() {
			s.halt("population transfer COMMITTED; installation identities changed; operator reconciliation required; no shutdown flush", "")
			_ = sess.Send("Population transfer records committed; reconciliation required; the engine has halted.\n")
			return commandCommittedError
		}
		// All checks finish before these two non-callback assignments. Do not
		// install read-only witnesses from the workspace.
		*w.ships[id].actor = *input.Selected.Proposed
		if input.Partner != nil {
			*w.ships[input.Partner.Expected.Number].actor = *input.Partner.Proposed
		} else {
			p := input.Planet.Expected
			*w.planets[[2]uint8{p.StarNum, p.PlanetNum}].actor = *input.Planet.Proposed
		}
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
	s.halt(fmt.Sprintf("population transfer %s; persistence or response failed; operator reconciliation required; no shutdown flush", outcome), "")
	_ = sess.Send(fmt.Sprintf("Population transfer %s; operator reconciliation required; the engine has halted.\n", outcome))
	switch outcome {
	case db.CommandCommitted:
		return commandCommittedError
	case db.CommandUncertain:
		return commandUncertain
	default:
		return commandNotCommitted
	}
}
