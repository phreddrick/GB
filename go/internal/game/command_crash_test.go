package game

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

// Only the Go test executable recognizes these child variables. Production has
// no command fault environment option, debug listener or transport control.
func TestCR17BDumpCrashChild(t *testing.T) {
	if os.Getenv("CR17B_CRASH_CHILD") != "1" {
		return
	}
	path, stage := os.Getenv("CR17B_CRASH_PATH"), os.Getenv("CR17B_CRASH_STAGE")
	n, e := strconv.Atoi(os.Getenv("CR17B_CRASH_STAR"))
	if e != nil || n < 0 || n >= model.NumStars {
		t.Fatal("invalid isolated child star")
	}
	store, e := db.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	cfg := testConfig(t, path)
	fixedNow, e := time.Parse(time.RFC3339Nano, os.Getenv("CR17B_CRASH_NOW"))
	if e != nil {
		t.Fatal("invalid isolated child clock")
	}
	state, e := New(cfg, store, Options{Now: func() time.Time { return fixedNow }})
	if e != nil {
		t.Fatal(e)
	}
	if state.Mode() != ModeServing {
		t.Fatal("disposable child not serving")
	}
	_, e = call(state, ctxT(t, 10*time.Second), opTest, func() (bool, error) {
		state.dirs[0][0] = model.Directory{Level: model.LevelPlan, StarNum: uint8(n), PlanetNum: 0} //nolint:gosec // 0 <= n < NumStars (256)
		return true, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	store.SetMetaFault(func(op string) error {
		if op == stage {
			fmt.Println("CR17B_BOUNDARY_REACHED")
			<-make(chan struct{})
		}
		return nil
	})
	r := &recordingResponder{}
	if e := state.HandleCommand(ctxT(t, 20*time.Second), 1, 0, "dump 2", r); e != nil {
		t.Fatal(e)
	}
	t.Fatal("child escaped the requested crash boundary")
}

func TestCR17BDumpRealProcessCrash(t *testing.T) {
	for _, stage := range []string{"command.before", "command.write:1", "command.before-commit", "command.after-commit"} {
		t.Run(stage, func(t *testing.T) {
			f := newDumpFixture(t)
			if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
				t.Fatal(e)
			}
			beforeWorld := dumpStoredWorld(t, f.store)
			schedule, e := f.store.GetSchedule()
			if e != nil || schedule == nil {
				t.Fatal("missing crash-fixture schedule", e)
			}
			// Independent desired state, not reconstruction through the production API.
			want := dumpWant(dumpStoredWorld(t, f.store), f.home, 2, 0, 1, 2)
			before, aux := enrollmentDBSnapshot(t, f.actorEnrollmentFixture), dumpUnchangedAux(t, f.store)
			if e := f.store.Close(); e != nil {
				t.Fatal(e)
			}
			exe, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestCR17BDumpCrashChild$", "-test.v") //nolint:gosec // executes this Go test binary from os.Executable with fixed arguments
			// Preserve race-runtime settings, but make this test's disposable path and
			// boundary explicit. No credential/token values are passed or printed.
			cmd.Env = append(os.Environ(), "CR17B_CRASH_CHILD=1", "CR17B_CRASH_PATH="+f.path, "CR17B_CRASH_STAGE="+stage, "CR17B_CRASH_STAR="+strconv.Itoa(int(f.home)), "CR17B_CRASH_NOW="+schedule.SavedAt.Format(time.RFC3339Nano))
			stdout, e := cmd.StdoutPipe()
			if e != nil {
				t.Fatal(e)
			}
			cmd.Stderr = io.Discard
			if e := cmd.Start(); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			ready := make(chan struct{})
			readDone := make(chan struct{})
			go func() {
				defer close(readDone)
				scan := bufio.NewScanner(stdout)
				for scan.Scan() {
					if strings.TrimSpace(scan.Text()) == "CR17B_BOUNDARY_REACHED" {
						close(ready)
					}
				}
			}()
			// Stdout is drained before Wait, avoiding premature pipe closure races.
			select {
			case <-ready:
			case <-readDone:
				t.Fatal("child exited before reaching actual transaction hook")
			case <-ctx.Done():
				t.Fatal("child did not reach actual transaction hook")
			}
			if e := cmd.Process.Signal(syscall.SIGKILL); e != nil {
				t.Fatal(e)
			}
			<-readDone
			e = cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(e, &exit) {
				t.Fatal("child was not killed", e)
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatal("child did not terminate by SIGKILL")
			}
			reopened, e := db.Open(f.path)
			if e != nil {
				t.Fatal("interrupted bbolt store cannot reopen", e)
			}
			f.store = reopened
			if stage == "command.after-commit" {
				if !reflect.DeepEqual(dumpStoredWorld(t, f.store), want) || dumpUnchangedAux(t, f.store) != aux {
					t.Fatal("confirmed commit lost or changed unrelated metadata/allocator")
				}
			} else if enrollmentDBSnapshot(t, f.actorEnrollmentFixture) != before || !reflect.DeepEqual(dumpStoredWorld(t, f.store), beforeWorld) {
				t.Fatal("SIGKILL left partial world/AP/free-list/placement state")
			}
			// These phases have known process-test oracles: pre-commit interruption or
			// a nil Update followed by lost response. This is NOT uncertain-write repair.
			f.open(t)
			if f.state.Mode() != ModeServing {
				t.Fatal("known complete store failed startup validation")
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				for _, r := range f.state.races {
					if r != nil && !f.state.governed(r) {
						t.Error("government identity lost after process crash")
					}
				}
			})
		})
	}
}
