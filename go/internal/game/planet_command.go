package game

import (
	"reflect"

	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// planetEdit copies the actor's authoritative record for the two single-planet
// commands in CR-17A. Neither command changes sectors, but do not lend the slice
// to a proposal that has not committed. This is not general command staging.
func planetEdit(p *model.Planet) *model.Planet {
	n := *p
	if p.Sectors != nil {
		n.Sectors = append([]model.Sector{}, p.Sectors...)
	}
	return &n
}

// commitPlanetEdit is actor-only. Store reads are precondition comparisons,
// never the mutable source of a command. Deferred legacy handlers can still
// leave newer store copies: refuse disagreement instead of overwriting them.
// The existing PutPlanet transaction covers exactly one planet. AP and live
// notifications are handled by the caller only after this returns true.
func (s *State) commitPlanetEdit(before, next *model.Planet, sess net.Responder) (ok bool) {
	phase := "precondition"
	fail := func(reason string) {
		s.halt("planet command "+reason+"; operator reconciliation required; shutdown will not flush", "")
		_ = sess.Send("Planet command persistence could not be confirmed; the engine has halted for operator review.\n")
	}
	defer func() {
		if recover() != nil {
			// A panic while a write is in flight proves neither rollback nor
			// durability. Do not leak hook/encoding values in diagnostics.
			fail(phase + " panic; outcome unconfirmed")
			ok = false
		}
	}()
	// Command admission already handles draining. Once started, an operation
	// must finish before Stop's queued flush, even if draining becomes true.
	if s.Mode() != ModeServing {
		_ = sess.Send(refusalMessage(s.Mode()))
		return false
	}
	if before == nil || next == nil || before.StarNum != next.StarNum || before.PlanetNum != next.PlanetNum || s.planets[before.StarNum][before.PlanetNum] != before {
		fail("has inconsistent actor planet identity; no write attempted")
		return false
	}
	stored, err := s.store.GetPlanet(before.StarNum, before.PlanetNum)
	if err != nil || !reflect.DeepEqual(before, stored) {
		fail("precondition unavailable or actor/store planet differs; no write attempted")
		return false
	}
	phase = "write"
	put := s.store.PutPlanet
	if s.testHooks != nil && s.testHooks.planetCommandWrite != nil {
		put = s.testHooks.planetCommandWrite
	}
	if err := put(next); err != nil {
		// Generic write errors cannot establish durability even if readback
		// happens to show the intended record. Never install or retry here.
		fail("write failed; outcome unconfirmed")
		return false
	}
	phase = "committed installation"
	// Struct assignment cannot fail and preserves the actor record's address.
	// Borrowed proposal data is not exposed by the two private callers.
	*before = *next
	return true
}
