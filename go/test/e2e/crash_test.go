package e2e

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
)

// TestKillDuringTurnLeavesLastCommit (CR-18, plan 8.11 T1): a real server
// process is killed with SIGKILL while a turn is running. The store must be
// exactly the last commit afterwards (same logical digest, same commit
// record, schedule not advanced), the next start must serve from it, and
// the interrupted turn must be neither counted nor half applied.
func TestKillDuringTurnLeavesLastCommit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gb.db")

	// Initialise through the real admin path, then stop orderly so that the
	// store holds a clean commit to compare against.
	s := startServer(t, dbPath, nil)
	if code, out := s.gbctl("universe", "init", "-seed", "5", "-stars", "3", "-min-planets", "1", "-max-planets", "2"); code != 0 {
		t.Fatalf("universe init exit %d: %s", code, out)
	}
	if code := s.signal(syscall.SIGTERM, 25*time.Second); code != 0 {
		t.Fatalf("orderly stop exit %d\n%s", code, s.logs.String())
	}
	before, recBefore := storeDigest(t, dbPath)
	if recBefore.Kind != "shutdown" || recBefore.Turn != 0 {
		t.Fatalf("commit record after init and stop: %+v", recBefore)
	}

	// Restart with a slow turn, trigger an update, kill the process mid-turn.
	s = startServer(t, dbPath, map[string]string{"GB_TEST_HOOKS": "1", "GB_TEST_TURN_DELAY": "8s"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.gbctl("-timeout", "30s", "update")
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !s.hasLog("turn starting") {
		if time.Now().After(deadline) {
			t.Fatalf("turn did not start\n%s", s.logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond) // well inside the 8 s turn body
	if err := s.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-s.exited
	<-done
	if s.hasLog("turn complete") {
		t.Fatalf("the turn completed before the kill; test invalid\n%s", s.logs.String())
	}

	after, recAfter := storeDigest(t, dbPath)
	if after != before {
		t.Fatal("store changed by a turn that was killed before its commit")
	}
	if *recAfter != *recBefore {
		t.Fatalf("commit record changed: %+v -> %+v", recBefore, recAfter)
	}

	// Recovery: the next start serves from the last commit, counts no turn,
	// and a later update commits normally.
	s = startServer(t, dbPath, nil)
	st := s.status()
	if st.Status.Mode != "SERVING" || st.Status.NumUpdates != 0 || st.Status.Stars != 3 || st.Status.Universe == nil || st.Status.Universe.Seed != 5 {
		t.Fatalf("after kill and restart: %+v", st.Status)
	}
	if s.hasLog("CORRUPT_STORE") {
		t.Fatalf("store refused after a kill\n%s", s.logs.String())
	}
	if code, out := s.gbctl("-timeout", "60s", "update"); code != 0 {
		t.Fatalf("update after recovery exit %d: %s", code, out)
	}
	if st := s.status(); st.Status.NumUpdates != 1 {
		t.Fatalf("update after recovery not counted: %+v", st.Status)
	}
	if code := s.signal(syscall.SIGTERM, 25*time.Second); code != 0 {
		t.Fatalf("final stop exit %d", code)
	}
	_, recFinal := storeDigest(t, dbPath)
	if recFinal.Turn != 1 || recFinal.CommitSeq != recBefore.CommitSeq+2 { // update + shutdown
		t.Fatalf("final commit record %+v (before %+v)", recFinal, recBefore)
	}
}

// storeDigest opens the (closed) store, returns its logical digest and
// commit record, and closes it again.
func storeDigest(t *testing.T, dbPath string) (string, *db.CommitRecord) {
	t.Helper()
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	d, err := store.WorldDigest()
	if err != nil {
		t.Fatal(err)
	}
	rec, err := store.GetCommitRecord()
	if err != nil || rec == nil {
		t.Fatalf("commit record: %+v %v", rec, err)
	}
	return d, rec
}
