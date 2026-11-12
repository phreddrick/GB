package game

import (
	"context"
	"encoding/json"
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

// habitatFixture places one factory docked aboard one habitat that orbits the
// enrolled race's home planet. Numbers are chosen so that the legacy and the
// staged arithmetic are easy to check by hand: factory Size 2 (ship_size of a
// default factory: 1 + 0.01*20 crew + 0.02*50 cargo = 2.2 -> 2), BuildCost 7
// (activation cost 4*7 = 28), habitat MaxHanger 10 with Hanger 2. Activation
// leaves the factory at size 1 and the habitat at resource 72, hanger 1.
type habitatFixture struct {
	*actorEnrollmentFixture
	star, planet     uint8
	carrier, factory uint32
}

func newHabitatFixture(t *testing.T) *habitatFixture {
	return newHabitatFixtureWithCarrierOwner(t, 1)
}

func newHabitatFixtureWithCarrierOwner(t *testing.T, carrierOwner uint8) *habitatFixture {
	t.Helper()
	f := newEnrollmentActor(t)
	a := enrollActor(t, f, actorEnrollmentRequest("operator")).Placement
	if carrierOwner == 2 {
		// A real second empire is required for subsequent turns and restart,
		// not just an otherwise dangling Owner=2 on the habitat.
		enrollActor(t, f, actorEnrollmentRequest("account-2"))
	}
	x := &habitatFixture{f, a.Star, a.Planet, 70, 77}
	onEnrollmentActor(t, f, func() {
		pl := f.state.planets[a.Star][a.Planet]
		pl.Info[0].Resource = 100
		race := f.state.races[0]
		carrier := &model.Ship{}
		f.state.getshipDefaults(carrier, model.StypeHabitat, f.state.races[carrierOwner-1])
		carrier.Number = x.carrier
		carrier.Owner = carrierOwner
		carrier.Race = carrierOwner
		carrier.Governor = 0
		carrier.Alive = true
		carrier.Active = true
		carrier.Popn = 1
		carrier.Resource = 100
		carrier.Notified = true
		carrier.WhatOrbits = model.LevelPlan
		carrier.WhatDest = model.LevelPlan
		carrier.StOrbits = a.Star
		carrier.PNumOrbits = a.Planet
		carrier.DestStar = a.Star
		carrier.DestPNum = a.Planet
		carrier.XPos = f.state.stars[a.Star].XPos + pl.XPos
		carrier.YPos = f.state.stars[a.Star].YPos + pl.YPos
		carrier.NextShip = pl.Ships
		pl.Ships = carrier.Number
		fac := &model.Ship{}
		f.state.getshipDefaults(fac, model.OtypeFactory, race)
		fac.Number = x.factory
		fac.Owner = 1
		fac.Race = 1
		fac.Governor = 0
		fac.BuildCost = 7
		fac.BuildType = uint16(model.StypeCargo)
		fac.Alive = true
		fac.Active = true
		fac.On = false
		fac.Notified = true
		fac.Docked = true
		fac.Popn = 1
		fac.WhatOrbits = model.LevelShip
		fac.WhatDest = model.LevelShip
		fac.DestShipNo = carrier.Number
		fac.StOrbits = a.Star
		fac.PNumOrbits = a.Planet
		fac.XPos = carrier.XPos
		fac.YPos = carrier.YPos
		fac.NextShip = carrier.Ships
		carrier.Ships = fac.Number
		carrier.Hanger = fac.Size
		f.state.ships[carrier.Number] = carrier
		f.state.ships[fac.Number] = fac
		for _, sh := range []*model.Ship{carrier, fac} {
			if e := f.store.PutShip(sh); e != nil {
				t.Error(e)
			}
		}
		if e := f.store.PutPlanet(pl); e != nil {
			t.Error(e)
		}
	})
	return x
}

type habitatWorld struct {
	ActorFactory, StoredFactory *model.Ship
	ActorCarrier, StoredCarrier *model.Ship
	Output                      string `json:"output,omitempty"`
}

func (f *habitatFixture) captureOnActor(t *testing.T) habitatWorld {
	t.Helper()
	fac, e := f.store.GetShip(f.factory)
	if e != nil {
		t.Error(e)
	}
	car, e := f.store.GetShip(f.carrier)
	if e != nil {
		t.Error(e)
	}
	return habitatWorld{ActorFactory: factoryCopyShip(t, f.state.ships[f.factory]), StoredFactory: fac, ActorCarrier: factoryCopyShip(t, f.state.ships[f.carrier]), StoredCarrier: car}
}
func (f *habitatFixture) snapshot(t *testing.T) habitatWorld {
	t.Helper()
	var out habitatWorld
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { out = f.captureOnActor(t) })
	return out
}
func (f *habitatFixture) command(t *testing.T, text string) string {
	t.Helper()
	r := &recordingResponder{}
	if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, text, r); e != nil {
		t.Fatal(e)
	}
	return r.text()
}
func (f *habitatFixture) activate(t *testing.T) string {
	t.Helper()
	return f.command(t, fmt.Sprintf("order #%d on", f.factory))
}

// habitatExpected returns the complete expected factory/carrier pair after a
// confirmed activation of the fixture.
func habitatExpected(t *testing.T, before habitatWorld) habitatWorld {
	t.Helper()
	x := habitatWorld{ActorFactory: factoryCopyShip(t, before.ActorFactory), StoredFactory: factoryCopyShip(t, before.StoredFactory), ActorCarrier: factoryCopyShip(t, before.ActorCarrier), StoredCarrier: factoryCopyShip(t, before.StoredCarrier)}
	for _, sh := range []*model.Ship{x.ActorFactory, x.StoredFactory} {
		sh.On = true
		sh.Notified = false
		sh.Size = 1
	}
	for _, sh := range []*model.Ship{x.ActorCarrier, x.StoredCarrier} {
		sh.Resource = 72
		sh.Hanger = 1
	}
	return x
}

// habitatAux serializes everything except the two written ships: all other
// ships, planets, stars, races, metadata and both free lists.
func habitatAux(t *testing.T, f *habitatFixture) string {
	t.Helper()
	ships, e := f.store.AllShips()
	if e != nil {
		t.Fatal(e)
	}
	delete(ships, f.factory)
	delete(ships, f.carrier)
	var all []any
	all = append(all, ships)
	for _, key := range [][]byte{db.KeyCommit, db.KeySchedule, db.KeyUniverse, db.KeyMaintenance, []byte("enrollment/v1/placement/operator-request")} {
		v, e := f.store.GetMeta(key)
		if e != nil {
			t.Fatal(e)
		}
		all = append(all, v)
	}
	races, e := f.store.AllRaces()
	if e != nil {
		t.Fatal(e)
	}
	free, e := f.store.FreeShipNums()
	if e != nil {
		t.Fatal(e)
	}
	cf, e := f.store.FreeCommodNums()
	if e != nil {
		t.Fatal(e)
	}
	sd, e := f.store.GetStarData()
	if e != nil {
		t.Fatal(e)
	}
	stars, e := f.store.AllStars()
	if e != nil {
		t.Fatal(e)
	}
	planets, e := f.store.AllPlanets()
	if e != nil {
		t.Fatal(e)
	}
	all = append(all, races, free, cf, sd, stars, planets)
	b, e := json.Marshal(all)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func assertHabitatOrbit(t *testing.T, f *habitatFixture) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		pl := f.state.planets[f.star][f.planet]
		seen := map[uint32]bool{}
		for n := pl.Ships; n != 0; {
			sh := f.state.ships[n]
			if sh == nil || seen[n] || !sh.Alive || sh.WhatOrbits != model.LevelPlan || sh.StOrbits != f.star || sh.PNumOrbits != f.planet {
				t.Error("invalid planet orbital identity")
				return
			}
			seen[n] = true
			n = sh.NextShip
		}
		car := f.state.ships[f.carrier]
		docked := map[uint32]bool{}
		for n := car.Ships; n != 0; {
			sh := f.state.ships[n]
			if sh == nil || docked[n] || !sh.Alive || sh.WhatOrbits != model.LevelShip || sh.DestShipNo != f.carrier {
				t.Error("invalid carrier chain identity")
				return
			}
			docked[n] = true
			n = sh.NextShip
		}
		if !seen[f.carrier] || !docked[f.factory] || !seen[f.state.races[0].GovShip] || !f.state.governed(f.state.races[0]) {
			t.Error("carrier/factory/government identity lost")
		}
	})
}

func TestCR17CHabitatCompleteStateAndBoundaries(t *testing.T) {
	for _, owner := range []uint8{1, 2} {
		t.Run(fmt.Sprintf("carrier-owner-%d", owner), func(t *testing.T) {
			testHabitatCompleteStateAndBoundaries(t, newHabitatFixtureWithCarrierOwner(t, owner))
		})
	}
}

func testHabitatCompleteStateAndBoundaries(t *testing.T, f *habitatFixture) {
	t.Helper()
	before := f.snapshot(t)
	want := habitatExpected(t, before)
	aux := habitatAux(t, f)
	var originalFactory, originalCarrier *model.Ship
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { originalFactory = f.state.ships[f.factory]; originalCarrier = f.state.ships[f.carrier] })
	out := f.activate(t)
	if !strings.Contains(out, "Factory activated at a cost of 28 resources") || !strings.Contains(out, "/on") || !strings.HasPrefix(out, "    #       name") {
		t.Fatal("activation output missing or reordered", out)
	}
	after := f.snapshot(t)
	after.Output = ""
	if !reflect.DeepEqual(after, want) {
		t.Fatal("complete factory/carrier proposal not installed/persisted")
	}
	if after.StoredCarrier.Mass != before.StoredCarrier.Mass || after.StoredFactory.Mass != before.StoredFactory.Mass {
		t.Fatal("mass changed; order.c changes no mass on activation")
	}
	if habitatAux(t, f) != aux {
		t.Fatal("unrelated records changed")
	}
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		if f.state.ships[f.factory] != originalFactory || f.state.ships[f.carrier] != originalCarrier {
			t.Error("actor object address replaced")
		}
	})
	assertHabitatOrbit(t, f)
	for _, stage := range []string{"restart before turn", "segment", "update", "restart"} {
		var e error
		switch stage {
		case "segment":
			e = f.state.TriggerSegment(ctxT(t, 10*time.Second))
		case "update":
			e = f.state.TriggerUpdate(ctxT(t, 10*time.Second))
		case "restart before turn", "restart":
			f.restart(t)
		}
		if e != nil {
			t.Fatal(e)
		}
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		assertHabitatOrbit(t, f)
		x := f.snapshot(t)
		if !x.StoredFactory.On || x.StoredCarrier.Resource != 72 || x.StoredCarrier.Hanger != 1 || x.StoredFactory.Number != f.factory || x.StoredCarrier.Number != f.carrier || !x.StoredFactory.Alive || !x.StoredCarrier.Alive {
			t.Fatal(stage, "activation lost or cost refunded")
		}
		wantCarrierOwner := before.StoredCarrier.Owner
		if stage != "restart before turn" {
			// Existing doShip mirrors doship.c:112-118: a carried ship
			// synchronizes its containing ship's owner/governor. Activation
			// itself and a restart before any turn preserve foreign ownership.
			// Go lacks C's earlier doown prepass (CR-13); this assertion
			// preserves that known turn behavior, not complete C turn parity.
			wantCarrierOwner = before.StoredFactory.Owner
		}
		if x.StoredFactory.Owner != before.StoredFactory.Owner || x.StoredFactory.Race != before.StoredFactory.Race || x.StoredCarrier.Owner != wantCarrierOwner || x.StoredCarrier.Race != before.StoredCarrier.Race {
			t.Fatal(stage, "factory/carrier ownership differs from existing turn rules")
		}
		// doship.c:110 (and State.doShip) recompute every ship's size each
		// turn, so the 0.2-scaled factory size reverts to ship_size after the
		// first turn while the habitat keeps the activation-time hanger; this
		// is the original C behaviour and is not changed here.
		if stage == "segment" && x.StoredFactory.Size != 2 {
			t.Fatal("turn-time size semantics changed", x.StoredFactory.Size)
		}
	}
}

func TestCR17CHabitatWriteFailures(t *testing.T) {
	for _, owner := range []uint8{1, 2} {
		t.Run(fmt.Sprintf("carrier-owner-%d", owner), func(t *testing.T) {
			testHabitatWriteFailures(t, owner)
		})
	}
}

func testHabitatWriteFailures(t *testing.T, carrierOwner uint8) {
	t.Helper()
	for _, stage := range []string{"habitat.before", "habitat.validated", "habitat.write:1", "habitat.write:2", "habitat.before-commit", "habitat.commit-error", "habitat.after-commit", "fence"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				f := newHabitatFixtureWithCarrierOwner(t, carrierOwner)
				before := f.snapshot(t)
				want := habitatExpected(t, before)
				actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				aux := habitatAux(t, f)
				reached := false
				r := &recordingResponder{}
				f.store.SetMetaFault(func(op string) error {
					if stage == "fence" && op == "habitat.before" {
						reached = true
						f.store.SetWriteFence(true)
						return nil
					}
					if op == stage {
						reached = true
						if r.text() != "" {
							t.Error("success/header escaped before confirmed persistence")
						}
						if panics {
							panic("secret fault payload")
						}
						return errors.New("secret fault payload")
					}
					return nil
				})
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, fmt.Sprintf("order #%d on", f.factory), r); e != nil {
					t.Fatal(e)
				}
				f.store.SetWriteFence(false)
				if !reached || f.state.Mode() != ModeHalted || strings.Contains(r.text(), "Factory activated") || strings.Contains(r.text(), "secret") {
					t.Fatal("fault not exercised or false success")
				}
				x := f.snapshot(t)
				committed := stage == "habitat.after-commit"
				visible := committed || stage == "habitat.commit-error"
				if visible {
					if !reflect.DeepEqual(x.StoredFactory, want.StoredFactory) || !reflect.DeepEqual(x.StoredCarrier, want.StoredCarrier) {
						t.Fatal("visible transaction incomplete: partial factory/carrier records")
					}
				} else if disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("aborted transaction left records/allocator/placement changes")
				}
				if committed {
					if !reflect.DeepEqual(x.ActorFactory, x.StoredFactory) || !reflect.DeepEqual(x.ActorCarrier, x.StoredCarrier) {
						t.Fatal("confirmed reporting error did not install complete state")
					}
				} else if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("unconfirmed change installed actor state")
				}
				if aux != habitatAux(t, f) {
					t.Fatal("auxiliary records changed")
				}
				if stage == "fence" && (f.store.FencedWrites() != 1 || f.store.LastFencedWrite() != "CommitHabitatActivation") {
					t.Fatal("write fence accounting bypassed")
				}
				after := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				if e := f.state.TriggerUpdate(ctxT(t, time.Second)); !errors.Is(e, ErrNotServing) {
					t.Fatal("halted update admitted", e)
				}
				if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || after != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("halted shutdown flushed stale proposal", e)
				}
				if stage == "habitat.commit-error" {
					f.store.SetMetaFault(nil)
					f.open(t)
					if f.state.Mode() != ModeHalted || after != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
						t.Fatal("reused handle bypassed uncertainty guard")
					}
				}
			})
		}
	}
}

func TestCR17CHabitatValidationAndSelectors(t *testing.T) {
	for _, command := range []string{"order #77 on extra", "order * on", "order F on", "order #4294967373 on", "order 4294967373 on", "order #18446744073709551616 on", "order #77junk on", "order # on", "order #0 on"} {
		t.Run(command, func(t *testing.T) {
			f := newHabitatFixture(t)
			// Wildcard/type selectors only reach ships of the current scope, so
			// the docked factory is addressable this way only from the carrier.
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				f.state.dirs[0][0] = model.Directory{Level: model.LevelShip, StarNum: f.star, PlanetNum: f.planet, ShipNo: f.carrier}
			})
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			out := f.command(t, command)
			if strings.Contains(out, "Factory activated") || f.state.Mode() != ModeServing || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("invalid/ambiguous selector changed records")
			}
		})
	}
	for _, kind := range []string{"insufficient resources", "insufficient hanger", "hanger below docked size", "already on", "inactive", "no crew", "unauthorized governor", "inactive governor", "transported by non-habitat", "zero AP"} {
		t.Run(kind, func(t *testing.T) {
			f := newHabitatFixture(t)
			g := 0
			wantText := ""
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				sh := f.state.ships[f.factory]
				car := f.state.ships[f.carrier]
				switch kind {
				case "insufficient resources":
					car.Resource = 27
					wantText = "You don't have 28 resources on Habitat #70"
				case "insufficient hanger":
					car.MaxHanger = 2
					car.Hanger = 2
					sh.Size = 0
					wantText = "Not enough hanger space free on Habitat #70. Need 1 more."
				case "hanger below docked size":
					car.Hanger = 1
					wantText = "hanger accounting is inconsistent"
				case "already on":
					sh.On = true
					wantText = "already activated"
				case "inactive":
					sh.Active = false
					wantText = "irradiated"
				case "no crew":
					sh.Popn = 0
					wantText = "has no crew"
				case "unauthorized governor":
					g = 1
					sh.Governor = 2
					f.state.races[0].Governor[1].Active = true
					f.state.dirs[0][1] = f.state.dirs[0][0]
					wantText = "not authorized"
				case "inactive governor":
					g = 1
					sh.Governor = 1
					f.state.dirs[0][1] = f.state.dirs[0][0]
					wantText = "not authorized"
				case "transported by non-habitat":
					car.Type = model.StypeCargo
					wantText = "currently being transported"
				case "zero AP":
					f.state.stars[f.star].AP[0] = 0
				}
				for _, s := range []*model.Ship{sh, car} {
					if e := f.store.PutShip(s); e != nil {
						t.Error(e)
					}
				}
				if e := f.store.PutRace(f.state.races[0]); e != nil {
					t.Error(e)
				}
				if e := f.store.PutStar(f.state.stars[f.star]); e != nil {
					t.Error(e)
				}
			})
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			r := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, "order #77 on", r); e != nil {
				t.Fatal(e)
			}
			if kind == "zero AP" {
				x := f.snapshot(t)
				if !strings.Contains(r.text(), "Factory activated") || !x.StoredFactory.On || x.StoredCarrier.Resource != 72 || f.state.stars[f.star].AP[0] != 0 {
					t.Fatal("zero AP must not block a factory activation (no AP charge)")
				}
				return
			}
			if strings.Contains(r.text(), "Factory activated") || !strings.Contains(r.text(), wantText) || f.state.Mode() != ModeServing || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatalf("validation failure escaped workspace or wrong text: %q", r.text())
			}
		})
	}
}

func TestCR17CHabitatCrossOwnerStaleCarrier(t *testing.T) {
	for _, field := range []string{"owner", "resources"} {
		t.Run(field, func(t *testing.T) {
			f := newHabitatFixtureWithCarrierOwner(t, 2)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			var disk string
			reached := false
			f.store.SetMetaFault(func(op string) error {
				if op == "habitat.before" {
					reached = true
					car, e := f.store.GetShip(f.carrier)
					if e != nil {
						t.Error(e)
						return e
					}
					if field == "owner" {
						car.Owner = 1
					} else {
						car.Resource++
					}
					if e := f.store.PutShip(car); e != nil {
						t.Error(e)
						return e
					}
					disk = enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				}
				return nil
			})
			out := f.activate(t)
			if !reached || disk == "" || f.state.Mode() != ModeHalted || strings.Contains(out, "Factory activated") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("changed foreign carrier preimage was overwritten or installed", out)
			}
			if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("stale carrier permitted shutdown flushing", e)
			}
		})
	}
}

func TestCR17CHabitatCarrierOwnershipDoesNotAuthorizeFactory(t *testing.T) {
	f := newHabitatFixtureWithCarrierOwner(t, 2)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		fac, car := f.state.ships[f.factory], f.state.ships[f.carrier]
		// Player 1 owns the habitat, but player 2 owns its docked factory.
		fac.Owner, fac.Race = 2, 2
		car.Owner, car.Race = 1, 1
		for _, sh := range []*model.Ship{fac, car} {
			if e := f.store.PutShip(sh); e != nil {
				t.Error(e)
			}
		}
	})
	actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
	if out := f.activate(t); strings.Contains(out, "Factory activated") || f.state.Mode() != ModeServing || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
		t.Fatal("habitat ownership authorized another player's factory", out)
	}
	// The staged entry independently refuses the same unauthorized factory.
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		if result := f.state.runHabitatActivation(1, 0, f.factory, &recordingResponder{}); result != commandValidationFailed {
			t.Error("staged entry bypassed factory authority", result)
		}
	})
	if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
		t.Fatal("unauthorized staged entry changed records")
	}
}

func TestCR17CHabitatOriginalCArithmetic(t *testing.T) {
	for _, tc := range []struct {
		cost      uint16
		remaining uint16
		success   bool
	}{{0, 65535, true}, {7, 65507, true}, {16383, 3, true}, {16384, 65535, false}, {32768, 65535, false}, {65535, 65535, false}} {
		t.Run(fmt.Sprint(tc.cost), func(t *testing.T) {
			f := newHabitatFixture(t)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				sh := f.state.ships[f.factory]
				sh.BuildCost = tc.cost
				sh.Damage = 31
				car := f.state.ships[f.carrier]
				car.Resource = 65535
				for _, s := range []*model.Ship{sh, car} {
					if e := f.store.PutShip(s); e != nil {
						t.Error(e)
					}
				}
			})
			before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			out := f.activate(t)
			x := f.snapshot(t)
			if tc.success {
				if !strings.Contains(out, fmt.Sprintf("cost of %d resources", int(tc.cost)*4)) || !x.StoredFactory.On || x.StoredCarrier.Resource != tc.remaining || x.StoredFactory.Size != 1 || x.StoredCarrier.Hanger != 1 {
					t.Fatal("C cost/damaged factory rule diverged", out)
				}
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			} else if strings.Contains(out, "Factory activated") || !strings.Contains(out, fmt.Sprintf("You don't have %d resources", int(tc.cost)*4)) || before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || f.state.Mode() != ModeServing {
				t.Fatal("wide C cost wrapped to affordable activation", out)
			}
		})
	}
	t.Run("over-full hanger uses signed C arithmetic", func(t *testing.T) {
		// max 10, hanger 12, docked size 2: C free space (10-12)+2 = 0 < 1.
		// The old uint16 subtraction computed 65534 free and activated.
		f := newHabitatFixture(t)
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			car := f.state.ships[f.carrier]
			car.Hanger = 12
			if e := f.store.PutShip(car); e != nil {
				t.Error(e)
			}
		})
		before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
		out := f.activate(t)
		if strings.Contains(out, "Factory activated") || !strings.Contains(out, "Need 1 more") || before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
			t.Fatal("uint16 underflow activated an over-full habitat", out)
		}
	})
	t.Run("large factory size arithmetic", func(t *testing.T) {
		// ship_size 65537 -> new size 13108; hanger 65535 - 65535 + 13108.
		f := newHabitatFixture(t)
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			sh := f.state.ships[f.factory]
			sh.MaxHanger = 65535
			sh.Size = 65535
			car := f.state.ships[f.carrier]
			car.MaxHanger = 65535
			car.Hanger = 65535
			for _, s := range []*model.Ship{sh, car} {
				if e := f.store.PutShip(s); e != nil {
					t.Error(e)
				}
			}
		})
		out := f.activate(t)
		x := f.snapshot(t)
		if !strings.Contains(out, "Factory activated") || x.StoredFactory.Size != 13108 || x.StoredCarrier.Hanger != 13108 || x.StoredCarrier.Resource != 72 {
			t.Fatal("boundary size arithmetic", out, x.StoredFactory.Size, x.StoredCarrier.Hanger)
		}
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	})
}

func TestCR17CHabitatNestedCarrierAndLocations(t *testing.T) {
	for _, where := range []string{"nested habitat", "star orbit", "universe"} {
		t.Run(where, func(t *testing.T) {
			f := newHabitatFixture(t)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				car := f.state.ships[f.carrier]
				pl := f.state.planets[f.star][f.planet]
				// Unlink the carrier from the planet chain (it is the head).
				pl.Ships = car.NextShip
				car.NextShip = 0
				switch where {
				case "nested habitat":
					mother := &model.Ship{}
					f.state.getshipDefaults(mother, model.StypeHabitat, f.state.races[0])
					mother.Number = 60
					mother.Owner = 1
					mother.Race = 1
					mother.Alive = true
					mother.Active = true
					mother.Popn = 1
					mother.MaxHanger = 2000
					mother.WhatOrbits = model.LevelPlan
					mother.WhatDest = model.LevelPlan
					mother.StOrbits = f.star
					mother.PNumOrbits = f.planet
					mother.NextShip = pl.Ships
					pl.Ships = mother.Number
					car.WhatOrbits = model.LevelShip
					car.WhatDest = model.LevelShip
					car.DestShipNo = mother.Number
					car.Docked = true
					car.NextShip = mother.Ships
					mother.Ships = car.Number
					mother.Hanger = car.Size
					f.state.ships[mother.Number] = mother
					if e := f.store.PutShip(mother); e != nil {
						t.Error(e)
					}
				case "star orbit":
					st := f.state.stars[f.star]
					car.WhatOrbits = model.LevelStar
					car.NextShip = st.Ships
					st.Ships = car.Number
					f.state.ships[f.factory].PNumOrbits = car.PNumOrbits
					if e := f.store.PutStar(st); e != nil {
						t.Error(e)
					}
				case "universe":
					car.WhatOrbits = model.LevelUniv
					car.NextShip = f.state.sdata.Ships
					f.state.sdata.Ships = car.Number
					if e := f.store.PutStarData(f.state.sdata); e != nil {
						t.Error(e)
					}
				}
				for _, s := range []*model.Ship{car, f.state.ships[f.factory]} {
					if e := f.store.PutShip(s); e != nil {
						t.Error(e)
					}
				}
				if e := f.store.PutPlanet(pl); e != nil {
					t.Error(e)
				}
			})
			before := f.snapshot(t)
			want := habitatExpected(t, before)
			aux := habitatAux(t, f)
			out := f.activate(t)
			after := f.snapshot(t)
			if !strings.Contains(out, "Factory activated at a cost of 28 resources") || !reflect.DeepEqual(after, want) || habitatAux(t, f) != aux {
				t.Fatal("activation through a nested/other-root location failed or leaked", out)
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		})
	}
}

func TestCR17CHabitatStalePreimagesAndIntegrity(t *testing.T) {
	for _, kind := range []string{"legacy fuel load", "stored carrier", "stored factory", "stored docked witness", "stored planet", "stored star AP", "stored race", "after proposal", "actor carrier chain cycle", "actor planet chain cycle", "actor missing carrier", "actor dead carrier", "actor missing planet", "actor carrier location mismatch", "actor destination mismatch", "factory absent from carrier chain", "carrier absent from planet chain", "witness pointer replaced"} {
		t.Run(kind, func(t *testing.T) {
			f := newHabitatFixture(t)
			switch kind {
			case "legacy fuel load":
				prepareLegacyFuelLoad(t, f.actorEnrollmentFixture, f.star, f.planet, f.state.races[0].GovShip, 100)
				out := f.command(t, fmt.Sprintf("load #%d f 1", f.state.races[0].GovShip))
				if !strings.Contains(out, "transferred") {
					t.Fatal("legacy fuel load not exercised")
				}
			case "stored carrier":
				sh, e := f.store.GetShip(f.carrier)
				if e != nil {
					t.Fatal(e)
				}
				sh.Resource++
				if e := f.store.PutShip(sh); e != nil {
					t.Fatal(e)
				}
			case "stored factory":
				sh, e := f.store.GetShip(f.factory)
				if e != nil {
					t.Fatal(e)
				}
				sh.Resource++
				if e := f.store.PutShip(sh); e != nil {
					t.Fatal(e)
				}
			case "stored docked witness", "actor carrier chain cycle", "factory absent from carrier chain":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					car := f.state.ships[f.carrier]
					w := &model.Ship{Number: 79, Owner: 1, Race: 1, Type: model.StypeCargo, Alive: true, Active: true, Docked: true, WhatOrbits: model.LevelShip, WhatDest: model.LevelShip, DestShipNo: f.carrier, StOrbits: f.star, PNumOrbits: f.planet, NextShip: car.Ships}
					car.Ships = w.Number
					f.state.ships[w.Number] = w
					if e := f.store.PutShip(w); e != nil {
						t.Error(e)
					}
					if e := f.store.PutShip(car); e != nil {
						t.Error(e)
					}
					switch kind {
					case "stored docked witness":
						stale := *w
						stale.Resource++
						if e := f.store.PutShip(&stale); e != nil {
							t.Error(e)
						}
					case "actor carrier chain cycle":
						f.state.ships[f.factory].NextShip = w.Number
					case "factory absent from carrier chain":
						w.NextShip = 0
					}
				})
			case "stored planet":
				p, e := f.store.GetPlanet(f.star, f.planet)
				if e != nil {
					t.Fatal(e)
				}
				p.Sectors[0].Resource++
				if e := f.store.PutPlanet(p); e != nil {
					t.Fatal(e)
				}
			case "stored star AP":
				st, e := f.store.GetStar(f.star)
				if e != nil {
					t.Fatal(e)
				}
				st.AP[0]--
				if e := f.store.PutStar(st); e != nil {
					t.Fatal(e)
				}
			case "stored race":
				r, e := f.store.GetRace(1)
				if e != nil {
					t.Fatal(e)
				}
				r.Tech++
				if e := f.store.PutRace(r); e != nil {
					t.Fatal(e)
				}
			case "actor planet chain cycle":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.ships[f.carrier].NextShip = f.carrier })
			case "actor missing carrier":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { delete(f.state.ships, f.carrier) })
			case "actor dead carrier":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.ships[f.carrier].Alive = false })
			case "actor missing planet":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { delete(f.state.planets[f.star], f.planet) })
			case "actor carrier location mismatch":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.ships[f.factory].PNumOrbits = 1 })
			case "actor destination mismatch":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.ships[f.factory].WhatDest = model.LevelPlan })
			case "carrier absent from planet chain":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					f.state.planets[f.star][f.planet].Ships = f.state.ships[f.carrier].NextShip
				})
			case "witness pointer replaced":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					f.state.testHooks.beforeCommandInstall = func() {
						n := f.state.races[0].GovShip
						sh := *f.state.ships[n]
						f.state.ships[n] = &sh
					}
				})
			}
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			if kind == "after proposal" {
				f.store.SetMetaFault(func(op string) error {
					if op == "habitat.before" {
						sh, e := f.store.GetShip(f.carrier)
						if e != nil {
							t.Error(e)
						}
						sh.Resource++
						if e := f.store.PutShip(sh); e != nil {
							t.Error(e)
						}
						disk = enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
					}
					return nil
				})
			}
			out := f.activate(t)
			if kind == "witness pointer replaced" {
				// The commit was confirmed before the identity changed: records
				// stand, nothing is installed, the engine halts (COMMITTED_ERROR).
				x := f.snapshot(t)
				if f.state.Mode() != ModeHalted || strings.Contains(out, "Factory activated") || !x.StoredFactory.On || x.StoredCarrier.Resource != 72 || x.ActorFactory.On || x.ActorCarrier.Resource != 100 || !strings.Contains(f.log.text(), "COMMITTED") {
					t.Fatal("replaced witness installed a partial proposal")
				}
				return
			}
			if strings.Contains(out, "Factory activated") || f.state.Mode() != ModeHalted || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("stale/inconsistent world overwritten or installed", out)
			}
		})
	}
}

func TestCR17CHabitatReadbackAndInstallationFailures(t *testing.T) {
	for _, kind := range []string{"readback unavailable", "install panic", "post-install panic"} {
		t.Run(kind, func(t *testing.T) {
			f := newHabitatFixture(t)
			before := f.snapshot(t)
			want := habitatExpected(t, before)
			switch kind {
			case "readback unavailable":
				f.store.SetMetaFault(func(op string) error {
					if op == "habitat.commit-error" || op == "command.readback" {
						return errors.New("private unavailable")
					}
					return nil
				})
			case "install panic":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					f.state.testHooks.beforeCommandInstall = func() { panic("private install panic") }
				})
			case "post-install panic":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					f.state.testHooks.afterCommandInstall = func() { panic("private release panic") }
				})
			}
			out := f.activate(t)
			x := f.snapshot(t)
			if f.state.Mode() != ModeHalted || strings.Contains(out, "Factory activated") || !reflect.DeepEqual(x.StoredFactory, want.StoredFactory) || !reflect.DeepEqual(x.StoredCarrier, want.StoredCarrier) || strings.Contains(f.log.text(), "private") {
				t.Fatal("committed records lost, false success or secret leaked")
			}
			switch kind {
			case "post-install panic":
				if !reflect.DeepEqual(x.ActorFactory, want.ActorFactory) || !reflect.DeepEqual(x.ActorCarrier, want.ActorCarrier) || !strings.Contains(f.log.text(), "COMMITTED") {
					t.Fatal("completed installation not kept after post-install failure")
				}
			default:
				if !reflect.DeepEqual(x.ActorFactory, before.ActorFactory) || !reflect.DeepEqual(x.ActorCarrier, before.ActorCarrier) {
					t.Fatal("unverified/partial actor installation")
				}
				class := "COMMITTED"
				if kind == "readback unavailable" {
					class = "UNCERTAIN"
				}
				if !strings.Contains(f.log.text(), class) {
					t.Fatal("outcome classification lost")
				}
			}
		})
	}
}

func TestCR17CHabitatResponseFailureAfterCommit(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(fmt.Sprint(panics), func(t *testing.T) {
			f := newHabitatFixture(t)
			before := f.snapshot(t)
			want := habitatExpected(t, before)
			r := &failingFactoryResponder{panics: panics}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "order #77 on", r); e != nil {
				t.Fatal(e)
			}
			x := f.snapshot(t)
			x.Output = ""
			if f.state.Mode() != ModeHalted || !reflect.DeepEqual(x, want) || !strings.Contains(f.log.text(), "COMMITTED") || strings.Contains(f.log.text(), "private") {
				t.Fatal("response failure misreported as rollback")
			}
		})
	}
}

func TestCR17CHabitatEnrollmentAndModeCompatibility(t *testing.T) {
	t.Run("enrollment", func(t *testing.T) {
		f := newHabitatFixture(t)
		if out := f.activate(t); !strings.Contains(out, "Factory activated") {
			t.Fatal(out)
		}
		if _, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest("account-2")); e != nil {
			t.Fatal("activation left stale enrollment preimage", e)
		}
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	})
	for _, mode := range []Mode{ModeQuiesced, ModeHalted} {
		t.Run(mode.String(), func(t *testing.T) {
			f := newHabitatFixture(t)
			if mode == ModeQuiesced {
				if e := f.state.Quiesce(ctxT(t, 10*time.Second)); e != nil {
					t.Fatal(e)
				}
			} else {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.halt("isolated mode fixture", "") })
			}
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			out := f.activate(t)
			if strings.Contains(out, "Factory activated") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("nonserving command executed")
			}
		})
	}
	t.Run("landed path still staged", func(t *testing.T) {
		f := newFactoryFixture(t)
		if out := f.activate(t); !strings.Contains(out, "Factory activated at a cost of 14 resources") {
			t.Fatal("landed interception regressed", out)
		}
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	})
}

func TestCR17CHabitatOutputAndPrivateStateWaitForCommit(t *testing.T) {
	f := newHabitatFixture(t)
	before := f.snapshot(t)
	r := &recordingResponder{}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	f.store.SetMetaFault(func(op string) error {
		if op == "habitat.write:1" {
			if r.text() != "" || !reflect.DeepEqual(f.state.ships[f.factory], before.ActorFactory) || !reflect.DeepEqual(f.state.ships[f.carrier], before.ActorCarrier) {
				t.Error("unconfirmed proposal/output escaped")
			}
			close(entered)
			<-release
		}
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "order #77 on", r) }()
	waitAcceptance(t, entered, "open habitat transaction")
	fac, e := f.store.GetShip(f.factory)
	if e != nil {
		t.Fatal(e)
	}
	car, e := f.store.GetShip(f.carrier)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(fac, before.StoredFactory) || !reflect.DeepEqual(car, before.StoredCarrier) || r.text() != "" {
		t.Error("readers observed an uncommitted prefix")
	}
	unblock()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	if !strings.Contains(r.text(), "Factory activated") {
		t.Fatal("confirmed output absent")
	}
}

func TestCR17CHabitatAdmittedCommandAndFollowingActions(t *testing.T) {
	for _, action := range []string{"second activation", "quiesce", "shutdown", "started cancellation"} {
		t.Run(action, func(t *testing.T) {
			f := newHabitatFixture(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			f.store.SetMetaFault(func(op string) error {
				if op == "habitat.write:1" {
					close(entered)
					<-release
				}
				return nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := &recordingResponder{}
			done := make(chan error, 1)
			go func() { done <- f.state.HandleCommand(ctx, 1, 0, "order #77 on", r) }()
			waitAcceptance(t, entered, "started habitat write")
			following := make(chan error, 1)
			r2 := &recordingResponder{}
			switch action {
			case "second activation":
				go func() { following <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "order #77 on", r2) }()
			case "quiesce":
				go func() { following <- f.state.Quiesce(ctxT(t, 10*time.Second)) }()
			case "shutdown":
				go func() { following <- f.state.Stop(ctxT(t, 10*time.Second)) }()
			case "started cancellation":
				cancel()
			}
			if action == "shutdown" {
				deadline := time.Now().Add(5 * time.Second)
				for !f.state.draining.Load() {
					if time.Now().After(deadline) {
						t.Fatal("shutdown did not begin draining")
					}
					time.Sleep(time.Millisecond)
				}
			}
			select {
			case e := <-done:
				t.Fatal("started request returned guessed outcome", e)
			default:
			}
			select {
			case e := <-following:
				t.Fatal("following operation overlapped transaction", e)
			default:
			}
			unblock()
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			if action != "started cancellation" {
				if e := <-following; e != nil {
					t.Fatal(e)
				}
			}
			fac, e := f.store.GetShip(f.factory)
			if e != nil {
				t.Fatal(e)
			}
			car, e := f.store.GetShip(f.carrier)
			if e != nil {
				t.Fatal(e)
			}
			if !fac.On || fac.Size != 1 || car.Resource != 72 || car.Hanger != 1 || !strings.Contains(r.text(), "Factory activated") {
				t.Fatal("admitted operation not committed completely")
			}
			if action == "second activation" {
				if strings.Contains(r2.text(), "Factory activated") || !strings.Contains(r2.text(), "already activated") {
					t.Fatal("second serialized activation charged again")
				}
			}
			if action != "shutdown" {
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			}
			if action == "quiesce" {
				f.restart(t)
				if f.state.Mode() != ModeQuiesced {
					t.Fatal("maintenance lost on restart")
				}
			}
		})
	}
}

func TestCR17CHabitatQueuedBehindTurnAndCanceled(t *testing.T) {
	f := newHabitatFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.testHooks.beforeTurn = func(turnKind) { close(entered); <-release } })
	turn := make(chan error, 1)
	go func() { turn <- f.state.TriggerSegment(ctxT(t, 10*time.Second)) }()
	waitAcceptance(t, entered, "blocked turn")
	before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	canceled := make(chan error, 1)
	r := &recordingResponder{}
	go func() { canceled <- f.state.HandleCommand(ctx, 1, 0, "order #77 on", r) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(f.state.cmdCh) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("command did not queue")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if e := <-canceled; !errors.Is(e, ErrNotExecuted) {
		t.Fatal("canceled queue entry executed", e)
	}
	if r.text() != "" || enrollmentDBSnapshot(t, f.actorEnrollmentFixture) != before {
		t.Fatal("canceled entry changed records")
	}
	done := make(chan error, 1)
	go func() { done <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "order #77 on", r) }()
	unblock()
	if e := <-turn; e != nil {
		t.Fatal(e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(r.text(), "Factory activated") {
		t.Fatal("following operation did not execute after turn")
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}

func TestCR17CHabitatInternalCompletionClasses(t *testing.T) {
	for _, tc := range []struct {
		name, stage string
		want        commandCompletion
	}{{"validation", "", commandValidationFailed}, {"abort", "habitat.write:1", commandNotCommitted}, {"commit", "", commandCommitted}, {"report error", "habitat.after-commit", commandCommittedError}, {"uncertain", "habitat.commit-error", commandUncertain}, {"rejected", "", commandRejected}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHabitatFixture(t)
			if tc.name == "validation" {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					car := f.state.ships[f.carrier]
					car.Resource = 0
					if e := f.store.PutShip(car); e != nil {
						t.Error(e)
					}
				})
			}
			if tc.name == "rejected" {
				if e := f.state.Quiesce(ctxT(t, 10*time.Second)); e != nil {
					t.Fatal(e)
				}
			}
			if tc.stage != "" {
				f.store.SetMetaFault(func(op string) error {
					if op == tc.stage {
						return errors.New("internal completion fault")
					}
					return nil
				})
			}
			got, e := call(f.state, ctxT(t, 10*time.Second), opTest, func() (commandCompletion, error) {
				return f.state.runHabitatActivation(1, 0, f.factory, &recordingResponder{}), nil
			})
			if e != nil || got != tc.want {
				t.Fatal("wrong internal completion", got, e)
			}
		})
	}
}

func TestCR17CHabitatRequestedShipIdentity(t *testing.T) {
	for _, selector := range []string{"#77", "77"} {
		t.Run(selector, func(t *testing.T) {
			f := newHabitatFixture(t)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				fac := f.state.ships[f.factory]
				fac.Number = 78
				f.state.ships[78] = fac
				// The requested key is inconsistent, while the actual factory's
				// identity, persisted record and carrier membership are valid.
				// Reject because of the requested key, not a malformed chain.
				car := f.state.ships[f.carrier]
				car.Ships = fac.Number
				for _, sh := range []*model.Ship{fac, car} {
					if e := f.store.PutShip(sh); e != nil {
						t.Error(e)
					}
				}
			})
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			disk := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			out := f.command(t, "order "+selector+" on")
			if f.state.Mode() != ModeHalted || strings.Contains(out, "Factory activated") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatalf("requested ship identity mismatch escaped staging: %q", out)
			}
			if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("identity failure permitted a stale shutdown flush", e)
			}
		})
	}
}

func TestCR17CHabitatLegacyBypassAndSelectors(t *testing.T) {
	t.Run("old habitat helper cannot write", func(t *testing.T) {
		f := newHabitatFixture(t)
		disk := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
		actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			r := &recordingResponder{}
			if e := f.state.activateFactory(1, 0, f.state.ships[f.factory], r); e == nil || strings.Contains(r.text(), "Factory activated") {
				t.Error("legacy habitat writer still enabled")
			}
			// giveOrders is the legacy caller: it must refuse without setting On.
			r2 := &recordingResponder{}
			f.state.giveOrders(1, 0, []string{"on"}, f.state.ships[f.factory], r2)
			if f.state.ships[f.factory].On || strings.Contains(r2.text(), "Factory activated") {
				t.Error("giveOrders bypassed staging")
			}
		})
		if disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
			t.Fatal("old helper bypassed staging")
		}
	})
	t.Run("nonfactory prefix cannot execute", func(t *testing.T) {
		f := newHabitatFixture(t)
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			car := f.state.ships[f.carrier]
			mine := &model.Ship{}
			f.state.getshipDefaults(mine, model.StypeMine, f.state.races[0])
			mine.Number = 78
			mine.Owner = 1
			mine.Race = 1
			mine.Alive = true
			mine.Active = true
			mine.On = false
			mine.Docked = true
			mine.WhatOrbits = model.LevelShip
			mine.WhatDest = model.LevelShip
			mine.DestShipNo = f.carrier
			mine.StOrbits = f.star
			mine.PNumOrbits = f.planet
			mine.NextShip = car.Ships
			car.Ships = mine.Number
			car.Hanger += mine.Size
			f.state.ships[mine.Number] = mine
			f.state.dirs[0][0] = model.Directory{Level: model.LevelShip, StarNum: f.star, PlanetNum: f.planet, ShipNo: f.carrier}
			for _, s := range []*model.Ship{mine, car} {
				if e := f.store.PutShip(s); e != nil {
					t.Error(e)
				}
			}
		})
		actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
		out := f.command(t, "order * on")
		if !strings.Contains(out, "one explicit") || strings.Contains(out, "Factory activated") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
			t.Fatal("mixed selector ran an earlier ship before refusing", out)
		}
	})
	t.Run("authorized governor", func(t *testing.T) {
		f := newHabitatFixtureWithCarrierOwner(t, 2)
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			sh := f.state.ships[f.factory]
			sh.Governor = 1
			f.state.races[0].Governor[1].Active = true
			f.state.dirs[0][1] = f.state.dirs[0][0]
			if e := f.store.PutShip(sh); e != nil {
				t.Error(e)
			}
			if e := f.store.PutRace(f.state.races[0]); e != nil {
				t.Error(e)
			}
		})
		r := &recordingResponder{}
		if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 1, "order #77 on", r); e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(r.text(), "Factory activated") {
			t.Fatal("authorized governor rejected", r.text())
		}
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	})
	t.Run("bounded rendering", func(t *testing.T) {
		// A docked ship's order row prints its name truncated (%14.14s) and
		// its location as "#<carrier>", so the habitat response cannot reach the
		// shared staging limit; the bound itself is exercised by the landed and
		// workspace tests. Assert the staged response stays small and complete.
		f := newHabitatFixture(t)
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			f.state.ships[f.factory].Name = strings.Repeat("x", maxCommandEffectBytes+1)
			f.state.ships[f.carrier].Name = strings.Repeat("y", maxCommandEffectBytes+1)
			for _, n := range []uint32{f.factory, f.carrier} {
				if e := f.store.PutShip(f.state.ships[n]); e != nil {
					t.Error(e)
				}
			}
		})
		out := f.activate(t)
		if !strings.Contains(out, "Factory activated") || len(out) > 512 || strings.Count(out, "\n") != 3 {
			t.Fatal("habitat response not bounded to header, activation and one order row", len(out))
		}
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	})
	t.Run("pending AP admission retained", func(t *testing.T) {
		f := newHabitatFixture(t)
		if out := f.command(t, "transfer 1 r 0"); !strings.Contains(out, "transferred") {
			t.Fatal("existing transfer not exercised")
		}
		before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
		_, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest("account-2"))
		assertActorEnrollmentFailure(t, e, "ENROLLMENT_PENDING_TURN", db.EnrollmentNotCommitted)
		if before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || f.state.Mode() != ModeServing {
			t.Fatal("pending AP changed store or halted")
		}
		if e := f.state.TriggerUpdate(ctxT(t, 10*time.Second)); e != nil {
			t.Fatal(e)
		}
		if out := f.activate(t); !strings.Contains(out, "Factory activated") {
			t.Fatal("reconciled activation failed", out)
		}
		if _, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest("account-2")); e != nil {
			t.Fatal("enrollment request not safely retryable", e)
		}
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	})
}
