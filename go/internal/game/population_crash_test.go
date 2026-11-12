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

const populationInstallStage = "population.installed"

// Only this Go test executable reads these variables; production has no new
// fault environment key, debug endpoint or separate database writer. The
// "population.installed" stage is the actor's post-installation test hook, not a
// store fault: the child blocks after the committed proposal was installed and
// before the staged response is released.
func TestPopulationCrashChild(t *testing.T) {
	if os.Getenv("CR17C_POPULATION_CHILD") != "1" {
		return
	}
	path, stage := os.Getenv("CR17C_POPULATION_PATH"), os.Getenv("CR17C_POPULATION_STAGE")
	number, e := strconv.ParseUint(os.Getenv("CR17C_POPULATION_NUMBER"), 10, 32)
	if e != nil || number == 0 {
		t.Fatal("invalid child selected-ship identity")
	}
	now, e := time.Parse(time.RFC3339Nano, os.Getenv("CR17C_POPULATION_NOW"))
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
		fmt.Println("CR17C_POPULATION_BOUNDARY")
		<-make(chan struct{})
	}
	if stage == populationInstallStage {
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
	variant := os.Getenv("CR17C_POPULATION_VARIANT")
	command := ""
	switch variant {
	case "planet-colonize":
		command = fmt.Sprintf("unload #%d c 2", number)
	case "planet-evacuate":
		command = fmt.Sprintf("load #%d m 20", number)
	case "ship-crew":
		command = fmt.Sprintf("load #%d c 2", number)
	case "ship-troops":
		command = fmt.Sprintf("unload #%d m 2", number)
	default:
		t.Fatal("invalid test-only transfer variant")
	}
	if e := state.HandleCommand(ctxT(t, 20*time.Second), 1, 0, command, &recordingResponder{}); e != nil {
		t.Fatal(e)
	}
	t.Fatal("child escaped selected cargo transaction boundary")
}

// Six actual SIGKILL boundaries for each of four variants (24 kills): before the transaction, after the selected-ship
// write, after the recipient write, immediately before commit, after the
// confirmed commit before installation, and after installation before the
// response. Restart must show either the complete original or the complete
// transferred population pair, never one half.
func TestPopulationRealProcessCrash(t *testing.T) {
	// Private actor/store fixtures permit parallel groups within the unchanged package timeout.
	t.Parallel()
	for _, variant := range []string{"planet-colonize", "planet-evacuate", "ship-crew", "ship-troops"} {
		t.Run(variant, func(t *testing.T) { testPopulationRealProcessCrash(t, variant) })
	}
}

func testPopulationRealProcessCrash(t *testing.T, variant string) {
	t.Helper()
	for _, stage := range []string{"population.before", "population.write:1", "population.write:2", "population.before-commit", "population.after-commit", populationInstallStage} {
		t.Run(stage, func(t *testing.T) {
			ships := strings.HasPrefix(variant, "ship-")
			f := newPopulationFixture(t, ships)
			if !ships {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					a := f.state.ships[f.factory]
					p := f.state.planets[f.star][f.planet]
					sec := p.SectorAt(int(a.LandX), int(a.LandY))
					if variant == "planet-colonize" {
						sec.Owner = 0
						sec.Popn = 0
						sec.Troops = 0
					} else {
						sec.Popn = 0
						sec.Troops = 20
					}
					populationTotals(p)
					persistCargo(t, f)
				})
			}
			if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
				t.Fatal(e)
			}
			schedule, e := f.store.GetSchedule()
			if e != nil || schedule == nil {
				t.Fatal("missing fixture schedule", e)
			}
			wantWorld := cargoStoredWorld(t, f)
			a := wantWorld.Ships[f.factory]
			switch variant {
			case "ship-crew":
				a.Popn = 12
				a.Mass += 2 * wantWorld.Races[0].Mass
				wantWorld.Ships[f.partner].Popn = 28
			case "ship-troops":
				a.Troops = 8
				a.Mass -= 2 * wantWorld.Races[0].Mass
				wantWorld.Ships[f.partner].Troops = 22
			case "planet-colonize":
				a.Popn = 8
				a.Mass -= 2 * wantWorld.Races[0].Mass
				p := wantWorld.Planets[f.star][f.planet]
				sec := p.SectorAt(int(a.LandX), int(a.LandY))
				sec.Popn = 2
				sec.Owner = 1
				populationTotals(p)
			case "planet-evacuate":
				a.Troops = 30
				a.Mass += 20 * wantWorld.Races[0].Mass
				p := wantWorld.Planets[f.star][f.planet]
				sec := p.SectorAt(int(a.LandX), int(a.LandY))
				sec.Troops = 0
				sec.Owner = 0
				populationTotals(p)
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
			cmd := exec.CommandContext(ctx, "/proc/self/exe", "-test.run=^TestPopulationCrashChild$", "-test.v")
			cmd.Env = append(os.Environ(), "CR17C_POPULATION_CHILD=1", "CR17C_POPULATION_VARIANT="+variant, "CR17C_POPULATION_PATH="+f.path, "CR17C_POPULATION_STAGE="+stage, "CR17C_POPULATION_NUMBER="+strconv.FormatUint(uint64(f.factory), 10), "CR17C_POPULATION_NOW="+schedule.SavedAt.Format(time.RFC3339Nano))
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
					if strings.TrimSpace(scanner.Text()) == "CR17C_POPULATION_BOUNDARY" {
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
			committed := stage == "population.after-commit" || stage == populationInstallStage
			if committed {
				if !reflect.DeepEqual(cargoStoredWorld(t, f), wantWorld) {
					t.Fatal("confirmed population commit incomplete or unrelated records changed")
				}

			} else if before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("open transaction survived as partial population/allocator/metadata state")
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
				if committed {
					got, e := f.store.GetShip(f.factory)
					want := wantWorld.Ships[f.factory]
					if e != nil || got.Popn != want.Popn || got.Troops != want.Troops {
						t.Fatal(kind, "lost transferred population", e)
					}
					if ships {
						got, e := f.store.GetShip(f.partner)
						want := wantWorld.Ships[f.partner]
						if e != nil || got.Popn != want.Popn || got.Troops != want.Troops {
							t.Fatal(kind, "lost partner people", e)
						}
					} else {
						got, e := f.store.GetPlanet(f.star, f.planet)
						want := wantWorld.Planets[f.star][f.planet]
						sec := got.SectorAt(int(wantWorld.Ships[f.factory].LandX), int(wantWorld.Ships[f.factory].LandY))
						wsec := want.SectorAt(int(wantWorld.Ships[f.factory].LandX), int(wantWorld.Ships[f.factory].LandY))
						if e != nil || got.Popn != want.Popn || got.Troops != want.Troops || sec.Owner != wsec.Owner || sec.Popn != wsec.Popn || sec.Troops != wsec.Troops || got.Info[0].NumSectsOwned != want.Info[0].NumSectsOwned {
							t.Fatal(kind, "lost colony/evacuation accounting", e)
						}
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
