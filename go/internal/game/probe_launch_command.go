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

// Look for actual membership even if a historical landing retained surface
// fields. Only a single numeric selection enters this path. Other selectors
// retain the existing iteration semantics.
func (s *State) handleProbeLaunch(p, g int, spec string, sess net.Responder) bool {
	if spec == "" || (spec[0] != '#' && (spec[0] < '0' || spec[0] > '9')) {
		return false
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(spec, "#"), "+"), 10, 32)
	if err != nil || n == 0 {
		// Do not let a wide integer alias a smaller ID in shipsBySpec.
		return true
	}
	id := uint32(n)
	sh := s.ships[id]
	if sh == nil || !sh.Alive || sh.Type != model.OtypeProbe || int(sh.Owner) != p {
		return false
	}
	var parent uint32
	for key, b := range s.ships {
		if b == nil || b.Type != model.StypeCarrier || b.WhatOrbits != model.LevelPlan || b.Docked {
			continue
		}
		_, member := s.shipPredecessor(b.Ships, id)
		declaredParent := sh.WhatOrbits == model.LevelShip && sh.DestShipNo == key
		if !member && !declaredParent {
			continue
		}
		if !authorized(g, sh) {
			_ = sess.Send("You are not authorized to launch this probe.\n")
			return true
		}
		if parent != 0 || key == 0 || b.Number != key || sh.Number != id {
			s.halt("probe launch has inconsistent Carrier membership; reconciliation required", "")
			_ = sess.Send("Launch halted: inconsistent hangar membership.\n")
			return true
		}
		parent = key
	}
	if parent == 0 {
		return false
	}
	s.runProbeLaunch(p, g, id, parent, sess)
	return true
}

// A turn can keep a live child in a dead Carrier's hold after dropping the
// Carrier from the exterior list. Validate this read-only refusal separately;
// the storage planner still requires a living, exterior-linked Carrier.
func detachedWreckLaunch(in commandrules.ProbeLaunch, b *model.Ship) error {
	if b.NextShip != 0 || b.Owner == 0 || int(b.Owner) > model.MaxPlayers || int(b.Governor) > model.MaxGovernors ||
		commandrules.FactoryBuildChain(in.Star, in.Planet, in.Orbit) != nil {
		return commandrules.ErrFactoryBuildInput
	}
	hold, err := commandrules.ProbeChildren(b, in.Children)
	if !hold[in.Selected] {
		return commandrules.ErrFactoryBuildInput
	}
	for _, ship := range in.Orbit {
		if hold[ship.Number] {
			return commandrules.ErrFactoryBuildInput
		}
	}
	if err != nil {
		return err
	}
	return commandrules.ErrLaunchRefused
}

func (s *State) runProbeLaunch(p, g int, id, parent uint32, sess net.Responder) {
	outcome := db.CommandNotCommitted
	fail := func() {
		s.halt(fmt.Sprintf("probe launch %s; operator reconciliation required; no shutdown flush", outcome), "")
		_ = sess.Send(fmt.Sprintf("Launch %s; engine halted for reconciliation.\n", outcome))
	}
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("probe launch %s; panic; operator reconciliation required; no shutdown flush", outcome), "")
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
	in := commandrules.ProbeLaunch{PlanetHangar: commandrules.PlanetHangar{Player: p, Governor: g, Scope: w.scope.before, Race: r}, Selected: id, Parent: parent}
	b, err := w.ship(parent)
	if err != nil || w.capturePlanetHold(b, &in.PlanetHangar) != nil {
		fail()
		return
	}
	detachedWreck := !b.Alive && !slices.ContainsFunc(in.Orbit, func(sh *model.Ship) bool { return sh.Number == parent })
	var change commandrules.ProbeLaunchChange
	if detachedWreck {
		err = detachedWreckLaunch(in, b)
	} else {
		change, err = commandrules.PlanProbeLaunch(in)
	}
	unsettled := errors.Is(err, commandrules.ErrProbeHoldUnsettled)
	if unsettled {
		if e := checkOtherLandingOrbits(b, in.Children, w.star, w.planet, w.ship); e != nil {
			fail()
			return
		}
	} else if err != nil && !errors.Is(err, commandrules.ErrLaunchRefused) {
		fail()
		return
	}
	pending, e := s.shipChainAdmission(w, parent, detachedWreck)
	if e != nil || !w.owns() {
		fail()
		return
	}
	if pending {
		_ = sess.Send("Launch deferred: retry after the next persisted turn.\n")
		return
	}
	if unsettled {
		_ = sess.Send("Cannot launch: this Carrier's hangar records are unsettled.\n")
		return
	}
	if err != nil {
		_ = sess.Send("Cannot launch from this hangar.\n")
		return
	}
	launched := fmt.Sprintf("%s launched from %s.\n", shipDisplayName(change.Probe), shipDisplayName(b))
	orbiting := fmt.Sprintf("Orbiting %s/%s.\n", s.starName(int(b.StOrbits)), s.planetName(int(b.StOrbits), int(b.PNumOrbits)))
	outcome, err = s.store.CommitProbeLaunch(in)
	if outcome == db.CommandCommitted {
		if s.testHooks != nil && s.testHooks.beforeCommandInstall != nil {
			s.testHooks.beforeCommandInstall()
		}
		if !w.owns() {
			fail()
			return
		}
		*w.ships[id].actor = *change.Probe
		*w.ships[parent].actor = *change.Carrier
		if pred := change.Predecessor; pred != nil {
			*w.ships[pred.Number].actor = *pred
		}
		*w.planets[[2]uint8{b.StOrbits, b.PNumOrbits}].actor = *change.Planet
		if s.testHooks != nil && s.testHooks.afterCommandInstall != nil {
			s.testHooks.afterCommandInstall()
		}
		if err == nil && sess.Send(launched) == nil && sess.Send(orbiting) == nil {
			return
		}
	}
	fail()
}
