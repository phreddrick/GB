package game

import (
	"errors"
	"fmt"
	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// The existing population parser owns grammar/selector admission. This classifier
// only selects foreign planet sectors; every recognized invocation stays here,
// even if its sign, ship authority or context later fails validation.
func (s *State) handleShipGroundAssault(p, g int, id uint32, r commandrules.PopulationRequest, sess net.Responder) bool {
	sh := s.ships[id]
	if sh == nil || sh.WhatDest != model.LevelPlan {
		return false
	}
	pl := s.planets[sh.StOrbits][sh.PNumOrbits]
	if pl == nil || sh.LandX >= pl.MaxX || sh.LandY >= pl.MaxY || len(pl.Sectors) != int(pl.MaxX)*int(pl.MaxY) {
		return false
	}
	owner := pl.SectorAt(int(sh.LandX), int(sh.LandY)).Owner
	if owner == 0 || int(owner) == p {
		return false
	}
	s.runShipGroundAssault(p, g, id, r, sess)
	return true
}
func (s *State) shipAssaultStage(p, g int, id uint32, r commandrules.PopulationRequest) (*commandWorkspace, db.ShipGroundAssault, string, error) {
	var in db.ShipGroundAssault
	if p < 1 || p >= model.MaxPlayers || g < 0 || g > model.MaxGovernors {
		return nil, in, "Invalid ship assault authority.\n", nil
	}
	w := newCommandWorkspace(s)
	w.scope = &workspaceScope{p, g, s.dirs[p-1][g]}
	if s.ships[id] == nil {
		return nil, in, "No matching assault ship.\n", nil
	}
	sh, e := w.ship(id)
	if e != nil {
		return nil, in, "", e
	}
	a, e := w.race(p)
	if e != nil {
		return nil, in, "", e
	}
	if !a.Governor[g].Active || a.Dissolved || int(sh.Owner) != p || !authorized(g, sh) || !sh.Alive || !sh.Active || !sh.Docked {
		return nil, in, "Unauthorized or inactive assault ship; no changes made.\n", nil
	}
	planet, e := w.planet(sh.StOrbits, sh.PNumOrbits)
	if e != nil {
		return nil, in, "", e
	}
	star, e := w.star(sh.StOrbits)
	if e != nil {
		return nil, in, "", e
	}
	if sh.LandX >= planet.MaxX || sh.LandY >= planet.MaxY {
		return nil, in, "Invalid assault landing coordinates; no changes made.\n", nil
	}
	d, e := w.race(int(planet.SectorAt(int(sh.LandX), int(sh.LandY)).Owner))
	if e != nil {
		return nil, in, "", e
	}
	if e = w.telescopeLocation(id, map[uint32]bool{}); e != nil {
		return nil, in, "", e
	}
	input := commandrules.ShipAssaultInput{Player: p, Governor: g, Scope: w.scope.before, Request: r, Ship: w.ships[id].before, Planet: w.planets[[2]uint8{planet.StarNum, planet.PlanetNum}].before, Star: w.stars[star.Number].before, Attacker: a, Defender: d}
	seen := map[uint32]bool{}
	for n := planet.Ships; n != 0; {
		if seen[n] {
			return nil, in, "", errors.New("cyclic planet ship chain")
		}
		seen[n] = true
		sh, e := w.ship(n)
		if e != nil {
			return nil, in, "", e
		}
		input.Ships = append(input.Ships, w.ships[n].before)
		if int(sh.Owner) != p && int(sh.Owner) != d.PlayerNum {
			_, already := w.races[int(sh.Owner)]
			race, e := w.race(int(sh.Owner))
			if e != nil {
				return nil, in, "", e
			}
			if !already {
				input.Races = append(input.Races, race)
			}
		}
		n = sh.NextShip
	}
	if e := commandrules.ValidateShipAssault(input); e != nil {
		if errors.Is(e, commandrules.ErrAssaultMechanical) {
			return nil, in, "Mechanical ground assaults are deferred; no changes made.\n", nil
		}
		return nil, in, "Invalid/unauthorized assault, inconsistent accounting, or unsupported hostile unload; no changes made.\n", nil
	}
	in.Input = input
	in.At = s.now()
	draw := s.assaultRandom
	if draw == nil {
		draw = secureAssaultDraw
	}
	// A bounded human assault uses at most seven round/integer draws. A missing
	// transcript requests the next draw; all previous outcomes are replayed, never
	// rerolled. Storage rejection never re-enters this preparation loop.
	for {
		change, e := commandrules.PlanShipAssault(input, in.Draws)
		if e == nil {
			in.Proposed = change
			break
		}
		var need *commandrules.AssaultNeedDraw
		if !errors.As(e, &need) {
			return nil, in, "Assault arithmetic/outcome is invalid; no changes made.\n", nil
		}
		if len(in.Draws) >= 7 {
			return nil, in, "", errors.New("unbounded assault transcript")
		}
		value, e := draw(need.Draw)
		if e != nil {
			return nil, in, "Unable to prepare assault randomness; no changes made.\n", nil
		}
		in.Draws = append(in.Draws, value)
	}
	c := in.Proposed
	text := "The invasion was repulsed; try again.\n"
	if c.Outcome == "VICTORY" {
		text = "VICTORY! The sector is yours!\n"
	} else if c.Outcome == "MUTUAL DESTRUCTION" {
		text = "Mutual destruction; neither side wins.\n"
	} else if c.Outcome != "REPULSE" {
		text = "Unopposed occupation; the sector is yours.\n"
	}
	if c.AttackStrength > 0 {
		_ = w.effects.Send(fmt.Sprintf("Attack: %.2f   Defense: %.2f.\n", c.AttackStrength, c.DefenseStrength))
	}
	_ = w.effects.Send(text)
	_ = w.effects.Send(fmt.Sprintf("Casualties: You: %d; Them: %d civ/%d mil. Absorbed: %d. AP: %d.\n", c.AttackerLoss, c.CivilianLoss, c.MilitaryLoss, c.Absorbed, c.APCost))
	if w.effects.failed {
		return nil, in, "Assault response exceeds staging limit; no changes made.\n", nil
	}
	if _, _, e := db.ShipAssaultMessages(in); e != nil {
		return nil, in, "Assault reporting exceeds staging limit; no changes made.\n", nil
	}
	return w, in, "", nil
}

func (s *State) runShipGroundAssault(p, g int, id uint32, r commandrules.PopulationRequest, sess net.Responder) (result commandCompletion) {
	outcome := db.CommandNotCommitted
	result = commandNotCommitted
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("ship ground assault %s; command panic; operator reconciliation required; no shutdown flush", outcome), "")
			func() {
				defer func() { _ = recover() }()
				_ = sess.Send(fmt.Sprintf("Ship ground assault %s; reconciliation required; engine halted.\n", outcome))
			}()
			switch outcome {
			case db.CommandCommitted:
				result = commandCommittedError
			case db.CommandUncertain:
				result = commandUncertain
			}
		}
	}()
	if s.Mode() != ModeServing {
		_ = sess.Send(refusalMessage(s.Mode()))
		return commandRejected
	}
	w, in, validation, e := s.shipAssaultStage(p, g, id, r)
	if e != nil {
		s.halt("ship ground assault has inconsistent actor records; no write attempted; reconciliation required", "")
		_ = sess.Send("Ship ground assault refused: inconsistent world; engine halted.\n")
		return commandNotCommitted
	}
	if validation != "" {
		_ = sess.Send(validation)
		return commandValidationFailed
	}
	if !w.owns() {
		s.halt("ship ground assault actor preimages changed; no write attempted", "")
		return commandNotCommitted
	}
	outcome, e = s.store.CommitShipGroundAssault(in)
	if outcome == db.CommandCommitted {
		if s.testHooks != nil && s.testHooks.beforeCommandInstall != nil {
			s.testHooks.beforeCommandInstall()
		}
		if !w.owns() {
			s.halt("ship ground assault COMMITTED; installation identities changed; no shutdown flush", "")
			_ = sess.Send("Ship ground assault COMMITTED; reconciliation required; engine halted.\n")
			return commandCommittedError
		}
		// No callbacks/allocations between these assignments; all identities were
		// checked together. Preserve the actor records' addresses and sector slice.
		pl := w.planets[[2]uint8{in.Input.Planet.StarNum, in.Input.Planet.PlanetNum}].actor
		sectors := pl.Sectors
		copy(sectors, in.Proposed.Planet.Sectors)
		*pl = *in.Proposed.Planet
		pl.Sectors = sectors
		*w.races[p].actor = *in.Proposed.Attacker
		*w.races[in.Input.Defender.PlayerNum].actor = *in.Proposed.Defender
		*w.ships[id].actor = *in.Proposed.Ship
		if s.testHooks != nil && s.testHooks.afterCommandInstall != nil {
			s.testHooks.afterCommandInstall()
		}
		if e == nil {
			_, tele, _ := db.ShipAssaultMessages(in)
			// Never call warn/pushTelegram: the inbox is already committed. A failed
			// online delivery must not create a second inbox write.
			if s.registry != nil {
				delivered := s.registry.Notify(tele.Player, tele.Governor, tele.Text)
				if !delivered && tele.Governor != 0 {
					s.registry.Notify(tele.Player, 0, tele.Text)
				}
			}
			if e = w.effects.release(sess); e == nil {
				return commandCommitted
			}
		}
	}
	s.halt(fmt.Sprintf("ship ground assault %s; persistence or response failed; reconciliation required; no shutdown flush", outcome), "")
	_ = sess.Send(fmt.Sprintf("Ship ground assault %s; reconciliation required; engine halted.\n", outcome))
	if outcome == db.CommandCommitted {
		return commandCommittedError
	}
	if outcome == db.CommandUncertain {
		return commandUncertain
	}
	return commandNotCommitted
}
