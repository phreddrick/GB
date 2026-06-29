package game

// commands_cs.go — change scope (cs) command.
// Mirrors cs.c from the C source.

import (
	"fmt"
	"strings"

	"github.com/gbgame/gb/internal/model"
	"github.com/gbgame/gb/internal/net"
)

// cmdCS implements the "cs" (change scope) command.
// Usage:
//   cs              — return to default home scope
//   cs <place>      — change to a new scope
//   cs -d <place>   — set the default home scope
func (s *State) cmdCS(p, g int, args []string, sess net.Responder) {
	race := s.raceByNum(p)
	if race == nil {
		sess.Send("cs: unknown player.\n")
		return
	}

	dir := &s.dirs[p-1][g]

	if len(args) == 0 {
		// Return to default scope
		dir.Level = race.Governor[g].DefLevel
		snum := int(race.Governor[g].DefSystem)
		if snum >= len(s.stars) || s.stars[snum] == nil {
			snum = 0
		}
		dir.StarNum = uint8(snum)
		pnum := int(race.Governor[g].DefPlanetNum)
		if int(dir.StarNum) < len(s.stars) && s.stars[dir.StarNum] != nil &&
			pnum >= int(s.stars[dir.StarNum].NumPlanets) {
			if s.stars[dir.StarNum].NumPlanets > 0 {
				pnum = int(s.stars[dir.StarNum].NumPlanets) - 1
			} else {
				pnum = 0
			}
		}
		dir.PlanetNum = uint8(pnum)
		dir.ShipNo = 0
		dir.LastX[0] = 0.0
		dir.LastY[0] = 0.0
		if int(dir.StarNum) < len(s.stars) && s.stars[dir.StarNum] != nil {
			dir.LastX[1] = s.stars[dir.StarNum].XPos
			dir.LastY[1] = s.stars[dir.StarNum].YPos
		}
		dir.Prompt = s.buildPrompt(p, g)
		return
	}

	// "cs -d <place>" — set new default scope
	if len(args) >= 2 && strings.ToLower(args[0]) == "-d" {
		where := s.getplace(p, g, args[1], false)
		if !where.Err && where.Level != model.LevelShip {
			race.Governor[g].DefLevel = where.Level
			race.Governor[g].DefSystem = where.StarNum
			race.Governor[g].DefPlanetNum = where.PlanetNum
			_ = s.store.PutRace(race)
			sess.Send(fmt.Sprintf("New home system is %s\n", s.dispplace(where)))
		} else {
			sess.Send("cs: bad home system.\n")
		}
		return
	}

	// "cs <place>" — change to specified scope
	where := s.getplace(p, g, args[0], false)
	if where.Err {
		sess.Send("cs: bad scope.\n")
		dir.LastX[0] = 0.0
		dir.LastY[0] = 0.0
		return
	}

	// Update lastx/lasty coordinates based on direction of travel
	switch dir.Level {
	case model.LevelUniv:
		dir.LastX[0] = 0.0
		dir.LastY[0] = 0.0
	case model.LevelStar:
		if where.Level == model.LevelUniv {
			if int(dir.StarNum) < len(s.stars) && s.stars[dir.StarNum] != nil {
				dir.LastX[1] = s.stars[dir.StarNum].XPos
				dir.LastY[1] = s.stars[dir.StarNum].YPos
			}
		} else {
			dir.LastX[0] = 0.0
			dir.LastY[0] = 0.0
		}
	case model.LevelPlan:
		pl := s.planet(int(dir.StarNum), int(dir.PlanetNum))
		if where.Level == model.LevelStar && where.StarNum == dir.StarNum {
			if pl != nil {
				dir.LastX[0] = pl.XPos
				dir.LastY[0] = pl.YPos
			}
		} else if where.Level == model.LevelUniv {
			if pl != nil && int(dir.StarNum) < len(s.stars) && s.stars[dir.StarNum] != nil {
				dir.LastX[1] = s.stars[dir.StarNum].XPos + pl.XPos
				dir.LastY[1] = s.stars[dir.StarNum].YPos + pl.YPos
			}
		} else {
			dir.LastX[0] = 0.0
			dir.LastY[0] = 0.0
		}
	case model.LevelShip:
		sh, ok := s.ships[dir.ShipNo]
		if ok && !sh.Docked {
			switch where.Level {
			case model.LevelUniv:
				dir.LastX[1] = sh.XPos
				dir.LastY[1] = sh.YPos
			case model.LevelStar:
				if sh.WhatOrbits >= model.LevelStar &&
					sh.StOrbits == where.StarNum &&
					int(sh.StOrbits) < len(s.stars) &&
					s.stars[sh.StOrbits] != nil {
					dir.LastX[0] = sh.XPos - s.stars[sh.StOrbits].XPos
					dir.LastY[0] = sh.YPos - s.stars[sh.StOrbits].YPos
				} else {
					dir.LastX[0] = 0.0
					dir.LastY[0] = 0.0
				}
			case model.LevelPlan:
				if sh.WhatOrbits == model.LevelPlan &&
					sh.StOrbits == where.StarNum &&
					sh.PNumOrbits == where.PlanetNum {
					pl := s.planet(int(sh.StOrbits), int(sh.PNumOrbits))
					if pl != nil && int(sh.StOrbits) < len(s.stars) &&
						s.stars[sh.StOrbits] != nil {
						dir.LastX[0] = sh.XPos - s.stars[sh.StOrbits].XPos - pl.XPos
						dir.LastY[0] = sh.YPos - s.stars[sh.StOrbits].YPos - pl.YPos
					}
				} else {
					dir.LastX[0] = 0.0
					dir.LastY[0] = 0.0
				}
			default:
				dir.LastX[0] = 0.0
				dir.LastY[0] = 0.0
			}
		} else {
			dir.LastX[0] = 0.0
			dir.LastY[0] = 0.0
		}
	}

	dir.Level = where.Level
	dir.StarNum = where.StarNum
	dir.PlanetNum = where.PlanetNum
	dir.ShipNo = where.ShipNo
	dir.Prompt = s.buildPrompt(p, g)
}
