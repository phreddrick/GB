package game

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/model"
)

func cargoPeer(t *testing.T, f *cargoFixture, level uint8) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		a, b := f.state.ships[f.factory], f.state.ships[f.partner]
		pl := f.state.planets[f.star][f.planet]
		b.Ships = 0
		b.Hanger = 0
		b.Docked = true
		b.WhatDest = model.LevelShip
		b.DestShipNo = a.Number
		a.WhatOrbits = level
		b.WhatOrbits = level
		a.NextShip = f.state.races[0].GovShip
		b.NextShip = a.Number
		if level != model.LevelPlan {
			pl.Ships = f.state.races[0].GovShip
			if level == model.LevelUniv {
				a.NextShip = f.state.sdata.Ships
				f.state.sdata.Ships = b.Number
			} else {
				a.NextShip = f.state.stars[f.star].Ships
				f.state.stars[f.star].Ships = b.Number
			}
		}
		persistCargo(t, f)
	})
}

// Fixture preparation only; never used to execute the command under test.
func persistCargo(t *testing.T, f *cargoFixture) {
	t.Helper()
	for _, n := range []uint32{f.factory, f.partner} {
		if n != 0 && f.state.ships[n] != nil {
			if e := f.store.PutShip(f.state.ships[n]); e != nil {
				t.Error(e)
			}
		}
	}
	if p := f.state.planets[f.star][f.planet]; p != nil {
		if e := f.store.PutPlanet(p); e != nil {
			t.Error(e)
		}
	}
	if e := f.store.PutStar(f.state.stars[f.star]); e != nil {
		t.Error(e)
	}
	if e := f.store.PutStarData(f.state.sdata); e != nil {
		t.Error(e)
	}
	if e := f.store.PutRace(f.state.races[0]); e != nil {
		t.Error(e)
	}
}
func cargoStoredWorld(t *testing.T, f *cargoFixture) telescopeAllWorld {
	t.Helper()
	return telescopeStoredWorld(t, &telescopeFixture{factoryFixture: f.factoryFixture})
}
func TestCargoGameplayMatrix(t *testing.T) {
	for _, location := range []string{"planet", "contained", "peer"} {
		for _, commodity := range []string{"r", "x", "&"} {
			for _, verb := range []string{"load", "unload"} {
				for _, amount := range []string{"", "0", "2", "-2"} {
					t.Run(location+"/"+commodity+"/"+verb+"/amount="+amount, func(t *testing.T) {
						f := newCargoFixture(t, location != "planet")
						if location == "peer" {
							cargoPeer(t, f, model.LevelPlan)
						}
						onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
							f.state.ships[f.factory].Crystals = 10
							f.state.ships[f.factory].Mount = 0
							f.state.stars[f.star].AP[0] = 0
							f.state.sdata.AP[0] = 0
							persistCargo(t, f)
						})
						var sourcePtr, otherPtr *model.Ship
						var planetPtr *model.Planet
						onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
							sourcePtr = f.state.ships[f.factory]
							if f.partner != 0 {
								otherPtr = f.state.ships[f.partner]
							}
							planetPtr = f.state.planets[f.star][f.planet]
						})
						want := cargoStoredWorld(t, f)
						delta := int64(2)
						if amount == "-2" {
							delta = -2
						}
						if verb == "unload" {
							delta = -delta
						}
						if amount == "" || amount == "0" {
							if verb == "unload" {
								delta = -10
							} else if location != "planet" {
								if commodity == "r" {
									delta = 30
								} else {
									delta = 10
								}
							} else if commodity == "r" {
								delta = 40
							} else {
								delta = 20
							}
						}
						src := want.Ships[f.factory]
						if commodity == "r" {
							src.Resource = cargoTestWord(t, 10+delta)
							src.Mass += float64(delta) * f.cfg.MassResource
						} else {
							src.Crystals = cargoTestWord(t, 10+delta)
						}
						if location == "planet" {
							p := want.Planets[f.star][f.planet]
							if commodity == "r" {
								p.Info[0].Resource = cargoTestWord(t, 100-delta)
							} else {
								p.Info[0].Crystals = cargoTestWord(t, 20-delta)
							}
						} else {
							o := want.Ships[f.partner]
							if commodity == "r" {
								o.Resource = cargoTestWord(t, 30-delta)
								if location == "peer" {
									o.Mass -= float64(delta) * f.cfg.MassResource
								}
							} else {
								o.Crystals = cargoTestWord(t, 10-delta)
							}
						}
						command := fmt.Sprintf("%s #%d %s", verb, f.factory, commodity)
						if amount != "" {
							command += " " + amount
						}
						out := f.command(t, command)
						if !strings.Contains(out, fmt.Sprintf("%d ", delta)) || !strings.Contains(out, "transferred.") || f.state.Mode() != ModeServing {
							t.Fatal("gameplay result", out)
						}
						if !reflect.DeepEqual(cargoStoredWorld(t, f), want) {
							t.Fatal("complete world differs; cargo/mass/AP/unrelated fields")
						}
						onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
							if f.state.ships[f.factory] != sourcePtr || f.state.planets[f.star][f.planet] != planetPtr || (f.partner != 0 && f.state.ships[f.partner] != otherPtr) {
								t.Error("actor pointer changed")
							}
						})
						assertActorStoreAgreementCargo(t, f)
					})
				}
			}
		}
	}
}
func TestCargoForeignOwnershipAndUniverse(t *testing.T) {
	for _, scenario := range []string{"resource gift", "negative resource gift", "forbidden resource load", "forbidden reverse load", "crystal load", "crystal unload", "foreign zero", "unauthorized selected", "governor", "universe", "star"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCargoFixture(t, true)
			cargoPeer(t, f, model.LevelPlan)
			if scenario != "universe" && scenario != "star" {
				enrollActor(t, f.actorEnrollmentFixture, actorEnrollmentRequest("account-2"))
			}
			verb, commodity, amount := "unload", "r", "2"
			g := 0
			success := false
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				a, b := f.state.ships[f.factory], f.state.ships[f.partner]
				b.Owner = 2
				b.Race = 2
				switch scenario {
				case "resource gift":
					success = true
				case "negative resource gift":
					verb = "load"
					amount = "-2"
					success = true
				case "forbidden resource load":
					verb = "load"
				case "forbidden reverse load":
					amount = "-2"
				case "crystal load":
					verb = "load"
					commodity = "x"
					amount = "1"
				case "crystal unload":
					commodity = "&"
					amount = "1"
				case "foreign zero":
					verb = "load"
					commodity = "x"
					amount = "0"
					success = true
				case "unauthorized selected":
					a.Owner = 2
					a.Race = 2
					b.Owner = 1
					b.Race = 1
				case "governor":
					g = 1
					a.Governor = 2
					f.state.races[0].Governor[1].Active = true
					f.state.dirs[0][1] = f.state.dirs[0][0]
				case "universe", "star":
					b.Owner = 1
					b.Race = 1
					success = true
				}
				persistCargo(t, f)
			})
			if scenario == "universe" {
				cargoPeer(t, f, model.LevelUniv)
			}
			if scenario == "star" {
				cargoPeer(t, f, model.LevelStar)
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					// Planet numbers are irrelevant to legitimate same-star peers.
					f.state.ships[f.factory].PNumOrbits = 1
					f.state.ships[f.partner].PNumOrbits = 0
					persistCargo(t, f)
				})
			}
			before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			r := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, fmt.Sprintf("%s #%d %s %s", verb, f.factory, commodity, amount), r); e != nil {
				t.Fatal(e)
			}
			if success {
				if !strings.Contains(r.text(), "transferred.") {
					t.Fatal("valid transfer refused", r.text())
				}
				if scenario != "foreign zero" {
					a, e := f.store.GetShip(f.factory)
					if e != nil {
						t.Fatal(e)
					}
					b, e := f.store.GetShip(f.partner)
					if e != nil || a.Resource != 8 || b.Resource != 32 {
						t.Fatal("wrong gift/recipient", e)
					}
				}
				assertActorStoreAgreementCargo(t, f)
			} else if strings.Contains(r.text(), "transferred.") || before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("unauthorized mutation", r.text())
			}
		})
	}
}
func TestCargoInvalidSelectorsAndDeferredPaths(t *testing.T) {
	for _, command := range []string{"load * r 1", "load C r 1", "load #4294967373 r 1", "load #77junk r 1", "load #0 x 1", "load #77 r 2junk", "load #77 r 2147483648", "unload #77 r -2147483648", "load #77 r 1 extra", "load #77 resources 1"} {
		t.Run(command, func(t *testing.T) {
			f := newCargoFixture(t, false)
			before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			out := f.command(t, command)
			if strings.Contains(out, "transferred.") || before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("invalid selector escaped staging", out)
			}
		})
	}
	for _, bad := range []string{"transporter", "dead partner", "missing partner", "broken reciprocal", "bad map identity", "cycle", "stored planet", "stored source", "stored star", "stored caller"} {
		t.Run(bad, func(t *testing.T) {
			f := newCargoFixture(t, true)
			cargoPeer(t, f, model.LevelPlan)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				a, b := f.state.ships[f.factory], f.state.ships[f.partner]
				switch bad {
				case "transporter":
					a.Type = model.OtypeTransDev
					a.On = true
					a.Special.Transport = &model.TransportSpecial{Target: 1}
				case "dead partner":
					b.Alive = false
				case "missing partner":
					delete(f.state.ships, f.partner)
				case "broken reciprocal":
					b.DestShipNo = 999
				case "bad map identity":
					a.Number = 78
					f.state.ships[78] = a
				case "cycle":
					b.NextShip = b.Number
				}
				if !strings.HasPrefix(bad, "stored ") {
					persistCargo(t, f)
				}
			})
			if strings.HasPrefix(bad, "stored ") {
				switch bad {
				case "stored planet":
					p, e := f.store.GetPlanet(f.star, f.planet)
					if e != nil {
						t.Fatal(e)
					}
					p.Info[1].Resource++
					if e := f.store.PutPlanet(p); e != nil {
						t.Fatal(e)
					}
				case "stored source":
					a, e := f.store.GetShip(f.factory)
					if e != nil {
						t.Fatal(e)
					}
					a.Fuel++
					if e := f.store.PutShip(a); e != nil {
						t.Fatal(e)
					}
				case "stored star":
					a, e := f.store.GetStar(f.star)
					if e != nil {
						t.Fatal(e)
					}
					a.AP[0]--
					if e := f.store.PutStar(a); e != nil {
						t.Fatal(e)
					}
				case "stored caller":
					a, e := f.store.GetRace(1)
					if e != nil {
						t.Fatal(e)
					}
					a.Morale++
					if e := f.store.PutRace(a); e != nil {
						t.Fatal(e)
					}
				}
			}
			before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			out := f.command(t, "load #77 r 1")
			if strings.Contains(out, "transferred.") || before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("invalid context wrote/undocked/transporter", out)
			}
		})
	}
}
func TestCargoActorFailureOutcomes(t *testing.T) {
	for _, ships := range []bool{false, true} {
		for _, stage := range []string{"cargo.before", "cargo.check:1", "cargo.validated", "cargo.write:1", "cargo.write:2", "cargo.before-commit", "cargo.commit-error", "cargo.after-commit", "fence"} {
			for _, panics := range []bool{false, true} {
				t.Run(fmt.Sprintf("ships=%t/%s/panic=%t", ships, stage, panics), func(t *testing.T) {
					f := newCargoFixture(t, ships)
					before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
					actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
					r := &recordingResponder{}
					reached := false
					f.store.SetMetaFault(func(op string) error {
						if stage == "fence" && op == "cargo.before" {
							reached = true
							f.store.SetWriteFence(true)
							return nil
						}
						if op == stage {
							reached = true
							if r.text() != "" {
								t.Error("output before persistence")
							}
							if panics {
								panic("private cargo fault")
							}
							return errors.New("private cargo fault")
						}
						return nil
					})
					if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "load #77 r 2", r); e != nil {
						t.Fatal(e)
					}
					f.store.SetWriteFence(false)
					if !reached || f.state.Mode() != ModeHalted || strings.Contains(r.text(), "transferred.") || strings.Contains(f.log.text(), "private cargo fault") {
						t.Fatal("fault did not fail closed", r.text())
					}
					visible := stage == "cargo.commit-error" || stage == "cargo.after-commit"
					if visible {
						s, e := f.store.GetShip(f.factory)
						if e != nil || s.Resource != 12 {
							t.Fatal("complete committed source missing", e)
						}
						if ships {
							o, e := f.store.GetShip(f.partner)
							if e != nil || o.Resource != 28 {
								t.Fatal("complete recipient missing", e)
							}
						} else {
							p, e := f.store.GetPlanet(f.star, f.planet)
							if e != nil || p.Info[0].Resource != 98 {
								t.Fatal("complete planet missing", e)
							}
						}
					} else if before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
						t.Fatal("partial abort")
					}
					if stage == "cargo.after-commit" {
						assertActorStoreAgreementCargo(t, f)
					} else if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
						t.Fatal("unverified proposal installed")
					}
					disk := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
					if e := f.state.TriggerUpdate(ctxT(t, time.Second)); !errors.Is(e, ErrNotServing) {
						t.Fatal("halted turn admitted", e)
					}
					if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
						t.Fatal("halted shutdown flushed", e)
					}
				})
			}
		}
	}
}
func TestCargoInstallationReadbackAndDelivery(t *testing.T) {
	for _, kind := range []string{"install panic", "replace source", "replace planet", "replace race", "scope changed", "readback failure", "response error", "response panic"} {
		t.Run(kind, func(t *testing.T) {
			f := newCargoFixture(t, false)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				f.state.testHooks.beforeCommandInstall = func() {
					switch kind {
					case "install panic":
						panic("private install")
					case "replace source":
						n := *f.state.ships[f.factory]
						f.state.ships[f.factory] = &n
					case "replace planet":
						n := *f.state.planets[f.star][f.planet]
						f.state.planets[f.star][f.planet] = &n
					case "replace race":
						n := *f.state.races[0]
						f.state.races[0] = &n
					case "scope changed":
						f.state.dirs[0][0].PlanetNum++
					}
				}
			})
			if kind == "readback failure" {
				f.store.SetMetaFault(func(op string) error {
					if op == "cargo.commit-error" || op == "command.readback" {
						return errors.New("private readback")
					}
					return nil
				})
			}
			if strings.HasPrefix(kind, "response") {
				r := &failingFactoryResponder{panics: kind == "response panic"}
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "load #77 r 2", r); e != nil {
					t.Fatal(e)
				}
			} else {
				out := f.command(t, "load #77 r 2")
				if strings.Contains(out, "transferred.") {
					t.Fatal("unsafe success")
				}
			}
			a, e := f.store.GetShip(f.factory)
			if e != nil || a.Resource != 12 {
				t.Fatal("durable source lost", e)
			}
			p, e := f.store.GetPlanet(f.star, f.planet)
			if e != nil || p.Info[0].Resource != 98 {
				t.Fatal("durable recipient lost", e)
			}
			if f.state.Mode() != ModeHalted || strings.Contains(f.log.text(), "private") {
				t.Fatal("unsafe failure/logging")
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				installed := strings.HasPrefix(kind, "response")
				if (f.state.ships[f.factory].Resource == 12) != installed || (f.state.planets[f.star][f.planet].Info[0].Resource == 98) != installed {
					t.Error("partial/incorrect install")
				}
			})
			disk := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("stale flush", e)
			}
		})
	}
}
func TestCargoAdmittedOperationAndModes(t *testing.T) {
	for _, action := range []string{"second transfer", "quiesce", "shutdown", "cancel started"} {
		t.Run(action, func(t *testing.T) {
			f := newCargoFixture(t, false)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			f.store.SetMetaFault(func(op string) error {
				if op == "cargo.write:1" {
					onceEntered := false
					select {
					case <-entered:
						onceEntered = true
					default:
					}
					if !onceEntered {
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
			go func() { done <- f.state.HandleCommand(ctx, 1, 0, "load #77 r 2", r) }()
			waitAcceptance(t, entered, "cargo write")
			if r.text() != "" {
				t.Fatal("early response")
			}
			old, e := f.store.GetShip(f.factory)
			if e != nil || old.Resource != 10 {
				t.Fatal("partial write visible", e)
			}
			next := make(chan error, 1)
			switch action {
			case "second transfer":
				go func() {
					next <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "load #77 r 2", &recordingResponder{})
				}()
			case "quiesce":
				go func() { next <- f.state.Quiesce(ctxT(t, 10*time.Second)) }()
			case "shutdown":
				go func() { next <- f.state.Stop(ctxT(t, 10*time.Second)) }()
			case "cancel started":
				cancel()
			}
			select {
			case e := <-done:
				t.Fatal("admitted request guessed outcome", e)
			default:
			}
			unblock()
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			if action != "cancel started" {
				if e := <-next; e != nil {
					t.Fatal(e)
				}
			}
			a, e := f.store.GetShip(f.factory)
			want := uint16(12)
			if action == "second transfer" {
				want = 14
			}
			if e != nil || a.Resource != want {
				t.Fatal("serialization/durability", e)
			}
			if action == "quiesce" {
				disk := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				out := f.command(t, "load #77 r 2")
				if strings.Contains(out, "transferred.") || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("quiesced command wrote")
				}
				f.restart(t)
				if f.state.Mode() != ModeQuiesced {
					t.Fatal("maintenance lost")
				}
			}
		})
	}
}

func assertCargoLinks(t *testing.T, f *cargoFixture) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		p := f.state.planets[f.star][f.planet]
		seen := map[uint32]bool{}
		for n := p.Ships; n != 0; {
			s := f.state.ships[n]
			if s == nil || seen[n] || s.WhatOrbits != model.LevelPlan {
				t.Error("bad planet chain")
				return
			}
			seen[n] = true
			n = s.NextShip
		}
		if f.partner == 0 {
			if !seen[f.factory] {
				t.Error("source missing from planet chain")
			}
		} else {
			c := f.state.ships[f.partner]
			if !seen[c.Number] || c.Ships != f.factory || f.state.ships[f.factory].DestShipNo != c.Number {
				t.Error("bad containing partner chain")
			}
		}
	})
}

func cargoTestWord(t *testing.T, n int64) uint16 {
	t.Helper()
	if n < 0 || n > 65535 {
		t.Fatal("test expected quantity out of range", n)
		return 0
	}
	return uint16(n)
}

// Existing stale-state regressions now exercise fuel, which intentionally
// remains legacy. Resource transfers must no longer create that disagreement.
func prepareLegacyFuelLoad(t *testing.T, f *actorEnrollmentFixture, star, planet uint8, ship uint32, stock uint16) {
	t.Helper()
	onEnrollmentActor(t, f, func() {
		s := f.state.ships[ship]
		s.MaxFuel = 100
		s.Fuel = 0
		p := f.state.planets[star][planet]
		p.Info[0].Fuel = stock
		if e := f.store.PutShip(s); e != nil {
			t.Error(e)
		}
		if e := f.store.PutPlanet(p); e != nil {
			t.Error(e)
		}
	})
}

func TestCargoFollowingEditsAndEnrollment(t *testing.T) {
	f := newFactoryFixture(t)
	gov := f.state.races[0].GovShip
	if out := f.command(t, fmt.Sprintf("load #%d r 2", gov)); !strings.Contains(out, "2 resources transferred") {
		t.Fatal(out)
	}
	if out := f.activate(t); !strings.Contains(out, "Factory activated") {
		t.Fatal("fresh cargo left stale factory preimages", out)
	}
	if _, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest("account-2")); e != nil {
		t.Fatal("zero-AP cargo blocked enrollment", e)
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	p, e := f.store.GetPlanet(f.star, f.planet)
	if e != nil || p.Info[0].Resource != 84 {
		t.Fatal("debits not preserved", e)
	}
}

func TestCargoQueuedBehindTurnAndCanceled(t *testing.T) {
	f := newCargoFixture(t, false)
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
	go func() { canceled <- f.state.HandleCommand(ctx, 1, 0, "load #77 r 2", r) }()
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
	go func() { done <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "load #77 r 2", r) }()
	unblock()
	if e := <-turn; e != nil {
		t.Fatal(e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(r.text(), "resources transferred") {
		t.Fatal("following operation did not execute after turn")
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}

func TestCargoHaltedAdmission(t *testing.T) {
	f := newCargoFixture(t, false)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.halt("isolated cargo admission test", "") })
	before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
	actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	if out := f.command(t, "load #77 r 2"); strings.Contains(out, "transferred.") || before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
		t.Fatal("halted cargo ran", out)
	}
}
