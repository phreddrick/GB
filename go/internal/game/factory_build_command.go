package game

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// Classification precedes every legacy mutation. Preserve legacy first-letter,
// Sscanf coordinates, ignored extra arguments and effective-count semantics.
// Other types, God/autoload/multiple builds and every ship-scope build remain
// explicitly legacy, WITHOUT the new durability guarantee.
func (s *State) handlePlanetFactoryBuild(p, g int, args []string, sess net.Responder) bool {
	if p < 1 || p > model.MaxPlayers || g < 0 || g > model.MaxGovernors || len(args) < 2 || len(args[0]) == 0 || args[0][0] != 'F' {
		return false
	}
	r := s.raceByNum(p)
	if r == nil || r.God || r.Governor[g].Toggle.AutoLoad || s.dirs[p-1][g].Level != model.LevelPlan {
		return false
	}
	count := 1
	if len(args) >= 3 {
		if n, _ := strconv.Atoi(args[2]); n > 0 {
			count = n
		}
	}
	if count != 1 {
		return false
	}
	if s.planets[s.dirs[p-1][g].StarNum][s.dirs[p-1][g].PlanetNum] == nil {
		_ = sess.Send("No such planet.\n")
		return true
	}
	s.runPlanetFactoryBuild(p, g, args, sess)
	return true
}

func (s *State) factoryBuildStage(p, g int, args []string) (*commandWorkspace, db.PlanetFactoryBuild, bool, error) {
	var in db.PlanetFactoryBuild
	w := newCommandWorkspace(s)
	dir := s.dirs[p-1][g]
	w.scope = &workspaceScope{p, g, dir}
	pl, e := w.planet(dir.StarNum, dir.PlanetNum)
	if e != nil {
		return nil, in, false, e
	}
	st, e := w.star(dir.StarNum)
	if e != nil {
		return nil, in, false, e
	}
	r, e := w.race(p)
	if e != nil {
		return nil, in, false, e
	}
	var ships []*model.Ship
	seen := map[uint32]bool{}
	for n := pl.Ships; n != 0; {
		if seen[n] {
			return nil, in, false, commandrules.ErrFactoryBuildInput
		}
		seen[n] = true
		sh, e := w.ship(n)
		if e != nil {
			return nil, in, false, e
		}
		ships = append(ships, w.ships[n].before)
		n = sh.NextShip
	}
	if e := commandrules.FactoryBuildChain(st, pl, ships); e != nil {
		return nil, in, false, e
	}
	pending, e := s.factoryBuildAdmission(w)
	if e != nil || pending {
		return w, in, pending, e
	}
	ticket, e := s.store.PlanetFactoryCandidate()
	var ce *db.CommandError
	if errors.As(e, &ce) && ce.Code == db.CommandStale {
		return w, in, true, nil
	}
	if e != nil {
		return nil, in, false, e
	}
	// An unsaved actor-only ship must never be overwritten by append allocation.
	if s.ships[ticket.Number] != nil {
		return w, in, true, nil
	}
	var x, y int
	_, _ = fmt.Sscanf(args[1], "%d,%d", &x, &y)
	in = db.PlanetFactoryBuild{Allocation: ticket, Input: commandrules.PlanetFactoryInput{Player: p, Governor: g, X: x, Y: y, Number: ticket.Number, Scope: dir, Race: r, Star: w.stars[dir.StarNum].before, Planet: w.planets[[2]uint8{dir.StarNum, dir.PlanetNum}].before, Ships: ships, SizeCrew: s.cfg.SizeCrew, SizeResource: s.cfg.SizeResource, MassSize: s.cfg.MassSize}}
	in.Proposed, e = commandrules.PlanPlanetFactory(in.Input)
	if e != nil {
		return w, in, false, e
	}
	w.planets[[2]uint8{dir.StarNum, dir.PlanetNum}] = workspacePlanet{w.planets[[2]uint8{dir.StarNum, dir.PlanetNum}].actor, in.Input.Planet, in.Proposed.Planet}
	f := in.Proposed.Ship
	for _, msg := range []string{
		fmt.Sprintf("F%d built at a cost of %d resources.\n", f.Number, f.BuildCost),
		fmt.Sprintf("Technology %.1f.\n", f.Tech),
		fmt.Sprintf("F%d is on sector %d,%d.\n", f.Number, x, y),
		fmt.Sprintf("Warning: This ship is constructed with a %d%% damage level.\n", f.Damage),
		"This ship does not need resources to repair.\n",
		"This factory may not begin repairs until it has been activated.\n",
		"Loaded with 0 crew and 0.0 fuel.\n",
	} {
		if e := w.effects.Send(msg); e != nil {
			return nil, in, false, e
		}
	}
	return w, in, false, nil
}

func (s *State) runPlanetFactoryBuild(p, g int, args []string, sess net.Responder) {
	outcome := db.CommandNotCommitted
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("planet factory construction %s; panic; no shutdown flush; reconciliation required", outcome), "")
			func() {
				defer func() { _ = recover() }()
				_ = sess.Send("Construction failed; engine halted for reconciliation.\n")
			}()
		}
	}()
	fail := func() {
		s.halt(fmt.Sprintf("planet factory construction %s; persistence/installation/reporting failed; no shutdown flush; reconciliation required", outcome), "")
		_ = sess.Send(fmt.Sprintf("Construction %s; engine halted for reconciliation.\n", outcome))
	}
	if s.Mode() != ModeServing {
		_ = sess.Send(refusalMessage(s.Mode()))
		return
	}
	if s.store.CommandWriteUncertain() {
		fail()
		return
	}
	w, in, pending, e := s.factoryBuildStage(p, g, args)
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
	if !w.owns() {
		fail()
		return
	}
	outcome, e = s.store.CommitPlanetFactory(in)
	if outcome == db.CommandCommitted {
		if s.testHooks != nil && s.testHooks.beforeCommandInstall != nil {
			s.testHooks.beforeCommandInstall()
		}
		if !w.owns() || s.ships[in.Input.Number] != nil {
			fail()
			return
		}
		// One actor: no callbacks or fallible gameplay between installs. Existing
		// objects retain addresses; the new identity has never been published.
		*w.planets[[2]uint8{in.Input.Planet.StarNum, in.Input.Planet.PlanetNum}].actor = *in.Proposed.Planet
		s.ships[in.Input.Number] = in.Proposed.Ship
		if s.testHooks != nil && s.testHooks.afterCommandInstall != nil {
			s.testHooks.afterCommandInstall()
		}
		if e == nil {
			for _, msg := range w.effects.lines {
				s.notify(p, g, msg)
			}
			return
		}
	}
	fail()
}
