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
	for _, which := range []string{"God", "multiple", "special", "ship", "info"} {
		t.Run(which, func(t *testing.T) {
			f := newFactoryBuildFixture(t, false)
			args := []string{"F", "0,0"}
			handled := true
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				switch which {
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
	for _, which := range []string{"late-planet", "late-chain", "late-AP", "late-free", "late-toggle", "late-sector", "preinstall", "installed", "delivery"} {
		t.Run(which, func(t *testing.T) {
			f := newFactoryBuildFixture(t, true)
			if which == "late-toggle" || which == "late-sector" {
				enableFactoryAutoload(t, f, 7, 0)
			}
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
					case "late-toggle":
						q := *in.Input.Race
						q.Governor[0].Toggle.AutoLoad = false
						e = f.store.PutRace(&q)
					case "late-sector":
						q := planetEdit(in.Input.Planet)
						q.SectorAt(0, 0).Fert++
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

func enableFactoryAutoload(t *testing.T, f *factoryFixture, civilians, troops uint16) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		p := f.state.planets[f.star][f.planet]
		sec := p.SectorAt(0, 0)
		sec.Popn, sec.Troops, sec.Mobilization = civilians, troops, 3
		populationTotals(p)
		r := f.state.races[0]
		r.Mass = 1.25
		if err := f.store.PutRace(r); err != nil {
			t.Fatal(err)
		}
		if err := f.store.PutPlanet(p); err != nil {
			t.Fatal(err)
		}
	})
	if msg := f.command(t, "toggle autoload"); msg != "autoload is now ON\n" {
		t.Fatalf("toggle autoload: %q", msg)
	}
}

func TestPlanetFactoryBuildAutoloadDispatch(t *testing.T) {
	t.Parallel()
	for _, free := range []bool{false, true} {
		for _, tc := range []struct{ civilians, troops, loaded uint16 }{{7, 0, 7}, {20, 3, 20}, {31, 0, 20}} {
			t.Run(fmt.Sprintf("free=%t/popn=%d/troops=%d", free, tc.civilians, tc.troops), func(t *testing.T) {
				f := newFactoryBuildFixture(t, free)
				enableFactoryAutoload(t, f, tc.civilians, tc.troops)
				in := factoryBuildInput(t, f)
				id := uint32(78)
				if free {
					id = 70
				}
				original := in.Input.Planet
				want := planetEdit(original)
				want.Info[0].Resource -= 23
				want.Ships = id
				sec := want.SectorAt(0, 0)
				sec.Popn -= tc.loaded
				if sec.Popn == 0 && tc.troops == 0 {
					sec.Owner = 0
				}
				populationTotals(want) // independent recount of every sector and owner's totals
				before := factoryBuildRecords(t, f)
				var ptr *model.Planet
				reg := &factoryBuildRegistry{}
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					ptr = f.state.planets[f.star][f.planet]
					reg.at = func(string) {
						planet, err := f.store.GetPlanet(f.star, f.planet)
						sh, shipErr := f.store.GetShip(id)
						if err != nil || shipErr != nil || sh == nil || !reflect.DeepEqual(planet, want) ||
							ptr != f.state.planets[f.star][f.planet] || !reflect.DeepEqual(ptr, planet) ||
							!reflect.DeepEqual(sh, f.state.ships[id]) {
							t.Error("build notice before complete durable and actor installation")
							return
						}
						if sh.Popn != tc.loaded || sh.Fuel != 0 || sh.BuildCost != 23 ||
							sh.Mass != sh.BaseMass+float64(tc.loaded)*1.25 || sh.NextShip != original.Ships {
							t.Errorf("incorrect factory #%d crew=%d fuel=%g mass=%g cost=%d next=%d", id, sh.Popn, sh.Fuel, sh.Mass, sh.BuildCost, sh.NextShip)
						}
					}
					f.state.SetRegistry(reg)
				})
				msg := f.command(t, "build F 0,0")
				loaded := fmt.Sprintf("Loaded with %d crew and 0.0 fuel.", tc.loaded)
				if msg != "" || f.state.Mode() != ModeServing || strings.Count(reg.all(), loaded) != 1 || reg.count() != 7 {
					t.Fatalf("build response %q, notices %q", msg, reg.all())
				}
				if !reflect.DeepEqual(factoryBuildExpected(t, before, in), factoryBuildRecords(t, f)) {
					t.Fatal("unexpected changes to durable records or allocator")
				}
			})
		}
	}
}

func TestPlanetFactoryBuildAutoloadScheduledPersistence(t *testing.T) {
	t.Parallel()
	f := newFactoryBuildFixture(t, true)
	enableFactoryAutoload(t, f, 7, 0)
	if msg := f.command(t, "build F 0,0"); msg != "" {
		t.Fatal(msg)
	}
	check := func() {
		t.Helper()
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		p, err := f.store.GetPlanet(f.star, f.planet)
		if err != nil || p == nil {
			t.Fatal("missing planet", err)
		}
		child, err := f.store.GetShip(70)
		if err != nil || child == nil || child.Type != model.OtypeFactory || child.Popn != 7 || child.Fuel != 0 ||
			child.Mass != child.BaseMass+7*1.25 || !child.Docked || child.WhatOrbits != model.LevelPlan || child.StOrbits != f.star || child.PNumOrbits != f.planet {
			t.Fatal("lost or changed crewed factory", child, err)
		}
		seen := map[uint32]bool{}
		for n := p.Ships; n != 0; {
			sh, e := f.store.GetShip(n)
			if e != nil || sh == nil || seen[n] {
				t.Fatal("broken planet ship chain", n, e)
			}
			seen[n] = true
			n = sh.NextShip
		}
		if !seen[70] || !seen[77] || p.SectorAt(0, 0).Owner != 0 || p.SectorAt(0, 0).Popn != 0 || p.Info[0].Resource != 9977 {
			t.Fatalf("lost construction or evacuation: chain=%v sector=%+v resources=%d", seen, p.SectorAt(0, 0), p.Info[0].Resource)
		}
		want := planetEdit(p)
		populationTotals(want)
		if p.Popn != want.Popn || p.Troops != want.Troops || p.Info != want.Info {
			t.Fatal("sector and planet accounting disagree")
		}
	}
	check()
	scheduledBuildTurnsAndRestart(t, f, check)
}

// Include all world records and allocation metadata, as well as actor state and notices.
func assertFactoryBuildRefused(t *testing.T, f *factoryFixture, reason string) {
	t.Helper()
	reg := &fakeRegistry{}
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
	before := factoryBuildRecords(t, f)
	actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	msg := f.command(t, "build F 0,0")
	if !strings.Contains(msg, reason) || f.state.Mode() != ModeServing || f.store.CommandWriteUncertain() ||
		reg.count() != 0 || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) {
		t.Fatalf("expected safe refusal %q, got %q, mode=%v, notices=%q", reason, msg, f.state.Mode(), reg.all())
	}
}

func TestPlanetFactoryBuildAutoloadAfterScrap(t *testing.T) {
	t.Parallel()
	f := newFactoryBuildFixture(t, true)
	enableFactoryAutoload(t, f, 5, 0)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		sh := f.state.ships[f.factory]
		sh.Popn = 10
		sh.Mass = sh.BaseMass + 10*f.state.races[0].Mass
		st := f.state.stars[f.star]
		st.AP[0] = 10
		if err := f.store.PutShip(sh); err != nil {
			t.Fatal(err)
		}
		if err := f.store.PutStar(st); err != nil {
			t.Fatal(err)
		}
	})
	if msg := f.command(t, "scrap #77"); !strings.Contains(msg, "Scrapped.") {
		t.Fatal(msg)
	}
	assertFactoryBuildRefused(t, f, "retry after the next persisted turn")
	updates := f.state.Status().NumUpdates
	var child uint32
	scheduledBuildTurnsAndRestart(t, f, func() {
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		p, err := f.store.GetPlanet(f.star, f.planet)
		if err != nil || p == nil {
			t.Fatal("missing home planet", err)
		}
		if f.state.Status().NumUpdates == updates { // the first scheduled segment
			if p.SectorAt(0, 0).Popn != 15 || p.Popn != 15 || p.Info[0].Popn != 5 {
				t.Fatal("scrap accounting discrepancy was not reproduced")
			}
			assertFactoryBuildRefused(t, f, "totals disagree")
			return
		}
		if child == 0 { // the full update recounted the planet; retry manually
			child = factoryBuildInput(t, f).Allocation.Number
			if msg := f.command(t, "build F 0,0"); msg != "" {
				t.Fatal(msg)
			}
		}
		if sh, err := f.store.GetShip(child); err != nil || sh == nil || sh.Type != model.OtypeFactory || sh.Popn == 0 {
			t.Fatal("lost factory after retry", child, err)
		}
	})
}

// Let a single source spread last. Every random direction from the center
// reaches an already-visited neutral neighbor with its mobilization intact.
func TestPlanetFactoryBuildAfterRecolonization(t *testing.T) {
	t.Parallel()
	f := newFactoryBuildFixture(t, true)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		p := f.state.planets[f.star][f.planet]
		p.MaxX, p.MaxY = 3, 3
		p.Sectors = make([]model.Sector, 9)
		for i := range p.Sectors {
			p.Sectors[i] = model.Sector{Type: model.SectorLand, Condition: model.SectorLand, Fert: 100, Eff: 10, Mobilization: 60}
		}
		source := p.SectorAt(1, 1)
		source.Owner, source.Race, source.Popn, source.Fert = 1, 1, 240, 50
		p.Info[0].MobSet = 60
		p.Conditions[model.AtmosToxic] = 0
		populationTotals(p)
		r := f.state.races[0]
		r.Conditions = p.Conditions
		r.Conditions[model.AtmosTemp] = p.Conditions[model.AtmosRTemp]
		r.Adventurism, r.BirthRate, r.Fertilize, r.NumberSexes = 1.0/6, 0, 0, 2
		r.Likes[model.SectorLand] = 1
		if err := f.store.PutRace(r); err != nil {
			t.Fatal(err)
		}
		if err := f.store.PutPlanet(p); err != nil {
			t.Fatal(err)
		}
		f.state.testHooks = &testHooks{orderPlanetSectors: func(pl *model.Planet, coords []sectorCoord) {
			if pl == p {
				for i, c := range coords {
					if c.x == 1 && c.y == 1 {
						last := len(coords) - 1
						coords[i], coords[last] = coords[last], coords[i]
						break
					}
				}
			}
		}}
	})
	if msg := f.command(t, "toggle autoload"); msg != "autoload is now ON\n" {
		t.Fatal(msg)
	}
	if err := f.state.TriggerUpdate(ctxT(t, 10*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	p, err := f.store.GetPlanet(f.star, f.planet)
	if err != nil || p == nil {
		t.Fatal(err)
	}
	info := p.Info[0]
	if info.NumSectsOwned != 2 || info.Popn != 240 || p.Popn != 240 || info.ComRead != 30 || info.Guns != 12 {
		t.Fatalf("expected two sectors and one sector's production: %+v, population %d", info, p.Popn)
	}
	for i, sec := range p.Sectors {
		if sec.Owner != 1 || i == 4 {
			continue
		}
		if sec.Popn == 0 || sec.Popn > 20 || sec.Mobilization != 60 {
			t.Fatalf("unexpected recolonized sector: %+v", sec)
		}
		x, y := i%int(p.MaxX), i/int(p.MaxX)
		if msg := f.command(t, fmt.Sprintf("build F %d,%d", x, y)); msg != "" {
			t.Fatalf("build on recolonized sector (%d,%d): %q, prebuild MobPoints=%d, sector sum=120", x, y, msg, info.MobPoints)
		}
		if info.MobPoints != 120 {
			t.Fatalf("full update persisted %d points for two 60-point sectors", info.MobPoints)
		}
		want := planetEdit(p)
		want.Ships = 70
		want.Sectors[i].Owner, want.Sectors[i].Popn = 0, 0
		want.Info[0].Resource -= 23
		populationTotals(want)
		check := func() {
			t.Helper()
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			got, err := f.store.GetPlanet(f.star, f.planet)
			if err != nil || !reflect.DeepEqual(got, want) || got.Info[0].MobPoints != 60 {
				t.Fatal("evacuation or persisted planet accounting changed", err)
			}
			sh, err := f.store.GetShip(70)
			if err != nil || sh == nil || sh.Type != model.OtypeFactory || sh.Popn != sec.Popn || sh.NextShip != p.Ships {
				t.Fatal("lost crewed factory after restart", sh, err)
			}
		}
		check()
		f.restart(t)
		check()
		return
	}
	t.Fatal("migration failed to establish a colony")
}

// Toxic damage removes one sector after every sector has produced. With one
// initial owner this also exercises the last-sector loss without a build.
func TestPlanetFactoryBuildUpdateLostMobilization(t *testing.T) {
	for _, width := range []uint8{1, 2} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			f := newFactoryBuildFixture(t, false)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				p := f.state.planets[f.star][f.planet]
				p.MaxX, p.MaxY = width, 1
				p.Sectors = make([]model.Sector, width)
				for i := range p.Sectors {
					p.Sectors[i] = model.Sector{Owner: 1, Race: 1, Troops: 1, Mobilization: 60}
				}
				p.ExplTimer = 4
				p.Info[0].MobSet = 60
				p.Conditions[model.AtmosToxic] = 100
				populationTotals(p)
				if err := f.store.PutPlanet(p); err != nil {
					t.Fatal(err)
				}
			})
			if err := f.state.TriggerUpdate(ctxT(t, 10*time.Second)); err != nil {
				t.Fatal(err)
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			p, err := f.store.GetPlanet(f.star, f.planet)
			if err != nil || p == nil {
				t.Fatal(err)
			}
			remaining := uint16(width - 1)
			i := p.Info[0]
			if i.NumSectsOwned != remaining || i.MobPoints != 60*uint64(remaining) || i.Guns != 12*width {
				t.Fatalf("lost sector: owners=%d points=%d guns=%d", i.NumSectsOwned, i.MobPoints, i.Guns)
			}
		})
	}
}

func TestPlanetFactoryBuildAutoloadHomeworld(t *testing.T) {
	f := newFactoryBuildFixture(t, true) // capital and aggregates come from actual enrollment
	p, err := f.store.GetPlanet(f.star, f.planet)
	if err != nil || p == nil || p.Sectors[0].Popn == 0 || p.Popn != uint64(p.Sectors[0].Popn) || p.Info[0].Popn != 0 {
		t.Fatal("expected initial homeworld accounting", err)
	}
	if msg := f.command(t, "toggle autoload"); msg != "autoload is now ON\n" {
		t.Fatal(msg)
	}
	assertFactoryBuildRefused(t, f, "totals disagree")
	if msg := f.command(t, "toggle autoload"); msg != "autoload is now OFF\n" {
		t.Fatal(msg)
	}
	if msg := f.command(t, "build F 0,0"); msg != "" {
		t.Fatal(msg)
	}
	p.Ships, p.Info[0].Resource = 70, p.Info[0].Resource-23
	got, err := f.store.GetPlanet(f.star, f.planet)
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatal("empty factory must preserve existing population accounts", err)
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}

func TestPlanetFactoryBuildAutoloadAccountingRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*model.Planet)
	}{
		{"planet undercount", func(p *model.Planet) { p.Popn-- }},
		{"player undercount", func(p *model.Planet) { p.Info[0].Popn-- }},
		{"ownership undercount", func(p *model.Planet) { p.Info[0].NumSectsOwned-- }},
		{"mobilization undercount", func(p *model.Planet) { p.Info[0].MobPoints-- }},
		{"planet overcount", func(p *model.Planet) { p.Popn++ }},
		{"player overcount", func(p *model.Planet) { p.Info[0].Popn++ }},
		{"ownership overcount", func(p *model.Planet) { p.Info[0].NumSectsOwned++ }},
		{"mobilization overcount", func(p *model.Planet) { p.Info[0].MobPoints++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFactoryBuildFixture(t, true)
			enableFactoryAutoload(t, f, 7, 0)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				p := f.state.planets[f.star][f.planet]
				tc.edit(p)
				if err := f.store.PutPlanet(p); err != nil {
					t.Fatal(err)
				}
			})
			assertFactoryBuildRefused(t, f, "totals disagree")
		})
	}
}

func TestPlanetFactoryBuildAutoloadAdmissionAndUncertainty(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"pending population", "invalid sector owner", "invalid race mass", "crew mass overflow", "uncertain commit"} {
		t.Run(which, func(t *testing.T) {
			f := newFactoryBuildFixture(t, true)
			enableFactoryAutoload(t, f, 7, 0)
			reg := &fakeRegistry{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				p := f.state.planets[f.star][f.planet]
				f.state.SetRegistry(reg)
				switch which {
				case "pending population":
					p.SectorAt(0, 0).Popn++
					populationTotals(p)
				case "invalid sector owner":
					p.Sectors[1].Owner = model.MaxPlayers + 1
					if err := f.store.PutPlanet(p); err != nil {
						t.Fatal(err)
					}
				case "invalid race mass", "crew mass overflow":
					r := f.state.races[0]
					r.Mass = -1
					if which == "crew mass overflow" {
						r.Mass = math.MaxFloat64
					}
					if e := f.store.PutRace(r); e != nil {
						t.Fatal(e)
					}
				}
			})
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			want := factoryBuildRecords(t, f)
			if which == "uncertain commit" {
				want = factoryBuildExpected(t, want, factoryBuildInput(t, f))
				f.store.SetMetaFault(func(op string) error {
					if op == "factory-build.commit-error" || op == "command.readback" {
						return errors.New("injected")
					}
					return nil
				})
			}
			msg := f.command(t, "build F 0,0")
			f.store.SetMetaFault(nil)
			if !reflect.DeepEqual(want, factoryBuildRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || strings.Contains(reg.all(), "gov:") {
				t.Fatal("unexpected proposal, durable world or success notice", which, msg)
			}
			if which == "pending population" {
				if f.state.Mode() != ModeServing || !strings.Contains(msg, "retry after the next persisted turn") {
					t.Fatal(msg, f.state.Mode())
				}
				return
			}
			if f.state.Mode() != ModeHalted || f.store.CommandWriteUncertain() != (which == "uncertain commit") {
				t.Fatal(msg, f.state.Mode())
			}
			if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("stale shutdown flush", e)
			}
		})
	}
}
