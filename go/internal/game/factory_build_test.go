package game

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

func stringMustReadFactoryBuild(t *testing.T, path string) string {
	t.Helper()
	root, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := root.Close(); e != nil {
			t.Error(e)
		}
	}()
	b, e := root.ReadFile(filepath.Base(path))
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func factoryBuildJSON(t *testing.T, v any) string {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func newFactoryBuildFixture(t *testing.T, free bool) *factoryFixture {
	t.Helper()
	f := newFactoryFixture(t)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		r := f.state.races[0]
		r.God = false
		r.Tech = 1000
		r.Governor[0].Toggle.AutoLoad = false
		f.state.stars[f.star].AP[0] = 0
		p := f.state.planets[f.star][f.planet]
		p.Info[0].Resource = 10000
		for _, e := range []error{f.store.PutPlanet(p), f.store.PutRace(r), f.store.PutStar(f.state.stars[f.star])} {
			if e != nil {
				t.Error(e)
			}
		}
		if free {
			if e := f.store.DeleteShip(70); e != nil {
				t.Error(e)
			}
		}
	})
	return f
}

type factoryBuildRegistry struct {
	fakeRegistry
	at func(string)
}

func (r *factoryBuildRegistry) Notify(p, g int, s string) bool {
	r.fakeRegistry.Notify(p, g, s)
	if r.at != nil {
		r.at(s)
	}
	return true
}
func factoryBuildRecords(t *testing.T, f *factoryFixture) map[string]string {
	t.Helper()
	select {
	case <-f.state.stopped:
		return commandFixtureRecords(t, commandFixtureImageStopped(t, &planetCommandFixture{actorEnrollmentFixture: f.actorEnrollmentFixture}))
	default:
		return commandFixtureRecords(t, commandFixtureImage(t, f.actorEnrollmentFixture))
	}
}
func factoryBuildInput(t *testing.T, f *factoryFixture) db.PlanetFactoryBuild {
	t.Helper()
	var in db.PlanetFactoryBuild
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		_, x, pending, e := f.state.factoryBuildStage(1, 0, []string{"F", "0,0"})
		if e != nil || pending {
			t.Fatal(e, pending)
		}
		in = x
	})
	return in
}
func factoryBuildExpected(t *testing.T, base map[string]string, in db.PlanetFactoryBuild) map[string]string {
	t.Helper()
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	// Reuse the fixture's canonical logical bucket/key representation.
	for k, v := range map[string]any{fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(in.Input.Number)): in.Proposed.Ship, fmt.Sprintf("%x/%x", db.BucketPlanets, db.PlanetKey(in.Input.Planet.StarNum, in.Input.Planet.PlanetNum)): in.Proposed.Planet} {
		out[k] = factoryBuildJSON(t, v)
	}
	if in.Allocation.FreeKey != 0 {
		outKey := fmt.Sprintf("%x/%016x", db.BucketShipFree, in.Allocation.FreeKey)
		delete(out, outKey)
	}
	return out
}
func TestPlanetFactoryBuildDispatchAtomic(t *testing.T) {
	for _, free := range []bool{false, true} {
		t.Run(fmt.Sprint(free), func(t *testing.T) {
			f := newFactoryBuildFixture(t, free)
			in := factoryBuildInput(t, f)
			before := factoryBuildRecords(t, f)
			var ptr *model.Planet
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { ptr = f.state.planets[f.star][f.planet] })
			reg := &factoryBuildRegistry{}
			observed := false
			reg.at = func(s string) {
				if strings.Contains(s, "built at a cost") {
					observed = true
					p, e := f.store.GetPlanet(f.star, f.planet)
					if e != nil {
						t.Error(e)
					}
					sh, e := f.store.GetShip(in.Input.Number)
					if e != nil {
						t.Error(e)
					}
					if !reflect.DeepEqual(p, in.Proposed.Planet) || !reflect.DeepEqual(sh, in.Proposed.Ship) || !reflect.DeepEqual(sh, f.state.ships[in.Input.Number]) || f.state.planets[f.star][f.planet] != ptr {
						t.Error("success before complete persistence/install")
					}
					f.store.SetWriteFence(true) // output cannot split this completed transaction
				}
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
			text := f.command(t, "build F 0,0")
			f.store.SetWriteFence(false)
			if !observed || text != "" || f.state.Mode() != ModeServing {
				t.Fatal("build not delivered", text, reg.all())
			}
			if want := factoryBuildExpected(t, before, in); !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("unrelated records or incomplete new record set")
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			// Compare complete new ship against the unchanged legacy design/init helpers.
			var legacy model.Ship
			legacyState := &State{cfg: f.cfg}
			legacyState.getshipDefaults(&legacy, model.OtypeFactory, in.Input.Race)
			legacyState.initializeNewShip(1, 0, in.Input.Race, &legacy, 0, 0)
			want := in.Proposed.Ship
			legacy.Number = want.Number
			legacy.Owner = want.Owner
			legacy.Governor = want.Governor
			legacy.Tech = want.Tech
			legacy.XPos = want.XPos
			legacy.YPos = want.YPos
			legacy.LandX = want.LandX
			legacy.LandY = want.LandY
			legacy.Class = want.Class
			legacy.WhatOrbits = want.WhatOrbits
			legacy.WhatDest = want.WhatDest
			legacy.StOrbits = want.StOrbits
			legacy.PNumOrbits = want.PNumOrbits
			legacy.DestStar = want.DestStar
			legacy.DestPNum = want.DestPNum
			legacy.Docked = want.Docked
			legacy.NextShip = want.NextShip
			if !reflect.DeepEqual(&legacy, want) {
				t.Fatal("factory semantics changed")
			}
		})
	}
}
func TestPlanetFactoryBuildGrammarAndAuthority(t *testing.T) {
	for _, arg := range []string{"F 0,0", "Factory 0,0 1 ignored", "F nonsense 0", "F 0,0 -2", "F 0,0 overflow"} {
		t.Run(arg, func(t *testing.T) {
			f := newFactoryBuildFixture(t, false)
			f.command(t, "build "+arg)
			sh, e := f.store.GetShip(78)
			if e != nil || sh == nil {
				t.Fatal("effective-one legacy grammar lost", e)
			}
		})
	}
	for _, which := range []string{"stock", "sector-owner", "sector-empty", "wasted", "enslaved", "governor", "inactive", "dissolved-race", "coordinates"} {
		t.Run(which, func(t *testing.T) {
			f := newFactoryBuildFixture(t, false)
			g := 0
			cmd := "build F 0,0"
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				p := f.state.planets[f.star][f.planet]
				r := f.state.races[0]
				st := f.state.stars[f.star]
				switch which {
				case "stock":
					p.Info[0].Resource = 0
				case "sector-owner":
					p.SectorAt(0, 0).Owner = 2
				case "sector-empty":
					p.SectorAt(0, 0).Popn = 0
				case "wasted":
					p.SectorAt(0, 0).Condition = model.SectorWasted
				case "enslaved":
					p.SlavedTo = 2
				case "governor":
					g = 1
					r.Governor[1].Active = true
					f.state.dirs[0][1] = f.state.dirs[0][0]
				case "inactive":
					r.Governor[0].Active = false
				case "dissolved-race":
					r.Dissolved = true
				case "coordinates":
					cmd = "build F -1,0"
				}
				for _, e := range []error{f.store.PutPlanet(p), f.store.PutRace(r), f.store.PutStar(st)} {
					if e != nil {
						t.Error(e)
					}
				}
			})
			before := factoryBuildRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			reg := &fakeRegistry{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
			r := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, cmd, r); e != nil {
				t.Fatal(e)
			}
			if f.state.Mode() != ModeServing || r.text() == "" || reg.count() != 0 || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("validation escaped", r.text())
			}
		})
	}
}
func TestPlanetFactoryBuildLegacyClassification(t *testing.T) {
	for _, which := range []string{"autoload", "God", "multiple", "special", "ship", "info"} {
		t.Run(which, func(t *testing.T) {
			f := newFactoryBuildFixture(t, false)
			args := []string{"F", "0,0"}
			handled := true
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				switch which {
				case "autoload":
					f.state.races[0].Governor[0].Toggle.AutoLoad = true
				case "God":
					f.state.races[0].God = true
				case "multiple":
					args = append(args, "2")
				case "special":
					args[0] = "w"
				case "ship":
					f.state.dirs[0][0].Level = model.LevelShip
				case "info":
					args = []string{"?"}
				}
				handled = f.state.handlePlanetFactoryBuild(1, 0, args, &recordingResponder{})
			})
			if handled {
				t.Fatal("unsupported variant entered slice")
			}
		})
	}
}
func TestPlanetFactoryBuildFailureMatrix(t *testing.T) {
	for _, stage := range []string{"factory-build.before", "factory-build.check:1", "factory-build.check:2", "factory-build.check:3", "factory-build.check:4", "factory-build.check:5", "factory-build.check:6", "factory-build.check:7", "factory-build.validated", "factory-build.write:1", "factory-build.write:2", "factory-build.write:3", "factory-build.before-commit", "factory-build.after-commit", "factory-build.commit-error"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				f := newFactoryBuildFixture(t, true)
				in := factoryBuildInput(t, f)
				before := factoryBuildRecords(t, f)
				actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
				reg := &fakeRegistry{}
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
				hit := false
				f.store.SetMetaFault(func(op string) error {
					if op == stage {
						hit = true
						if panics {
							panic("injected")
						}
						return errors.New("injected")
					}
					if op == "command.readback" {
						return errors.New("diagnostic readback unavailable")
					}
					return nil
				})
				f.command(t, "build F 0,0")
				f.store.SetMetaFault(nil)
				committed := stage == "factory-build.after-commit" || stage == "factory-build.commit-error"
				want := before
				if committed {
					want = factoryBuildExpected(t, before, in)
				}
				if !hit || f.state.Mode() != ModeHalted || strings.Contains(reg.all(), "built at a cost") || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
					t.Fatal("fault leaked or not reached", stage)
				}
				if stage != "factory-build.after-commit" && actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("unconfirmed proposal installed")
				}
				if stage == "factory-build.commit-error" && !f.store.CommandWriteUncertain() {
					t.Fatal("uncertainty guard missing")
				}
				_ = f.state.Stop(ctxT(t, 10*time.Second))
				if !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
					t.Fatal("stale shutdown flush")
				}
			})
		}
	}
}
func TestPlanetFactoryBuildPendingAndStructuralState(t *testing.T) {
	for _, which := range []string{"AP", "race", "planet", "ship", "partial-root", "actor-only", "candidate-actor-only", "missing-link", "cycle", "decode"} {
		t.Run(which, func(t *testing.T) {
			f := newFactoryBuildFixture(t, false)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				p := f.state.planets[f.star][f.planet]
				sh := f.state.ships[77]
				switch which {
				case "AP":
					f.state.stars[f.star].AP[0]++
				case "race":
					f.state.races[0].Morale++
				case "planet":
					p.Info[0].Resource--
				case "ship":
					sh.Damage++
				case "partial-root", "actor-only":
					q := factoryCopyShip(t, sh)
					q.Number = 78
					q.NextShip = p.Ships
					p.Ships = 78
					f.state.ships[78] = q
					if which == "partial-root" {
						if e := f.store.PutShip(q); e != nil {
							t.Error(e)
						}
					}
				case "candidate-actor-only":
					q := factoryCopyShip(t, sh)
					q.Number = 78
					f.state.ships[78] = q
				case "missing-link":
					sh.NextShip = 999
				case "cycle":
					sh.NextShip = 77
				case "decode":
					sh.Mass = math.NaN()
				}
			})
			before := factoryBuildRecords(t, f)
			reg := &fakeRegistry{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
			text := f.command(t, "build F 0,0")
			corrupt := which == "missing-link" || which == "cycle" || which == "decode"
			if !reflect.DeepEqual(before, factoryBuildRecords(t, f)) || strings.Contains(reg.all(), "built at a cost") || (!corrupt && reg.count() != 0) || ((f.state.Mode() == ModeHalted) != corrupt) || (!corrupt && !strings.Contains(text, "retry after the next persisted turn")) {
				t.Fatal("pending/corrupt misclassification", which, text)
			}
			if !corrupt && which != "actor-only" && which != "candidate-actor-only" {
				if e := f.state.TriggerSegment(ctxT(t, 10*time.Second)); e != nil {
					t.Fatal(e)
				}
				f.command(t, "build F 0,0")
				if f.state.Mode() != ModeServing {
					t.Fatal("manual retry failed")
				}
			}
		})
	}
}
func TestPlanetFactoryBuildTurnShutdownAndConcurrency(t *testing.T) {
	f := newFactoryBuildFixture(t, true)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "build F 0,0", r); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	for _, n := range []uint32{70, 78} {
		sh, e := f.store.GetShip(n)
		if e != nil || sh == nil {
			t.Fatal("duplicate/lost ID", n, e)
		}
	}
	if p, e := f.store.GetPlanet(f.star, f.planet); e != nil || p.Info[0].Resource != 9954 {
		t.Fatal("double-build charges", e)
	}
	f.restart(t)
	for _, update := range []bool{false, true} {
		before := f.state.Status()
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			when := f.state.sched.nextSegment.Add(time.Nanosecond)
			if update {
				when = f.state.sched.nextUpdate.Add(time.Nanosecond)
			}
			f.state.now = func() time.Time { return when }
		})
		deadline := time.Now().Add(10 * time.Second)
		for {
			st := f.state.Status()
			if (!update && st.NumSegments > before.NumSegments) || (update && st.NumUpdates > before.NumUpdates) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal(st)
			}
			time.Sleep(time.Millisecond)
		}
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		for _, n := range []uint32{70, 78} {
			sh, e := f.store.GetShip(n)
			if e != nil || sh == nil || sh.Type != model.OtypeFactory {
				t.Fatal("turn lost factory", e)
			}
		}
	}
	f.restart(t)
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}

func TestPlanetFactoryBuildLateWriterAndPostcommitFailures(t *testing.T) {
	for _, which := range []string{"late-planet", "late-chain", "late-AP", "late-free", "preinstall", "installed", "delivery"} {
		t.Run(which, func(t *testing.T) {
			f := newFactoryBuildFixture(t, true)
			in := factoryBuildInput(t, f)
			before := factoryBuildRecords(t, f)
			want := factoryBuildExpected(t, before, in)
			reg := &factoryBuildRegistry{}
			var afterWriter map[string]string
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				f.state.SetRegistry(reg)
				switch which {
				case "preinstall":
					f.state.testHooks.beforeCommandInstall = func() { panic("injected before install") }
				case "installed":
					f.state.testHooks.afterCommandInstall = func() { panic("injected after install") }
				case "delivery":
					reg.at = func(string) { panic("injected delivery failure") }
				}
			})
			if strings.HasPrefix(which, "late-") {
				f.store.SetMetaFault(func(op string) error {
					if op != "factory-build.before" {
						return nil
					}
					var e error
					switch which {
					case "late-planet":
						q := planetEdit(in.Input.Planet)
						q.Info[2].Resource++
						e = f.store.PutPlanet(q)
					case "late-AP":
						q := *in.Input.Star
						q.AP[0]++
						e = f.store.PutStar(&q)
					case "late-chain":
						q := factoryCopyShip(t, in.Input.Ships[0])
						q.Damage++
						e = f.store.PutShip(q)
					case "late-free":
						e = f.store.DeleteShip(71)
					}
					// Already executing on this fixture's sole actor, so no actor request.
					afterWriter = commandFixtureRecords(t, stringMustReadFactoryBuild(t, f.path))
					return e
				})
			}
			f.command(t, "build F 0,0")
			f.store.SetMetaFault(nil)
			if f.state.Mode() != ModeHalted {
				t.Fatal("post-admission failure not halted")
			}
			if afterWriter != nil {
				want = afterWriter
			}
			if !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("late writer or confirmed commit overwritten")
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				sh := f.state.ships[in.Input.Number]
				installed := which == "installed" || which == "delivery"
				if (sh != nil) != installed {
					t.Error("wrong actor outcome", which)
				}
			})
			if which != "delivery" && strings.Contains(reg.all(), "built at a cost") {
				t.Fatal("premature success")
			}
			_ = f.state.Stop(ctxT(t, 10*time.Second))
			if !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("stale flush after commit/CAS failure")
			}
		})
	}
}

func TestPlanetFactoryBuildRealLegacyFuelDefers(t *testing.T) {
	f := newFactoryBuildFixture(t, false)
	prepareLegacyFuelLoad(t, f.actorEnrollmentFixture, f.star, f.planet, 77, 100)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		st := f.state.stars[f.star]
		st.AP[0] = 10
		sh := f.state.ships[77]
		sh.Type = model.StypeCargo
		if e := f.store.PutShip(sh); e != nil {
			t.Error(e)
		}
		if e := f.store.PutStar(st); e != nil {
			t.Error(e)
		}
	})
	text := f.command(t, "load #77 f 1")
	if !strings.Contains(text, "fuel transferred") {
		t.Fatal("legacy load not exercised", text)
	}
	before := factoryBuildRecords(t, f)
	actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	text = f.command(t, "build F 0,0")
	if f.state.Mode() != ModeServing || !strings.Contains(text, "retry after the next persisted turn") || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
		t.Fatal("legacy pending load not deferred", text)
	}
	if e := f.state.TriggerSegment(ctxT(t, 10*time.Second)); e != nil {
		t.Fatal(e)
	}
	f.command(t, "build F 0,0")
	if sh, e := f.store.GetShip(78); e != nil || sh == nil {
		t.Fatal("manual retry did not construct", e)
	}
}

func TestPlanetFactoryBuildQuiescedAndFenced(t *testing.T) {
	for _, which := range []string{"quiesced", "fenced"} {
		t.Run(which, func(t *testing.T) {
			f := newFactoryBuildFixture(t, false)
			if which == "quiesced" {
				if e := f.state.Quiesce(ctxT(t, 10*time.Second)); e != nil {
					t.Fatal(e)
				}
			} else {
				f.store.SetWriteFence(true)
			}
			before := factoryBuildRecords(t, f)
			f.command(t, "build F 0,0")
			f.store.SetWriteFence(false)
			if !reflect.DeepEqual(before, factoryBuildRecords(t, f)) {
				t.Fatal("maintenance/fence wrote construction")
			}
			if which == "quiesced" && f.state.Mode() != ModeQuiesced {
				t.Fatal("quiesced changed mode")
			}
		})
	}
}
