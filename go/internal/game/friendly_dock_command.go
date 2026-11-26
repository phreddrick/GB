package game

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// Classify the original selector as well as its expansion: even a wildcard
// resolving to exactly one ship belongs to the legacy multi-selection path.
func (s *State) handleFriendlyDock(p, g int, spec, targetText string, a, b *model.Ship, sess net.Responder) bool {
	if spec == "" || (spec[0] != '#' && (spec[0] < '0' || spec[0] > '9')) || a.Owner != b.Owner || a.WhatOrbits != model.LevelStar || b.WhatOrbits != model.LevelStar {
		return false
	}
	if a.StOrbits != b.StOrbits {
		// Do not route a broken same-list member through the legacy writer.
		linked := func(st uint8, id uint32) bool {
			if int(st) >= len(s.stars) || s.stars[st] == nil {
				return false
			}
			_, ok := s.shipPredecessor(s.stars[st].Ships, id)
			return ok
		}
		if !linked(a.StOrbits, b.Number) && !linked(b.StOrbits, a.Number) {
			return false
		}
		s.halt("friendly dock has contradictory star membership; no shutdown flush", "")
		_ = sess.Send("Dock halted: inconsistent star membership.\n")
		return true
	}
	// Legacy parsing narrows an int to uint32. Do not let a wide or signed
	// alias reach either writer for an otherwise supported pair.
	source, e1 := strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(spec, "#"), "+"), 10, 32)
	target, e2 := strconv.ParseUint(strings.TrimPrefix(targetText, "+"), 10, 32)
	if e1 != nil || e2 != nil || source == 0 || target == 0 {
		_ = sess.Send("Invalid ship number.\n")
		return true
	}
	s.runFriendlyDock(p, g, uint32(source), uint32(target), a.StOrbits, sess)
	return true
}

func (s *State) runFriendlyDock(p, g int, source, target uint32, starNum uint8, sess net.Responder) {
	outcome := db.CommandNotCommitted
	fail := func() {
		s.halt(fmt.Sprintf("friendly dock %s; reconciliation required; no shutdown flush", outcome), "")
		_ = sess.Send(fmt.Sprintf("Dock %s; engine halted for reconciliation.\n", outcome))
	}
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("friendly dock %s; panic; no shutdown flush", outcome), "")
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
	race, e := w.race(p)
	if e != nil {
		fail()
		return
	}
	st, e := w.star(starNum)
	if e != nil {
		fail()
		return
	}
	orbit, e := constructionChain(st.Ships, w.ship)
	if e != nil {
		fail()
		return
	}
	in := commandrules.FriendlyDock{Player: p, Governor: g, Source: source, Target: target, Race: race, Star: st, Orbit: orbit, DistToDock: s.cfg.DistToDock}
	a, b, planErr := commandrules.PlanFriendlyDock(in)
	if planErr == commandrules.ErrUndockOutside {
		members, e := commandrules.StarExterior(st, orbit)
		if e != nil {
			fail()
			return
		}
		if w.absentStarSelections(st, members, source, target) != commandrules.ErrUndockUnsettled {
			fail()
			return
		}
		planErr = &commandrules.FactoryBuildRefusal{Message: "Cannot dock: star orbit records are unsettled.\n"}
	}
	var refusal *commandrules.FactoryBuildRefusal
	if planErr != nil && !errors.As(planErr, &refusal) {
		fail()
		return
	}
	pending, e := s.starPeerAdmission(w, st, race, orbit, nil, nil)
	if e != nil || !w.owns() {
		fail()
		return
	}
	if pending {
		_ = sess.Send("Dock deferred: retry after the next persisted turn.\n")
		return
	}
	if refusal != nil {
		_ = sess.Send(refusal.Message)
		return
	}
	outcome, e = s.store.CommitFriendlyDock(in)
	if outcome == db.CommandCommitted && w.installStarPeers(a, b) && e == nil {
		if sess.Send(fmt.Sprintf("%s docked with %s.\n", shipDisplayName(a), shipDisplayName(b))) == nil {
			return
		}
	}
	fail()
}
