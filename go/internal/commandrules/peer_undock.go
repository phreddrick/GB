package commandrules

import (
	"errors"

	"github.com/phreddrick/GB/go/internal/model"
)

// StarPeerUndock witnesses one numeric selection and the entire exterior chain.
// Directory scope does not constrain an explicit ship number.
type StarPeerUndock struct {
	Player, Governor int
	Selected         uint32
	Race             *model.Race
	Star             *model.Star
	Orbit            []*model.Ship
	MovedPeer        *model.Ship // named row in a different orbit; read-only
	DetachedWreck    *model.Ship // only when the named dead peer was removed by the turn rebuild
	WreckChildren    []*model.Ship
}

var (
	ErrUndockStructure = errors.New("invalid star peer undock witnesses")
	ErrUndockOutside   = errors.New("ship absent from star orbit")
	ErrUndockAuthority = errors.New("not authorized to undock this ship")
	ErrUndockUnsettled = errors.New("unsettled peer docking")
)

func validUndockShip(sh *model.Ship) bool {
	return sh != nil && sh.Number != 0 && sh.WhatOrbits <= model.LevelShip &&
		sh.Owner != 0 && int(sh.Owner) <= model.MaxPlayers && int(sh.Governor) <= model.MaxGovernors &&
		int(sh.Type) < model.NumShipTypes && sh.WhatDest <= model.LevelShip
}

func validStarMember(star *model.Star, sh *model.Ship) bool {
	return validUndockShip(sh) && sh.WhatOrbits == model.LevelStar && sh.StOrbits == star.Number
}

// StarExterior validates every link, including dead ships still awaiting the turn rebuild.
func StarExterior(star *model.Star, orbit []*model.Ship) (map[uint32]*model.Ship, error) {
	if star == nil || int(star.Number) >= model.NumStars || int(star.NumPlanets) > model.MaxPlanets {
		return nil, ErrUndockStructure
	}
	members := make(map[uint32]*model.Ship, len(orbit))
	next := star.Ships
	for _, sh := range orbit {
		if !validStarMember(star, sh) || next != sh.Number || members[next] != nil {
			return nil, ErrUndockStructure
		}
		members[next] = sh
		next = sh.NextShip
	}
	for _, sh := range orbit {
		if sh.Ships != 0 && members[sh.Ships] != nil {
			return nil, ErrUndockStructure
		}
	}
	if next != 0 {
		return nil, ErrUndockStructure
	}
	return members, nil
}

// DetachedStarWreck checks exterior detachment separately from the wreck's
// hangar. A turn can reinsert surviving children into their dead parent.
func DetachedStarWreck(st *model.Star, members map[uint32]*model.Ship, sh *model.Ship, children []*model.Ship) bool {
	if !validStarMember(st, sh) || sh.Alive || sh.NextShip != 0 || members[sh.Number] != nil {
		return false
	}
	hold, err := ProbeChildren(sh, children)
	if err != nil && err != ErrProbeHoldUnsettled {
		return false
	}
	for _, member := range members {
		if hold[member.Number] || member.Ships == sh.Number || hold[member.Ships] {
			return false
		}
	}
	return true
}

func PlanStarPeerUndock(in StarPeerUndock) (selected, partner *model.Ship, err error) {
	members, err := StarExterior(in.Star, in.Orbit)
	if err != nil {
		return nil, nil, err
	}
	a := members[in.Selected]
	if in.Player < 1 || in.Player > model.MaxPlayers || in.Governor < 0 || in.Governor > model.MaxGovernors || in.Race == nil || in.Race.PlayerNum != in.Player {
		return nil, nil, ErrUndockStructure
	}
	if a == nil {
		return nil, nil, ErrUndockOutside
	}
	if !a.Alive || int(a.Owner) != in.Player || in.Race.Dissolved || !in.Race.Governor[in.Governor].Active || (in.Governor != 0 && int(a.Governor) != in.Governor) {
		return nil, nil, ErrUndockAuthority
	}
	if !a.Docked || a.WhatDest != model.LevelShip {
		return nil, nil, ErrUndockUnsettled
	}
	b := members[a.DestShipNo]
	if wreck := in.DetachedWreck; wreck != nil {
		if wreck.Number != a.DestShipNo || !DetachedStarWreck(in.Star, members, wreck, in.WreckChildren) {
			return nil, nil, ErrUndockStructure
		}
		b = wreck
	} else if len(in.WreckChildren) != 0 {
		return nil, nil, ErrUndockStructure
	}
	if moved := in.MovedPeer; moved != nil {
		// The remote list is outside the write set. Only the named ship's
		// location and outgoing link can determine whether it is a peer.
		if b != nil || moved.Number != a.DestShipNo || !MovedStarPeer(in.Star, members, moved) {
			return nil, nil, ErrUndockStructure
		}
		// In a hold DestShipNo identifies the parent, even when it happens
		// to name the selected ship. It is never a reciprocal exterior dock.
		if ExteriorDockBack(moved, a.Number) {
			return nil, nil, ErrUndockUnsettled
		}
		return releasedStarPeer(a), nil, nil
	}
	if b == nil {
		return nil, nil, ErrUndockOutside
	}
	if b.Owner != a.Owner && ExteriorDockBack(b, a.Number) {
		return nil, nil, ErrUndockUnsettled
	}
	selected = releasedStarPeer(a)
	// A ship owns its outgoing dock link. Only a live reciprocal peer
	// has a second link to release. Preserve orders and links on all others.
	if b != a && ExteriorDockBack(b, a.Number) {
		partner = releasedStarPeer(b)
	}
	return selected, partner, nil
}

// MovedStarPeer checks the off-list row against the entire selected exterior.
// Its remote list is never edited or relied upon as an insertion point.
func MovedStarPeer(st *model.Star, members map[uint32]*model.Ship, sh *model.Ship) bool {
	if !validUndockShip(sh) || validStarMember(st, sh) || members[sh.Number] != nil ||
		sh.NextShip == sh.Number || sh.Ships == sh.Number || members[sh.NextShip] != nil || members[sh.Ships] != nil {
		return false
	}
	if sh.WhatOrbits == model.LevelPlan && int(sh.PNumOrbits) >= model.MaxPlanets {
		return false
	}
	if sh.WhatOrbits == model.LevelShip && (sh.DestShipNo == 0 || sh.DestShipNo == sh.Number) {
		return false
	}
	for _, member := range members {
		if member.Ships == sh.Number && (sh.WhatOrbits != model.LevelShip || sh.DestShipNo != member.Number) {
			return false
		}
	}
	return true
}

// ExteriorDockBack distinguishes an actual peer link from containment and orders.
func ExteriorDockBack(sh *model.Ship, selected uint32) bool {
	return sh.WhatOrbits != model.LevelShip && sh.Alive && sh.Docked && sh.WhatDest == model.LevelShip && sh.DestShipNo == selected
}

func releasedStarPeer(sh *model.Ship) *model.Ship {
	released := *sh
	released.Docked, released.WhatDest, released.DestShipNo = false, model.LevelUniv, 0
	return &released
}
