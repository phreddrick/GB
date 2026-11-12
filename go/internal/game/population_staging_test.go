package game

import (
	"context"
	"errors"
	"fmt"
	"github.com/phreddrick/GB/go/internal/model"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPopulationActorFailureOutcomes(t *testing.T) {
	// Private actor/store fixtures permit parallel groups within the unchanged package timeout.
	t.Parallel()
	for _, ships := range []bool{false, true} {
		for _, stage := range []string{"population.before", "population.check:1", "population.validated", "population.write:1", "population.write:2", "population.before-commit", "population.commit-error", "population.after-commit", "fence"} {
			for _, panics := range []bool{false, true} {
				t.Run(fmt.Sprintf("ships=%t/%s/panic=%t", ships, stage, panics), func(t *testing.T) {
					f := newPopulationFixture(t, ships)
					before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
					actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
					r := &recordingResponder{}
					reached := false
					f.store.SetMetaFault(func(op string) error {
						if stage == "fence" && op == "population.before" {
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
								panic("private population fault")
							}
							return errors.New("private population fault")
						}
						return nil
					})
					if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "load #77 c 2", r); e != nil {
						t.Fatal(e)
					}
					f.store.SetWriteFence(false)
					if !reached || f.state.Mode() != ModeHalted || strings.Contains(r.text(), "complement") || strings.Contains(f.log.text(), "private population fault") {
						t.Fatal("fault did not fail closed", r.text())
					}
					visible := stage == "population.commit-error" || stage == "population.after-commit"
					if visible {
						s, e := f.store.GetShip(f.factory)
						if e != nil || s.Popn != 12 {
							t.Fatal("complete committed source missing", e)
						}
						if ships {
							o, e := f.store.GetShip(f.partner)
							if e != nil || o.Popn != 28 {
								t.Fatal("complete recipient missing", e)
							}
						} else {
							p, e := f.store.GetPlanet(f.star, f.planet)
							if e != nil || p.Info[0].Popn != 28 {
								t.Fatal("complete planet missing", e)
							}
						}
					} else if before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
						t.Fatal("partial abort")
					}
					if stage == "population.after-commit" {
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

func TestPopulationInstallationReadbackAndDelivery(t *testing.T) {
	// Private actor/store fixtures permit parallel groups within the unchanged package timeout.
	t.Parallel()
	for _, kind := range []string{"install panic", "replace source", "replace planet", "replace race", "scope changed", "readback failure", "response error", "response panic"} {
		t.Run(kind, func(t *testing.T) {
			f := newPopulationFixture(t, false)
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
					if op == "population.commit-error" || op == "command.readback" {
						return errors.New("private readback")
					}
					return nil
				})
			}
			if strings.HasPrefix(kind, "response") {
				r := &failingFactoryResponder{panics: kind == "response panic"}
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "load #77 c 2", r); e != nil {
					t.Fatal(e)
				}
			} else {
				out := f.command(t, "load #77 c 2")
				if strings.Contains(out, "complement") {
					t.Fatal("unsafe success")
				}
			}
			a, e := f.store.GetShip(f.factory)
			if e != nil || a.Popn != 12 {
				t.Fatal("durable source lost", e)
			}
			p, e := f.store.GetPlanet(f.star, f.planet)
			if e != nil || p.Info[0].Popn != 28 {
				t.Fatal("durable recipient lost", e)
			}
			if f.state.Mode() != ModeHalted || strings.Contains(f.log.text(), "private") {
				t.Fatal("unsafe failure/logging")
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				installed := strings.HasPrefix(kind, "response")
				if (f.state.ships[f.factory].Popn == 12) != installed || (f.state.planets[f.star][f.planet].Info[0].Popn == 28) != installed {
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

func TestPopulationAdmittedOperationAndModes(t *testing.T) {
	// Private actor/store fixtures permit parallel groups within the unchanged package timeout.
	t.Parallel()
	for _, action := range []string{"second transfer", "quiesce", "shutdown", "cancel started"} {
		t.Run(action, func(t *testing.T) {
			f := newPopulationFixture(t, false)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			f.store.SetMetaFault(func(op string) error {
				if op == "population.write:1" {
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
			go func() { done <- f.state.HandleCommand(ctx, 1, 0, "load #77 c 2", r) }()
			waitAcceptance(t, entered, "population write")
			if r.text() != "" {
				t.Fatal("early response")
			}
			old, e := f.store.GetShip(f.factory)
			if e != nil || old.Popn != 10 {
				t.Fatal("partial write visible", e)
			}
			next := make(chan error, 1)
			switch action {
			case "second transfer":
				go func() {
					next <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "load #77 c 2", &recordingResponder{})
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
			if e != nil || a.Popn != want {
				t.Fatal("serialization/durability", e)
			}
			if action == "quiesce" {
				disk := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				out := f.command(t, "load #77 c 2")
				if strings.Contains(out, "complement") || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
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

func TestPopulationQueuedBehindTurnAndCanceled(t *testing.T) {
	// Private actor/store fixtures permit parallel groups within the unchanged package timeout.
	t.Parallel()
	f := newPopulationFixture(t, false)
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
	go func() { canceled <- f.state.HandleCommand(ctx, 1, 0, "load #77 c 2", r) }()
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
	go func() { done <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "load #77 c 2", r) }()
	unblock()
	if e := <-turn; e != nil {
		t.Fatal(e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(r.text(), "crew complement") {
		t.Fatal("following operation did not execute after turn")
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}

func TestPopulationHaltedAdmission(t *testing.T) {
	// Private actor/store fixtures permit parallel groups within the unchanged package timeout.
	t.Parallel()
	f := newPopulationFixture(t, false)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.halt("isolated population admission test", "") })
	before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
	actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	if out := f.command(t, "load #77 c 2"); strings.Contains(out, "complement") || before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
		t.Fatal("halted population ran", out)
	}
}

func TestPopulationInvalidSelectorsAndDeferredPaths(t *testing.T) {
	// Private actor/store fixtures permit parallel groups within the unchanged package timeout.
	t.Parallel()
	for _, command := range []string{"load * c 1", "load C c 1", "load #4294967373 c 1", "load #77junk c 1", "load #0 m 1", "load #77 c 2junk", "load #77 c 2147483648", "unload #77 c -2147483648", "load #77 c 1 extra", "load #77 cesources 1"} {
		t.Run(command, func(t *testing.T) {
			f := newPopulationFixture(t, false)
			before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			out := f.command(t, command)
			if strings.Contains(out, "complement") || before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("invalid selector escaped staging", out)
			}
		})
	}
	for _, bad := range []string{"transporter", "dead partner", "missing partner", "broken reciprocal", "bad map identity", "cycle", "stored planet", "stored source", "stored star", "stored caller"} {
		t.Run(bad, func(t *testing.T) {
			f := newPopulationFixture(t, true)
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
					p.Info[1].Popn++
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
			out := f.command(t, "load #77 c 1")
			if strings.Contains(out, "complement") || before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("invalid context wrote/undocked/transporter", out)
			}
		})
	}
}
