package e2e

import (
	"encoding/json"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Durable maintenance mode across process boundaries (CR-43, ADR-0010):
// crash (SIGKILL) and orderly shutdown (SIGTERM) after a quiesce both leave
// a store that starts QUIESCED and Ready; a resume followed by a crash
// leaves a store that starts SERVING. Every assertion goes through the real
// binaries: gbctl against the admin API, /readyz over HTTP, and the logs.

type maintReply struct {
	OK     bool `json:"ok"`
	Status struct {
		Mode        string    `json:"mode"`
		GameReady   bool      `json:"game_ready"`
		NumUpdates  int       `json:"num_updates"`
		NumSegments int       `json:"num_segments"`
		NextUpdate  time.Time `json:"next_update"`
		Maintenance *struct {
			Durable bool   `json:"durable"`
			Error   string `json:"error"`
		} `json:"maintenance"`
	} `json:"status"`
}

func (s *server) maintStatus() maintReply {
	s.t.Helper()
	code, out := s.gbctl("status")
	if code != 0 {
		s.t.Fatalf("gbctl status exit %d: %s", code, out)
	}
	var r maintReply
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		s.t.Fatalf("gbctl status output: %v\n%s", err, out)
	}
	return r
}

// killHard sends SIGKILL and waits for the process to be gone (a crash: no
// shutdown commit, no deferred code).
func (s *server) killHard() {
	s.t.Helper()
	if err := s.cmd.Process.Kill(); err != nil {
		s.t.Fatal(err)
	}
	select {
	case <-s.exited:
	case <-time.After(20 * time.Second):
		s.t.Fatal("process did not die after SIGKILL")
	}
}

func expectReadyz(t *testing.T, s *server, code int, mode string, gameReady, maintenance bool) map[string]any {
	t.Helper()
	got, body, err := s.get("/readyz")
	if err != nil || got != code || body["mode"] != mode || body["game_ready"] != gameReady {
		t.Fatalf("/readyz code=%d body=%v err=%v (want %d %s game_ready=%v)", got, body, err, code, mode, gameReady)
	}
	if maintenance && body["maintenance"] != true {
		t.Fatalf("/readyz must report maintenance: %v", body)
	}
	if !maintenance && body["maintenance"] != nil {
		t.Fatalf("/readyz must not report maintenance: %v", body)
	}
	return body
}

func TestQuiesceSurvivesCrashAndRestart(t *testing.T) {
	dbPath := t.TempDir() + "/gb.db"
	s := startServer(t, dbPath, nil)
	if code, out := s.gbctl("universe", "init", "-seed", "4242", "-stars", "3", "-min-planets", "1", "-max-planets", "1"); code != 0 {
		t.Fatalf("init exit %d: %s", code, out)
	}
	if code, out := s.gbctl("update"); code != 0 {
		t.Fatalf("update exit %d: %s", code, out)
	}
	st0 := s.maintStatus()
	if st0.Status.Mode != "SERVING" || !st0.Status.GameReady || st0.Status.Maintenance != nil || st0.Status.NumUpdates != 1 {
		t.Fatalf("status before quiesce: %+v", st0.Status)
	}
	expectReadyz(t, s, 200, "SERVING", true, false)

	// 1. Quiesce: Ready (200) for the orchestrator, game not ready, durable.
	if code, out := s.gbctl("quiesce"); code != 0 {
		t.Fatalf("quiesce exit %d: %s", code, out)
	}
	st1 := s.maintStatus()
	if st1.Status.Mode != "QUIESCED" || st1.Status.GameReady || st1.Status.Maintenance == nil || !st1.Status.Maintenance.Durable {
		t.Fatalf("status after quiesce: %+v maint=%+v", st1.Status, st1.Status.Maintenance)
	}
	body := expectReadyz(t, s, 200, "QUIESCED", false, true)
	if body["maintenance_durable"] != true {
		t.Fatalf("readyz must say the quiesce is durable: %v", body)
	}
	if code, _ := s.gbctl("update"); code != 4 {
		t.Fatalf("update while quiesced must be refused (exit 4), got %d", code)
	}

	// 2. Crash: no shutdown commit. The next start must be QUIESCED.
	s.killHard()
	s2 := startServer(t, dbPath, nil)
	if !s2.hasLog("starting QUIESCED") {
		t.Fatalf("restart after a quiesce must log the durable maintenance start\n%s", s2.logs.String())
	}
	st2 := s2.maintStatus()
	if st2.Status.Mode != "QUIESCED" || st2.Status.GameReady || st2.Status.Maintenance == nil || !st2.Status.Maintenance.Durable {
		t.Fatalf("status after crash+restart: %+v maint=%+v", st2.Status, st2.Status.Maintenance)
	}
	if st2.Status.NumUpdates != 1 || st2.Status.NumSegments != st1.Status.NumSegments || !st2.Status.NextUpdate.Equal(st1.Status.NextUpdate) {
		t.Fatalf("counters or schedule changed across the quiesced restart: %+v -> %+v", st1.Status, st2.Status)
	}
	expectReadyz(t, s2, 200, "QUIESCED", false, true)
	if s2.hasLog("turn starting") {
		t.Fatalf("no turn may run on a quiesced start\n%s", s2.logs.String())
	}
	if code, _ := s2.gbctl("segment"); code != 4 {
		t.Fatalf("segment while quiesced must be refused (exit 4), got %d", code)
	}

	// 3. Orderly shutdown while quiesced (the shutdown commit must keep the record).
	if code := s2.signal(syscall.SIGTERM, 25*time.Second); code != 0 {
		t.Fatalf("SIGTERM while quiesced: exit %d\n%s", code, s2.logs.String())
	}
	if !s2.hasLog("shutdown: committed") {
		t.Fatalf("orderly shutdown while quiesced must commit\n%s", s2.logs.String())
	}
	s3 := startServer(t, dbPath, nil)
	st3 := s3.maintStatus()
	if st3.Status.Mode != "QUIESCED" || st3.Status.NumUpdates != 1 {
		t.Fatalf("status after orderly restart: %+v", st3.Status)
	}
	expectReadyz(t, s3, 200, "QUIESCED", false, true)

	// 4. Resume: durable clear; a crash right after must restart SERVING.
	if code, out := s3.gbctl("resume"); code != 0 {
		t.Fatalf("resume exit %d: %s", code, out)
	}
	st4 := s3.maintStatus()
	if st4.Status.Mode != "SERVING" || !st4.Status.GameReady || st4.Status.Maintenance != nil {
		t.Fatalf("status after resume: %+v", st4.Status)
	}
	expectReadyz(t, s3, 200, "SERVING", true, false)
	s3.killHard()
	s4 := startServer(t, dbPath, nil)
	st5 := s4.maintStatus()
	if st5.Status.Mode != "SERVING" || st5.Status.Maintenance != nil || st5.Status.NumUpdates != 1 {
		t.Fatalf("status after resume+crash+restart: %+v", st5.Status)
	}
	if s4.hasLog("starting QUIESCED") {
		t.Fatal("no maintenance start expected after a resume")
	}
	expectReadyz(t, s4, 200, "SERVING", true, false)
	// A turn still commits after the whole cycle (CR-18 path untouched).
	if code, out := s4.gbctl("segment"); code != 0 {
		t.Fatalf("segment after resume: exit %d %s", code, out)
	}
	if st := s4.maintStatus(); st.Status.NumSegments != 1 {
		t.Fatalf("segment did not count: %+v", st.Status)
	}
	if code := s4.signal(syscall.SIGTERM, 25*time.Second); code != 0 {
		t.Fatalf("final shutdown exit %d", code)
	}
	for _, bad := range []string{"CORRUPT_STORE", "engine halted", "panic"} {
		if strings.Contains(s.logs.String()+s2.logs.String()+s3.logs.String()+s4.logs.String(), bad) {
			t.Fatalf("unexpected %q in the logs", bad)
		}
	}
}
