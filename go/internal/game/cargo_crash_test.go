package game

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
)

const cargoInstallStage = "cargo.installed"

// Only this Go test executable reads these variables; production has no new
// fault environment key, debug endpoint or separate database writer. The
// "cargo.installed" stage is the actor's post-installation test hook, not a
// store fault: the child blocks after the committed proposal was installed and
// before the staged response is released.
func TestCargoCrashChild(t *testing.T) {
	if os.Getenv("CR17C_CARGO_CHILD") != "1" {
		return
	}
	path, stage := os.Getenv("CR17C_CARGO_PATH"), os.Getenv("CR17C_CARGO_STAGE")
	number, e := strconv.ParseUint(os.Getenv("CR17C_CARGO_NUMBER"), 10, 32)
	if e != nil || number == 0 {
		t.Fatal("invalid child selected-ship identity")
	}
	now, e := time.Parse(time.RFC3339Nano, os.Getenv("CR17C_CARGO_NOW"))
	if e != nil {
		t.Fatal("invalid isolated clock")
	}
	store, e := db.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	state, e := New(testConfig(t, path), store, Options{Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	if state.Mode() != ModeServing {
		t.Fatal("child actor not serving")
	}
	boundary := func() {
		fmt.Println("CR17C_CARGO_BOUNDARY")
		<-make(chan struct{})
	}
	if stage == cargoInstallStage {
		if e := state.request(ctxT(t, 10*time.Second), opTest, func() error {
			state.testHooks = &testHooks{afterCommandInstall: boundary}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	} else {
		store.SetMetaFault(func(op string) error {
			if op == stage {
				boundary()
			}
			return nil
		})
	}
	if e := state.HandleCommand(ctxT(t, 20*time.Second), 1, 0, fmt.Sprintf("load #%d r 2", number), &recordingResponder{}); e != nil {
		t.Fatal(e)
	}
	t.Fatal("child escaped selected cargo transaction boundary")
}

// Six actual SIGKILL boundaries: before the transaction, after the selected-ship
// write, after the recipient write, immediately before commit, after the
// confirmed commit before installation, and after installation before the
// response. Restart must show either the complete original or the complete
// transferred cargo pair, never one half.
func TestCargoRealProcessCrash(t *testing.T) {
	for _, ships := range []bool{false, true} {
		t.Run(fmt.Sprintf("ships=%t", ships), func(t *testing.T) {
			testCargoRealProcessCrash(t, ships)
		})
	}
}

func testCargoRealProcessCrash(t *testing.T, ships bool) {
	t.Helper()
	for _, stage := range []string{"cargo.before", "cargo.write:1", "cargo.write:2", "cargo.before-commit", "cargo.after-commit", cargoInstallStage} {
		t.Run(stage, func(t *testing.T) {
			f := newCargoFixture(t, ships)
			if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
				t.Fatal(e)
			}
			schedule, e := f.store.GetSchedule()
			if e != nil || schedule == nil {
				t.Fatal("missing fixture schedule", e)
			}
			wantWorld := cargoStoredWorld(t, f)
			wantWorld.Ships[f.factory].Resource = 12
			wantWorld.Ships[f.factory].Mass += 2 * f.cfg.MassResource
			if ships {
				wantWorld.Ships[f.partner].Resource = 28
			} else {
				wantWorld.Planets[f.star][f.planet].Info[0].Resource = 98
			}
			before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			freeBefore, e := f.store.FreeShipNums()
			if e != nil {
				t.Fatal(e)
			}
			if e := f.store.Close(); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			// Linux host/CI: execute this exact Go test binary without a
			// variable executable path or a process-execution lint exception.
			cmd := exec.CommandContext(ctx, "/proc/self/exe", "-test.run=^TestCargoCrashChild$", "-test.v")
			cmd.Env = append(os.Environ(), "CR17C_CARGO_CHILD=1", "CR17C_CARGO_PATH="+f.path, "CR17C_CARGO_STAGE="+stage, "CR17C_CARGO_NUMBER="+strconv.FormatUint(uint64(f.factory), 10), "CR17C_CARGO_NOW="+schedule.SavedAt.Format(time.RFC3339Nano))
			stdout, e := cmd.StdoutPipe()
			if e != nil {
				t.Fatal(e)
			}
			var childStderr bytes.Buffer
			cmd.Stderr = &childStderr
			if e := cmd.Start(); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			ready, readDone := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(readDone)
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					if strings.TrimSpace(scanner.Text()) == "CR17C_CARGO_BOUNDARY" {
						close(ready)
					}
				}
			}()
			select {
			case <-ready:
			case <-readDone:
				t.Fatal("child exited before actual transaction boundary")
			case <-ctx.Done():
				t.Fatal("child failed to reach boundary")
			}
			if e := cmd.Process.Signal(syscall.SIGKILL); e != nil {
				t.Fatal(e)
			}
			<-readDone
			e = cmd.Wait()
			if strings.Contains(childStderr.String(), "WARNING: DATA RACE") {
				t.Fatal("crash child reported a data race")
			}
			var exit *exec.ExitError
			if !errors.As(e, &exit) {
				t.Fatal("child not killed", e)
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatal("actual SIGKILL not observed")
			}
			reopened, e := db.Open(f.path)
			if e != nil {
				t.Fatal(e)
			}
			f.store = reopened
			committed := stage == "cargo.after-commit" || stage == cargoInstallStage
			if committed {
				if !reflect.DeepEqual(cargoStoredWorld(t, f), wantWorld) {
					t.Fatal("confirmed cargo commit incomplete or unrelated records changed")
				}

			} else if before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("open transaction survived as partial cargo/allocator/metadata state")
			}
			// Only known pre-commit or confirmed post-commit test oracles restart here.
			// No genuinely uncertain outcome is repaired/reopened as a retry.
			f.open(t)
			if f.state.Mode() != ModeServing {
				t.Fatal("complete store rejected at startup")
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			assertCargoLinks(t, f)
			for _, kind := range []string{"segment", "update"} {
				var e error
				if kind == "segment" {
					e = f.state.TriggerSegment(ctxT(t, 10*time.Second))
				} else {
					e = f.state.TriggerUpdate(ctxT(t, 10*time.Second))
				}
				if e != nil {
					t.Fatal(e)
				}
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
				assertCargoLinks(t, f)
				x, e := f.store.GetShip(f.factory)
				wantResource := uint16(10)
				if committed {
					wantResource = 12
				}
				if e != nil || x.Resource != wantResource {
					t.Fatal(kind, "lost source cargo", e)
				}
				if ships {
					o, e := f.store.GetShip(f.partner)
					want := uint16(30)
					if committed {
						want = 28
					}
					if e != nil || o.Resource != want {
						t.Fatal(kind, "lost partner cargo", e)
					}
				} else {
					p, e := f.store.GetPlanet(f.star, f.planet)
					want := uint16(100)
					if committed {
						want = 98
					}
					if e != nil || p.Info[0].Resource != want {
						t.Fatal(kind, "lost planet cargo", e)
					}
				}

			}
			free, e := f.store.FreeShipNums()
			if e != nil || !reflect.DeepEqual(free, freeBefore) {
				t.Fatal("allocator free list changed by crash recovery", free, e)
			}
		})
	}
}
