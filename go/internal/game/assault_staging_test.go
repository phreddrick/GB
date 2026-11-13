package game

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

func highAssaultDraw(d commandrules.AssaultDraw) (commandrules.AssaultDraw, error) {
	if d.Kind == "round" {
		d.Fraction = float64(2147483647) / 2147483648
	} else {
		d.Integer = d.Bound
	}
	return d, nil
}
func newGroundAssaultFixture(t *testing.T, military bool) *factoryFixture {
	t.Helper()
	f := newFactoryFixture(t)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		a := f.state.races[0]
		a.Name = "attacker"
		a.Morale = 0
		a.Fighters = 10
		a.Absorb = false
		a.BirthRate = 0
		a.Translate = [model.MaxPlayers]int8{}
		a.Points = [model.MaxPlayers]uint32{}
		a.Likes[1] = 1
		d := *a
		d.Name = "defender"
		d.PlayerNum = 2
		d.Fighters = 1
		d.God = false
		d.GovShip = 0
		f.state.races[1] = &d
		p := f.state.planets[f.star][f.planet]
		for i := range p.Sectors {
			p.Sectors[i].Owner = 0
			p.Sectors[i].Popn = 0
			p.Sectors[i].Troops = 0
		}
		p.Sectors[0] = model.Sector{Owner: 1, Race: 1, Popn: 1000, Mobilization: 3, Condition: 1, Type: 1, Fert: 100, Resource: 17, Crystals: 3}
		if military {
			p.Sectors[0].Popn = 0
			p.Sectors[0].Troops = 1000
		}
		p.Sectors[1] = model.Sector{Owner: 2, Race: 2, Popn: 1, Mobilization: 7, Condition: 1, Type: 1, Fert: 100, Resource: 19, Crystals: 4}
		populationTotals(p)
		st := f.state.stars[f.star]
		st.AP[0] = 100
		f.state.dirs[0][0].Level = model.LevelPlan
		f.state.dirs[0][0].StarNum = f.star
		f.state.dirs[0][0].PlanetNum = f.planet
		f.state.assaultRandom = highAssaultDraw
		for _, r := range []*model.Race{a, &d} {
			if e := f.store.PutRace(r); e != nil {
				t.Error(e)
			}
		}
		if e := f.store.PutPlanet(p); e != nil {
			t.Error(e)
		}
		if e := f.store.PutStar(st); e != nil {
			t.Error(e)
		}
	})
	return f
}
func assaultRecords(t *testing.T, f *factoryFixture) map[string]string {
	// Once the actor exited, there is no writer; a direct private fixture-file
	// copy verifies every record and bucket sequence without an actor request.
	select {
	case <-f.state.stopped:
		root, e := os.OpenRoot(filepath.Dir(f.path))
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			if e := root.Close(); e != nil {
				t.Error(e)
			}
		}()
		b, e := root.ReadFile("enrollment.db")
		if e != nil {
			t.Fatal(e)
		}
		return commandFixtureRecords(t, string(b))
	default:
		return commandFixtureRecords(t, commandFixtureImage(t, f.actorEnrollmentFixture))
	}
}
func assertAssaultActorAndEffects(t *testing.T, f *factoryFixture, want db.GroundAssault) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		p := f.state.planets[f.star][f.planet]
		st := f.state.stars[f.star]
		if !reflect.DeepEqual(p, want.Proposed.Planet) || !reflect.DeepEqual(st, want.Proposed.Star) || !reflect.DeepEqual(f.state.races[0], want.Proposed.Attacker) || !reflect.DeepEqual(f.state.races[1], want.Proposed.Defender) {
			t.Error("complete actor proposal differs")
		}
		disk, e := f.store.GetPlanet(f.star, f.planet)
		if e != nil || !reflect.DeepEqual(p, disk) {
			t.Error("actor/store planet differs", e)
		}
		ds, e := f.store.GetStar(f.star)
		if e != nil || !reflect.DeepEqual(st, ds) {
			t.Error("actor/store star differs", e)
		}
		for _, r := range f.state.races {
			if r == nil {
				continue
			}
			dr, e := f.store.GetRace(r.PlayerNum)
			if e != nil || !reflect.DeepEqual(r, dr) {
				t.Error("actor/store race differs", e)
			}
		}
		if f.state.groundAssaults[0][1][f.star] != 0 {
			t.Error("duplicate volatile reporting counter")
		}
	})
	lines, _, e := f.store.ReadNewsFrom(model.NewsCombat, 0)
	if e != nil || len(lines) != 1 {
		t.Fatal("missing/duplicate news", len(lines), e)
	}
	has, e := f.store.HasTelegrams(2, 0)
	if e != nil || !has {
		t.Fatal("defender inbox missing", e)
	}
}
func assaultPrepared(t *testing.T, f *factoryFixture, military bool) db.GroundAssault {
	t.Helper()
	var in db.GroundAssault
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		_, v, validation, e := f.state.groundAssaultStage(1, 0, commandrules.AssaultRequest{Direction: '6', Quantity: 1000, Military: military, MoveFactor: 1})
		if e != nil || validation != "" {
			t.Error(validation, e)
		}
		in = v
	})
	return in
}
func TestGroundAssaultActorCR19Capture(t *testing.T) {
	t.Parallel()
	for _, mil := range []bool{false, true} {
		t.Run(fmt.Sprint(mil), func(t *testing.T) {
			f := newGroundAssaultFixture(t, mil)
			want := assaultPrepared(t, f, mil)
			before := assaultRecords(t, f)
			var p *model.Planet
			var a, d *model.Race
			var st *model.Star
			var sector *model.Sector
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				p = f.state.planets[f.star][f.planet]
				a = f.state.races[0]
				d = f.state.races[1]
				st = f.state.stars[f.star]
				sector = &p.Sectors[1]
			})
			verb := "move"
			if mil {
				verb = "deploy"
			}
			out := f.command(t, verb+" 0,0 6 1000")
			if !strings.Contains(out, "VICTORY! The sector is yours!") {
				t.Fatal("CR19 actual capture missing", out)
			}
			assertAssaultActorAndEffects(t, f, want)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				p := f.state.planets[f.star][f.planet]
				if p.Sectors[1].Owner != 1 || (mil && p.Sectors[1].Troops != 1000) || (!mil && p.Sectors[1].Popn != 1000) || p.Sectors[0].Owner != 0 {
					t.Error("independent C-derived capture assertion")
				}
			})
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				if f.state.planets[f.star][f.planet] != p || f.state.races[0] != a || f.state.races[1] != d || f.state.stars[f.star] != st || &p.Sectors[1] != sector {
					t.Error("actor object identity changed")
				}
			})
			after := assaultRecords(t, f)
			for key, value := range before {
				if strings.HasPrefix(key, "7368697073/") || strings.HasPrefix(key, "6d657461/") || strings.HasPrefix(key, "bucket:") {
					if key == "bucket:74656c656772616d73" {
						continue
					}
					if after[key] != value {
						t.Fatal("unrelated record/allocator changed", key)
					}
				}
			}
			for _, stage := range []string{"segment", "update", "restart"} {
				var e error
				switch stage {
				case "segment":
					e = f.state.TriggerSegment(ctxT(t, 10*time.Second))
				case "update":
					e = f.state.TriggerUpdate(ctxT(t, 10*time.Second))
				default:
					f.restart(t)
				}
				if e != nil {
					t.Fatal(stage, e)
				}
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					p := f.state.planets[f.star][f.planet]
					if p.Sectors[1].Owner != 1 || p.Info[1].NumSectsOwned != 0 {
						t.Error(stage, "capture lost")
					}
					disk, e := f.store.GetPlanet(f.star, f.planet)
					if e != nil || !reflect.DeepEqual(p, disk) {
						t.Error(stage, "stale overwrite", e)
					}
				})
			}
			news, _, e := f.store.ReadNewsFrom(model.NewsCombat, 0)
			if e != nil || len(news) != 1 {
				t.Fatal("duplicate aggregate after turns", len(news), e)
			}
		})
	}
}
func TestGroundAssaultActorOutcomesAndRefusals(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"mutual", "repulse", "empty", "absorption", "negative", "zero", "overflow", "badcoord", "baddir", "invalidsuffix", "multihostile", "friendly-prefix", "inactive", "governor", "wrongorigin", "insufficient-ap", "mechanical", "mechanical-peer", "third-party-afv", "broken-chain", "cyclic-chain", "invalid-ship", "stale-planet", "stale-race", "stale-ap", "stale-ship", "hostile-unload"} {
		t.Run(kind, func(t *testing.T) {
			f := newGroundAssaultFixture(t, false)
			cmd := "move 0,0 6 1000"
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				p := f.state.planets[f.star][f.planet]
				a, d := f.state.races[0], f.state.races[1]
				switch kind {
				case "mutual":
					p.Sectors[0].Popn = 1
					a.Fighters = 1
					cmd = "move 0,0 6 1"
				case "repulse":
					p.Sectors[0].Popn = 10
					a.Fighters = 1
					d.Fighters = 255
					p.Sectors[1].Popn = 1000
					cmd = "move 0,0 6 10"
				case "empty":
					p.Sectors[1].Popn = 0
				case "absorption":
					a.Absorb = true
				case "negative":
					cmd = "move 0,0 6 -1"
				case "zero":
					cmd = "move 0,0 6 0"
				case "overflow":
					cmd = "move 0,0 6 18446744073709551616"
				case "badcoord":
					cmd = "move 0,0junk 6 1"
				case "baddir":
					cmd = "move 0,0 ? 1"
				case "invalidsuffix":
					cmd = "move 0,0 6? 1"
				case "multihostile":
					cmd = "move 0,0 66 1"
				case "friendly-prefix":
					p.Sectors[2] = p.Sectors[1]
					p.Sectors[1].Owner = 0
					p.Sectors[1].Popn = 0
					cmd = "move 0,0 66 1"
				case "inactive":
					a.Governor[0].Active = false
				case "governor":
					f.state.dirs[0][1] = f.state.dirs[0][0]
					a.Governor[1].Active = true
					f.state.stars[f.star].Governor[0] = 2
				case "wrongorigin":
					p.Sectors[0].Owner = 2
				case "hostile-unload":
					now := f.state.now()
					f.state.now = func() time.Time { return now }
					sh := f.state.ships[f.factory]
					sh.LandX = 1
					sh.LandY = 0
					if e := f.store.PutShip(sh); e != nil {
						t.Error(e)
					}
					cmd = "unload #77 c 1"
				case "insufficient-ap":
					f.state.stars[f.star].AP[0] = 0
				case "mechanical", "mechanical-peer", "third-party-afv":
					sh := f.state.ships[f.factory]
					sh.Type = model.OtypeAFV
					sh.Owner = 2
					sh.LandX = 1
					sh.LandY = 0
					sh.Primary = 10
					sh.Guns = model.GunPrimary
					sh.Retaliate = 10
					sh.Destruct = 10
					if kind == "mechanical-peer" {
						sh.WhatDest = model.LevelShip
						sh.DestShipNo = sh.NextShip
						peer := f.state.ships[sh.NextShip]
						peer.Docked = true
						peer.WhatDest = model.LevelShip
						peer.DestShipNo = sh.Number
						if e := f.store.PutShip(peer); e != nil {
							t.Error(e)
						}
					}
					if kind == "third-party-afv" {
						r := *d
						r.PlayerNum = 3
						r.Name = "third"
						f.state.races[2] = &r
						sh.Owner = 3
						if e := f.store.PutRace(&r); e != nil {
							t.Error(e)
						}
					}
					if e := f.store.PutShip(sh); e != nil {
						t.Error(e)
					}
				case "broken-chain":
					p.Ships = 99999
				case "cyclic-chain":
					f.state.ships[f.factory].NextShip = f.factory
				case "invalid-ship":
					f.state.ships[f.factory].Number = 123
				}
				populationTotals(p)
				for _, r := range f.state.races {
					if r == nil {
						continue
					}
					if e := f.store.PutRace(r); e != nil {
						t.Error(e)
					}
				}
				if e := f.store.PutStar(f.state.stars[f.star]); e != nil {
					t.Error(e)
				}
				if e := f.store.PutPlanet(p); e != nil {
					t.Error(e)
				}
				if kind == "stale-planet" {
					q := planetEdit(p)
					q.Sectors[1].Resource++
					if e := f.store.PutPlanet(q); e != nil {
						t.Error(e)
					}
				}
				if kind == "stale-race" {
					q := *d
					q.Motto = "newer"
					if e := f.store.PutRace(&q); e != nil {
						t.Error(e)
					}
				}
				if kind == "stale-ship" {
					q := *f.state.ships[f.factory]
					q.Damage++
					if e := f.store.PutShip(&q); e != nil {
						t.Error(e)
					}
				}
				if kind == "stale-ap" {
					q := *f.state.stars[f.star]
					q.AP[0]--
					if e := f.store.PutStar(&q); e != nil {
						t.Error(e)
					}
				}
			})
			var shipWant db.ShipGroundAssault
			if kind == "hostile-unload" {
				shipWant = shipAssaultPrepared(t, f, false, 1)
			}
			before := assaultRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			r := &recordingResponder{}
			g := 0
			if kind == "governor" {
				g = 1
			}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, cmd, r); e != nil {
				t.Fatal(e)
			}
			valid := kind == "mutual" || kind == "repulse" || kind == "empty" || kind == "absorption"
			if kind == "hostile-unload" {
				// Newly authorized ship assault: retain full atomicity/effect
				// checks instead of the historical blanket refusal assertion.
				if !strings.Contains(r.text(), "Casualties:") {
					t.Fatal("approved ship assault did not execute", r.text())
				}
				assertShipAssaultActor(t, f, shipWant)
				if !reflect.DeepEqual(assaultRecords(t, f), shipAssaultExpectedRecords(t, before, shipWant)) {
					t.Fatal("partial newly enabled ship assault")
				}
			} else if valid {
				if reflect.DeepEqual(before, assaultRecords(t, f)) {
					t.Fatal("all combat rejected", r.text())
				}
				out := r.text()
				word := map[string]string{"mutual": "Mutual destruction", "repulse": "repulsed", "empty": "Unopposed occupation", "absorption": "VICTORY"}[kind]
				if !strings.Contains(out, word) {
					t.Fatal("wrong outcome", out)
				}
			} else if !reflect.DeepEqual(before, assaultRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || strings.Contains(r.text(), "Casualties") || strings.Contains(r.text(), "VICTORY") {
				t.Fatal("refusal mutated or emitted effects", r.text())
			}
		})
	}
}
func TestGroundAssaultActorFaults(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"assault.before", "assault.check:1", "assault.validated", "assault.write:1", "assault.write:2", "assault.write:3", "assault.write:4", "assault.news", "assault.telegram", "assault.before-commit", "assault.commit-error", "assault.after-commit", "fence"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", stage, panics), func(t *testing.T) {
				f := newGroundAssaultFixture(t, false)
				want := assaultPrepared(t, f, false)
				before := assaultRecords(t, f)
				actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
				r := &recordingResponder{}
				reached := false
				draws := 0
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					f.state.assaultRandom = func(d commandrules.AssaultDraw) (commandrules.AssaultDraw, error) { draws++; return highAssaultDraw(d) }
				})
				f.store.SetMetaFault(func(op string) error {
					if stage == "fence" && op == "assault.before" {
						reached = true
						f.store.SetWriteFence(true)
						return nil
					}
					if op == stage {
						reached = true
						if r.text() != "" {
							t.Error("premature output")
						}
						if panics {
							panic("private assault fault")
						}
						return errors.New("private assault fault")
					}
					return nil
				})
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "move 0,0 6 1000", r); e != nil {
					t.Fatal(e)
				}
				f.store.SetWriteFence(false)
				if !reached || draws != len(want.Draws) || f.state.Mode() != ModeHalted || strings.Contains(r.text(), "VICTORY") || strings.Contains(r.text(), "Casualties") {
					t.Fatal("failure/reroll/output", draws, r.text())
				}
				visible := stage == "assault.commit-error" || stage == "assault.after-commit"
				if visible {
					p, e := f.store.GetPlanet(f.star, f.planet)
					if e != nil || !reflect.DeepEqual(p, want.Proposed.Planet) {
						t.Fatal("complete committed planet", e)
					}
					news, _, e := f.store.ReadNewsFrom(model.NewsCombat, 0)
					has, te := f.store.HasTelegrams(2, 0)
					if e != nil || te != nil || len(news) != 1 || !has {
						t.Fatal("partial effects", e, te)
					}
				} else if !reflect.DeepEqual(before, assaultRecords(t, f)) {
					t.Fatal("aborted write persisted")
				}
				if stage == "assault.after-commit" {
					assertAssaultActorAndEffects(t, f, want)
				} else if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("unverified install")
				}
				disk := assaultRecords(t, f)
				if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || !reflect.DeepEqual(disk, assaultRecords(t, f)) {
					t.Fatal("stale halted flush", e)
				}
			})
		}
	}
}
func TestGroundAssaultActorLifecycle(t *testing.T) {
	t.Parallel()
	f := newGroundAssaultFixture(t, false)
	if e := f.state.Quiesce(ctxT(t, 10*time.Second)); e != nil {
		t.Fatal(e)
	}
	quiesced := assaultRecords(t, f)
	r := &recordingResponder{}
	_ = f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "move 0,0 6 1000", r)
	if !reflect.DeepEqual(quiesced, assaultRecords(t, f)) {
		t.Fatal("quiesced mutation")
	}
	if e := f.state.Resume(ctxT(t, 10*time.Second)); e != nil {
		t.Fatal(e)
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	e := f.state.HandleCommand(cancelCtx, 1, 0, "move 0,0 6 1000", r)
	if !errors.Is(e, ErrNotExecuted) {
		t.Fatal("queued cancellation", e)
	}
}

type assaultDeliveryRegistry struct {
	fakeRegistry
	check  func()
	panics bool
}

func (r *assaultDeliveryRegistry) Notify(_, _ int, _ string) bool {
	if r.check != nil {
		r.check()
	}
	if r.panics {
		panic("private delivery panic")
	}
	return false
}
func TestGroundAssaultInstallReadbackAndDelivery(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"install panic", "planet identity", "attacker identity", "defender identity", "star identity", "scope", "after install panic", "readback", "response error", "response panic", "notification unavailable", "notification panic", "rng error"} {
		t.Run(kind, func(t *testing.T) {
			f := newGroundAssaultFixture(t, false)
			want := assaultPrepared(t, f, false)
			before := assaultRecords(t, f)
			r := &recordingResponder{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				f.state.testHooks.beforeCommandInstall = func() {
					switch kind {
					case "install panic":
						panic("private install panic")
					case "planet identity":
						p := *f.state.planets[f.star][f.planet]
						f.state.planets[f.star][f.planet] = &p
					case "attacker identity":
						a := *f.state.races[0]
						f.state.races[0] = &a
					case "defender identity":
						d := *f.state.races[1]
						f.state.races[1] = &d
					case "star identity":
						st := *f.state.stars[f.star]
						f.state.stars[f.star] = &st
					case "scope":
						f.state.dirs[0][0].PlanetNum++
					}
				}
				if kind == "after install panic" {
					f.state.testHooks.afterCommandInstall = func() { panic("private postinstall panic") }
				}
				if strings.HasPrefix(kind, "notification") {
					f.state.SetRegistry(&assaultDeliveryRegistry{panics: kind == "notification panic", check: func() {
						if f.state.planets[f.star][f.planet].Sectors[1].Owner != 1 {
							t.Error("notification before installation")
						}
						news, _, e := f.store.ReadNewsFrom(model.NewsCombat, 0)
						has, te := f.store.HasTelegrams(2, 0)
						if e != nil || te != nil || len(news) != 1 || !has {
							t.Error("notification before persistence")
						}
					}})
				}
				if kind == "rng error" {
					f.state.assaultRandom = func(d commandrules.AssaultDraw) (commandrules.AssaultDraw, error) {
						return d, errors.New("private RNG error")
					}
				}
			})
			if kind == "readback" {
				f.store.SetMetaFault(func(op string) error {
					if op == "assault.commit-error" || op == "command.readback" {
						return errors.New("private readback")
					}
					return nil
				})
			}
			if strings.HasPrefix(kind, "response") {
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "move 0,0 6 1000", &failingFactoryResponder{panics: kind == "response panic"}); e != nil {
					t.Fatal(e)
				}
			} else {
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "move 0,0 6 1000", r); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "rng error" {
				if !reflect.DeepEqual(before, assaultRecords(t, f)) || f.state.Mode() != ModeServing {
					t.Fatal("RNG preparation changed state")
				}
				return
			}
			installed := strings.HasPrefix(kind, "response") || strings.HasPrefix(kind, "notification") || kind == "after install panic"
			if installed {
				assertAssaultActorAndEffects(t, f, want)
			} else {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					if f.state.planets[f.star][f.planet].Sectors[1].Owner != 2 || f.state.races[0].Morale != 0 || f.state.stars[f.star].AP[0] != 100 {
						t.Error("partial install")
					}
				})
			}
			news, _, e := f.store.ReadNewsFrom(model.NewsCombat, 0)
			has, te := f.store.HasTelegrams(2, 0)
			if e != nil || te != nil || len(news) != 1 || !has {
				t.Fatal("committed effect lost/duplicated", e, te)
			}
			if kind == "notification unavailable" {
				if f.state.Mode() != ModeServing || !strings.Contains(r.text(), "VICTORY") {
					t.Fatal("best effort failure lost confirmed result")
				}
				return
			}
			if f.state.Mode() != ModeHalted || strings.Contains(f.log.text(), "private") {
				t.Fatal("failure did not halt safely")
			}
			disk := assaultRecords(t, f)
			if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || !reflect.DeepEqual(disk, assaultRecords(t, f)) {
				t.Fatal("stale shutdown flush", e)
			}
		})
	}
}
func TestGroundAssaultSerializationAndDraining(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"second assault", "quiesce", "shutdown", "cancel started", "cancel queued"} {
		t.Run(action, func(t *testing.T) {
			f := newGroundAssaultFixture(t, false)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				p := f.state.planets[f.star][f.planet]
				p.Sectors[int(p.MaxX)-1] = p.Sectors[1]
				populationTotals(p)
				if e := f.store.PutPlanet(p); e != nil {
					t.Error(e)
				}
			})
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			f.store.SetMetaFault(func(op string) error {
				if op == "assault.write:1" {
					select {
					case <-entered:
					default:
						close(entered)
						<-release
					}
				}
				return nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := &recordingResponder{}
			done := make(chan error, 1)
			go func() { done <- f.state.HandleCommand(ctx, 1, 0, "move 0,0 6 100", r) }()
			waitAcceptance(t, entered, "assault transaction")
			if r.text() != "" {
				t.Fatal("early output")
			}
			p, e := f.store.GetPlanet(f.star, f.planet)
			if e != nil || p.Sectors[1].Owner != 2 {
				t.Fatal("uncommitted record visible", e)
			}
			next := make(chan error, 1)
			switch action {
			case "second assault":
				go func() {
					next <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "move 0,0 4 100", &recordingResponder{})
				}()
			case "quiesce":
				go func() { next <- f.state.Quiesce(ctxT(t, 10*time.Second)) }()
			case "shutdown":
				go func() { next <- f.state.Stop(ctxT(t, 10*time.Second)) }()
			case "cancel started":
				cancel()
			case "cancel queued":
				queued, cancelQueued := context.WithCancel(context.Background())
				defer cancelQueued()
				go func() { next <- f.state.HandleCommand(queued, 1, 0, "move 0,0 4 100", &recordingResponder{}) }()
				deadline := time.Now().Add(5 * time.Second)
				for len(f.state.cmdCh) == 0 {
					if time.Now().After(deadline) {
						t.Fatal("second request did not queue")
					}
					runtime.Gosched()
				}
				cancelQueued()
				if e := <-next; !errors.Is(e, ErrNotExecuted) {
					t.Fatal("queued request ran", e)
				}
			}
			unblock()
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			if action == "second assault" || action == "quiesce" || action == "shutdown" {
				if e := <-next; e != nil {
					t.Fatal(e)
				}
			}
			p, e = f.store.GetPlanet(f.star, f.planet)
			if e != nil || p.Sectors[1].Owner != 1 {
				t.Fatal("admitted assault lost", e)
			}
			count := 1
			if action == "second assault" {
				count = 2
				if p.Sectors[int(p.MaxX)-1].Owner != 1 {
					t.Fatal("serialized second assault missing")
				}
			}
			news, _, e := f.store.ReadNewsFrom(model.NewsCombat, 0)
			if e != nil || len(news) != count {
				t.Fatal("lost/duplicate news", len(news), e)
			}
			if action == "quiesce" {
				before := assaultRecords(t, f)
				_ = f.command(t, "move 0,0 4 100")
				if !reflect.DeepEqual(before, assaultRecords(t, f)) {
					t.Fatal("quiesced write")
				}
				f.restart(t)
				if f.state.Mode() != ModeQuiesced {
					t.Fatal("maintenance not durable")
				}
			}
		})
	}
}

func TestGroundAssaultAliasesDefaultAndGovernor(t *testing.T) {
	t.Parallel()
	for _, direction := range []byte("12346789bknhlyju") {
		t.Run(string(direction), func(t *testing.T) {
			f := newGroundAssaultFixture(t, false)
			originY := 0
			if strings.ContainsRune("789yju", rune(direction)) {
				originY = 1
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				p := f.state.planets[f.star][f.planet]
				from, to := p.Sectors[0], p.Sectors[1]
				for i := range p.Sectors {
					p.Sectors[i].Owner = 0
					p.Sectors[i].Popn = 0
					p.Sectors[i].Troops = 0
				}
				*p.SectorAt(0, originY) = from
				x, y, ok := getMoveDir(direction, 0, originY, p)
				if !ok {
					t.Error("alias invalid")
				}
				*p.SectorAt(x, y) = to
				populationTotals(p)
				f.state.races[0].Governor[1].Active = true
				f.state.stars[f.star].Governor[0] = 1
				f.state.dirs[0][1] = f.state.dirs[0][0]
				for _, r := range []*model.Race{f.state.races[0]} {
					if e := f.store.PutRace(r); e != nil {
						t.Error(e)
					}
				}
				if e := f.store.PutStar(f.state.stars[f.star]); e != nil {
					t.Error(e)
				}
				if e := f.store.PutPlanet(p); e != nil {
					t.Error(e)
				}
			})
			out := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 1, fmt.Sprintf("move 0,%d %c", originY, direction), out); e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(out.text(), "VICTORY") {
				t.Fatal("valid positive default/alias/governor rejected", out.text())
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				p := f.state.planets[f.star][f.planet]
				x, y, _ := getMoveDir(direction, 0, originY, p)
				target := p.SectorAt(x, y)
				if target.Owner != 1 || target.Popn != 1000 || p.SectorAt(0, originY).Owner != 0 || p.Popn != 1000 || f.state.stars[f.star].AP[0] != 92 {
					t.Error("C defined alias/default result")
				}
			})
		})
	}
}

func TestGroundAssaultPreservesLegacyReporting(t *testing.T) {
	t.Parallel()
	f := newGroundAssaultFixture(t, false)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.groundAssaults[0][1][f.star] = 3 })
	out := f.command(t, "move 0,0 6 1000")
	if !strings.Contains(out, "VICTORY") {
		t.Fatal(out)
	}
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		if f.state.groundAssaults[0][1][f.star] != 3 {
			t.Error("new assault modified legacy reporting")
		}
	})
	if e := f.state.TriggerSegment(ctxT(t, 10*time.Second)); e != nil {
		t.Fatal(e)
	}
	lines, _, e := f.store.ReadNewsFrom(model.NewsCombat, 0)
	if e != nil || len(lines) != 2 {
		t.Fatal("legacy/new report duplicated or lost", len(lines), e)
	}
}

func TestGroundAssaultAlliedAFVIsImmutableWitness(t *testing.T) {
	t.Parallel()
	f := newGroundAssaultFixture(t, false)
	var before *model.Ship
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		a, d := f.state.races[0], f.state.races[1]
		a.Allied = 2
		d.Allied = 1
		sh := f.state.ships[f.factory]
		sh.Type = model.OtypeAFV
		sh.Owner = 2
		sh.LandX = 1
		sh.LandY = 0
		sh.Primary = 10
		sh.Guns = model.GunPrimary
		sh.Retaliate = 10
		sh.Destruct = 10
		before = factoryCopyShip(t, sh)
		for _, r := range []*model.Race{a, d} {
			if e := f.store.PutRace(r); e != nil {
				t.Error(e)
			}
		}
		if e := f.store.PutShip(sh); e != nil {
			t.Error(e)
		}
	})
	out := f.command(t, "move 0,0 6 1000")
	if !strings.Contains(out, "VICTORY") {
		t.Fatal("mutually allied AFV incorrectly treated as mechanical combat", out)
	}
	got, e := f.store.GetShip(f.factory)
	if e != nil || !reflect.DeepEqual(got, before) {
		t.Fatal("read-only allied AFV rewritten", e)
	}
}
