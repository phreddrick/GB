package game

import (
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

// Legacy exterior construction saves the new ships before the star head.
// A later prepend can leave the run pointing into the middle of the current
// list. The producer can build several designs; eligibility to scrap a probe
// is checked by the scrap command, independently of this read-only walk.
func (w *commandWorkspace) checkDetachedStarMember(st *model.Star, members map[uint32]*model.Ship, id uint32) error {
	run, err := constructionChain(id, w.ship)
	if err != nil {
		return err
	}
	ids := make(map[uint32]bool, len(run))
	for _, sh := range run {
		ids[sh.Number] = true
	}
	// Detect intersections even at an interior sibling. An unrooted incoming
	// NextShip (e.g. another ship from the same build) is not a second list.
	s := w.s
	intersects := func(head uint32) bool {
		for steps := 0; head != 0 && steps < len(s.ships); steps++ {
			if ids[head] {
				return true
			}
			sh := s.ships[head]
			if sh == nil || sh.Number != head {
				break
			}
			head = sh.NextShip
		}
		return false
	}
	if s.sdata == nil || intersects(s.sdata.Ships) {
		return commandrules.ErrUndockStructure
	}
	for n, star := range s.stars {
		if n != int(st.Number) && star != nil && intersects(star.Ships) {
			return commandrules.ErrUndockStructure
		}
	}
	for _, planets := range s.planets {
		for _, pl := range planets {
			if pl != nil && intersects(pl.Ships) {
				return commandrules.ErrUndockStructure
			}
		}
	}
	held := false
	for _, parent := range s.ships {
		if parent == nil || !intersects(parent.Ships) {
			continue
		}
		if _, containsSelected := s.shipPredecessor(parent.Ships, id); held || !containsSelected {
			return commandrules.ErrUndockStructure
		}
		held = true
	}
	if held {
		return w.absentStarMember(st, members, id) // validates the complete historical hold
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
			peer, err := w.ship(sh.DestShipNo)
			if err != nil || !commandrules.ValidExteriorDockClaim(sh, peer) {
				return commandrules.ErrUndockStructure
			}
		}
	}
	return commandrules.ErrUndockUnsettled // includes a detached run ending at zero
}
