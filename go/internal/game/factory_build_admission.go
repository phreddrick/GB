package game

import (
	"reflect"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
)

// Read-only admission: valid pending legacy changes defer, not HALT or repair.
// Both stored and actor topology are validated, including stored tails omitted
// by a partial save. CAS remains strict inside the eventual write transaction.
func (s *State) factoryBuildAdmission(w *commandWorkspace) (bool, error) {
	return s.constructionAdmission(w, 0)
}

func (s *State) constructionAdmission(w *commandWorkspace, carrier uint32) (bool, error) {
	bad := commandrules.ErrFactoryBuildInput
	pending := false
	storedShips := map[uint32]*model.Ship{}
	missing := map[uint32]bool{}
	var free map[uint32]bool
	acceptPartial := func(sh *model.Ship) error {
		a, ok := w.ships[sh.Number]
		if !ok || !walkSameLocation(a.before, sh) {
			return bad
		}
		if free == nil {
			nums, e := s.store.FreeShipNums()
			if e != nil {
				return e
			}
			free = map[uint32]bool{}
			for _, n := range nums {
				free[n] = true
			}
		}
		if free[sh.Number] {
			return bad
		}
		pending = true
		return nil
	}
	readShip := func(id uint32) (*model.Ship, error) {
		if sh := storedShips[id]; sh != nil {
			return sh, nil
		}
		sh, e := s.store.GetShip(id)
		if e != nil {
			return nil, e
		}
		if sh == nil {
			a, ok := w.ships[id]
			if !ok {
				return nil, bad
			}
			if e := acceptPartial(a.before); e != nil {
				return nil, e
			}
			sh = a.before
			missing[id] = true
		}
		if sh.Number != id || id == 0 || int(sh.Type) >= model.NumShipTypes || sh.Owner == 0 || int(sh.Owner) > model.MaxPlayers || int(sh.Governor) > model.MaxGovernors || sh.WhatOrbits > model.LevelShip {
			return nil, bad
		}
		storedShips[id] = sh
		return sh, nil
	}
	// A persisted link in a probe build must resolve to a persisted record.
	// Actor-only records may still explain an unpersisted change of head.
	readLinked := func(id uint32) (*model.Ship, error) {
		sh, err := readShip(id)
		if carrier != 0 && missing[id] {
			return nil, bad
		}
		return sh, err
	}
	for id, r := range w.races {
		q, e := s.store.GetRace(id)
		if e != nil {
			return false, e
		}
		if q == nil || q.PlayerNum != id {
			return false, bad
		}
		pending = pending || !reflect.DeepEqual(q, r.before)
	}
	for key, p := range w.planets {
		q, e := s.store.GetPlanet(key[0], key[1])
		if e != nil {
			return false, e
		}
		st, e := s.store.GetStar(key[0])
		if e != nil {
			return false, e
		}
		if q == nil || q.StarNum != key[0] || q.PlanetNum != key[1] || st == nil || st.Number != key[0] {
			return false, bad
		}
		pending = pending || !reflect.DeepEqual(q, p.before) || !reflect.DeepEqual(st, w.stars[key[0]].before)
		var chain []*model.Ship
		seen := map[uint32]bool{}
		for n := q.Ships; n != 0; {
			if seen[n] {
				return false, bad
			}
			seen[n] = true
			sh, e := readLinked(n)
			if e != nil {
				return false, e
			}
			chain = append(chain, sh)
			n = sh.NextShip
		}
		if e := commandrules.FactoryBuildChain(st, q, chain); e != nil {
			return false, e
		}
	}
	for id, a := range w.ships {
		sh, e := readShip(id)
		if e != nil {
			return false, e
		}
		pending = pending || !reflect.DeepEqual(sh, a.before)
	}
	// Follow the stored hold even when an older/different head is absent from
	// the actor's witnesses. A broken stored tail is not an ordinary deferral.
	roots := map[walkStoredRoot]map[uint32]bool{}
	var landedInHold map[uint32]bool
	if carrier != 0 {
		b, e := readShip(carrier)
		if e != nil {
			return false, e
		}
		children, e := constructionChain(b.Ships, readLinked)
		if e != nil {
			return false, e
		}
		hold, err := commandrules.ProbeChildren(b, children)
		if err != nil && err != commandrules.ErrProbeHoldUnsettled {
			return false, err
		}
		if err == commandrules.ErrProbeHoldUnsettled {
			if err := checkOtherLandingOrbits(b, children, s.store.GetStar, s.store.GetPlanet, readLinked); err != nil {
				return false, err
			}
			// Check the Carrier's own persisted planet chain. A child still
			// linked there as well as in the hold is structural corruption.
			if err := s.walkStoredLocation(b, readShip, roots, map[uint32]bool{}, acceptPartial); err != nil {
				return false, err
			}
			exterior := roots[walkStoredRoot{level: model.LevelPlan, star: b.StOrbits, planet: b.PNumOrbits}]
			landedInHold = map[uint32]bool{}
			for _, child := range children {
				if exterior[child.Number] {
					return false, bad
				}
				if child.WhatOrbits == model.LevelPlan {
					landedInHold[child.Number] = true
				}
			}
			// The entire hold was checked above, including ordinary siblings.
			roots[walkStoredRoot{level: model.LevelShip, parent: b.Number}] = hold
		}
	}
	ids := make([]uint32, 0, len(storedShips))
	for id := range storedShips {
		ids = append(ids, id)
	}
	for _, id := range ids {
		if !missing[id] && !landedInHold[id] {
			if e := s.walkStoredLocation(storedShips[id], readShip, roots, map[uint32]bool{}, acceptPartial); e != nil {
				return false, e
			}
		}
	}
	return pending, nil
}
