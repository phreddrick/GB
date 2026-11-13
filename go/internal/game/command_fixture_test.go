package game

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

// Command tests need a playable empire, but do not test its initial enrollment.
// Build that prerequisite through the real actor once. Only immutable bytes,
// value-only directories and policy callbacks over immutable credentials escape
// the seed; every consumer gets its own file, Store, actor, config and policy.
// Enrollment/bootstrap tests continue to use newEnrollmentActor directly.
var commandFixtureSeed struct {
	sync.Once
	image     string
	dirs      [model.MaxPlayers][model.MaxGovernors + 1]model.Directory
	policy    EnrollmentPolicy
	placement db.PlacementRecord
	actorJSON string
}

// commandFixtureImage is ONLY for the exclusively owned disposable seed/test
// actors below. Read synchronously on the sole writer's goroutine, after all
// initialization/enrollment writes return. No hooks or external writer have
// access to the seed, and a turn or shutdown cannot overlap this callback.
// bbolt has no autonomous writer. This is not a live-store backup interface.
func commandFixtureImage(t *testing.T, f *actorEnrollmentFixture) string {
	t.Helper()
	var image string
	onEnrollmentActor(t, f, func() {
		b, err := os.ReadFile(f.path)
		if err != nil {
			t.Error(err)
			return
		}
		image = string(b)
	})
	if image == "" {
		t.Fatal("empty command fixture image")
	}
	return image
}

func newCommandEnrollmentActor(t *testing.T) (*actorEnrollmentFixture, db.PlacementRecord) {
	t.Helper()
	commandFixtureSeed.Do(func() {
		seed := newEnrollmentActor(t)
		commandFixtureSeed.placement = enrollActor(t, seed, actorEnrollmentRequest("operator")).Placement
		commandFixtureSeed.image = commandFixtureImage(t, seed)
		onEnrollmentActor(t, seed, func() { commandFixtureSeed.dirs = seed.state.dirs })
		commandFixtureSeed.actorJSON = actorEnrollmentSnapshot(t, seed)
		commandFixtureSeed.policy = *seed.policy
	})
	if commandFixtureSeed.image == "" {
		t.Fatal("command fixture seed initialization failed")
	}
	f := &actorEnrollmentFixture{path: filepath.Join(t.TempDir(), "enrollment.db"), log: &enrollmentLog{}}
	// Conversion from immutable string makes a private buffer; no borrowed mmap,
	// file, slice, model, Store uncertainty guard or test hook is shared.
	if err := os.WriteFile(f.path, []byte(commandFixtureSeed.image), 0600); err != nil {
		t.Fatal(err)
	}
	f.cfg = testConfig(t, f.path)
	var err error
	f.store, err = db.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	// A cached prerequisite must not inherit an aging turn deadline. Use the
	// same fresh schedule construction as newEnrollmentActor, with this file's
	// own timestamp. No test/process clock or scheduling limit is changed.
	now := time.Now().UTC()
	clock := &State{cfg: f.cfg}
	if err := f.store.PutSchedule(clock.scheduleRecord(clock.freshSchedule(now), now)); err != nil {
		t.Fatal(err)
	}
	policy := commandFixtureSeed.policy
	f.policy = &policy
	f.open(t)
	// Directories are actor-local session context, not persisted world records.
	// Preserve exactly the same initial home scope as real enrollment installs.
	onEnrollmentActor(t, f, func() { f.state.dirs = commandFixtureSeed.dirs })
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = f.state.Stop(ctx)
		_ = f.store.Close()
	})
	return f, commandFixtureSeed.placement
}

// Compare every logical bbolt record, including metadata, auxiliary buckets and
// free lists. A fresh fixture legitimately has its own three schedule times.
func commandFixtureRecords(t *testing.T, image string) map[string]string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if err := os.WriteFile(path, []byte(image), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	rows := map[string]string{}
	err = store.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, bucket *bolt.Bucket) error {
			rows[fmt.Sprintf("bucket:%x", name)] = fmt.Sprint(bucket.Sequence())
			return bucket.ForEach(func(k, v []byte) error {
				if v == nil {
					return fmt.Errorf("unexpected nested fixture bucket")
				}
				value := string(v)
				if string(name) == string(db.BucketMeta) && string(k) == string(db.KeySchedule) {
					var schedule db.ScheduleRecord
					if err := json.Unmarshal(v, &schedule); err != nil {
						return err
					}
					schedule.SavedAt = time.Time{}
					schedule.NextUpdate = time.Time{}
					schedule.NextSegment = time.Time{}
					b, err := json.Marshal(schedule)
					if err != nil {
						return err
					}
					value = string(b)
				}
				rows[fmt.Sprintf("%x/%x", name, k)] = value
				return nil
			})
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func assertCommandFixtureSeed(t *testing.T, f *actorEnrollmentFixture) {
	t.Helper()
	if actorEnrollmentSnapshot(t, f) != commandFixtureSeed.actorJSON {
		t.Fatal("command fixture differs from the real enrolled actor world/directories")
	}
	want := commandFixtureRecords(t, commandFixtureSeed.image)
	got := commandFixtureRecords(t, commandFixtureImage(t, f))
	if !reflect.DeepEqual(got, want) {
		t.Fatal("command fixture did not preserve every enrolled database record")
	}
	onEnrollmentActor(t, f, func() {
		if !f.state.sched.nextUpdate.After(f.state.now()) || !f.state.sched.nextSegment.After(f.state.now()) || f.state.sched.nextUpdate.Sub(f.state.sched.nextSegment) != f.state.updateInterval()-f.state.segmentInterval() {
			t.Error("fixture schedule is not fresh or uses different intervals")
		}
	})
}

func TestCommandFixtureMatchesRealEnrollment(t *testing.T) {
	before := time.Now().UTC()
	f, placement := newCommandEnrollmentActor(t)
	after := time.Now().UTC()
	assertCommandFixtureSeed(t, f)
	onEnrollmentActor(t, f, func() {
		anchor := f.state.sched.nextUpdate.Add(-f.state.updateInterval())
		if anchor.Before(before) || anchor.After(after) || f.state.sched.numUpdates != 0 || f.state.sched.numSegments != 0 {
			t.Error("fixture inherited seed deadlines or turn counters")
		}
	})
	// Independently regenerate and enroll rather than only testing copy fidelity.
	fresh := newEnrollmentActor(t)
	actual := enrollActor(t, fresh, actorEnrollmentRequest("operator")).Placement
	if actorEnrollmentSnapshot(t, fresh) != actorEnrollmentSnapshot(t, f) {
		t.Fatal("cached prerequisite differs from independently initialized/enrolled world")
	}
	// Those two fields bind the fresh timestamp; GetPlacement validates its hash.
	actual.CreatedAt, placement.CreatedAt = time.Time{}, time.Time{}
	actual.RecordHash, placement.RecordHash = "", ""
	if actual != placement {
		t.Fatal("historical placement identity changed")
	}
}

func TestCommandFixtureIsolation(t *testing.T) {
	t.Parallel()
	for i := 0; i < 8; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			f, placement := newCommandEnrollmentActor(t)
			other, _ := newCommandEnrollmentActor(t)
			if f.path == other.path || f.store == other.store || f.state == other.state || f.cfg == other.cfg || f.policy == other.policy || f.log == other.log {
				t.Fatal("mutable fixture infrastructure shared")
			}
			onEnrollmentActor(t, f, func() {
				st := f.state.stars[placement.Star]
				p := f.state.planets[placement.Star][placement.Planet]
				sh := f.state.ships[placement.GovernmentShip]
				r := f.state.races[0]
				st.Name = "isolated star"
				p.Sectors[0].Resource++
				p.Info[0].Resource++
				sh.Resource++
				r.Governor[0].Money++
				f.state.sdata.AP[0]++
				f.state.dirs[0][0].Prompt = "isolated directory"
				for _, err := range []error{f.store.PutStar(st), f.store.PutPlanet(p), f.store.PutShip(sh), f.store.PutRace(r), f.store.PutStarData(f.state.sdata)} {
					if err != nil {
						t.Error(err)
					}
				}
				f.state.testHooks.beforeCommandInstall = func() { panic("private hook") }
			})
			f.policy.Prepare = nil
			f.cfg.DefaultUpdateMinutes++
			f.store.SetWriteFence(true)
			assertCommandFixtureSeed(t, other)
			later, _ := newCommandEnrollmentActor(t)
			assertCommandFixtureSeed(t, later)
			f.store.SetWriteFence(false)
		})
	}
}
