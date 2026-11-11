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
	"github.com/phreddrick/GB/go/internal/model"
)

// Only this Go test executable reads these variables; production has no new
// fault environment key, debug endpoint or separate database writer.
func TestTelescopeCrashChild(t *testing.T) {
	if os.Getenv("CR17C_TELESCOPE_CHILD") != "1" {
		return
	}
	path, stage := os.Getenv("CR17C_TELESCOPE_PATH"), os.Getenv("CR17C_TELESCOPE_STAGE")
	number, e := strconv.ParseUint(os.Getenv("CR17C_TELESCOPE_NUMBER"), 10, 32)
	if e != nil || number == 0 {
		t.Fatal("invalid child telescope identity")
	}
	now, e := time.Parse(time.RFC3339Nano, os.Getenv("CR17C_TELESCOPE_NOW"))
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
			fmt.Println("CR17C_TELESCOPE_BOUNDARY")
			<-make(chan struct{})
		}
		return nil
	})
	if e := state.HandleCommand(ctxT(t, 20*time.Second), 1, 0, fmt.Sprintf("order #%d aim /AimTarget/Sight", number), &recordingResponder{}); e != nil {
		t.Fatal(e)
	}
	t.Fatal("child escaped selected transaction boundary")
}

func TestTelescopeRealProcessCrash(t *testing.T) {
	for _, stage := range []string{"telescope.before", "telescope.write:1", "telescope.write:2", "telescope.write:3", "telescope.before-commit", "telescope.after-commit"} {
		t.Run(stage, func(t *testing.T) {
			f := newTelescopeFixture(t, model.OtypeStele)
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
			aim := *wantShip.Special.AimedAt
			aim.Level = model.LevelPlan
			aim.StarNum = f.target
			aim.PlanetNum = 0
			aim.ShipNo = 0
			wantShip.Special.AimedAt = &aim
			wantShip.Fuel -= .3
			wantShip.Mass -= .3 * .05
			wantShip.Notified = false
			wantWorld := telescopeStoredWorld(t, f)
			wantWorld.Ships[f.factory] = wantShip
			wantWorld.Stars[f.target].SetExplored(1)
			wantWorld.Planets[f.target][0].Info[0].Explored = true
			before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)

			if e := f.store.Close(); e != nil {
				t.Fatal(e)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			// Linux host/runner: the kernel resolves this immutable self link to
			// this exact Go test executable. No variable executable or lint
			// suppression is needed; all child arguments remain fixed.
			cmd := exec.CommandContext(ctx, "/proc/self/exe", "-test.run=^TestTelescopeCrashChild$", "-test.v")
			cmd.Env = append(os.Environ(), "CR17C_TELESCOPE_CHILD=1", "CR17C_TELESCOPE_PATH="+f.path, "CR17C_TELESCOPE_STAGE="+stage, "CR17C_TELESCOPE_NUMBER="+strconv.FormatUint(uint64(f.factory), 10), "CR17C_TELESCOPE_NOW="+schedule.SavedAt.Format(time.RFC3339Nano))
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
					if strings.TrimSpace(scanner.Text()) == "CR17C_TELESCOPE_BOUNDARY" {
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
			if stage == "telescope.after-commit" {
				got, e := f.store.GetShip(f.factory)
				if e != nil || !reflect.DeepEqual(got, wantShip) || !reflect.DeepEqual(telescopeStoredWorld(t, f), wantWorld) {
					t.Fatal("confirmed telescope/star/planet commit lost or incomplete", e)
				}
			} else if before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("open transaction survived as partial telescope/star/planet/allocator/metadata state")
			}
			// Only known pre-commit or confirmed post-commit test oracles restart here.
			// No genuinely uncertain outcome is repaired/reopened as a retry.
			f.open(t)
			if f.state.Mode() != ModeServing {
				t.Fatal("complete store rejected at startup")
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			assertFactoryOrbit(t, f.factoryFixture)
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
				assertFactoryOrbit(t, f.factoryFixture)
				x := f.snapshotAim(t)
				complete := stage == "telescope.after-commit"
				wantLevel := uint8(model.LevelUniv)
				if complete {
					wantLevel = model.LevelPlan
				}
				if x.StoredShip.Special.AimedAt.Level != wantLevel || x.StoredStar.IsExplored(1) != complete || x.StoredPlanet.Info[0].Explored != complete {
					t.Fatal(kind, "crash recovery lost complete telescope aim/exploration")
				}
			}
		})
	}
}
