package game

// commands_load.go implements cmdLoad and related transfer commands.
//
// C sources ported: load.c
//   cmdLoad      — load(mode=0): load commodities onto ship
//   cmdUnload    — load(mode=1): unload commodities from ship
//   cmdJettison  — jettison(): discard commodities from a ship in space
//   cmdDump      — dump(): give exploration data to another player
//   cmdTransfer  — transfer(): transfer planet stockpiles between players
//   cmdMount     — mount(mnt=1): mount a crystal
//   cmdUnmount   — mount(mnt=0): dismount a crystal
//
// Internal helpers:
//   doLoad                  — shared impl for cmdLoad / cmdUnload
//   jettisonCheck           — validate jettison amount
//   unloadOntoAlienSector   — ground assault when unloading onto alien sector
//   landedOn                — landed_on(): ship docked on another specific ship
//   shipMaxCrystals         — crystal capacity (ship.Mount value)

import (
	"fmt"
	"strings"

	"github.com/gbgame/gb/internal/model"
	"github.com/gbgame/gb/internal/net"
)

// shipMaxCrystals returns the maximum crystal cargo capacity of a ship.
// Mirrors Max_crystals(s) in ships.h: uses ship.Mount (= Shipdata[type][AbilMount]).
func shipMaxCrystals(ship *model.Ship) int {
	return int(ship.Mount)
}

// landedOn returns true if ship s is docked on (orbiting) the given shipno.
// Mirrors landed_on(s, shipno) in load.c.
func landedOn(ship *model.Ship, shipNo uint32) bool {
	return ship.WhatOrbits == model.LevelShip && ship.DestShipNo == shipNo
}

// cmdLoad handles "load <ships> <commodity> [amount]" — load from planet/ship.
func (s *State) cmdLoad(p, g int, args []string, sess net.Responder) {
	s.doLoad(p, g, args, sess, false)
}

// cmdUnload handles "unload <ships> <commodity> [amount]" — unload to planet/ship.
// (In the C server this was "load" in mode=1, invoked as "unload" via a separate
// command entry in process_command.)
func (s *State) cmdUnload(p, g int, args []string, sess net.Responder) {
	s.doLoad(p, g, args, sess, true)
}

// doLoad implements load/unload for all commodities.
// mode=false → load (C: mode=0); mode=true → unload (C: mode=1).
// Mirrors load() in load.c.
func (s *State) doLoad(p, g int, args []string, sess net.Responder, unloadMode bool) {
	if len(args) < 2 {
		sess.Send("Load what?\n")
		return
	}

	race := s.raceByNum(p)
	if race == nil {
		sess.Send("Internal error: race not found.\n")
		return
	}

	dir := s.dirs[p-1][g]
	ships := s.shipsBySpec(p, g, args[0])

	for _, ship := range ships {
		if ship.Owner != uint8(p) || !ship.Alive {
			continue
		}
		if !ship.Active {
			sendf(sess, "%s is irradiated and inactive.\n", shipDisplayName(ship))
			continue
		}
		if !ship.Docked {
			sendf(sess, "%s is not landed or docked.\n", shipDisplayName(ship))
			continue
		}

		// AP check.
		if ship.WhatOrbits == model.LevelUniv {
			if !s.enoughAP(p, g, s.sdata.AP[p-1], 1) {
				continue
			}
		} else {
			star := s.stars[ship.StOrbits]
			if star == nil || !s.enoughAP(p, g, star.AP[p-1], 1) {
				continue
			}
		}

		commod := args[1][0]
		amt := 0
		if len(args) > 2 {
			fmt.Sscanf(args[2], "%d", &amt)
		}
		if unloadMode {
			amt = -amt
		}

		// VNs cannot unload.
		if amt < 0 && ship.Type == model.OtypeVN {
			sess.Send("You can't unload VNs.\n")
			continue
		}

		// Resolve transfer partner: planet or ship.
		var sh bool    // ship-to-ship
		var diff bool  // different owner
		var s2 *model.Ship
		var planet *model.Planet

		if ship.WhatDest == model.LevelPlan {
			// Landed on planet — must be in planet scope.
			sendf(sess, "%s at %d,%d\n", shipDisplayName(ship), ship.LandX, ship.LandY)
			if ship.StOrbits != dir.StarNum || ship.PNumOrbits != dir.PlanetNum {
				sess.Send("Change scope to the planet this ship is landed on.\n")
				continue
			}
			pl, err := s.store.GetPlanet(ship.StOrbits, ship.PNumOrbits)
			if err != nil || pl == nil {
				sess.Send("Planet data not found.\n")
				continue
			}
			planet = pl
		} else {
			// Docked to ship.
			if ship.DestShipNo == 0 {
				sendf(sess, "%s is not docked.\n", shipDisplayName(ship))
				continue
			}
			s2 = s.ships[ship.DestShipNo]
			if s2 == nil {
				sess.Send("Destination ship is bogus.\n")
				continue
			}
			if !s2.Alive || !(ship.WhatOrbits == model.LevelShip || s2.DestShipNo == ship.Ships) {
				// Ship it was docked with died or undocked.
				ship.Docked = false
				ship.WhatDest = model.LevelUniv
				s.saveShip(ship)
				sendf(sess, "%s is not docked.\n", shipDisplayName(s2))
				continue
			}
			if overloaded(s2) && s2.WhatOrbits == model.LevelShip {
				sendf(sess, "%s is overloaded!\n", shipDisplayName(s2))
				continue
			}
			sendf(sess, "%s docked with %s\n", shipDisplayName(ship), shipDisplayName(s2))
			sh = true
			if int(s2.Owner) != p {
				sendf(sess, "Player %d owns that ship.\n", s2.Owner)
				diff = true
			}
		}

		// Compute limits.
		uplim, lolim := 0, 0
		proc := false

		switch commod {
		case 'x', '&':
			if sh {
				if diff {
					uplim, lolim = 0, 0
				} else {
					uplim = min2(int(s2.Crystals), shipMaxCrystals(ship)-int(ship.Crystals))
					lolim = -min2(int(ship.Crystals), shipMaxCrystals(s2)-int(s2.Crystals))
				}
			} else {
				uplim = min2(int(planet.Info[p-1].Crystals), shipMaxCrystals(ship)-int(ship.Crystals))
				lolim = -int(ship.Crystals)
			}
			proc = true
		case 'c':
			if sh {
				if diff {
					uplim, lolim = 0, 0
				} else {
					uplim = min2(int(s2.Popn), shipMaxCrew(ship)-int(ship.Popn))
					lolim = -min2(int(ship.Popn), shipMaxCrew(s2)-int(s2.Popn))
				}
			} else {
				sect := planet.SectorAt(int(ship.LandX), int(ship.LandY))
				if sect == nil {
					sess.Send("Sector data not found.\n")
					continue
				}
				uplim = min2(int(sect.Popn), shipMaxCrew(ship)-int(ship.Popn))
				lolim = -int(ship.Popn)
			}
			proc = true
		case 'm':
			if sh {
				if diff {
					uplim, lolim = 0, 0
				} else {
					uplim = min2(int(s2.Troops), shipMaxMil(ship)-int(ship.Troops))
					lolim = -min2(int(ship.Troops), shipMaxMil(s2)-int(s2.Troops))
				}
			} else {
				sect := planet.SectorAt(int(ship.LandX), int(ship.LandY))
				if sect == nil {
					sess.Send("Sector data not found.\n")
					continue
				}
				uplim = min2(int(sect.Troops), shipMaxMil(ship)-int(ship.Troops))
				lolim = -int(ship.Troops)
			}
			proc = true
		case 'd':
			if sh {
				if diff {
					uplim = 0
				} else {
					uplim = min2(int(s2.Destruct), shipMaxDestruct(ship)-int(ship.Destruct))
				}
				lolim = -min2(int(ship.Destruct), shipMaxDestruct(s2)-int(s2.Destruct))
			} else {
				uplim = min2(int(planet.Info[p-1].Destruct), shipMaxDestruct(ship)-int(ship.Destruct))
				lolim = -int(ship.Destruct)
			}
			proc = true
		case 'f':
			if sh {
				if diff {
					uplim = 0
				} else {
					uplim = min2(int(s2.Fuel), int(shipMaxFuel(ship))-int(ship.Fuel))
				}
				lolim = -min2(int(ship.Fuel), int(shipMaxFuel(s2))-int(s2.Fuel))
			} else {
				uplim = min2(int(planet.Info[p-1].Fuel), int(shipMaxFuel(ship))-int(ship.Fuel))
				lolim = -int(ship.Fuel)
			}
			proc = true
		case 'r':
			if sh {
				if ship.Type == model.StypeShuttle && ship.WhatOrbits != model.LevelShip {
					if diff {
						uplim = 0
					} else {
						uplim = int(s2.Resource)
					}
				} else {
					if diff {
						uplim = 0
					} else {
						uplim = min2(int(s2.Resource), shipMaxResource(ship)-int(ship.Resource))
					}
				}
				if s2.Type == model.StypeShuttle && ship.WhatOrbits != model.LevelShip {
					lolim = -int(ship.Resource)
				} else {
					lolim = -min2(int(ship.Resource), shipMaxResource(s2)-int(s2.Resource))
				}
			} else {
				uplim = min2(int(planet.Info[p-1].Resource), shipMaxResource(ship)-int(ship.Resource))
				lolim = -int(ship.Resource)
			}
			proc = true
		default:
			sess.Send("No such commodity valid.\n")
			continue
		}

		if !proc {
			continue
		}

		if amt < lolim || amt > uplim {
			sendf(sess, "you can only transfer between %d and %d.\n", lolim, uplim)
			continue
		}

		// Default amount: max in the given direction.
		if amt == 0 {
			if unloadMode {
				amt = lolim
			} else {
				amt = uplim
			}
		}

		// Apply the transfer.
		switch commod {
		case 'c':
			if sh {
				s2.Popn -= uint16(amt)
				if !landedOn(ship, s2.Number) {
					s2.Mass -= float64(amt) * race.Mass
				}
				ship.Popn += uint16(amt)
				ship.Mass += float64(amt) * race.Mass
				sendf(sess, "crew complement of %s is now %d.\n", shipDisplayName(ship), ship.Popn)
			} else {
				sect := planet.SectorAt(int(ship.LandX), int(ship.LandY))
				if sect == nil {
					sess.Send("Sector data not found.\n")
					continue
				}
				if sect.Owner != 0 && int(sect.Owner) != p {
					sess.Send("That sector is already occupied by another player!\n")
					s.unloadOntoAlienSector(p, g, planet, ship, sect, moveCIV, -amt, sess)
					s.saveShip(ship)
					if err := s.store.PutPlanet(planet); err != nil {
						s.logError("PutPlanet", err)
					}
					continue
				}
				if sect.Popn == 0 && sect.Troops == 0 && amt < 0 {
					planet.Info[p-1].NumSectsOwned++
					planet.Info[p-1].MobPoints += uint64(sect.Mobilization)
					sect.Owner = uint8(p)
					sendf(sess, "sector %d,%d COLONIZED.\n", ship.LandX, ship.LandY)
				}
				sect.Popn -= uint16(amt)
				planet.Popn -= uint64(amt)
				planet.Info[p-1].Popn -= uint32(amt)
				if sect.Popn == 0 && sect.Troops == 0 {
					planet.Info[p-1].NumSectsOwned--
					planet.Info[p-1].MobPoints -= uint64(sect.Mobilization)
					sect.Owner = 0
					sendf(sess, "sector %d,%d evacuated.\n", ship.LandX, ship.LandY)
				}
				ship.Popn += uint16(amt)
				ship.Mass += float64(amt) * race.Mass
				sendf(sess, "crew complement of %s is now %d.\n", shipDisplayName(ship), ship.Popn)
				if err := s.store.PutPlanet(planet); err != nil {
					s.logError("PutPlanet", err)
				}
			}
		case 'm':
			if sh {
				s2.Troops -= uint16(amt)
				if !landedOn(ship, s2.Number) {
					s2.Mass -= float64(amt) * race.Mass
				}
				ship.Troops += uint16(amt)
				ship.Mass += float64(amt) * race.Mass
				sendf(sess, "troop complement of %s is now %d.\n", shipDisplayName(ship), ship.Troops)
			} else {
				sect := planet.SectorAt(int(ship.LandX), int(ship.LandY))
				if sect == nil {
					sess.Send("Sector data not found.\n")
					continue
				}
				if sect.Owner != 0 && int(sect.Owner) != p {
					sess.Send("That sector is already occupied by another player!\n")
					s.unloadOntoAlienSector(p, g, planet, ship, sect, moveMIL, -amt, sess)
					s.saveShip(ship)
					if err := s.store.PutPlanet(planet); err != nil {
						s.logError("PutPlanet", err)
					}
					continue
				}
				if sect.Popn+sect.Troops == 0 && amt < 0 {
					planet.Info[p-1].NumSectsOwned++
					planet.Info[p-1].MobPoints += uint64(sect.Mobilization)
					sect.Owner = uint8(p)
					sendf(sess, "sector %d,%d OCCUPIED.\n", ship.LandX, ship.LandY)
				}
				sect.Troops -= uint16(amt)
				planet.Troops -= uint64(amt)
				planet.Info[p-1].Troops -= uint32(amt)
				if sect.Troops+sect.Popn == 0 {
					planet.Info[p-1].NumSectsOwned--
					planet.Info[p-1].MobPoints -= uint64(sect.Mobilization)
					sect.Owner = 0
					sendf(sess, "sector %d,%d evacuated.\n", ship.LandX, ship.LandY)
				}
				ship.Troops += uint16(amt)
				ship.Mass += float64(amt) * race.Mass
				sendf(sess, "troop complement of %s is now %d.\n", shipDisplayName(ship), ship.Troops)
				if err := s.store.PutPlanet(planet); err != nil {
					s.logError("PutPlanet", err)
				}
			}
		case 'd':
			if sh {
				s2.Destruct -= uint16(amt)
				if !landedOn(ship, s2.Number) {
					s2.Mass -= float64(amt) * s.cfg.MassDestruct
				}
			} else {
				planet.Info[p-1].Destruct -= uint16(amt)
				if err := s.store.PutPlanet(planet); err != nil {
					s.logError("PutPlanet", err)
				}
			}
			s.rcvDestruct(ship, amt)
			sendf(sess, "%d destruct transferred.\n", amt)
			if shipMaxCrew(ship) == 0 {
				sendf(sess, "\n%s ", shipDisplayName(ship))
				if ship.Destruct > 0 {
					sess.Send("now boobytrapped.\n")
				} else {
					sess.Send("no longer boobytrapped.\n")
				}
			}
		case 'x', '&':
			if sh {
				s2.Crystals -= uint16(amt)
			} else {
				planet.Info[p-1].Crystals -= uint16(amt)
				if err := s.store.PutPlanet(planet); err != nil {
					s.logError("PutPlanet", err)
				}
			}
			ship.Crystals += uint16(amt)
			sendf(sess, "%d crystal(s) transferred.\n", amt)
		case 'f':
			if sh {
				s2.Fuel -= float64(amt)
				if !landedOn(ship, s2.Number) {
					s2.Mass -= float64(amt) * s.cfg.MassFuel
				}
			} else {
				planet.Info[p-1].Fuel -= uint16(amt)
				if err := s.store.PutPlanet(planet); err != nil {
					s.logError("PutPlanet", err)
				}
			}
			s.rcvFuel(ship, float64(amt))
			sendf(sess, "%d fuel transferred.\n", amt)
		case 'r':
			if sh {
				s2.Resource -= uint16(amt)
				if !landedOn(ship, s2.Number) {
					s2.Mass -= float64(amt) * s.cfg.MassResource
				}
			} else {
				planet.Info[p-1].Resource -= uint16(amt)
				if err := s.store.PutPlanet(planet); err != nil {
					s.logError("PutPlanet", err)
				}
			}
			s.rcvResource(ship, amt)
			sendf(sess, "%d resources transferred.\n", amt)
		}

		// Save the docked ship if ship-to-ship.
		if sh {
			s.saveShip(s2)
		}

		// Auto-transporter: beam cargo to target if this is a TransDev with target set.
		if ship.Type == model.OtypeTransDev && ship.Special.Transport.Target != 0 && ship.On {
			s.doTransporter(race, g, ship, sess)
		}

		s.saveShip(ship)
		s.deductAPs(p, g, 1, int(ship.StOrbits), ship.WhatOrbits == model.LevelUniv)
	}
}

// cmdJettison handles "jettison <ships> <commodity> [amount]".
// Discards cargo from a ship in space (cannot be used while landed).
// Mirrors jettison() in load.c.
func (s *State) cmdJettison(p, g int, args []string, sess net.Responder) {
	if len(args) < 2 {
		sess.Send("Jettison what?\n")
		return
	}

	race := s.raceByNum(p)
	if race == nil {
		sess.Send("Internal error: race not found.\n")
		return
	}

	ships := s.shipsBySpec(p, g, args[0])
	for _, ship := range ships {
		if ship.Owner != uint8(p) || !ship.Alive {
			continue
		}
		if landed(ship) {
			sess.Send("Ship is landed, cannot jettison.\n")
			continue
		}
		if !ship.Active {
			sendf(sess, "%s is irradiated and inactive.\n", shipDisplayName(ship))
			continue
		}
		if ship.WhatOrbits == model.LevelUniv {
			if !s.enoughAP(p, g, s.sdata.AP[p-1], 1) {
				continue
			}
		} else {
			star := s.stars[ship.StOrbits]
			if star == nil || !s.enoughAP(p, g, star.AP[p-1], 1) {
				continue
			}
		}

		amt := 0
		if len(args) > 2 {
			fmt.Sscanf(args[2], "%d", &amt)
		}

		commod := args[1][0]
		modified := false

		switch commod {
		case 'x':
			if amt, ok := s.jettisonCheck(p, g, amt, int(ship.Crystals), sess); ok {
				ship.Crystals -= uint16(amt)
				sendf(sess, "%d crystal%s jettisoned.\n", amt, pluralS(amt))
				modified = true
			}
		case 'c':
			if amt, ok := s.jettisonCheck(p, g, amt, int(ship.Popn), sess); ok {
				ship.Popn -= uint16(amt)
				ship.Mass -= float64(amt) * race.Mass
				if amt == 1 {
					sendf(sess, "%d crew hurls itself into deep space.\n", amt)
				} else {
					sendf(sess, "%d crew hurl themselves into deep space.\n", amt)
				}
				sendf(sess, "Complement of %s is now %d.\n", shipDisplayName(ship), ship.Popn)
				modified = true
			}
		case 'm':
			if amt, ok := s.jettisonCheck(p, g, amt, int(ship.Troops), sess); ok {
				if amt == 1 {
					sendf(sess, "%d military hurls itself into deep space.\n", amt)
				} else {
					sendf(sess, "%d military hurl themselves into deep space.\n", amt)
				}
				sendf(sess, "Complement of %s is now %d.\n", shipDisplayName(ship), ship.Troops-uint16(amt))
				ship.Troops -= uint16(amt)
				ship.Mass -= float64(amt) * race.Mass
				modified = true
			}
		case 'd':
			if amt, ok := s.jettisonCheck(p, g, amt, int(ship.Destruct), sess); ok {
				s.useDestruct(ship, amt)
				sendf(sess, "%d destruct jettisoned.\n", amt)
				if shipMaxCrew(ship) == 0 {
					sendf(sess, "\n%s ", shipDisplayName(ship))
					if ship.Destruct > 0 {
						sess.Send("still boobytrapped.\n")
					} else {
						sess.Send("no longer boobytrapped.\n")
					}
				}
				modified = true
			}
		case 'f':
			if amt, ok := s.jettisonCheck(p, g, amt, int(ship.Fuel), sess); ok {
				s.useFuel(ship, float64(amt))
				sendf(sess, "%d fuel jettisoned.\n", amt)
				modified = true
			}
		case 'r':
			if amt, ok := s.jettisonCheck(p, g, amt, int(ship.Resource), sess); ok {
				s.useResource(ship, amt)
				sendf(sess, "%d resources jettisoned.\n", amt)
				modified = true
			}
		default:
			sess.Send("No such commodity valid.\n")
			continue
		}

		if modified {
			s.saveShip(ship)
		}
	}
}

// jettisonCheck validates and normalises a jettison amount.
// Returns (finalAmt, true) on success, (0, false) on failure.
func (s *State) jettisonCheck(p, g, amt, max int, sess net.Responder) (int, bool) {
	if amt == 0 {
		amt = max
	}
	if amt < 0 {
		sess.Send("Nice try.\n")
		return 0, false
	}
	if amt > max {
		sendf(sess, "You can jettison at most %d\n", max)
		return 0, false
	}
	return amt, true
}

// cmdDump handles "dump <player> [place ...]".
// Transfers star/planet exploration data to another player.
// Mirrors dump() in load.c.
func (s *State) cmdDump(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]

	star := s.stars[dir.StarNum]
	if star == nil {
		sess.Send("You are not in a star system.\n")
		return
	}
	if !s.enoughAP(p, g, star.AP[p-1], 1) {
		return
	}

	if len(args) < 1 {
		sess.Send("Dump to whom?\n")
		return
	}

	target := s.getPlayerArg(args[0])
	if target == 0 {
		sess.Send("No such player.\n")
		return
	}

	srcRace := s.raceByNum(p)
	dstRace := s.raceByNum(target)
	if srcRace == nil || dstRace == nil {
		sess.Send("No such player.\n")
		return
	}
	if dstRace.Guest {
		sess.Send("Cheater!\n")
		return
	}
	if srcRace.Guest {
		sess.Send("Cheater!\n")
		return
	}
	if g != 0 {
		sess.Send("Only leaders are allowed to use dump.\n")
		return
	}

	// Transfer star and planet exploration bits.
	// If no place args: transfer all known stars.
	// Otherwise: transfer each named star.
	starNums := []int{}
	if len(args) < 2 {
		for i, st := range s.stars {
			if st != nil && st.IsExplored(p) {
				starNums = append(starNums, i)
			}
		}
	} else {
		for _, arg := range args[1:] {
			// Resolve star by name or number.
			sn := -1
			for i, st := range s.stars {
				if st != nil && strings.EqualFold(st.Name, arg) {
					sn = i
					break
				}
			}
			if sn < 0 {
				fmt.Sscanf(arg, "%d", &sn)
				sn-- // convert 1-based input to 0-based
			}
			if sn >= 0 && sn < len(s.stars) && s.stars[sn] != nil {
				starNums = append(starNums, sn)
			}
		}
	}

	for _, sn := range starNums {
		st := s.stars[sn]
		if !st.IsExplored(p) {
			continue
		}
		st.SetExplored(target)
		if err := s.store.PutStar(st); err != nil {
			s.logError("PutStar", err)
			continue
		}
		for j := 0; j < int(st.NumPlanets); j++ {
			pl, err := s.store.GetPlanet(uint8(sn), uint8(j))
			if err != nil || pl == nil {
				continue
			}
			if pl.Info[p-1].Explored {
				pl.Info[target-1].Explored = true
				if err := s.store.PutPlanet(pl); err != nil {
					s.logError("PutPlanet", err)
				}
			}
		}
	}

	s.deductAPs(p, g, 1, int(dir.StarNum), false)

	s.warnRace(target, fmt.Sprintf("%s [%d] has given you exploration data.\n", srcRace.Name, p))
	sess.Send("Exploration Data transferred.\n")
}

// cmdTransfer handles "transfer <player> <commodity> <amount>".
// Moves planet-level stockpiles from the current player to another.
// Mirrors transfer() in load.c.
func (s *State) cmdTransfer(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]

	if dir.Level != model.LevelPlan {
		sess.Send("You need to be in planet scope to do this.\n")
		return
	}

	star := s.stars[dir.StarNum]
	if star == nil || !s.enoughAP(p, g, star.AP[p-1], 1) {
		return
	}

	if len(args) < 3 {
		sess.Send("Usage: transfer <player> <commodity> <amount>\n")
		return
	}

	target := s.getPlayerArg(args[0])
	if target == 0 {
		sess.Send("No such player.\n")
		return
	}

	planet, err := s.store.GetPlanet(dir.StarNum, dir.PlanetNum)
	if err != nil || planet == nil {
		sess.Send("Planet data not found.\n")
		return
	}

	commod := args[1][0]
	give := 0
	fmt.Sscanf(args[2], "%d", &give)

	if give < 0 {
		sess.Send("You must specify a positive amount.\n")
		return
	}

	location := fmt.Sprintf("%s/%s:", star.Name, star.PlanetNames[dir.PlanetNum])

	switch commod {
	case 'r':
		if give > int(planet.Info[p-1].Resource) {
			sendf(sess, "You don't have %d on this planet.\n", give)
		} else {
			planet.Info[p-1].Resource -= uint16(give)
			planet.Info[target-1].Resource += uint16(give)
			msg := fmt.Sprintf("%s %d resources transferred from player %d to player #%d\n", location, give, p, target)
			sess.Send(msg)
			s.warnRace(target, msg)
		}
	case 'x', '&':
		if give > int(planet.Info[p-1].Crystals) {
			sendf(sess, "You don't have %d on this planet.\n", give)
		} else {
			planet.Info[p-1].Crystals -= uint16(give)
			planet.Info[target-1].Crystals += uint16(give)
			msg := fmt.Sprintf("%s %d crystal(s) transferred from player %d to player #%d\n", location, give, p, target)
			sess.Send(msg)
			s.warnRace(target, msg)
		}
	case 'f':
		if give > int(planet.Info[p-1].Fuel) {
			sendf(sess, "You don't have %d fuel on this planet.\n", give)
		} else {
			planet.Info[p-1].Fuel -= uint16(give)
			planet.Info[target-1].Fuel += uint16(give)
			msg := fmt.Sprintf("%s %d fuel transferred from player %d to player #%d\n", location, give, p, target)
			sess.Send(msg)
			s.warnRace(target, msg)
		}
	case 'd':
		if give > int(planet.Info[p-1].Destruct) {
			sendf(sess, "You don't have %d destruct on this planet.\n", give)
		} else {
			planet.Info[p-1].Destruct -= uint16(give)
			planet.Info[target-1].Destruct += uint16(give)
			msg := fmt.Sprintf("%s %d destruct transferred from player %d to player #%d\n", location, give, p, target)
			sess.Send(msg)
			s.warnRace(target, msg)
		}
	default:
		sess.Send("What?\n")
	}

	if err := s.store.PutPlanet(planet); err != nil {
		s.logError("PutPlanet", err)
	}
	s.deductAPs(p, g, 1, int(dir.StarNum), false)
}

// cmdMount handles "mount <ships>" — mount a crystal on a ship.
// Mirrors mount(mnt=1) in load.c.
func (s *State) cmdMount(p, g int, args []string, sess net.Responder) {
	s.doMount(p, g, args, sess, true)
}

// cmdUnmount handles "unmount <ships>" — dismount a crystal from a ship.
// Mirrors mount(mnt=0) in load.c.
func (s *State) cmdUnmount(p, g int, args []string, sess net.Responder) {
	s.doMount(p, g, args, sess, false)
}

// doMount implements crystal mount/dismount.
// Mirrors mount() in load.c.
func (s *State) doMount(p, g int, args []string, sess net.Responder, mnt bool) {
	if len(args) < 1 {
		sess.Send("Mount which ship?\n")
		return
	}

	ships := s.shipsBySpec(p, g, args[0])
	for _, ship := range ships {
		if !ship.Alive || int(ship.Owner) != p {
			continue
		}
		if ship.Mount == 0 {
			sess.Send("This ship is not equipped with a crystal mount.\n")
			continue
		}
		if ship.Mounted && mnt {
			sess.Send("You already have a crystal mounted.\n")
			continue
		} else if !ship.Mounted && !mnt {
			sess.Send("You don't have a crystal mounted.\n")
			continue
		} else if !ship.Mounted && mnt {
			if ship.Crystals == 0 {
				sess.Send("You have no crystals on board.\n")
				continue
			}
			ship.Mounted = true
			ship.Crystals--
			sess.Send("Mounted.\n")
		} else if ship.Mounted && !mnt {
			if int(ship.Crystals) >= shipMaxCrystals(ship) {
				sess.Send("You can't dismount the crystal. Max allowed already on board.\n")
				continue
			}
			ship.Mounted = false
			ship.Crystals++
			sess.Send("Dismounted.\n")
			if ship.HyperDrive.Charge != 0 || ship.HyperDrive.Ready {
				ship.HyperDrive.Charge = 0
				ship.HyperDrive.Ready = false
				sess.Send("Discharged.\n")
			}
			if ship.Laser != 0 && ship.FireLaser != 0 {
				ship.FireLaser = 0
				sess.Send("Laser deactivated.\n")
			}
		} else {
			sess.Send("Weird error in 'mount'.\n")
			continue
		}
		s.saveShip(ship)
	}
}

// doTransporter beams all cargo from ship s (an OTYPE_TRANSDEV) to its target
// TransDev. Both devices must be landed and undamaged.
// Mirrors do_transporter() in load.c.
func (s *State) doTransporter(race *model.Race, g int, ship *model.Ship, sess net.Responder) {
	p := int(ship.Owner)

	if !landed(ship) {
		sess.Send("Origin ship not landed.\n")
		return
	}
	dir := s.dirs[p-1][g]
	if ship.StOrbits != dir.StarNum || ship.PNumOrbits != dir.PlanetNum {
		sess.Send("Change scope to the planet the ship is landed on!\n")
		return
	}
	if ship.Damage > 0 {
		sess.Send("Origin device is damaged.\n")
		return
	}

	target := ship.Special.Transport.Target
	if target == 0 {
		return
	}
	s2 := s.ships[uint32(target)]
	if s2 == nil {
		sess.Send("The hopper seems to be blocked.\n")
		return
	}
	if !s2.Alive || s2.Type != model.OtypeTransDev || !s2.On {
		sess.Send("The target device is not receiving.\n")
		return
	}
	if !landed(s2) {
		sess.Send("Target ship not landed.\n")
		return
	}
	if s2.Damage > 0 {
		sess.Send("Target device is damaged.\n")
		return
	}

	sess.Send("Zap\007!\n")

	var bufr, buff2, bufd, bufc, bufx string

	if ship.Resource > 0 {
		s.rcvResource(s2, int(ship.Resource))
		sendf(sess, "%d resources transferred.\n", ship.Resource)
		bufr = fmt.Sprintf("%d Resources\n", ship.Resource)
		s.useResource(ship, int(ship.Resource))
	}
	if ship.Fuel > 0 {
		s.rcvFuel(s2, ship.Fuel)
		sendf(sess, "%g fuel transferred.\n", ship.Fuel)
		buff2 = fmt.Sprintf("%g Fuel\n", ship.Fuel)
		s.useFuel(ship, ship.Fuel)
	}
	if ship.Destruct > 0 {
		s.rcvDestruct(s2, int(ship.Destruct))
		sendf(sess, "%d destruct transferred.\n", ship.Destruct)
		bufd = fmt.Sprintf("%d Destruct\n", ship.Destruct)
		s.useDestruct(ship, int(ship.Destruct))
	}
	if ship.Popn > 0 {
		s2.Mass += float64(ship.Popn) * race.Mass
		s2.Popn += ship.Popn
		sendf(sess, "%d population transferred.\n", ship.Popn)
		if race.Metamorph {
			bufc = fmt.Sprintf("%d tons of biomass\n", ship.Popn)
		} else {
			bufc = fmt.Sprintf("%d population\n", ship.Popn)
		}
		ship.Mass -= float64(ship.Popn) * race.Mass
		ship.Popn = 0
	}
	if ship.Crystals > 0 {
		s2.Crystals += ship.Crystals
		sendf(sess, "%d crystal(s) transferred.\n", ship.Crystals)
		bufx = fmt.Sprintf("%d crystal(s)\n", ship.Crystals)
		ship.Crystals = 0
	}

	if int(s2.Owner) != int(ship.Owner) {
		telegram := fmt.Sprintf("Audio-vibatory-physio-molecular transport device #%s gave your ship %s the following:\n%s%s%s%s%s",
			shipDisplayName(ship), shipDisplayName(s2), bufr, bufd, buff2, bufc, bufx)
		s.warn(int(s2.Owner), int(s2.Governor), telegram)
	}

	s.saveShip(s2)
}

// unloadOntoAlienSector conducts a ground assault when unloading crew/troops
// onto a sector owned by another player.
// Mirrors unload_onto_alien_sector() in load.c.
func (s *State) unloadOntoAlienSector(
	p, g int,
	planet *model.Planet,
	ship *model.Ship,
	sect *model.Sector,
	what int, // moveCIV or moveMIL
	people int,
	sess net.Responder,
) {
	if people <= 0 {
		sess.Send("You have to unload to assault alien sectors.\n")
		return
	}

	race := s.raceByNum(p)
	alienOwner := int(sect.Owner)
	alien := s.raceByNum(alienOwner)
	if race == nil || alien == nil {
		return
	}

	// Record ground assault for turn-end reporting.
	starNum := int(s.dirs[p-1][g].StarNum)
	if p >= 1 && p <= model.MaxPlayers && alienOwner >= 1 && alienOwner <= model.MaxPlayers && starNum < model.NumStars {
		s.groundAssaults[p-1][alienOwner-1][starNum]++
	}

	// Both races learn about each other.
	if int(alien.Translate[p-1])+5 > 100 {
		alien.Translate[p-1] = 100
	} else {
		alien.Translate[p-1] += 5
	}
	if int(race.Translate[alienOwner-1])+5 > 100 {
		race.Translate[alienOwner-1] = 100
	} else {
		race.Translate[alienOwner-1] += 5
	}

	oldOwner := alienOwner
	star := s.stars[s.dirs[p-1][g].StarNum]
	oldGov := 0
	if star != nil {
		oldGov = int(star.Governor[alienOwner-1])
	}

	if what == moveCIV {
		ship.Popn -= uint16(people)
	} else {
		ship.Troops -= uint16(people)
	}
	ship.Mass -= float64(people) * race.Mass

	if what == moveCIV {
		sendf(sess, "%d civ unloaded...\n", people)
	} else {
		sendf(sess, "%d mil unloaded...\n", people)
	}
	sendf(sess, "Crew compliment %d civ  %d mil\n", ship.Popn, ship.Troops)

	if what == moveCIV {
		sendf(sess, "%d civ assault %d civ/%d mil\n", people, sect.Popn, sect.Troops)
	} else {
		sendf(sess, "%d mil assault %d civ/%d mil\n", people, sect.Popn, sect.Troops)
	}

	oldpopn := people
	old2popn := int(sect.Popn)
	old3popn := int(sect.Troops)

	defense := defenseData[sect.Condition]

	var astrength, dstrength float64
	var casualties, casualties2, casualties3 int

	groundAttack(race, alien, &people, what,
		&sect.Popn, &sect.Troops,
		int(ship.Armor), defense,
		1.0-float64(ship.Damage)/100.0, float64(alien.Likes[sect.Condition]),
		&astrength, &dstrength,
		&casualties, &casualties2, &casualties3)

	sendf(sess, "Attack: %.2f   Defense: %.2f.\n", astrength, dstrength)

	starName := ""
	planetName := ""
	if star != nil {
		starName = star.Name
		if int(s.dirs[p-1][g].PlanetNum) < len(star.PlanetNames) {
			planetName = star.PlanetNames[s.dirs[p-1][g].PlanetNum]
		}
	}

	var outcome string
	if sect.Popn+sect.Troops == 0 { // victory
		absorbed := 0
		if race.Absorb {
			absorbed = intRand(0, old2popn+old3popn)
			sendf(sess, "%d alien bodies absorbed.\n", absorbed)
			s.warn(oldOwner, oldGov, fmt.Sprintf("Metamorphs have absorbed %d bodies!!!\n", absorbed))
		}
		if what == moveCIV {
			sect.Popn = uint16(people + absorbed)
		} else {
			sect.Popn = uint16(absorbed)
			sect.Troops = uint16(people)
		}
		sect.Owner = uint8(p)
		adjustMorale(race, alien, int(alien.Fighters))

		outcome = "VICTORY"
		sess.Send("VICTORY! The sector is yours!\n")
		if people > 0 {
			if what == moveCIV {
				sendf(sess, "%d civilians move in.\n", people)
			} else {
				sendf(sess, "%d troops move in.\n", people)
			}
		}
		planet.Info[p-1].NumSectsOwned++
		planet.Info[p-1].MobPoints += uint64(sect.Mobilization)
		planet.Info[oldOwner-1].NumSectsOwned--
		planet.Info[oldOwner-1].MobPoints -= uint64(sect.Mobilization)
	} else { // retreat
		absorbed := 0
		if alien.Absorb {
			absorbed = intRand(0, oldpopn-people)
			sendf(sess, "%d alien bodies absorbed.\n", absorbed)
			s.warn(p, g, fmt.Sprintf("Metamorphs have absorbed %d bodies!!!\n", absorbed))
			sect.Popn += uint16(absorbed)
		}
		if what == moveCIV {
			sendf(sess, "Loading %d civ\n", people)
			ship.Popn += uint16(people)
		} else {
			sendf(sess, "Loading %d mil\n", people)
			ship.Troops += uint16(people)
		}
		ship.Mass += float64(people) * race.Mass
		adjustMorale(alien, race, int(race.Fighters))

		outcome = "DEFEAT"
		sess.Send("The invasion was repulsed; try again.\n")
	}

	whatStr := "civ"
	if what == moveMIL {
		whatStr = "mil"
	}
	telegramMsg := fmt.Sprintf("/%s/%s: %s [%d] %s assaults %s [%d] %c(%d,%d) %s\n",
		starName, planetName,
		race.Name, p, shipDisplayName(ship),
		alien.Name, alien.PlayerNum,
		sectorSymbol(int(sect.Condition)), ship.LandX, ship.LandY,
		outcome)

	if sect.Owner == uint8(p) {
		telegramMsg += "Sector CAPTURED!\n"
	} else {
		telegramMsg += "You fought them off!\n"
	}

	if sect.Popn+sect.Troops == 0 && people == 0 {
		telegramMsg += "You killed all of them!\n"
		if int(race.Translate[oldOwner-1])+5 > 100 {
			race.Translate[oldOwner-1] = 100
		} else {
			race.Translate[oldOwner-1] += 5
		}
	}
	if people == 0 {
		sess.Send("Oh no! They killed your party to the last man!\n")
		if int(alien.Translate[p-1])+5 > 100 {
			alien.Translate[p-1] = 100
		} else {
			alien.Translate[p-1] += 5
		}
	}

	casLine := fmt.Sprintf("Casualties: You: %d %s, Them: %d civ/%d mil\n",
		casualties, whatStr, casualties2, casualties3)
	sess.Send(casLine)

	telegramMsg += fmt.Sprintf("Casualties: You: %d civ/%d mil, Them: %d %s\n",
		casualties2, casualties3, casualties, whatStr)
	s.warn(oldOwner, oldGov, telegramMsg)

	if err := s.store.PutRace(alien); err != nil {
		s.logError("PutRace alien", err)
	}
	if err := s.store.PutRace(race); err != nil {
		s.logError("PutRace race", err)
	}

}

// ---- Helpers ----------------------------------------------------------------

// min2 returns the smaller of two ints.
// (named min2 to avoid collision with Go 1.21 builtin min)
func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// pluralS returns "s" if n != 1, else "".
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// shipMaxMil returns the maximum troop capacity.
// In C: Max_mil(s) = s->maxmil; in Go: MaxCrew - current popn for non-factories.
func shipMaxMil(ship *model.Ship) int {
	return int(ship.MaxCrew) - int(ship.Popn)
}

// logError logs a non-fatal store error.
func (s *State) logError(ctx string, err error) {
	if err != nil {
		_ = fmt.Sprintf("store error [%s]: %v", ctx, err) // would use log.Printf in production
	}
}
