package game

import (
	"strconv"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

func (s *State) handleCarrierProbe(p, g int, args []string, sess net.Responder) bool {
	if len(args) == 0 || len(args[0]) == 0 || args[0][0] != ':' || p < 1 || p > model.MaxPlayers || g < 0 || g > model.MaxGovernors {
		return false
	}
	dir := s.dirs[p-1][g]
	if dir.Level != model.LevelShip {
		return false
	}
	b, r := s.ships[dir.ShipNo], s.raceByNum(p)
	if b == nil || b.Type != model.StypeCarrier || b.WhatOrbits != model.LevelPlan || r == nil || r.God {
		return false
	}
	if len(args) > 1 {
		if n, _ := strconv.Atoi(args[1]); n > 1 {
			return false
		}
	}
	// Ineligible builders use the legacy refusal, before allocation or output.
	var st, pl int
	if s.buildAtShip(p, g, r, b, &st, &pl) < 0 {
		return false
	}
	s.runShipConstruction(p, g, sess, s.stageCarrierProbe)
	return true
}

func (s *State) stageCarrierProbe(p, g int) (stagedShipBuild, bool, error) {
	w, in, pending, e := s.probeBuildStage(p, g)
	if e != nil || pending {
		return stagedShipBuild{}, pending, e
	}
	return stagedShipBuild{w, in.Child, func() (db.CommandOutcome, error) { return s.store.CommitCarrierProbe(in) }}, false, nil
}

func (s *State) probeBuildStage(p, g int) (*commandWorkspace, db.CarrierProbeBuild, bool, error) {
	var in db.CarrierProbeBuild
	w := newCommandWorkspace(s)
	dir := s.dirs[p-1][g]
	w.scope = &workspaceScope{p, g, dir}
	ctx := commandrules.ProbeBuild{Player: p, Governor: g, Scope: dir}
	r, e := w.race(p)
	if e != nil {
		return w, in, false, e
	}
	ctx.Race = r
	b, e := w.ship(dir.ShipNo)
	if e != nil {
		return w, in, false, e
	}
	if e := w.capturePlanetHold(b, &ctx); e != nil {
		return w, in, false, e
	}
	_, holdErr := ctx.Carrier()
	if holdErr != nil && holdErr != commandrules.ErrProbeHoldUnsettled {
		return w, in, false, holdErr
	}
	if holdErr == commandrules.ErrProbeHoldUnsettled {
		if err := checkOtherLandingOrbits(b, ctx.Children, w.star, w.planet, w.ship); err != nil {
			return w, in, false, err
		}
	}
	pending, e := s.constructionAdmission(w, b.Number)
	if e != nil || pending {
		return w, in, pending, e
	}
	if holdErr != nil {
		return w, in, false, holdErr
	}
	ticket, pending, e := s.shipBuildCandidate()
	if e != nil || pending {
		return w, in, pending, e
	}
	child, e := commandrules.PlanProbeBuild(ctx, ticket.Number, s.cfg)
	if e != nil {
		return w, in, false, e
	}
	in = db.CarrierProbeBuild{Input: ctx, Allocation: ticket, Child: child, Config: s.cfg}
	return w, in, false, nil
}

// Capture the same actor-owned physical lists for both building and launching.
func (w *commandWorkspace) capturePlanetHold(b *model.Ship, ctx *commandrules.PlanetHangar) error {
	if _, e := w.star(b.StOrbits); e != nil {
		return e
	}
	ctx.Star = w.stars[b.StOrbits].before
	if _, e := w.planet(b.StOrbits, b.PNumOrbits); e != nil {
		return e
	}
	ctx.Planet = w.planets[[2]uint8{b.StOrbits, b.PNumOrbits}].before
	read := func(n uint32) (*model.Ship, error) {
		if _, e := w.ship(n); e != nil {
			return nil, e
		}
		return w.ships[n].before, nil
	}
	var e error
	ctx.Orbit, e = constructionChain(ctx.Planet.Ships, read)
	if e != nil {
		return e
	}
	ctx.Children, e = constructionChain(b.Ships, read)
	if e != nil {
		return e
	}
	return nil
}

func constructionChain(head uint32, read func(uint32) (*model.Ship, error)) ([]*model.Ship, error) {
	var chain []*model.Ship
	seen := map[uint32]bool{}
	for n := head; n != 0; {
		if seen[n] {
			return nil, commandrules.ErrFactoryBuildInput
		}
		seen[n] = true
		sh, e := read(n)
		if e != nil {
			return nil, e
		}
		chain = append(chain, sh)
		n = sh.NextShip
	}
	return chain, nil
}

// Flying ships could land by proximity from a different planet's orbit. For
// historical holds, verify those declared roots before treating their stale
// orbit as harmless. These witnesses can only defer or refuse a build.
func checkOtherLandingOrbits(b *model.Ship, children []*model.Ship,
	star func(uint8) (*model.Star, error), planet func(uint8, uint8) (*model.Planet, error),
	read func(uint32) (*model.Ship, error)) error {
	roots := map[[2]uint8]bool{}
	held := make(map[uint32]bool, len(children))
	for _, child := range children {
		held[child.Number] = true
		if child.WhatOrbits == model.LevelPlan && (child.StOrbits != b.StOrbits || child.PNumOrbits != b.PNumOrbits) {
			roots[[2]uint8{child.StOrbits, child.PNumOrbits}] = true
		}
	}
	for key := range roots {
		st, err := star(key[0])
		if err != nil {
			return err
		}
		pl, err := planet(key[0], key[1])
		if err != nil {
			return err
		}
		if st == nil || st.Number != key[0] || pl == nil || pl.PlanetNum != key[1] {
			return commandrules.ErrFactoryBuildInput
		}
		chain, err := constructionChain(pl.Ships, read)
		if err != nil {
			return err
		}
		if err := commandrules.FactoryBuildChain(st, pl, chain); err != nil {
			return err
		}
		for _, sh := range chain {
			if held[sh.Number] {
				return commandrules.ErrFactoryBuildInput
			}
		}
	}
	return nil
}
