package game

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

// Only the Carrier and its landing supplies are fixture setup. Both docks,
// the reciprocal release and the landing use the normal command dispatcher.
func staleUndockFixture(t *testing.T) *factoryFixture {
	t.Helper()
	f := peerUndockFixture(t)
	if out := f.command(t, "dock #79 #78"); !strings.Contains(out, "docked with") {
		t.Fatal(out)
	}
	checkPeerRelease(t, f, 2, 79, 78, 79, 78)
	return f
}

func landedUndockFixture(t *testing.T) *factoryFixture {
	t.Helper()
	f := staleUndockFixture(t)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		r, st, b := f.state.races[0], f.state.stars[f.star], f.state.ships[78]
		c := new(model.Ship)
		f.state.getshipDefaults(c, model.StypeCarrier, r)
		c.Number, c.Owner, c.Alive, c.Active = 80, 1, true, true
		c.WhatOrbits, c.StOrbits, c.XPos, c.YPos, c.Popn = model.LevelStar, f.star, b.XPos, b.YPos, 1
		c.Mass += r.Mass
		f.state.ships[80] = c
		f.state.insertShipStar(int(f.star), c)
		b.Fuel = 50
		st.AP[0] = 5
		for _, e := range []error{f.store.PutShip(c), f.store.PutShip(b), f.store.PutStar(st)} {
			if e != nil {
				t.Fatal(e)
			}
		}
	})
	if out := f.command(t, "land #78 #80"); !strings.Contains(out, "c78 landed on X80") {
		t.Fatal(out)
	}
	return f
}

func checkLandedUndockGraph(t *testing.T, f *factoryFixture, stillDocked bool) {
	t.Helper()
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	a, b, carrier := landingShip(t, f, 77), landingShip(t, f, 78), landingShip(t, f, 80)
	if a.Docked != stillDocked || (a.DestShipNo == 78) != stillDocked || (a.WhatDest == model.LevelShip) != stillDocked ||
		!b.Docked || b.WhatOrbits != model.LevelShip || b.WhatDest != model.LevelShip || b.DestShipNo != 80 ||
		carrier.Ships != 78 || b.NextShip != 0 || carrier.Hanger != b.Size {
		t.Fatal("selected link or occupied Carrier hold changed")
	}
	st, e := f.store.GetStar(f.star)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[uint32]bool{}
	for n := st.Ships; n != 0; n = landingShip(t, f, n).NextShip {
		if seen[n] || n == 78 {
			t.Fatal("duplicate exterior or hold membership", n)
		}
		seen[n] = true
	}
	for _, n := range []uint32{77, 79, 80} {
		if !seen[n] {
			t.Fatal("missing exterior ship", n)
		}
	}
}

func TestCR17PeerUndockAfterPartnerLands(t *testing.T) {
	t.Parallel()
	for _, wait := range []bool{false, true} {
		t.Run(fmt.Sprintf("retry-after-restart-%t", wait), func(t *testing.T) {
			f := landedUndockFixture(t)
			checkLandedUndockGraph(t, f, true)
			if wait {
				scheduledBuildTurnsAndRestart(t, f, func() { checkLandedUndockGraph(t, f, true) })
			}
			checkPeerRelease(t, f, 1, 77, 78, 77)
			scheduledBuildTurnsAndRestart(t, f, func() { checkLandedUndockGraph(t, f, false) })
		})
	}
}

// Move the named row between complete lists in a fixture. Navigation and
// docking below are real commands; movement between these orbits is seeded.
func moveNamedUndockShip(t *testing.T, f *factoryFixture, dest string) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		b, prev := f.state.ships[78], f.state.ships[79]
		if prev.NextShip != b.Number {
			t.Fatal("unexpected source exterior")
		}
		prev.NextShip = b.NextShip
		var err error
		switch dest {
		case "planet":
			pl := f.state.planet(int(f.star), int(f.planet))
			b.WhatOrbits, b.PNumOrbits = model.LevelPlan, f.planet
			f.state.insertShipPlan(pl, b)
			err = f.store.PutPlanet(pl)
		case "universe":
			b.WhatOrbits = model.LevelUniv
			f.state.insertShipUniv(b)
			err = f.store.PutStarData(f.state.sdata)
		case "selected hold":
			parent := f.state.ships[77]
			b.Docked = true
			f.state.insertShipInShip(b, parent)
			err = f.store.PutShip(parent)
		case "other star":
			n := f.star ^ 1
			dst := f.state.stars[n]
			if dst == nil {
				dst = &model.Star{Number: n}
				f.state.stars[n] = dst
			}
			b.StOrbits = n
			f.state.insertShipStar(int(n), b)
			err = f.store.PutStar(dst)
		default:
			t.Fatal("unknown fixture orbit", dest)
		}
		for _, e := range []error{err, f.store.PutShip(prev), f.store.PutShip(b)} {
			if e != nil {
				t.Fatal(e)
			}
		}
	})
}

func TestCR17PeerUndockMovedExterior(t *testing.T) {
	t.Parallel()
	for _, dest := range []string{"planet", "other star", "universe", "selected hold"} {
		t.Run(dest, func(t *testing.T) {
			f := staleUndockFixture(t)
			moveNamedUndockShip(t, f, dest)
			switch dest {
			case "planet":
				msg := f.command(t, "order #78 destination #77")
				b := landingShip(t, f, 78)
				if b.Docked || b.WhatDest != model.LevelShip || b.DestShipNo != 77 {
					t.Fatal("missing navigation order", msg)
				}
			case "other star":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					c := factoryCopyShip(t, f.state.ships[78])
					c.Number = 80
					f.state.ships[80] = c
					f.state.insertShipStar(int(c.StOrbits), c)
					if e := f.store.PutShip(c); e != nil {
						t.Fatal(e)
					}
					if e := f.store.PutStar(f.state.stars[c.StOrbits]); e != nil {
						t.Fatal(e)
					}
				})
				if out := f.command(t, "dock #78 #80"); !strings.Contains(out, "docked with") {
					t.Fatal(out)
				}
			}
			checkPeerRelease(t, f, 1, 77, 78, 77)
		})
	}
}

func TestCR17PeerUndockMovedWitness(t *testing.T) {
	t.Parallel()
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprintf("late-%t", late), func(t *testing.T) {
			f := landedUndockFixture(t)
			before := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			want := factoryBuildRecords(t, f)
			b := landingShip(t, f, 78)
			b.Name = "outside the star list"
			want[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(78))] = factoryBuildJSON(t, b)
			if !late {
				if err := f.store.PutShip(b); err != nil {
					t.Fatal(err)
				}
			}
			hit := false
			f.store.SetMetaFault(func(op string) error {
				if op == "peer-undock.before" {
					hit = true
					if late {
						return f.store.PutShip(b)
					}
				}
				return nil
			})
			msg := f.command(t, "launch #77")
			f.store.SetMetaFault(nil)
			if hit != late || strings.Contains(msg, "undocked from") || before != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("off-list change escaped admission or transaction compare", msg)
			}
			if late {
				if f.state.Mode() != ModeHalted {
					t.Fatal("late write did not halt", msg)
				}
				if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
					t.Fatal("stale flush", e)
				}
			} else if f.state.Mode() != ModeServing || !strings.Contains(msg, "deferred") {
				t.Fatal("pending moved peer", msg)
			}
		})
	}
}

// Direct storage boundary: the closed transaction must refuse a reciprocal
// cross-star dock even if called without the dispatcher's legacy routing.
func TestCR17PeerUndockRemoteReciprocity(t *testing.T) {
	t.Parallel()
	f := peerUndockFixture(t)
	moveNamedUndockShip(t, f, "other star")
	st, e := f.store.GetStar(f.star)
	if e != nil {
		t.Fatal(e)
	}
	r, e := f.store.GetRace(1)
	if e != nil {
		t.Fatal(e)
	}
	in := commandrules.StarPeerUndock{Player: 1, Governor: 1, Selected: 77, Race: r, Star: st, MovedPeer: landingShip(t, f, 78)}
	for n := st.Ships; n != 0; {
		sh := landingShip(t, f, n)
		in.Orbit = append(in.Orbit, sh)
		n = sh.NextShip
	}
	want := factoryBuildRecords(t, f)
	out, e := f.store.CommitStarPeerUndock(in)
	var ce *db.CommandError
	if out != db.CommandNotCommitted || !errors.As(e, &ce) || ce.Code != db.CommandInvalid || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
		t.Fatal("remote reciprocal pair accepted as a stale link", out, e)
	}
}

func TestCR17PeerUndockMovedCrash(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"write:1", "before-commit", "after-commit"} {
		t.Run(stage, func(t *testing.T) { checkPeerUndockKill(t, landedUndockFixture(t), stage, 77) })
	}
}

// Capture can change the named ship's owner while the outgoing link
// persists. Only an actual reciprocal foreign pair takes the legacy route.
func TestCR17PeerUndockMovedForeignClaim(t *testing.T) {
	t.Parallel()
	f := landedUndockFixture(t)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		b := f.state.ships[78]
		b.Owner = 2
		if err := f.store.PutShip(b); err != nil {
			t.Fatal(err)
		}
	})
	checkPeerRelease(t, f, 1, 77, 78, 77)
}

func TestCR17PeerUndockCorruptMovedLink(t *testing.T) {
	t.Parallel()
	for _, storedOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("stored-only-%t", storedOnly), func(t *testing.T) {
			f := landedUndockFixture(t)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				b := factoryCopyShip(t, f.state.ships[78])
				b.NextShip = 77 // child also links into the selected exterior
				if !storedOnly {
					f.state.ships[78].NextShip = 77
				}
				if err := f.store.PutShip(b); err != nil {
					t.Fatal(err)
				}
			})
			before := factoryBuildRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			out := f.command(t, "launch #77")
			if f.state.Mode() != ModeHalted || strings.Contains(out, "undocked from") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) {
				t.Fatal("corrupt moved link escaped", out)
			}
			if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) {
				t.Fatal("stale flush", e)
			}
		})
	}
}
