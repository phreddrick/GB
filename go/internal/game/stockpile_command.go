package game

import (
	"fmt"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// commitStockpileTransfer is actor-only. cmdTransfer has validated the complete
// invocation and built a private planet proposal. Capture immutable witnesses
// and charge star AP in the same operation. Route edits keep their existing
// single-planet contract. No persistent notification/outbox is added.
func (s *State) commitStockpileTransfer(p, g, target int, commodity byte, amount uint16, dir model.Directory, planet, next *model.Planet, star *model.Star, msg string, sess net.Responder) {
	outcome := db.CommandNotCommitted
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("stockpile transfer %s; command panic; operator reconciliation required; no shutdown flush", outcome), "")
			func() {
				defer func() { _ = recover() }()
				_ = sess.Send("Stockpile transfer persistence could not be confirmed to the caller; the engine has halted for operator review.\n")
			}()
		}
	}()
	fail := func() {
		s.halt(fmt.Sprintf("stockpile transfer %s; persistence or response failure; operator reconciliation required; no shutdown flush", outcome), "")
		_ = sess.Send(fmt.Sprintf("Stockpile transfer %s; persistence could not be confirmed to the caller; the engine has halted for operator review.\n", outcome))
	}
	if s.Mode() != ModeServing {
		_ = sess.Send(refusalMessage(s.Mode()))
		return
	}
	w := newCommandWorkspace(s)
	w.scope = &workspaceScope{p, g, dir}
	key := [2]uint8{dir.StarNum, dir.PlanetNum}
	w.planets[key] = workspacePlanet{planet, planetEdit(planet), next}
	proposedStar, e := w.star(dir.StarNum)
	if e != nil {
		fail()
		return
	}
	if proposedStar.AP[p-1] == 0 {
		fail()
		return
	}
	caller, e := w.race(p)
	if e != nil {
		fail()
		return
	}
	recipient, e := w.race(target)
	if e != nil {
		fail()
		return
	}
	proposedStar.AP[p-1]--
	in := db.StockpileTransfer{Player: p, Governor: g, Recipient: target, Commodity: commodity, Amount: amount, Scope: dir, Planet: db.CommandPlanet{Expected: w.planets[key].before, Proposed: next}, Star: db.CommandStar{Expected: w.stars[dir.StarNum].before, Proposed: proposedStar}, Caller: caller, Target: recipient}
	if w.stars[dir.StarNum].actor != star || !w.owns() {
		fail()
		return
	}

	outcome, e = s.store.CommitStockpileTransfer(in)
	if outcome == db.CommandCommitted {
		if s.testHooks != nil && s.testHooks.beforeCommandInstall != nil {
			s.testHooks.beforeCommandInstall()
		}
		if !w.owns() {
			fail()
			return
		}
		// No callback or fallible operation between the two stable-address installs.
		*w.planets[key].actor = *in.Planet.Proposed
		*w.stars[dir.StarNum].actor = *in.Star.Proposed
		if s.testHooks != nil && s.testHooks.afterCommandInstall != nil {
			s.testHooks.afterCommandInstall()
		}
		if e == nil {
			// msg is the one private immutable legacy response: delay it,
			// without imposing a new size restriction on existing world names.
			if e = sess.Send(msg); e == nil {
				// warnRace is registry-only live delivery, not a hidden telegram write.
				s.warnRace(target, msg)
				return
			}
		}
	}
	// Confirmed commits stay installed even if reporting failed. Confirmed aborts
	// and uncertain writes install nothing and conservatively HALT, never retry.
	fail()
}
