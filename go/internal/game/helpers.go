package game

// helpers.go — shared utilities used by multiple command files.
// Provides ship-list iteration, location parsing/formatting, and orbit
// chain management that were scattered across shlmisc.c, getplace.c,
// and GB_server.c in the C source.

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"

	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// ---- Authorization helpers -----------------------------------------------

// authorized mirrors authorized() in shlmisc.c:
// governor 0 is the race-level governor and can control all ships.
func authorized(governor int, ship *model.Ship) bool {
	return governor == 0 || int(ship.Governor) == governor
}

// controlStar mirrors control() in mobiliz.c:
// governor 0 can act in any star system; non-zero governors must match the
// system assignment.
func controlStar(playerNum, governor int, star *model.Star) bool {
	return governor == 0 || int(star.Governor[playerNum-1]) == governor
}

// ---- Speed / capability helpers ------------------------------------------

// speedRating returns true if the ship is rated for maneuvering.
// Mirrors speed_rating(s) macro: Shipdata[s->type][ABIL_SPEED] > 0.
func speedRating(ship *model.Ship) bool {
	return model.Shipdata[ship.Type][model.AbilSpeed] > 0
}

// ---- Distance helpers ----------------------------------------------------

// distSq returns the squared Euclidean distance between two points.
func distSq(x1, y1, x2, y2 float64) float64 {
	dx := x2 - x1
	dy := y2 - y1
	return dx*dx + dy*dy
}

// dist returns the Euclidean distance between two points.
func dist(x1, y1, x2, y2 float64) float64 {
	return math.Sqrt(distSq(x1, y1, x2, y2))
}

// ---- Orbit chain removal -------------------------------------------------

// removeShipPlan removes a ship from a planet's orbit linked list.
// Mirrors remove_sh_plan() in GB_server.c.
func (s *State) removeShipPlan(planet *model.Planet, ship *model.Ship) {
	shipNum := ship.Number
	if planet.Ships == shipNum {
		planet.Ships = ship.NextShip
		ship.NextShip = 0
		return
	}
	for cur := planet.Ships; cur != 0; {
		carrier, ok := s.ships[cur]
		if !ok {
			break
		}
		if carrier.NextShip == shipNum {
			carrier.NextShip = ship.NextShip
			ship.NextShip = 0
			return
		}
		cur = carrier.NextShip
	}
}

// removeShipStar removes a ship from a star's orbit linked list.
// Mirrors remove_sh_star() in GB_server.c.
func (s *State) removeShipStar(star *model.Star, ship *model.Ship) {
	shipNum := ship.Number
	if star.Ships == shipNum {
		star.Ships = ship.NextShip
		ship.NextShip = 0
		return
	}
	for cur := star.Ships; cur != 0; {
		carrier, ok := s.ships[cur]
		if !ok {
			break
		}
		if carrier.NextShip == shipNum {
			carrier.NextShip = ship.NextShip
			ship.NextShip = 0
			return
		}
		cur = carrier.NextShip
	}
}

// removeShipShip removes a ship from its carrier's docked-ships list.
// Mirrors remove_sh_ship() in GB_server.c.
func (s *State) removeShipShip(ship, carrier *model.Ship) {
	shipNum := ship.Number
	if carrier.Ships == shipNum {
		carrier.Ships = ship.NextShip
		ship.NextShip = 0
		ship.WhatOrbits = carrier.WhatOrbits
		ship.StOrbits = carrier.StOrbits
		ship.PNumOrbits = carrier.PNumOrbits
		return
	}
	for cur := carrier.Ships; cur != 0; {
		prev, ok := s.ships[cur]
		if !ok {
			break
		}
		if prev.NextShip == shipNum {
			prev.NextShip = ship.NextShip
			ship.NextShip = 0
			ship.WhatOrbits = carrier.WhatOrbits
			ship.StOrbits = carrier.StOrbits
			ship.PNumOrbits = carrier.PNumOrbits
			return
		}
		cur = prev.NextShip
	}
}

// ---- Crash check ---------------------------------------------------------

// crashRoll is set by crashCheck so callers can report the dice result.
var crashRoll int

// crashCheck returns true if the ship will crash when attempting to land
// with the given fuel requirement. Mirrors crash() in land.c.
func crashCheck(ship *model.Ship, fuel float64) bool {
	crashRoll = 0
	if ship.Fuel < fuel {
		return true
	}
	r := rand.Intn(100) + 1
	crashRoll = r
	if r <= int(ship.Damage) {
		return true
	}
	crashRoll = 0
	return false
}

// ---- Ship-list iteration -------------------------------------------------

// shipsBySpec returns the ships owned by playerNum that match the specifier.
// spec may be:
//   - "#NNN" or "NNN" — a single ship number
//   - "X" or "*" — type letters and wildcards; returns all matching ships
//     at the player's current scope level for that governor
//
// Mirrors start_shiplist / do_shiplist / in_list in shlmisc.c.
func (s *State) shipsBySpec(playerNum, governor int, spec string) []*model.Ship {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}

	// Single ship by number
	if spec[0] == '#' || (len(spec) > 0 && spec[0] >= '0' && spec[0] <= '9') {
		numStr := spec
		if spec[0] == '#' {
			numStr = spec[1:]
		}
		n, err := strconv.Atoi(numStr)
		if err != nil || n <= 0 {
			return nil
		}
		sh, ok := s.ships[uint32(n)]
		if !ok || !sh.Alive || int(sh.Owner) != playerNum {
			return nil
		}
		return []*model.Ship{sh}
	}

	// Type/wildcard spec — collect ships at current scope for this governor
	dir := s.dirs[playerNum-1][governor]
	var firstShip uint32
	switch dir.Level {
	case model.LevelUniv:
		firstShip = s.sdata.Ships
	case model.LevelStar:
		if int(dir.StarNum) < len(s.stars) && s.stars[dir.StarNum] != nil {
			firstShip = s.stars[dir.StarNum].Ships
		}
	case model.LevelPlan:
		if pl := s.planet(int(dir.StarNum), int(dir.PlanetNum)); pl != nil {
			firstShip = pl.Ships
		}
	case model.LevelShip:
		if sh, ok := s.ships[dir.ShipNo]; ok {
			firstShip = sh.Ships
		}
	}

	var result []*model.Ship
	for cur := firstShip; cur != 0; {
		sh, ok := s.ships[cur]
		if !ok {
			break
		}
		cur = sh.NextShip
		if !sh.Alive || int(sh.Owner) != playerNum {
			continue
		}
		if matchShipSpec(sh, spec) {
			result = append(result, sh)
		}
	}
	return result
}

// matchShipSpec returns true if the ship type letter matches the specifier.
func matchShipSpec(ship *model.Ship, spec string) bool {
	if spec == "*" {
		return true
	}
	if int(ship.Type) >= len(model.ShipLetters) {
		return false
	}
	letter := model.ShipLetters[ship.Type]
	for i := 0; i < len(spec); i++ {
		if spec[i] == '*' || spec[i] == byte(letter) {
			return true
		}
	}
	return false
}

// planet looks up a planet by star and planet number.
func (s *State) planet(starNum, planetNum int) *model.Planet {
	if m, ok := s.planets[uint8(starNum)]; ok {
		return m[uint8(planetNum)]
	}
	return nil
}

// ---- Location parsing / display -----------------------------------------

// getplace parses a location string and returns a model.Place.
// Mirrors Getplace() in getplace.c.
// ignoreExpl: if true, skip the "you haven't explored this" check.
func (s *State) getplace(playerNum, governor int, str string, ignoreExpl bool) model.Place {
	var where model.Place
	if len(str) == 0 {
		where.Err = true
		return where
	}

	race := s.raceByNum(playerNum)
	god := race != nil && race.God

	switch str[0] {
	case '/':
		where.Level = model.LevelUniv
		return s.getplace2(playerNum, governor, str[1:], where, ignoreExpl, god)
	case '#':
		n, err := strconv.Atoi(str[1:])
		if err != nil || n <= 0 {
			where.Err = true
			return where
		}
		sh, ok := s.ships[uint32(n)]
		if !ok {
			where.Err = true
			return where
		}
		if sh.Owner != uint8(playerNum) && !ignoreExpl && !god {
			where.Err = true
			return where
		}
		if !sh.Alive && !god {
			where.Err = true
			return where
		}
		where.Level = model.LevelShip
		where.ShipNo = uint32(n)
		where.StarNum = sh.StOrbits
		where.PlanetNum = sh.PNumOrbits
		return where
	case '-':
		where.Level = model.LevelUniv
		return where
	default:
		dir := s.dirs[playerNum-1][governor]
		where.Level = dir.Level
		where.StarNum = dir.StarNum
		where.PlanetNum = dir.PlanetNum
		where.ShipNo = dir.ShipNo
		if str[0] == '.' {
			// '.' means current scope — just return
			return where
		}
		return s.getplace2(playerNum, governor, str, where, ignoreExpl, god)
	}
}

// getplace2 is the recursive helper for getplace.
// Mirrors Getplace2() in getplace.c.
func (s *State) getplace2(playerNum, governor int, str string, where model.Place, ignoreExpl bool, god bool) model.Place {
	if where.Err || str == "" || str == "\n" {
		return where
	}
	if str[0] == '.' {
		// go up one level
		if where.Level == model.LevelUniv {
			where.Err = true
			return where
		}
		if where.Level == model.LevelShip {
			if sh, ok := s.ships[where.ShipNo]; ok {
				where.Level = sh.WhatOrbits
				if where.Level == model.LevelShip {
					where.ShipNo = sh.DestShipNo
				}
			} else {
				where.Level--
			}
		} else {
			where.Level--
		}
		// consume all dots and slashes
		i := 0
		for i < len(str) && (str[i] == '.' || str[i] == '/') {
			i++
		}
		return s.getplace2(playerNum, governor, str[i:], where, ignoreExpl, god)
	}

	// Extract next name component (up to '/' or end)
	end := strings.IndexAny(str, "/ \n")
	var component string
	var rest string
	if end < 0 {
		component = str
		rest = ""
	} else {
		component = str[:end]
		rest = str[end+1:]
	}
	if component == "" {
		return s.getplace2(playerNum, governor, rest, where, ignoreExpl, god)
	}

	switch where.Level {
	case model.LevelUniv:
		for i, star := range s.stars {
			if star == nil {
				continue
			}
			if strings.HasPrefix(strings.ToLower(star.Name), strings.ToLower(component)) {
				where.Level = model.LevelStar
				where.StarNum = uint8(i)
				if ignoreExpl || star.IsExplored(playerNum) || god {
					return s.getplace2(playerNum, governor, rest, where, ignoreExpl, god)
				}
				where.Err = true
				return where
			}
		}
		where.Err = true
		return where
	case model.LevelStar:
		star := s.stars[where.StarNum]
		if star == nil {
			where.Err = true
			return where
		}
		for i := 0; i < int(star.NumPlanets); i++ {
			pname := star.PlanetNames[i]
			if strings.HasPrefix(strings.ToLower(pname), strings.ToLower(component)) {
				where.Level = model.LevelPlan
				where.PlanetNum = uint8(i)
				pl := s.planet(int(where.StarNum), i)
				if ignoreExpl || (pl != nil && pl.Info[playerNum-1].Explored) || god {
					return s.getplace2(playerNum, governor, rest, where, ignoreExpl, god)
				}
				where.Err = true
				return where
			}
		}
		where.Err = true
		return where
	default:
		where.Err = true
		return where
	}
}

// dispplace returns a human-readable string for a Place.
// Mirrors Dispplace() in getplace.c.
func (s *State) dispplace(place model.Place) string {
	switch place.Level {
	case model.LevelStar:
		return fmt.Sprintf("/%s", s.starName(int(place.StarNum)))
	case model.LevelPlan:
		return fmt.Sprintf("/%s/%s",
			s.starName(int(place.StarNum)),
			s.planetName(int(place.StarNum), int(place.PlanetNum)))
	case model.LevelShip:
		return fmt.Sprintf("#%d", place.ShipNo)
	default:
		return "/"
	}
}

// dispShipLocFull returns a full location string for a ship.
// Mirrors Dispshiploc() in getplace.c.
func (s *State) dispShipLocFull(ship *model.Ship) string {
	switch ship.WhatOrbits {
	case model.LevelStar:
		return fmt.Sprintf("/%s", s.starName(int(ship.StOrbits)))
	case model.LevelPlan:
		return fmt.Sprintf("/%s/%s",
			s.starName(int(ship.StOrbits)),
			s.planetName(int(ship.StOrbits), int(ship.PNumOrbits)))
	case model.LevelShip:
		return fmt.Sprintf("#%d", ship.DestShipNo)
	default:
		return "/"
	}
}

// ---- Prompt building -----------------------------------------------------

// buildPrompt constructs the shell prompt string for a player/governor.
// Mirrors do_prompt() in cs.c.
func (s *State) buildPrompt(playerNum, governor int) string {
	dir := s.dirs[playerNum-1][governor]
	var ap int
	switch dir.Level {
	case model.LevelUniv:
		ap = int(s.sdata.AP[playerNum-1])
		return fmt.Sprintf(" ( [%d] / )\n", ap)
	case model.LevelStar:
		if int(dir.StarNum) < len(s.stars) && s.stars[dir.StarNum] != nil {
			ap = int(s.stars[dir.StarNum].AP[playerNum-1])
			return fmt.Sprintf(" ( [%d] /%s )\n", ap, s.starName(int(dir.StarNum)))
		}
	case model.LevelPlan:
		if int(dir.StarNum) < len(s.stars) && s.stars[dir.StarNum] != nil {
			ap = int(s.stars[dir.StarNum].AP[playerNum-1])
			return fmt.Sprintf(" ( [%d] /%s/%s )\n", ap,
				s.starName(int(dir.StarNum)),
				s.planetName(int(dir.StarNum), int(dir.PlanetNum)))
		}
	case model.LevelShip:
		sh, ok := s.ships[dir.ShipNo]
		if !ok {
			break
		}
		switch sh.WhatOrbits {
		case model.LevelUniv:
			ap = int(s.sdata.AP[playerNum-1])
			return fmt.Sprintf(" ( [%d] /#%d )\n", ap, dir.ShipNo)
		case model.LevelStar:
			ap = int(s.stars[sh.StOrbits].AP[playerNum-1])
			return fmt.Sprintf(" ( [%d] /%s/#%d )\n", ap,
				s.starName(int(sh.StOrbits)), dir.ShipNo)
		case model.LevelPlan:
			ap = int(s.stars[sh.StOrbits].AP[playerNum-1])
			return fmt.Sprintf(" ( [%d] /%s/%s/#%d )\n", ap,
				s.starName(int(sh.StOrbits)),
				s.planetName(int(sh.StOrbits), int(sh.PNumOrbits)),
				dir.ShipNo)
		}
	}
	return " ( [?] / )\n"
}

// ---- DB save helpers used by navigation commands -------------------------

// savePlanet persists a planet back to the DB.
func (s *State) savePlanet(planet *model.Planet) {
	_ = s.store.PutPlanet(planet)
}

// saveShip persists a ship back to the DB.
func (s *State) saveShip(ship *model.Ship) {
	_ = s.store.PutShip(ship)
}

// saveStar persists a star back to the DB.
func (s *State) saveStar(star *model.Star) {
	_ = s.store.PutStar(star)
}

// warnRace sends a telegram/notification to another race's default governor.
func (s *State) warnRace(playerNum int, text string) {
	s.notifyRace(playerNum, text)
}

// ---- Overloaded check ----------------------------------------------------

// overloaded returns true if the ship is carrying more than its limits allow.
// Mirrors overloaded() in land.c.
func overloaded(ship *model.Ship) bool {
	return int(ship.Resource) > shipMaxResource(ship) ||
		ship.Fuel > shipMaxFuel(ship) ||
		int(ship.Popn)+int(ship.Troops) > int(ship.MaxCrew) ||
		int(ship.Destruct) > shipMaxDestruct(ship)
}

// docked returns true if the ship is docked to another ship (not landed on a planet).
// Mirrors docked() in land.c.
func dockedToShip(ship *model.Ship) bool {
	return ship.Docked && ship.WhatDest == model.LevelShip
}

// ---- Receive troops helper -----------------------------------------------

// rcvTroops adds troops to a ship.
func (s *State) rcvTroops(ship *model.Ship, amt int, mass float64) {
	ship.Troops += uint16(amt)
	ship.Mass += float64(amt) * mass
}

// ---- Send output helpers used by commands --------------------------------

// sendf is a printf-style helper for sess.Send.
func sendf(sess net.Responder, format string, args ...interface{}) {
	sess.Send(fmt.Sprintf(format, args...))
}
