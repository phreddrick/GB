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
)

// Only this Go test executable reads these variables; production has no new
// fault environment key, debug endpoint or separate database writer.
func TestCR17CFactoryCrashChild(t *testing.T) {
	if os.Getenv("CR17C_FACTORY_CHILD") != "1" {
		return
	}
	path, stage := os.Getenv("CR17C_FACTORY_PATH"), os.Getenv("CR17C_FACTORY_STAGE")
	number, e := strconv.ParseUint(os.Getenv("CR17C_FACTORY_NUMBER"), 10, 32)
	if e != nil || number == 0 {
		t.Fatal("invalid child factory identity")
	}
	now, e := time.Parse(time.RFC3339Nano, os.Getenv("CR17C_FACTORY_NOW"))
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
	store.SetMetaFault(func(op string) error {
		if op == stage {
			fmt.Println("CR17C_FACTORY_BOUNDARY")
			<-make(chan struct{})
		}
		return nil
	})
	if e := state.HandleCommand(ctxT(t, 20*time.Second), 1, 0, fmt.Sprintf("order #%d on", number), &recordingResponder{}); e != nil {
		t.Fatal(e)
	}
	t.Fatal("child escaped selected transaction boundary")
}

func TestCR17CFactoryRealProcessCrash(t *testing.T) {
	for _, stage := range []string{"factory.before", "factory.write:1", "factory.before-commit", "factory.after-commit"} {
		t.Run(stage, func(t *testing.T) {
			f := newFactoryFixture(t)
			if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
				t.Fatal(e)
			}
			schedule, e := f.store.GetSchedule()
			if e != nil || schedule == nil {
				t.Fatal("missing fixture schedule", e)
			}
			ship, e := f.store.GetShip(f.factory)
			if e != nil {
				t.Fatal(e)
			}
			wantShip := factoryCopyShip(t, ship)
			wantShip.On = true
			wantShip.Notified = false
			wantWorld := dumpStoredWorld(t, f.store)
			wantWorld.Planets[f.star][f.planet].Info[0].Resource = 86
			before, aux := enrollmentDBSnapshot(t, f.actorEnrollmentFixture), factoryAux(t, f)
			if e := f.store.Close(); e != nil {
				t.Fatal(e)
			}
			exe, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestCR17CFactoryCrashChild$", "-test.v") //nolint:gosec // own Go test executable from os.Executable; fixed arguments
			cmd.Env = append(os.Environ(), "CR17C_FACTORY_CHILD=1", "CR17C_FACTORY_PATH="+f.path, "CR17C_FACTORY_STAGE="+stage, "CR17C_FACTORY_NUMBER="+strconv.FormatUint(uint64(f.factory), 10), "CR17C_FACTORY_NOW="+schedule.SavedAt.Format(time.RFC3339Nano))
			stdout, e := cmd.StdoutPipe()
			if e != nil {
				t.Fatal(e)
			}
			cmd.Stderr = io.Discard
			if e := cmd.Start(); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			ready, readDone := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(readDone)
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					if strings.TrimSpace(scanner.Text()) == "CR17C_FACTORY_BOUNDARY" {
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
			if stage == "factory.after-commit" {
				got, e := f.store.GetShip(f.factory)
				if e != nil || !reflect.DeepEqual(got, wantShip) || !reflect.DeepEqual(dumpStoredWorld(t, f.store), wantWorld) || factoryAux(t, f) != aux {
					t.Fatal("confirmed factory/planet commit lost or incomplete", e)
				}
			} else if before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("open transaction survived as partial factory/planet/allocator/metadata state")
			}
			// Only known pre-commit or confirmed post-commit test oracles restart here.
			// No genuinely uncertain outcome is repaired/reopened as a retry.
			f.open(t)
			if f.state.Mode() != ModeServing {
				t.Fatal("complete store rejected at startup")
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			assertFactoryOrbit(t, f)
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
				assertFactoryOrbit(t, f)
				x := f.snapshot(t)
				wantOn, wantResource := false, uint16(100)
				if stage == "factory.after-commit" {
					wantOn, wantResource = true, 86
				}
				if x.StoredShip.On != wantOn || x.StoredPlanet.Info[0].Resource != wantResource {
					t.Fatal(kind, "crash recovery lost complete activation identity")
				}
			}
		})
	}
}
