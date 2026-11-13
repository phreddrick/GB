package game

import (
	"context"
	"errors"
	"fmt"
	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func newShipAssaultFixture(t *testing.T, military bool) *factoryFixture {
	t.Helper()
	f := newGroundAssaultFixture(t, false)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		// Private fixture clock keeps independently prepared durable messages
		// stable across an intervening wall-clock second under the race suite.
		now := f.state.now()
		f.state.now = func() time.Time { return now }
		sh := f.state.ships[77]
		sh.Type = model.StypeCargo
		sh.LandX = 1
		sh.LandY = 0
		sh.MaxCrew = 400
		sh.Popn = 200
		sh.Troops = 0
		sh.Armor = 1
		sh.Damage = 0
		sh.Mass = 700
		if military {
			sh.Popn = 0
			sh.Troops = 200
		}
		f.state.races[0].Mass = 2
		persistShipAssault(t, f)
	})
	return f
}
func persistShipAssault(t *testing.T, f *factoryFixture) {
	t.Helper()
	for _, r := range f.state.races {
		if r != nil {
			if e := f.store.PutRace(r); e != nil {
				t.Error(e)
			}
		}
	}
	for _, sh := range f.state.ships {
		if e := f.store.PutShip(sh); e != nil {
			t.Error(e)
		}
	}
	if e := f.store.PutPlanet(f.state.planets[f.star][f.planet]); e != nil {
		t.Error(e)
	}
	if e := f.store.PutStar(f.state.stars[f.star]); e != nil {
		t.Error(e)
	}
}
func shipAssaultPrepared(t *testing.T, f *factoryFixture, military bool, amount int64) db.ShipGroundAssault {
	t.Helper()
	var in db.ShipGroundAssault
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		c := byte('c')
		if military {
			c = 'm'
		}
		_, v, validation, e := f.state.shipAssaultStage(1, 0, 77, commandrules.PopulationRequest{Commodity: c, Unload: true, Amount: amount})
		if e != nil || validation != "" {
			t.Error(validation, e)
		}
		in = v
	})
	return in
}
func shipAssaultCommand(military bool, amount string) string {
	c := "c"
	if military {
		c = "m"
	}
	return "unload #77 " + c + " " + amount
}
func assertShipAssaultActor(t *testing.T, f *factoryFixture, in db.ShipGroundAssault) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		if !reflect.DeepEqual(f.state.ships[77], in.Proposed.Ship) || !reflect.DeepEqual(f.state.planets[f.star][f.planet], in.Proposed.Planet) || !reflect.DeepEqual(f.state.races[0], in.Proposed.Attacker) || !reflect.DeepEqual(f.state.races[1], in.Proposed.Defender) || !reflect.DeepEqual(f.state.stars[f.star], in.Input.Star) {
			t.Error("complete actor assault proposal differs")
		}
		if f.state.groundAssaults[0][1][f.star] != 0 {
			t.Error("duplicate volatile reporting")
		}
	})
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}
func TestShipAssaultGameplayAndMass(t *testing.T) {
	t.Parallel()
	for _, mil := range []bool{false, true} {
		for _, scenario := range []string{"victory", "repulse-no-loss", "partial-retreat", "mutual", "total-loss", "occupation", "attacker-absorb", "defender-absorb", "absorb-capacity", "default", "zero-default", "zero-AP"} {
			t.Run(fmt.Sprintf("military=%t/%s", mil, scenario), func(t *testing.T) {
				f := newShipAssaultFixture(t, mil)
				amount := "100"
				n := int64(100)
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					sh := f.state.ships[77]
					p := f.state.planets[f.star][f.planet]
					a, d := f.state.races[0], f.state.races[1]
					sec := &p.Sectors[1]
					switch scenario {
					case "repulse-no-loss", "partial-retreat", "defender-absorb":
						a.Fighters = 1
						d.Fighters = 1
						sec.Popn = 100
						sec.Troops = 0
						if mil {
							sec.Popn = 0
							sec.Troops = 100
						}
						f.state.assaultRandom = func(v commandrules.AssaultDraw) (commandrules.AssaultDraw, error) {
							if v.Kind == "round" {
								v.Fraction = 0.999999
							} else if scenario != "repulse-no-loss" {
								v.Integer = v.Bound / 2
							}
							return v, nil
						}
						if scenario == "repulse-no-loss" {
							sh.Popn = 100
							sh.Troops = 0
							if mil {
								sh.Popn = 0
								sh.Troops = 100
							}
							sh.Mass = 210
						}
						if scenario == "defender-absorb" {
							d.Absorb = true
						}
					case "mutual":
						a.Fighters = 1
						d.Fighters = 1
						sh.Popn = 1
						sh.Troops = 0
						sh.Mass = 302
						sec.Popn = 1
						sec.Troops = 0
						if mil {
							sh.Popn = 0
							sh.Troops = 1
							sec.Popn = 0
							sec.Troops = 1
						}
						amount = "1"
						n = 1
						a.Absorb = true
						d.Absorb = true
					case "total-loss":
						a.Fighters = 1
						d.Fighters = 255
						sec.Popn = 10000
					case "occupation":
						sec.Popn = 0
						sec.Troops = 0
					case "attacker-absorb":
						a.Absorb = true
					case "absorb-capacity":
						a.Absorb = true
						sh.Popn = 65535
						sh.Troops = 0
						sh.MaxCrew = 65535
						sh.Mass = 200000
						amount = "65535"
						n = 65535
						if mil {
							sh.Popn = 0
							sh.Troops = 65535
						}
					case "default":
						amount = ""
						n = 0
					case "zero-default":
						amount = "0"
						n = 0
					case "zero-AP":
						f.state.stars[f.star].AP[0] = 0
					}
					populationTotals(p)
					persistShipAssault(t, f)
				})
				want := shipAssaultPrepared(t, f, mil, n)
				before := assaultRecords(t, f)
				var ship *model.Ship
				var sector *model.Sector
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { ship = f.state.ships[77]; sector = &f.state.planets[f.star][f.planet].Sectors[1] })
				out := f.command(t, shipAssaultCommand(mil, amount))
				if !strings.Contains(out, "Casualties:") {
					t.Fatal("assault never executed", out)
				}
				assertShipAssaultActor(t, f, want)
				if !reflect.DeepEqual(assaultRecords(t, f), shipAssaultExpectedRecords(t, before, want)) {
					t.Fatal("incomplete or unrelated durable mutation")
				}
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					if ship != f.state.ships[77] || sector != &f.state.planets[f.star][f.planet].Sectors[1] {
						t.Error("actor pointer identity changed")
					}
				})
				if scenario == "repulse-no-loss" && (want.Proposed.Ship.Mass != 210 || (mil && want.Proposed.Ship.Troops != 100) || (!mil && want.Proposed.Ship.Popn != 100)) {
					t.Fatal("C duplicate mass subtraction regression")
				}
				if scenario == "partial-retreat" && want.Proposed.Ship.Mass != 600 {
					t.Fatal("partial survivor refund", want.Proposed.Ship.Mass)
				}
				if scenario == "mutual" && (want.Proposed.Planet.Sectors[1].Owner != 0 || want.Proposed.Absorbed != 0) {
					t.Fatal("mutual resurrection/capture")
				}
			})
		}
	}
}

func TestShipAssaultRefusals(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"negative", "hostile-load", "empty-default", "overflow", "malformed", "mixed", "wildcard", "owner", "governor", "inactive", "dead", "undocked", "landing", "root-cycle", "wrong-destination", "transporter", "mechanical", "mechanical-peer", "bad-totals", "negative-mass", "negative-race-mass"} {
		t.Run(scenario, func(t *testing.T) {
			f := newShipAssaultFixture(t, false)
			command := "unload #77 c 100"
			g := 0
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				s := f.state.ships[77]
				p := f.state.planets[f.star][f.planet]
				switch scenario {
				case "negative":
					command = "unload #77 c -1"
				case "hostile-load":
					command = "load #77 c 1"
				case "overflow":
					command = "unload #4294967373 c 1"
				case "malformed":
					command = "unload #77 c 1x"
				case "mixed":
					command = "unload #77,78 c 1"
				case "wildcard":
					command = "unload * c 1"
				case "empty-default":
					s.Popn = 0
					command = "unload #77 c"
				case "owner":
					s.Owner = 2
				case "governor":
					g = 1
					f.state.races[0].Governor[1].Active = true
					f.state.dirs[0][1] = f.state.dirs[0][0]
					s.Governor = 2
				case "inactive":
					s.Active = false
				case "dead":
					s.Alive = false
				case "undocked":
					s.Docked = false
				case "landing":
					s.LandX = p.MaxX
				case "root-cycle":
					s.NextShip = s.Number
				case "wrong-destination":
					s.DestPNum++
				case "transporter":
					s.Type = model.OtypeTransDev
					s.On = true
					s.Special.Transport = &model.TransportSpecial{Target: 1}
				case "mechanical", "mechanical-peer":
					m := *s
					m.Number = 78
					m.Owner = 2
					m.Type = model.OtypeAFV
					m.NextShip = p.Ships
					m.Primary = 2
					m.Guns = model.GunPrimary
					m.Destruct = 100
					m.Retaliate = 2
					m.Popn = 1
					if scenario == "mechanical-peer" {
						m.WhatDest = model.LevelShip
						m.DestShipNo = 79
					}
					f.state.ships[78] = &m
					p.Ships = 78
				case "bad-totals":
					p.Popn++
				case "negative-mass":
					s.Mass = 1
				case "negative-race-mass":
					f.state.races[0].Mass = -1
				}
				persistShipAssault(t, f)
			})
			before := assaultRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			r := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, command, r); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(before, assaultRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || strings.Contains(r.text(), "Casualties:") || strings.Contains(r.text(), "VICTORY") {
				t.Fatal("refusal mutated or emitted success", r.text())
			}
		})
	}
}
func TestShipAssaultActorFaults(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"ship-assault.before", "ship-assault.check:1", "ship-assault.validated", "ship-assault.write:1", "ship-assault.write:2", "ship-assault.write:3", "ship-assault.write:4", "assault.news", "assault.telegram", "ship-assault.before-commit", "ship-assault.commit-error", "ship-assault.after-commit", "fence"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", stage, panics), func(t *testing.T) {
				f := newShipAssaultFixture(t, false)
				want := shipAssaultPrepared(t, f, false, 100)
				before := assaultRecords(t, f)
				actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
				r := &recordingResponder{}
				reached := false
				draws := 0
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					f.state.assaultRandom = func(d commandrules.AssaultDraw) (commandrules.AssaultDraw, error) { draws++; return highAssaultDraw(d) }
				})
				f.store.SetMetaFault(func(op string) error {
					if stage == "fence" && op == "ship-assault.before" {
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
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, shipAssaultCommand(false, "100"), r); e != nil {
					t.Fatal(e)
				}
				f.store.SetWriteFence(false)
				if !reached || draws != len(want.Draws) || f.state.Mode() != ModeHalted || strings.Contains(r.text(), "VICTORY") || strings.Contains(r.text(), "Casualties") {
					t.Fatal("failure/reroll/output", draws, r.text())
				}
				visible := stage == "ship-assault.commit-error" || stage == "ship-assault.after-commit"
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
				if stage == "ship-assault.after-commit" {
					assertShipAssaultActor(t, f, want)
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
func TestShipAssaultInstallReadbackAndDelivery(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"install panic", "planet identity", "ship identity", "attacker identity", "defender identity", "star identity", "scope", "after install panic", "readback", "response error", "response panic", "notification unavailable", "notification panic", "rng error"} {
		t.Run(kind, func(t *testing.T) {
			f := newShipAssaultFixture(t, false)
			want := shipAssaultPrepared(t, f, false, 100)
			before := assaultRecords(t, f)
			r := &recordingResponder{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				f.state.testHooks.beforeCommandInstall = func() {
					switch kind {
					case "install panic":
						panic("private install panic")
					case "ship identity":
						sh := *f.state.ships[77]
						f.state.ships[77] = &sh
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
					if op == "ship-assault.commit-error" || op == "command.readback" {
						return errors.New("private readback")
					}
					return nil
				})
			}
			if strings.HasPrefix(kind, "response") {
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, shipAssaultCommand(false, "100"), &failingFactoryResponder{panics: kind == "response panic"}); e != nil {
					t.Fatal(e)
				}
			} else {
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, shipAssaultCommand(false, "100"), r); e != nil {
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
				assertShipAssaultActor(t, f, want)
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
func TestShipAssaultSerializationAndDraining(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"second assault", "quiesce", "shutdown", "cancel started", "cancel queued"} {
		t.Run(action, func(t *testing.T) {
			f := newShipAssaultFixture(t, false)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				p := f.state.planets[f.star][f.planet]
				p.Sectors[int(p.MaxX)-1] = p.Sectors[1]
				sh := *f.state.ships[77]
				sh.Number = 78
				sh.LandX = p.MaxX - 1
				sh.NextShip = p.Ships
				f.state.ships[78] = &sh
				p.Ships = 78
				if e := f.store.PutShip(&sh); e != nil {
					t.Error(e)
				}
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
				if op == "ship-assault.write:1" {
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
			go func() { done <- f.state.HandleCommand(ctx, 1, 0, shipAssaultCommand(false, "100"), r) }()
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
					next <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "unload #78 c 100", &recordingResponder{})
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
				go func() { next <- f.state.HandleCommand(queued, 1, 0, "unload #78 c 100", &recordingResponder{}) }()
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
				_ = f.command(t, "unload #78 c 100")
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
