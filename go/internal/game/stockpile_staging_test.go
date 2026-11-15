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

func stockpileStar(t *testing.T, f *planetCommandFixture) *model.Star {
	t.Helper()
	var st model.Star
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { st = *f.state.stars[f.star] })
	return &st
}
func stockpileRecords(t *testing.T, f *planetCommandFixture) map[string]string {
	t.Helper()
	select {
	case <-f.state.stopped:
		return commandFixtureRecords(t, commandFixtureImageStopped(t, f))
	default:
		return commandFixtureRecords(t, commandFixtureImage(t, f.actorEnrollmentFixture))
	}
}
func TestStockpileTransferFaultsAndNoStaleFlush(t *testing.T) {
	for _, stage := range []string{"stockpile.before", "stockpile.check:2", "stockpile.validated", "stockpile.write:1", "stockpile.write:2", "stockpile.before-commit", "stockpile.after-commit", "stockpile.commit-error"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				f := newPlanetCommandFixture(t)
				before := stockpileRecords(t, f)
				actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
				planet := f.storedPlanet(t)
				star := stockpileStar(t, f)
				reg := &fakeRegistry{}
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
				reached := false
				r := &recordingResponder{}
				f.store.SetMetaFault(func(op string) error {
					if op == "command.readback" {
						return errors.New("diagnostics unavailable")
					}
					if op != stage {
						return nil
					}
					reached = true
					if r.text() != "" || reg.count() != 0 {
						t.Error("success or notification before confirmation")
					}
					if !reflect.DeepEqual(f.state.planets[f.star][f.planet], planet) || !reflect.DeepEqual(f.state.stars[f.star], star) {
						t.Error("proposal installed before commit")
					}
					if panics {
						panic("isolated transaction panic")
					}
					return errors.New("isolated transaction fault")
				})
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "transfer 2 x 7", r); e != nil {
					t.Fatal(e)
				}
				if !reached || f.state.Mode() != ModeHalted || strings.Contains(r.text(), "transferred") || strings.Contains(reg.all(), "transferred") {
					t.Fatal("failure escaped or falsely succeeded", r.text())
				}
				f.store.SetMetaFault(nil)
				after := stockpileRecords(t, f)
				committed := stage == "stockpile.after-commit"
				visible := committed || stage == "stockpile.commit-error"
				if visible {
					want := stockpileExpectedRecords(t, before, planet, star)
					if !reflect.DeepEqual(after, want) {
						t.Fatal("visible record set is partial or changed auxiliaries")
					}
				} else if !reflect.DeepEqual(before, after) {
					t.Fatal("confirmed abort changed records")
				}
				if committed {
					assertActorStoreAgreement(t, f.actorEnrollmentFixture)
				} else if actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) != actor {
					t.Fatal("aborted/uncertain proposal installed")
				}
				if f.store.CommandWriteUncertain() != (stage == "stockpile.commit-error") {
					t.Fatal("uncertainty guard incorrect")
				}
				if e := f.state.TriggerUpdate(ctxT(t, time.Second)); !errors.Is(e, ErrNotServing) {
					t.Fatal("halted update accepted", e)
				}
				if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || !reflect.DeepEqual(after, stockpileRecords(t, f)) {
					t.Fatal("stale shutdown flush", e)
				}
			})
		}
	}
}

func TestStockpileTransferInstallationAndResponseFailures(t *testing.T) {
	for _, which := range []string{"preinstall-panic", "identity", "postinstall-panic", "response"} {
		t.Run(which, func(t *testing.T) {
			f := newPlanetCommandFixture(t)
			before := stockpileRecords(t, f)
			p, st := f.storedPlanet(t), stockpileStar(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			r := &stockpileFailingResponder{fail: which == "response"}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				switch which {
				case "preinstall-panic":
					f.state.testHooks.beforeCommandInstall = func() { panic("install panic") }
				case "identity":
					f.state.testHooks.beforeCommandInstall = func() { q := *f.state.stars[f.star]; f.state.stars[f.star] = &q }
				case "postinstall-panic":
					f.state.testHooks.afterCommandInstall = func() { panic("after install panic") }
				}
			})
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "transfer 2 x 7", r); e != nil {
				t.Fatal(e)
			}
			if strings.Contains(r.text(), "transferred") {
				t.Fatal("success released before installation/effect boundary completed")
			}
			if f.state.Mode() != ModeHalted || !reflect.DeepEqual(stockpileRecords(t, f), stockpileExpectedRecords(t, before, p, st)) {
				t.Fatal("committed result lost or failed to halt")
			}
			if which == "postinstall-panic" || which == "response" {
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			} else if actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) != actor {
				t.Fatal("partial installation")
			}
			if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) {
				t.Fatal(e)
			}
		})
	}
}

type stockpileFailingResponder struct {
	recordingResponder
	fail bool
}

func (r *stockpileFailingResponder) Send(s string) error {
	if r.fail {
		return errors.New("disconnected")
	}
	return r.recordingResponder.Send(s)
}

func TestStockpileTransferEffectsAndDrain(t *testing.T) {
	f := newPlanetCommandFixture(t)
	before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var pp *model.Planet
	var sp *model.Star
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { pp = f.state.planets[f.star][f.planet]; sp = f.state.stars[f.star] })
	r := &recordingResponder{}
	reg := &fakeRegistry{}
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
	f.store.SetMetaFault(func(op string) error {
		if op == "stockpile.before" {
			close(entered)
			<-release
		}
		return nil
	})
	done, stopped := make(chan error, 1), make(chan error, 1)
	go func() { done <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "transfer 2 x 7", r) }()
	waitAcceptance(t, entered, "stockpile admission")
	if r.text() != "" || reg.count() != 0 || enrollmentDBSnapshot(t, f.actorEnrollmentFixture) != before {
		t.Fatal("effects before commit")
	}
	go func() { stopped <- f.state.Stop(ctxT(t, 10*time.Second)) }()
	deadline := time.Now().Add(5 * time.Second)
	for !f.state.draining.Load() {
		if time.Now().After(deadline) {
			t.Fatal("drain not started")
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
	f.store.SetMetaFault(nil)
	if !strings.Contains(r.text(), "transferred") || reg.count() == 0 {
		t.Fatal("committed response not released")
	}
	// Shutdown legitimately changes commit metadata; world effects must match.
	gotP := f.storedPlanet(t)
	gotS, e := f.store.GetStar(f.star)
	if e != nil {
		t.Fatal(e)
	}
	if gotP.Info[0].Crystals != 93 || gotP.Info[1].Crystals != 17 || gotS.AP[0] != 49 || pp.Info[0].Crystals != 93 || sp.AP[0] != 49 {
		t.Fatal("draining lost complete transfer")
	}
}

func TestStockpileTransferPendingEnrollmentGuard(t *testing.T) {
	f := newPlanetCommandFixture(t)
	f.command(t, "transfer 2 x 7")
	// Transfer AP is now durable: enrollment can follow immediately. The same
	// pending-turn guard must still reject an unrelated volatile actor AP edit.
	if _, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest("account-3")); e != nil {
		t.Fatal("durable transfer unnecessarily blocked enrollment", e)
	}
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.stars[f.star].AP[0]-- })
	_, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest("guest-operator"))
	assertActorEnrollmentFailure(t, e, "ENROLLMENT_PENDING_TURN", db.EnrollmentNotCommitted)
}

func TestStockpileTransferScheduledTurnsAndRestart(t *testing.T) {
	f := newPlanetCommandFixture(t)
	var pp *model.Planet
	var sp *model.Star
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { pp = f.state.planets[f.star][f.planet]; sp = f.state.stars[f.star] })
	f.command(t, "transfer 2 x 7")
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		if pp != f.state.planets[f.star][f.planet] || sp != f.state.stars[f.star] {
			t.Error("actor addresses replaced")
		}
	})
	f.restart(t)
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
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
				t.Fatal("scheduled turn did not finish")
			}
			time.Sleep(time.Millisecond)
		}
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		if f.storedPlanet(t).Info[0].Crystals != 93 || f.storedPlanet(t).Info[1].Crystals != 17 {
			t.Fatal("scheduled turn reverted stockpiles")
		}
		rec, e := f.store.GetCommitRecord()
		if e != nil {
			t.Fatal(e)
		}
		want := db.CommitKindSegment
		if update {
			want = db.CommitKindUpdate
		}
		if rec.Kind != want {
			t.Fatal("wrong durable scheduled boundary", rec.Kind)
		}
	}
	f.restart(t)
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}

func TestStockpileTransferFullMatrix(t *testing.T) {
	for _, commodity := range []string{"r", "x", "&", "f", "d"} {
		for _, target := range []int{1, 2} {
			for _, full := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/to%d/full=%t", commodity, target, full), func(t *testing.T) {
					f := newPlanetCommandFixture(t)
					want := f.actorPlanet(t)
					wantStar := stockpileStar(t, f)
					from, to := stockpileTestBalances(want, commodity, target)
					n := uint16(0)
					if full {
						n = *from
					}
					*from -= n
					*to += n
					wantStar.AP[0]--
					if out := f.command(t, fmt.Sprintf("transfer %d %s %d", target, commodity, n)); !strings.Contains(out, "transferred") {
						t.Fatal(out)
					}
					gotStar, e := f.store.GetStar(f.star)
					if e != nil {
						t.Fatal(e)
					}
					if !reflect.DeepEqual(want, f.actorPlanet(t)) || !reflect.DeepEqual(want, f.storedPlanet(t)) || !reflect.DeepEqual(wantStar, gotStar) || !reflect.DeepEqual(wantStar, stockpileStar(t, f)) {
						t.Fatal("commodity/self/zero/max/AP semantics changed")
					}
				})
			}
		}
	}
}
func stockpileTestBalances(p *model.Planet, c string, target int) (*uint16, *uint16) {
	a, b := &p.Info[0], &p.Info[target-1]
	switch c {
	case "r":
		return &a.Resource, &b.Resource
	case "f":
		return &a.Fuel, &b.Fuel
	case "d":
		return &a.Destruct, &b.Destruct
	default:
		return &a.Crystals, &b.Crystals
	}
}
func TestStockpileTransferActorConflicts(t *testing.T) {
	for _, which := range []string{"planet", "star", "caller", "target", "fence"} {
		t.Run(which, func(t *testing.T) {
			f := newPlanetCommandFixture(t)
			switch which {
			case "planet":
				v := f.storedPlanet(t)
				v.Info[2].Resource++
				if e := f.store.PutPlanet(v); e != nil {
					t.Fatal(e)
				}
			case "star":
				v, e := f.store.GetStar(f.star)
				if e != nil {
					t.Fatal(e)
				}
				v.AP[1]++
				if e := f.store.PutStar(v); e != nil {
					t.Fatal(e)
				}
			case "caller", "target":
				n := 1
				if which == "target" {
					n = 2
				}
				v, e := f.store.GetRace(n)
				if e != nil {
					t.Fatal(e)
				}
				v.Morale++
				if e := f.store.PutRace(v); e != nil {
					t.Fatal(e)
				}
			case "fence":
				f.store.SetWriteFence(true)
			}
			disk := stockpileRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			out := f.command(t, "transfer 2 r 7")
			f.store.SetWriteFence(false)
			if strings.Contains(out, "transferred") || f.state.Mode() != ModeHalted || !reflect.DeepEqual(disk, stockpileRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("conflict/fence mutated or succeeded", out)
			}
		})
	}
}
func TestStockpileTransferConcurrentRequests(t *testing.T) {
	f := newPlanetCommandFixture(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "transfer 2 x 1", r); e != nil || !strings.Contains(r.text(), "transferred") {
				t.Error("serialized transfer failed", e, r.text())
			}
		}()
	}
	wg.Wait()
	p := f.storedPlanet(t)
	st, e := f.store.GetStar(f.star)
	if e != nil {
		t.Fatal(e)
	}
	if p.Info[0].Crystals != 92 || p.Info[1].Crystals != 18 || st.AP[0] != 42 {
		t.Fatal("serialized requests lost or duplicated effects")
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}

func TestStockpileTransferPreservesLongWorldNames(t *testing.T) {
	f := newPlanetCommandFixture(t)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		st := f.state.stars[f.star]
		st.Name = strings.Repeat("s", 5000)
		if e := f.store.PutStar(st); e != nil {
			t.Error(e)
		}
	})
	out := f.command(t, "transfer 2 x 7")
	st, e := f.store.GetStar(f.star)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(out, strings.Repeat("s", 5000)) || !strings.Contains(out, "transferred") || st.AP[0] != 49 || f.storedPlanet(t).Info[0].Crystals != 93 {
		t.Fatal("private response staging imposed a new gameplay/name restriction")
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}
