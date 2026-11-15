package game

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

func stockpileExpectedRecords(t *testing.T, before map[string]string, p *model.Planet, st *model.Star) map[string]string {
	t.Helper()
	want := map[string]string{}
	for k, v := range before {
		want[k] = v
	}
	q := planetEdit(p)
	q.Info[0].Crystals -= 7
	q.Info[1].Crystals += 7
	next := *st
	next.AP[0]--
	for k, v := range map[string]any{fmt.Sprintf("%x/%x", db.BucketPlanets, db.PlanetKey(p.StarNum, p.PlanetNum)): q, fmt.Sprintf("%x/%x", db.BucketStars, db.StarKey(st.Number)): &next} {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		want[k] = string(b)
	}
	return want
}

// Fault environment is read only by this test executable, never production.
func TestStockpileTransferCrashChild(t *testing.T) {
	if os.Getenv("GB_STOCKPILE_CHILD") != "1" {
		return
	}
	path, stage := os.Getenv("GB_STOCKPILE_PATH"), os.Getenv("GB_STOCKPILE_STAGE")
	now, e := time.Parse(time.RFC3339Nano, os.Getenv("GB_STOCKPILE_NOW"))
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
	boundary := func() { fmt.Println("GB_STOCKPILE_BOUNDARY"); <-make(chan struct{}) }
	onEnrollmentActor(t, &actorEnrollmentFixture{state: state}, func() {
		gov := state.races[0].Governor[0]
		state.dirs[0][0] = model.Directory{Level: model.LevelPlan, StarNum: gov.HomeSystem, PlanetNum: gov.HomePlanetNum}
		switch stage {
		case "stockpile.preinstall":
			state.testHooks = &testHooks{beforeCommandInstall: boundary}
		case "stockpile.installed":
			state.testHooks = &testHooks{afterCommandInstall: boundary}
		}
	})
	store.SetMetaFault(func(op string) error {
		if op == stage {
			boundary()
		}
		return nil
	})
	if e := state.HandleCommand(ctxT(t, 20*time.Second), 1, 0, "transfer 2 x 7", &recordingResponder{}); e != nil {
		t.Fatal(e)
	}
	t.Fatal("escaped selected crash boundary")
}
func TestStockpileTransferRealProcessCrash(t *testing.T) {
	for _, stage := range []string{"stockpile.before", "stockpile.check:1", "stockpile.validated", "stockpile.write:1", "stockpile.write:2", "stockpile.before-commit", "stockpile.after-commit", "stockpile.preinstall", "stockpile.installed"} {
		t.Run(stage, func(t *testing.T) {
			f := newPlanetCommandFixture(t)
			if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
				t.Fatal(e)
			}
			schedule, e := f.store.GetSchedule()
			if e != nil || schedule == nil {
				t.Fatal(e)
			}
			p, e := f.store.GetPlanet(f.star, f.planet)
			if e != nil {
				t.Fatal(e)
			}
			st, e := f.store.GetStar(f.star)
			if e != nil {
				t.Fatal(e)
			}
			before := commandFixtureRecords(t, commandFixtureImageStopped(t, f))
			want := stockpileExpectedRecords(t, before, p, st)
			if e := f.store.Close(); e != nil {
				t.Fatal(e)
			}
			exe, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, exe, "-test.run=^TestStockpileTransferCrashChild$", "-test.v") //nolint:gosec // own Go test executable and fixed arguments
			child.Env = append(os.Environ(), "GB_STOCKPILE_CHILD=1", "GB_STOCKPILE_PATH="+f.path, "GB_STOCKPILE_STAGE="+stage, "GB_STOCKPILE_NOW="+schedule.SavedAt.Format(time.RFC3339Nano))
			stdout, e := child.StdoutPipe()
			if e != nil {
				t.Fatal(e)
			}
			child.Stderr = io.Discard
			if e := child.Start(); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = child.Process.Kill() })
			ready, readDone := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(readDone)
				scan := bufio.NewScanner(stdout)
				for scan.Scan() {
					if strings.TrimSpace(scan.Text()) == "GB_STOCKPILE_BOUNDARY" {
						close(ready)
					}
				}
			}()
			select {
			case <-ready:
			case <-readDone:
				t.Fatal("child exited before boundary")
			case <-ctx.Done():
				t.Fatal("child did not reach boundary")
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
				t.Fatal("real SIGKILL not observed")
			}
			f.store, e = db.Open(f.path)
			if e != nil {
				t.Fatal(e)
			}
			got := commandFixtureRecords(t, commandFixtureImageStopped(t, f))
			committed := stage == "stockpile.after-commit" || stage == "stockpile.preinstall" || stage == "stockpile.installed"
			if committed {
				if !reflect.DeepEqual(got, want) {
					t.Fatal("partial/missing committed planet/AP or unrelated record changed")
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
				wantCrystals := uint16(100)
				if committed {
					wantCrystals = 93
				}
				if f.storedPlanet(t).Info[0].Crystals != wantCrystals {
					t.Fatal("turn reverted crash outcome")
				}
			}
		})
	}
}

// Only a stopped disposable actor with a completed Store write may be read.
func commandFixtureImageStopped(t *testing.T, f *planetCommandFixture) string {
	t.Helper()
	b, e := os.ReadFile(f.path)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
