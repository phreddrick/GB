package game

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/model"
)

func detachedUndockFixture(t *testing.T) *factoryFixture {
	t.Helper()
	f := detachedDockBuild(t, model.StypeCargo, false, []string{"build S 2"}, model.StypeStation, model.StypeStation)
	dockReopenedBuild(t, f)
	return f
}

// Assert both the result and absence of any persistence or output from either
// writer. On a HALT, also verify that shutdown cannot flush the actor.
func expectDetachedUndock(t *testing.T, f *factoryFixture, cmd, reply string, mode Mode) {
	t.Helper()
	before, actor := factoryBuildRecords(t, f), actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		f.state.testHooks.afterLegacyShipWrite = func(id uint32) { t.Errorf("%s wrote legacy ship %d", cmd, id) }
	})
	f.store.SetMetaFault(func(op string) error {
		if strings.HasPrefix(op, "peer-undock.") {
			t.Errorf("%s entered write operation %s", cmd, op)
		}
		return nil
	})
	out := f.command(t, cmd)
	f.store.SetMetaFault(nil)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.testHooks.afterLegacyShipWrite = nil })
	if f.state.Mode() != mode || !strings.Contains(out, reply) || strings.Contains(out, "undocked from") ||
		!reflect.DeepEqual(before, factoryBuildRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
		t.Fatalf("%s: got %q in %v, want %q in %v with unchanged actor and store", cmd, out, f.state.Mode(), reply, mode)
	}
	if mode == ModeHalted {
		e := f.state.Stop(ctxT(t, 10*time.Second))
		if !errors.Is(e, ErrHaltedNotFlushed) || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) {
			t.Fatal("halted shutdown changed the store", e)
		}
	}
}

func TestCR17PeerUndockDetachedBuild(t *testing.T) {
	t.Parallel()
	for _, id := range []uint32{77, 78} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			f := detachedUndockFixture(t)
			cmd := fmt.Sprintf("launch #%d", id)
			expectDetachedUndock(t, f, cmd, "unsettled", ModeServing)
			scheduledBuildTurnsAndRestart(t, f, func() {
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
				for _, pair := range [][2]uint32{{77, 78}, {78, 77}} {
					sh := landingShip(t, f, pair[0])
					if !sh.Docked || sh.DestShipNo != pair[1] {
						t.Fatal("repair changed docking", sh.Number)
					}
				}
			})

			want := factoryBuildRecords(t, f)
			rows := undockedRecords(t, f, want, 77, 78)
			notices := 0
			resp := &probeLaunchResponder{at: func(msg string) {
				notices++
				for n, sh := range rows {
					if !reflect.DeepEqual(sh, landingShip(t, f, n)) || !reflect.DeepEqual(sh, f.state.ships[n]) {
						t.Errorf("peer %d not durable and installed at %q", n, msg)
					}
				}
			}}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, cmd, resp); e != nil {
				t.Fatal(e)
			}
			if notices != 1 || !strings.Contains(resp.text(), "undocked from") || f.state.Mode() != ModeServing ||
				!reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("retry did not persist exactly the undock with zero AP cost", resp.text())
			}
		})
	}
}

func TestCR17PeerUndockDetachedDefers(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"detached row", "rooted row", "stored detached row", "actor repairs root", "store repairs root"} {
		t.Run(change, func(t *testing.T) {
			f := detachedUndockFixture(t)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				switch change {
				case "detached row":
					f.state.ships[78].Name = "pending Station rename"
				case "rooted row":
					f.state.ships[77].Name = "pending Cargo rename"
				case "stored detached row":
					sh := *f.state.ships[78]
					sh.Name = "saved Station rename"
					if err := f.store.PutShip(&sh); err != nil {
						t.Error(err)
					}
				case "actor repairs root":
					f.state.stars[f.star].Ships = 79
				case "store repairs root":
					st := *f.state.stars[f.star]
					st.Ships = 79
					if err := f.store.PutStar(&st); err != nil {
						t.Error(err)
					}
				}
			})
			for _, cmd := range []string{"launch #77", "launch #78"} {
				expectDetachedUndock(t, f, cmd, "deferred", ModeServing)
			}
		})
	}
}

func TestCR17PeerUndockDetachedCorruption(t *testing.T) {
	t.Parallel()
	for _, fault := range []struct{ name, cmd string }{
		{"actor cycle", "launch #77"},
		{"saved cycle", "launch #78"},
		{"saved dangling tail", "launch #77"},
		{"saved missing named peer", "launch #77"},
		{"actor invalid claim", "launch #78"},
		{"saved invalid claim", "launch #77"},
		{"saved invalid owner", "launch #78"},
		{"actor planet overlap", "launch #77"},
		{"saved planet overlap", "launch #78"},
		{"saved hold overlap", "launch #77"},
		{"saved universe overlap after actor repair", "launch #78"},
	} {
		t.Run(fault.name, func(t *testing.T) {
			f := detachedUndockFixture(t)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				row := *f.state.ships[78]
				var err error
				switch fault.name {
				case "actor cycle":
					f.state.ships[78].NextShip = 79
				case "saved cycle":
					row.NextShip = 79
					err = f.store.PutShip(&row)
				case "saved dangling tail":
					row.NextShip = 9999
					err = f.store.PutShip(&row)
				case "saved missing named peer":
					err = f.store.DeleteShip(78)
				case "actor invalid claim":
					f.state.ships[78].WhatDest = model.LevelPlan
				case "saved invalid claim":
					row.DestShipNo = 9999
					err = f.store.PutShip(&row)
				case "saved invalid owner":
					row.Owner = 0
					err = f.store.PutShip(&row)
				case "actor planet overlap":
					f.state.planets[f.star][f.planet].Ships = 79
				case "saved planet overlap":
					pl := *f.state.planets[f.star][f.planet]
					pl.Ships = 79
					err = f.store.PutPlanet(&pl)
				case "saved hold overlap":
					holder := *f.state.ships[77]
					holder.Ships = 79
					err = f.store.PutShip(&holder)
				case "saved universe overlap after actor repair":
					f.state.stars[f.star].Ships = 79
					data := *f.state.sdata
					data.Ships = 79
					err = f.store.PutStarData(&data)
				}
				if err != nil {
					t.Error(err)
				}
			})
			expectDetachedUndock(t, f, fault.cmd, "halted", ModeHalted)
		})
	}
}

func TestCR17PeerUndockDetachedLegacyClaims(t *testing.T) {
	t.Parallel()
	t.Run("overwritten peer", func(t *testing.T) {
		f := detachedBuildFixture(t, "carrier launch")
		dockReopenedBuild(t, f)
		if out := f.command(t, "dock X #77"); !strings.Contains(out, "docked with") ||
			landingShip(t, f, 77).DestShipNo != 75 || landingShip(t, f, 78).DestShipNo != 77 {
			t.Fatal("expected legacy overwrite preserving the previous claim", out)
		}
		expectDetachedUndock(t, f, "launch #78", "unsettled", ModeServing)
		if out := f.command(t, "launch X"); !strings.Contains(out, "undocked from") ||
			landingShip(t, f, 77).Docked || !landingShip(t, f, 78).Docked {
			t.Fatal("legacy launch did not leave a stale claim only on the detached ship", out)
		}
		expectDetachedUndock(t, f, "launch #78", "unsettled", ModeServing)
	})
	t.Run("detached self claim", func(t *testing.T) {
		f := pendingDockBuild(t, model.StypeCargo, false, []string{"build S"}, model.StypeStation)
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			f.state.dirs[0][0] = model.Directory{Level: model.LevelStar, StarNum: f.star}
		})
		if out := f.command(t, "dock S #78"); !strings.Contains(out, "docked with") ||
			landingShip(t, f, 78).DestShipNo != 78 {
			t.Fatal("expected legacy self claim", out)
		}
		reopenUnflushedBuild(t, f)
		expectDetachedUndock(t, f, "launch #78", "unsettled", ModeServing)
	})
	t.Run("both detached", func(t *testing.T) {
		f := pendingDockBuild(t, model.StypeCargo, false, []string{"build S 2"}, model.StypeStation, model.StypeStation)
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			f.state.dirs[0][0] = model.Directory{Level: model.LevelStar, StarNum: f.star}
		})
		// With the pending actor root, the numeric peer and both Stations
		// are visible to the legacy type selector before the unflushed stop.
		if out := f.command(t, "dock S #78"); !strings.Contains(out, "docked with") {
			t.Fatal(out)
		}
		a, b := landingShip(t, f, 78), landingShip(t, f, 79)
		if !a.Docked || !b.Docked || a.DestShipNo != 79 || b.DestShipNo != 78 {
			t.Fatal("expected the first Station to retain its claim on the second", a.DestShipNo, b.DestShipNo)
		}
		reopenUnflushedBuild(t, f)
		for _, cmd := range []string{"launch #78", "launch #79"} {
			expectDetachedUndock(t, f, cmd, "unsettled", ModeServing)
		}
		// The selected #78's suffix still ends at #77. A broken #79 must
		// halt even though validating only #78's suffix would miss it.
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			row := *f.state.ships[79]
			row.NextShip = 9999
			if err := f.store.PutShip(&row); err != nil {
				t.Error(err)
			}
		})
		expectDetachedUndock(t, f, "launch #78", "halted", ModeHalted)
	})
}
