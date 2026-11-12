package game

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

type planetCommandFixture struct {
	*actorEnrollmentFixture
	star, planet uint8
	ship         uint32
}

func TestCR17ALegacyFuelLoadCannotBeOverwrittenByPlanetEdit(t *testing.T) {
	for _, edit := range []string{"route 1 activate", "transfer 2 r 7"} {
		t.Run(strings.Fields(edit)[0], func(t *testing.T) {
			f := newPlanetCommandFixture(t)
			prepareLegacyFuelLoad(t, f.actorEnrollmentFixture, f.star, f.planet, f.ship, 500)
			before := f.actorPlanet(t)
			out := f.command(t, fmt.Sprintf("load #%d f 7", f.ship))
			if !strings.Contains(out, "7 fuel transferred") || before.Info[0].Fuel != 500 || f.storedPlanet(t).Info[0].Fuel != 493 || f.actorPlanet(t).Info[0].Fuel != 500 {
				t.Fatal("deferred legacy fuel load did not reach the stale-copy boundary")
			}
			ship, e := f.store.GetShip(f.ship)
			if e != nil || ship.Fuel != 7 {
				t.Fatal("load cargo was not persisted", e)
			}
			actorBefore, diskBefore := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			out = f.command(t, edit)
			if f.state.Mode() != ModeHalted || strings.Contains(out, "Set.") || strings.Contains(out, "transferred") || !strings.Contains(out, "operator review") {
				t.Fatal("new edit overwrote an uncorrected legacy fuel load or falsely succeeded")
			}
			if actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) != actorBefore || enrollmentDBSnapshot(t, f.actorEnrollmentFixture) != diskBefore {
				t.Fatal("preimage refusal changed actor/store/allocator/placement")
			}
			if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) {
				t.Fatal("halted state was flushed", e)
			}
			if enrollmentDBSnapshot(t, f.actorEnrollmentFixture) != diskBefore {
				t.Fatal("shutdown erased the stored legacy fuel load")
			}
		})
	}
}

func TestCR17APlanetEditWriteFailures(t *testing.T) {
	for _, command := range []string{"route 1 activate", "transfer 2 x 7"} {
		for _, stage := range []string{"before-error", "before-panic", "fence", "after-error", "after-panic"} {
			t.Run(strings.Fields(command)[0]+"/"+stage, func(t *testing.T) {
				f := newPlanetCommandFixture(t)
				actorBefore, diskBefore := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				ticketBefore, e := f.store.EnrollmentShipCandidate()
				if e != nil {
					t.Fatal(e)
				}
				var reached bool
				var proposed *model.Planet
				r := &recordingResponder{}
				registry := &fakeRegistry{}
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					f.state.SetRegistry(registry)
					f.state.testHooks.planetCommandWrite = func(p *model.Planet) error {
						reached = true
						proposed = copyCommandPlanet(p)
						if r.text() != "" || registry.count() != 0 {
							t.Error("success/notification escaped before the write")
						}
						switch stage {
						case "before-error":
							return errors.New("injected before write")
						case "before-panic":
							panic("injected before write")
						case "fence":
							f.store.SetWriteFence(true)
							defer f.store.SetWriteFence(false)
							return f.store.PutPlanet(p)
						}
						if e := f.store.PutPlanet(p); e != nil {
							return e
						}
						if stage == "after-panic" {
							panic("injected after visible commit")
						}
						return errors.New("error despite visible change")
					}
				})
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, command, r); e != nil {
					t.Fatal(e)
				}
				if !reached || f.state.Mode() != ModeHalted || strings.Contains(r.text(), "Set.") || strings.Contains(r.text(), "transferred") || !strings.Contains(r.text(), "could not be confirmed") {
					t.Fatal("write fault was not reached or did not fail closed")
				}
				if f.state.metrics.Commands.Value(strings.Fields(command)[0], "halted") != 1 {
					t.Error("failed persistence counted as successful command")
				}
				if actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) != actorBefore {
					t.Error("failed edit changed actor state/AP")
				}
				if strings.HasPrefix(stage, "after") {
					if !reflect.DeepEqual(f.storedPlanet(t), proposed) {
						t.Fatal("visible single-record commit incomplete")
					}
				} else if enrollmentDBSnapshot(t, f.actorEnrollmentFixture) != diskBefore {
					t.Error("known uncommitted fault changed persistent state")
				}
				if stage == "fence" && (f.store.FencedWrites() != 1 || f.store.LastFencedWrite() != "PutPlanet") {
					t.Error("write bypassed CR-18 fence/accounting")
				}
				ticketAfter, e := f.store.EnrollmentShipCandidate()
				if e != nil || ticketAfter != ticketBefore {
					t.Error("planet command changed enrollment allocator", e)
				}
				diskAfter := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				if e := f.state.TriggerSegment(ctxT(t, time.Second)); !errors.Is(e, ErrNotServing) {
					t.Error("halted segment accepted", e)
				}
				if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || enrollmentDBSnapshot(t, f.actorEnrollmentFixture) != diskAfter {
					t.Error("halted shutdown flushed stale state", e)
				}
			})
		}
	}
}

func TestCR17AInvalidPlanetCommandsDoNotMutate(t *testing.T) {
	for _, command := range []string{
		"route 0 activate", "route 99 activate", "route 1x activate", "route 99999999999999999999999999 activate",
		"route 1 activate extra", "route 1 deactivate extra", "route 1 land", "route 1 land bad", "route 1 land 0,", "route 1 land 0,0,1", "route 1 land -1,0", "route 1 land 3,0", "route 1 land 0,2", "route 1 load", "route 1 unload", "route 1 load f extra", "route 1 /missing/place",
		"transfer", "transfer 2 r", "transfer 2 r 1 extra", "transfer missing r 1", "transfer 2 z 1", "transfer 2 r -1", "transfer 2 r junk", "transfer 2 r 1junk", "transfer 2 r 999999999999999999999999999999", "transfer 2 r 1001",
	} {
		t.Run(command, func(t *testing.T) {
			f := newPlanetCommandFixture(t)
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			output := f.command(t, command)
			if output == "" || strings.Contains(output, "Set.") || strings.Contains(output, "transferred") {
				t.Error("invalid command appeared successful")
			}
			if f.state.Mode() != ModeServing || actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) != actor || enrollmentDBSnapshot(t, f.actorEnrollmentFixture) != disk {
				t.Error("invalid command changed world, AP, storage or mode")
			}
		})
	}
}

func TestCR17ARouteFieldsAndActorReads(t *testing.T) {
	for _, tc := range []struct {
		order string
		want  model.MerchantRoute
	}{
		{"activate", model.MerchantRoute{Set: true}},
		{"deactivate", model.MerchantRoute{}},
		{"land 2,1", model.MerchantRoute{X: 2, Y: 1}},
		{"load fdrx", model.MerchantRoute{Load: uint8(model.MFuel | model.MDestruct | model.MResources | model.MCrystals)}},
		{"unload rx", model.MerchantRoute{Unload: uint8(model.MResources | model.MCrystals)}},
		{"destination", model.MerchantRoute{DestPlanet: 1}},
	} {
		t.Run(tc.order, func(t *testing.T) {
			f := newPlanetCommandFixture(t)
			order := tc.order
			wantRoute := tc.want
			if order == "destination" {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					st := f.state.stars[f.star]
					order = "/" + st.Name + "/" + st.PlanetNames[1]
				})
				wantRoute.DestStar = f.star
			}
			before := f.actorPlanet(t)
			want := copyCommandPlanet(before)
			want.Info[0].Route[model.MaxRoutes-1] = wantRoute
			if output := f.command(t, fmt.Sprintf("route %d %s", model.MaxRoutes, order)); !strings.Contains(output, "Set.") {
				t.Fatal("valid route rejected")
			}
			if !reflect.DeepEqual(f.actorPlanet(t), want) || !reflect.DeepEqual(f.storedPlanet(t), want) {
				t.Error("route changed unintended fields or missed intended values")
			}
		})
	}
	// Read-only route reporting uses the actor, with no mutable DB copy/write.
	f := newPlanetCommandFixture(t)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		f.state.planets[f.star][f.planet].Info[0].Route[0] = model.MerchantRoute{Set: true, X: 2, Y: 1}
	})
	disk := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
	if out := f.command(t, "route"); !strings.Contains(out, "land  2, 1") || !strings.Contains(out, "Done.") || enrollmentDBSnapshot(t, f.actorEnrollmentFixture) != disk {
		t.Error("read-only routes did not use the actor or wrote the store")
	}
}

func TestCR17ATransferCommoditiesAndArithmetic(t *testing.T) {
	for _, commodity := range []string{"r", "x", "&", "f", "d"} {
		for _, target := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/%d", commodity, target), func(t *testing.T) {
				f := newPlanetCommandFixture(t)
				want := f.actorPlanet(t)
				from, to := &want.Info[0], &want.Info[target-1]
				switch commodity {
				case "r":
					from.Resource -= 7
					to.Resource += 7
				case "x", "&":
					from.Crystals -= 7
					to.Crystals += 7
				case "f":
					from.Fuel -= 7
					to.Fuel += 7
				case "d":
					from.Destruct -= 7
					to.Destruct += 7
				}
				if out := f.command(t, fmt.Sprintf("transfer %d %s 7", target, commodity)); !strings.Contains(out, "transferred") || !reflect.DeepEqual(f.actorPlanet(t), want) || !reflect.DeepEqual(f.storedPlanet(t), want) {
					t.Error("valid C-derived balance edit failed")
				}
			})
		}
	}
	for _, quantity := range []int{0, 1, 1000} {
		t.Run(fmt.Sprintf("resource-boundary-%d", quantity), func(t *testing.T) {
			f := newPlanetCommandFixture(t)
			// Preserve the defined unsigned-short assignment arithmetic; C also
			// wraps the destination. This PR does not invent a balance cap rule.
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				p := f.state.planets[f.star][f.planet]
				p.Info[1].Resource = 65535
				if e := f.store.PutPlanet(p); e != nil {
					t.Error(e)
				}
			})
			f.command(t, fmt.Sprintf("transfer 2 r %d", quantity))
			p := f.actorPlanet(t)
			if int(p.Info[0].Resource) != 1000-quantity || int(p.Info[1].Resource) != (65535+quantity)%65536 || !reflect.DeepEqual(p, f.storedPlanet(t)) {
				t.Error("historical uint16 arithmetic changed")
			}
		})
	}
}

func TestCR17AEnrollmentAdmissionAfterPlanetEdit(t *testing.T) {
	for _, cmd := range []string{"route 1 activate", "transfer 2 x 7"} {
		t.Run(strings.Fields(cmd)[0], func(t *testing.T) {
			f := newPlanetCommandFixture(t)
			f.command(t, cmd)
			before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			req := actorEnrollmentRequest("account-3")
			_, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), req)
			if strings.HasPrefix(cmd, "transfer") {
				assertActorEnrollmentFailure(t, e, "ENROLLMENT_PENDING_TURN", db.EnrollmentNotCommitted)
				if f.state.Mode() != ModeServing || enrollmentDBSnapshot(t, f.actorEnrollmentFixture) != before {
					t.Fatal("AP-only deferral wrote or halted")
				}
				if e := f.state.TriggerUpdate(ctxT(t, 10*time.Second)); e != nil {
					t.Fatal(e)
				}
				if _, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), req); e != nil {
					t.Fatal(e)
				}
			} else if e != nil {
				t.Fatal("route edit unnecessarily blocked enrollment", e)
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		})
	}
}

func TestCR17APlanetEditEffectsWaitForWrite(t *testing.T) {
	for _, command := range []string{"route 1 activate", "transfer 2 x 7"} {
		t.Run(strings.Fields(command)[0], func(t *testing.T) {
			f := newPlanetCommandFixture(t)
			before := f.storedPlanet(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				f.state.testHooks.planetCommandWrite = func(p *model.Planet) error {
					original := f.state.planets[f.star][f.planet]
					if p == original || &p.Sectors[0] == &original.Sectors[0] || !reflect.DeepEqual(original, before) {
						t.Error("proposal aliases or prematurely changes actor planet")
					}
					close(entered)
					<-release
					return f.store.PutPlanet(p)
				}
			})
			r := &recordingResponder{}
			done := make(chan error, 1)
			go func() { done <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, command, r) }()
			waitAcceptance(t, entered, "planet command write")
			if r.text() != "" || !reflect.DeepEqual(before, f.storedPlanet(t)) {
				t.Error("uncommitted edit was advertised or stored")
			}
			unblock()
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			if r.text() == "" || !reflect.DeepEqual(f.actorPlanet(t), f.storedPlanet(t)) {
				t.Error("committed edit not installed before result")
			}
		})
	}
}

func TestCR17AQuiescedAndHaltedPlanetEditsAreRefused(t *testing.T) {
	for _, mode := range []Mode{ModeQuiesced, ModeHalted} {
		for _, command := range []string{"route 1 activate", "transfer 2 x 7"} {
			t.Run(mode.String()+"/"+strings.Fields(command)[0], func(t *testing.T) {
				f := newPlanetCommandFixture(t)
				if mode == ModeQuiesced {
					if e := f.state.Quiesce(ctxT(t, 10*time.Second)); e != nil {
						t.Fatal(e)
					}
				} else {
					onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.halt("isolated refusal fixture", "") })
				}
				actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				out := f.command(t, command)
				if strings.Contains(out, "Set.") || strings.Contains(out, "transferred") || out == "" || actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) != actor || enrollmentDBSnapshot(t, f.actorEnrollmentFixture) != disk {
					t.Error("non-serving command changed state or falsely succeeded")
				}
			})
		}
	}
}

func TestCR17AStartedEditFinishesBeforeShutdown(t *testing.T) {
	for _, text := range []string{"route 1 activate", "transfer 2 x 7"} {
		t.Run(strings.Fields(text)[0], func(t *testing.T) {
			f := newPlanetCommandFixture(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				f.state.testHooks.planetCommandWrite = func(p *model.Planet) error {
					close(entered)
					<-release
					return f.store.PutPlanet(p)
				}
			})
			r := &recordingResponder{}
			done, stopped := make(chan error, 1), make(chan error, 1)
			go func() { done <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, text, r) }()
			waitAcceptance(t, entered, "started command")
			go func() { stopped <- f.state.Stop(ctxT(t, 10*time.Second)) }()
			deadline := time.Now().Add(5 * time.Second)
			for !f.state.draining.Load() {
				if time.Now().After(deadline) {
					t.Fatal("shutdown not admitted")
				}
				time.Sleep(time.Millisecond)
			}
			unblock()
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			if e := <-stopped; e != nil {
				t.Fatal(e)
			}
			if strings.Contains(text, "route") {
				if !strings.Contains(r.text(), "Set.") || !f.storedPlanet(t).Info[0].Route[0].Set {
					t.Error("admitted route withdrawn by drain")
				}
			} else if !strings.Contains(r.text(), "transferred") || f.storedPlanet(t).Info[0].Crystals != 93 || f.storedPlanet(t).Info[1].Crystals != 17 {
				t.Error("admitted transfer withdrawn by drain")
			}
			commit, e := f.store.GetCommitRecord()
			if e != nil || commit.Kind != db.CommitKindShutdown {
				t.Error("missing shutdown boundary", e)
			}
		})
	}
}

func TestCR17AUnreadablePreimageHaltsWithoutWrite(t *testing.T) {
	f := newPlanetCommandFixture(t)
	disk := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
	actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	if e := f.store.Close(); e != nil {
		t.Fatal(e)
	}
	out := f.command(t, "route 1 activate")
	if f.state.Mode() != ModeHalted || strings.Contains(out, "Set.") || actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) != actor {
		t.Fatal("unreadable preimage did not fail closed")
	}
	if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) {
		t.Fatal(e)
	}
	// Reopen only for isolated post-stop diagnosis, never to serve/retry or
	// clear an uncertainty guard. The failed operation attempted no write.
	store, e := db.Open(f.path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = store.Close() }()
	f.store = store
	if enrollmentDBSnapshot(t, f.actorEnrollmentFixture) != disk {
		t.Error("read failure modified durable world")
	}
}

func newPlanetCommandFixture(t *testing.T) *planetCommandFixture {
	t.Helper()
	f := newEnrollmentActor(t)
	a := enrollActor(t, f, actorEnrollmentRequest("operator")).Placement
	enrollActor(t, f, actorEnrollmentRequest("account-2"))
	p := &planetCommandFixture{actorEnrollmentFixture: f, star: a.Star, planet: a.Planet, ship: a.GovernmentShip}
	onEnrollmentActor(t, f, func() {
		pl := f.state.planets[p.star][p.planet]
		pl.Info[0].Resource, pl.Info[0].Fuel, pl.Info[0].Destruct, pl.Info[0].Crystals = 1000, 500, 60, 100
		pl.Info[1].Resource, pl.Info[1].Fuel, pl.Info[1].Destruct, pl.Info[1].Crystals = 10, 20, 30, 10
		// Crystal production cannot disguise or alter the tested balance across
		// the real update. Other normal population/production changes are allowed.
		for i := range pl.Sectors {
			pl.Sectors[i].Crystals = 0
		}
		pl.Sectors[1].Owner, pl.Sectors[1].Race, pl.Sectors[1].Popn = 2, 2, 2
		pl.Info[1].NumSectsOwned, pl.Info[1].Explored = 1, true
		pl.Popn += 2
		f.state.stars[p.star].AP[0] = 50
		if e := f.store.PutPlanet(pl); e != nil {
			t.Error(e)
		}
		if e := f.store.PutStar(f.state.stars[p.star]); e != nil {
			t.Error(e)
		}
	})
	return p
}

func copyCommandPlanet(p *model.Planet) *model.Planet {
	n := *p
	if p.Sectors != nil {
		n.Sectors = append([]model.Sector{}, p.Sectors...)
	}
	return &n
}

func (f *planetCommandFixture) actorPlanet(t *testing.T) *model.Planet {
	t.Helper()
	p, e := call(f.state, ctxT(t, 10*time.Second), opTest, func() (*model.Planet, error) {
		return copyCommandPlanet(f.state.planets[f.star][f.planet]), nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func (f *planetCommandFixture) command(t *testing.T, text string) string {
	t.Helper()
	r := &recordingResponder{}
	if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, text, r); e != nil {
		t.Fatal(e)
	}
	return r.text()
}

func (f *planetCommandFixture) storedPlanet(t *testing.T) *model.Planet {
	t.Helper()
	p, e := f.store.GetPlanet(f.star, f.planet)
	if e != nil || p == nil {
		t.Fatal("planet record unavailable", e)
	}
	return p
}

// The desired assertions are deliberately run BEFORE the implementation fix.
// Each case continues through segment/update/restart to preserve the original
// stale actor -> CommitWorld overwrite evidence, not just an existence check.
func TestCR17APlanetEditsSurviveBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		edited        func(*model.Planet)
	}{
		{"merchant-route", "route 1 activate", func(p *model.Planet) { p.Info[0].Route[0].Set = true }},
		{"stockpile-transfer", "transfer 2 x 7", func(p *model.Planet) { p.Info[0].Crystals -= 7; p.Info[1].Crystals += 7 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPlanetCommandFixture(t)
			before := f.actorPlanet(t)
			shipBefore, e := f.store.GetShip(f.ship)
			if e != nil {
				t.Fatal(e)
			}
			want := copyCommandPlanet(before)
			tc.edited(want)
			f.command(t, tc.command)
			actor, stored := f.actorPlanet(t), f.storedPlanet(t)
			if !reflect.DeepEqual(actor, want) {
				t.Error("command: actor planet does not contain the complete intended edit")
			}
			if !reflect.DeepEqual(stored, want) {
				t.Error("command: stored planet differs from complete intended edit")
			}
			shipAfter, e := f.store.GetShip(f.ship)
			if e != nil || !reflect.DeepEqual(shipBefore, shipAfter) {
				t.Error("single-planet command changed ship cargo/equipment/links", e)
			}
			for _, stage := range []string{"command", "segment", "update", "restart"} {
				switch stage {
				case "segment":
					if e := f.state.TriggerSegment(ctxT(t, 10*time.Second)); e != nil {
						t.Fatal(e)
					}
				case "update":
					if e := f.state.TriggerUpdate(ctxT(t, 10*time.Second)); e != nil {
						t.Fatal(e)
					}
				case "restart":
					f.restart(t)
				}
				actor, stored = f.actorPlanet(t), f.storedPlanet(t)
				t.Logf("%s: route actor/store=%t/%t; crystal balances actor=%d/%d store=%d/%d", stage, actor.Info[0].Route[0].Set, stored.Info[0].Route[0].Set, actor.Info[0].Crystals, actor.Info[1].Crystals, stored.Info[0].Crystals, stored.Info[1].Crystals)
				if !reflect.DeepEqual(actor, stored) {
					t.Errorf("%s: conflicting actor/store planet state", stage)
				}
				if actor.Info[0].Route != want.Info[0].Route || actor.Info[0].Crystals != want.Info[0].Crystals || actor.Info[1].Crystals != want.Info[1].Crystals {
					t.Errorf("%s: successfully persisted command edit was erased", stage)
				}
				sh, e := f.store.GetShip(f.ship)
				if e != nil || sh == nil || sh.Number != f.ship || sh.Owner != 1 || !sh.Alive || !sh.Docked || sh.WhatOrbits != model.LevelPlan || sh.StOrbits != f.star || sh.PNumOrbits != f.planet || stored.Ships != f.ship || sh.Resource != shipBefore.Resource || sh.Fuel != shipBefore.Fuel || sh.Destruct != shipBefore.Destruct || sh.Crystals != shipBefore.Crystals {
					t.Errorf("%s: government/cargo/orbit invariant", stage)
				}
				capital := stored.SectorAt(int(sh.LandX), int(sh.LandY))
				if capital.Owner != 1 || capital.Race != 1 || capital.Popn == 0 || stored.Popn < uint64(capital.Popn) || stored.Info[0].NumSectsOwned == 0 {
					t.Errorf("%s: colony sector/population/ownership invariant", stage)
				}
			}
		})
	}
}
