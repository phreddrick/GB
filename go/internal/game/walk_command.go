package game

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// Intercept the existing single-step grammar before any legacy effect. C/Go
// use the first direction byte and ignore additional arguments; retain that.
// Combat and alternate docking scopes remain on their unchanged legacy path.
func (s *State) handleNoncombatWalk(p, g int, args []string, sess net.Responder) bool {
	if p < 1 || p > model.MaxPlayers || g < 0 || g > model.MaxGovernors {
		_ = sess.Send("Invalid walk authority.\n")
		return true
	}
	if len(args) < 2 {
		return false
	}
	n, e := strconv.ParseInt(strings.TrimPrefix(args[0], "#"), 10, 64)
	if e != nil || n <= 0 || n > math.MaxUint32 {
		_ = sess.Send("No such ship.\n")
		return true
	}
	id := uint32(n)
	sh := s.ships[id]
	if sh == nil || !sh.Alive {
		_ = sess.Send("No such ship.\n")
		return true
	}
	if sh.Number != id {
		s.halt("walk ship identity disagrees with map key; no write attempted", "")
		return true
	}
	if int(sh.Owner) != p || !authorized(g, sh) || sh.Type != model.OtypeAFV || !landed(sh) || sh.Popn == 0 {
		return false
	}
	if len(args[1]) == 0 {
		_ = sess.Send("Illegal move.\n")
		return true
	}
	return s.runNoncombatWalk(p, g, id, args[1][0], sess)
}

func (s *State) walkStage(p, g int, id uint32, direction byte) (*commandWorkspace, db.NoncombatWalk, string, error) {
	var in db.NoncombatWalk
	w := newCommandWorkspace(s)
	w.scope = &workspaceScope{p, g, s.dirs[p-1][g]}
	sh, e := w.ship(id)
	if e != nil {
		return nil, in, "", e
	}
	if e := w.walkLocation(id); e != nil {
		return nil, in, "", e
	}
	pl := w.planets[[2]uint8{sh.StOrbits, sh.PNumOrbits}].before
	st := w.stars[sh.StOrbits].before
	if pl.MaxX == 0 || pl.MaxY == 0 || len(pl.Sectors) != int(pl.MaxX)*int(pl.MaxY) {
		return nil, in, "", commandrules.ErrWalkInput
	}
	if _, e := w.race(p); e != nil {
		return nil, in, "", e
	}
	for _, entry := range w.ships {
		if _, e := w.race(int(entry.before.Owner)); e != nil {
			return nil, in, "", e
		}
	}
	if x, y, ok := commandrules.WalkDestination(pl, int(sh.LandX), int(sh.LandY), direction); ok {
		if owner := int(pl.SectorAt(x, y).Owner); owner != 0 {
			if _, e := w.race(owner); e != nil {
				return nil, in, "", e
			}
		}
	}
	in = db.NoncombatWalk{Scope: w.scope.before, Input: commandrules.WalkInput{Player: p, Governor: g, RecipientCount: len(s.races), Ship: w.ships[id].before, Star: st, Planet: pl, Direction: direction, MassFuel: s.cfg.MassFuel}}
	for _, r := range w.races {
		in.Input.Races = append(in.Input.Races, r.before)
	}
	for _, sh := range w.ships {
		in.Input.Ships = append(in.Input.Ships, sh.before)
	}
	in.Proposed, e = commandrules.PlanWalk(in.Input)
	if e != nil {
		return nil, in, "", e
	}
	// One private immutable message, with legacy text and no new name-size cap.
	msg := fmt.Sprintf("%s moving from %d,%d to %d,%d on %s.\n", shipDisplayName(sh), sh.LandX, sh.LandY, in.Proposed.Ship.LandX, in.Proposed.Ship.LandY, s.dispShipLocFull(sh))
	return w, in, msg, nil
}

// false means a fully classified legacy variant; no proposals/effects escape
// that classification. Once admitted here a failure never retries via legacy.
func (s *State) runNoncombatWalk(p, g int, id uint32, direction byte, sess net.Responder) (handled bool) {
	handled = true
	outcome := db.CommandNotCommitted
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("noncombat walk %s; command panic; no shutdown flush; reconciliation required", outcome), "")
			func() {
				defer func() { _ = recover() }()
				_ = sess.Send("Walk persistence/reporting failed; engine halted for reconciliation.\n")
			}()
		}
	}()
	fail := func() {
		s.halt(fmt.Sprintf("noncombat walk %s; persistence/installation/reporting failed; no shutdown flush; reconciliation required", outcome), "")
		_ = sess.Send(fmt.Sprintf("Walk %s; engine halted for reconciliation.\n", outcome))
	}
	if s.Mode() != ModeServing {
		_ = sess.Send(refusalMessage(s.Mode()))
		return true
	}
	if s.store.CommandWriteUncertain() {
		fail()
		return true
	}
	w, in, msg, e := s.walkStage(p, g, id, direction)
	if errors.Is(e, commandrules.ErrWalkLegacy) {
		return false
	}
	var refusal *commandrules.WalkRefusal
	if errors.As(e, &refusal) {
		if refusal.Registry {
			s.notify(p, g, refusal.Message)
		} else {
			_ = sess.Send(refusal.Message)
		}
		return true
	}
	if e != nil {
		fail()
		return true
	}
	pending, admissionErr := s.walkAdmission(w, id)
	if admissionErr != nil {
		fail()
		return true
	}
	if pending {
		_ = sess.Send("Walk deferred: actor and stored records differ; retry after the next persisted turn.\n")
		return true
	}
	if !w.owns() {
		fail()
		return true
	}
	outcome, e = s.store.CommitNoncombatWalk(in)
	if outcome == db.CommandCommitted {
		if s.testHooks != nil && s.testHooks.beforeCommandInstall != nil {
			s.testHooks.beforeCommandInstall()
		}
		if !w.owns() {
			fail()
			return true
		}
		// Stable actor addresses; no callback/fallible operation between writes.
		*w.ships[id].actor = *in.Proposed.Ship
		*w.stars[in.Input.Star.Number].actor = *in.Proposed.Star
		if s.testHooks != nil && s.testHooks.afterCommandInstall != nil {
			s.testHooks.afterCommandInstall()
		}
		if e == nil {
			// Preserve other-player-only, registry-only delivery. No news/inbox
			// writer, caller success text, or volatile combat reporting is added.
			for i, info := range in.Input.Planet.Info[:in.Input.RecipientCount] {
				if info.NumSectsOwned > 0 && i+1 != p {
					s.notify(i+1, int(in.Input.Star.Governor[i]), msg)
				}
			}
			return true
		}
	}
	fail()
	return true
}

// Walk needs the complete actual planet chain, including dead ships awaiting
// turn cleanup. Destination/order fields are not location evidence for a landed
// ship. Keep telescope's stricter contract unchanged.
func (w *commandWorkspace) walkLocation(id uint32) error {
	sh, e := w.ship(id)
	if e != nil {
		return e
	}
	if sh.WhatOrbits != model.LevelPlan {
		return commandrules.ErrWalkInput
	}
	st, e := w.star(sh.StOrbits)
	if e != nil {
		return e
	}
	pl, e := w.planet(sh.StOrbits, sh.PNumOrbits)
	if e != nil {
		return e
	}
	if pl.PlanetNum >= st.NumPlanets {
		return commandrules.ErrWalkInput
	}
	seen := map[uint32]bool{}
	for n := pl.Ships; n != 0; {
		if seen[n] {
			return commandrules.ErrWalkInput
		}
		seen[n] = true
		x, e := w.ship(n)
		if e != nil {
			return e
		}
		if x.WhatOrbits != model.LevelPlan || x.StOrbits != sh.StOrbits || x.PNumOrbits != sh.PNumOrbits {
			return commandrules.ErrWalkInput
		}
		n = x.NextShip
	}
	if !seen[id] {
		return commandrules.ErrWalkInput
	}
	return nil
}
