package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func walkRecords(t *testing.T, f *cargoFixture) map[string]string {
	t.Helper()
	select {
	case <-f.state.stopped:
		return commandFixtureRecords(t, commandFixtureImageStopped(t, &planetCommandFixture{actorEnrollmentFixture: f.actorEnrollmentFixture}))
	default:
		return commandFixtureRecords(t, commandFixtureImage(t, f.actorEnrollmentFixture))
	}
}
func walkNotice(t *testing.T, f *cargoFixture) {
	t.Helper()
	enrollActor(t, f.actorEnrollmentFixture, actorEnrollmentRequest("account-2"))
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		pl := f.state.planets[f.star][f.planet]
		sect := pl.SectorAt(2, 0)
		sect.Owner = 2
		sect.Race = 2
		sect.Popn = 1
		pl.Info[1].NumSectsOwned = 1
		pl.Info[1].Popn = 1
		pl.Popn++
		if e := f.store.PutPlanet(pl); e != nil {
			t.Error(e)
		}
	})
}
func TestWalkDirectionsScopeAndAuthority(t *testing.T) {
	for _, spec := range []string{"#77 6", "77 l", "+77 6suffix ignored"} {
		t.Run(spec, func(t *testing.T) {
			f := newWalkFixture(t)
			before := f.snapshot(t)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.dirs[0][0] = model.Directory{Level: model.LevelUniv} })
			f.command(t, "walk "+spec)
			after := f.snapshot(t)
			if !reflect.DeepEqual(after.ActorShip, after.StoredShip) || after.ActorShip.LandX != 1 || after.ActorShip.Fuel != 9 || !reflect.DeepEqual(before.StoredPlanet, after.StoredPlanet) {
				t.Fatal("grammar/scope/ownership changed")
			}
		})
	}
	for _, which := range []string{"north", "terrain", "fuel", "AP", "wrong-governor", "inactive-governor", "wrong-owner", "overflow", "negative", "mixed", "wildcard", "empty"} {
		t.Run(which, func(t *testing.T) {
			f := newWalkFixture(t)
			cmd := "walk #77 6"
			g := 0
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				sh := f.state.ships[f.factory]
				pl := f.state.planets[f.star][f.planet]
				r := f.state.races[0]
				switch which {
				case "north":
					cmd = "walk #77 8"
				case "terrain":
					r.Likes[pl.SectorAt(1, 0).Condition] = 0
				case "fuel":
					sh.Fuel = .5
				case "AP":
					f.state.stars[f.star].AP[0] = 0
				case "wrong-governor":
					g = 1
					sh.Governor = 2
					r.Governor[1].Active = true
				case "inactive-governor":
					g = 1
					sh.Governor = 1
				case "wrong-owner":
					sh.Owner = 2
				case "overflow":
					cmd = "walk #4294967373 6"
				case "negative":
					cmd = "walk #-77 6"
				case "mixed":
					cmd = "walk #77,#1 6"
				case "wildcard":
					cmd = "walk * 6"
				case "empty":
					cmd = "walk"
				}
				for _, e := range []error{f.store.PutShip(sh), f.store.PutPlanet(pl), f.store.PutStar(f.state.stars[f.star]), f.store.PutRace(r)} {
					if e != nil {
						t.Error(e)
					}
				}
			})
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			before := walkRecords(t, f)
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, cmd, &recordingResponder{}); e != nil {
				t.Fatal(e)
			}
			if f.state.Mode() != ModeServing || !reflect.DeepEqual(before, walkRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("invalid command changed world")
			}
		})
	}
}
func TestWalkAlliedSectorAndLegacyCombat(t *testing.T) {
	for _, which := range []string{"mutual-allies", "hostile-sector", "hostile-AFV", "one-way-alliance"} {
		t.Run(which, func(t *testing.T) {
			f := newWalkFixture(t)
			walkNotice(t, f)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				pl := f.state.planets[f.star][f.planet]
				a, b := f.state.races[0], f.state.races[1]
				if which != "hostile-AFV" {
					pl.SectorAt(1, 0).Owner = 2
					pl.Info[1].NumSectsOwned++
				}
				if which == "mutual-allies" || which == "one-way-alliance" {
					a.Allied |= 2
				}
				if which == "mutual-allies" {
					b.Allied |= 1
				}
				if which == "hostile-AFV" {
					enemy := factoryCopyShip(t, f.state.ships[f.factory])
					enemy.Number = 78
					enemy.Owner = 2
					enemy.LandX = 1
					enemy.Popn = 1
					enemy.Primary = 1
					enemy.Guns = model.GunPrimary
					enemy.Retaliate = 1
					enemy.Destruct = 1
					enemy.NextShip = pl.Ships
					pl.Ships = 78
					f.state.ships[78] = enemy
					if e := f.store.PutShip(enemy); e != nil {
						t.Error(e)
					}
				}
				for _, e := range []error{f.store.PutPlanet(pl), f.store.PutRace(a), f.store.PutRace(b)} {
					if e != nil {
						t.Error(e)
					}
				}
			})
			var classify error
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { _, _, _, classify = f.state.walkStage(1, 0, f.factory, '6') })
			if which != "mutual-allies" && !errors.Is(classify, commandrules.ErrWalkLegacy) {
				t.Fatal("legacy combat not classified", classify)
			}
			if which == "mutual-allies" && classify != nil {
				t.Fatal(classify)
			}
			used := false
			f.store.SetMetaFault(func(op string) error {
				if op == "walk.before" {
					used = true
				}
				return nil
			})
			planet := f.snapshot(t).StoredPlanet
			out := f.command(t, "walk #77 6")
			f.store.SetMetaFault(nil)
			if used != (which == "mutual-allies") {
				t.Fatal("wrong writer selected")
			}
			v := f.snapshot(t)
			if which == "mutual-allies" {
				if v.ActorShip.Fuel != 9 || !reflect.DeepEqual(v.ActorShip, v.StoredShip) || !reflect.DeepEqual(v.StoredPlanet, planet) {
					t.Fatal("allied noncombat changed planet")
				}
			} else if which == "hostile-AFV" {
				// Legacy skips volleys when the selected AFV has no retaliatory guns,
				// then moves in memory only. Preserve that out-of-scope behavior.
				if v.ActorShip.LandX != 1 || v.StoredShip.LandX != 0 {
					t.Fatal("legacy AFV behavior changed")
				}
			} else if !strings.Contains(out, "nothing to attack") {
				t.Fatal("legacy hostile-sector refusal changed", out)
			}
		})
	}
}
func TestWalkFailuresNoStaleFlush(t *testing.T) {
	for _, stage := range []string{"walk.before", "walk.write:1", "walk.write:2", "walk.before-commit", "walk.after-commit", "walk.commit-error"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				f := newWalkFixture(t)
				actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
				before := walkRecords(t, f)
				v := f.snapshot(t)
				want := walkExpectedRecords(t, f, before, v.ActorShip, v.Star)
				reg := &fakeRegistry{}
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
				reached := false
				f.store.SetMetaFault(func(op string) error {
					if op == "command.readback" {
						return errors.New("readback unavailable")
					}
					if op != stage {
						return nil
					}
					reached = true
					if strings.Contains(reg.all(), "moving from") || actorEnrollmentSnapshotUnsafe(f) != actor {
						t.Error("premature effect or actor mutation")
					}
					if panics {
						panic("test")
					}
					return errors.New("test")
				})
				f.command(t, "walk #77 6")
				f.store.SetMetaFault(nil)
				if !reached || f.state.Mode() != ModeHalted || strings.Contains(reg.all(), "moving from") {
					t.Fatal("fault escaped")
				}
				visible := stage == "walk.after-commit" || stage == "walk.commit-error"
				expected := before
				if visible {
					expected = want
				}
				if !reflect.DeepEqual(expected, walkRecords(t, f)) {
					t.Fatal("partial ship/AP persistence")
				}
				if stage == "walk.after-commit" {
					assertActorStoreAgreement(t, f.actorEnrollmentFixture)
				} else if actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) != actor {
					t.Fatal("unconfirmed proposal installed")
				}
				if f.store.CommandWriteUncertain() != (stage == "walk.commit-error") {
					t.Fatal("uncertainty guard")
				}
				if e := f.state.TriggerSegment(ctxT(t, time.Second)); !errors.Is(e, ErrNotServing) {
					t.Fatal(e)
				}
				if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || !reflect.DeepEqual(expected, walkRecords(t, f)) {
					t.Fatal("stale flush", e)
				}
			})
		}
	}
}

// Only called synchronously by the actor's transaction fault, never a second
// actor request (which would deadlock). Same representation as the fixture helper.
func actorEnrollmentSnapshotUnsafe(f *cargoFixture) string {
	b, e := json.Marshal([]any{f.state.stars, f.state.planets, f.state.races, f.state.ships, f.state.sdata, f.state.dirs, f.state.power, f.state.blocks, f.state.powerBlocks})
	if e != nil {
		panic(e)
	}
	return string(b)
}
func TestWalkStaleAndMalformedContext(t *testing.T) {
	for _, which := range []string{"ship", "star", "planet", "race", "orbit", "cycle", "missing-root", "identity", "mass"} {
		t.Run(which, func(t *testing.T) {
			f := newWalkFixture(t)
			if which == "ship" || which == "star" || which == "planet" || which == "race" || which == "orbit" {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					var e error
					switch which {
					case "ship":
						q := factoryCopyShip(t, f.state.ships[f.factory])
						q.Fuel++
						e = f.store.PutShip(q)
					case "star":
						q := *f.state.stars[f.star]
						q.AP[1]++
						e = f.store.PutStar(&q)
					case "planet":
						q := planetEdit(f.state.planets[f.star][f.planet])
						q.Info[1].Resource++
						e = f.store.PutPlanet(q)
					case "race":
						q := *f.state.races[0]
						q.Morale++
						e = f.store.PutRace(&q)
					case "orbit":
						n := f.state.ships[f.factory].NextShip
						q := factoryCopyShip(t, f.state.ships[n])
						q.Fuel++
						e = f.store.PutShip(q)
					}
					if e != nil {
						t.Error(e)
					}
				})
			} else {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					switch which {
					case "cycle":
						f.state.ships[f.factory].NextShip = f.factory
					case "missing-root":
						f.state.planets[f.star][f.planet].Ships = 0
					case "identity":
						f.state.ships[f.factory].Number++
					case "mass":
						f.state.ships[f.factory].Mass = 0
					}
				})
			}
			before := walkRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			text := f.command(t, "walk #77 6")
			wantMode := ModeHalted
			if which == "ship" || which == "star" || which == "planet" || which == "race" || which == "orbit" {
				wantMode = ModeServing
				if !strings.Contains(text, "retry after the next persisted turn") {
					t.Fatal("valid divergence did not defer", text)
				}
			}
			if f.state.Mode() != wantMode || !reflect.DeepEqual(before, walkRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("divergence/structural classification or no-mutation contract failed")
			}
		})
	}
}
func TestWalkInstallationAndNotifications(t *testing.T) {
	for _, which := range []string{"preinstall", "identity", "postinstall", "delivery"} {
		t.Run(which, func(t *testing.T) {
			f := newWalkFixture(t)
			walkNotice(t, f)
			before := walkRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			v := f.snapshot(t)
			reg := &walkDeliveryRegistry{fail: which == "delivery"}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				f.state.SetRegistry(reg)
				switch which {
				case "preinstall":
					f.state.testHooks.beforeCommandInstall = func() { panic("test") }
				case "identity":
					f.state.testHooks.beforeCommandInstall = func() { q := *f.state.ships[f.factory]; f.state.ships[f.factory] = &q }
				case "postinstall":
					f.state.testHooks.afterCommandInstall = func() { panic("test") }
				}
			})
			f.command(t, "walk #77 6")
			if f.state.Mode() != ModeHalted || !reflect.DeepEqual(walkExpectedRecords(t, f, before, v.ActorShip, v.Star), walkRecords(t, f)) {
				t.Fatal("committed result missing")
			}
			if which == "postinstall" || which == "delivery" {
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			} else if actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) != actor {
				t.Fatal("partial installation")
			}
			if strings.Contains(reg.all(), "moving from") {
				t.Fatal("premature movement notice")
			}
		})
	}
}

type walkDeliveryRegistry struct {
	fakeRegistry
	fail bool
}

func (r *walkDeliveryRegistry) Notify(p, g int, s string) bool {
	if r.fail {
		panic("delivery failure")
	}
	return r.fakeRegistry.Notify(p, g, s)
}
func TestWalkEffectsCancellationAndDrain(t *testing.T) {
	f := newWalkFixture(t)
	walkNotice(t, f)
	reg := &fakeRegistry{}
	var ship *model.Ship
	var star *model.Star
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg); ship = f.state.ships[f.factory]; star = f.state.stars[f.star] })
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	f.store.SetMetaFault(func(op string) error {
		if op == "walk.before" {
			close(entered)
			<-release
		}
		return nil
	})
	done := make(chan error, 1)
	go func() {
		done <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "walk #77 6", &recordingResponder{})
	}()
	waitAcceptance(t, entered, "walk admission")
	if reg.count() != 0 || ship.LandX != 0 || star.AP[0] != 50 {
		t.Fatal("premature effect")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := f.state.HandleCommand(ctx, 1, 0, "walk #77 6", &recordingResponder{}); !errors.Is(e, ErrNotExecuted) {
		t.Fatal("cancellation", e)
	}
	queuedCtx, queuedCancel := context.WithCancel(context.Background())
	defer queuedCancel()
	queued := make(chan error, 1)
	go func() { queued <- f.state.HandleCommand(queuedCtx, 1, 0, "walk #77 6", &recordingResponder{}) }()
	queueDeadline := time.Now().Add(5 * time.Second)
	for len(f.state.cmdCh) == 0 {
		if time.Now().After(queueDeadline) {
			t.Fatal("second command not queued")
		}
		time.Sleep(time.Millisecond)
	}
	queuedCancel()
	if e := <-queued; !errors.Is(e, ErrNotExecuted) {
		t.Fatal("queued cancellation", e)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- f.state.Stop(ctxT(t, 10*time.Second)) }()
	deadline := time.Now().Add(5 * time.Second)
	for !f.state.draining.Load() {
		if time.Now().After(deadline) {
			t.Fatal("drain")
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
	if !strings.Contains(reg.all(), "moving from") || ship.LandX != 1 || star.AP[0] != 49 {
		t.Fatal("admitted step did not finish")
	}
	st, e := f.store.GetStar(f.star)
	sh, e2 := f.store.GetShip(f.factory)
	if e != nil || e2 != nil || st.AP[0] != 49 || sh.LandX != 1 || sh.Fuel != 9 {
		t.Fatal("shutdown lost walk", e, e2)
	}
}
func TestWalkQuiescedRefuses(t *testing.T) {
	f := newWalkFixture(t)
	if e := f.state.Quiesce(ctxT(t, 10*time.Second)); e != nil {
		t.Fatal(e)
	}
	before := walkRecords(t, f)
	f.command(t, "walk #77 6")
	if !reflect.DeepEqual(before, walkRecords(t, f)) {
		t.Fatal("quiesced walk wrote")
	}
}

func TestWalkConcurrentCommands(t *testing.T) {
	f := newWalkFixture(t)
	errors := make(chan error, 2)
	for range 2 {
		go func() {
			errors <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "walk #77 6", &recordingResponder{})
		}()
	}
	for range 2 {
		if e := <-errors; e != nil {
			t.Fatal(e)
		}
	}
	v := f.snapshot(t)
	st, e := f.store.GetStar(f.star)
	if e != nil || !reflect.DeepEqual(v.ActorShip, v.StoredShip) || v.ActorShip.LandX != 2 || v.ActorShip.Fuel != 8 || st.AP[0] != 48 {
		t.Fatal("serialized commands lost state", e)
	}
}
