package game

import (
	"math"
	"reflect"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
)

// Admission is read-only. Otherwise valid actor/store differences can be normal
// legacy pending state; defer without guessing their writer or reconciling them.
// Valid actor witnesses can explain absent records or unlinked stored members
// after a partial legacy save. Decode errors, unknown gaps and malformed chains
// remain safety failures; no missing record is silently manufactured or written.
// All preimages are independently rechecked inside the eventual transaction.
func (s *State) walkAdmission(w *commandWorkspace, selected uint32) (bool, error) {
	pending := false
	bad := commandrules.ErrWalkInput
	ships := map[uint32]*model.Ship{}
	races := map[int]*model.Race{}
	missing := map[uint32]bool{}
	var free map[uint32]bool
	// Evidence is the already validated complete actor chain, compatible actual
	// location and no contradictory deletion/free-list entry. This is admission
	// deferral, not proof of writer intent, recovery or permission to write.
	acceptPartial := func(sh *model.Ship) error {
		actor, ok := w.ships[sh.Number]
		if !ok || !walkSameLocation(actor.before, sh) {
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
	readRace := func(id int) (*model.Race, error) {
		if r := races[id]; r != nil {
			return r, nil
		}
		if id < 1 || id > model.MaxPlayers {
			return nil, bad
		}
		r, e := s.store.GetRace(id)
		if e != nil {
			return nil, e
		}
		if id < 1 || id > model.MaxPlayers || r == nil || r.PlayerNum != id {
			return nil, bad
		}
		races[id] = r
		return r, nil
	}
	readShip := func(id uint32) (*model.Ship, error) {
		if sh := ships[id]; sh != nil {
			return sh, nil
		}
		if id == 0 {
			return nil, bad
		}
		sh, e := s.store.GetShip(id)
		if e != nil {
			return nil, e
		}
		if sh == nil {
			actor, ok := w.ships[id]
			if !ok {
				return nil, bad
			}
			if e := acceptPartial(actor.before); e != nil {
				return nil, e
			}
			// Private actor preimage is used only to continue validating partial
			// topology. pending is already true, so this cannot admit a transaction.
			sh = actor.before
			missing[id] = true
		}
		if sh.Number != id || int(sh.Type) >= len(model.Shipdata) || sh.WhatOrbits > model.LevelShip || int(sh.Governor) > model.MaxGovernors {
			return nil, bad
		}
		if _, e := readRace(int(sh.Owner)); e != nil {
			return nil, e
		}
		ships[id] = sh
		return sh, nil
	}
	stars := map[uint8]*model.Star{}
	for id, x := range w.stars {
		stored, e := s.store.GetStar(id)
		if e != nil {
			return false, e
		}
		if stored == nil || stored.Number != id || int(stored.NumPlanets) > model.MaxPlanets {
			return false, bad
		}
		stars[id] = stored
		pending = pending || !reflect.DeepEqual(stored, x.before)
	}
	for id, x := range w.ships {
		stored, e := readShip(id)
		if e != nil {
			return false, e
		}
		pending = pending || !reflect.DeepEqual(stored, x.before)
	}
	f := ships[selected]
	if f == nil || math.IsNaN(f.Fuel) || math.IsInf(f.Fuel, 0) || f.Fuel < 0 || math.IsNaN(f.Mass) || math.IsInf(f.Mass, 0) || f.Mass < 0 {
		return false, bad
	}
	for id, x := range w.planets {
		stored, e := s.store.GetPlanet(id[0], id[1])
		if e != nil {
			return false, e
		}
		st := stars[id[0]]
		if stored == nil || stored.StarNum != id[0] || stored.PlanetNum != id[1] || st == nil || stored.PlanetNum >= st.NumPlanets || stored.MaxX == 0 || stored.MaxY == 0 || len(stored.Sectors) != int(stored.MaxX)*int(stored.MaxY) {
			return false, bad
		}
		pending = pending || !reflect.DeepEqual(stored, x.before)
		// A stored root may legitimately differ from the actor's root. Walk the
		// entire stored root as well, rather than labeling a broken link "pending".
		seen := map[uint32]bool{}
		for n := stored.Ships; n != 0; {
			if seen[n] {
				return false, bad
			}
			seen[n] = true
			sh, e := readShip(n)
			if e != nil {
				return false, e
			}
			if sh.WhatOrbits != model.LevelPlan || sh.StOrbits != id[0] || sh.PNumOrbits != id[1] {
				return false, bad
			}
			n = sh.NextShip
		}
		if f.Alive && f.WhatOrbits == model.LevelPlan && f.StOrbits == id[0] && f.PNumOrbits == id[1] {
			if !missing[selected] && f.Docked && (f.LandX >= stored.MaxX || f.LandY >= stored.MaxY) {
				return false, bad
			}
		}
	}
	for id, x := range w.races {
		stored, e := readRace(id)
		if e != nil {
			return false, e
		}
		pending = pending || !reflect.DeepEqual(stored, x.before)
	}
	roots := map[walkStoredRoot]map[uint32]bool{}
	// readShip may cache additional parent/tail records during validation. Freeze
	// participants first so Go map insertion order cannot change classification;
	// recursively read records are checked at their location/tail read site.
	participants := make([]uint32, 0, len(ships))
	for id := range ships {
		participants = append(participants, id)
	}
	for _, id := range participants {
		sh := ships[id]
		if missing[id] {
			continue
		}
		if e := s.walkStoredLocation(sh, readShip, roots, map[uint32]bool{}, acceptPartial); e != nil {
			return false, e
		}
	}
	return pending, nil
}

// Validate actual persisted membership even if a legacy effect changed the
// ship's location relative to the actor. Dead ships may already be unlinked.
// Root caching bounds chain traversal; this helper never changes any record.
type walkStoredRoot struct {
	level, star, planet uint8
	parent              uint32
}

func (s *State) walkStoredLocation(sh *model.Ship, readShip func(uint32) (*model.Ship, error), roots map[walkStoredRoot]map[uint32]bool, parents map[uint32]bool, acceptPartial func(*model.Ship) error) error {
	bad := commandrules.ErrWalkInput
	if parents[sh.Number] {
		return bad
	}
	parents[sh.Number] = true
	defer delete(parents, sh.Number)
	key := walkStoredRoot{level: sh.WhatOrbits}
	var head uint32
	var pl *model.Planet
	switch sh.WhatOrbits {
	case model.LevelUniv:
		d, e := s.store.GetStarData()
		if e != nil {
			return e
		}
		if d == nil {
			return bad
		}
		head = d.Ships
	case model.LevelStar, model.LevelPlan:
		key.star = sh.StOrbits
		st, e := s.store.GetStar(sh.StOrbits)
		if e != nil {
			return e
		}
		if st == nil || st.Number != sh.StOrbits || int(st.NumPlanets) > model.MaxPlanets {
			return bad
		}
		head = st.Ships
		if sh.WhatOrbits == model.LevelPlan {
			key.planet = sh.PNumOrbits
			pl, e = s.store.GetPlanet(sh.StOrbits, sh.PNumOrbits)
			if e != nil {
				return e
			}
			if pl == nil || pl.StarNum != sh.StOrbits || pl.PlanetNum != sh.PNumOrbits || pl.PlanetNum >= st.NumPlanets || pl.MaxX == 0 || pl.MaxY == 0 || len(pl.Sectors) != int(pl.MaxX)*int(pl.MaxY) {
				return bad
			}
			head = pl.Ships
		}
	case model.LevelShip:
		key.parent = sh.DestShipNo
		parent, e := readShip(sh.DestShipNo)
		if e != nil {
			return e
		}
		if e = s.walkStoredLocation(parent, readShip, roots, parents, acceptPartial); e != nil {
			return e
		}
		head = parent.Ships
	default:
		return bad
	}
	seen, ok := roots[key]
	if !ok {
		seen = map[uint32]bool{}
		for n := head; n != 0; {
			if seen[n] {
				return bad
			}
			seen[n] = true
			x, e := readShip(n)
			if e != nil {
				return e
			}
			if x.WhatOrbits != key.level {
				return bad
			}
			switch key.level {
			case model.LevelStar:
				if x.StOrbits != key.star {
					return bad
				}
			case model.LevelPlan:
				if x.StOrbits != key.star || x.PNumOrbits != key.planet {
					return bad
				}
			case model.LevelShip:
				if x.DestShipNo != key.parent {
					return bad
				}
			}
			n = x.NextShip
		}
		roots[key] = seen
	}
	if sh.Alive && !seen[sh.Number] {
		if e := acceptPartial(sh); e != nil {
			return e
		}
		// An omitted head can explain missing membership, but must not conceal
		// a cycle, unknown link or incompatible location in the unlinked tail.
		tail := map[uint32]bool{}
		for x := sh; !seen[x.Number]; {
			if tail[x.Number] || !walkSameLocation(sh, x) {
				return bad
			}
			tail[x.Number] = true
			if x.NextShip == 0 {
				break
			}
			var e error
			x, e = readShip(x.NextShip)
			if e != nil {
				return e
			}
		}
	}
	if sh.Alive && sh.Docked && pl != nil && (sh.LandX >= pl.MaxX || sh.LandY >= pl.MaxY) {
		return bad
	}
	return nil
}

func walkSameLocation(a, b *model.Ship) bool {
	if a == nil || b == nil || a.WhatOrbits != b.WhatOrbits {
		return false
	}
	switch a.WhatOrbits {
	case model.LevelUniv:
		return true
	case model.LevelStar:
		return a.StOrbits == b.StOrbits
	case model.LevelPlan:
		return a.StOrbits == b.StOrbits && a.PNumOrbits == b.PNumOrbits
	case model.LevelShip:
		return a.DestShipNo == b.DestShipNo
	default:
		return false
	}
}
