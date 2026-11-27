package game

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

func (s *State) handleCargoBuild(p, g int, args []string, sess net.Responder) bool {
	if len(args) == 0 || len(args[0]) == 0 || args[0][0] != 'X' || p < 1 || p > model.MaxPlayers || g < 0 || g > model.MaxGovernors {
		return false
	}
	dir := s.dirs[p-1][g]
	if dir.Level != model.LevelShip {
		return false
	}
	b := s.ships[dir.ShipNo]
	if b == nil || b.Type != model.StypeCargo {
		return false
	}
	if len(args) > 1 {
		if n, _ := strconv.Atoi(args[1]); n > 1 {
			return false
		}
	}
	// Preserve the legacy eligibility messages before traversal (a docked cargo
	// ship can legitimately be inside another ship and cannot construct here).
	var st, pl int
	if s.buildAtShip(p, g, s.raceByNum(p), b, &st, &pl) < 0 {
		return false
	}
	s.runShipConstruction(p, g, sess, s.stageCargoCarrier)
	return true
}

func (s *State) cargoBuildStage(p, g int) (*commandWorkspace, db.CargoCarrierBuild, bool, error) {
	w := newCommandWorkspace(s)
	dir := s.dirs[p-1][g]
	w.scope = &workspaceScope{p, g, dir}
	ctx := commandrules.CargoBuild{Player: p, Governor: g, Scope: dir}
	var in db.CargoCarrierBuild
	r, e := w.race(p)
	if e != nil {
		return w, in, false, e
	}
	ctx.Race = r
	b, e := w.ship(dir.ShipNo)
	if e != nil {
		return w, in, false, e
	}
	var head uint32
	switch b.WhatOrbits {
	case model.LevelPlan, model.LevelStar:
		st, e := w.star(b.StOrbits)
		if e != nil {
			return w, in, false, e
		}
		ctx.Star = w.stars[b.StOrbits].before
		head = st.Ships
		if b.WhatOrbits == model.LevelPlan {
			pl, e := w.planet(b.StOrbits, b.PNumOrbits)
			if e != nil {
				return w, in, false, e
			}
			ctx.Planet = w.planets[[2]uint8{b.StOrbits, b.PNumOrbits}].before
			head = pl.Ships
		}
	case model.LevelUniv:
		ctx.Universe, e = w.starData()
		if e != nil {
			return w, in, false, e
		}
		head = ctx.Universe.Ships
	default:
		return w, in, false, commandrules.ErrFactoryBuildInput
	}
	seen := map[uint32]bool{}
	for n := head; n != 0; {
		if seen[n] {
			return w, in, false, commandrules.ErrFactoryBuildInput
		}
		seen[n] = true
		sh, e := w.ship(n)
		if e != nil {
			return w, in, false, e
		}
		ctx.Ships = append(ctx.Ships, w.ships[n].before)
		n = sh.NextShip
	}
	if _, e := ctx.Builder(); e != nil {
		return w, in, false, e
	}
	pending, e := s.factoryBuildAdmission(w)
	if e != nil || pending {
		return w, in, pending, e
	}
	if ctx.Universe != nil {
		stored, e := s.store.GetStarData()
		if e != nil {
			return w, in, false, e
		}
		if stored == nil {
			return w, in, false, commandrules.ErrFactoryBuildInput
		}
		if !reflect.DeepEqual(stored, ctx.Universe) {
			return w, in, true, nil
		}
	}
	if ctx.Star != nil && ctx.Planet == nil {
		stored, e := s.store.GetStar(ctx.Star.Number)
		if e != nil {
			return w, in, false, e
		}
		if stored == nil || stored.Number != ctx.Star.Number {
			return w, in, false, commandrules.ErrFactoryBuildInput
		}
		if !reflect.DeepEqual(stored, ctx.Star) {
			return w, in, true, nil
		}
	}
	ticket, pending, e := s.shipBuildCandidate()
	if pending || e != nil {
		return w, in, pending, e
	}
	child, e := commandrules.PlanCargoBuild(ctx, ticket.Number, s.cfg)
	if e != nil {
		return w, in, false, e
	}
	in = db.CargoCarrierBuild{Input: ctx, Allocation: ticket, Child: child, Config: s.cfg}
	return w, in, false, nil
}

// The actor installs the complete set through the same
// commit-outcome path. The store entry points themselves stay closed and typed.
type stagedShipBuild struct {
	workspace *commandWorkspace
	children  []*model.Ship
	notice    string
	commit    func() (db.CommandOutcome, error)
}

func (s *State) shipBuildCandidate() (db.FactoryAllocation, bool, error) {
	ticket, e := s.store.PlanetFactoryCandidate()
	var ce *db.CommandError
	if errors.As(e, &ce) && ce.Code == db.CommandStale {
		return ticket, true, nil
	}
	return ticket, e == nil && s.ships[ticket.Number] != nil, e
}

func (s *State) stageCargoCarrier(p, g int) (stagedShipBuild, bool, error) {
	w, in, pending, e := s.cargoBuildStage(p, g)
	if e != nil || pending {
		return stagedShipBuild{}, pending, e
	}
	return stagedShipBuild{workspace: w, children: []*model.Ship{in.Child}, commit: func() (db.CommandOutcome, error) { return s.store.CommitCargoCarrier(in) }}, false, nil
}

func (s *State) runShipConstruction(p, g int, sess net.Responder, stage func(int, int) (stagedShipBuild, bool, error)) {
	outcome := db.CommandNotCommitted
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("ship construction %s; panic; no shutdown flush; reconciliation required", outcome), "")
			func() {
				defer func() { _ = recover() }()
				_ = sess.Send("Construction failed; engine halted for reconciliation.\n")
			}()
		}
	}()
	fail := func() {
		s.halt(fmt.Sprintf("ship construction %s; persistence/installation/reporting failed; no shutdown flush; reconciliation required", outcome), "")
		_ = sess.Send(fmt.Sprintf("Construction %s; engine halted for reconciliation.\n", outcome))
	}
	if s.Mode() != ModeServing {
		_ = sess.Send(refusalMessage(s.Mode()))
		return
	}
	if s.store.CommandWriteUncertain() {
		outcome = db.CommandUncertain
		fail()
		return
	}
	build, pending, e := stage(p, g)
	var refusal *commandrules.FactoryBuildRefusal
	if errors.As(e, &refusal) {
		_ = sess.Send(refusal.Message)
		return
	}
	if e != nil {
		fail()
		return
	}
	if pending {
		_ = sess.Send("Construction deferred: actor and stored records differ or allocation is pending; retry after the next persisted turn.\n")
		return
	}
	w := build.workspace
	if !w.owns() {
		fail()
		return
	}
	outcome, e = build.commit()
	if outcome != db.CommandCommitted || !build.install() {
		fail()
		return
	}
	if s.testHooks != nil && s.testHooks.afterCommandInstall != nil {
		s.testHooks.afterCommandInstall()
	}
	if e != nil {
		fail()
		return
	}
	for _, c := range build.children {
		s.reportShipConstruction(p, g, c, w.races[p].before.God)
	}
	if build.notice != "" {
		_ = sess.Send(build.notice)
	}
}

func (build stagedShipBuild) install() bool {
	w := build.workspace
	s := w.s
	if s.testHooks != nil && s.testHooks.beforeCommandInstall != nil {
		s.testHooks.beforeCommandInstall()
	}
	if !w.owns() {
		return false
	}
	for _, c := range build.children {
		if s.ships[c.Number] != nil {
			return false
		}
	}
	// The actor publishes every row and the owning list before any callback.
	// Existing records keep their addresses.
	builder := w.ships[w.scope.before.ShipNo].actor
	r := w.races[w.scope.player].before
	for _, c := range build.children {
		if c.WhatOrbits == model.LevelShip {
			commandrules.InstallProbeDebits(builder, c)
		} else {
			builder.Resource -= c.BuildCost
			if !r.God {
				builder.Popn -= c.Popn
				builder.Fuel -= c.Fuel
			}
		}
		switch c.WhatOrbits {
		case model.LevelPlan:
			w.planets[[2]uint8{c.StOrbits, c.PNumOrbits}].actor.Ships = c.Number
		case model.LevelStar:
			w.stars[c.StOrbits].actor.Ships = c.Number
		case model.LevelUniv:
			w.data.actor.Ships = c.Number
		}
		s.ships[c.Number] = c
	}
	return true
}

func (s *State) reportShipConstruction(p, g int, c *model.Ship, god bool) {
	s.notify(p, g, fmt.Sprintf("%c%d built at a cost of %d resources.\n", model.ShipLetters[c.Type], c.Number, c.BuildCost))
	s.notify(p, g, fmt.Sprintf("Technology %.1f.\n", c.Tech))
	if c.Damage > 0 {
		s.notify(p, g, fmt.Sprintf("Warning: This ship is constructed with a %d%% damage level.\n", c.Damage))
	}
	if c.Type == model.OtypeProbe {
		s.notify(p, g, "This ship is robotic, and may not repair itself.\n")
	} else {
		s.notify(p, g, "This ship does not need resources to repair.\n")
	}
	crew, fuel := c.Popn, c.Fuel
	if god {
		crew, fuel = 0, 0
	}
	s.notify(p, g, fmt.Sprintf("Loaded with %d crew and %.1f fuel.\n", crew, fuel))
}
