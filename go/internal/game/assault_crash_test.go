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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

const assaultInstalled = "assault.installed"

// Only this test executable reads fault controls. No production environment
// switch, debug route or alternate writer is introduced.
func TestGroundAssaultCrashChild(t *testing.T) {
	if os.Getenv("CR17_ASSAULT_CHILD") != "1" {
		return
	}
	store, e := db.Open(os.Getenv("CR17_ASSAULT_PATH"))
	if e != nil {
		t.Fatal(e)
	}
	now, e := time.Parse(time.RFC3339Nano, os.Getenv("CR17_ASSAULT_NOW"))
	if e != nil {
		t.Fatal(e)
	}
	state, e := New(testConfig(t, os.Getenv("CR17_ASSAULT_PATH")), store, Options{Now: func() time.Time { return now }})
	if e != nil || state.Mode() != ModeServing {
		t.Fatal("child startup", e)
	}
	boundary := func() { fmt.Println("CR17_ASSAULT_BOUNDARY"); <-make(chan struct{}) }
	stage := os.Getenv("CR17_ASSAULT_STAGE")
	if e := state.request(ctxT(t, 10*time.Second), opTest, func() error {
		home := state.races[0].Governor[0]
		state.dirs[0][0] = model.Directory{Level: model.LevelPlan, StarNum: home.HomeSystem, PlanetNum: home.HomePlanetNum}
		state.assaultRandom = highAssaultDraw
		if stage == assaultInstalled {
			state.testHooks = &testHooks{afterCommandInstall: boundary}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if stage != assaultInstalled {
		store.SetMetaFault(func(op string) error {
			if op == stage {
				boundary()
			}
			return nil
		})
	}
	verb := "move"
	if os.Getenv("CR17_ASSAULT_MILITARY") == "1" {
		verb = "deploy"
	}
	if e := state.HandleCommand(ctxT(t, 20*time.Second), 1, 0, verb+" 0,0 6 1000", &recordingResponder{}); e != nil {
		t.Fatal(e)
	}
	t.Fatal("escaped actual assault boundary")
}
func assaultExpectedRecords(t *testing.T, before map[string]string, in db.GroundAssault) map[string]string {
	t.Helper()
	out := map[string]string{}
	for k, v := range before {
		out[k] = v
	}
	add := func(bucket, key []byte, value any) {
		b, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		out[fmt.Sprintf("%x/%x", bucket, key)] = string(b)
	}
	add(db.BucketPlanets, db.PlanetKey(in.Input.Planet.StarNum, in.Input.Planet.PlanetNum), in.Proposed.Planet)
	add(db.BucketStars, db.StarKey(in.Input.Star.Number), in.Proposed.Star)
	add(db.BucketRaces, db.RaceKey(1), in.Proposed.Attacker)
	add(db.BucketRaces, db.RaceKey(2), in.Proposed.Defender)
	news, tele, e := db.AssaultMessages(in)
	if e != nil {
		t.Fatal(e)
	}
	out[fmt.Sprintf("%x/%x", db.BucketNews, db.NewsKey(model.NewsCombat, 1))] = news.Text
	bucket := fmt.Sprintf("bucket:%x", db.BucketTelegrams)
	seq, e := strconv.ParseUint(out[bucket], 10, 64)
	if e != nil {
		t.Fatal(e)
	}
	seq++
	out[bucket] = strconv.FormatUint(seq, 10)
	out[fmt.Sprintf("%x/%x", db.BucketTelegrams, db.TelegramKeyPG(2, 0, seq))] = tele.Text
	return out
}

// Twenty real kills: each civilian/military variant crosses all four world
// writes, both effect writes, and the precommit/commit/installation boundaries.
func TestGroundAssaultRealProcessCrash(t *testing.T) {
	t.Parallel()
	for _, mil := range []bool{false, true} {
		for _, stage := range []string{"assault.before", "assault.write:1", "assault.write:2", "assault.write:3", "assault.write:4", "assault.news", "assault.telegram", "assault.before-commit", "assault.after-commit", assaultInstalled} {
			t.Run(fmt.Sprintf("military=%t/%s", mil, stage), func(t *testing.T) {
				f := newGroundAssaultFixture(t, mil)
				want := assaultPrepared(t, f, mil)
				if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
					t.Fatal(e)
				}
				schedule, e := f.store.GetSchedule()
				if e != nil || schedule == nil {
					t.Fatal(e)
				}
				want.At = schedule.SavedAt
				rawMeta := map[string]string{}
				for _, key := range [][]byte{db.KeyCommit, db.KeySchedule, db.KeyMaintenance, db.KeyUniverse} {
					b, e := f.store.GetMeta(key)
					if e != nil {
						t.Fatal(e)
					}
					rawMeta[string(key)] = string(b)
				}
				before := assaultRecords(t, f)
				after := assaultExpectedRecords(t, before, want)
				if e := f.store.Close(); e != nil {
					t.Fatal(e)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				child := exec.CommandContext(ctx, "/proc/self/exe", "-test.run=^TestGroundAssaultCrashChild$", "-test.v")
				military := "0"
				if mil {
					military = "1"
				}
				child.Env = append(os.Environ(), "CR17_ASSAULT_CHILD=1", "CR17_ASSAULT_PATH="+f.path, "CR17_ASSAULT_STAGE="+stage, "CR17_ASSAULT_MILITARY="+military, "CR17_ASSAULT_NOW="+want.At.Format(time.RFC3339Nano))
				stdout, e := child.StdoutPipe()
				if e != nil {
					t.Fatal(e)
				}
				var stderr bytes.Buffer
				child.Stderr = &stderr
				if e := child.Start(); e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { _ = child.Process.Kill() })
				ready, done := make(chan struct{}), make(chan struct{})
				go func() {
					defer close(done)
					scan := bufio.NewScanner(stdout)
					for scan.Scan() {
						if strings.TrimSpace(scan.Text()) == "CR17_ASSAULT_BOUNDARY" {
							close(ready)
						}
					}
				}()
				select {
				case <-ready:
				case <-done:
					t.Fatal("child exited before boundary", stderr.String())
				case <-ctx.Done():
					t.Fatal("boundary timed out", stderr.String())
				}
				if e := child.Process.Signal(syscall.SIGKILL); e != nil {
					t.Fatal(e)
				}
				<-done
				e = child.Wait()
				var exit *exec.ExitError
				if !errors.As(e, &exit) {
					t.Fatal("child not killed", e)
				}
				status, ok := exit.Sys().(syscall.WaitStatus)
				if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL || strings.Contains(stderr.String(), "WARNING: DATA RACE") {
					t.Fatal("invalid real crash", stderr.String())
				}
				f.store, e = db.Open(f.path)
				if e != nil {
					t.Fatal(e)
				}
				// The general fixture reader normalizes schedule timestamps; this
				// operation must preserve those raw bytes as well, including after
				// a real kill. No schedule mutation is excused by normalization.
				for key, before := range rawMeta {
					b, e := f.store.GetMeta([]byte(key))
					if e != nil || string(b) != before {
						t.Fatal("raw metadata changed during assault crash", key, e)
					}
				}
				committed := stage == "assault.after-commit" || stage == assaultInstalled
				wantRows := before
				if committed {
					wantRows = after
				}
				if !reflect.DeepEqual(assaultRecords(t, f), wantRows) {
					t.Fatal("crash produced partial world/messages/sequences or changed unrelated records")
				}
				// Restart only known-aborted or confirmed-committed test oracles. This does
				// not reopen/repair a genuinely uncertain handle as an automatic recovery.
				f.open(t)
				if f.state.Mode() != ModeServing {
					t.Fatal("complete store rejected")
				}
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
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
					if committed {
						p, e := f.store.GetPlanet(f.star, f.planet)
						if e != nil || p.Sectors[1].Owner != 1 || p.Info[1].NumSectsOwned != 0 {
							t.Fatal("later turn erased conquest", e)
						}
						news, _, e := f.store.ReadNewsFrom(model.NewsCombat, 0)
						if e != nil || len(news) != 1 {
							t.Fatal("report duplicated/lost", e)
						}
					}
				}
			})
		}
	}
}
