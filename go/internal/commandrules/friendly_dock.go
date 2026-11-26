package commandrules

import (
	"fmt"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
)

// FriendlyDock describes an explicit numeric selection, independent of directory.
type FriendlyDock struct {
	Player, Governor int
	Source, Target   uint32
	Race             *model.Race
	Star             *model.Star
	Orbit            []*model.Ship
	DistToDock       float64
}

// ValidExteriorDockClaim checks a legacy outgoing claim before a read-only
// refusal. The named ship can have died, moved, or acquired a different peer.
func ValidExteriorDockClaim(ship, named *model.Ship) bool {
	return validUndockShip(ship) && validUndockShip(named) && ExteriorDockBack(ship, named.Number)
}

// PlanFriendlyDock has no caller-supplied after-images. Go's numeric selector
// requires ownership, but neither ship must be assigned to the logged-in governor.
func PlanFriendlyDock(in FriendlyDock) (*model.Ship, *model.Ship, error) {
	members, err := StarExterior(in.Star, in.Orbit)
	if err != nil {
		return nil, nil, err
	}
	if in.Player < 1 || in.Player > model.MaxPlayers || in.Governor < 0 || in.Governor > model.MaxGovernors || in.Race == nil || in.Race.PlayerNum != in.Player || !finiteCargoMass(in.DistToDock) {
		return nil, nil, ErrUndockStructure
	}
	a, b := members[in.Source], members[in.Target]
	if a == nil || b == nil {
		return nil, nil, ErrUndockOutside
	}
	if !a.Alive || !b.Alive || int(a.Owner) != in.Player || int(b.Owner) != in.Player || in.Race.Dissolved || !in.Race.Governor[in.Governor].Active || in.Governor > int(in.Race.Governors) {
		return nil, nil, &FactoryBuildRefusal{"You are not authorized to dock these ships.\n"}
	}
	if a.Number == b.Number {
		return nil, nil, &FactoryBuildRefusal{"You can't dock with yourself!\n"}
	}
	for _, sh := range []*model.Ship{a, b} {
		if sh.Docked {
			return nil, nil, &FactoryBuildRefusal{fmt.Sprintf("%c%d is already docked.\n", model.ShipLetters[sh.Type], sh.Number)}
		}
	}
	dx, dy := a.XPos-b.XPos, a.YPos-b.YPos
	if math.Sqrt(dx*dx+dy*dy) > in.DistToDock {
		return nil, nil, &FactoryBuildRefusal{fmt.Sprintf("%c%d must be within %.2f of %c%d.\n", model.ShipLetters[a.Type], a.Number, in.DistToDock, model.ShipLetters[b.Type], b.Number)}
	}
	source, target := *a, *b
	source.Docked, source.WhatDest, source.DestShipNo = true, model.LevelShip, b.Number
	target.Docked, target.WhatDest, target.DestShipNo = true, model.LevelShip, a.Number
	return &source, &target, nil
}
