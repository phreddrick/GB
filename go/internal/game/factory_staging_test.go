package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/phreddrick/GB/go/internal/db"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/model"
)

type factoryFixture struct {
	*actorEnrollmentFixture
	star, planet uint8
	factory      uint32
}

func newFactoryFixture(t *testing.T) *factoryFixture {
	t.Helper()
	f, a := newCommandEnrollmentActor(t)
	x := &factoryFixture{f, a.Star, a.Planet, 77}
	onEnrollmentActor(t, f, func() {
		pl := f.state.planets[a.Star][a.Planet]
		pl.Info[0].Resource = 100
		for i := range pl.Sectors {
			pl.Sectors[i].Resource = 0
			pl.Sectors[i].Crystals = 0
		}
		sh := &model.Ship{}
		f.state.getshipDefaults(sh, model.OtypeFactory, f.state.races[0])
		sh.Number = 77
		sh.Owner = 1
		sh.Race = 1
		sh.Governor = 0
		sh.BuildCost = 7
		sh.BuildType = uint16(model.StypeCargo)
		sh.Alive = true
		sh.Active = true
		sh.On = false
		sh.Notified = true
		sh.Docked = true
		sh.Popn = 1
		sh.WhatOrbits = model.LevelPlan
		sh.WhatDest = model.LevelPlan
		sh.StOrbits = a.Star
		sh.DestStar = a.Star
		sh.PNumOrbits = a.Planet
		sh.DestPNum = a.Planet
		sh.LandX = a.CapitalX
		sh.LandY = a.CapitalY
		sh.XPos = f.state.stars[a.Star].XPos + pl.XPos
		sh.YPos = f.state.stars[a.Star].YPos + pl.YPos
		sh.NextShip = pl.Ships
		pl.Ships = sh.Number
		f.state.ships[sh.Number] = sh
		if e := f.store.PutPlanet(pl); e != nil {
			t.Error(e)
		}
		if e := f.store.PutShip(sh); e != nil {
			t.Error(e)
		}
	})
	return x
}
func factoryCopyShip(t *testing.T, p *model.Ship) *model.Ship {
	t.Helper()
	b, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	var q model.Ship
	if e := json.Unmarshal(b, &q); e != nil {
		t.Fatal(e)
	}
	return &q
}

type factoryWorld struct {
	ActorPlanet, StoredPlanet *model.Planet
	ActorShip, StoredShip     *model.Ship
	Star                      *model.Star
	Output                    string `json:"output,omitempty"`
}

func (f *factoryFixture) captureOnActor(t *testing.T) factoryWorld {
	t.Helper()
	pl, e := f.store.GetPlanet(f.star, f.planet)
	if e != nil {
		t.Error(e)
	}
	sh, e := f.store.GetShip(f.factory)
	if e != nil {
		t.Error(e)
	}
	star := *f.state.stars[f.star]
	return factoryWorld{ActorPlanet: copyCommandPlanet(f.state.planets[f.star][f.planet]), StoredPlanet: pl, ActorShip: factoryCopyShip(t, f.state.ships[f.factory]), StoredShip: sh, Star: &star}
}
func (f *factoryFixture) snapshot(t *testing.T) factoryWorld {
	t.Helper()
	var out factoryWorld
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { out = f.captureOnActor(t) })
	return out
}
func (f *factoryFixture) command(t *testing.T, text string) string {
	t.Helper()
	r := &recordingResponder{}
	if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, text, r); e != nil {
		t.Fatal(e)
	}
	return r.text()
}
func (f *factoryFixture) activate(t *testing.T) string {
	t.Helper()
	return f.command(t, fmt.Sprintf("order #%d on", f.factory))
}

func TestCR17CFactoryCompleteStateAndBoundaries(t *testing.T) {
	f := newFactoryFixture(t)
	before := f.snapshot(t)
	wantPlanet := copyCommandPlanet(before.ActorPlanet)
	wantPlanet.Info[0].Resource = 86
	wantShip := factoryCopyShip(t, before.ActorShip)
	wantShip.On = true
	wantShip.Notified = false
	aux := factoryAux(t, f)
	var originalShip *model.Ship
	var originalPlanet *model.Planet
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { originalShip = f.state.ships[f.factory]; originalPlanet = f.state.planets[f.star][f.planet] })
	out := f.activate(t)
	if !strings.Contains(out, "Factory activated at a cost of 14 resources") || !strings.Contains(out, "/on") {
		t.Fatal("activation output missing")
	}
	after := f.snapshot(t)
	if !reflect.DeepEqual(after.ActorPlanet, wantPlanet) || !reflect.DeepEqual(after.StoredPlanet, wantPlanet) || !reflect.DeepEqual(after.ActorShip, wantShip) || !reflect.DeepEqual(after.StoredShip, wantShip) {
		t.Fatal("complete factory/planet proposal not installed/persisted")
	}
	// Auxiliary comparison intentionally excludes the changed factory ship, but
	// includes all other ships, free lists, credentials, placement and turn metadata.
	if factoryAux(t, f) != aux {
		t.Fatal("unrelated records changed")
	}
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		if f.state.ships[f.factory] != originalShip || f.state.planets[f.star][f.planet] != originalPlanet {
			t.Error("actor object address replaced")
		}
	})
	for _, stage := range []string{"segment", "update", "restart"} {
		var e error
		switch stage {
		case "segment":
			e = f.state.TriggerSegment(ctxT(t, 10*time.Second))
		case "update":
			e = f.state.TriggerUpdate(ctxT(t, 10*time.Second))
		case "restart":
			f.restart(t)
		}
		if e != nil {
			t.Fatal(e)
		}
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		x := f.snapshot(t)
		if !x.StoredShip.On || x.StoredPlanet.Info[0].Resource != 86 || x.StoredShip.Number != f.factory || x.StoredShip.Owner != 1 || !x.StoredShip.Alive {
			t.Fatal(stage, "activation lost or cost refunded")
		}
		assertFactoryOrbit(t, f)
	}
}

func factoryAux(t *testing.T, f *factoryFixture) string {
	t.Helper()
	ships, e := f.store.AllShips()
	if e != nil {
		t.Fatal(e)
	}
	delete(ships, f.factory)
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
	all = append(all, races)
	free, e := f.store.FreeShipNums()
	if e != nil {
		t.Fatal(e)
	}
	all = append(all, free)
	cf, e := f.store.FreeCommodNums()
	if e != nil {
		t.Fatal(e)
	}
	all = append(all, cf)
	sd, e := f.store.GetStarData()
	if e != nil {
		t.Fatal(e)
	}
	all = append(all, sd)
	stars, e := f.store.AllStars()
	if e != nil {
		t.Fatal(e)
	}
	all = append(all, stars)
	b, e := json.Marshal(all)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func assertFactoryOrbit(t *testing.T, f *factoryFixture) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		pl := f.state.planets[f.star][f.planet]
		seen := map[uint32]bool{}
		for n := pl.Ships; n != 0; {
			sh := f.state.ships[n]
			if sh == nil || seen[n] || !sh.Alive || sh.WhatOrbits != model.LevelPlan || sh.StOrbits != f.star || sh.PNumOrbits != f.planet {
				t.Error("invalid orbital identity")
				return
			}
			seen[n] = true
			n = sh.NextShip
		}
		if !seen[f.factory] || !seen[f.state.races[0].GovShip] || !f.state.governed(f.state.races[0]) {
			t.Error("factory/government identity lost")
		}
	})
}

func TestCR17CFactoryWriteFailures(t *testing.T) {
	for _, stage := range []string{"factory.before", "factory.validated", "factory.write:1", "factory.write:2", "factory.before-commit", "factory.commit-error", "factory.after-commit", "fence"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				f := newFactoryFixture(t)
				before := f.snapshot(t)
				actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				aux := factoryAux(t, f)
				reached := false
				r := &recordingResponder{}
				f.store.SetMetaFault(func(op string) error {
					if stage == "fence" && op == "factory.before" {
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
				committed := stage == "factory.after-commit"
				visible := committed || stage == "factory.commit-error"
				if visible {
					wantP := copyCommandPlanet(before.StoredPlanet)
					wantP.Info[0].Resource = 86
					wantS := factoryCopyShip(t, before.StoredShip)
					wantS.On = true
					wantS.Notified = false
					if !reflect.DeepEqual(x.StoredPlanet, wantP) || !reflect.DeepEqual(x.StoredShip, wantS) {
						t.Fatal("visible transaction incomplete")
					}
				} else if disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("aborted transaction left records/allocator/placement changes")
				}
				if committed {
					if !reflect.DeepEqual(x.ActorPlanet, x.StoredPlanet) || !reflect.DeepEqual(x.ActorShip, x.StoredShip) {
						t.Fatal("confirmed reporting error did not install complete state")
					}
				} else if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("unconfirmed change installed actor/AP state")
				}
				if aux != factoryAux(t, f) {
					t.Fatal("auxiliary records changed")
				}
				if stage == "fence" && (f.store.FencedWrites() != 1 || f.store.LastFencedWrite() != "CommitFactoryActivation") {
					t.Fatal("write fence accounting bypassed")
				}
				after := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				if e := f.state.TriggerUpdate(ctxT(t, time.Second)); !errors.Is(e, ErrNotServing) {
					t.Fatal("halted update admitted", e)
				}
				if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || after != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("halted shutdown flushed stale proposal", e)
				}
				if stage == "factory.commit-error" {
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

func TestCR17CFactoryValidationAndSelectors(t *testing.T) {
	for _, command := range []string{"order #77 on extra", "order * on", "order F on", "order #4294967373 on", "order 4294967373 on", "order #18446744073709551616 on", "order #77junk on", "order # on", "order #0 on"} {
		t.Run(command, func(t *testing.T) {
			f := newFactoryFixture(t)
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			out := f.command(t, command)
			if strings.Contains(out, "Factory activated") || f.state.Mode() != ModeServing || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("invalid/ambiguous selector changed records")
			}
		})
	}
	for _, kind := range []string{"insufficient resources", "already on", "inactive", "no crew", "unauthorized governor"} {
		t.Run(kind, func(t *testing.T) {
			f := newFactoryFixture(t)
			g := 0
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				sh := f.state.ships[f.factory]
				pl := f.state.planets[f.star][f.planet]
				switch kind {
				case "insufficient resources":
					pl.Info[0].Resource = 13
				case "already on":
					sh.On = true
				case "inactive":
					sh.Active = false
				case "no crew":
					sh.Popn = 0
				case "unauthorized governor":
					g = 1
					sh.Governor = 2
					f.state.dirs[0][1] = f.state.dirs[0][0]
				}
				if e := f.store.PutShip(sh); e != nil {
					t.Error(e)
				}
				if e := f.store.PutPlanet(pl); e != nil {
					t.Error(e)
				}
			})
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			r := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, "order #77 on", r); e != nil {
				t.Fatal(e)
			}
			if strings.Contains(r.text(), "Factory activated") || f.state.Mode() != ModeServing || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("validation failure escaped workspace")
			}
		})
	}
}

func TestCR17CFactoryOriginalCCostAndRules(t *testing.T) {
	for _, tc := range []struct {
		cost      uint16
		remaining uint16
		success   bool
	}{{0, 65535, true}, {7, 65521, true}, {32767, 1, true}, {32768, 65535, false}, {65535, 65535, false}} {
		t.Run(fmt.Sprint(tc.cost), func(t *testing.T) {
			f := newFactoryFixture(t)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				sh := f.state.ships[f.factory]
				sh.BuildCost = tc.cost
				sh.Damage = 31
				pl := f.state.planets[f.star][f.planet]
				pl.Info[0].Resource = 65535
				f.state.stars[f.star].AP[0] = 0
				if e := f.store.PutShip(sh); e != nil {
					t.Error(e)
				}
				if e := f.store.PutPlanet(pl); e != nil {
					t.Error(e)
				}
				if e := f.store.PutStar(f.state.stars[f.star]); e != nil {
					t.Error(e)
				}
			})
			before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			out := f.activate(t)
			x := f.snapshot(t)
			if tc.success {
				if !strings.Contains(out, "Factory activated") || !x.StoredShip.On || x.StoredPlanet.Info[0].Resource != tc.remaining || x.Star.AP[0] != 0 {
					t.Fatal("C cost/damaged factory/zero AP rule diverged")
				}
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			} else if strings.Contains(out, "Factory activated") || before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || f.state.Mode() != ModeServing {
				t.Fatal("wide C cost wrapped to affordable activation")
			}
		})
	}
}

func TestCR17CFactoryStalePreimagesAndIntegrity(t *testing.T) {
	for _, kind := range []string{"legacy fuel load", "stored planet", "stored factory", "stored star AP", "stored witness", "after proposal", "actor chain cycle", "actor missing planet", "actor landing mismatch", "factory absent from chain"} {
		t.Run(kind, func(t *testing.T) {
			f := newFactoryFixture(t)
			switch kind {
			case "legacy fuel load":
				prepareLegacyFuelLoad(t, f.actorEnrollmentFixture, f.star, f.planet, f.state.races[0].GovShip, 100)
				out := f.command(t, fmt.Sprintf("load #%d f 1", f.state.races[0].GovShip))
				if !strings.Contains(out, "transferred") {
					t.Fatal("legacy fuel load not exercised")
				}
			case "stored planet":
				p, e := f.store.GetPlanet(f.star, f.planet)
				if e != nil {
					t.Fatal(e)
				}
				p.Sectors[0].Resource++
				if e := f.store.PutPlanet(p); e != nil {
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
			case "stored star AP":
				st, e := f.store.GetStar(f.star)
				if e != nil {
					t.Fatal(e)
				}
				st.AP[0]--
				if e := f.store.PutStar(st); e != nil {
					t.Fatal(e)
				}
			case "stored witness":
				sh, e := f.store.GetShip(f.state.races[0].GovShip)
				if e != nil {
					t.Fatal(e)
				}
				sh.Resource++
				if e := f.store.PutShip(sh); e != nil {
					t.Fatal(e)
				}
			case "actor chain cycle":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.ships[f.factory].NextShip = f.factory })
			case "actor missing planet":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { delete(f.state.planets[f.star], f.planet) })
			case "actor landing mismatch":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.ships[f.factory].DestPNum = 1 })
			case "factory absent from chain":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.planets[f.star][f.planet].Ships = f.state.ships[f.factory].NextShip })
			}
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			if kind == "after proposal" {
				f.store.SetMetaFault(func(op string) error {
					if op == "factory.before" {
						p, e := f.store.GetPlanet(f.star, f.planet)
						if e != nil {
							t.Error(e)
						}
						p.Info[0].Resource++
						if e := f.store.PutPlanet(p); e != nil {
							t.Error(e)
						}
						disk = enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
					}
					return nil
				})
			}
			out := f.activate(t)
			if strings.Contains(out, "Factory activated") || f.state.Mode() != ModeHalted || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("stale/inconsistent world overwritten or installed")
			}
		})
	}
}

func TestCR17CWorkspacePrivateCopiesAndBounds(t *testing.T) {
	f := newFactoryFixture(t)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		sh := f.state.ships[f.factory]
		sh.Special = model.ShipSpecial{AimedAt: &model.AimedAtSpecial{Intensity: 1}, Mind: &model.MindSpecial{Busy: true}, Pod: &model.PodSpecial{Decay: 1}, Timer: &model.TimerSpecial{Count: 1}, Impact: &model.ImpactSpecial{X: 1}, Trigger: &model.TriggerSpecial{Radius: 1}, Terraform: &model.TerraformSpecial{Index: 1}, Transport: &model.TransportSpecial{Target: 1}, Waste: &model.WasteSpecial{Toxic: 1}}
		if e := f.store.PutShip(sh); e != nil {
			t.Error(e)
		}
	})
	actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	disk := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		w := newCommandWorkspace(f.state)
		sh, e := w.ship(f.factory)
		if e != nil {
			t.Error(e)
			return
		}
		sh.Special.AimedAt.Intensity++
		sh.Special.Mind.Busy = false
		sh.Special.Pod.Decay++
		sh.Special.Timer.Count++
		sh.Special.Impact.X++
		sh.Special.Trigger.Radius++
		sh.Special.Terraform.Index++
		sh.Special.Transport.Target++
		sh.Special.Waste.Toxic++
		pl, e := w.planet(f.star, f.planet)
		if e != nil {
			t.Error(e)
			return
		}
		pl.Sectors[0].Popn++
		st, e := w.star(f.star)
		if e != nil {
			t.Error(e)
			return
		}
		st.AP[0]--
		if !w.owns() || w.factoryReadOnly(f.factory) {
			t.Error("proposal aliases preimages or read-only mutation accepted")
		}
		if !reflect.DeepEqual(w.ships[f.factory].before, f.state.ships[f.factory]) {
			t.Error("ship preimage borrowed mutable pointers")
		}
	})
	if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
		t.Fatal("workspace mutation escaped")
	}
	var effects commandEffects
	if e := effects.Send(strings.Repeat("x", maxCommandEffectBytes)); e != nil {
		t.Fatal(e)
	}
	if e := effects.Send("x"); e == nil || !effects.failed {
		t.Fatal("unbounded command output")
	}
	if e := effects.SendJSON(struct{}{}); e == nil {
		t.Fatal("unsupported effect family accepted")
	}
	if _, e := workspaceCopyShip(nil); e == nil {
		t.Fatal("nil ship copy accepted")
	}
}

func TestCR17CFactoryReadbackAndInstallationFailures(t *testing.T) {
	for _, kind := range []string{"readback unavailable", "install panic", "witness identity replaced"} {
		t.Run(kind, func(t *testing.T) {
			f := newFactoryFixture(t)
			before := f.snapshot(t)
			if kind == "readback unavailable" {
				f.store.SetMetaFault(func(op string) error {
					if op == "factory.commit-error" || op == "command.readback" {
						return errors.New("private unavailable")
					}
					return nil
				})
			} else {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					f.state.testHooks.beforeCommandInstall = func() {
						if kind == "install panic" {
							panic("private install panic")
						}
						n := f.state.races[0].GovShip
						sh := *f.state.ships[n]
						f.state.ships[n] = &sh
					}
				})
			}
			out := f.activate(t)
			x := f.snapshot(t)
			if f.state.Mode() != ModeHalted || strings.Contains(out, "Factory activated") || !x.StoredShip.On || x.StoredPlanet.Info[0].Resource != 86 || !reflect.DeepEqual(x.ActorShip, before.ActorShip) || !reflect.DeepEqual(x.ActorPlanet, before.ActorPlanet) {
				t.Fatal("unverified/partial actor installation")
			}
			class := "COMMITTED"
			if kind == "readback unavailable" {
				class = "UNCERTAIN"
			}
			if !strings.Contains(f.log.text(), class) {
				t.Fatal("outcome classification lost")
			}
		})
	}
}

type failingFactoryResponder struct {
	recordingResponder
	panics bool
}

func (r *failingFactoryResponder) Send(string) error {
	if r.panics {
		panic("private transport panic")
	}
	return errors.New("private transport failure")
}
func TestCR17CFactoryResponseFailureAfterCommit(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(fmt.Sprint(panics), func(t *testing.T) {
			f := newFactoryFixture(t)
			r := &failingFactoryResponder{panics: panics}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "order #77 on", r); e != nil {
				t.Fatal(e)
			}
			x := f.snapshot(t)
			if f.state.Mode() != ModeHalted || !x.StoredShip.On || x.StoredPlanet.Info[0].Resource != 86 || !reflect.DeepEqual(x.ActorShip, x.StoredShip) || !reflect.DeepEqual(x.ActorPlanet, x.StoredPlanet) || !strings.Contains(f.log.text(), "COMMITTED") || strings.Contains(f.log.text(), "private") {
				t.Fatal("response failure misreported as rollback")
			}
		})
	}
}

func TestCR17CFactoryEnrollmentAndModeCompatibility(t *testing.T) {
	t.Run("enrollment", func(t *testing.T) {
		f := newFactoryFixture(t)
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
			f := newFactoryFixture(t)
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
}

func TestCR17CFactoryOutputAndPrivateStateWaitForCommit(t *testing.T) {
	f := newFactoryFixture(t)
	before := f.snapshot(t)
	r := &recordingResponder{}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	f.store.SetMetaFault(func(op string) error {
		if op == "factory.write:1" {
			if r.text() != "" || !reflect.DeepEqual(f.state.ships[f.factory], before.ActorShip) || !reflect.DeepEqual(f.state.planets[f.star][f.planet], before.ActorPlanet) {
				t.Error("unconfirmed proposal/output escaped")
			}
			close(entered)
			<-release
		}
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "order #77 on", r) }()
	waitAcceptance(t, entered, "open factory transaction")
	sh, e := f.store.GetShip(f.factory)
	if e != nil {
		t.Fatal(e)
	}
	pl, e := f.store.GetPlanet(f.star, f.planet)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(sh, before.StoredShip) || !reflect.DeepEqual(pl, before.StoredPlanet) || r.text() != "" {
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

func TestCR17CFactoryAdmittedCommandAndFollowingActions(t *testing.T) {
	for _, action := range []string{"second activation", "quiesce", "shutdown", "started cancellation"} {
		t.Run(action, func(t *testing.T) {
			f := newFactoryFixture(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			f.store.SetMetaFault(func(op string) error {
				if op == "factory.write:1" {
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
			waitAcceptance(t, entered, "started factory write")
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
			sh, e := f.store.GetShip(f.factory)
			if e != nil {
				t.Fatal(e)
			}
			pl, e := f.store.GetPlanet(f.star, f.planet)
			if e != nil {
				t.Fatal(e)
			}
			if !sh.On || pl.Info[0].Resource != 86 || !strings.Contains(r.text(), "Factory activated") {
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

func TestCR17CFactoryQueuedBehindTurnAndCanceled(t *testing.T) {
	f := newFactoryFixture(t)
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

func TestCR17CFactoryInternalCompletionClasses(t *testing.T) {
	for _, tc := range []struct {
		name, stage string
		want        commandCompletion
	}{{"validation", "", commandValidationFailed}, {"abort", "factory.write:1", commandNotCommitted}, {"commit", "", commandCommitted}, {"report error", "factory.after-commit", commandCommittedError}, {"uncertain", "factory.commit-error", commandUncertain}, {"rejected", "", commandRejected}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFactoryFixture(t)
			if tc.name == "validation" {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					p := f.state.planets[f.star][f.planet]
					p.Info[0].Resource = 0
					if e := f.store.PutPlanet(p); e != nil {
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
				return f.state.runFactoryActivation(1, 0, f.factory, &recordingResponder{}), nil
			})
			if e != nil || got != tc.want {
				t.Fatal("wrong internal completion", got, e)
			}
		})
	}
}

func TestCR17CFactoryBoundedEffectsAndLegacyBypass(t *testing.T) {
	t.Run("bounded rendering", func(t *testing.T) {
		f := newFactoryFixture(t)
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			st := f.state.stars[f.star]
			st.Name = strings.Repeat("x", maxCommandEffectBytes+1)
			if e := f.store.PutStar(st); e != nil {
				t.Error(e)
			}
		})
		actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
		out := f.activate(t)
		if strings.Contains(out, "Factory activated") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || f.state.Mode() != ModeServing {
			t.Fatal("oversized rendering escaped staged validation")
		}
	})
	t.Run("old landed helper cannot write", func(t *testing.T) {
		f := newFactoryFixture(t)
		disk := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			if e := f.state.activateFactory(1, 0, f.state.ships[f.factory], &recordingResponder{}); e == nil {
				t.Error("legacy landed writer still enabled")
			}
		})
		if disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
			t.Fatal("old helper bypassed staging")
		}
	})
	t.Run("cycle selector terminates", func(t *testing.T) {
		f := newFactoryFixture(t)
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			f.state.planets[f.star][f.planet].Ships = f.state.races[0].GovShip
			sh := f.state.ships[f.state.races[0].GovShip]
			sh.NextShip = sh.Number
		})
		actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
		out := f.command(t, "order * on")
		if f.state.Mode() != ModeHalted || strings.Contains(out, "Factory activated") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
			t.Fatal("cyclic selection ran a partial request")
		}
	})
}

func TestCR17CFactoryAuthorizedGovernorAndMixedPrefix(t *testing.T) {
	t.Run("authorized governor", func(t *testing.T) {
		f := newFactoryFixture(t)
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
			t.Fatal("authorized governor rejected")
		}
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	})
	t.Run("nonfactory prefix cannot execute", func(t *testing.T) {
		f := newFactoryFixture(t)
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			pl := f.state.planets[f.star][f.planet]
			mine := &model.Ship{}
			f.state.getshipDefaults(mine, model.StypeMine, f.state.races[0])
			mine.Number = 78
			mine.Owner = 1
			mine.Race = 1
			mine.Alive = true
			mine.Active = true
			mine.On = false
			mine.Docked = true
			mine.WhatOrbits = model.LevelPlan
			mine.WhatDest = model.LevelPlan
			mine.StOrbits = f.star
			mine.DestStar = f.star
			mine.PNumOrbits = f.planet
			mine.DestPNum = f.planet
			mine.NextShip = pl.Ships
			pl.Ships = mine.Number
			f.state.ships[mine.Number] = mine
			if e := f.store.PutShip(mine); e != nil {
				t.Error(e)
			}
			if e := f.store.PutPlanet(pl); e != nil {
				t.Error(e)
			}
		})
		actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
		out := f.command(t, "order * on")
		if !strings.Contains(out, "one explicit") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
			t.Fatal("mixed selector ran an earlier ship before refusing")
		}
	})
}

func TestCR17CFactoryRetainsPendingAPAdmission(t *testing.T) {
	f := newFactoryFixture(t)
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
}
