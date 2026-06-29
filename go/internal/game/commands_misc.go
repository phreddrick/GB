package game

// commands_misc.go — smaller commands: capital, zoom, orbit, autoreport,
// mobilize, dissolve, enslave, fuel, analysis, explore, map.
// Mirrors capital.c, zoom.c, orbit.c, autoreport.c, etc.

import (
	"fmt"
	"math"
	"strings"

	"github.com/gbgame/gb/internal/model"
	"github.com/gbgame/gb/internal/net"
)

// ---- cmdCapital ----------------------------------------------------------

// cmdCapital designates a government ship as the racial capital.
// Usage: capital [#shipno]
// Mirrors capital() in capital.c.
func (s *State) cmdCapital(p, g int, args []string, sess net.Responder) {
	if g != 0 {
		sess.Send("Only the leader may designate the capital.\n")
		return
	}
	race := s.raceByNum(p)
	if race == nil {
		return
	}

	var shipNo uint32
	if len(args) == 0 {
		shipNo = race.GovShip
	} else {
		str := args[0]
		if len(str) > 0 && str[0] == '#' {
			str = str[1:]
		}
		shipNo = uint32(atoi(str))
	}

	if shipNo == 0 {
		sess.Send("Change the capital to be what ship?\n")
		return
	}

	sh, ok := s.ships[shipNo]
	if !ok {
		sendf(sess, "Ship #%d not found.\n", shipNo)
		return
	}

	if len(args) > 0 {
		if errMsg := s.testShip(p, g, sh); errMsg != "" {
			sess.Send("You can't do that!\n")
			return
		}
		if !landed(sh) {
			sess.Send("Try landing this ship first!\n")
			return
		}
		snum := int(sh.StOrbits)
		if snum < 0 || snum >= len(s.stars) || s.stars[snum] == nil {
			return
		}
		if !s.enoughAP(p, g, s.stars[snum].AP[p-1], 1) {
			return
		}
		if sh.Type != model.OtypeGov {
			sendf(sess, "That ship is not a %s.\n", model.ShipNames[model.OtypeGov])
			return
		}
		s.deductAPs(p, g, 1, snum, false)
		race.GovShip = shipNo
		_ = s.store.PutRace(race)
	}

	maxCrew := int(sh.MaxCrew)
	if maxCrew == 0 {
		maxCrew = 1
	}
	eff := float64(sh.Popn) / float64(maxCrew) * float64(100-int(sh.Damage))
	sendf(sess, "Efficiency of governmental center: %.0f%%.\n", eff)
}

// ---- cmdZoom -------------------------------------------------------------

// cmdZoom adjusts the map zoom level.
// Usage: zoom [value] or zoom [num/denom]
// Mirrors zoom() in zoom.c.
func (s *State) cmdZoom(p, g int, args []string, sess net.Responder) {
	dir := &s.dirs[p-1][g]
	i := 0
	if dir.Level == model.LevelUniv {
		i = 1
	}

	if len(args) > 0 {
		var num, denom float64
		if n, _ := fmt.Sscanf(args[0], "%f/%f", &num, &denom); n == 2 {
			if denom == 0.0 {
				sess.Send("Illegal denominator value.\n")
			} else {
				dir.Zoom[i] = num / denom
			}
		} else {
			fmt.Sscanf(args[0], "%f", &num)
			dir.Zoom[i] = num
		}
	}

	sendf(sess, "Zoom value %g, lastx = %g, lasty = %g.\n",
		dir.Zoom[i], dir.LastX[i], dir.LastY[i])
}

// ---- cmdOrbit ------------------------------------------------------------

// cmdOrbit displays the orbit layout at the current scope.
// This is a simplified text representation — the C source drew an ASCII
// graphic using curses. We output a list of objects instead.
// Mirrors orbit() in orbit.c.
func (s *State) cmdOrbit(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]

	switch dir.Level {
	case model.LevelUniv:
		// List all known stars
		sess.Send("Universe orbit:\n")
		for _, star := range s.stars {
			if star == nil {
				continue
			}
			if !star.IsExplored(p) {
				race := s.raceByNum(p)
				if race == nil || !race.God {
					continue
				}
			}
			sess.Send(fmt.Sprintf("  %-10s  (%.0f, %.0f)  grav=%.2f  planets=%d\n",
				star.Name, star.XPos, star.YPos, star.Gravity, star.NumPlanets))
		}

	case model.LevelStar:
		if int(dir.StarNum) >= len(s.stars) || s.stars[dir.StarNum] == nil {
			return
		}
		star := s.stars[dir.StarNum]
		sendf(sess, "Star /%s orbit:\n", star.Name)
		for i := 0; i < int(star.NumPlanets); i++ {
			pl := s.planet(int(dir.StarNum), i)
			if pl == nil {
				continue
			}
			pname := star.PlanetNames[i]
			explored := ""
			if !pl.Info[p-1].Explored {
				explored = " (unexplored)"
			}
			sendf(sess, "  %-10s  pos=(%.1f,%.1f)  pop=%d%s\n",
				pname, pl.XPos, pl.YPos, pl.Popn, explored)
		}
		// List ships at this star
		s.listShipsAt(p, star.Ships, sess)

	case model.LevelPlan:
		if int(dir.StarNum) >= len(s.stars) || s.stars[dir.StarNum] == nil {
			return
		}
		pl := s.planet(int(dir.StarNum), int(dir.PlanetNum))
		if pl == nil {
			return
		}
		star := s.stars[dir.StarNum]
		sendf(sess, "Planet /%s/%s orbit:\n",
			star.Name, star.PlanetNames[dir.PlanetNum])
		sendf(sess, "  Population: %d  Troops: %d  Toxicity: %d%%\n",
			pl.Popn, pl.Troops, pl.Conditions[model.AtmosToxic])
		s.listShipsAt(p, pl.Ships, sess)

	case model.LevelShip:
		sh, ok := s.ships[dir.ShipNo]
		if !ok {
			return
		}
		sendf(sess, "Ship %s contents:\n", shipDisplayName(sh))
		s.listShipsAt(p, sh.Ships, sess)
	}
}

// listShipsAt lists ships in a linked list starting at firstShip.
func (s *State) listShipsAt(playerNum int, firstShip uint32, sess net.Responder) {
	race := s.raceByNum(playerNum)
	god := race != nil && race.God
	for cur := firstShip; cur != 0; {
		sh, ok := s.ships[cur]
		if !ok {
			break
		}
		cur = sh.NextShip
		if !sh.Alive {
			continue
		}
		if int(sh.Owner) != playerNum && !god {
			// Show name but mask owner if enemy
			sendf(sess, "  %-6s  %-12s  [unknown race]\n",
				shipDisplayName(sh), sh.Name)
			continue
		}
		sendf(sess, "  %-6s  %-12s  fuel=%.0f  crew=%d  dmg=%d%%\n",
			shipDisplayName(sh), sh.Name, sh.Fuel, sh.Popn, sh.Damage)
	}
}

// ---- cmdAutoreport -------------------------------------------------------

// cmdAutoreport toggles automatic planet reports each update.
// Usage: autoreport [place]
// Mirrors autoreport() in autoreport.c.
func (s *State) cmdAutoreport(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]

	var snum, pnum int
	if len(args) > 0 {
		where := s.getplace(p, g, args[0], false)
		if where.Err || where.Level != model.LevelPlan {
			sess.Send("Scope must be a planet.\n")
			return
		}
		snum = int(where.StarNum)
		pnum = int(where.PlanetNum)
	} else {
		if dir.Level != model.LevelPlan {
			sess.Send("Scope must be a planet.\n")
			return
		}
		snum = int(dir.StarNum)
		pnum = int(dir.PlanetNum)
	}

	pl := s.planet(snum, pnum)
	if pl == nil {
		sess.Send("Planet not found.\n")
		return
	}

	if !controlStar(p, g, s.stars[snum]) {
		sess.Send("You are not authorized to do this here.\n")
		return
	}

	if pl.Info[p-1].AutoRep > 0 {
		pl.Info[p-1].AutoRep = 0
	} else {
		pl.Info[p-1].AutoRep = uint8(s.cfg.TelegMaxAuto)
	}
	s.savePlanet(pl)

	status := "unset"
	if pl.Info[p-1].AutoRep > 0 {
		status = "set"
	}
	sendf(sess, "Autoreport on %s has been %s.\n",
		s.planetName(snum, pnum), status)
}

// ---- cmdMobilize ---------------------------------------------------------

// cmdMobilize sets the mobilization target for a planet.
// Usage: mobilize [target%]
// Mirrors mobilize() in mobiliz.c.
func (s *State) cmdMobilize(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]
	if dir.Level != model.LevelPlan {
		sess.Send("Scope must be a planet.\n")
		return
	}
	if int(dir.StarNum) >= len(s.stars) || s.stars[dir.StarNum] == nil {
		return
	}
	star := s.stars[dir.StarNum]
	if !controlStar(p, g, star) {
		sess.Send("You are not authorized to do that here.\n")
		return
	}
	if !s.enoughAP(p, g, star.AP[p-1], 1) {
		return
	}

	pl := s.planet(int(dir.StarNum), int(dir.PlanetNum))
	if pl == nil {
		return
	}

	if len(args) == 0 {
		sendf(sess, "Mobilization: %d%% (target: %d%%)\n",
			pl.Info[p-1].ComRead, pl.Info[p-1].MobSet)
		return
	}

	target := atoi(args[0])
	if target < 0 {
		target = 0
	}
	if target > 100 {
		target = 100
	}
	pl.Info[p-1].MobSet = uint8(target)
	s.savePlanet(pl)
	s.deductAPs(p, g, 1, int(dir.StarNum), false)
	sendf(sess, "Mobilization target set to %d%%.\n", target)
}

// ---- cmdDissolve ---------------------------------------------------------

// cmdDissolve dissolves the race (ends the game for this player).
// Usage: dissolve
// Mirrors dissolve() in dissolve.c.
func (s *State) cmdDissolve(p, g int, args []string, sess net.Responder) {
	if !s.cfg.Dissolve {
		sess.Send("Dissolve is not enabled on this server.\n")
		return
	}
	if g != 0 {
		sess.Send("Only the race leader may dissolve.\n")
		return
	}
	race := s.raceByNum(p)
	if race == nil {
		return
	}

	// Require confirmation word
	if len(args) == 0 || !strings.EqualFold(args[0], "confirm") {
		sess.Send("Usage: dissolve confirm\n")
		sess.Send("WARNING: This will permanently end your race!\n")
		return
	}

	race.Dissolved = true
	_ = s.store.PutRace(race)

	sess.Send("Your race has been dissolved. Farewell.\n")
	s.shout(fmt.Sprintf("Race %s has dissolved!\n", race.Name))
}

// ---- cmdEnslave ----------------------------------------------------------

// cmdEnslave attempts to enslave alien population on a planet sector.
// Usage: enslave [x,y]
// Mirrors enslave.c.
func (s *State) cmdEnslave(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]
	if dir.Level != model.LevelPlan {
		sess.Send("Scope must be a planet.\n")
		return
	}
	if int(dir.StarNum) >= len(s.stars) || s.stars[dir.StarNum] == nil {
		return
	}
	if !s.enoughAP(p, g, s.stars[dir.StarNum].AP[p-1], 1) {
		return
	}

	pl := s.planet(int(dir.StarNum), int(dir.PlanetNum))
	if pl == nil {
		return
	}

	var x, y int
	if len(args) > 0 {
		fmt.Sscanf(args[0], "%d,%d", &x, &y)
	} else {
		sess.Send("Usage: enslave <x,y>\n")
		return
	}

	if x < 0 || y < 0 || x >= int(pl.MaxX) || y >= int(pl.MaxY) {
		sess.Send("Illegal coordinates.\n")
		return
	}

	sect := pl.SectorAt(x, y)
	if sect.Owner == 0 || int(sect.Owner) == p {
		sess.Send("No alien population to enslave here.\n")
		return
	}

	race := s.raceByNum(p)
	if race == nil {
		return
	}

	// Check we have military force in the same sector
	var troopCount uint16
	for _, sh := range s.ships {
		if !sh.Alive || int(sh.Owner) != p {
			continue
		}
		if !landed(sh) || sh.StOrbits != dir.StarNum || sh.PNumOrbits != dir.PlanetNum {
			continue
		}
		if sh.LandX != uint8(x) || sh.LandY != uint8(y) {
			continue
		}
		troopCount += sh.Troops
	}
	if troopCount == 0 {
		sess.Send("You need troops in this sector to enslave.\n")
		return
	}

	alienOwner := int(sect.Owner)
	enslaved := sect.Popn
	sect.Owner = uint8(p)
	sect.Race = uint8(alienOwner)
	pl.Info[p-1].NumSectsOwned++
	if pl.Info[alienOwner-1].NumSectsOwned > 0 {
		pl.Info[alienOwner-1].NumSectsOwned--
	}

	s.deductAPs(p, g, 1, int(dir.StarNum), false)
	s.savePlanet(pl)

	sendf(sess, "Enslaved %d population in sector %d,%d.\n", enslaved, x, y)
	alienRace := s.raceByNum(alienOwner)
	if alienRace != nil {
		s.notifyRace(alienOwner, fmt.Sprintf("%s has enslaved your people at /%s/%s sector %d,%d!\n",
			race.Name,
			s.starName(int(dir.StarNum)),
			s.planetName(int(dir.StarNum), int(dir.PlanetNum)),
			x, y))
	}
}

// ---- cmdFuel -------------------------------------------------------------

// cmdFuel estimates fuel needed to travel to a destination.
// Usage: fuel #<shipno> [destination]
// Mirrors fuel() in fuel.c. This is a simplified version that computes
// straight-line distance fuel estimates rather than the full simulation loop.
func (s *State) cmdFuel(p, g int, args []string, sess net.Responder) {
	if len(args) == 0 {
		sess.Send("Usage: fuel <ship-spec> [destination]\n")
		return
	}

	ships := s.shipsBySpec(p, g, args[0])
	if len(ships) == 0 {
		sess.Send("No matching ships found.\n")
		return
	}

	for _, sh := range ships {
		s.showFuelEstimate(p, g, sh, args[1:], sess)
	}
}

func (s *State) showFuelEstimate(p, g int, sh *model.Ship, destArgs []string, sess net.Responder) {
	sendf(sess, "Ship %s: fuel=%.1f/%.0f\n",
		shipDisplayName(sh), sh.Fuel, float64(sh.MaxFuel))

	if len(destArgs) == 0 || sh.WhatDest == model.LevelUniv {
		sendf(sess, "  No destination set.\n")
		return
	}

	// Compute distance to destination
	var destX, destY float64
	switch sh.WhatDest {
	case model.LevelStar:
		snum := int(sh.DestStar)
		if snum < len(s.stars) && s.stars[snum] != nil {
			destX = s.stars[snum].XPos
			destY = s.stars[snum].YPos
		}
	case model.LevelPlan:
		snum := int(sh.DestStar)
		pnum := int(sh.DestPNum)
		if snum < len(s.stars) && s.stars[snum] != nil {
			destX = s.stars[snum].XPos
			destY = s.stars[snum].YPos
		}
		if pl := s.planet(snum, pnum); pl != nil {
			destX += pl.XPos
			destY += pl.YPos
		}
	case model.LevelShip:
		if destShip, ok := s.ships[sh.DestShipNo]; ok {
			destX = destShip.XPos
			destY = destShip.YPos
		}
	default:
		sess.Send("  No destination.\n")
		return
	}

	d := dist(sh.XPos, sh.YPos, destX, destY)
	speed := float64(sh.Speed)
	if speed < 1 {
		speed = 1
	}
	turnsNeeded := math.Ceil(d / speed)
	fuelPerTurn := sh.Mass * s.cfg.FuelUse * speed
	totalFuel := fuelPerTurn * turnsNeeded

	sendf(sess, "  Distance: %.1f  Speed: %.0f  Turns: %.0f\n", d, speed, turnsNeeded)
	sendf(sess, "  Estimated fuel needed: %.1f (have %.1f)\n", totalFuel, sh.Fuel)
	if totalFuel > sh.Fuel {
		sendf(sess, "  WARNING: Insufficient fuel by %.1f units!\n", totalFuel-sh.Fuel)
	} else {
		sendf(sess, "  Fuel OK (%.1f remaining after arrival)\n", sh.Fuel-totalFuel)
	}
}

// ---- cmdAnalysis ---------------------------------------------------------

// cmdAnalysis displays detailed analysis of the current scope.
// Usage: analysis
// Mirrors analysis.c (a report on player standings / sector breakdown).
func (s *State) cmdAnalysis(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]

	switch dir.Level {
	case model.LevelPlan:
		s.analysisPlanet(p, g, int(dir.StarNum), int(dir.PlanetNum), sess)
	case model.LevelStar:
		s.analysisStar(p, g, int(dir.StarNum), sess)
	default:
		s.analysisUniverse(p, g, sess)
	}
}

func (s *State) analysisPlanet(p, g, starNum, planetNum int, sess net.Responder) {
	pl := s.planet(starNum, planetNum)
	if pl == nil {
		return
	}
	star := s.stars[starNum]

	sendf(sess, "=== Analysis of /%s/%s ===\n",
		star.Name, star.PlanetNames[planetNum])
	sendf(sess, "Dimensions: %dx%d  Total sectors: %d\n",
		pl.MaxX, pl.MaxY, int(pl.MaxX)*int(pl.MaxY))
	sendf(sess, "Total population: %d  Troops: %d\n", pl.Popn, pl.Troops)
	sendf(sess, "Total resources: %d\n", pl.TotalResources)

	// Per-player breakdown
	for i := 0; i < model.MaxPlayers; i++ {
		if pl.Info[i].NumSectsOwned == 0 {
			continue
		}
		race := s.raceByNum(i + 1)
		raceName := fmt.Sprintf("player%d", i+1)
		if race != nil {
			raceName = race.Name
		}
		sendf(sess, "  %-20s: %d sectors, pop=%d, troops=%d, res=%d\n",
			raceName,
			pl.Info[i].NumSectsOwned,
			pl.Info[i].Popn,
			pl.Info[i].Troops,
			pl.Info[i].Resource)
	}
}

func (s *State) analysisStar(p, g, starNum int, sess net.Responder) {
	star := s.stars[starNum]
	if star == nil {
		return
	}
	sendf(sess, "=== Analysis of /%s ===\n", star.Name)
	sendf(sess, "Gravity: %.2f  Stability: %d  Temperature: %d\n",
		star.Gravity, star.Stability, star.Temperature)
	sendf(sess, "Planets: %d\n", star.NumPlanets)
	for i := 0; i < int(star.NumPlanets); i++ {
		pl := s.planet(starNum, i)
		if pl == nil {
			continue
		}
		sendf(sess, "  %-10s: pop=%d  resources=%d\n",
			star.PlanetNames[i], pl.Popn, pl.TotalResources)
	}
}

func (s *State) analysisUniverse(p, g int, sess net.Responder) {
	sess.Send("=== Universe Analysis ===\n")
	for _, race := range s.races {
		if race == nil || race.Dissolved {
			continue
		}
		sendf(sess, "  %-20s: tech=%.1f  planets=%d  morale=%d\n",
			race.Name, race.Tech,
			race.ControlledPlanets, race.Morale)
	}
}

// ---- cmdExplore ----------------------------------------------------------

// cmdExplore shows unexplored stars or sets exploration priorities.
// Usage: explore
// Mirrors explore.c.
func (s *State) cmdExplore(p, g int, args []string, sess net.Responder) {
	race := s.raceByNum(p)
	if race == nil {
		return
	}

	sess.Send("Unexplored stars:\n")
	count := 0
	for _, star := range s.stars {
		if star == nil {
			continue
		}
		if !star.IsExplored(p) {
			d := dist(0, 0, star.XPos, star.YPos)
			sendf(sess, "  %-10s  (%.0f, %.0f)  dist=%.0f\n",
				star.Name, star.XPos, star.YPos, d)
			count++
		}
	}
	if count == 0 {
		sess.Send("All stars have been explored.\n")
	} else {
		sendf(sess, "Total: %d unexplored star(s).\n", count)
	}
}
