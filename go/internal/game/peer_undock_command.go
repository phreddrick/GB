package game

import (
	"fmt"
	"reflect"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

func (s *State) handleStarPeerUndock(p, g int, id uint32, sh *model.Ship, sess net.Responder) bool {
	if sh.WhatOrbits != model.LevelStar || !sh.Docked {
		return false
	}
	other := s.ships[sh.DestShipNo]
	reciprocal := sh.WhatDest == model.LevelShip && other != nil && commandrules.ExteriorDockBack(other, id)
	if reciprocal && other.Owner != sh.Owner {
		return false
	}
	if reciprocal && other.WhatOrbits == model.LevelStar && other.StOrbits != sh.StOrbits {
		// Legacy dock compares distance and scope level. Leave a genuinely
		// cross-star pair on that path, but never trust a stray star number
		// on a member still linked in the selected ship's exterior.
		if int(sh.StOrbits) < len(s.stars) && int(other.StOrbits) < len(s.stars) {
			src, dst := s.stars[sh.StOrbits], s.stars[other.StOrbits]
			if src != nil && dst != nil {
				_, here := s.shipPredecessor(src.Ships, other.Number)
				_, there := s.shipPredecessor(dst.Ships, other.Number)
				_, selectedHere := s.shipPredecessor(src.Ships, id)
				if !here && there && selectedHere {
					return false
				}
			}
		}
	}
	s.runStarPeerUndock(p, g, id, sess)
	return true
}

func (s *State) runStarPeerUndock(p, g int, id uint32, sess net.Responder) {
	outcome := db.CommandNotCommitted
	fail := func() {
		s.halt(fmt.Sprintf("peer undock %s; reconciliation required; no shutdown flush", outcome), "")
		_ = sess.Send(fmt.Sprintf("Undock %s; engine halted for reconciliation.\n", outcome))
	}
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("peer undock %s; panic; no shutdown flush", outcome), "")
		}
	}()
	if s.Mode() != ModeServing {
		_ = sess.Send(refusalMessage(s.Mode()))
		return
	}
	if s.store.CommandWriteUncertain() {
		outcome = db.CommandUncertain
		fail()
		return
	}
	w := newCommandWorkspace(s)
	race, err := w.race(p)
	if err != nil || g < 0 || g > model.MaxGovernors {
		fail()
		return
	}
	if race.Dissolved || !race.Governor[g].Active || !authorized(g, s.ships[id]) {
		_ = sess.Send("You are not authorized to undock this ship.\n")
		return
	}
	star, err := w.star(s.ships[id].StOrbits)
	if err != nil {
		fail()
		return
	}
	orbit, err := constructionChain(star.Ships, w.ship)
	if err != nil {
		fail()
		return
	}
	in := commandrules.StarPeerUndock{Player: p, Governor: g, Selected: id, Race: race, Star: star, Orbit: orbit}
	// The selected ship can outlive its dock link. A named ship may have
	// moved to another orbit, or been unlinked by the turn as a wreck.
	if member, ok := w.ships[id]; ok && member.before.WhatDest == model.LevelShip {
		target := member.before.DestShipNo
		if _, listed := w.ships[target]; !listed {
			sh, e := w.ship(target)
			if e != nil {
				fail()
				return
			}
			if sh.WhatOrbits != model.LevelStar || sh.StOrbits != star.Number {
				in.MovedPeer = sh
			} else if !sh.Alive {
				in.DetachedWreck = sh
				in.WreckChildren, err = constructionChain(sh.Ships, w.ship)
				if err != nil {
					fail()
					return
				}
			}
		}
	}
	a, b, planErr := commandrules.PlanStarPeerUndock(in)
	var exteriorPeers []uint32
	if planErr == commandrules.ErrUndockOutside {
		exteriorPeers, planErr = w.absentUndockMembers(in)
	} else if a != nil && in.DetachedWreck == nil && in.MovedPeer == nil {
		// A settled actor can still be ahead of a partially saved exterior.
		exteriorPeers = []uint32{id, w.ships[id].before.DestShipNo}
	}
	if planErr != nil && planErr != commandrules.ErrUndockUnsettled {
		fail()
		return
	}
	pending, err := s.starPeerAdmission(w, in.Star, in.Race, in.Orbit, in.DetachedWreck, in.MovedPeer, exteriorPeers...)
	if err != nil || !w.owns() {
		fail()
		return
	}
	if pending {
		_ = sess.Send("Undock deferred: retry after the next persisted turn.\n")
		return
	}
	if planErr != nil {
		_ = sess.Send("Cannot undock: peer docking records are unsettled.\n")
		return
	}
	named := w.ships[w.ships[id].before.DestShipNo].before
	notice := fmt.Sprintf("%s undocked from %s.\n", shipDisplayName(a), shipDisplayName(named))
	outcome, err = s.store.CommitStarPeerUndock(in)
	if outcome == db.CommandCommitted {
		if !w.installStarPeers(a, b) {
			fail()
			return
		}
		if err == nil && sess.Send(notice) == nil {
			return
		}
	}
	fail()
}

func (w *commandWorkspace) installStarPeers(a, b *model.Ship) bool {
	s := w.s
	if s.testHooks != nil && s.testHooks.beforeCommandInstall != nil {
		s.testHooks.beforeCommandInstall()
	}
	if !w.owns() {
		return false
	}
	*w.ships[a.Number].actor = *a
	if b != nil {
		*w.ships[b.Number].actor = *b
	}
	if s.testHooks != nil && s.testHooks.afterCommandInstall != nil {
		s.testHooks.afterCommandInstall()
	}
	return true
}

// A selected ship or its named peer may live in the prefix omitted by a
// partial save. Check both roles, even if their detached runs do not meet.
func (w *commandWorkspace) absentUndockMembers(in commandrules.StarPeerUndock) ([]uint32, error) {
	members, err := commandrules.StarExterior(in.Star, in.Orbit)
	if err != nil {
		return nil, err
	}
	selected, err := w.ship(in.Selected)
	if err != nil {
		return nil, err
	}
	named, err := w.ship(selected.DestShipNo)
	if err != nil || !commandrules.ValidExteriorDockClaim(selected, named) {
		return nil, commandrules.ErrUndockStructure
	}
	view := w.starExteriorView()
	if err := view.checkAbsentUndockPeer(in.Star, members, selected); err != nil {
		return nil, err
	}
	ids := []uint32{selected.Number}
	if named.Number != selected.Number {
		if err := view.checkAbsentUndockPeer(in.Star, members, named); err != nil {
			return nil, err
		}
		ids = append(ids, named.Number)
	}
	return ids, commandrules.ErrUndockUnsettled
}

// Independently walk the durable root, including stored-only tails. A broken
// stored link is not a pending actor edit. The shared partial-save admission
// also checks actor members missing from the durable root and any detached peer.
func (s *State) starPeerAdmission(w *commandWorkspace, star *model.Star, raceBefore *model.Race, orbit []*model.Ship, wreck, moved *model.Ship, exteriorPeers ...uint32) (bool, error) {
	st, err := s.store.GetStar(star.Number)
	if err != nil || st == nil || st.Number != star.Number {
		return false, commandrules.ErrUndockStructure
	}
	readShip := func(id uint32) (*model.Ship, error) {
		sh, e := s.store.GetShip(id)
		if e != nil || sh == nil || sh.Number != id {
			return nil, commandrules.ErrUndockStructure
		}
		return sh, nil
	}
	chain, err := constructionChain(st.Ships, readShip)
	if err != nil {
		return false, err
	}
	storedMembers, err := commandrules.StarExterior(st, chain)
	if err != nil {
		return false, err
	}
	detachedPending := false
	for _, id := range exteriorPeers {
		if storedMembers[id] == nil {
			detachedPending, err = s.storedUndockAbsence(w, st, storedMembers, exteriorPeers)
			if err != nil {
				return false, err
			}
			break
		}
	}
	named := wreck
	if moved != nil {
		named = moved
	}
	if named != nil && storedMembers[named.Number] == nil {
		sh, e := s.store.GetShip(named.Number)
		if e == nil && sh == nil && moved != nil {
			return s.factoryBuildAdmission(w)
		}
		if e != nil || sh == nil || sh.Number != named.Number {
			return false, commandrules.ErrUndockStructure
		}
		if sh.WhatOrbits != model.LevelStar || sh.StOrbits != st.Number {
			if !commandrules.MovedStarPeer(st, storedMembers, sh) {
				return false, commandrules.ErrUndockStructure
			}
		} else if !sh.Alive || wreck != nil {
			children, e := constructionChain(sh.Ships, readShip)
			if e != nil || !commandrules.DetachedStarWreck(st, storedMembers, sh, children) {
				return false, commandrules.ErrUndockStructure
			}
		}
	}
	race, err := s.store.GetRace(raceBefore.PlayerNum)
	if err != nil || race == nil || race.PlayerNum != raceBefore.PlayerNum {
		return false, commandrules.ErrUndockStructure
	}
	sameOrbit := reflect.DeepEqual(st, star) && reflect.DeepEqual(chain, orbit)
	if sameOrbit && reflect.DeepEqual(race, raceBefore) {
		// Moved peers, detached wrecks and historical holds add off-list witnesses.
		equal := true
		if len(w.ships) != len(chain) {
			for id, e := range w.ships {
				sh, err := s.store.GetShip(id)
				if err != nil {
					return false, err
				}
				if !reflect.DeepEqual(sh, e.before) {
					equal = false
					break
				}
			}
		}
		if equal {
			return detachedPending, nil
		}
	}
	pending, err := s.factoryBuildAdmission(w)
	return pending || detachedPending || !sameOrbit || !reflect.DeepEqual(race, raceBefore), err
}
