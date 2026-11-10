package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/enrollmentapi"
	bolt "go.etcd.io/bbolt"
)

var acceptanceBuild struct {
	sync.Once
	path string
	err  error
}

// The barrier implementation exists only in this separately tagged binary.
// The ordinary server has no environment-controlled enrollment barrier.
func cr07fCrashBinary(t *testing.T) string {
	t.Helper()
	binaries(t)
	acceptanceBuild.Do(func() {
		acceptanceBuild.path = filepath.Join(binDir, "gb-server-enrollment-acceptance")
		cmd := exec.Command("go", "build", "-tags", "gb_enrollment_acceptance", "-o", acceptanceBuild.path, "../../cmd/gb-server") //nolint:gosec // builds only this repository's test binary
		if out, e := cmd.CombinedOutput(); e != nil {
			acceptanceBuild.err = fmt.Errorf("acceptance binary build: %w: %s", e, out)
		}
	})
	if acceptanceBuild.err != nil {
		t.Fatal(acceptanceBuild.err)
	}
	return acceptanceBuild.path
}

// Hash every key/value and bucket sequence in a CLOSED disposable store.
// Startup legitimately rewrites only schedule.saved_at; normalize that one
// field while retaining every schedule counter, deadline and other record.
// This covers placement metadata and free lists beyond WorldDigest's scope.
func cr07fBucketEvidence(t *testing.T, path string) map[string][32]byte {
	t.Helper()
	store, e := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if e != nil {
		t.Fatal("readonly crash evidence open", e)
	}
	defer func() { _ = store.Close() }()
	result := map[string][32]byte{}
	e = store.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			var entries []any
			entries = append(entries, b.Sequence())
			e := b.ForEach(func(k, v []byte) error {
				if v == nil {
					return fmt.Errorf("unexpected nested bucket")
				}
				if bytes.Equal(name, db.BucketMeta) && bytes.Equal(k, db.KeySchedule) {
					var schedule db.ScheduleRecord
					if e := json.Unmarshal(v, &schedule); e != nil {
						return e
					}
					schedule.SavedAt = time.Time{}
					var e error
					v, e = json.Marshal(schedule)
					if e != nil {
						return e
					}
				}
				entries = append(entries, []any{k, v})
				return nil
			})
			if e != nil {
				return e
			}
			raw, e := json.Marshal(entries)
			if e != nil {
				return e
			}
			result[string(name)] = sha256.Sum256(raw)
			return nil
		})
	})
	if e != nil {
		t.Fatal("crash evidence read", e)
	}
	return result
}

func cr07fReachedStage(logs, stage string) bool {
	for _, line := range strings.Split(logs, "\n") {
		var record struct {
			Message string `json:"msg"`
			Stage   string `json:"stage"`
		}
		if json.Unmarshal([]byte(line), &record) == nil && record.Message == "CR07F enrollment barrier" && record.Stage == stage {
			return true
		}
	}
	return false
}

func TestCR07FEnrollmentCrashWindows(t *testing.T) {
	for _, stage := range []string{"before-request", "enrollment.before", "enrollment.construct", "enrollment.race", "enrollment.before-commit", "enrollment.after-commit"} {
		t.Run(stage, func(t *testing.T) {
			f := newCR07FFixture(t)
			// An eligible absent-record free-list entry proves both rollback of
			// a staged pop and atomic consumption on the successful commit.
			store, e := db.Open(f.path)
			if e != nil {
				t.Fatal(e)
			}
			e = store.DeleteShip(7)
			closeErr := store.Close()
			if e != nil || closeErr != nil {
				t.Fatal("disposable free-list fixture setup failed")
			}
			f.base = cr07fRead(t, f.path, f.requests)
			before := cr07fBucketEvidence(t, f.path)
			f.manifest.BootstrapEnabled = true
			cr07fJSON(t, f.authority, f.manifest)
			_, ctl := binaries(t)
			s := startServerBinary(t, f.path, map[string]string{"GB_ENROLL_AUTHORITY_FILE": f.authority, "GB_ACCEPTANCE_ENROLL_STAGE": stage}, cr07fCrashBinary(t), ctl)
			f.processes = append(f.processes, s)
			type reply struct {
				code int
				body string
			}
			var done chan reply
			if stage != "before-request" {
				requestFile := filepath.Join(f.dir, "crash-request.json")
				cr07fJSON(t, requestFile, f.requests[0])
				done = make(chan reply, 1)
				go func() {
					code, body := s.gbctl("-timeout", "15s", "enroll", "-f", requestFile)
					done <- reply{code, body}
				}()
				deadline := time.Now().Add(10 * time.Second)
				for !cr07fReachedStage(s.logs.String(), stage) {
					select {
					case <-s.exited:
						t.Fatal("child exited before the requested transaction stage")
					case <-done:
						t.Fatal("HTTP request returned before the transaction barrier")
					default:
					}
					if time.Now().After(deadline) {
						t.Fatal("transaction stage barrier not reached")
					}
					time.Sleep(time.Millisecond)
				}
			}
			// Kill only this test's owned disposable subprocess. No shutdown
			// handler can flush an in-memory proposal across this boundary.
			if e := s.cmd.Process.Signal(syscall.SIGKILL); e != nil {
				t.Fatal(e)
			}
			select {
			case <-s.exited:
			case <-time.After(10 * time.Second):
				t.Fatal("SIGKILL child did not exit")
			}
			if done != nil {
				select {
				case response := <-done:
					f.output = append(f.output, response.body)
					if response.code == 0 {
						t.Fatal("unreceived HTTP response was reported as confirmed success")
					}
				case <-time.After(20 * time.Second):
					t.Fatal("disconnected CLI did not finish")
				}
			}
			after := cr07fBucketEvidence(t, f.path)
			world := cr07fRead(t, f.path, f.requests)
			if stage != "enrollment.after-commit" {
				if !reflect.DeepEqual(before, after) || world.audit(t) != f.base.audit(t) {
					t.Fatal("interrupted uncommitted transaction changed records or allocator")
				}
			} else {
				if world.placements[0] == nil || world.placements[0].GovernmentShip != 7 || world.ticket.Number != 8 || !reflect.DeepEqual(f.base.commit, world.commit) {
					t.Fatal("post-commit crash lost placement, free-list pop or CR18 invariant")
				}
				r := world.placements[0]
				view := enrollmentapi.View{Placement: &enrollmentapi.Placement{Player: r.Player, Star: r.Star, Planet: r.Planet, CapitalX: r.CapitalX, CapitalY: r.CapitalY, GovernmentShip: r.GovernmentShip, CreatedAt: r.CreatedAt}}
				f.initial(t, world, []enrollmentapi.View{view})
			}
			// Only known-aborted writes and a known successful bbolt commit
			// are restarted here. This is not uncertain-write reconciliation.
			s = f.start(t, true, nil)
			result := f.enroll(t, s, f.requests[0], 0, "")
			if result.Replay != (stage == "enrollment.after-commit") || result.Placement.GovernmentShip != 7 {
				t.Fatal("restart did not retain the correct atomic outcome")
			}
			f.stop(t, s)
			f.initial(t, cr07fRead(t, f.path, f.requests), []enrollmentapi.View{result})
			s = f.start(t, true, nil)
			f.play(t, s, 0, result)
			f.stop(t, s)
			f.playable(t, cr07fRead(t, f.path, f.requests), []enrollmentapi.View{result})
		})
	}
}

func TestCR07FProductionBuildIgnoresCrashHook(t *testing.T) {
	f := newCR07FFixture(t)
	s := f.start(t, true, map[string]string{"GB_ACCEPTANCE_ENROLL_STAGE": "enrollment.before"})
	result := f.enroll(t, s, f.requests[0], 0, "")
	if strings.Contains(s.logs.String(), "CR07F enrollment barrier") {
		t.Fatal("acceptance-only control exists in ordinary server")
	}
	f.stop(t, s)
	f.initial(t, cr07fRead(t, f.path, f.requests), []enrollmentapi.View{result})
}
