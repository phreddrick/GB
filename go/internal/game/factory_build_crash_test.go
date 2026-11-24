package game

import (
	"bufio"
	"context"
	"errors"
	"fmt"
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

// Fault environment is read only by this test executable, never production.
func TestPlanetFactoryBuildCrashChild(t *testing.T) {
	if os.Getenv("GB_FACTORY_BUILD_CHILD") != "1" {
		return
	}
	path, stage := os.Getenv("GB_FACTORY_BUILD_PATH"), os.Getenv("GB_FACTORY_BUILD_STAGE")
	now, e := time.Parse(time.RFC3339Nano, os.Getenv("GB_FACTORY_BUILD_NOW"))
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
	command := "build F 0,0"
	boundary := func() { fmt.Println("GB_FACTORY_BUILD_BOUNDARY"); <-make(chan struct{}) }
	onEnrollmentActor(t, &actorEnrollmentFixture{state: state}, func() {
		gov := state.races[0].Governor[0]
		state.dirs[0][0] = model.Directory{Level: model.LevelPlan, StarNum: gov.HomeSystem, PlanetNum: gov.HomePlanetNum}
		if strings.HasPrefix(stage, "cargo-build.") || strings.HasPrefix(stage, "probe-build.") {
			state.dirs[0][0].Level = model.LevelShip
			state.dirs[0][0].ShipNo = 77
			command = "build X"
			if strings.HasPrefix(stage, "probe-build.") {
				command = "build :"
			}
		}
		if strings.HasPrefix(stage, "probe-launch.") {
			command = "launch #78"
		}
		if strings.HasPrefix(stage, "friendly-dock.") {
			command = "dock #77 #78"
		}
		if strings.HasPrefix(stage, "peer-undock.") {
			command = "launch #77"
		}
		if strings.HasPrefix(stage, "probe-land.") {
			command = "land #78 #77"
		}
		switch stage {
		case "friendly-dock.preinstall", "peer-undock.preinstall", "probe-land.preinstall", "factory-build.preinstall", "cargo-build.preinstall", "probe-build.preinstall", "probe-launch.preinstall":
			state.testHooks = &testHooks{beforeCommandInstall: boundary}
		case "friendly-dock.installed", "peer-undock.installed", "probe-land.installed", "factory-build.installed", "cargo-build.installed", "probe-build.installed", "probe-launch.installed":
			state.testHooks = &testHooks{afterCommandInstall: boundary}
		}
	})
	store.SetMetaFault(func(op string) error {
		if op == stage {
			boundary()
		}
		return nil
	})
	if e := state.HandleCommand(ctxT(t, 20*time.Second), 1, 0, command, &recordingResponder{}); e != nil {
		t.Fatal(e)
	}
	t.Fatal("escaped selected crash boundary")
}
func TestPlanetFactoryBuildRealProcessCrash(t *testing.T) {
	checkPlanetFactoryCrashes(t, false)
}

func TestPlanetFactoryBuildAutoloadRealProcessCrash(t *testing.T) {
	t.Parallel()
	checkPlanetFactoryCrashes(t, true)
}

func checkPlanetFactoryCrashes(t *testing.T, autoload bool) {
	t.Helper()
	for _, free := range []bool{false, true} {
		for _, stage := range []string{"factory-build.before", "factory-build.check:1", "factory-build.validated", "factory-build.write:1", "factory-build.write:2", "factory-build.write:3", "factory-build.before-commit", "factory-build.after-commit", "factory-build.preinstall", "factory-build.installed"} {
			t.Run(fmt.Sprintf("free=%t/%s", free, stage), func(t *testing.T) {
				f := newFactoryBuildFixture(t, free)
				if autoload {
					enableFactoryAutoload(t, f, 7, 0)
				}
				in := factoryBuildInput(t, f)
				if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
					t.Fatal(e)
				}
				schedule, e := f.store.GetSchedule()
				if e != nil || schedule == nil {
					t.Fatal(e)
				}
				before := commandFixtureRecords(t, commandFixtureImageStopped(t, &planetCommandFixture{actorEnrollmentFixture: f.actorEnrollmentFixture}))
				want := factoryBuildExpected(t, before, in)
				killConstructionAt(t, f, stage, schedule.SavedAt)
				got := commandFixtureRecords(t, commandFixtureImageStopped(t, &planetCommandFixture{actorEnrollmentFixture: f.actorEnrollmentFixture}))
				committed := stage == "factory-build.after-commit" || stage == "factory-build.preinstall" || stage == "factory-build.installed"
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

					got, e := f.store.GetShip(in.Input.Number)
					if e != nil || (got != nil) != committed {
						t.Fatal("turn reverted crash construction outcome", e)
					}

				}
			})
		}
	}
}

func killConstructionAt(t *testing.T, f *factoryFixture, stage string, saved time.Time) {
	t.Helper()
	if e := f.store.Close(); e != nil {
		t.Fatal(e)
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, exe, "-test.run=^TestPlanetFactoryBuildCrashChild$", "-test.v") //nolint:gosec // own Go test executable and fixed arguments
	child.Env = append(os.Environ(), "GB_FACTORY_BUILD_CHILD=1", "GB_FACTORY_BUILD_PATH="+f.path, "GB_FACTORY_BUILD_STAGE="+stage, "GB_FACTORY_BUILD_NOW="+saved.Format(time.RFC3339Nano))
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
			if strings.TrimSpace(scan.Text()) == "GB_FACTORY_BUILD_BOUNDARY" {
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
}
