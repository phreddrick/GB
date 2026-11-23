package game

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// Only a numeric selection and a ship target in the same planet orbit enter
// this writer. The proximity-based cross-planet landing remains separate.
func (s *State) handleProbeLanding(p, g int, spec, target string, sess net.Responder) bool {
	if !strings.HasPrefix(target, "#") || spec == "" || (spec[0] != '#' && (spec[0] < '0' || spec[0] > '9')) {
		return false
	}
	// Reject overflowing selectors before a narrowing conversion can alias
	// another ship. Preserve legacy not-found handling for ordinary bad text.
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(spec, "#"), "+"), 10, 32)
	if errors.Is(err, strconv.ErrRange) {
		_ = sess.Send("Invalid ship number.\n")
		return true
	}
	if err != nil || n == 0 {
		return false
	}
	c := s.ships[uint32(n)]
	if c == nil || !c.Alive || int(c.Owner) != p || c.Type != model.OtypeProbe || c.WhatOrbits != model.LevelPlan || c.Docked {
		return false
	}
	m, err := strconv.ParseUint(strings.TrimPrefix(target[1:], "+"), 10, 32)
	if errors.Is(err, strconv.ErrRange) || strings.HasPrefix(target[1:], "-") {
		_ = sess.Send("Invalid ship number.\n")
		return true
	}
	if err != nil || m == 0 {
		return false
	}
	b := s.ships[uint32(m)]
	if b == nil || b.Type != model.StypeCarrier || !b.Alive || b.Docked || b.WhatOrbits != model.LevelPlan ||
		c.StOrbits != b.StOrbits || c.PNumOrbits != b.PNumOrbits {
		return false
	}
	if !authorized(g, c) || !authorized(g, b) || int(b.Owner) != p || !b.Active {
		_ = sess.Send("You are not authorized to land this probe on that Carrier.\n")
		return true
	}
	s.runProbeLanding(p, g, uint32(n), uint32(m), sess)
	return true
}

func (s *State) runProbeLanding(p, g int, id, parent uint32, sess net.Responder) {
	outcome := db.CommandNotCommitted
	fail := func() {
		s.halt(fmt.Sprintf("probe landing %s; operator reconciliation required; no shutdown flush", outcome), "")
		_ = sess.Send(fmt.Sprintf("Landing %s; engine halted for reconciliation.\n", outcome))
	}
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("probe landing %s; panic; operator reconciliation required; no shutdown flush", outcome), "")
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
	r, err := w.race(p)
	if err != nil || g < 0 || g > model.MaxGovernors {
		fail()
		return
	}
	w.scope = &workspaceScope{p, g, s.dirs[p-1][g]}
	in := commandrules.ProbeLanding{
		ProbeTransit: commandrules.ProbeTransit{
			PlanetHangar: commandrules.PlanetHangar{Player: p, Governor: g, Scope: w.scope.before, Race: r},
			Selected:     id, Parent: parent,
		},
		MassFuel: s.cfg.MassFuel, DistToDock: s.cfg.DistToDock,
	}
	b, err := w.ship(parent)
	if err != nil || w.capturePlanetHold(b, &in.PlanetHangar) != nil {
		fail()
		return
	}
	change, fuel, planErr := commandrules.PlanProbeLanding(in)
	// If a participant still declares a planet orbit after a historical land,
	// its actual hold can explain an absent exterior member. This branch can
	// only refuse or defer; never feed that extra graph to the landing writer.
	var otherHolds []uint32
	if planErr == commandrules.ErrFactoryBuildInput {
		otherHolds, err = w.historicalLandingHolds(in)
		if err != nil {
			fail()
			return
		}
		if len(otherHolds) != 0 {
			planErr = &commandrules.FactoryBuildRefusal{Message: "Cannot land: a ship is already linked in another hold.\n"}
		}
	}
	var refused *commandrules.FactoryBuildRefusal
	switch {
	case planErr == commandrules.ErrProbeHoldUnsettled:
		if checkOtherLandingOrbits(b, in.Children, w.star, w.planet, w.ship) != nil {
			fail()
			return
		}
		refused = &commandrules.FactoryBuildRefusal{Message: "Cannot land: this Carrier's hangar records are unsettled.\n"}
	case planErr != nil && !errors.As(planErr, &refused):
		fail()
		return
	}
	pending, err := s.shipChainAdmission(w, parent, false, otherHolds...)
	if err != nil || !w.owns() {
		fail()
		return
	}
	if pending {
		_ = sess.Send("Landing deferred: retry after the next persisted turn.\n")
		return
	}
	if refused != nil {
		_ = sess.Send(refused.Message)
		return
	}
	msg := fmt.Sprintf("%s landed on %s using %.1f fuel.\n", shipDisplayName(change.Probe), shipDisplayName(b), fuel)
	outcome, err = s.store.CommitProbeLanding(in)
	if outcome == db.CommandCommitted && w.installProbeTransit(change) && err == nil && sess.Send(msg) == nil {
		return
	}
	fail()
}

// A known old insertion could leave a held ship with its planet fields.
// For any missing participant require positive, disjoint hold membership;
// an unexplained absence or a duplicate is still structural corruption.
func (w *commandWorkspace) historicalLandingHolds(in commandrules.ProbeLanding) ([]uint32, error) {
	bad := commandrules.ErrFactoryBuildInput
	if commandrules.FactoryBuildChain(in.Star, in.Planet, in.Orbit) != nil {
		return nil, bad
	}
	b := w.ships[in.Parent].before
	held, err := commandrules.ProbeChildren(b, in.Children)
	if err != nil && err != commandrules.ErrProbeHoldUnsettled {
		return nil, bad
	}
	for _, sh := range in.Orbit {
		if held[sh.Number] {
			return nil, bad
		}
	}
	owners := map[uint32]uint32{}
	for id := range held {
		owners[id] = in.Parent
	}
	var holders []uint32
	for _, id := range []uint32{in.Parent, in.Selected} {
		if slices.ContainsFunc(in.Orbit, func(sh *model.Ship) bool { return sh.Number == id }) || held[id] {
			continue
		}
		var found bool
		for n, other := range w.s.ships {
			if other == nil || n == in.Parent || other.Ships == 0 {
				continue
			}
			if _, member := w.s.shipPredecessor(other.Ships, id); !member {
				continue
			}
			if found || other.Number != n {
				return nil, bad
			}
			found = true
			parent, e := w.ship(n)
			if e != nil {
				return nil, e
			}
			children, e := constructionChain(parent.Ships, w.ship)
			if e != nil {
				return nil, e
			}
			legacy, e := commandrules.ProbeChildren(parent, children)
			if e != commandrules.ErrProbeHoldUnsettled || !legacy[id] {
				return nil, bad
			}
			for child := range legacy {
				if previous := owners[child]; previous != 0 && previous != n {
					return nil, bad
				}
				owners[child] = n
			}
			for _, sh := range in.Orbit {
				if legacy[sh.Number] {
					return nil, bad
				}
			}
			if e := checkOtherLandingOrbits(parent, children, w.star, w.planet, w.ship); e != nil {
				return nil, e
			}
			if !slices.Contains(holders, n) {
				holders = append(holders, n)
			}
		}
		if !found {
			return nil, bad
		}
	}
	for _, n := range holders {
		if cyclicHold(owners, n) {
			return nil, bad
		}
	}
	return holders, nil
}
