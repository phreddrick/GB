package game

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

// Check the whole durable mutation, plus each witness's actor value and
// identity at the first opportunity for the client to observe success.
func checkPeerRelease(t *testing.T, f *factoryFixture, g int, selected, named uint32, changed ...uint32) {
	t.Helper()
	want := factoryBuildRecords(t, f)
	after := undockedRecords(t, f, want, changed...)
	actors := map[uint32]*model.Ship{}
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		for _, n := range []uint32{77, 78, 79, 80} {
			if sh := f.state.ships[n]; sh != nil {
				actors[n] = sh
				if after[n] == nil {
					after[n] = factoryCopyShip(t, sh)
				}
			}
		}
	})
	writes := 0
	f.store.SetMetaFault(func(op string) error {
		if strings.HasPrefix(op, "peer-undock.write:") {
			writes++
		}
		return nil
	})
	calls := 0
	resp := &probeLaunchResponder{at: func(msg string) {
		calls++
		for n, sh := range after {
			if f.state.ships[n] != actors[n] || !reflect.DeepEqual(actors[n], sh) || !reflect.DeepEqual(landingShip(t, f, n), sh) {
				t.Errorf("wrong peer state at notice for #%d: %q", n, msg)
			}
		}
	}}
	if err := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, fmt.Sprintf("launch #%d", selected), resp); err != nil {
		t.Fatal(err)
	}
	f.store.SetMetaFault(nil)
	if writes != len(changed) {
		t.Fatalf("wanted %d ship writes, saw %d", len(changed), writes)
	}
	if calls != 1 || resp.text() != fmt.Sprintf("c%d undocked from c%d.\n", selected, named) || f.state.Mode() != ModeServing || !reflect.DeepEqual(factoryBuildRecords(t, f), want) {
		t.Fatalf("wrong release or writes for #%d: %q", selected, resp.text())
	}
}

// The scheduled turn may change other gameplay fields, but each live ship
// remains uniquely reachable and each outgoing dock link survives as expected.
func checkRecoveryBoundary(t *testing.T, f *factoryFixture, links map[uint32]uint32, dead uint32) {
	t.Helper()
	scheduledBuildTurnsAndRestart(t, f, func() {
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		st, e := f.store.GetStar(f.star)
		if e != nil {
			t.Fatal(e)
		}
		seen := map[uint32]bool{}
		for n := st.Ships; n != 0; {
			sh := landingShip(t, f, n)
			if seen[n] || !sh.Alive || sh.StOrbits != st.Number || sh.WhatOrbits != model.LevelStar {
				t.Fatal("invalid star chain", n)
			}
			seen[n] = true
			n = sh.NextShip
		}
		for n, target := range links {
			sh := landingShip(t, f, n)
			if seen[n] != (n != dead) || sh.Alive != (n != dead) || sh.Docked != (target != 0) || sh.DestShipNo != target || (sh.WhatDest == model.LevelShip) != (target != 0) {
				t.Fatalf("turn or restart changed dock graph for #%d: alive=%v docked=%v target=%d reached=%v", n, sh.Alive, sh.Docked, sh.DestShipNo, seen[n])
			}
		}
	})
}

func killUndockPartner(t *testing.T, f *factoryFixture) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		sh := f.state.ships[78]
		f.state.killShip(1, sh)
		if err := f.store.PutShip(sh); err != nil {
			t.Fatal(err)
		}
	})
}

// Historical Go wrote both outgoing links even if it overwrote a target
// or docked a ship to itself. Keep these saved worlds recoverable after the
// explicit friendly dock begins refusing their creation.
func seedLegacyDock(t *testing.T, f *factoryFixture, source, target uint32) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		a, b := f.state.ships[source], f.state.ships[target]
		a.Docked, a.WhatDest, a.DestShipNo = true, model.LevelShip, target
		b.Docked, b.WhatDest, b.DestShipNo = true, model.LevelShip, source
		for _, ship := range []*model.Ship{a, b} {
			if e := f.store.PutShip(ship); e != nil {
				t.Fatal(e)
			}
		}
	})
}

func TestCR17PeerUndockRecovery(t *testing.T) {
	t.Parallel()
	for _, first := range []uint32{77, 78, 80} {
		t.Run(fmt.Sprintf("overwrite-first-%d", first), func(t *testing.T) {
			f := peerUndockFixture(t)
			// One more flying ship allows two independently stale claimants.
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				st := f.state.stars[f.star]
				sh := factoryCopyShip(t, f.state.ships[79])
				sh.Number, sh.NextShip = 80, st.Ships
				st.Ships = 80
				f.state.ships[80] = sh
				if e := f.store.PutShip(sh); e != nil {
					t.Fatal(e)
				}
				if e := f.store.PutStar(st); e != nil {
					t.Fatal(e)
				}
			})
			for _, n := range []uint32{79, 80} {
				seedLegacyDock(t, f, n, 78)
			}
			checkRecoveryBoundary(t, f, map[uint32]uint32{77: 78, 78: 80, 79: 78, 80: 78}, 0)
			if first == 77 {
				checkPeerRelease(t, f, 1, 77, 78, 77)
				checkPeerRelease(t, f, 2, 80, 78, 80, 78)
			} else {
				other := uint32(78)
				if first == 78 {
					other = 80
				}
				checkPeerRelease(t, f, 2, first, other, first, other)
				checkPeerRelease(t, f, 1, 77, 78, 77)
			}
			checkPeerRelease(t, f, 2, 79, 78, 79)
			checkRecoveryBoundary(t, f, map[uint32]uint32{77: 0, 78: 0, 79: 0, 80: 0}, 0)
		})
	}
	t.Run("self", func(t *testing.T) {
		f := peerUndockFixture(t)
		seedLegacyDock(t, f, 79, 79)
		checkRecoveryBoundary(t, f, map[uint32]uint32{77: 78, 78: 77, 79: 79}, 0)
		checkPeerRelease(t, f, 2, 79, 79, 79)
		checkRecoveryBoundary(t, f, map[uint32]uint32{77: 78, 78: 77, 79: 0}, 0)
	})
	for _, detached := range []bool{false, true} {
		t.Run(fmt.Sprintf("wreck-detached-%v", detached), func(t *testing.T) {
			f := peerUndockFixture(t)
			killUndockPartner(t, f)
			if detached {
				checkRecoveryBoundary(t, f, map[uint32]uint32{77: 78, 78: 77, 79: 0}, 78)
			}
			checkPeerRelease(t, f, 1, 77, 78, 77)
			checkRecoveryBoundary(t, f, map[uint32]uint32{77: 0, 78: 77, 79: 0}, 78)
		})
	}
}

func TestCR17PeerUndockRecoveryConflicts(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"selected", "other pair member", "named peer", "detached wreck", "pending wreck", "stored wreck links", "uncertain"} {
		t.Run(which, func(t *testing.T) {
			f := peerUndockFixture(t)
			id := uint32(78)
			switch which {
			case "detached wreck", "pending wreck", "stored wreck links":
				killUndockPartner(t, f)
				checkRecoveryBoundary(t, f, map[uint32]uint32{77: 78, 78: 77, 79: 0}, 78)
			default:
				seedLegacyDock(t, f, 79, 78)
				if which == "selected" {
					id = 77
				}
				if which == "other pair member" {
					id = 79
				}
			}
			want := factoryBuildRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			sh := landingShip(t, f, id)
			sh.Name = "different"
			if which == "stored wreck links" {
				sh.NextShip = 77
			}
			point := "peer-undock.before"
			if which == "uncertain" {
				point = "peer-undock.commit-error"
				undockedRecords(t, f, want, 77)
			} else {
				want[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(id))] = factoryBuildJSON(t, sh)
			}
			admission := which == "pending wreck" || which == "stored wreck links"
			if admission {
				if err := f.store.PutShip(sh); err != nil {
					t.Fatal(err)
				}
			}
			hit := false
			f.store.SetMetaFault(func(op string) error {
				if op == point {
					hit = true
					if which == "uncertain" {
						return errors.New("injected")
					}
					return f.store.PutShip(sh)
				}
				return nil
			})
			msg := f.command(t, "launch #77")
			f.store.SetMetaFault(nil)
			if hit == admission || strings.Contains(msg, "undocked from") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("recovery conflict published output or changed state", msg)
			}
			if which == "pending wreck" {
				if f.state.Mode() != ModeServing || !strings.Contains(msg, "deferred") {
					t.Fatal(msg)
				}
				return
			}
			if f.state.Mode() != ModeHalted || !strings.Contains(msg, "halted") || f.store.CommandWriteUncertain() != (which == "uncertain") {
				t.Fatal("wrong failure disposition", msg)
			}
			if err := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(err, ErrHaltedNotFlushed) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("stale flush", err)
			}
		})
	}
}

func TestCR17PeerUndockSingleWriteCrash(t *testing.T) {
	t.Parallel()
	for _, layout := range []string{"overwritten", "detached wreck"} {
		for _, stage := range []string{"before", "validated", "write:1", "before-commit", "after-commit", "preinstall", "installed"} {
			t.Run(layout+"/"+stage, func(t *testing.T) {
				f := peerUndockFixture(t)
				if layout == "overwritten" {
					seedLegacyDock(t, f, 79, 78)
				} else {
					killUndockPartner(t, f)
					checkRecoveryBoundary(t, f, map[uint32]uint32{77: 78, 78: 77, 79: 0}, 78)
				}
				checkPeerUndockKill(t, f, stage, 77)
			})
		}
	}
}

func TestCR17PeerUndockPreservesNavigationOrder(t *testing.T) {
	t.Parallel()
	f := peerUndockFixture(t)
	seedLegacyDock(t, f, 79, 78)
	checkPeerRelease(t, f, 2, 78, 79, 78, 79)
	// An undocked ship may receive a navigation order to the stale claimant.
	out := f.command(t, "order #78 destination #77")
	sh := landingShip(t, f, 78)
	if sh.Docked || sh.WhatDest != model.LevelShip || sh.DestShipNo != 77 {
		t.Fatalf("order setup failed: %q", out)
	}
	checkPeerRelease(t, f, 1, 77, 78, 77)
}

// Real scrap clears the victim's current partner, but an older claimant
// still points to the wreck. Its legacy AP debit first needs a persisted turn.
func TestCR17PeerUndockAfterScrap(t *testing.T) {
	t.Parallel()
	f := peerUndockFixture(t)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		st := f.state.stars[f.star]
		st.AP[0] = 1
		if e := f.store.PutStar(st); e != nil {
			t.Fatal(e)
		}
	})
	seedLegacyDock(t, f, 79, 78)
	if out := f.command(t, "scrap #78"); !strings.Contains(out, "Destroyed.") {
		t.Fatal(out)
	}
	want := factoryBuildRecords(t, f)
	if out := f.command(t, "launch #77"); !strings.Contains(out, "deferred") || f.state.Mode() != ModeServing || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
		t.Fatal("pending scrap", out)
	}
	checkRecoveryBoundary(t, f, map[uint32]uint32{77: 78, 78: 79, 79: 0}, 78)
	checkPeerRelease(t, f, 1, 77, 78, 77)
}

// Turn rebuild keeps living children in a dead ship's hangar, even though
// the parent is detached. The outgoing dock claim is unrelated to that hold.
func TestCR17PeerUndockOccupiedWreck(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"release", "late child", "stored missing child", "stored live exterior absent"} {
		t.Run(which, func(t *testing.T) {
			f := peerUndockFixture(t)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				b := f.state.ships[78]
				child := factoryCopyShip(t, f.state.ships[79])
				child.Number, child.Docked = 80, true
				f.state.insertShipInShip(child, b)
				f.state.ships[80] = child
				if e := f.store.PutShip(child); e != nil {
					t.Fatal(e)
				}
				if e := f.store.PutShip(b); e != nil {
					t.Fatal(e)
				}
			})
			killUndockPartner(t, f)
			checkRecoveryBoundary(t, f, map[uint32]uint32{77: 78, 78: 77, 79: 0}, 78)
			b, c := landingShip(t, f, 78), landingShip(t, f, 80)
			if b.Ships != 80 || c.WhatOrbits != model.LevelShip || c.DestShipNo != 78 || !c.Alive {
				t.Fatal("turn did not preserve wreck hold")
			}
			if which == "release" {
				checkPeerRelease(t, f, 1, 77, 78, 77)
				checkRecoveryBoundary(t, f, map[uint32]uint32{77: 0, 78: 77, 79: 0}, 78)
				if landingShip(t, f, 78).Ships != 80 || landingShip(t, f, 80).DestShipNo != 78 {
					t.Fatal("lost wreck child")
				}
				return
			}
			want := factoryBuildRecords(t, f)
			if which == "late child" {
				c.Name = "late"
				want[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(80))] = factoryBuildJSON(t, c)
				f.store.SetMetaFault(func(op string) error {
					if op == "peer-undock.before" {
						return f.store.PutShip(c)
					}
					return nil
				})
			} else {
				if which == "stored missing child" {
					b.Ships = 9999
				} else {
					b.Alive = true
				}
				if e := f.store.PutShip(b); e != nil {
					t.Fatal(e)
				}
				want = factoryBuildRecords(t, f)
			}
			msg := f.command(t, "launch #77")
			f.store.SetMetaFault(nil)
			if f.state.Mode() != ModeHalted || strings.Contains(msg, "undocked from") || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("invalid wreck hold accepted", msg)
			}
			if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("stale wreck flush", e)
			}
		})
	}
}
