package game

// commands_profile.go — toggle, profile, and name commands.
// Mirrors toggle.c, prof.c, and name.c from the C source.

import (
	"fmt"
	"math"
	"strings"

	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// ---- cmdToggle -----------------------------------------------------------

// cmdToggle flips governor display/behavior options.
// Usage: toggle [option]
// Mirrors toggle() in toggle.c.
func (s *State) cmdToggle(p, g int, args []string, sess net.Responder) {
	race := s.raceByNum(p)
	if race == nil {
		return
	}
	gov := &race.Governor[g]

	onOff := func(b bool) string {
		if b {
			return "ON"
		}
		return "OFF"
	}

	if len(args) == 0 {
		sendf(sess, "gag is %s\n", onOff(gov.Toggle.Gag))
		sendf(sess, "inverse is %s\n", onOff(gov.Toggle.Inverse))
		sendf(sess, "double_digits is %s\n", onOff(gov.Toggle.DoubleDigits))
		sendf(sess, "geography is %s\n", onOff(gov.Toggle.Geography))
		sendf(sess, "autoload is %s\n", onOff(gov.Toggle.AutoLoad))
		sendf(sess, "color is %s\n", onOff(gov.Toggle.Color))
		sendf(sess, "compatibility is %s\n", onOff(gov.Toggle.Compat))
		if gov.Toggle.Invisible {
			sess.Send("INVISIBLE\n")
		} else {
			sess.Send("VISIBLE\n")
		}
		sendf(sess, "highlight player %d\n", gov.Toggle.Highlight)
		if race.God {
			sendf(sess, "monitor is %s\n", onOff(race.Monitor))
		}
		return
	}

	opt := strings.ToLower(args[0])
	switch {
	case strings.HasPrefix("inverse", opt):
		gov.Toggle.Inverse = !gov.Toggle.Inverse
		sendf(sess, "inverse is now %s\n", onOff(gov.Toggle.Inverse))
	case strings.HasPrefix("double_digits", opt):
		gov.Toggle.DoubleDigits = !gov.Toggle.DoubleDigits
		sendf(sess, "double_digits is now %s\n", onOff(gov.Toggle.DoubleDigits))
	case strings.HasPrefix("geography", opt):
		gov.Toggle.Geography = !gov.Toggle.Geography
		sendf(sess, "geography is now %s\n", onOff(gov.Toggle.Geography))
	case strings.HasPrefix("gag", opt):
		gov.Toggle.Gag = !gov.Toggle.Gag
		sendf(sess, "gag is now %s\n", onOff(gov.Toggle.Gag))
	case strings.HasPrefix("autoload", opt):
		gov.Toggle.AutoLoad = !gov.Toggle.AutoLoad
		sendf(sess, "autoload is now %s\n", onOff(gov.Toggle.AutoLoad))
	case strings.HasPrefix("color", opt):
		gov.Toggle.Color = !gov.Toggle.Color
		sendf(sess, "color is now %s\n", onOff(gov.Toggle.Color))
	case strings.HasPrefix("visible", opt):
		gov.Toggle.Invisible = !gov.Toggle.Invisible
		if gov.Toggle.Invisible {
			sess.Send("invisible is now ON\n")
		} else {
			sess.Send("visible is now ON\n")
		}
	case strings.HasPrefix("compatibility", opt):
		gov.Toggle.Compat = !gov.Toggle.Compat
		sendf(sess, "compatibility is now %s\n", onOff(gov.Toggle.Compat))
	case strings.HasPrefix("monitor", opt) && race.God:
		race.Monitor = !race.Monitor
		sendf(sess, "monitor is now %s\n", onOff(race.Monitor))
	default:
		sendf(sess, "No such option '%s'\n", args[0])
		return
	}
	_ = s.store.PutRace(race)
}

// ---- cmdProfile ----------------------------------------------------------

// cmdProfile shows the racial profile.
// Usage: profile [player-number ...]
// Mirrors profile() / whois() in prof.c.
func (s *State) cmdProfile(p, g int, args []string, sess net.Responder) {
	if len(args) == 0 {
		s.showProfile(p, g, p, sess)
		return
	}
	for _, arg := range args {
		n := atoi(arg)
		if n < 1 || n > model.MaxPlayers {
			sendf(sess, "Identify: Invalid player number #%d.\n", n)
			continue
		}
		s.showProfile(p, g, n, sess)
	}
}

func (s *State) showProfile(viewer, viewerGov, target int, sess net.Responder) {
	race := s.raceByNum(target)
	if race == nil {
		sendf(sess, "Player %d not found.\n", target)
		return
	}

	viewerRace := s.raceByNum(viewer)
	god := viewerRace != nil && viewerRace.God

	if target == viewer {
		// Full profile for yourself
		gov := &race.Governor[viewerGov]
		sendf(sess, "[%2d, %d] %s \"%s\"\n", target, viewerGov, race.Name, gov.Name)
		sendf(sess, "Tech level: %.1f\n", race.Tech)
		sendf(sess, "Morale: %d\n", race.Morale)
		sendf(sess, "Money: %d  Income: %d  Maintain: %d\n",
			gov.Money, gov.Income, gov.Maintain)
		sendf(sess, "Controlled planets: %d\n", race.ControlledPlanets)
		return
	}

	// Limited profile for other players
	if god {
		sendf(sess, "[%2d] %s  tech=%.1f  morale=%d\n",
			target, race.Name, race.Tech, race.Morale)
	} else {
		sendf(sess, "[%2d] %s\n", target, race.Name)
	}
}

// ---- cmdName -------------------------------------------------------------

// cmdName renames a ship or planet.
// Usage: name <#shipno|planet> <newname>
// Mirrors name.c (simplified).
func (s *State) cmdName(p, g int, args []string, sess net.Responder) {
	if len(args) < 2 {
		sess.Send("Usage: name <#shipno> <new-name>\n")
		return
	}

	target := args[0]
	newName := strings.Join(args[1:], " ")

	if len(target) == 0 {
		return
	}

	if target[0] == '#' {
		// Rename a ship
		shipNo := uint32(atoi(target[1:]))
		sh, ok := s.ships[shipNo]
		if !ok || !sh.Alive {
			sendf(sess, "Ship #%d not found.\n", shipNo)
			return
		}
		if int(sh.Owner) != p {
			sess.Send("You don't own that ship.\n")
			return
		}
		if errMsg := s.testShip(p, g, sh); errMsg != "" {
			sess.Send(errMsg)
			return
		}
		oldName := sh.Name
		sh.Name = newName
		s.saveShip(sh)
		sendf(sess, "%s renamed to '%s'.\n", oldName, newName)
		return
	}

	// Rename a planet (must be at planet scope)
	dir := s.dirs[p-1][g]
	if dir.Level != model.LevelPlan {
		sess.Send("Change scope to a planet first, or use #shipno for ships.\n")
		return
	}
	if int(dir.StarNum) >= len(s.stars) || s.stars[dir.StarNum] == nil {
		return
	}
	star := s.stars[dir.StarNum]
	if !controlStar(p, g, star) {
		sess.Send("You are not authorized to rename planets here.\n")
		return
	}
	if !s.enoughAP(p, g, star.AP[p-1], 1) {
		return
	}
	oldName := star.PlanetNames[dir.PlanetNum]
	star.PlanetNames[dir.PlanetNum] = newName
	s.saveStar(star)
	s.deductAPs(p, g, 1, int(dir.StarNum), false)
	sendf(sess, "Planet %s renamed to %s.\n", oldName, newName)
}

// ---- cmdMax --------------------------------------------------------------

// cmdMax shows the maximum/capacity values for a ship.
// Usage: max <#shipno>
// Mirrors max.c.
func (s *State) cmdMax(p, g int, args []string, sess net.Responder) {
	if len(args) == 0 {
		sess.Send("Usage: max <#shipno>\n")
		return
	}

	ships := s.shipsBySpec(p, g, args[0])
	if len(ships) == 0 {
		sess.Send("No matching ships found.\n")
		return
	}

	for _, sh := range ships {
		sendf(sess, "=== %s (%s) ===\n", shipDisplayName(sh), sh.Name)
		sendf(sess, "  Fuel:     %.1f / %.0f\n", sh.Fuel, float64(sh.MaxFuel))
		sendf(sess, "  Resource: %d / %d\n", sh.Resource, shipMaxResource(sh))
		sendf(sess, "  Destruct: %d / %d\n", sh.Destruct, shipMaxDestruct(sh))
		sendf(sess, "  Crew:     %d / %d\n", sh.Popn, sh.MaxCrew)
		sendf(sess, "  Troops:   %d\n", sh.Troops)
		sendf(sess, "  Hanger:   %d / %d\n", sh.Hanger, sh.MaxHanger)
		sendf(sess, "  Speed:    %d / %d\n", sh.Speed, sh.MaxSpeed)
		sendf(sess, "  Damage:   %d%%\n", sh.Damage)
		sendf(sess, "  Mass:     %.2f\n", sh.Mass)
		if sh.Crystals > 0 || sh.Mounted {
			sendf(sess, "  Crystals: %d%s\n", sh.Crystals,
				map[bool]string{true: " (+1 mounted)", false: ""}[sh.Mounted])
		}
	}
}

// ---- cmdSurvey -----------------------------------------------------------

// cmdSurvey shows the sector map for the current planet scope.
// Usage: survey
// Mirrors survey.c.
func (s *State) cmdSurvey(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]
	if dir.Level != model.LevelPlan {
		sess.Send("Scope must be a planet to survey.\n")
		return
	}
	pl := s.planet(int(dir.StarNum), int(dir.PlanetNum))
	if pl == nil {
		return
	}
	if !pl.Info[p-1].Explored {
		sess.Send("You have not explored this planet yet.\n")
		return
	}

	star := s.stars[dir.StarNum]
	sendf(sess, "=== /%s/%s  (%dx%d) ===\n",
		star.Name, star.PlanetNames[dir.PlanetNum],
		pl.MaxX, pl.MaxY)
	sendf(sess, "%-4s", "")
	for x := 0; x < int(pl.MaxX); x++ {
		sendf(sess, "%2d", x)
	}
	sess.Send("\n")

	sectorChars := []byte{'.', 'L', '^', 'G', 'I', 'F', 'D', '#', 'W'}
	for y := 0; y < int(pl.MaxY); y++ {
		sendf(sess, "%3d ", y)
		for x := 0; x < int(pl.MaxX); x++ {
			sec := pl.SectorAt(x, y)
			ch := byte('?')
			if int(sec.Type) < len(sectorChars) {
				ch = sectorChars[sec.Type]
			}
			if sec.Condition == model.SectorWasted {
				ch = 'W'
			}
			if sec.Owner > 0 {
				// Owned sectors shown as player number or '*' for self
				if int(sec.Owner) == p {
					ch = '*'
				} else {
					ch = '+'
				}
			}
			sendf(sess, " %c", ch)
		}
		sess.Send("\n")
	}

	// Legend
	sess.Send("\nKey: . sea  L land  ^ mountain  G gas  I ice  F forest  D desert  # plated  W wasted\n")
	sess.Send("     * your territory  + alien territory\n")
	sendf(sess, "Population: %d  Troops: %d  Resources: %d\n",
		pl.Info[p-1].Popn, pl.Info[p-1].Troops, pl.Info[p-1].Resource)
}

// ---- cmdMap --------------------------------------------------------------

// cmdMap shows a star-level or universe-level map.
// Usage: map [place]
// Mirrors map.c.
func (s *State) cmdMap(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]

	var mapLevel uint8
	var starNum int
	if len(args) > 0 {
		where := s.getplace(p, g, args[0], false)
		if where.Err {
			sess.Send("map: bad location.\n")
			return
		}
		mapLevel = where.Level
		starNum = int(where.StarNum)
	} else {
		mapLevel = dir.Level
		starNum = int(dir.StarNum)
	}

	switch mapLevel {
	case model.LevelUniv:
		s.mapUniverse(p, g, sess)
	case model.LevelStar, model.LevelPlan:
		s.mapStar(p, g, starNum, sess)
	default:
		s.mapUniverse(p, g, sess)
	}
}

func (s *State) mapUniverse(p, g int, sess net.Responder) {
	race := s.raceByNum(p)
	god := race != nil && race.God

	sess.Send("Universe map:\n")
	for _, star := range s.stars {
		if star == nil {
			continue
		}
		if !star.IsExplored(p) && !god {
			continue
		}
		inhabited := ""
		if star.IsInhabited(p) {
			inhabited = " *"
		}
		sendf(sess, "  %-12s (%.0f, %.0f) grav=%.2f np=%d%s\n",
			star.Name, star.XPos, star.YPos,
			star.Gravity, star.NumPlanets, inhabited)
	}
}

func (s *State) mapStar(p, g, starNum int, sess net.Responder) {
	if starNum < 0 || starNum >= len(s.stars) || s.stars[starNum] == nil {
		return
	}
	star := s.stars[starNum]
	race := s.raceByNum(p)
	god := race != nil && race.God

	sendf(sess, "Star /%s — grav=%.2f  stability=%d  temp=%d  nova=%d\n",
		star.Name, star.Gravity, star.Stability, star.Temperature, star.NovaStage)

	for i := 0; i < int(star.NumPlanets); i++ {
		pl := s.planet(starNum, i)
		if pl == nil {
			continue
		}
		explored := ""
		if !pl.Info[p-1].Explored && !god {
			explored = " (unexplored)"
		}
		planetTypes := []string{"Class M", "Asteroid", "Airless", "Iceball",
			"Jovian", "Waterball", "Forest", "Desert"}
		ptname := "unknown"
		if int(pl.Type) < len(planetTypes) {
			ptname = planetTypes[pl.Type]
		}
		sendf(sess, "  %d. %-10s  %-10s  pos=(%.1f,%.1f)  pop=%d%s\n",
			i+1, star.PlanetNames[i], ptname,
			pl.XPos, pl.YPos, pl.Popn, explored)
	}

	// List ships at this star
	s.listShipsAt(p, star.Ships, sess)
}

// ---- cmdVictory ----------------------------------------------------------

// cmdVictory shows the current victory standings.
// Usage: victory
func (s *State) cmdVictory(p, g int, args []string, sess net.Responder) {
	if !s.cfg.Victory {
		sess.Send("Victory conditions are not enabled on this server.\n")
		return
	}

	sess.Send("=== Victory Standings ===\n")
	for _, race := range s.races {
		if race == nil || race.Dissolved {
			continue
		}
		sendf(sess, "  [%2d] %-20s  score=%d  tech=%.1f  planets=%d\n",
			race.PlayerNum, race.Name, race.VictoryScore,
			race.Tech, race.ControlledPlanets)
	}
}

// ---- cmdRST --------------------------------------------------------------

// cmdRST (race status turn) shows a comprehensive status report.
// Usage: rst
// Mirrors rst.c.
func (s *State) cmdRST(p, g int, args []string, sess net.Responder) {
	race := s.raceByNum(p)
	if race == nil {
		return
	}
	gov := &race.Governor[g]

	sess.Send("=== Race Status Report ===\n")
	sendf(sess, "Race: %s  Player: %d  Governor: %d (%s)\n",
		race.Name, p, g, gov.Name)
	sendf(sess, "Tech: %.2f  Morale: %d  Turn: %d\n",
		race.Tech, race.Morale, race.Turn)
	sendf(sess, "Money: %d  Income: %d  Maintain: %d\n",
		gov.Money, gov.Income, gov.Maintain)
	sendf(sess, "Controlled planets: %d\n", race.ControlledPlanets)

	// Count ships
	shipCount := 0
	for _, sh := range s.ships {
		if sh.Alive && int(sh.Owner) == p && authorized(g, sh) {
			shipCount++
		}
	}
	sendf(sess, "Ships under command: %d\n", shipCount)

	// Diplomacy summary
	sess.Send("\nDiplomacy:\n")
	for _, other := range s.races {
		if other == nil || other.PlayerNum == p {
			continue
		}
		bit := uint64(1) << uint(other.PlayerNum-1)
		if race.AtWar&bit != 0 {
			sendf(sess, "  WAR     with %s\n", other.Name)
		} else if race.Allied&bit != 0 {
			sendf(sess, "  ALLIED  with %s\n", other.Name)
		}
	}
	sess.Send("\n")

	// Current scope
	dir := s.dirs[p-1][g]
	sendf(sess, "Current scope: %s\n", s.dispplace(model.Place{
		Level:     dir.Level,
		StarNum:   dir.StarNum,
		PlanetNum: dir.PlanetNum,
		ShipNo:    dir.ShipNo,
	}))
}

// ---- cmdDock -------------------------------------------------------------

// cmdDock docks two ships together (cross-docking, not landing).
// Usage: dock <ship-spec> #<shipno>
// Mirrors dock.c (simplified — full boarding/assault is complex).
func (s *State) cmdDock(p, g int, args []string, sess net.Responder) {
	if len(args) < 2 {
		sess.Send("Usage: dock <ship-spec> #<target-shipno>\n")
		return
	}

	ships := s.shipsBySpec(p, g, args[0])
	if len(ships) == 0 {
		sess.Send("No matching ships found.\n")
		return
	}

	targetStr := args[1]
	if len(targetStr) > 0 && targetStr[0] == '#' {
		targetStr = targetStr[1:]
	}
	targetNo := uint32(atoi(targetStr))
	target, ok := s.ships[targetNo]
	if !ok || !target.Alive {
		sendf(sess, "Ship #%d not found.\n", targetNo)
		return
	}

	for _, sh := range ships {
		s.doDockOne(p, g, sh, target, sess)
	}
}

func (s *State) doDockOne(p, g int, sh, target *model.Ship, sess net.Responder) {
	if sh.Docked {
		sendf(sess, "%s is already docked.\n", shipDisplayName(sh))
		return
	}
	if sh.WhatOrbits != target.WhatOrbits {
		sess.Send("Ships are not in the same scope.\n")
		return
	}

	d := dist(sh.XPos, sh.YPos, target.XPos, target.YPos)
	if d > s.cfg.DistToDock {
		sendf(sess, "%s must be within %.2f of %s.\n",
			shipDisplayName(sh), s.cfg.DistToDock, shipDisplayName(target))
		return
	}

	// Friendly docking
	if int(target.Owner) == p {
		sh.Docked = true
		sh.WhatDest = model.LevelShip
		sh.DestShipNo = target.Number
		target.Docked = true
		target.WhatDest = model.LevelShip
		target.DestShipNo = sh.Number
		sendf(sess, "%s docked with %s.\n", shipDisplayName(sh), shipDisplayName(target))
		s.saveShip(sh)
		s.saveShip(target)
		return
	}

	// ---- Assault docking ------------------------------------------------
	// Mirrors dock() with Assault=1 in dock.c.

	if target.Type == model.OtypeVN {
		sess.Send("You can't assault Von Neumann machines.\n")
		return
	}

	race := s.races[p-1]
	alien := s.races[target.Owner-1]
	if race == nil || alien == nil {
		sess.Send("Race data unavailable.\n")
		return
	}

	// Determine assault type: default military, else civilians.
	what := "mil"
	var boarders int
	if sh.Troops > 0 {
		boarders = int(sh.Troops)
	} else if sh.Popn > 0 {
		boarders = int(sh.Popn)
		what = "civ"
	} else {
		sess.Send("You have no crew or troops to assault with.\n")
		return
	}
	if int(target.MaxCrew) > 0 && boarders > int(target.MaxCrew) {
		boarders = int(target.MaxCrew)
	}
	if int(target.MaxCrew) > 0 && boarders <= 0 {
		sendf(sess, "Illegal number of boarders (%d).\n", boarders)
		return
	}

	// Snapshot the old owner before combat alters the ship.
	old2owner := int(target.Owner)
	old2gov := int(target.Governor)

	// Fuel cost (doubled for assault).
	fuel := 0.05 + d*0.025*2.0*math.Sqrt(sh.Mass)
	if fuel > sh.Fuel {
		sess.Send("Not enough fuel.\n")
		return
	}

	sendf(sess, "Distance to %s: %.2f.\n", shipDisplayName(target), d)
	sendf(sess, "This maneuver will take %.2f fuel (of %.2f.)\n\n", fuel, sh.Fuel)

	// Defender fires first (single shot, mode 3 = defensive fire).
	var defLong, defShort string
	s.shootShipToShip(target, sh, s.retalStrength(target), 0, 0, &defLong, &defShort)
	if !sh.Alive {
		// Attacker destroyed; abort.
		s.saveShip(sh)
		s.saveShip(target)
		sess.Send("The defender's fire destroyed your ship before boarding!\n")
		return
	}
	if !target.Alive {
		s.saveShip(sh)
		s.saveShip(target)
		sess.Send("The defender's return fire somehow destroyed their own ship.\n")
		return
	}

	// Remove boarders from attacker now (re-added on retreat).
	if what == "mil" {
		sh.Troops -= uint16(boarders)
	} else {
		sh.Popn -= uint16(boarders)
	}
	sh.Mass -= float64(boarders) * race.Mass

	// Calculate boarding strengths.
	var fighters float64
	if what == "mil" {
		fighters = 10.0 * float64(race.Fighters)
	} else {
		fighters = 1.0
	}
	bstrength := float64(boarders) * fighters *
		0.01 * race.Tech *
		moraleFactor(float64(race.Morale-alien.Morale))

	b2strength := (float64(target.Popn) + 10.0*float64(target.Troops)*float64(alien.Fighters)) *
		0.01 * alien.Tech *
		moraleFactor(float64(alien.Morale-race.Morale))

	sendf(sess, "Boarding strength :%.2f       Defense strength: %.2f.\n",
		bstrength, b2strength)

	// Move into position.
	sh.Fuel -= fuel
	sh.XPos = target.XPos + float64(intRand(-1, 1))
	sh.YPos = target.YPos + float64(intRand(-1, 1))
	if sh.HyperDrive.On {
		sh.HyperDrive.On = false
		sess.Send("Hyper-drive deactivated.\n")
	}

	// Undock target if it was docked somewhere else.
	if target.Docked && target.WhatDest == model.LevelShip {
		if prev, ok := s.ships[target.DestShipNo]; ok {
			prev.Docked = false
			prev.WhatDest = model.LevelUniv
			prev.DestShipNo = 0
			s.saveShip(prev)
		}
		target.Docked = false
		target.WhatDest = model.LevelUniv
		target.DestShipNo = 0
	}

	// Combat casualties.
	casualtyScale := boarders
	if int(target.Troops)+int(target.Popn) < casualtyScale {
		casualtyScale = int(target.Troops) + int(target.Popn)
	}

	dam, dam2, booby := 0, 0, 0
	casualties, casualties2, casualties3 := 0, 0, 0

	if b2strength > 0 {
		casualties = intRand(0, roundRand(float64(casualtyScale)*(b2strength+1.0)/(bstrength+1.0)))
		if casualties > boarders {
			casualties = boarders
		}
		boarders -= casualties

		dam = intRand(0, roundRand(25.0*(b2strength+1.0)/(bstrength+1.0)))
		if dam > 100 {
			dam = 100
		}
		sh.Damage += uint8(dam)
		if sh.Damage >= 100 {
			s.killShip(p, sh)
		}

		casualties2 = intRand(0, roundRand(float64(casualtyScale)*(bstrength+1.0)/(b2strength+1.0)))
		if int(casualties2) > int(target.Popn) {
			casualties2 = int(target.Popn)
		}
		casualties3 = intRand(0, roundRand(float64(casualtyScale)*(bstrength+1.0)/(b2strength+1.0)))
		if casualties3 > int(target.Troops) {
			casualties3 = int(target.Troops)
		}
		target.Popn -= uint16(casualties2)
		target.Mass -= float64(casualties2) * alien.Mass
		target.Troops -= uint16(casualties3)
		target.Mass -= float64(casualties3) * alien.Mass

		dam2 = intRand(0, roundRand(25.0*(bstrength+1.0)/(b2strength+1.0)))
		if dam2 > 100 {
			dam2 = 100
		}
		target.Damage += uint8(dam2)
		if target.Damage >= 100 {
			s.killShip(old2owner, target)
		}
	} else {
		// Ship surrenders without a fight.
		target.Popn = 0
		target.Troops = 0
		if target.MaxCrew == 0 && target.Destruct > 0 {
			booby = intRand(0, 10*int(target.Destruct))
			if booby > 100 {
				booby = 100
			}
		}
	}

	// Resolve outcome.
	telegramBuf := fmt.Sprintf("%s ASSAULTED by %s at star #%d\n",
		shipDisplayName(target), shipDisplayName(sh), sh.StOrbits)
	telegramBuf += fmt.Sprintf("Your damage: %d%%, theirs: %d%%.\n", dam2, dam)

	if target.MaxCrew == 0 && target.Destruct > 0 {
		telegramBuf += fmt.Sprintf("(Your boobytrap gave them %d%% damage.)\n", booby)
		sh.Damage += uint8(booby)
		if sh.Damage >= 100 {
			s.killShip(p, sh)
		}
		sendf(sess, "Their boobytrap gave you %d%% damage!\n", booby)
	}

	sendf(sess, "Damage taken:  You: %d%% (now %d%%)\n", dam, sh.Damage)
	if !sh.Alive {
		sess.Send("              YOUR SHIP WAS DESTROYED!!!\n")
		telegramBuf += "              Their ship DESTROYED!!!\n"
	}
	sendf(sess, "              Them: %d%% (now %d%%)\n", dam2, target.Damage)
	if !target.Alive {
		sess.Send("              Their ship DESTROYED!!!  Boarders are dead.\n")
		telegramBuf += "              YOUR SHIP WAS DESTROYED!!!\n"
	}

	if sh.Alive {
		if target.Popn == 0 && target.Troops == 0 {
			// Captured.
			sh.Docked = true
			sh.WhatDest = model.LevelShip
			sh.DestShipNo = target.Number

			target.Docked = true
			target.WhatDest = model.LevelShip
			target.DestShipNo = sh.Number
			target.Owner = uint8(p)
			target.Governor = uint8(g)

			if what == "mil" {
				target.Troops = uint16(boarders)
			} else {
				target.Popn = uint16(boarders)
			}
			target.Mass += float64(boarders) * race.Mass

			if casualties2+casualties3 > 0 {
				adjustMorale(race, alien, int(target.BuildCost))
			}
			telegramBuf += "CAPTURED!\n"
			sess.Send("VICTORY! the ship is yours!\n")
			if boarders > 0 {
				sendf(sess, "%d boarders move in.\n", boarders)
			}
			s.captureStuff(target)
		} else {
			// Retreat — return boarders.
			if what == "mil" {
				sh.Troops += uint16(boarders)
			} else {
				sh.Popn += uint16(boarders)
			}
			sh.Mass += float64(boarders) * race.Mass
			adjustMorale(alien, race, int(race.Fighters))
			sess.Send("The boarding was repulsed; try again.\n")
			telegramBuf += "You fought them off!\n"
		}
	} else {
		sess.Send("The assault was too much for your bucket of bolts.\n")
		telegramBuf += "The assault was too much for their ship..\n"
	}

	if target.Alive {
		if target.MaxCrew > 0 && boarders == 0 {
			sess.Send("Oh no! They killed your boarding party to the last man!\n")
		}
		if sh.Popn == 0 && sh.Troops == 0 {
			telegramBuf += "You killed all their crew!\n"
		}
	} else {
		sess.Send("The assault weakened their ship too much!\n")
		telegramBuf += "Your ship was weakened too much!\n"
	}

	telegramBuf += fmt.Sprintf("Casualties: Yours: %d mil/%d civ    Theirs: %d %s\n",
		casualties3, casualties2, casualties, what)
	sendf(sess, "Crew casualties: Yours: %d %s    Theirs: %d mil/%d civ\n",
		casualties, what, casualties3, casualties2)

	// Translate boost from first contact.
	if int(alien.Translate[p-1])+5 > 100 {
		alien.Translate[p-1] = 100
	} else {
		alien.Translate[p-1] += 5
	}
	if int(race.Translate[old2owner-1])+5 > 100 {
		race.Translate[old2owner-1] = 100
	} else {
		race.Translate[old2owner-1] += 5
	}
	if boarders == 0 && (target.Popn+target.Troops) > 0 {
		if int(alien.Translate[p-1])+25 > 100 {
			alien.Translate[p-1] = 100
		} else {
			alien.Translate[p-1] += 25
		}
	}
	if int(target.Owner) == p {
		if int(race.Translate[old2owner-1])+25 > 100 {
			race.Translate[old2owner-1] = 100
		} else {
			race.Translate[old2owner-1] += 25
		}
	}
	_ = s.store.PutRace(race)
	_ = s.store.PutRace(alien)

	// Warn old owner.
	s.pushTelegram(old2owner, old2gov, telegramBuf)

	// Broadcast to star.
	outcome := "assaulted"
	if !target.Alive {
		outcome = "DESTROYED"
	} else if int(target.Owner) == p {
		outcome = "CAPTURED"
	}
	combatMsg := fmt.Sprintf("%s %s %s at star #%d.\n",
		shipDisplayName(sh), outcome, shipDisplayName(target), sh.StOrbits)
	if int(target.Owner) == p || !target.Alive {
		s.postCombat(combatMsg)
	}
	s.notifyStar(p, g, old2owner, int(sh.StOrbits), combatMsg)

	s.saveShip(sh)
	s.saveShip(target)
}

// Ensure fmt is used (suppress unused import error if all funcs were stubs).
var _ = fmt.Sprintf
