package game

// commands_nav.go — land, launch, scrap, and capture commands.
// Mirrors land.c, launch.c, scrap.c, and capture.c from the C source.

import (
	"fmt"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// ---- cmdLand -------------------------------------------------------------

// cmdLand lands a ship on a planet sector or docks it to another ship.
// Usage: land <ship-spec> <x,y>        — land on planet
//
//	land <ship-spec> #<shipno>    — dock/load onto another ship
//
// Mirrors land() in land.c.
func (s *State) cmdLand(p, g int, args []string, sess net.Responder) {
	if len(args) < 2 {
		sess.Send("Land what?\n")
		return
	}

	ships := s.shipsBySpec(p, g, args[0])
	for _, sh := range ships {
		s.doLandOne(p, g, sh, args[1], sess)
	}
}

func (s *State) doLandOne(p, g int, sh *model.Ship, target string, sess net.Responder) {
	if overloaded(sh) {
		sendf(sess, "%s is too overloaded to land.\n", shipDisplayName(sh))
		return
	}
	if sh.Type == model.OtypeQuarry {
		sess.Send("You can't load quarries onto ship.\n")
		return
	}
	if dockedToShip(sh) {
		sess.Send("That ship is docked to another ship.\n")
		return
	}

	if len(target) > 0 && target[0] == '#' {
		// Land on / dock to another ship
		s.doLandOnShip(p, g, sh, target[1:], sess)
		return
	}

	// Land on planet sector
	s.doLandOnPlanet(p, g, sh, target, sess)
}

func (s *State) doLandOnShip(p, g int, sh *model.Ship, shipNoStr string, sess net.Responder) {
	shipNo := uint32(atoi(shipNoStr))
	s2, ok := s.ships[shipNo]
	if !ok {
		sendf(sess, "Ship #%d wasn't found.\n", shipNo)
		return
	}
	if errMsg := s.testShip(p, g, s2); errMsg != "" {
		sess.Send("Illegal format.\n")
		return
	}
	if s2.Type == model.OtypeFactory {
		sess.Send("Can't land on factories.\n")
		return
	}

	if landed(sh) {
		// Ship-to-ship loading while both are on a planet surface
		if !landed(s2) {
			sendf(sess, "%s is not landed on a planet.\n", shipDisplayName(s2))
			return
		}
		if s2.StOrbits != sh.StOrbits {
			sess.Send("These ships are not in the same star system.\n")
			return
		}
		if s2.PNumOrbits != sh.PNumOrbits {
			sess.Send("These ships are not landed on the same planet.\n")
			return
		}
		if s2.LandX != sh.LandX || s2.LandY != sh.LandY {
			sess.Send("These ships are not in the same sector.\n")
			return
		}
		if sh.On {
			sendf(sess, "%s must be turned off before loading.\n", shipDisplayName(sh))
			return
		}
		hangerNeeded := s.shipSize(sh)
		if int(s2.Hanger)+hangerNeeded > int(s2.MaxHanger) {
			sendf(sess, "Mothership does not have %d hanger space available to load ship.\n", hangerNeeded)
			return
		}
		// Remove from planet, insert into carrier
		pl := s.planet(int(sh.StOrbits), int(sh.PNumOrbits))
		if pl != nil {
			s.removeShipPlan(pl, sh)
			s.savePlanet(pl)
		}
		s.insertShipInShip(sh, s2)
		s2.Mass += sh.Mass
		s2.Hanger += uint16(hangerNeeded)
		sh.Docked = true
		sendf(sess, "%s loaded onto %s using 0 fuel.\n", shipDisplayName(sh), shipDisplayName(s2))
		s.saveShip(sh)
		s.saveShip(s2)
		return
	}

	if sh.Docked {
		sendf(sess, "%s is already docked or landed.\n", shipDisplayName(sh))
		return
	}

	// Flying ship docking with another ship
	if sh.WhatOrbits != s2.WhatOrbits {
		sess.Send("Those ships are not in the same scope.\n")
		return
	}
	d := dist(s2.XPos, s2.YPos, sh.XPos, sh.YPos)
	if d > s.cfg.DistToDock {
		sendf(sess, "%s must be %.2f or closer to %s.\n",
			shipDisplayName(sh), s.cfg.DistToDock, shipDisplayName(s2))
		return
	}
	fuel := 0.05 + d*0.025*math.Sqrt(sh.Mass)
	if sh.Fuel < fuel {
		sess.Send("Not enough fuel.\n")
		return
	}
	hangerNeeded := s.shipSize(sh)
	if int(s2.Hanger)+hangerNeeded > int(s2.MaxHanger) {
		sendf(sess, "Mothership does not have %d hanger space available to load ship.\n", hangerNeeded)
		return
	}
	s.useFuel(sh, fuel)

	// Remove from current scope
	if sh.WhatOrbits == model.LevelPlan {
		if pl := s.planet(int(sh.StOrbits), int(sh.PNumOrbits)); pl != nil {
			s.removeShipPlan(pl, sh)
			s.savePlanet(pl)
		}
	} else if sh.WhatOrbits == model.LevelStar {
		if int(sh.StOrbits) < len(s.stars) && s.stars[sh.StOrbits] != nil {
			s.removeShipStar(s.stars[sh.StOrbits], sh)
			s.saveStar(s.stars[sh.StOrbits])
		}
	} else {
		sess.Send("Ship is not in planet or star scope.\n")
		return
	}
	s.insertShipInShip(sh, s2)
	s2.Mass += sh.Mass
	s2.Hanger += uint16(hangerNeeded)
	sendf(sess, "%s landed on %s using %.1f fuel.\n",
		shipDisplayName(sh), shipDisplayName(s2), fuel)
	sh.Docked = true
	s.saveShip(sh)
	s.saveShip(s2)
}

func (s *State) doLandOnPlanet(p, g int, sh *model.Ship, target string, sess net.Responder) {
	if sh.Docked {
		sendf(sess, "%s is docked.\n", shipDisplayName(sh))
		return
	}
	var x, y int
	n, _ := fmt.Sscanf(target, "%d,%d", &x, &y)
	if n < 2 {
		x, y = -1, -1
	}

	if sh.WhatOrbits != model.LevelPlan {
		sendf(sess, "%s doesn't orbit a planet.\n", shipDisplayName(sh))
		return
	}
	if model.Shipdata[sh.Type][model.AbilCanLand] == 0 {
		sess.Send("This ship is not equipped to land.\n")
		return
	}
	dir := s.dirs[p-1][g]
	if sh.StOrbits != dir.StarNum || sh.PNumOrbits != dir.PlanetNum {
		sess.Send("You have to cs to the planet it orbits.\n")
		return
	}
	if !speedRating(sh) {
		sess.Send("This ship is not rated for maneuvering.\n")
		return
	}
	if int(sh.StOrbits) >= len(s.stars) || s.stars[sh.StOrbits] == nil {
		return
	}
	star := s.stars[sh.StOrbits]
	if !s.enoughAP(p, g, star.AP[p-1], 1) {
		return
	}

	planet := s.planet(int(sh.StOrbits), int(sh.PNumOrbits))
	if planet == nil {
		return
	}

	grav := s.gravityOf(planet)
	sendf(sess, "Planet /%s/%s has gravity field of %.2f.\n",
		s.starName(int(sh.StOrbits)),
		s.planetName(int(sh.StOrbits), int(sh.PNumOrbits)),
		grav)

	planetX := star.XPos + planet.XPos
	planetY := star.YPos + planet.YPos
	d := dist(planetX, planetY, sh.XPos, sh.YPos)
	sendf(sess, "Distance to planet: %.2f.\n", d)

	if d > s.cfg.DistToLand {
		sendf(sess, "%s must be %.3g or closer to the planet (%.2f).\n",
			shipDisplayName(sh), s.cfg.DistToLand, d)
		return
	}

	fuel := sh.Mass * grav * s.cfg.LandGravMassFactor

	if x < 0 || y < 0 || x >= int(planet.MaxX) || y >= int(planet.MaxY) {
		sess.Send("Illegal coordinates.\n")
		return
	}

	// DEFENSE: enemies with planetary guns fire on landing ship
	if s.cfg.Defense {
		for i := 1; i <= model.MaxPlayers; i++ {
			if !sh.Alive || i == p {
				continue
			}
			if int(i-1) >= len(s.races) || s.races[i-1] == nil {
				continue
			}
			alien := s.races[i-1]
			if planet.Info[i-1].Popn == 0 || planet.Info[i-1].Guns == 0 || planet.Info[i-1].Destruct == 0 {
				continue
			}
			if alien.IsAtWar(int(sh.Owner)) {
				strength := int(planet.Info[i-1].Guns)
				if int(planet.Info[i-1].Destruct) < strength {
					strength = int(planet.Info[i-1].Destruct)
				}
				if strength > 0 {
					var longMsg, shortMsg string
					dmg := s.shootPlanetToShip(alien, planet, sh, strength, &longMsg, &shortMsg)
					planet.Info[i-1].Destruct -= uint16(strength)
					if dmg > 0 {
						s.notify(int(sh.Owner), int(sh.Governor),
							fmt.Sprintf("Planetary guns fire on %s (damage %d%%)!\n",
								shipDisplayName(sh), dmg))
						s.notifyRace(i,
							fmt.Sprintf("Planetary guns fired on %s at %s/%s!\n",
								shipDisplayName(sh),
								s.starName(int(sh.StOrbits)),
								s.planetName(int(sh.StOrbits), int(sh.PNumOrbits))))
					}
				}
			}
		}
		if !sh.Alive {
			s.savePlanet(planet)
			s.saveShip(sh)
			return
		}
	}

	numdest := 0
	if crashCheck(sh, fuel) {
		// Ship crashes
		var longMsg, shortMsg string
		n := s.shootShipToPlanet(sh, planet,
			int(sh.Destruct)/3, x, y, 1, 0, 0, &longMsg, &shortMsg)
		if n < 0 {
			n = 0
		}
		numdest = n
		sendf(sess, "BOOM!! %s crashes on sector %d,%d with blast radius of %d.\n",
			shipDisplayName(sh), x, y, numdest)
		for i := 1; i <= model.MaxPlayers; i++ {
			if planet.Info[i-1].NumSectsOwned > 0 || i == p {
				s.warnRace(i, fmt.Sprintf("%s crashed on /%s/%s!\n",
					shipDisplayName(sh),
					s.starName(int(sh.StOrbits)),
					s.planetName(int(sh.StOrbits), int(sh.PNumOrbits))))
			}
		}
		if crashRoll != 0 {
			sendf(sess, "Ship damage %d%% (you rolled a %d)\n", int(sh.Damage), crashRoll)
		} else {
			sendf(sess, "You had %.1ff while the landing required %.1ff\n", sh.Fuel, fuel)
		}
		s.killShip(p, sh)
	} else {
		sh.LandX = uint8(x)
		sh.LandY = uint8(y)
		sh.XPos = planet.XPos + star.XPos
		sh.YPos = planet.YPos + star.YPos
		s.useFuel(sh, fuel)
		sh.Docked = true
		sh.WhatDest = model.LevelPlan
		sh.DestStar = sh.StOrbits
		sh.DestPNum = sh.PNumOrbits
	}

	sect := planet.SectorAt(x, y)
	if sect.Condition == model.SectorWasted {
		sess.Send("Warning: That sector is a wasteland!\n")
	} else if sect.Owner != 0 && int(sect.Owner) != p {
		alien := s.raceByNum(int(sect.Owner))
		race := s.raceByNum(p)
		if race != nil && alien != nil &&
			race.IsAllied(int(sect.Owner)) && alien.IsAllied(p) {
			sendf(sess, "You have landed on allied sector (%s).\n", alien.Name)
		} else {
			alienName := "unknown"
			if alien != nil {
				alienName = alien.Name
			}
			sendf(sess, "You have landed on an alien sector (%s).\n", alienName)
		}
	}

	s.deductAPs(p, g, 1, int(sh.StOrbits), sh.WhatOrbits == model.LevelUniv)

	s.savePlanet(planet)
	if numdest > 0 {
		// sector was already modified by shootShipToPlanet
	}

	// Notify other races on the planet
	msg := fmt.Sprintf("%s observed landing on sector %d,%d, planet /%s/%s.\n",
		shipDisplayName(sh), sh.LandX, sh.LandY,
		s.starName(int(sh.StOrbits)),
		s.planetName(int(sh.StOrbits), int(sh.PNumOrbits)))
	for i := 1; i <= model.MaxPlayers; i++ {
		if planet.Info[i-1].NumSectsOwned > 0 && i != p {
			s.notify(i, int(star.Governor[i-1]), msg)
		}
	}

	if sh.Alive {
		sendf(sess, "%s landed on planet.\n", shipDisplayName(sh))
	}
	s.saveShip(sh)
}

// ---- cmdLaunch -----------------------------------------------------------

// cmdLaunch launches a ship from a planet or undocks it from a carrier.
// Usage: launch <ship-spec>
// Mirrors launch() in launch.c.
func (s *State) cmdLaunch(p, g int, args []string, sess net.Responder) {
	if len(args) == 0 {
		sess.Send("Launch what?\n")
		return
	}
	ships := s.shipsBySpec(p, g, args[0])
	for _, sh := range ships {
		s.doLaunchOne(p, g, sh, sess)
	}
}

func (s *State) doLaunchOne(p, g int, sh *model.Ship, sess net.Responder) {
	if !speedRating(sh) && landed(sh) {
		sess.Send("That ship is not designed to be launched.\n")
		return
	}
	if !sh.Docked && sh.WhatOrbits != model.LevelShip {
		sendf(sess, "%s is not landed or docked.\n", shipDisplayName(sh))
		return
	}

	apCount := 1
	if !landed(sh) {
		apCount = 0
	}

	if landed(sh) && sh.Resource > uint16(shipMaxResource(sh)) {
		sendf(sess, "%s is too overloaded to launch.\n", shipDisplayName(sh))
		return
	}

	if sh.WhatOrbits == model.LevelShip {
		// Launching from inside a carrier
		carrier, ok := s.ships[sh.DestShipNo]
		if !ok {
			sendf(sess, "%s: carrier ship not found.\n", shipDisplayName(sh))
			return
		}
		if sh.Type == model.OtypeFactory && sh.On {
			sess.Send("Factories cannot be launched once turned on.\n")
			sess.Send("Consider using 'scrap'.\n")
			return
		}
		s.launchFromCarrier(p, g, sh, carrier, apCount, sess)
		return
	}

	// Undock from another ship (docked via dest pointer, not inside it)
	if sh.WhatDest == model.LevelShip {
		s2, ok := s.ships[sh.DestShipNo]
		if !ok {
			return
		}
		if s2.WhatOrbits == model.LevelUniv {
			if !s.enoughAP(p, g, s.sdata.AP[p-1], apCount) {
				return
			}
			s.deductAPs(p, g, apCount, 0, true)
		} else {
			if int(sh.StOrbits) >= len(s.stars) || s.stars[sh.StOrbits] == nil {
				return
			}
			if !s.enoughAP(p, g, s.stars[sh.StOrbits].AP[p-1], apCount) {
				return
			}
			s.deductAPs(p, g, apCount, int(sh.StOrbits), false)
		}
		sh.Docked = false
		sh.WhatDest = model.LevelUniv
		sh.DestShipNo = 0
		s2.Docked = false
		s2.WhatDest = model.LevelUniv
		s2.DestShipNo = 0
		sendf(sess, "%s undocked from %s.\n", shipDisplayName(sh), shipDisplayName(s2))
		s.saveShip(sh)
		s.saveShip(s2)
		return
	}

	// Launching from a planet
	if int(sh.StOrbits) >= len(s.stars) || s.stars[sh.StOrbits] == nil {
		return
	}
	star := s.stars[sh.StOrbits]
	if !s.enoughAP(p, g, star.AP[p-1], apCount) {
		return
	}
	s.deductAPs(p, g, apCount, int(sh.StOrbits), false)

	planet := s.planet(int(sh.StOrbits), int(sh.PNumOrbits))
	if planet == nil {
		return
	}

	grav := s.gravityOf(planet)
	sendf(sess, "Planet /%s/%s has gravity field of %.2f\n",
		s.starName(int(sh.StOrbits)),
		s.planetName(int(sh.StOrbits), int(sh.PNumOrbits)), grav)

	fuel := grav * sh.Mass * s.cfg.LaunchGravMassFactor
	if sh.Fuel < fuel {
		sendf(sess, "%s does not have enough fuel! (%.1f)\n", shipDisplayName(sh), fuel)
		return
	}
	s.useFuel(sh, fuel)
	sh.Docked = false
	sh.WhatDest = model.LevelUniv

	// Position ship near planet with random offset
	offset := s.cfg.DistToLand / 4.0
	sh.XPos = star.XPos + planet.XPos + randFloat(-offset, offset)
	sh.YPos = star.YPos + planet.YPos + randFloat(-offset, offset)

	// Special-case reset for dust/greenhouse objects
	switch sh.Type {
	case model.OtypeCanist, model.OtypeGreen:
		if sh.Special.Timer != nil {
			sh.Special.Timer.Count = 0
		}
	}
	sh.Notified = false

	if !planet.Explored {
		planet.Explored = true
		s.savePlanet(planet)
	}

	// Notify others on the planet
	msg := fmt.Sprintf("%s observed launching from planet /%s/%s.\n",
		shipDisplayName(sh), s.starName(int(sh.StOrbits)),
		s.planetName(int(sh.StOrbits), int(sh.PNumOrbits)))
	for i := 1; i <= model.MaxPlayers; i++ {
		if planet.Info[i-1].NumSectsOwned > 0 && i != p {
			s.notify(i, int(star.Governor[i-1]), msg)
		}
	}

	sendf(sess, "%s launched from planet, using %.1f fuel.\n", shipDisplayName(sh), fuel)

	switch sh.Type {
	case model.OtypeCanist:
		sess.Send("A cloud of dust envelopes your planet.\n")
	case model.OtypeGreen:
		sess.Send("Green house gases surround the planet.\n")
	}

	s.saveShip(sh)
}

// launchFromCarrier handles the case where a ship is inside a carrier.
func (s *State) launchFromCarrier(p, g int, sh, carrier *model.Ship, apCount int, sess net.Responder) {
	s.removeShipShip(sh, carrier)

	if landed(carrier) {
		// Carrier is on a planet — place ship on same planet surface
		planet := s.planet(int(carrier.StOrbits), int(carrier.PNumOrbits))
		if planet != nil {
			s.insertShipPlan(planet, sh)
			s.savePlanet(planet)
		}
		sh.StOrbits = carrier.StOrbits
		sh.PNumOrbits = carrier.PNumOrbits
		sh.DestPNum = carrier.PNumOrbits
		sh.DestStar = carrier.DestStar
		sh.XPos = carrier.XPos
		sh.YPos = carrier.YPos
		sh.LandX = carrier.LandX
		sh.LandY = carrier.LandY
		sh.Docked = true
		sh.WhatDest = model.LevelPlan
		sendf(sess, "Landed on %s/%s.\n",
			s.starName(int(sh.StOrbits)),
			s.planetName(int(sh.StOrbits), int(sh.PNumOrbits)))
	} else if carrier.WhatOrbits == model.LevelPlan {
		sendf(sess, "%s launched from %s.\n", shipDisplayName(sh), shipDisplayName(carrier))
		sh.XPos = carrier.XPos
		sh.YPos = carrier.YPos
		sh.Docked = false
		sh.WhatDest = model.LevelUniv
		planet := s.planet(int(carrier.StOrbits), int(carrier.PNumOrbits))
		if planet != nil {
			s.insertShipPlan(planet, sh)
			s.savePlanet(planet)
		}
		sh.StOrbits = carrier.StOrbits
		sh.PNumOrbits = carrier.PNumOrbits
		sendf(sess, "Orbiting %s/%s.\n",
			s.starName(int(sh.StOrbits)),
			s.planetName(int(sh.StOrbits), int(sh.PNumOrbits)))
	} else if carrier.WhatOrbits == model.LevelStar {
		sendf(sess, "%s launched from %s.\n", shipDisplayName(sh), shipDisplayName(carrier))
		sh.XPos = carrier.XPos
		sh.YPos = carrier.YPos
		sh.Docked = false
		sh.WhatDest = model.LevelUniv
		if int(carrier.StOrbits) < len(s.stars) && s.stars[carrier.StOrbits] != nil {
			s.insertShipStar(int(carrier.StOrbits), sh)
			s.saveStar(s.stars[carrier.StOrbits])
		}
		sh.StOrbits = carrier.StOrbits
		sendf(sess, "Orbiting %s.\n", s.starName(int(sh.StOrbits)))
	} else if carrier.WhatOrbits == model.LevelUniv {
		sendf(sess, "%s launched from %s.\n", shipDisplayName(sh), shipDisplayName(carrier))
		sh.XPos = carrier.XPos
		sh.YPos = carrier.YPos
		sh.Docked = false
		sh.WhatDest = model.LevelUniv
		s.insertShipUniv(sh)
		s.saveShip(sh) // sdata is updated inside insertShipUniv
		sess.Send("Universe level.\n")
	} else {
		sess.Send("You can't launch that ship.\n")
		return
	}

	carrier.Mass -= sh.Mass
	carrier.Hanger -= uint16(s.shipSize(sh))
	s.saveShip(sh)
	s.saveShip(carrier)
}

// ---- cmdScrap ------------------------------------------------------------

// cmdScrap destroys a ship and recovers its resources.
// Usage: scrap <ship-spec>
// Mirrors scrap() in scrap.c.
func (s *State) cmdScrap(p, g int, args []string, sess net.Responder) {
	if len(args) == 0 {
		sess.Send("Scrap what?\n")
		return
	}
	ships := s.shipsBySpec(p, g, args[0])
	for _, sh := range ships {
		s.doScrapOne(p, g, sh, sess)
	}
}

func (s *State) doScrapOne(p, g int, sh *model.Ship, sess net.Responder) {
	if sh.MaxCrew > 0 && sh.Popn == 0 {
		sess.Send("Can't scrap that ship - no crew.\n")
		return
	}
	if sh.WhatOrbits == model.LevelUniv {
		return // can't scrap at universe level
	}
	if int(sh.StOrbits) >= len(s.stars) || s.stars[sh.StOrbits] == nil {
		return
	}
	if !s.enoughAP(p, g, s.stars[sh.StOrbits].AP[p-1], 1) {
		return
	}

	if sh.WhatOrbits == model.LevelPlan && sh.Type == model.OtypeToxWC && sh.Special.Waste != nil {
		sendf(sess, "WARNING: This will release %d toxin points back into the atmosphere!!\n",
			sh.Special.Waste.Toxic)
	}

	if !sh.Docked {
		sendf(sess, "%s is not landed or docked.\nNo resources can be reclaimed.\n",
			shipDisplayName(sh))
	}

	var planet *model.Planet
	var sect *model.Sector
	if sh.WhatOrbits == model.LevelPlan {
		planet = s.planet(int(sh.StOrbits), int(sh.PNumOrbits))
		if landed(sh) && planet != nil {
			sect = planet.SectorAt(int(sh.LandX), int(sh.LandY))
		}
	}

	var s2 *model.Ship
	if dockedToShip(sh) {
		s2ok := false
		s2, s2ok = s.ships[sh.DestShipNo]
		if !s2ok {
			return
		}
	}

	scrapval := int(s.shipcostForType(sh.Type, s.raceByNum(p)))/2 + int(sh.Resource)
	destval := 0
	crewval := 0
	troopval := 0
	xtalval := 0
	var fuelval float64

	if sh.Docked {
		race := s.raceByNum(p)
		sendf(sess, "%s: original cost: %d\n", shipDisplayName(sh),
			s.shipcostForType(sh.Type, race))
		suffix := ""
		if sh.Resource > 0 {
			suffix = "(with stockpile) "
		}
		sendf(sess, "         scrap value%s: %d rp's.\n", suffix, scrapval)

		// Cap scrap value at carrier's remaining capacity
		if s2 != nil && s2.Type != model.StypeShuttle {
			room := int(s2.MaxResource) - int(s2.Resource)
			if scrapval > room {
				scrapval = room
				sendf(sess, "(There is only room for %d resources.)\n", scrapval)
			}
		}

		if sh.Fuel > 0 {
			sendf(sess, "Fuel recovery: %.0f.\n", sh.Fuel)
			fuelval = sh.Fuel
			if s2 != nil {
				room := shipMaxFuel(s2) - s2.Fuel
				if fuelval > room {
					fuelval = room
					sendf(sess, "(There is only room for %.2f fuel.)\n", fuelval)
				}
			}
		}

		if sh.Destruct > 0 {
			sendf(sess, "Weapons recovery: %d.\n", sh.Destruct)
			destval = int(sh.Destruct)
			if s2 != nil {
				room := shipMaxDestruct(s2) - int(s2.Destruct)
				if destval > room {
					destval = room
					sendf(sess, "(There is only room for %d destruct.)\n", destval)
				}
			}
		}

		if int(sh.Popn)+int(sh.Troops) > 0 {
			if sect != nil && sect.Owner > 0 && int(sect.Owner) != p {
				sess.Send("You don't own this sector; no crew can be recovered.\n")
			} else {
				sendf(sess, "Population/Troops recovery: %d/%d.\n", sh.Popn, sh.Troops)
				troopval = int(sh.Troops)
				crewval = int(sh.Popn)
				if s2 != nil {
					roomTroop := int(s2.MaxCrew) - int(s2.Troops)
					if troopval > roomTroop {
						troopval = roomTroop
						sendf(sess, "(There is only room for %d troops.)\n", troopval)
					}
					roomCrew := int(s2.MaxCrew) - int(s2.Popn)
					if crewval > roomCrew {
						crewval = roomCrew
						sendf(sess, "(There is only room for %d crew.)\n", crewval)
					}
				}
			}
		}

		if int(sh.Crystals)+btoi2(sh.Mounted) > 0 {
			if sect != nil && sect.Owner > 0 && int(sect.Owner) != p {
				sess.Send("You don't own this sector; no crystals can be recovered.\n")
			} else {
				xtalval = int(sh.Crystals)
				if sh.Mounted {
					xtalval++
				}
				if s2 != nil {
					room := int(s2.MaxResource) - int(s2.Crystals) // crystals stored separately
					if xtalval > room {
						xtalval = room
					}
				}
				sendf(sess, "Crystal recovery: %d.\n", xtalval)
			}
		}
	}

	// Adjust carrier hanger if inside a ship
	if sh.WhatOrbits == model.LevelShip {
		if carrier, ok := s.ships[sh.DestShipNo]; ok {
			carrier.Hanger -= uint16(s.shipSize(sh))
			s.saveShip(carrier)
		}
	}

	s.deductAPs(p, g, 1, int(sh.StOrbits), sh.WhatOrbits == model.LevelUniv)

	s.killShip(p, sh)

	// Transfer resources to docked ship
	if s2 != nil {
		s2.Crystals += uint16(xtalval)
		s.rcvFuel(s2, fuelval)
		s.rcvDestruct(s2, destval)
		s.rcvResource(s2, scrapval)
		race := s.raceByNum(p)
		mass := 0.1
		if race != nil {
			mass = race.Mass
		}
		s.rcvTroops(s2, troopval, mass)
		s.rcvPopn(s2, crewval, mass)
		// Undock the carrier if it was docked to us (but not if we were inside it)
		if !(sh.WhatOrbits == model.LevelShip) {
			s2.Docked = false
			s2.WhatDest = model.LevelUniv
			s2.DestShipNo = 0
		}
		s.saveShip(s2)
	}

	if planet != nil && landed(sh) {
		if sect != nil {
			if int(sect.Owner) == p {
				sect.Popn += uint16(troopval + crewval)
			} else if sect.Owner == 0 {
				sect.Owner = uint8(p)
				sect.Popn += uint16(crewval)
				sect.Troops += uint16(troopval)
				planet.Info[p-1].NumSectsOwned++
				planet.Info[p-1].Popn += uint32(crewval + troopval)
				sendf(sess, "Sector %d,%d Colonized.\n", sh.LandX, sh.LandY)
			}
		}
		planet.Info[p-1].Resource += uint16(scrapval)
		planet.Popn += uint64(crewval)
		planet.Info[p-1].Destruct += uint16(destval)
		planet.Info[p-1].Fuel += uint16(fuelval)
		planet.Info[p-1].Crystals += uint16(xtalval)
		s.savePlanet(planet)
	}

	if landed(sh) {
		sess.Send("\nScrapped.\n")
	} else {
		sess.Send("\nDestroyed.\n")
	}
	s.saveShip(sh)
}

// btoi2 converts a bool to int (0 or 1).
func btoi2(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- cmdCapture ----------------------------------------------------------

// cmdCapture boards and captures an enemy ship landed on a planet.
// Usage: capture <enemy-ship-number>
// Mirrors capture() in capture.c.
func (s *State) cmdCapture(p, g int, args []string, sess net.Responder) {
	if len(args) == 0 {
		sess.Send("Capture what?\n")
		return
	}

	dir := s.dirs[p-1][g]
	if dir.Level != model.LevelPlan {
		sess.Send("You must be at planet scope to capture a ship.\n")
		return
	}
	if int(dir.StarNum) >= len(s.stars) || s.stars[dir.StarNum] == nil {
		return
	}
	if !s.enoughAP(p, g, s.stars[dir.StarNum].AP[p-1], 1) {
		return
	}

	trimmed := args[0]
	if len(trimmed) > 0 && trimmed[0] == '#' {
		trimmed = trimmed[1:]
	}
	targetNo := uint32(atoi(trimmed))
	target, ok := s.ships[targetNo]
	if !ok || !target.Alive {
		sendf(sess, "Ship #%d not found.\n", targetNo)
		return
	}
	if !landed(target) {
		sendf(sess, "%s is not landed on a planet.\n", shipDisplayName(target))
		return
	}
	if target.StOrbits != dir.StarNum || target.PNumOrbits != dir.PlanetNum {
		sess.Send("That ship is not on this planet.\n")
		return
	}
	if int(target.Owner) == p {
		sess.Send("You can't capture your own ship.\n")
		return
	}

	// Find an attacking ship in the same sector
	var attacker *model.Ship
	for _, sh := range s.ships {
		if !sh.Alive || int(sh.Owner) != p {
			continue
		}
		if !landed(sh) {
			continue
		}
		if sh.StOrbits != dir.StarNum || sh.PNumOrbits != dir.PlanetNum {
			continue
		}
		if sh.LandX != target.LandX || sh.LandY != target.LandY {
			continue
		}
		if sh.Popn == 0 && sh.Troops == 0 {
			continue
		}
		attacker = sh
		break
	}
	if attacker == nil {
		sess.Send("You need a crewed ship in the same sector to capture.\n")
		return
	}

	planet := s.planet(int(dir.StarNum), int(dir.PlanetNum))
	if planet == nil {
		return
	}
	sect := planet.SectorAt(int(target.LandX), int(target.LandY))

	// Boarding combat: attacker troops vs defender troops
	attackForce := int(attacker.Troops) + int(attacker.Popn)/10
	defForce := int(target.Troops) + int(target.Popn)/10

	// Sector defense bonus for the defender
	defBonus := 1
	switch sect.Condition {
	case model.SectorLand:
		defBonus = 2
	case model.SectorMount:
		defBonus = 3
	}
	defForce *= defBonus

	sendf(sess, "Boarding %s! Attack force: %d, Defense force: %d\n",
		shipDisplayName(target), attackForce, defForce)

	if attackForce <= defForce {
		// Attack fails
		casualties := attackForce / 3
		if casualties < 1 {
			casualties = 1
		}
		attCasual := min2i(casualties, int(attacker.Troops))
		attacker.Troops -= uint16(attCasual)
		attacker.Mass -= float64(attCasual) * s.raceByNum(p).Mass
		sendf(sess, "Boarding repulsed! Attacker lost %d troops.\n", attCasual)
		s.saveShip(attacker)
	} else {
		// Attack succeeds — capture the ship
		defCasual := defForce / 2
		attCasual := attackForce / 4
		if attCasual < 1 {
			attCasual = 1
		}

		attacker.Troops -= uint16(min2i(attCasual, int(attacker.Troops)))

		// Apply defender casualties.
		defTroopLoss := min2i(defCasual, int(target.Troops))
		target.Troops -= uint16(defTroopLoss)
		defCivLoss := min2i(defCasual-defTroopLoss, int(target.Popn))
		target.Popn -= uint16(defCivLoss)
		defMass := float64(defTroopLoss+defCivLoss) * func() float64 {
			if r := s.raceByNum(int(target.Owner)); r != nil {
				return r.Mass
			}
			return 1.0
		}()
		target.Mass -= defMass

		targetRace := s.raceByNum(int(target.Owner))
		oldOwner := int(target.Owner)
		target.Owner = uint8(p)
		target.Governor = uint8(g)

		sendf(sess, "%s captured! (attacker lost %d troops)\n",
			shipDisplayName(target), attCasual)

		// Notify the previous owner
		if targetRace != nil {
			s.notifyRace(oldOwner, fmt.Sprintf("%s has been captured by %s!\n",
				shipDisplayName(target), s.raceByNum(p).Name))
		}

		s.deductAPs(p, g, 1, int(dir.StarNum), false)
		s.saveShip(attacker)
		s.saveShip(target)
		s.savePlanet(planet)
	}
}
