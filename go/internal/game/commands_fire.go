package game

// commands_fire.go implements player-directed combat commands.
//
// C source ported: fire.c
//   cmdFire     — ship vs ship        (fire())
//   cmdBombard  — ship vs planet      (bombard())
//   cmdDefend   — planet vs ship      (defend(), only when Defense feature on)
//   cmdDetonate — manually detonate a mine (detonate())

import (
	"fmt"
	"strconv"

	"github.com/gbgame/gb/internal/model"
	"github.com/gbgame/gb/internal/net"
)

// cmdFire fires one or more ships at a single target ship.
//
// Syntax: fire <ship-spec> <target#> [<strength>]
//         cew  <ship-spec> <target#>          (CEW variant)
//
// Mirrors fire() in fire.c.
func (s *State) cmdFire(p, g int, args []string, sess net.Responder) {
	s.doFire(p, g, args, sess, false)
}

// cmdCEW fires CEW (crystal energy weapon) at a target ship.
// Syntax: cew <ship-spec> <target#>
func (s *State) cmdCEW(p, g int, args []string, sess net.Responder) {
	s.doFire(p, g, args, sess, true)
}

func (s *State) doFire(p, g int, args []string, sess net.Responder, cew bool) {
	if len(args) < 2 {
		sess.Send("Syntax: 'fire <ship> <target> [<strength>]'.\n")
		return
	}

	// Parse target ship number (optional leading '#').
	targetArg := args[1]
	if len(targetArg) > 0 && targetArg[0] == '#' {
		targetArg = targetArg[1:]
	}
	toShipNo, err := strconv.Atoi(targetArg)
	if err != nil || toShipNo <= 0 {
		sess.Send("Bad ship number.\n")
		return
	}

	// Optional explicit strength.
	strengthArg := -1
	if len(args) >= 3 {
		if n, err2 := strconv.Atoi(args[2]); err2 == nil {
			strengthArg = n
		}
	}

	ships := s.shipsBySpec(p, g, args[0])
	for _, from := range ships {
		fromShipNo := int(from.Number)

		if !authorized(g, from) {
			continue
		}
		if !from.Active {
			sendf(sess, "%s is irradiated and inactive.\n", shipDisplayName(from))
			continue
		}

		// AP check.
		if from.WhatOrbits == model.LevelUniv {
			if !s.enoughAP(p, g, s.sdata.AP[p-1], 1) {
				continue
			}
		} else {
			star := s.stars[from.StOrbits]
			if star == nil || !s.enoughAP(p, g, star.AP[p-1], 1) {
				continue
			}
		}

		if cew {
			if from.CEW == 0 {
				sess.Send("That ship is not equipped to fire CEWs.\n")
				continue
			}
			if !from.Mounted {
				sess.Send("You need to have a crystal mounted to fire CEWs.\n")
				continue
			}
		}

		if toShipNo == fromShipNo {
			sess.Send("Get real.\n")
			continue
		}

		to, ok := s.ships[uint32(toShipNo)]
		if !ok || to == nil {
			continue
		}

		// Save defender's retaliation strength and make a copy for return-fire.
		var retal int
		s.checkRetalStrength(to, &retal)
		dummy := *to // copy for retaliation shot

		// AFV restriction: both must be landed on the same planet and adjacent.
		if from.Type == model.OtypeAFV {
			if !landed(from) {
				sendf(sess, "%s isn't landed on a planet!\n", shipDisplayName(from))
				continue
			}
			if !landed(to) {
				sendf(sess, "%s isn't landed on a planet!\n", shipDisplayName(to))
				continue
			}
		}
		if landed(from) && landed(to) {
			if from.StOrbits != to.StOrbits || from.PNumOrbits != to.PNumOrbits {
				sess.Send("Landed ships can only attack other landed ships if they are on the same planet!\n")
				continue
			}
			planet := s.planet(int(from.StOrbits), int(from.PNumOrbits))
			if planet == nil {
				continue
			}
			if !adjacent(int(from.LandX), int(from.LandY),
				int(to.LandX), int(to.LandY), planet) {
				sess.Send("You are not adjacent to your target!\n")
				continue
			}
		}

		strength := 0
		if cew {
			if float64(from.Fuel) < float64(from.CEW) {
				sendf(sess, "You need %d fuel to fire CEWs.\n", from.CEW)
				continue
			}
			if landed(from) || landed(to) {
				sess.Send("CEWs cannot originate from or targeted to ships landed on planets.\n")
				continue
			}
			sendf(sess, "CEW strength %d.\n", from.CEW)
			strength = int(from.CEW) / 2
		} else {
			var maxStrength int
			s.checkRetalStrength(from, &maxStrength)
			if strengthArg >= 0 {
				strength = strengthArg
			} else {
				s.checkRetalStrength(from, &strength)
			}
			if strength > maxStrength {
				strength = maxStrength
				label := "Guns"
				if laserOn(from) {
					label = "Laser strength"
				}
				sendf(sess, "%s set to %d\n", label, strength)
			}
		}

		// Crystal overload check for lasers and CEWs.
		if laserOn(from) || cew {
			cewVal := 0
			if cew {
				cewVal = 1
			}
			s.checkOverload(from, cewVal, &strength)
		}

		if strength <= 0 {
			sess.Send("No attack.\n")
			s.ships[uint32(fromShipNo)] = from
			continue
		}

		var longMsg, shortMsg string
		damage := s.shootShipToShip(from, to, strength, boolInt(cew), 0, &longMsg, &shortMsg)

		if damage < 0 {
			sess.Send("Illegal attack.\n")
			continue
		}

		// Expend weapons.
		if laserOn(from) || cew {
			s.useFuel(from, 2.0*float64(strength))
		} else {
			s.useDestruct(from, strength)
		}

		if !to.Alive {
			s.post(model.NewsCombat, shortMsg)
		}
		s.notifyStar(p, g, int(to.Owner), int(from.StOrbits), shortMsg)
		s.warn(int(to.Owner), int(to.Governor), longMsg)
		sess.Send(longMsg)

		// Defender self-retaliates if protect.self and damage was inflicted.
		if retal > 0 && damage > 0 && to.Protect.Self {
			strength = retal
			if laserOn(to) {
				s.checkOverload(to, 0, &strength)
			}
			var lmsg2, smsg2 string
			if dmg2 := s.shootShipToShip(&dummy, from, strength, 0, 1, &lmsg2, &smsg2); dmg2 >= 0 {
				if laserOn(to) {
					s.useFuel(to, 2.0*float64(strength))
				} else {
					s.useDestruct(to, strength)
				}
				if !from.Alive {
					s.post(model.NewsCombat, smsg2)
				}
				s.notifyStar(p, g, int(to.Owner), int(from.StOrbits), smsg2)
				sess.Send(lmsg2)
				s.warn(int(to.Owner), int(to.Governor), lmsg2)
			}
		}

		// Protecting ships retaliate if damage was inflicted and attacker is not an AFV.
		if damage > 0 && from.Alive && from.Type != model.OtypeAFV {
			s.protectorRetaliate(p, g, from, to, uint16(toShipNo), sess)
		}

		s.ships[uint32(fromShipNo)] = from
		s.ships[uint32(toShipNo)] = to
		s.deductAPs(p, g, 1, int(from.StOrbits), false)
	}
}

// protectorRetaliate fires all ships that are protecting toShip back at fromShip.
// Mirrors the "protecting ships retaliate" loop in fire() in fire.c.
func (s *State) protectorRetaliate(p, g int, from, to *model.Ship, toShipNo uint16, sess net.Responder) {
	var firstShip uint32
	if to.WhatOrbits == model.LevelStar {
		if star := s.stars[to.StOrbits]; star != nil {
			firstShip = star.Ships
		}
	} else if to.WhatOrbits == model.LevelPlan {
		if pl := s.planet(int(to.StOrbits), int(to.PNumOrbits)); pl != nil {
			firstShip = pl.Ships
		}
	}

	for sh := firstShip; sh != 0 && from.Alive; {
		ship, ok := s.ships[sh]
		if !ok {
			break
		}
		next := ship.NextShip

		if ship.Protect.On &&
			ship.Protect.Ship == toShipNo &&
			uint32(sh) != uint32(from.Number) &&
			sh != uint32(toShipNo) &&
			ship.Alive && ship.Active {

			var strength int
			s.checkRetalStrength(ship, &strength)
			if laserOn(ship) {
				s.checkOverload(ship, 0, &strength)
			}

			var lmsg, smsg string
			if dmg := s.shootShipToShip(ship, from, strength, 0, 0, &lmsg, &smsg); dmg >= 0 {
				if laserOn(ship) {
					s.useFuel(ship, 2.0*float64(strength))
				} else {
					s.useDestruct(ship, strength)
				}
				if !from.Alive {
					s.post(model.NewsCombat, smsg)
				}
				s.notifyStar(p, g, int(ship.Owner), int(from.StOrbits), smsg)
				sess.Send(lmsg)
				s.warn(int(ship.Owner), int(ship.Governor), lmsg)
			}
			s.ships[sh] = ship
		}
		sh = next
	}
}

// cmdBombard fires a ship at a planet sector.
//
// Syntax: bombard <ship-spec> [<x,y> [<strength>]]
//
// Mirrors bombard() in fire.c.
func (s *State) cmdBombard(p, g int, args []string, sess net.Responder) {
	if len(args) < 1 {
		sess.Send("Syntax: 'bombard <ship> [<x,y> [<strength>]]'.\n")
		return
	}

	// Optional x,y.
	targetX, targetY := -1, -1
	if len(args) >= 2 {
		fmt.Sscanf(args[1], "%d,%d", &targetX, &targetY)
	}

	// Optional strength.
	strengthArg := -1
	if len(args) >= 3 {
		if n, err := strconv.Atoi(args[2]); err == nil {
			strengthArg = n
		}
	}

	ships := s.shipsBySpec(p, g, args[0])
	for _, from := range ships {
		if !authorized(g, from) {
			continue
		}
		if !from.Active {
			sendf(sess, "%s is irradiated and inactive.\n", shipDisplayName(from))
			continue
		}
		if from.WhatOrbits != model.LevelPlan {
			sess.Send("You must be in orbit around a planet to bombard.\n")
			continue
		}
		if from.Type == model.OtypeAFV && !landed(from) {
			sess.Send("This ship is not landed on the planet.\n")
			continue
		}

		star := s.stars[from.StOrbits]
		if star == nil || !s.enoughAP(p, g, star.AP[p-1], 1) {
			continue
		}

		var maxStrength int
		s.checkRetalStrength(from, &maxStrength)
		strength := maxStrength
		if strengthArg >= 0 {
			strength = strengthArg
		}
		if strength > maxStrength {
			strength = maxStrength
			label := "Guns"
			if laserOn(from) {
				label = "Laser strength"
			}
			sendf(sess, "%s set to %d\n", label, strength)
		}
		if laserOn(from) {
			s.checkOverload(from, 0, &strength)
		}
		if strength <= 0 {
			sess.Send("No attack.\n")
			s.ships[from.Number] = from
			continue
		}

		planet := s.planet(int(from.StOrbits), int(from.PNumOrbits))
		if planet == nil {
			continue
		}

		// Sector target: random if not specified.
		x, y := targetX, targetY
		if x < 0 {
			x = intRand(0, int(planet.MaxX)-1)
			y = intRand(0, int(planet.MaxY)-1)
		} else if x > int(planet.MaxX)-1 || y < 0 || y > int(planet.MaxY)-1 {
			sess.Send("Illegal sector.\n")
			continue
		}

		if landed(from) && !adjacent(int(from.LandX), int(from.LandY), x, y, planet) {
			sess.Send("You are not adjacent to that sector.\n")
			continue
		}

		// Planetary defense network check.
		if !landed(from) && s.hasPlanDefNet(planet, p) {
			sess.Send("Target has planetary defense networks.\nThese have to be eliminated before you can attack sectors.\n")
			continue
		}

		var longMsg, shortMsg string
		numdest := s.shootShipToPlanet(from, planet, strength, x, y, 1, 0, 0, &longMsg, &shortMsg)

		if numdest < 0 {
			sess.Send("Illegal attack.\n")
			continue
		}

		if laserOn(from) {
			s.useFuel(from, 2.0*float64(strength))
		} else {
			s.useDestruct(from, strength)
		}

		s.post(model.NewsCombat, shortMsg)
		s.notifyStar(p, g, 0, int(from.StOrbits), shortMsg)
		// Telegram all races whose sectors were hit.
		for i := 0; i < model.MaxPlayers; i++ {
			if planet.Info[i].Explored { // reused as "was hit" flag by shootShipToPlanet
				s.warn(i+1, int(star.Governor[i]), longMsg)
				planet.Info[i].Explored = false // clear after use
			}
		}
		sess.Send(longMsg)

		// Planet retaliates (Defense feature).
		if s.cfg.Defense && numdest > 0 && from.Type != model.OtypeAFV {
			for i := 0; i < model.MaxPlayers; i++ {
				if planet.SlavedTo != 0 {
					break
				}
				alien := s.raceByNum(i + 1)
				if alien == nil || planet.Info[i].NumSectsOwned == 0 {
					continue
				}
				planStrength := int(planet.Info[i].Destruct)
				if int(planet.Info[i].Guns) < planStrength {
					planStrength = int(planet.Info[i].Guns)
				}
				if planStrength <= 0 {
					continue
				}
				planet.Info[i].Destruct -= uint16(planStrength)
				var lmsg2, smsg2 string
				s.shootPlanetToShip(alien, planet, from, planStrength, &lmsg2, &smsg2)
				s.warn(i+1, int(star.Governor[i]), lmsg2)
				sess.Send(lmsg2)
				if !from.Alive {
					s.post(model.NewsCombat, smsg2)
				}
				s.notifyStar(p, g, i+1, int(from.StOrbits), smsg2)
			}
		}

		// Protecting ships retaliate if not an AFV.
		if numdest > 0 && from.Alive && from.Type != model.OtypeAFV {
			s.planetProtectorRetaliate(p, g, from, planet, sess)
		}

		s.ships[from.Number] = from
		_ = s.store.PutPlanet(planet)
		s.deductAPs(p, g, 1, int(from.StOrbits), false)
	}
}

// hasPlanDefNet returns true if the planet has an active enemy planetary
// defense network (OtypePlanDef ship not owned by playerNum).
func (s *State) hasPlanDefNet(pl *model.Planet, playerNum int) bool {
	for sh := pl.Ships; sh != 0; {
		ship, ok := s.ships[sh]
		if !ok {
			break
		}
		if ship.Alive && ship.Type == model.OtypePlanDef && int(ship.Owner) != playerNum {
			return true
		}
		sh = ship.NextShip
	}
	return false
}

// planetProtectorRetaliate fires all planet-protect ships back at the attacker.
// Mirrors the protect.planet retaliation loop in bombard() in fire.c.
func (s *State) planetProtectorRetaliate(p, g int, from *model.Ship, planet *model.Planet, sess net.Responder) {
	for sh := planet.Ships; sh != 0 && from.Alive; {
		ship, ok := s.ships[sh]
		if !ok {
			break
		}
		next := ship.NextShip

		if ship.Protect.Planet && sh != from.Number && ship.Alive && ship.Active {
			var strength int
			if laserOn(ship) {
				s.checkOverload(ship, 0, &strength)
			}
			s.checkRetalStrength(ship, &strength)

			var lmsg, smsg string
			if dmg := s.shootShipToShip(ship, from, strength, 0, 0, &lmsg, &smsg); dmg >= 0 {
				if laserOn(ship) {
					s.useFuel(ship, 2.0*float64(strength))
				} else {
					s.useDestruct(ship, strength)
				}
				if !from.Alive {
					s.post(model.NewsCombat, smsg)
				}
				s.notifyStar(p, g, int(ship.Owner), int(from.StOrbits), smsg)
				s.warn(int(ship.Owner), int(ship.Governor), lmsg)
				sess.Send(lmsg)
			}
			s.ships[sh] = ship
		}
		sh = next
	}
}

// cmdDefend fires a planet's guns at an orbiting ship.
// Only available when cfg.Defense is set.
//
// Syntax: defend <target#> <x,y> [<strength>]
//
// Mirrors defend() in fire.c.
func (s *State) cmdDefend(p, g int, args []string, sess net.Responder) {
	if !s.cfg.Defense {
		sess.Send("Planetary defense is not enabled.\n")
		return
	}

	dir := s.dirs[p-1][g]
	if int(dir.Level) != model.LevelPlan {
		sess.Send("You have to set scope to the planet first.\n")
		return
	}
	if len(args) < 2 {
		sess.Send("Syntax: 'defend <ship> <sector> [<strength>]'.\n")
		return
	}

	star := s.stars[dir.StarNum]
	if star == nil || !controlStar(p, g, star) {
		sess.Send("You are not authorized to do that in this system.\n")
		return
	}
	if !s.enoughAP(p, g, star.AP[p-1], 1) {
		return
	}

	// Parse target ship number.
	targetArg := args[0]
	if len(targetArg) > 0 && targetArg[0] == '#' {
		targetArg = targetArg[1:]
	}
	toShipNo, err := strconv.Atoi(targetArg)
	if err != nil || toShipNo <= 0 {
		sess.Send("Bad ship number.\n")
		return
	}

	planet := s.planet(int(dir.StarNum), int(dir.PlanetNum))
	if planet == nil {
		sess.Send("Internal error: planet not found.\n")
		return
	}

	if planet.Info[p-1].NumSectsOwned == 0 {
		sess.Send("You do not occupy any sectors here.\n")
		return
	}
	if planet.SlavedTo > 0 && int(planet.SlavedTo) != p {
		sess.Send("This planet is enslaved.\n")
		return
	}

	to, ok := s.ships[uint32(toShipNo)]
	if !ok || to == nil {
		sess.Send("No such ship.\n")
		return
	}
	if to.WhatOrbits != model.LevelPlan {
		sess.Send("The ship is not in planet orbit.\n")
		return
	}
	if to.StOrbits != dir.StarNum || to.PNumOrbits != dir.PlanetNum {
		sess.Send("Target is not in orbit around this planet.\n")
		return
	}
	if landed(to) {
		sess.Send("Planet guns can't fire on landed ships.\n")
		return
	}

	// Sector from which we fire.
	x, y := -1, -1
	fmt.Sscanf(args[1], "%d,%d", &x, &y)
	if x < 0 || x > int(planet.MaxX)-1 || y < 0 || y > int(planet.MaxY)-1 {
		sess.Send("Illegal sector.\n")
		return
	}
	sect := planet.SectorAt(x, y)
	if int(sect.Owner) != p {
		sess.Send("Nice try.\n")
		return
	}

	strength := int(planet.Info[p-1].Guns)
	if len(args) >= 3 {
		if n, err2 := strconv.Atoi(args[2]); err2 == nil {
			strength = n
		}
	}
	if int(planet.Info[p-1].Destruct) < strength {
		strength = int(planet.Info[p-1].Destruct)
	}
	if int(planet.Info[p-1].Guns) < strength {
		strength = int(planet.Info[p-1].Guns)
	}
	if strength <= 0 {
		sendf(sess, "No attack - %d guns, %dd\n",
			planet.Info[p-1].Guns, planet.Info[p-1].Destruct)
		return
	}

	Race := s.raceByNum(p)
	if Race == nil {
		return
	}

	var retal int
	s.checkRetalStrength(to, &retal)
	dummy := *to // copy for retaliation shot

	var longMsg, shortMsg string
	damage := s.shootPlanetToShip(Race, planet, to, strength, &longMsg, &shortMsg)

	if damage < 0 {
		sendf(sess, "Target out of range  %.2f!\n", s.cfg.SystemSize)
		return
	}

	planet.Info[p-1].Destruct -= uint16(strength)
	if !to.Alive {
		s.post(model.NewsCombat, shortMsg)
	}
	s.notifyStar(p, g, int(to.Owner), int(to.StOrbits), shortMsg)
	s.warn(int(to.Owner), int(to.Governor), longMsg)
	sess.Send(longMsg)

	// Defender retaliates with a ship-to-planet shot if protect.self.
	if retal > 0 && damage > 0 && to.Protect.Self {
		strength = retal
		if laserOn(to) {
			s.checkOverload(to, 0, &strength)
		}
		var lmsg2, smsg2 string
		if numdest := s.shootShipToPlanet(&dummy, planet, strength, x, y, 1, 0, 0, &lmsg2, &smsg2); numdest >= 0 {
			if laserOn(to) {
				s.useFuel(to, 2.0*float64(strength))
			} else {
				s.useDestruct(to, strength)
			}
			s.post(model.NewsCombat, smsg2)
			s.notifyStar(p, g, int(to.Owner), int(to.StOrbits), smsg2)
			sess.Send(lmsg2)
			s.warn(int(to.Owner), int(to.Governor), lmsg2)
		}
	}

	// Protecting ships retaliate.
	if damage > 0 {
		s.planetProtectorRetaliate(p, g, &dummy, planet, sess)
	}

	s.ships[uint32(toShipNo)] = to
	_ = s.store.PutPlanet(planet)
	s.deductAPs(p, g, 1, int(dir.StarNum), false)
}

// cmdDetonate manually detonates a mine owned by the player.
//
// Syntax: detonate <mine-spec>
//
// Mirrors detonate() in fire.c.
func (s *State) cmdDetonate(p, g int, args []string, sess net.Responder) {
	if len(args) < 1 {
		sess.Send("Detonate what?\n")
		return
	}
	ships := s.shipsBySpec(p, g, args[0])
	for _, sh := range ships {
		if !authorized(g, sh) {
			continue
		}
		if sh.Type != model.StypeMine {
			sess.Send("That is not a mine.\n")
			continue
		}
		if !sh.On {
			sess.Send("The mine is not activated.\n")
			continue
		}
		if sh.Docked || sh.WhatOrbits == model.LevelShip {
			sess.Send("The mine is docked or landed.\n")
			continue
		}
		s.doMine(sh.Number, true)
	}
}
