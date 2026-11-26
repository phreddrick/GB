package game

import (
	"reflect"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
)

// An absent selection can justify a refusal, never a write. Check both ends
// of a dock: they may belong to different detached runs or historical holds.
func (w *commandWorkspace) absentStarSelections(st *model.Star, members map[uint32]*model.Ship, ids ...uint32) error {
	for _, id := range ids {
		if members[id] == nil {
			if err := w.checkDetachedStarMember(st, members, id); err != commandrules.ErrUndockUnsettled {
				return err
			}
		}
	}
	return commandrules.ErrUndockUnsettled
}

// The same read-only topology check can witness the actor's workspace or
// independently read saved rows. Root traversal never treats a dock claim as
// a physical list edge.
type starExteriorView struct {
	ships   map[uint32]*model.Ship
	stars   []*model.Star
	planets map[uint8]map[uint8]*model.Planet
	sdata   *model.StarData
	ship    func(uint32) (*model.Ship, error)
}

func (w *commandWorkspace) starExteriorView() starExteriorView {
	s := w.s
	return starExteriorView{ships: s.ships, stars: s.stars, planets: s.planets, sdata: s.sdata, ship: w.ship}
}

func (w *commandWorkspace) checkDetachedStarMember(st *model.Star, members map[uint32]*model.Ship, id uint32) error {
	return w.starExteriorView().checkDetachedStarMember(st, members, id)
}

// Legacy exterior construction saves the new ships before the star head.
// A later prepend can leave the run pointing into the middle of the current
// list. The producer can build several designs; eligibility to scrap a probe
// is checked by the scrap command, independently of this read-only walk.
func (v starExteriorView) checkDetachedStarMember(st *model.Star, members map[uint32]*model.Ship, id uint32) error {
	run, err := constructionChain(id, v.ship)
	if err != nil {
		return err
	}
	ids := make(map[uint32]bool, len(run))
	for _, sh := range run {
		ids[sh.Number] = true
	}
	// Detect intersections even at an interior sibling. An unrooted incoming
	// NextShip (e.g. another ship from the same build) is not a second list.
	intersects := func(head uint32) bool {
		for steps := 0; head != 0 && steps < len(v.ships); steps++ {
			if ids[head] {
				return true
			}
			sh := v.ships[head]
			if sh == nil || sh.Number != head {
				break
			}
			head = sh.NextShip
		}
		return false
	}
	if v.sdata == nil || intersects(v.sdata.Ships) {
		return commandrules.ErrUndockStructure
	}
	for n, star := range v.stars {
		if n != int(st.Number) && star != nil && intersects(star.Ships) {
			return commandrules.ErrUndockStructure
		}
	}
	for _, planets := range v.planets {
		for _, pl := range planets {
			if pl != nil && intersects(pl.Ships) {
				return commandrules.ErrUndockStructure
			}
		}
	}
	var holder uint32
	for n, parent := range v.ships {
		if parent == nil || !intersects(parent.Ships) {
			continue
		}
		if holder != 0 || n == 0 || n == id || parent.Number != n || !v.contains(parent.Ships, id) {
			return commandrules.ErrUndockStructure
		}
		holder = n
	}
	if holder != 0 {
		return v.checkHistoricalStarHold(st, members, run[0], holder)
	}

	head := *st
	head.Ships = id
	if _, err := commandrules.StarExterior(&head, run); err != nil {
		return err
	}
	for _, sh := range run {
		if members[sh.Number] != nil {
			break // the suffix was validated from the current star head
		}
		if !sh.Alive || sh.Ships != 0 {
			return commandrules.ErrUndockStructure
		}
		if sh.Docked {
			// Type selection can dock with a detached target. Its outgoing
			// peer claim is a witness, not an exterior or hangar list edge.
			peer, err := v.ship(sh.DestShipNo)
			if err != nil || !commandrules.ValidExteriorDockClaim(sh, peer) {
				return commandrules.ErrUndockStructure
			}
		}
	}
	return commandrules.ErrUndockUnsettled // includes a detached run ending at zero
}

func (v starExteriorView) contains(head, id uint32) bool {
	for n, steps := head, 0; n != 0 && steps < len(v.ships); steps++ {
		sh := v.ships[n]
		if sh == nil || sh.Number != n {
			return false
		}
		if n == id {
			return true
		}
		n = sh.NextShip
	}
	return false
}

// Some historical landings put a ship in a hold but preserved its star and
// order fields. Only a complete, disjoint hold can explain that absence.
func (v starExteriorView) checkHistoricalStarHold(st *model.Star, members map[uint32]*model.Ship, ship *model.Ship, holder uint32) error {
	if (!ship.Alive && ship.NextShip == 0) || ship.WhatOrbits != model.LevelStar || ship.StOrbits != st.Number {
		return commandrules.ErrUndockUnsettled
	}
	if !ship.Alive {
		return commandrules.ErrUndockStructure
	}
	parent, err := v.ship(holder)
	if err != nil {
		return err
	}
	children, err := constructionChain(parent.Ships, v.ship)
	if err != nil {
		return err
	}
	for _, child := range children {
		if members[child.Number] != nil || child.Number == holder ||
			(child.WhatOrbits != model.LevelShip && child.WhatOrbits != model.LevelStar) ||
			(child.WhatOrbits == model.LevelShip && child.DestShipNo != holder) {
			return commandrules.ErrUndockStructure
		}
	}
	return commandrules.ErrUndockUnsettled
}

// A missing same-star living member needs the detached-list check. A named
// peer can instead be a moved ship or a rebuilt wreck, with the same
// read-only witness constraints as in the normal undock planner.
func (v starExteriorView) checkAbsentUndockPeer(st *model.Star, members map[uint32]*model.Ship, sh *model.Ship) error {
	if members[sh.Number] != nil {
		return nil
	}
	if sh.WhatOrbits != model.LevelStar || sh.StOrbits != st.Number {
		if !commandrules.MovedStarPeer(st, members, sh) {
			return commandrules.ErrUndockStructure
		}
		return nil
	}
	if !sh.Alive {
		children, err := constructionChain(sh.Ships, v.ship)
		if err != nil || !commandrules.DetachedStarWreck(st, members, sh, children) {
			return commandrules.ErrUndockStructure
		}
		return nil
	}
	if err := v.checkDetachedStarMember(st, members, sh.Number); err != commandrules.ErrUndockUnsettled {
		return err
	}
	return nil
}

// Only a peer-undock request with an absent saved member needs a separate
// scan of saved roots and holds. Never substitute actor rows for persisted
// links on that scan. A pending actor-only row is allowed only as an
// unreferenced selection, then checked by ordinary partial-save admission.
func (s *State) storedUndockAbsence(w *commandWorkspace, st *model.Star, members map[uint32]*model.Ship, ids []uint32) (bool, error) {
	ships, err := s.store.AllShips()
	if err != nil {
		return false, err
	}
	stars, err := s.store.AllStars()
	if err != nil {
		return false, err
	}
	planets, err := s.store.AllPlanets()
	if err != nil {
		return false, err
	}
	data, err := s.store.GetStarData()
	if err != nil {
		return false, err
	}
	if data == nil {
		return false, commandrules.ErrUndockStructure
	}
	pending := false
	read := func(id uint32) (*model.Ship, error) {
		sh, e := s.store.GetShip(id)
		if e != nil {
			return nil, e
		}
		if sh != nil && sh.Number != id {
			return nil, commandrules.ErrUndockStructure
		}
		if !reflect.DeepEqual(sh, w.ships[id].before) {
			pending = true
		}
		return sh, nil
	}
	view := starExteriorView{ships: ships, stars: stars, planets: planets, sdata: data, ship: func(id uint32) (*model.Ship, error) {
		sh, err := read(id)
		if err != nil || sh == nil {
			return nil, commandrules.ErrUndockStructure
		}
		return sh, nil
	}}
	selected, err := read(ids[0])
	if err != nil {
		return false, err
	}
	// The durable selected ship may still name a different peer. Its own
	// claim must resolve in the saved graph before treating it as pending.
	if selected != nil && selected.Alive && selected.WhatOrbits == model.LevelStar && selected.StOrbits == st.Number &&
		selected.Docked && selected.WhatDest == model.LevelShip {
		peer, err := view.ship(selected.DestShipNo)
		if err != nil || !commandrules.ValidExteriorDockClaim(selected, peer) {
			return false, commandrules.ErrUndockStructure
		}
		ids = append(ids, peer.Number)
	}
	seen := map[uint32]bool{}
	for _, id := range ids {
		if seen[id] || members[id] != nil {
			continue
		}
		seen[id] = true
		sh, err := read(id)
		if err != nil {
			return false, err
		}
		if sh != nil {
			if err := view.checkAbsentUndockPeer(st, members, sh); err != nil {
				return false, err
			}
		}
	}
	return pending, nil
}
