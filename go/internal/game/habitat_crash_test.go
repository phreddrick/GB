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

const habitatInstallStage = "habitat.installed"

// Only this Go test executable reads these variables; production has no new
// fault environment key, debug endpoint or separate database writer. The
// "habitat.installed" stage is the actor's post-installation test hook, not a
// store fault: the child blocks after the committed proposal was installed and
// before the staged response is released.
func TestCR17CHabitatCrashChild(t *testing.T) {
	if os.Getenv("CR17C_HABITAT_CHILD") != "1" {
		return
	}
	path, stage := os.Getenv("CR17C_HABITAT_PATH"), os.Getenv("CR17C_HABITAT_STAGE")
	number, e := strconv.ParseUint(os.Getenv("CR17C_HABITAT_NUMBER"), 10, 32)
	if e != nil || number == 0 {
		t.Fatal("invalid child factory identity")
	}
	now, e := time.Parse(time.RFC3339Nano, os.Getenv("CR17C_HABITAT_NOW"))
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
		fmt.Println("CR17C_HABITAT_BOUNDARY")
		<-make(chan struct{})
	}
	if stage == habitatInstallStage {
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
	if e := state.HandleCommand(ctxT(t, 20*time.Second), 1, 0, fmt.Sprintf("order #%d on", number), &recordingResponder{}); e != nil {
		t.Fatal(e)
	}
	t.Fatal("child escaped selected transaction boundary")
}

// Six actual SIGKILL boundaries: before the transaction, after the factory
// write, after the carrier write, immediately before commit, after the
// confirmed commit before installation, and after installation before the
// response. Restart must show either the complete original or the complete
// activated factory/carrier pair, never one half.
func TestCR17CHabitatRealProcessCrash(t *testing.T) {
	for _, stage := range []string{"habitat.before", "habitat.write:1", "habitat.write:2", "habitat.before-commit", "habitat.after-commit", habitatInstallStage} {
		t.Run(stage, func(t *testing.T) {
			f := newHabitatFixture(t)
			if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
				t.Fatal(e)
			}
			schedule, e := f.store.GetSchedule()
			if e != nil || schedule == nil {
				t.Fatal("missing fixture schedule", e)
			}
			factory, e := f.store.GetShip(f.factory)
			if e != nil {
				t.Fatal(e)
			}
			carrier, e := f.store.GetShip(f.carrier)
			if e != nil {
				t.Fatal(e)
			}
			wantFactory := factoryCopyShip(t, factory)
			wantFactory.On = true
			wantFactory.Notified = false
			wantFactory.Size = 1
			wantCarrier := factoryCopyShip(t, carrier)
			wantCarrier.Resource = 72
			wantCarrier.Hanger = 1
			wantWorld := dumpStoredWorld(t, f.store)
			before, aux := enrollmentDBSnapshot(t, f.actorEnrollmentFixture), habitatAux(t, f)
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
			cmd := exec.CommandContext(ctx, "/proc/self/exe", "-test.run=^TestCR17CHabitatCrashChild$", "-test.v")
			cmd.Env = append(os.Environ(), "CR17C_HABITAT_CHILD=1", "CR17C_HABITAT_PATH="+f.path, "CR17C_HABITAT_STAGE="+stage, "CR17C_HABITAT_NUMBER="+strconv.FormatUint(uint64(f.factory), 10), "CR17C_HABITAT_NOW="+schedule.SavedAt.Format(time.RFC3339Nano))
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
					if strings.TrimSpace(scanner.Text()) == "CR17C_HABITAT_BOUNDARY" {
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
			committed := stage == "habitat.after-commit" || stage == habitatInstallStage
			if committed {
				gotF, e1 := f.store.GetShip(f.factory)
				gotC, e2 := f.store.GetShip(f.carrier)
				if e1 != nil || e2 != nil || !reflect.DeepEqual(gotF, wantFactory) || !reflect.DeepEqual(gotC, wantCarrier) || !reflect.DeepEqual(dumpStoredWorld(t, f.store), wantWorld) || habitatAux(t, f) != aux {
					t.Fatal("confirmed factory/carrier commit lost or incomplete", e1, e2)
				}
			} else if before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("open transaction survived as partial factory/carrier/allocator/metadata state")
			}
			// Only known pre-commit or confirmed post-commit test oracles restart here.
			// No genuinely uncertain outcome is repaired/reopened as a retry.
			f.open(t)
			if f.state.Mode() != ModeServing {
				t.Fatal("complete store rejected at startup")
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			assertHabitatOrbit(t, f)
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
				assertHabitatOrbit(t, f)
				x := f.snapshot(t)
				wantOn, wantResource, wantHanger := false, uint16(100), uint16(2)
				if committed {
					wantOn, wantResource, wantHanger = true, 72, 1
				}
				if x.StoredFactory.On != wantOn || x.StoredCarrier.Resource != wantResource || x.StoredCarrier.Hanger != wantHanger || int(x.StoredFactory.Size) != f.state.shipSize(x.StoredFactory) {
					t.Fatal(kind, "crash recovery lost complete activation identity")
				}
			}
			free, e := f.store.FreeShipNums()
			if e != nil || !reflect.DeepEqual(free, freeBefore) {
				t.Fatal("allocator free list changed by crash recovery", free, e)
			}
		})
	}
}
