package game

// ship.go implements per-ship turn processing and movement.
//
// Sources ported:
//   doship.c    – doship(), domass(), doown(), domissile(), domine(), doabm(),
//                 do_repair(), do_habitat(), do_pod(), infect_planet(),
//                 do_meta_infect(), do_canister(), do_greenhouse(), do_mirror(),
//                 do_god(), do_ap(), do_oap(), do_weapon_plant()
//   moveship.c  – Moveship(), msg_OOF(), followable(), do_merchant()
//   misc.c      – logscale()
//   max.c       – gravity()
//   GB_server.c – kill_ship(), adjust_morale()

import (
	"fmt"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
)

// MoveConsts scales the movement amount per orbit level (univ, star, plan).
// Matches MoveConsts[] in moveship.c.
var moveConsts = [3]float64{600.0, 300.0, 50.0}

// SpeedConsts converts a ship's speed (0–9) to a distance multiplier.
// Matches SpeedConsts[] in moveship.c.
var speedConsts = [10]float64{0.0, 0.61, 1.26, 1.50, 1.73, 1.81, 1.90, 1.93, 1.96, 1.97}

// ---- Ship cargo/mass helpers -----------------------------------------------
// These mirror use_fuel(), rcv_fuel() etc. in load.c.
// Each operation adjusts both the cargo quantity and the ship's mass.

func (s *State) useFuel(ship *model.Ship, amt float64) {
	ship.Fuel -= amt
	ship.Mass -= amt * s.cfg.MassFuel
}

func (s *State) useDestruct(ship *model.Ship, amt int) {
	ship.Destruct -= uint16(amt)
	ship.Mass -= float64(amt) * s.cfg.MassDestruct
}

func (s *State) useResource(ship *model.Ship, amt int) {
	ship.Resource -= uint16(amt)
	ship.Mass -= float64(amt) * s.cfg.MassResource
}

func (s *State) rcvFuel(ship *model.Ship, amt float64) {
	ship.Fuel += amt
	ship.Mass += amt * s.cfg.MassFuel
}

func (s *State) rcvResource(ship *model.Ship, amt int) {
	ship.Resource += uint16(amt)
	ship.Mass += float64(amt) * s.cfg.MassResource
}

func (s *State) rcvDestruct(ship *model.Ship, amt int) {
	ship.Destruct += uint16(amt)
	ship.Mass += float64(amt) * s.cfg.MassDestruct
}

func (s *State) rcvPopn(ship *model.Ship, amt int, mass float64) {
	ship.Popn += uint16(amt)
	ship.Mass += float64(amt) * mass
}

// ---- Ship attribute helpers (mirrors of C macros in ships.h) ----------------

// shipMaxCrew returns the effective maximum crew, accounting for troops on board.
// Mirrors Max_crew(s) in ships.h.
func shipMaxCrew(ship *model.Ship) int {
	if ship.Type == model.OtypeFactory {
		return int(model.Shipdata[ship.Type][model.AbilMaxCrew]) - int(ship.Troops)
	}
	return int(ship.MaxCrew) - int(ship.Troops)
}

// shipMaxResource returns the maximum resource capacity.
// Mirrors Max_resource(s) in ships.h.
func shipMaxResource(ship *model.Ship) int {
	if ship.Type == model.OtypeFactory {
		return int(model.Shipdata[ship.Type][model.AbilCargo])
	}
	return int(ship.MaxResource)
}

// shipMaxDestruct returns the maximum destruct capacity.
// Mirrors Max_destruct(s) in ships.h.
func shipMaxDestruct(ship *model.Ship) int {
	if ship.Type == model.OtypeFactory {
		return int(model.Shipdata[ship.Type][model.AbilDestCap])
	}
	return int(ship.MaxDestruct)
}

// canRepair returns true if the ship has a repair capability.
// Mirrors Repair(s) in ships.h: factories repair when on, ships when crewed.
func canRepair(ship *model.Ship) bool {
	if ship.Type == model.OtypeFactory {
		return ship.On
	}
	return shipMaxCrew(ship) > 0
}

// shipBody returns the non-hanger structural size.
// Mirrors Body(s) = size - max_hanger in ships.h.
func shipBody(ship *model.Ship) int {
	return int(ship.Size) - int(ship.MaxHanger)
}

// getMass returns the base structural mass of a ship (without cargo).
// Mirrors getmass() in build.c.
func (s *State) getMass(ship *model.Ship) float64 {
	return 1.0 +
		s.cfg.MassArmor*float64(ship.Armor) +
		s.cfg.MassSize*float64(shipBody(ship)) +
		s.cfg.MassHanger*float64(ship.MaxHanger) +
		s.cfg.MassGuns*float64(ship.Primary)*float64(ship.PrimType) +
		s.cfg.MassGuns*float64(ship.Secondary)*float64(ship.SecType)
}

// shipSize computes the current size of a ship from its components.
// Mirrors ship_size() in build.c.  Called once per turn (for debugging in C).
func (s *State) shipSize(ship *model.Ship) int {
	size := 1.0 +
		s.cfg.SizeGuns*float64(ship.Primary) +
		s.cfg.SizeGuns*float64(ship.Secondary) +
		s.cfg.SizeCrew*float64(ship.MaxCrew) +
		s.cfg.SizeResource*float64(ship.MaxResource) +
		s.cfg.SizeFuel*float64(ship.MaxFuel) +
		s.cfg.SizeDestruct*float64(ship.MaxDestruct) +
		float64(ship.MaxHanger)
	return int(size)
}

// ---- Planet gravity helper -------------------------------------------------

// gravity returns the gravitational constant for a planet.
// Mirrors gravity() in max.c.
func (s *State) gravityOf(planet *model.Planet) float64 {
	return float64(planet.MaxX) * float64(planet.MaxY) * s.cfg.GravFactor
}

// ---- Misc math helper ------------------------------------------------------

// logscale maps [0,100] to [0,1] on a logarithmic curve.
// Mirrors logscale() in misc.c.
func logscale(x int) float64 {
	return math.Log10(float64(x)+1.0) / 2.0
}

// ---- kill_ship / morale ----------------------------------------------------

// killShip marks a ship dead and updates morale accordingly.
// Mirrors kill_ship() in GB_server.c.
func (s *State) killShip(playerNum int, ship *model.Ship) {
	ship.WhoKilled = int16(playerNum)
	ship.Alive = false
	ship.Notified = false

	victim := s.raceByNum(int(ship.Owner))
	if victim == nil {
		return
	}

	if ship.Type == model.OtypeVN || ship.Type == model.OtypeBers {
		if s.sdata != nil {
			s.sdata.VNHitList[playerNum-1]++
			// Update VN location indices (random slot replacement)
			if intRand(0, 1) == 0 {
				s.sdata.VNIndex1[playerNum-1] = ship.StOrbits
			} else {
				s.sdata.VNIndex2[playerNum-1] = ship.StOrbits
			}
		}
		return
	}

	if victim.GovShip == ship.Number {
		victim.GovShip = 0
	}

	if !victim.God && playerNum != int(ship.Owner) && ship.Type != model.OtypeVN {
		killer := s.raceByNum(playerNum)
		if killer != nil {
			adjustMorale(killer, victim, int(ship.BuildCost))
		}
	} else if int(ship.Owner) == playerNum && !ship.Docked && shipMaxCrew(ship) > 0 {
		victim.Morale -= 2 * int64(ship.BuildCost)
	}
}

// adjustMorale adjusts morale after combat.
// Mirrors adjust_morale() in GB_server.c.
func adjustMorale(winner, loser *model.Race, amount int) {
	winner.Morale += int64(amount)
	loser.Morale -= int64(amount)
}

// shootShipToShip, shootShipToPlanet, retalStrength, currentCaliber
// are implemented in combat.go.

// postCombat logs a combat event to the news feed.
func (s *State) postCombat(msg string) {
	s.post(model.NewsCombat, msg)
}

// notifyStar sends a message to all players inhabiting a star, except
// the sender (playerNum/governor).  Mirrors notify_star() in GB_server.c.
func (s *State) notifyStar(playerNum, governor, god, starNum int, msg string) {
	if starNum < 0 || starNum >= len(s.stars) || s.stars[starNum] == nil {
		return
	}
	star := s.stars[starNum]
	for i, r := range s.races {
		if r == nil {
			continue
		}
		p := i + 1
		if p == playerNum {
			continue // don't echo back to sender
		}
		if star.IsInhabited(p) {
			s.pushTelegram(p, 0, msg)
		}
	}
}

// doVN is implemented in vn.go.

// ---- Main ship turn --------------------------------------------------------

// doShip processes a single ship for one turn tick.
// Corresponds to doship() in doship.c.
// Called by doTurn for every live ship.
func (s *State) doShip(ship *model.Ship, update bool) {
	segments := s.cfg.MovesPerUpdate

	ship.Active = true

	if ship.Owner == 0 {
		ship.Alive = false
	}

	if !ship.Alive {
		return
	}

	// --- Radiation damage ---------------------------------------------------
	if ship.Rad > 0 {
		ship.Active = true
		// Irradiated ships may be immobilised this tick.
		if success(int(ship.Rad)) {
			ship.Active = false
		}
		if update {
			ship.Popn = uint16(roundRand(float64(ship.Popn) * 0.80))
			ship.Troops = uint16(roundRand(float64(ship.Troops) * 0.80))
			repairRate := int(s.cfg.RepairRate)
			if int(ship.Rad) >= repairRate {
				ship.Rad -= uint8(intRand(0, repairRate))
			} else {
				ship.Rad -= uint8(intRand(0, int(ship.Rad)))
			}
		}
	} else {
		ship.Active = true
	}

	// Ships with no crew and no dock destination float adrift.
	if ship.Popn == 0 && shipMaxCrew(ship) > 0 && !ship.Docked {
		ship.WhatDest = model.LevelUniv
	}

	// --- Nova stage damage --------------------------------------------------
	if ship.WhatOrbits != model.LevelUniv {
		star := s.stars[ship.StOrbits]
		if star != nil && star.NovaStage > 0 {
			dmg := 5 * int(star.NovaStage) / (int(ship.EffArmor(model.Shipdata)) + 1) / segments
			ship.Damage += uint8(dmg)
			if ship.Damage >= 100 {
				s.killShip(int(ship.Owner), ship)
				return
			}
		}
	}

	// --- Factory syncs tech to race -----------------------------------------
	if ship.Type == model.OtypeFactory && !ship.On {
		race := s.raceByNum(int(ship.Owner))
		if race != nil {
			ship.Tech = race.Tech
		}
	}

	// --- Movement -----------------------------------------------------------
	if ship.Active {
		s.moveShip(ship, update, true, false)
	}

	// Refresh size (in C this is "for debugging", but Body() uses it).
	ship.Size = uint16(s.shipSize(ship))

	// --- Orbit of another ship: sync ownership ------------------------------
	if ship.WhatOrbits == model.LevelShip {
		mother := s.ships[ship.DestShipNo]
		if mother != nil && mother.Owner != ship.Owner {
			mother.Owner = ship.Owner
			mother.Governor = ship.Governor
		}
	} else if ship.WhatOrbits != model.LevelUniv &&
		(ship.Popn > 0 || ship.Type == model.OtypeProbe) {
		// Mark star explored/inhabited by the ship's owner.
		star := s.stars[ship.StOrbits]
		if star != nil {
			star.SetInhabited(int(ship.Owner))
			star.SetExplored(int(ship.Owner))
			if ship.WhatOrbits == model.LevelPlan {
				planet := s.planets[ship.StOrbits][ship.PNumOrbits]
				if planet != nil {
					planet.Info[ship.Owner-1].Explored = true
				}
			}
		}
	}

	// --- Power accumulators (update only) -----------------------------------
	if update && int(ship.Owner) >= 1 && int(ship.Owner) <= model.MaxPlayers {
		pw := &s.power[ship.Owner-1]
		pw.ShipsOwned++
		pw.Resource += uint64(ship.Resource)
		pw.Fuel += uint64(ship.Fuel)
		pw.Destruct += uint64(ship.Destruct)
		pw.Popn += uint64(ship.Popn)
		pw.Troops += uint64(ship.Troops)
	}

	// Mark star/planet inhabited by this ship.
	if ship.WhatOrbits != model.LevelUniv &&
		(ship.Popn > 0 || ship.Type == model.OtypeProbe) {
		star := s.stars[ship.StOrbits]
		if star != nil {
			star.SetInhabited(int(ship.Owner))
			star.SetExplored(int(ship.Owner))
		}
	}

	// --- Active ship special actions ----------------------------------------
	if !ship.Active {
		return
	}

	// Bombardment: mark planet as having hostile activity.
	if ship.CanBombard(model.Shipdata) && ship.Bombard &&
		ship.WhatOrbits == model.LevelPlan &&
		ship.WhatDest == model.LevelPlan &&
		ship.DestStar == ship.StOrbits &&
		ship.DestPNum == ship.PNumOrbits {
		s.stinfo[ship.StOrbits][ship.PNumOrbits].Inhab = true
	}

	// Repair.
	if ship.Damage > 0 && canRepair(ship) {
		s.doRepair(ship)
	}

	// Per-type update actions.
	if update {
		switch ship.Type {
		case model.OtypeCanist:
			s.doCanister(ship)
		case model.OtypeGreen:
			s.doGreenhouse(ship)
		case model.StypeMirror:
			s.doMirror(ship)
		case model.StypeGod:
			s.doGodShip(ship)
		case model.OtypeAP:
			s.doAP(ship)
		case model.OtypeVN, model.OtypeBers:
			if ship.Special.Mind != nil && ship.Special.Mind.Progenitor == 0 {
				ship.Special.Mind.Progenitor = 1
			}
			s.doVN(ship)
		case model.StypeOAP:
			s.doOAP(ship)
		case model.StypeHabitat:
			s.doHabitat(ship)
		}
	}

	if ship.Type == model.StypePod {
		s.doPod(ship)
	}
}

// ---- Mass / ownership propagation ------------------------------------------

// doMass recomputes the mass of a ship and all ships docked within it
// recursively, then adds cargo mass on top.
// Mirrors domass() in doship.c.
func (s *State) doMass(ship *model.Ship) {
	race := s.raceByNum(int(ship.Owner))
	var raceMass float64
	if race != nil {
		raceMass = race.Mass
	}

	ship.Mass = 0
	ship.Hanger = 0
	sh := ship.Ships
	for sh != 0 {
		child := s.ships[sh]
		if child == nil {
			break
		}
		s.doMass(child)
		ship.Mass += child.Mass
		ship.Hanger += child.Size
		sh = child.NextShip
	}

	ship.Mass += s.getMass(ship)
	ship.Mass += float64(ship.Popn+ship.Troops) * raceMass
	ship.Mass += float64(ship.Destruct) * s.cfg.MassDestruct
	ship.Mass += ship.Fuel * s.cfg.MassFuel
	ship.Mass += float64(ship.Resource) * s.cfg.MassResource
}

// doOwn propagates ownership to all ships docked inside a ship recursively.
// Mirrors doown() in doship.c.
func (s *State) doOwn(ship *model.Ship) {
	sh := ship.Ships
	for sh != 0 {
		child := s.ships[sh]
		if child == nil {
			break
		}
		s.doOwn(child)
		child.Owner = ship.Owner
		child.Governor = ship.Governor
		sh = child.NextShip
	}
}

// ---- Missile / mine / ABM --------------------------------------------------

// doMissile checks whether a missile has reached its target and detonates it.
// Mirrors domissile() in doship.c.
func (s *State) doMissile(ship *model.Ship) {
	if !ship.Alive || ship.Owner == 0 || !ship.On || ship.Docked {
		return
	}

	// Arrived at target planet — look for PDNs first.
	if ship.WhatDest == model.LevelPlan &&
		ship.WhatOrbits == model.LevelPlan &&
		ship.DestPNum == ship.PNumOrbits {

		planet := s.planets[ship.StOrbits][ship.PNumOrbits]
		if planet == nil {
			return
		}

		// Check for Planetary Defense Network ships.
		pdn := uint32(0)
		sh := planet.Ships
		for sh != 0 && pdn == 0 {
			def := s.ships[sh]
			if def == nil {
				break
			}
			if def.Alive && def.Type == model.OtypePlanDef {
				ship.WhatDest = model.LevelShip
				ship.XPos = def.XPos
				ship.YPos = def.YPos
				ship.DestShipNo = def.Number
				pdn = def.Number
			}
			sh = def.NextShip
		}

		if pdn == 0 {
			// Determine impact coordinates.
			var bombX, bombY int
			if ship.Special.Impact != nil && ship.Special.Impact.Scatter != 0 {
				bombX = intRand(1, int(planet.MaxX)) - 1
				bombY = intRand(1, int(planet.MaxY)) - 1
			} else if ship.Special.Impact != nil {
				bombX = int(ship.Special.Impact.X) % int(planet.MaxX)
				bombY = int(ship.Special.Impact.Y) % int(planet.MaxY)
			}

			var longBuf, shortBuf string
			numdest := s.shootShipToPlanet(ship, planet, int(ship.Destruct),
				bombX, bombY, 1, 0, 3 /*HEAVY*/, &longBuf, &shortBuf)
			s.pushTelegram(int(ship.Owner), int(ship.Governor), longBuf)
			s.killShip(int(ship.Owner), ship)

			numRaces := s.numRaces()
			for i := 1; i <= numRaces; i++ {
				if int(planet.Info[i-1].NumSectsOwned) > 0 && i != int(ship.Owner) {
					star := s.stars[ship.StOrbits]
					var gov int
					if star != nil {
						gov = int(star.Governor[i-1])
					}
					s.pushTelegram(i, gov,
						fmt.Sprintf("%s dropped on %s.\n\t%d sectors destroyed.\n",
							shipDisplayName(ship), s.shipLocation(ship), numdest))
				}
			}
			if numdest > 0 {
				s.postCombat(fmt.Sprintf("%s dropped on %s.\n",
					shipDisplayName(ship), s.shipLocation(ship)))
			}
		}
		return
	}

	// Homing missile — check range to target ship.
	if ship.WhatDest == model.LevelShip {
		target := s.ships[ship.DestShipNo]
		if target == nil {
			return
		}
		dist := math.Hypot(ship.XPos-target.XPos, ship.YPos-target.YPos)
		threshold := float64(ship.Speed) * s.cfg.StrikeDistanceFactor *
			(100.0 - float64(ship.Damage)) / 100.0
		if dist <= threshold {
			var longBuf, shortBuf string
			s.shootShipToShip(ship, target, int(ship.Destruct), 0, 0,
				&longBuf, &shortBuf)
			s.pushTelegram(int(ship.Owner), int(ship.Governor), longBuf)
			s.pushTelegram(int(target.Owner), int(target.Governor), longBuf)
			s.killShip(int(ship.Owner), ship)
			s.postCombat(shortBuf)
		}
	}
}

// doMine checks proximity and detonates a mine if enemies are in range.
// Mirrors domine() in doship.c.
func (s *State) doMine(shipNo uint32, detonate bool) {
	ship := s.ships[shipNo]
	if ship == nil {
		return
	}
	if ship.Type != model.StypeMine || !ship.Alive || ship.Owner == 0 {
		return
	}
	if !ship.On && !detonate {
		return
	}

	// Find the ship-list for this orbit level.
	var firstShip uint32
	switch ship.WhatOrbits {
	case model.LevelStar:
		star := s.stars[ship.StOrbits]
		if star == nil {
			return
		}
		firstShip = star.Ships
	case model.LevelPlan:
		planet := s.planets[ship.StOrbits][ship.PNumOrbits]
		if planet == nil {
			return
		}
		firstShip = planet.Ships
	default:
		return
	}

	// Scan for enemy within trigger radius.
	triggered := detonate
	if !triggered {
		race := s.raceByNum(int(ship.Owner))
		sh := firstShip
		for sh != 0 && !triggered {
			other := s.ships[sh]
			if other == nil {
				break
			}
			if !race.IsAllied(int(other.Owner)) && other.Owner != ship.Owner {
				dx := other.XPos - ship.XPos
				dy := other.YPos - ship.YPos
				rng := math.Sqrt(dx*dx + dy*dy)
				if ship.Special.Trigger != nil && int(rng) <= int(ship.Special.Trigger.Radius) {
					triggered = true
					break
				}
			}
			sh = other.NextShip
		}
	}

	if !triggered {
		return
	}

	// Detonate: attack all ships in orbit.
	s.postCombat(fmt.Sprintf("%s detonated at %s\n",
		shipDisplayName(ship), s.shipLocation(ship)))
	s.notifyStar(int(ship.Owner), int(ship.Governor), 0, int(ship.StOrbits),
		fmt.Sprintf("%s detonated at %s\n", shipDisplayName(ship), s.shipLocation(ship)))

	sh := firstShip
	for sh != 0 {
		other := s.ships[sh]
		if other == nil {
			break
		}
		next := other.NextShip
		if sh != shipNo && other.Alive &&
			other.Type != model.OtypeCanist && other.Type != model.OtypeGreen {
			var longBuf, shortBuf string
			dmg := s.shootShipToShip(ship, other, int(ship.Destruct), 0, 0,
				&longBuf, &shortBuf)
			if dmg > 0 {
				s.postCombat(shortBuf)
				s.pushTelegram(int(other.Owner), int(other.Governor), longBuf)
			}
		}
		sh = next
	}

	// If landed on a planet, nuke a sector too.
	if ship.WhatOrbits == model.LevelPlan {
		planet := s.planets[ship.StOrbits][ship.PNumOrbits]
		if planet != nil {
			var x, y int
			if landed(ship) {
				x, y = int(ship.LandX), int(ship.LandY)
			} else {
				x = intRand(0, int(planet.MaxX)-1)
				y = intRand(0, int(planet.MaxY)-1)
			}
			var longBuf, shortBuf string
			numdest := s.shootShipToPlanet(ship, planet,
				int(ship.Destruct), x, y, 1, 0, 1 /*LIGHT*/, &longBuf, &shortBuf)

			numRaces := s.numRaces()
			for i := 1; i <= numRaces; i++ {
				if planet.Info[i-1].NumSectsOwned > 0 {
					extra := ""
					if numdest > 0 {
						extra = fmt.Sprintf(" - %d sectors destroyed.", numdest)
					}
					star := s.stars[ship.StOrbits]
					gov := 0
					if star != nil {
						gov = int(star.Governor[i-1])
					}
					s.pushTelegram(i, gov,
						fmt.Sprintf("%s detonated at %s%s\n",
							shipDisplayName(ship), s.shipLocation(ship), extra))
				}
			}
		}
	}
	s.killShip(int(ship.Owner), ship)
}

// doABM fires an anti-ballistic missile ship at incoming missiles/mines.
// Mirrors doabm() in doship.c.
func (s *State) doABM(ship *model.Ship) {
	if !ship.Alive || ship.Owner == 0 || !ship.On || ship.Retaliate == 0 || ship.Destruct == 0 {
		return
	}
	if !landed(ship) {
		return
	}

	planet := s.planets[ship.StOrbits][ship.PNumOrbits]
	if planet == nil {
		return
	}

	race := s.raceByNum(int(ship.Owner))
	sh := planet.Ships
	for sh != 0 && ship.Destruct > 0 {
		target := s.ships[sh]
		if target == nil {
			break
		}
		if target.Alive &&
			(target.Type == model.StypeMissile || target.Type == model.StypeMine) &&
			target.Owner != ship.Owner {
			// Check mutual alliance exclusion.
			alliedA := race != nil && race.IsAllied(int(target.Owner))
			targetRace := s.raceByNum(int(target.Owner))
			alliedB := targetRace != nil && targetRace.IsAllied(int(ship.Owner))
			if !(alliedA && alliedB) {
				numdest := s.retalStrength(ship)
				numdest = min2i(numdest, int(ship.Destruct))
				numdest = min2i(numdest, int(ship.Retaliate))
				s.useDestruct(ship, numdest)
				var longBuf, shortBuf string
				s.shootShipToShip(ship, target, numdest, 0, 0, &longBuf, &shortBuf)
				s.pushTelegram(int(ship.Owner), int(ship.Governor), longBuf)
				s.pushTelegram(int(target.Owner), int(target.Governor), longBuf)
				s.postCombat(shortBuf)
			}
		}
		sh = target.NextShip
	}
}

// ---- Per-type ship actions -------------------------------------------------

// doRepair repairs a damaged ship, consuming resources (or free for stations).
// Mirrors do_repair() in doship.c.
func (s *State) doRepair(ship *model.Ship) {
	segments := s.cfg.MovesPerUpdate
	maxRep := s.cfg.RepairRate / float64(segments)

	var cost int
	// Stations and ships docked at stations repair for free.
	if model.Shipdata[ship.Type][model.AbilRepair] != 0 {
		cost = 0
	} else if ship.Docked && ship.WhatDest == model.LevelShip {
		mother := s.ships[ship.DestShipNo]
		if mother != nil && mother.Type == model.StypeStation {
			cost = 0
		} else {
			maxCrew := shipMaxCrew(ship)
			if maxCrew > 0 {
				maxRep *= float64(ship.Popn) / float64(maxCrew)
			}
			cost = int(0.005 * maxRep * float64(ship.BuildCost))
		}
	} else if ship.Docked && ship.WhatOrbits == model.LevelShip {
		mother := s.ships[ship.DestShipNo]
		if mother != nil && mother.Type == model.StypeStation {
			cost = 0
		} else {
			maxCrew := shipMaxCrew(ship)
			if maxCrew > 0 {
				maxRep *= float64(ship.Popn) / float64(maxCrew)
			}
			cost = int(0.005 * maxRep * float64(ship.BuildCost))
		}
	} else {
		maxCrew := shipMaxCrew(ship)
		if maxCrew > 0 {
			maxRep *= float64(ship.Popn) / float64(maxCrew)
		}
		cost = int(0.005 * maxRep * float64(ship.BuildCost))
	}

	drep := int(maxRep)
	if cost <= int(ship.Resource) {
		s.useResource(ship, cost)
		if int(ship.Damage)-drep < 0 {
			ship.Damage = 0
		} else {
			ship.Damage -= uint8(drep)
		}
	} else {
		// Use all remaining resources, repair proportionally.
		if cost > 0 {
			drep = int(maxRep * float64(ship.Resource) / float64(cost))
		}
		s.useResource(ship, int(ship.Resource))
		if int(ship.Damage)-drep < 0 {
			ship.Damage = 0
		} else {
			ship.Damage -= uint8(drep)
		}
	}
}

// doHabitat runs a habitat ship: converts fuel to resources, grows population.
// Mirrors do_habitat() in doship.c.
func (s *State) doHabitat(ship *model.Ship) {
	if !ship.On {
		return
	}
	race := s.raceByNum(int(ship.Owner))
	if race == nil {
		return
	}

	maxCrew := shipMaxCrew(ship)
	if maxCrew == 0 {
		return
	}

	fuse := ship.Fuel * (float64(ship.Popn) / float64(maxCrew)) *
		(1.0 - 0.01*float64(ship.Damage))
	add := int(fuse / 20)
	if int(ship.Resource)+add > shipMaxResource(ship) {
		add = shipMaxResource(ship) - int(ship.Resource)
	}
	fuse = 20.0 * float64(add)
	s.rcvResource(ship, add)
	s.useFuel(ship, fuse)

	// Weapon plants docked inside habitat produce destruct.
	sh := ship.Ships
	for sh != 0 {
		wplant := s.ships[sh]
		if wplant == nil {
			break
		}
		if wplant.Type == model.OtypeWPlant {
			s.rcvDestruct(ship, s.doWeaponPlant(wplant))
		}
		sh = wplant.NextShip
	}

	// Population growth.
	addPopn := roundRand(float64(ship.Popn) * race.BirthRate)
	maxCrew2 := shipMaxCrew(ship) // may have changed with troops
	if int(ship.Popn)+addPopn > maxCrew2 {
		addPopn = maxCrew2 - int(ship.Popn)
	}
	if addPopn > 0 {
		s.rcvPopn(ship, addPopn, race.Mass)
	}
}

// doPod handles a bio-pod heating up near a star or decaying in orbit.
// Mirrors do_pod() in doship.c.
func (s *State) doPod(ship *model.Ship) {
	if ship.Special.Pod == nil {
		return
	}
	segments := s.cfg.MovesPerUpdate

	if ship.WhatOrbits == model.LevelStar {
		star := s.stars[ship.StOrbits]
		if star == nil {
			return
		}
		if int(ship.Special.Pod.Temperature) >= s.cfg.PodThreshold {
			// Pod explodes — try to infect a random planet.
			i := intRand(0, int(star.NumPlanets)-1)
			msg := fmt.Sprintf("%s has warmed and exploded at %s\n",
				shipDisplayName(ship), s.shipLocation(ship))
			if s.infectPlanet(int(ship.Owner), int(ship.StOrbits), i) {
				msg += fmt.Sprintf("\tmeta-colony established on %s.", star.PlanetNames[i])
			} else {
				msg += "\tno spores have survived."
			}
			s.pushTelegram(int(ship.Owner), int(ship.Governor), msg)
			s.killShip(int(ship.Owner), ship)
		} else {
			ship.Special.Pod.Temperature += uint8(roundRand(
				float64(star.Temperature) / float64(segments)))
		}
	} else if ship.WhatOrbits == model.LevelPlan {
		if int(ship.Special.Pod.Decay) >= s.cfg.PodDecay {
			s.pushTelegram(int(ship.Owner), int(ship.Governor),
				fmt.Sprintf("%s has decayed at %s\n",
					shipDisplayName(ship), s.shipLocation(ship)))
			s.killShip(int(ship.Owner), ship)
		} else {
			ship.Special.Pod.Decay += uint8(roundRand(1.0 / float64(segments)))
		}
	}
}

// infectPlanet attempts to establish a meta-colony on a random planet sector.
// Mirrors infect_planet() in doship.c.
func (s *State) infectPlanet(who, starNum, planetNum int) bool {
	if success(s.cfg.SporeSuccessRate) {
		planet := s.planets[uint8(starNum)][uint8(planetNum)]
		if planet != nil {
			s.doMetaInfect(who, planet)
		}
		return true
	}
	return false
}

// doMetaInfect plants a pod colony in a random sector.
// Mirrors do_meta_infect() in doship.c.
func (s *State) doMetaInfect(who int, planet *model.Planet) {
	x := intRand(0, int(planet.MaxX)-1)
	y := intRand(0, int(planet.MaxY)-1)
	sec := planet.SectorAt(x, y)
	if sec == nil {
		return
	}

	owner := int(sec.Owner)
	ownerRace := s.raceByNum(owner)
	contested := owner != 0 && who != owner &&
		ownerRace != nil &&
		float64(intRand(1, 100)) >
			100.0*(1.0-math.Exp(-float64(sec.Troops)*float64(ownerRace.Fighters)/50.0))

	if !contested {
		planet.Info[who-1].Explored = true
		planet.Info[who-1].NumSectsOwned++
		sec.Troops = 0
		race := s.raceByNum(who)
		if race != nil {
			sec.Popn = uint16(race.NumberSexes)
		} else {
			sec.Popn = 1
		}
		sec.Owner = uint8(who)
		sec.Condition = sec.Type
	}
}

// doCanister ticks down a dust canister, cooling the planet below.
// Mirrors do_canister() in doship.c.
func (s *State) doCanister(ship *model.Ship) {
	if ship.WhatOrbits != model.LevelPlan || landed(ship) {
		return
	}
	if ship.Special.Timer == nil {
		ship.Special.Timer = &model.TimerSpecial{}
	}
	ship.Special.Timer.Count++
	if int(ship.Special.Timer.Count) < s.cfg.Dissipate {
		if s.stinfo[ship.StOrbits][ship.PNumOrbits].TempAdd < -90 {
			s.stinfo[ship.StOrbits][ship.PNumOrbits].TempAdd = -100
		} else {
			s.stinfo[ship.StOrbits][ship.PNumOrbits].TempAdd -= 10
		}
	} else {
		// Timer expired — destroy canister, notify all players on planet.
		planet := s.planets[ship.StOrbits][ship.PNumOrbits]
		star := s.stars[ship.StOrbits]
		var starName string
		if star != nil {
			starName = star.Name
		}
		numRaces := s.numRaces()
		for j := 1; j <= numRaces; j++ {
			if planet != nil && planet.Info[j-1].NumSectsOwned > 0 {
				gov := 0
				if star != nil {
					gov = int(star.Governor[j-1])
				}
				s.pushTelegram(j, gov,
					fmt.Sprintf("Canister of dust previously covering /%s has dissipated.\n",
						starName))
			}
		}
		s.killShip(int(ship.Owner), ship)
	}
}

// doGreenhouse ticks a greenhouse gas ship, warming the planet below.
// Mirrors do_greenhouse() in doship.c.
func (s *State) doGreenhouse(ship *model.Ship) {
	if ship.WhatOrbits != model.LevelPlan || landed(ship) {
		return
	}
	if ship.Special.Timer == nil {
		ship.Special.Timer = &model.TimerSpecial{}
	}
	ship.Special.Timer.Count++
	if int(ship.Special.Timer.Count) < s.cfg.Dissipate {
		if s.stinfo[ship.StOrbits][ship.PNumOrbits].TempAdd > 90 {
			s.stinfo[ship.StOrbits][ship.PNumOrbits].TempAdd = 100
		} else {
			s.stinfo[ship.StOrbits][ship.PNumOrbits].TempAdd += 10
		}
	} else {
		planet := s.planets[ship.StOrbits][ship.PNumOrbits]
		star := s.stars[ship.StOrbits]
		numRaces := s.numRaces()
		for j := 1; j <= numRaces; j++ {
			if planet != nil && planet.Info[j-1].NumSectsOwned > 0 {
				gov := 0
				if star != nil {
					gov = int(star.Governor[j-1])
				}
				s.pushTelegram(j, gov,
					fmt.Sprintf("Greenhouse gases at %s have dissipated.\n",
						s.shipLocation(ship)))
			}
		}
		s.killShip(int(ship.Owner), ship)
	}
}

// doMirror fires a solar mirror at a ship or planet.
// Mirrors do_mirror() in doship.c.
func (s *State) doMirror(ship *model.Ship) {
	if ship.Special.AimedAt == nil {
		return
	}
	aim := ship.Special.AimedAt
	switch aim.Level {
	case model.LevelShip:
		target := s.ships[uint32(aim.ShipNo)]
		if target == nil || !target.Alive {
			return
		}
		// Must be in the same system.
		if (ship.WhatOrbits != model.LevelStar && ship.WhatOrbits != model.LevelPlan) ||
			(target.WhatOrbits != model.LevelStar && target.WhatOrbits != model.LevelPlan) ||
			ship.StOrbits != target.StOrbits {
			return
		}
		rng := math.Hypot(ship.XPos-target.XPos, ship.YPos-target.YPos)
		dmg := intRand(0, roundRand(
			(2.0/float64(shipBody(target)))*float64(aim.Intensity)/
				(rng/s.cfg.PLOrbitSize+1.0)))
		msg := fmt.Sprintf("%s aimed at %s\n", shipDisplayName(ship), shipDisplayName(target))
		target.Damage += uint8(dmg)
		if dmg > 0 {
			msg += fmt.Sprintf("%d%% damage done.\n", dmg)
		}
		if target.Damage >= 100 {
			msg += fmt.Sprintf("%s DESTROYED!!!\n", shipDisplayName(target))
			s.killShip(int(ship.Owner), target)
		}
		s.pushTelegram(int(target.Owner), int(target.Governor), msg)
		s.pushTelegram(int(ship.Owner), int(ship.Governor), msg)

	case model.LevelPlan:
		star := s.stars[ship.StOrbits]
		planet := s.planets[ship.StOrbits][aim.PlanetNum]
		if star == nil || planet == nil {
			return
		}
		rng := math.Hypot(
			ship.XPos-(star.XPos+planet.XPos),
			ship.YPos-(star.YPos+planet.YPos))
		var intensity int
		if rng > s.cfg.PLOrbitSize {
			intensity = int(s.cfg.PLOrbitSize) * int(aim.Intensity) / int(rng)
		} else {
			intensity = int(aim.Intensity)
		}
		i := roundRand(0.01 * (100.0 - float64(ship.Damage)) * float64(intensity))
		s.stinfo[ship.StOrbits][aim.PlanetNum].TempAdd += int16(i)

	case model.LevelStar:
		if int(aim.StarNum) > 0 && int(aim.StarNum) < model.NumStars &&
			ship.WhatOrbits > model.LevelUniv &&
			aim.StarNum == ship.StOrbits {
			star := s.stars[aim.StarNum]
			if star != nil {
				star.Stability += uint8(intRand(0, 1))
			}
		}
	}
}

// doGodShip refills a god ship's cargo to max capacity.
// Mirrors do_god() in doship.c.
func (s *State) doGodShip(ship *model.Ship) {
	race := s.raceByNum(int(ship.Owner))
	if race == nil || !race.God {
		return
	}
	ship.Fuel = shipMaxFuel(ship)
	ship.Destruct = uint16(shipMaxDestruct(ship))
	ship.Resource = uint16(shipMaxResource(ship))
}

// doAP runs an atmosphere processor ship: gradually shifts planet conditions.
// Mirrors do_ap() in doship.c.
func (s *State) doAP(ship *model.Ship) {
	if !landed(ship) || !ship.On {
		return
	}
	race := s.raceByNum(int(ship.Owner))
	if race == nil {
		return
	}
	planet := s.planets[ship.StOrbits][ship.PNumOrbits]
	if planet == nil {
		return
	}
	if ship.Fuel >= 3.0 {
		s.useFuel(ship, 3.0)
		apf := apPlanetFactor(planet)
		cf := crewFactor(ship)
		for j := model.AtmosRTemp + 1; j <= model.AtmosOther; j++ {
			d := roundRand(apf * cf * float64(race.Conditions[j]-planet.Conditions[j]))
			if d != 0 {
				planet.Conditions[j] += int16(d)
			}
		}
	} else if !ship.Notified {
		ship.Notified = true
		ship.On = false
		s.msgOOF(ship)
	}
}

// doOAP marks a planet as intimidated for enslavement purposes.
// Mirrors do_oap() in doship.c.
func (s *State) doOAP(ship *model.Ship) {
	if ship.WhatOrbits == model.LevelPlan {
		s.stinfo[ship.StOrbits][ship.PNumOrbits].Intimidated = true
	}
}

// doWeaponPlant produces destruct from a weapon plant ship.
// Mirrors do_weapon_plant() in doship.c.
func (s *State) doWeaponPlant(ship *model.Ship) int {
	race := s.raceByNum(int(ship.Owner))
	if race == nil {
		return 0
	}
	maxrate := int(race.Tech / 2.0)

	resCap := float64(ship.Resource) / s.cfg.ResCostWPlant
	fuelCap := ship.Fuel / s.cfg.FuelCostWPlant
	rateF := resCap
	if fuelCap < rateF {
		rateF = fuelCap
	}
	maxCrew := shipMaxCrew(ship)
	if maxCrew <= 0 {
		return 0
	}
	rate := roundRand(rateF *
		(1.0 - 0.01*float64(ship.Damage)) *
		float64(ship.Popn) / float64(maxCrew))
	if rate > maxrate {
		rate = maxrate
	}
	s.useResource(ship, rate*int(s.cfg.ResCostWPlant))
	s.useFuel(ship, float64(rate)*s.cfg.FuelCostWPlant)
	return rate
}

// crewFactor returns the fraction of max crew currently aboard (0.0–1.0).
// Mirrors crew_factor() in doship.c.
func crewFactor(ship *model.Ship) float64 {
	maxCrew := int(model.Shipdata[ship.Type][model.AbilMaxCrew])
	if maxCrew == 0 {
		return 0.0
	}
	return float64(ship.Popn) / float64(maxCrew)
}

// apPlanetFactor returns how effective an AP is given planet size.
// Mirrors ap_planet_factor() in doship.c.  Larger planets slow AP down.
func apPlanetFactor(planet *model.Planet) float64 {
	x := float64(planet.MaxX) * float64(planet.MaxY)
	return apFactorConst / (apFactorConst + x)
}

// apFactorConst mirrors AP_FACTOR from tweakables.h.
const apFactorConst = 50.0

// ---- Ship movement ---------------------------------------------------------

// moveShip moves a ship one tick toward its destination.
// mode=true means this is an update tick (hyperdrive will fire).
// sendMessages=true means OOF and arrival telegrams are sent.
// checkingFuel=true suppresses side effects (used for fuel prediction).
// Mirrors Moveship() in moveship.c.
func (s *State) moveShip(ship *model.Ship, mode bool, sendMessages bool, checkingFuel bool) {
	segments := s.cfg.MovesPerUpdate

	// ---- Hyperspace --------------------------------------------------------
	if ship.HyperDrive.Has && ship.HyperDrive.On {
		if !mode {
			return // hyperjumps only happen on full updates
		}
		if ship.HyperDrive.Ready {
			destStar := s.stars[ship.DestStar]
			if destStar == nil {
				return
			}
			dist := math.Hypot(ship.XPos-destStar.XPos, ship.YPos-destStar.YPos)
			distFac := s.cfg.HyperDistFactor * (ship.Tech + 100.0)
			var fuse float64
			if ship.Mounted && dist > distFac {
				fuse = s.cfg.HyperDriveFuelUse * math.Sqrt(ship.Mass) * (dist / distFac)
			} else {
				fuse = s.cfg.HyperDriveFuelUse * math.Sqrt(ship.Mass) *
					(dist / distFac) * (dist / distFac)
			}
			if ship.Fuel < fuse {
				if sendMessages {
					s.pushTelegram(int(ship.Owner), int(ship.Governor),
						fmt.Sprintf("%s at %s does not have %.1ff to do hyperspace jump.",
							shipDisplayName(ship), s.shipLocation(ship), fuse))
				}
				ship.HyperDrive.On = false
				return
			}
			s.useFuel(ship, fuse)
			heading := math.Atan2(destStar.XPos-ship.XPos, destStar.YPos-ship.YPos)
			sn := math.Sin(heading)
			cs := math.Cos(heading)
			ship.XPos = destStar.XPos - sn*0.9*s.cfg.SystemSize
			ship.YPos = destStar.YPos - cs*0.9*s.cfg.SystemSize
			ship.WhatOrbits = model.LevelStar
			ship.StOrbits = ship.DestStar
			ship.Protect.Planet = false
			ship.HyperDrive.On = false
			ship.HyperDrive.Ready = false
			ship.HyperDrive.Charge = 0
			if sendMessages {
				s.pushTelegram(int(ship.Owner), int(ship.Governor),
					fmt.Sprintf("%s arrived at %s.", shipDisplayName(ship), s.shipLocation(ship)))
			}
		} else if ship.Mounted {
			ship.HyperDrive.Ready = true
			ship.HyperDrive.Charge = uint8(s.cfg.HyperDriveReadyCharge)
		} else {
			if int(ship.HyperDrive.Charge) == s.cfg.HyperDriveReadyCharge {
				ship.HyperDrive.Ready = true
			} else {
				ship.HyperDrive.Charge++
			}
		}
		return
	}

	// ---- Impulse drive -----------------------------------------------------
	if ship.Speed == 0 || ship.Docked || !ship.Alive {
		return
	}
	if ship.WhatDest == model.LevelUniv && !ship.Navigate.On {
		return
	}

	fuse := 0.5 * float64(ship.Speed) * float64(1+btoi(ship.Protect.Evade)) *
		ship.Mass * s.cfg.FuelUse / float64(segments)

	if ship.Fuel < fuse {
		if sendMessages {
			s.msgOOF(ship)
		}
		// Small/cheap ships adrift in deep space are lost.
		if ship.WhatOrbits == model.LevelUniv &&
			(ship.BuildCost <= 50 || ship.Type == model.OtypeVN || ship.Type == model.OtypeBers) {
			if sendMessages {
				s.pushTelegram(int(ship.Owner), int(ship.Governor),
					fmt.Sprintf("%s has been lost in deep space.", shipDisplayName(ship)))
				s.killShip(int(ship.Owner), ship)
			}
		}
		return
	}

	if ship.Navigate.On {
		// ---- Navigational bearing mode ------------------------------------
		heading := 0.0174329252 * float64(ship.Navigate.Bearing)
		mfactor := s.cfg.ShipMoveScale *
			(1.0 - 0.01*float64(ship.Rad)) *
			(1.0 - 0.01*float64(ship.Damage)) *
			speedConsts[ship.Speed] *
			moveConsts[ship.WhatOrbits] /
			float64(segments)
		s.useFuel(ship, fuse)
		sn := math.Sin(heading)
		cs := math.Cos(heading)
		ship.XPos += sn * mfactor
		ship.YPos += -cs * mfactor
		ship.Navigate.Turns--
		if ship.Navigate.Turns == 0 {
			ship.Navigate.On = false
		}

		// Check for orbit break.
		ost := s.stars[ship.StOrbits]
		if ost != nil {
			if ship.WhatOrbits == model.LevelPlan {
				opl := s.planets[ship.StOrbits][ship.PNumOrbits]
				if opl != nil {
					dist := math.Hypot(ship.XPos-(ost.XPos+opl.XPos), ship.YPos-(ost.YPos+opl.YPos))
					if dist > s.cfg.PLOrbitSize {
						ship.WhatOrbits = model.LevelStar
						ship.Protect.Planet = false
					}
				}
			} else if ship.WhatOrbits == model.LevelStar {
				dist := math.Hypot(ship.XPos-ost.XPos, ship.YPos-ost.YPos)
				if dist > s.cfg.SystemSize {
					ship.WhatOrbits = model.LevelUniv
					ship.Protect.Evade = false
					ship.Protect.Planet = false
				}
			}
		}
		return
	}

	// ---- Destination-following mode ----------------------------------------
	destLevel := int(ship.WhatDest)
	var xdest, ydest float64
	destStar := int(ship.DestStar)
	destPNum := int(ship.DestPNum)

	var dsh *model.Ship
	if destLevel == model.LevelShip {
		dsh = s.ships[ship.DestShipNo]
		if dsh == nil {
			ship.WhatDest = model.LevelUniv
			return
		}
		destStar = int(dsh.StOrbits)
		destPNum = int(dsh.PNumOrbits)
		xdest = dsh.XPos
		ydest = dsh.YPos
		switch dsh.WhatOrbits {
		case model.LevelPlan:
			if ship.WhatOrbits != dsh.WhatOrbits || ship.PNumOrbits != dsh.PNumOrbits {
				destLevel = model.LevelPlan
			}
		case model.LevelStar:
			if ship.WhatOrbits != dsh.WhatOrbits || ship.StOrbits != dsh.StOrbits {
				destLevel = model.LevelStar
			}
		}
	}

	dst := s.stars[destStar]
	ost := s.stars[ship.StOrbits]

	if destLevel == model.LevelStar ||
		(destLevel == model.LevelPlan &&
			(int(ship.StOrbits) != destStar || ship.WhatOrbits == model.LevelUniv)) {
		destLevel = model.LevelStar
		if dst != nil {
			xdest = dst.XPos
			ydest = dst.YPos
		}
	} else if destLevel == model.LevelPlan && int(ship.StOrbits) == destStar {
		dpl := s.planets[uint8(destStar)][uint8(destPNum)]
		if dst != nil && dpl != nil {
			xdest = dst.XPos + dpl.XPos
			ydest = dst.YPos + dpl.YPos
		}
		if math.Hypot(ship.XPos-xdest, ship.YPos-ydest) <= s.cfg.DistToLand {
			destLevel = model.LevelUniv
		}
	}

	trueDist := math.Hypot(ship.XPos-xdest, ship.YPos-ydest)
	moveDist := trueDist

	// Early-out: already at destination within tolerance.
	if trueDist < s.cfg.DistToLand &&
		ship.WhatOrbits == uint8(destLevel) &&
		int(ship.StOrbits) == destStar &&
		int(ship.PNumOrbits) == destPNum {
		return
	}

	heading := math.Atan2(xdest-ship.XPos, -ydest+ship.YPos)
	mfactor := s.cfg.ShipMoveScale *
		(1.0 - 0.01*float64(ship.Rad)) *
		(1.0 - 0.01*float64(ship.Damage)) *
		speedConsts[ship.Speed] *
		moveConsts[ship.WhatOrbits] /
		float64(segments)

	// Keep ship from overshooting into the center of the system.
	if destLevel == model.LevelStar &&
		(int(ship.StOrbits) != destStar || ship.WhatOrbits == model.LevelUniv) {
		moveDist -= s.cfg.SystemSize * 0.90
	} else if destLevel == model.LevelPlan && ship.WhatOrbits == model.LevelStar &&
		int(ship.StOrbits) == destStar && trueDist >= s.cfg.PLOrbitSize {
		moveDist -= s.cfg.PLOrbitSize * 0.90
	}

	// Following a ship: check it's still followable.
	if ship.WhatDest == model.LevelShip && dsh != nil {
		if !s.followable(ship, dsh) {
			ship.WhatDest = model.LevelUniv
			ship.Protect.Evade = false
			if sendMessages {
				s.pushTelegram(int(ship.Owner), int(ship.Governor),
					fmt.Sprintf("%s at %s lost sight of destination ship #%d.",
						shipDisplayName(ship), s.shipLocation(ship), ship.DestShipNo))
			}
			return
		}
	}

	if trueDist > s.cfg.DistToLand {
		s.useFuel(ship, fuse)
		sn := math.Sin(heading)
		cs := math.Cos(heading)
		dx := sn * mfactor
		dy := -cs * mfactor
		// Don't overshoot.
		if math.Hypot(dx, dy) > moveDist && moveDist > 0 {
			dx = sn * moveDist
			dy = -cs * moveDist
		}
		ship.XPos += dx
		ship.YPos += dy
	}

	// Check for orbit break after moving.
	if ost != nil {
		if ship.WhatOrbits == model.LevelPlan {
			opl := s.planets[ship.StOrbits][ship.PNumOrbits]
			if opl != nil {
				dist := math.Hypot(ship.XPos-(ost.XPos+opl.XPos), ship.YPos-(ost.YPos+opl.YPos))
				if dist > s.cfg.PLOrbitSize {
					ship.WhatOrbits = model.LevelStar
					ship.Protect.Planet = false
				}
			}
		} else if ship.WhatOrbits == model.LevelStar {
			dist := math.Hypot(ship.XPos-ost.XPos, ship.YPos-ost.YPos)
			if dist > s.cfg.SystemSize {
				ship.WhatOrbits = model.LevelUniv
				ship.Protect.Evade = false
				ship.Protect.Planet = false
			}
		}
	}

	// Check for arrival at destination star.
	if dst != nil && (destLevel == model.LevelStar ||
		(destLevel == model.LevelPlan &&
			(int(ship.StOrbits) != destStar || ship.WhatOrbits == model.LevelUniv))) {
		starDist := math.Hypot(ship.XPos-dst.XPos, ship.YPos-dst.YPos)
		if starDist <= s.cfg.SystemSize*1.5 {
			ship.WhatOrbits = model.LevelStar
			ship.Protect.Planet = false
			ship.StOrbits = uint8(destStar)
			// Set governor for newly-explored system.
			if !checkingFuel && (ship.Popn > 0 || ship.Type == model.OtypeProbe) {
				if !dst.IsInhabited(int(ship.Owner)) {
					dst.Governor[ship.Owner-1] = ship.Governor
				}
				dst.SetExplored(int(ship.Owner))
				dst.SetInhabited(int(ship.Owner))
			}
			if sendMessages && ship.Type != model.OtypeVN {
				s.pushTelegram(int(ship.Owner), int(ship.Governor),
					fmt.Sprintf("%s arrived at %s.", shipDisplayName(ship), s.shipLocation(ship)))
			}
			if ship.WhatDest == model.LevelStar {
				ship.WhatDest = model.LevelUniv
			}
		}
		return
	}

	// Check for arrival in planet orbit.
	if dst != nil && destLevel == model.LevelPlan && destStar == int(ship.StOrbits) {
		dpl := s.planets[uint8(destStar)][uint8(destPNum)]
		if dpl != nil {
			dist := math.Hypot(ship.XPos-(dst.XPos+dpl.XPos), ship.YPos-(dst.YPos+dpl.YPos))
			if dist <= s.cfg.PLOrbitSize {
				if !checkingFuel && (ship.Popn > 0 || ship.Type == model.OtypeProbe) {
					dpl.Info[ship.Owner-1].Explored = true
					dst.SetExplored(int(ship.Owner))
					dst.SetInhabited(int(ship.Owner))
				}
				ship.WhatOrbits = model.LevelPlan
				ship.PNumOrbits = uint8(destPNum)
				if dist <= s.cfg.DistToLand {
					if !checkingFuel && !s.doMerchant(ship, dpl) {
						if ship.WhatDest == model.LevelPlan {
							ship.WhatDest = model.LevelUniv
						}
					}
					if sendMessages {
						msg := fmt.Sprintf("%s within landing distance of %s.",
							shipDisplayName(ship), s.shipLocation(ship))
						if ship.Type == model.StypeOAP {
							msg += "\nEnslavement of the planet is now possible."
						}
						if ship.Type != model.OtypeVN {
							s.pushTelegram(int(ship.Owner), int(ship.Governor), msg)
						}
					}
				} else if sendMessages && ship.Type != model.OtypeVN {
					s.pushTelegram(int(ship.Owner), int(ship.Governor),
						fmt.Sprintf("%s arriving at %s.", shipDisplayName(ship), s.shipLocation(ship)))
				}
			}
		}
		return
	}

	// Check for arrival at another ship.
	if destLevel == model.LevelShip && dsh != nil {
		dist := math.Hypot(ship.XPos-dsh.XPos, ship.YPos-dsh.YPos)
		if dist <= s.cfg.PLOrbitSize {
			switch dsh.WhatOrbits {
			case model.LevelPlan:
				ship.WhatOrbits = model.LevelPlan
				ship.StOrbits = dsh.StOrbits
				ship.PNumOrbits = dsh.PNumOrbits
			case model.LevelStar:
				ship.WhatOrbits = model.LevelStar
				ship.StOrbits = dsh.StOrbits
				ship.Protect.Planet = false
			}
		}
	}
}

// followable returns true if s1 can follow s2 (same owner, allied, or close).
// Mirrors followable() in moveship.c.
func (s *State) followable(s1, s2 *model.Ship) bool {
	if !s2.Alive || !s1.Active || s2.WhatOrbits == model.LevelShip {
		return false
	}
	dx := s1.XPos - s2.XPos
	dy := s1.YPos - s2.YPos
	rng := 4.0 * logscale(int(s1.Tech+1.0)) * s.cfg.SystemSize

	if s1.Owner == s2.Owner {
		return true
	}
	race := s.raceByNum(int(s2.Owner))
	if race != nil && race.IsAllied(int(s1.Owner)) {
		return true
	}
	return math.Sqrt(dx*dx+dy*dy) <= rng
}

// doMerchant handles automated merchant route operations when a ship arrives.
// Returns true if the ship should remain on its route, false if orders complete.
// Mirrors do_merchant() in moveship.c.
func (s *State) doMerchant(ship *model.Ship, planet *model.Planet) bool {
	if ship.Merchant == 0 {
		return false
	}
	i := int(ship.Owner) - 1
	j := int(ship.Merchant) - 1
	if j < 0 || j >= model.MaxRoutes {
		return false
	}
	if !planet.Info[i].Route[j].Set {
		return false
	}
	route := &planet.Info[i].Route[j]

	// Verify sector ownership.
	sec := planet.SectorAt(int(route.X), int(route.Y))
	if sec != nil && sec.Owner != 0 && sec.Owner != ship.Owner {
		return false
	}

	// Land if not already landed.
	if !landed(ship) {
		fuel := ship.Mass * s.gravityOf(planet) * s.cfg.LandGravMassFactor
		if ship.Fuel < fuel {
			ship.WhatDest = model.LevelUniv
			s.pushTelegram(int(ship.Owner), int(ship.Governor),
				fmt.Sprintf("%s: Not enough fuel to land!\n", shipDisplayName(ship)))
			return true
		}
		ship.LandX = route.X
		ship.LandY = route.Y
		star := s.stars[ship.StOrbits]
		if star != nil {
			ship.XPos = planet.XPos + star.XPos
			ship.YPos = planet.YPos + star.YPos
		}
		s.useFuel(ship, fuel)
		ship.Docked = true
		ship.WhatDest = model.LevelPlan
		ship.DestStar = ship.StOrbits
		ship.DestPNum = ship.PNumOrbits
	}

	// Load cargo per route instructions.
	load := route.Load
	if load != 0 {
		if load&model.MFuel != 0 {
			amount := shipMaxFuel(ship) - ship.Fuel
			if amount > float64(planet.Info[i].Fuel) {
				amount = float64(planet.Info[i].Fuel)
			}
			planet.Info[i].Fuel -= uint16(amount)
			s.rcvFuel(ship, amount)
		}
		if load&model.MResources != 0 {
			amount := shipMaxResource(ship) - int(ship.Resource)
			if amount > int(planet.Info[i].Resource) {
				amount = int(planet.Info[i].Resource)
			}
			planet.Info[i].Resource -= uint16(amount)
			s.rcvResource(ship, amount)
		}
		if load&model.MCrystals != 0 {
			amount := int(planet.Info[i].Crystals)
			planet.Info[i].Crystals -= uint16(amount)
			ship.Crystals += uint16(amount)
		}
		if load&model.MDestruct != 0 {
			amount := shipMaxDestruct(ship) - int(ship.Destruct)
			if amount > int(planet.Info[i].Destruct) {
				amount = int(planet.Info[i].Destruct)
			}
			planet.Info[i].Destruct -= uint16(amount)
			s.rcvDestruct(ship, amount)
		}
	}

	// Unload cargo.
	unload := route.Unload
	if unload != 0 {
		if unload&model.MFuel != 0 {
			amount := ship.Fuel
			planet.Info[i].Fuel += uint16(amount)
			s.useFuel(ship, amount)
		}
		if unload&model.MResources != 0 {
			amount := int(ship.Resource)
			planet.Info[i].Resource += uint16(amount)
			s.useResource(ship, amount)
		}
		if unload&model.MCrystals != 0 {
			amount := int(ship.Crystals)
			planet.Info[i].Crystals += uint16(amount)
			ship.Crystals -= uint16(amount)
		}
		if unload&model.MDestruct != 0 {
			amount := int(ship.Destruct)
			planet.Info[i].Destruct += uint16(amount)
			s.useDestruct(ship, amount)
		}
	}

	// Launch toward next destination.
	fuel := ship.Mass * s.gravityOf(planet) * s.cfg.LaunchGravMassFactor
	if ship.Fuel < fuel {
		s.pushTelegram(int(ship.Owner), int(ship.Governor),
			fmt.Sprintf("%s: Not enough fuel to launch!\n", shipDisplayName(ship)))
		return true
	}
	ship.WhatDest = model.LevelPlan
	ship.DestStar = uint8(route.DestStar)
	ship.DestPNum = uint8(route.DestPlanet)
	ship.Docked = false
	s.useFuel(ship, fuel)

	// Set hyperjump orders if available and cross-system.
	if ship.HyperDrive.Has && int(ship.StOrbits) != int(route.DestStar) {
		ship.Navigate.On = false
		ship.HyperDrive.On = true
		if ship.Mounted {
			ship.HyperDrive.Charge = 1
			ship.HyperDrive.Ready = true
		} else {
			ship.HyperDrive.Charge = 0
			ship.HyperDrive.Ready = false
		}
	}
	return true
}

// btoi converts a bool to 0 or 1.
func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
