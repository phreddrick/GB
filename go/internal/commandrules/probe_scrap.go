package commandrules

import (
	"errors"

	"github.com/phreddrick/GB/go/internal/model"
)

// ProbeScrap covers the zero-salvage case. The directory and the assigned
// ship governor do not constrain an explicit numeric Go scrap selection.
type ProbeScrap struct {
	Player, Governor int
	Selected         uint32
	Race             *model.Race
	Star             *model.Star
	Orbit            []*model.Ship
}

var ErrScrapAP = errors.New("insufficient star AP to scrap")

// SoloScrapProbe excludes every ship-side branch of scrap and killShip that
// would recover cargo, change morale, or affect another object. WhatDest on
// an undocked ship is an order, not evidence of a docking relationship.
func SoloScrapProbe(sh *model.Ship) bool {
	return sh != nil && sh.Type == model.OtypeProbe && sh.Alive && !sh.Docked && sh.WhatOrbits == model.LevelStar &&
		sh.MaxCrew == 0 && sh.Popn == 0 && sh.Troops == 0 && sh.Ships == 0
}

func PlanProbeScrap(in ProbeScrap) (*model.Ship, *model.Star, error) {
	members, err := StarExterior(in.Star, in.Orbit)
	if err != nil {
		return nil, nil, err
	}
	if in.Selected == 0 || in.Player < 1 || in.Player > model.MaxPlayers || in.Governor < 0 || in.Governor > model.MaxGovernors || in.Race == nil || in.Race.PlayerNum != in.Player {
		return nil, nil, ErrUndockStructure
	}
	sh := members[in.Selected]
	if sh == nil {
		return nil, nil, ErrUndockOutside
	}
	if !SoloScrapProbe(sh) || sh.Number == in.Race.GovShip {
		return nil, nil, ErrUndockStructure
	}
	if int(sh.Owner) != in.Player || in.Race.Dissolved || in.Governor > int(in.Race.Governors) || !in.Race.Governor[in.Governor].Active {
		return nil, nil, ErrUndockAuthority
	}
	if in.Star.AP[in.Player-1] == 0 {
		return nil, nil, ErrScrapAP
	}
	dead, star := *sh, *in.Star
	dead.WhoKilled, dead.Alive, dead.Notified = int16(in.Player), false, false
	star.AP[in.Player-1]--
	return &dead, &star, nil
}
