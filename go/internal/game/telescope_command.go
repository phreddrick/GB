package game

import (
	"errors"
	"fmt"
	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
	"math"
	"reflect"
	"strconv"
	"strings"
)

func isTelescope(sh *model.Ship) bool {
	return sh != nil && (sh.Type == model.OtypeStele || sh.Type == model.OtypeGtele)
}

// Intercept the complete invocation before the legacy header or selector loop.
func (s *State) handleTelescopeOrder(p, g int, args []string, sess net.Responder) bool {
	if len(args) < 2 || !matchCmd(strings.ToLower(args[1]), "aim") {
		return false
	}
	if p < 1 || p > model.MaxPlayers || g < 0 || g > model.MaxGovernors {
		_ = sess.Send("Invalid command authority.\n")
		return true
	}
	spec := args[0]
	if strings.HasPrefix(spec, "#") || (len(spec) > 0 && spec[0] >= '0' && spec[0] <= '9') {
		id, e := strconv.ParseUint(strings.TrimPrefix(spec, "#"), 10, 32)
		if e != nil || id == 0 {
			_ = sess.Send("Invalid ship number.\n")
			return true
		}
		sh := s.ships[uint32(id)]
		if !isTelescope(sh) {
			return false
		}
		if len(args) != 3 {
			_ = sess.Send("Usage: order #<telescope> aim <place>\n")
			return true
		}
		s.runTelescopeAim(p, g, uint32(id), args[2], sess)
		return true
	}
	// Resolve the original scope chain read-only and bounded by cycle detection.
	dir := s.dirs[p-1][g]
	var head uint32
	bad := false
	switch dir.Level {
	case model.LevelUniv:
		if s.sdata == nil {
			bad = true
		} else {
			head = s.sdata.Ships
		}
	case model.LevelStar:
		if int(dir.StarNum) >= len(s.stars) || s.stars[dir.StarNum] == nil {
			bad = true
		} else {
			head = s.stars[dir.StarNum].Ships
		}
	case model.LevelPlan:
		if s.planets[dir.StarNum][dir.PlanetNum] == nil {
			bad = true
		} else {
			head = s.planets[dir.StarNum][dir.PlanetNum].Ships
		}
	case model.LevelShip:
		if s.ships[dir.ShipNo] == nil {
			bad = true
		} else {
			head = s.ships[dir.ShipNo].Ships
		}
	default:
		bad = true
	}
	seen := map[uint32]bool{}
	for !bad && head != 0 {
		sh := s.ships[head]
		if sh == nil || sh.Number != head || seen[head] {
			bad = true
			break
		}
		seen[head] = true
		if sh.Alive && int(sh.Owner) == p && isTelescope(sh) && matchShipSpec(sh, spec) {
			_ = sess.Send("Telescope aiming requires one explicit ship number; no orders changed.\n")
			return true
		}
		head = sh.NextShip
	}
	if bad {
		s.halt("aim selection has inconsistent orbital records; operator reconciliation required", "")
		_ = sess.Send("Aim selection refused; the engine has halted.\n")
		return true
	}
	return false
}

// Capture complete source/target chains, including nested carriers. These are
// immutable identity witnesses, not additional writable command families.
func (w *commandWorkspace) telescopeLocation(id uint32, parents map[uint32]bool) error {
	if parents[id] {
		return errors.New("cyclic carrier hierarchy")
	}
	parents[id] = true
	sh, e := w.ship(id)
	if e != nil {
		return e
	}
	var head uint32
	switch sh.WhatOrbits {
	case model.LevelUniv:
		d, e := w.starData()
		if e != nil {
			return e
		}
		head = d.Ships
	case model.LevelStar:
		st, e := w.star(sh.StOrbits)
		if e != nil {
			return e
		}
		head = st.Ships
	case model.LevelPlan:
		st, e := w.star(sh.StOrbits)
		if e != nil {
			return e
		}
		pl, e := w.planet(sh.StOrbits, sh.PNumOrbits)
		if e != nil {
			return e
		}
		if pl.PlanetNum >= st.NumPlanets {
			return errors.New("invalid source planet")
		}
		head = pl.Ships
	case model.LevelShip:
		parent, e := w.ship(sh.DestShipNo)
		if e != nil {
			return e
		}
		if e := w.telescopeLocation(parent.Number, parents); e != nil {
			return e
		}
		head = parent.Ships
	default:
		return errors.New("invalid source orbit")
	}
	found := false
	seen := map[uint32]bool{}
	for head != 0 {
		if seen[head] {
			return errors.New("cyclic source chain")
		}
		seen[head] = true
		x, e := w.ship(head)
		if e != nil {
			return e
		}
		if !x.Alive || x.WhatOrbits != sh.WhatOrbits {
			return errors.New("inconsistent source chain")
		}
		switch sh.WhatOrbits {
		case model.LevelStar:
			if x.StOrbits != sh.StOrbits {
				return errors.New("inconsistent star orbit")
			}
		case model.LevelPlan:
			if x.StOrbits != sh.StOrbits || x.PNumOrbits != sh.PNumOrbits {
				return errors.New("inconsistent planet orbit")
			}
		case model.LevelShip:
			if x.DestShipNo != sh.DestShipNo {
				return errors.New("inconsistent carrier orbit")
			}
		}
		if head == id {
			found = true
		}
		head = x.NextShip
	}
	if !found {
		return errors.New("ship missing from orbital chain")
	}
	return nil
}
func (s *State) telescopeStage(p, g int, id uint32, target string) (*commandWorkspace, db.TelescopeAim, string, error) {
	var in db.TelescopeAim
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
		return nil, in, "You are not authorized to aim this telescope.\n", nil
	}
	sh, e := w.ship(id)
	if e != nil {
		return nil, in, "", e
	}
	if !sh.Alive || !isTelescope(sh) || int(sh.Owner) != p || !authorized(g, sh) {
		return nil, in, "You are not authorized to aim this telescope.\n", nil
	}
	if !sh.Active {
		return nil, in, "This telescope is irradiated; it cannot be given orders.\n", nil
	}
	if sh.Popn == 0 && shipMaxCrew(sh) > 0 {
		return nil, in, "This telescope has no crew and is not robotic.\n", nil
	}
	if strings.HasPrefix(target, "#") {
		n, e := strconv.ParseUint(target[1:], 10, 16)
		if e != nil || n == 0 || n > math.MaxUint16 {
			return nil, in, "Invalid or unrepresentable telescope target.\n", nil
		}
	}
	place := s.getplace(p, g, target, true)
	if place.Err || place.ShipNo > math.MaxUint16 {
		return nil, in, "Error in destination.\n", nil
	}
	if e := w.telescopeLocation(id, map[uint32]bool{}); e != nil {
		return nil, in, "", e
	}
	var star *model.Star
	var planet *model.Planet
	if place.Level == model.LevelStar || place.Level == model.LevelPlan {
		star, e = w.star(place.StarNum)
		if e != nil {
			return nil, in, "", e
		}
		if place.Level == model.LevelPlan {
			planet, e = w.planet(place.StarNum, place.PlanetNum)
			if e != nil {
				return nil, in, "", e
			}
		}
	}
	if place.Level == model.LevelShip {
		target, e := w.ship(place.ShipNo)
		if e != nil {
			return nil, in, "", e
		}
		if target.Alive {
			if e := w.telescopeLocation(target.Number, map[uint32]bool{}); e != nil {
				return nil, in, "", e
			}
		}
	}
	change, e := commandrules.PlanTelescope(sh, place, star, planet, s.cfg.FuelManeuver, s.cfg.MassFuel)
	if errors.Is(e, commandrules.ErrFuel) {
		return nil, in, fmt.Sprintf("Not enough maneuvering fuel (%.2f).\n", s.cfg.FuelManeuver), nil
	}
	if e != nil {
		return nil, in, "", e
	}
	aim := change.Aim
	sh.Special.AimedAt = &aim
	sh.Fuel = change.Fuel
	sh.Mass = change.Mass
	sh.Notified = false
	in = db.TelescopeAim{Player: p, Governor: g, Ship: db.CommandShip{Expected: w.ships[id].before, Proposed: sh}, Target: place, ManeuverFuel: s.cfg.FuelManeuver, MassFuel: s.cfg.MassFuel, Context: db.TelescopeContext{Race: race}}
	if star != nil {
		if change.Explore {
			star.SetExplored(p)
		}
		in.Star = &db.CommandStar{Expected: w.stars[star.Number].before, Proposed: star}
	}
	if planet != nil {
		if change.Explore {
			planet.Info[p-1].Explored = true
		}
		in.Planet = &db.CommandPlanet{Expected: w.planets[[2]uint8{planet.StarNum, planet.PlanetNum}].before, Proposed: planet}
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
	s.dispOrdersHeader(&w.effects)
	switch place.Level {
	case model.LevelStar, model.LevelPlan:
		kind := "Star"
		if place.Level == model.LevelPlan {
			kind = "Planet"
		}
		_ = w.effects.Send(fmt.Sprintf("%s %s ", kind, s.dispPlace(p, g, place)))
		if change.Explore {
			_ = w.effects.Send(fmt.Sprintf("Surveyed, distance %g.\n", change.Distance))
		} else {
			_ = w.effects.Send(fmt.Sprintf("Too far to see (%g, max %g).\n", change.Distance, change.Range))
		}
	case model.LevelUniv:
		_ = w.effects.Send("There is nothing out here to aim at.")
	case model.LevelShip:
		_ = w.effects.Send("You can't see anything of use there.\n")
	}
	_ = w.effects.Send(fmt.Sprintf("Aimed at %s\n", s.dispPlace(p, g, place)))
	s.dispOrders(p, g, sh, &w.effects)
	if w.effects.failed {
		return nil, in, "Telescope response exceeds the supported staging limit; no changes made.\n", nil
	}
	return w, in, "", nil
}
func (w *commandWorkspace) telescopeReadOnly(in db.TelescopeAim) bool {
	for n, e := range w.ships {
		if n != in.Ship.Expected.Number && !reflect.DeepEqual(e.before, e.next) {
			return false
		}
	}
	for n, e := range w.stars {
		if (in.Star == nil || n != in.Star.Expected.Number) && !reflect.DeepEqual(e.before, e.next) {
			return false
		}
	}
	for k, e := range w.planets {
		if (in.Planet == nil || k != [2]uint8{in.Planet.Expected.StarNum, in.Planet.Expected.PlanetNum}) && !reflect.DeepEqual(e.before, e.next) {
			return false
		}
	}
	return true
}

func (s *State) runTelescopeAim(p, g int, id uint32, target string, sess net.Responder) (result commandCompletion) {
	outcome := db.CommandNotCommitted
	result = commandNotCommitted
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("telescope aiming %s; command panic; operator reconciliation required; no shutdown flush", outcome), "")
			func() {
				defer func() { _ = recover() }()
				_ = sess.Send(fmt.Sprintf("Telescope aiming %s; operator reconciliation required; the engine has halted.\n", outcome))
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
	w, input, validation, e := s.telescopeStage(p, g, id, target)
	if e != nil {
		s.halt("telescope aiming has inconsistent actor records; no write attempted; operator reconciliation required", "")
		_ = sess.Send("Telescope aiming refused: inconsistent world; the engine has halted.\n")
		return commandNotCommitted
	}
	if validation != "" {
		_ = sess.Send(validation)
		return commandValidationFailed
	}
	if !w.owns() || !w.telescopeReadOnly(input) {
		s.halt("telescope actor identity changed before persistence; operator reconciliation required", "")
		_ = sess.Send("Telescope aiming refused; the engine has halted.\n")
		return commandNotCommitted
	}
	outcome, e = s.store.CommitTelescopeAim(input)
	if outcome == db.CommandCommitted {
		if s.testHooks != nil && s.testHooks.beforeCommandInstall != nil {
			s.testHooks.beforeCommandInstall()
		}
		if !w.owns() {
			s.halt("telescope aiming COMMITTED; installation identities changed; operator reconciliation required; no shutdown flush", "")
			_ = sess.Send("Telescope aiming records committed; reconciliation required; the engine has halted.\n")
			return commandCommittedError
		}
		// All checks finish before these checked non-callback assignments. Do not
		// install read-only witnesses from the workspace.
		*w.ships[id].actor = *input.Ship.Proposed
		if input.Star != nil {
			*w.stars[input.Star.Expected.Number].actor = *input.Star.Proposed
		}
		if input.Planet != nil {
			key := [2]uint8{input.Planet.Expected.StarNum, input.Planet.Expected.PlanetNum}
			*w.planets[key].actor = *input.Planet.Proposed
		}
		if e == nil {
			if e = w.effects.release(sess); e == nil {
				return commandCommitted
			}
		}
	}
	// Confirmed commits remain installed. Unknown writes never install and
	// retain the shared Store guard; neither case is a rollback claim.
	s.halt(fmt.Sprintf("telescope aiming %s; persistence or response failed; operator reconciliation required; no shutdown flush", outcome), "")
	_ = sess.Send(fmt.Sprintf("Telescope aiming %s; operator reconciliation required; the engine has halted.\n", outcome))
	switch outcome {
	case db.CommandCommitted:
		return commandCommittedError
	case db.CommandUncertain:
		return commandUncertain
	default:
		return commandNotCommitted
	}
}
