package game

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

func walkExpectedRecords(t *testing.T, f *cargoFixture, before map[string]string, sh *model.Ship, st *model.Star) map[string]string {
	t.Helper()
	want := map[string]string{}
	for k, v := range before {
		want[k] = v
	}
	ship := factoryCopyShip(t, sh)
	if ship.LandX != 0 || ship.LandY != 0 {
		t.Fatal("fault/crash fixture origin changed")
	}
	ship.LandX = 1
	ship.Fuel--
	ship.Mass -= f.cfg.MassFuel
	next := *st
	next.AP[0]--
	for k, v := range map[string]any{fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(sh.Number)): ship, fmt.Sprintf("%x/%x", db.BucketStars, db.StarKey(st.Number)): &next} {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		want[k] = string(b)
	}
	return want
}

// Fault environment is read only by this test executable, never production.
func TestWalkCrashChild(t *testing.T) {
	if os.Getenv("GB_WALK_CHILD") != "1" {
		return
	}
	path, stage := os.Getenv("GB_WALK_PATH"), os.Getenv("GB_WALK_STAGE")
	now, e := time.Parse(time.RFC3339Nano, os.Getenv("GB_WALK_NOW"))
	if e != nil {
		t.Fatal(e)
	}
	store, e := db.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	state, e := New(testConfig(t, path), store, Options{Now: func() time.Time { return now }})
	if e != nil || state.Mode() != ModeServing {
		t.Fatal("child not serving", e)
	}
	boundary := func() { fmt.Println("GB_WALK_BOUNDARY"); <-make(chan struct{}) }
	onEnrollmentActor(t, &actorEnrollmentFixture{state: state}, func() {
		gov := state.races[0].Governor[0]
		state.dirs[0][0] = model.Directory{Level: model.LevelPlan, StarNum: gov.HomeSystem, PlanetNum: gov.HomePlanetNum}
		switch stage {
		case "walk.preinstall":
			state.testHooks = &testHooks{beforeCommandInstall: boundary}
		case "walk.installed":
			state.testHooks = &testHooks{afterCommandInstall: boundary}
		}
	})
	store.SetMetaFault(func(op string) error {
		if op == stage {
			boundary()
		}
		return nil
	})
	if e := state.HandleCommand(ctxT(t, 20*time.Second), 1, 0, "walk #77 6", &recordingResponder{}); e != nil {
		t.Fatal(e)
	}
	t.Fatal("escaped selected crash boundary")
}
func TestWalkRealProcessCrash(t *testing.T) {
	for _, stage := range []string{"walk.before", "walk.check:1", "walk.validated", "walk.write:1", "walk.write:2", "walk.before-commit", "walk.after-commit", "walk.preinstall", "walk.installed"} {
		t.Run(stage, func(t *testing.T) {
			f := newWalkFixture(t)
			if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
				t.Fatal(e)
			}
			schedule, e := f.store.GetSchedule()
			if e != nil || schedule == nil {
				t.Fatal(e)
			}
			sh, e := f.store.GetShip(f.factory)
			if e != nil {
				t.Fatal(e)
			}
			st, e := f.store.GetStar(f.star)
			if e != nil {
				t.Fatal(e)
			}
			before := commandFixtureRecords(t, commandFixtureImageStopped(t, &planetCommandFixture{actorEnrollmentFixture: f.actorEnrollmentFixture}))
			want := walkExpectedRecords(t, f, before, sh, st)
			if e := f.store.Close(); e != nil {
				t.Fatal(e)
			}
			exe, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, exe, "-test.run=^TestWalkCrashChild$", "-test.v") //nolint:gosec // own Go test executable and fixed arguments
			child.Env = append(os.Environ(), "GB_WALK_CHILD=1", "GB_WALK_PATH="+f.path, "GB_WALK_STAGE="+stage, "GB_WALK_NOW="+schedule.SavedAt.Format(time.RFC3339Nano))
			stdout, e := child.StdoutPipe()
			if e != nil {
				t.Fatal(e)
			}
			stderr := &walkChildStderr{}
			child.Stderr = stderr
			if e := child.Start(); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = child.Process.Kill() })
			ready, readDone := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(readDone)
				scan := bufio.NewScanner(stdout)
				for scan.Scan() {
					if strings.TrimSpace(scan.Text()) == "GB_WALK_BOUNDARY" {
						close(ready)
					}
				}
			}()
			select {
			case <-ready:
			case <-readDone:
				_ = child.Wait()
				t.Fatal("child exited before boundary", stderr.String())
			case <-ctx.Done():
				_ = child.Process.Kill()
				<-readDone
				_ = child.Wait()
				t.Fatal("child did not reach boundary", stderr.String())
			}
			if e := child.Process.Signal(syscall.SIGKILL); e != nil {
				t.Fatal(e)
			}
			<-readDone
			e = child.Wait()
			var exit *exec.ExitError
			if !errors.As(e, &exit) {
				t.Fatal("not a killed process", e)
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatal("real SIGKILL not observed", stderr.String())
			}
			if strings.Contains(stderr.String(), "DATA RACE") || stderr.overflow {
				t.Fatal("child race report or truncated stderr", stderr.String())
			}
			f.store, e = db.Open(f.path)
			if e != nil {
				t.Fatal(e)
			}
			got := commandFixtureRecords(t, commandFixtureImageStopped(t, &planetCommandFixture{actorEnrollmentFixture: f.actorEnrollmentFixture}))
			committed := stage == "walk.after-commit" || stage == "walk.preinstall" || stage == "walk.installed"
			if committed {
				if !reflect.DeepEqual(got, want) {
					t.Fatal("partial/missing committed ship/AP or unrelated record changed")
				}
			} else if !reflect.DeepEqual(got, before) {
				t.Fatal("precommit SIGKILL persisted partial records")
			}
			// Known test boundaries define old/new outcomes; no uncertain production
			// outcome is reopened as a retry or automatic recovery policy.
			f.open(t)
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			for _, update := range []bool{false, true} {
				var e error
				if update {
					e = f.state.TriggerUpdate(ctxT(t, 10*time.Second))
				} else {
					e = f.state.TriggerSegment(ctxT(t, 10*time.Second))
				}
				if e != nil {
					t.Fatal(e)
				}
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)

				got, e := f.store.GetShip(f.factory)
				if e != nil {
					t.Fatal(e)
				}
				wantX := uint8(0)
				wantFuel := 10.
				if committed {
					wantX = 1
					wantFuel = 9
				}
				if got.LandX != wantX || got.Fuel != wantFuel {
					t.Fatal("turn reverted crash outcome")
				}

			}
		})
	}
}

// exec drains stderr concurrently; synchronize reads on early failure too and
// bound retained bytes while always draining to avoid blocking a crash child.
type walkChildStderr struct {
	mu       sync.Mutex
	b        bytes.Buffer
	overflow bool
}

func (w *walkChildStderr) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	keep := (1 << 20) - w.b.Len()
	if len(p) > keep {
		w.overflow = true
		p = p[:keep]
	}
	_, _ = w.b.Write(p)
	return n, nil
}
func (w *walkChildStderr) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.b.String() }

func TestWalkCrashStderrCapture(t *testing.T) {
	w := &walkChildStderr{}
	if n, e := w.Write([]byte("WARNING: DATA RACE\n")); e != nil || n != 19 {
		t.Fatal("stderr write", n, e)
	}
	if !strings.Contains(w.String(), "DATA RACE") {
		t.Fatal("race report lost")
	}
	b := make([]byte, 1<<20)
	if n, e := w.Write(b); e != nil || n != len(b) {
		t.Fatal("stderr draining", n, e)
	}
	if !w.overflow || len(w.String()) != 1<<20 {
		t.Fatal("stderr bound not enforced")
	}
}
