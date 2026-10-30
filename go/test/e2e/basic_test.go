// Package e2e runs gb-server and gbctl as real processes against temporary
// databases: the Phase 1 acceptance scenario (initialise, log in, command,
// forced update, orderly shutdown, restart with the same universe and
// schedule) and the signal-handling cases. Nothing here touches a real
// player database; every server binds 127.0.0.1 on ephemeral ports.
package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

// Test-only credentials for throwaway servers on loopback.
const (
	jwtSecret  = "0123456789abcdef0123456789abcdef-e2e"
	adminToken = "e2e-admin-token-0123456789" //nolint:gosec // G101: test fixture, not a real credential
)

var (
	buildOnce sync.Once
	binDir    string
	buildErr  error
)

// binaries builds gb-server and gbctl once per test run.
func binaries(t *testing.T) (server, gbctl string) {
	t.Helper()
	if testing.Short() {
		t.Skip("e2e: skipped in -short mode")
	}
	buildOnce.Do(func() {
		binDir, buildErr = os.MkdirTemp("", "gb-e2e-bin-")
		if buildErr != nil {
			return
		}
		for _, pkg := range []string{"gb-server", "gbctl"} {
			cmd := exec.Command("go", "build", "-o", filepath.Join(binDir, pkg), "../../cmd/"+pkg) //nolint:gosec // G204: builds this module's own commands
			cmd.Env = os.Environ()
			if out, err := cmd.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("build %s: %v\n%s", pkg, err, out)
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return filepath.Join(binDir, "gb-server"), filepath.Join(binDir, "gbctl")
}

func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		_ = os.RemoveAll(binDir)
	}
	os.Exit(code)
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// server is a running gb-server subprocess.
type server struct {
	t        *testing.T
	cmd      *exec.Cmd
	addr     string // player listener
	admin    string // admin listener URL
	logs     *logBuffer
	exited   chan struct{}
	exitErr  error
	dbPath   string
	gbctlBin string
}

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// startServer launches gb-server on the given database with extra
// environment and waits until /readyz answers (any code) or the process
// exits.
func startServer(t *testing.T, dbPath string, extraEnv map[string]string) *server {
	t.Helper()
	serverBin, gbctlBin := binaries(t)
	port, adminPort := freePort(t), freePort(t)
	s := &server{t: t, dbPath: dbPath, gbctlBin: gbctlBin, logs: &logBuffer{}, exited: make(chan struct{})}
	s.addr = fmt.Sprintf("127.0.0.1:%d", port)
	s.admin = fmt.Sprintf("http://127.0.0.1:%d", adminPort)
	cmd := exec.Command(serverBin) //nolint:gosec // G204: binary built by this test
	env := map[string]string{
		"GB_ADDR":             s.addr,
		"GB_ADMIN_ADDR":       fmt.Sprintf("127.0.0.1:%d", adminPort),
		"GB_DB_PATH":          dbPath,
		"GB_JWT_SECRET":       jwtSecret,
		"GB_ADMIN_TOKEN":      adminToken,
		"GB_UPDATE_MINUTES":   "60",
		"GB_MOVES_PER_UPDATE": "3",
		"GB_SHUTDOWN_TIMEOUT": "20",
		"GB_LOG_LEVEL":        "info",
	}
	for k, v := range extraEnv {
		env[k] = v
	}
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout = s.logs
	cmd.Stderr = s.logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.cmd = cmd
	go func() {
		s.exitErr = cmd.Wait()
		close(s.exited)
	}()
	t.Cleanup(func() {
		select {
		case <-s.exited:
		default:
			_ = cmd.Process.Kill()
			<-s.exited
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-s.exited:
			t.Fatalf("server exited during startup: %v\n%s", s.exitErr, s.logs.String())
		default:
		}
		if _, _, err := s.get("/readyz"); err == nil {
			return s
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server did not become reachable\n%s", s.logs.String())
	return nil
}

func (s *server) get(path string) (int, map[string]any, error) {
	resp, err := http.Get("http://" + s.addr + path) //nolint:gosec // G107: test server
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &body)
	return resp.StatusCode, body, nil
}

// gbctl runs the real gbctl binary against this server.
func (s *server) gbctl(args ...string) (int, string) {
	cmd := exec.Command(s.gbctlBin, append([]string{"-server", s.admin, "-json"}, args...)...) //nolint:gosec // G204: binary built by this test
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GBCTL_TOKEN=" + adminToken}
	out, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		code = -1
	}
	return code, string(out)
}

type gbctlReply struct {
	OK     bool `json:"ok"`
	Status struct {
		Mode        string    `json:"mode"`
		NextUpdate  time.Time `json:"next_update"`
		NextSegment time.Time `json:"next_segment"`
		NumUpdates  int       `json:"num_updates"`
		NumSegments int       `json:"num_segments"`
		Stars       int       `json:"stars"`
		Races       int       `json:"races"`
		Universe    *struct {
			Seed int64 `json:"seed"`
		} `json:"universe"`
	} `json:"status"`
}

func (s *server) status() gbctlReply {
	code, out := s.gbctl("status")
	if code != 0 {
		s.t.Fatalf("gbctl status exit %d: %s", code, out)
	}
	var r gbctlReply
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		s.t.Fatalf("gbctl status output: %v\n%s", err, out)
	}
	return r
}

// signal sends sig and waits for exit, returning the exit code.
func (s *server) signal(sig syscall.Signal, wait time.Duration) int {
	if err := s.cmd.Process.Signal(sig); err != nil {
		s.t.Fatal(err)
	}
	select {
	case <-s.exited:
	case <-time.After(wait):
		s.t.Fatalf("server did not exit within %s after %v\n%s", wait, sig, s.logs.String())
	}
	var ee *exec.ExitError
	if errors.As(s.exitErr, &ee) {
		return ee.ExitCode()
	}
	if s.exitErr != nil {
		s.t.Fatalf("wait: %v", s.exitErr)
	}
	return 0
}

// addFixtureRace writes a race directly into a database no server has open.
func addFixtureRace(t *testing.T, dbPath string, num int, name, password string) {
	t.Helper()
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	r := &model.Race{PlayerNum: num, Name: name, Password: string(hash), Governors: 1, IQ: 100, Tech: 1}
	r.Governor[0] = model.Governor{Name: name, Active: true, Money: 1000}
	if err := store.PutRace(r); err != nil {
		t.Fatal(err)
	}
}

func (s *server) login(race, pw string) string {
	resp, err := http.Post("http://"+s.addr+"/auth/login", "application/json", //nolint:gosec // G107: test server
		strings.NewReader(fmt.Sprintf(`{"race":%q,"password":%q,"governor":0}`, race, pw)))
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Token string `json:"token"`
	}
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		s.t.Fatalf("login: %d %s", resp.StatusCode, data)
	}
	_ = json.Unmarshal(data, &body)
	return body.Token
}

func (s *server) connect(token string) *websocket.Conn {
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+s.addr+"/ws?token="+token, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// readUntil reads text frames until one contains needle or the deadline.
func readUntil(t *testing.T, conn *websocket.Conn, needle string, d time.Duration) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(d))
	var all strings.Builder
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %q: %v (got %q)", needle, err, all.String())
		}
		all.Write(msg)
		if strings.Contains(all.String(), needle) {
			return all.String()
		}
	}
}

// logLines returns the parsed JSON log lines.
func (s *server) logLines() []map[string]any {
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(s.logs.String()))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err == nil {
			out = append(out, m)
		}
	}
	return out
}

func (s *server) hasLog(msgContains string) bool {
	for _, l := range s.logLines() {
		if m, _ := l["msg"].(string); strings.Contains(m, msgContains) {
			return true
		}
	}
	return false
}

// TestAcceptanceScenario is the Phase 1 end-to-end scenario.
func TestAcceptanceScenario(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gb.db")

	// 1. Fresh store: the server starts UNINITIALIZED and does not generate.
	s := startServer(t, dbPath, nil)
	code, body, _ := s.get("/readyz")
	if code != 200 || body["mode"] != "UNINITIALIZED" || body["game_ready"] != false {
		t.Fatalf("readyz on empty store: %d %v", code, body)
	}
	if st := s.status(); st.Status.Stars != 0 {
		t.Fatalf("ordinary startup generated %d stars", st.Status.Stars)
	}
	// Refused without the token.
	cmd := exec.Command(s.gbctlBin, "-server", s.admin, "status") //nolint:gosec // G204: binary built by this test
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GBCTL_TOKEN=wrong-token-0123456789"}
	if err := cmd.Run(); err == nil {
		t.Fatal("gbctl with a wrong token succeeded")
	}
	// Initialise a small seeded universe through gbctl.
	if code, out := s.gbctl("universe", "init", "-seed", "4242", "-stars", "6", "-min-planets", "1", "-max-planets", "3"); code != 0 {
		t.Fatalf("universe init: %d %s", code, out)
	}
	st := s.status()
	if st.Status.Mode != "SERVING" || st.Status.Stars != 6 || st.Status.Universe == nil || st.Status.Universe.Seed != 4242 {
		t.Fatalf("after init: %+v", st.Status)
	}
	// A second init is refused (exit 4) and changes nothing.
	if code, out := s.gbctl("universe", "init", "-seed", "1"); code != 4 {
		t.Fatalf("re-init: %d %s", code, out)
	}
	if code, body, _ := s.get("/readyz"); code != 200 || body["game_ready"] != true {
		t.Fatalf("readyz after init: %d %v", code, body)
	}
	initialSchedule := s.status().Status
	// Stop cleanly so a fixture race can be written offline (racegen stand-in).
	if code := s.signal(syscall.SIGTERM, 25*time.Second); code != 0 {
		t.Fatalf("first shutdown exit %d\n%s", code, s.logs.String())
	}
	if !s.hasLog("shutdown complete") {
		t.Fatalf("no shutdown log\n%s", s.logs.String())
	}
	addFixtureRace(t, dbPath, 1, "Testers", "pw-testers")

	// 2. Restart: same universe, startup did not regenerate, schedule kept.
	s = startServer(t, dbPath, nil)
	st = s.status()
	if st.Status.Stars != 6 || st.Status.Universe == nil || st.Status.Universe.Seed != 4242 || st.Status.Races != 1 {
		t.Fatalf("after restart: %+v", st.Status)
	}
	if !st.Status.NextUpdate.Equal(initialSchedule.NextUpdate) {
		t.Fatalf("schedule changed across restart: %v -> %v", initialSchedule.NextUpdate, st.Status.NextUpdate)
	}

	// 3. Log in and run safe commands over the WebSocket.
	token := s.login("Testers", "pw-testers")
	conn := s.connect(token)
	readUntil(t, conn, "Welcome back, Testers", 5*time.Second)
	for _, c := range []string{"who", "help", "survey"} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	readUntil(t, conn, "Testers", 5*time.Second) // who lists the race
	if code, body, _ := s.get("/status"); code != 200 || body["connected"] != float64(1) {
		t.Fatalf("status with one session: %d %v", code, body)
	}

	// 4. Forced update through gbctl: the process survives (CR-04).
	if code, out := s.gbctl("update"); code != 0 {
		t.Fatalf("gbctl update: %d %s", code, out)
	}
	if code, out := s.gbctl("segment"); code != 0 {
		t.Fatalf("gbctl segment: %d %s", code, out)
	}
	readUntil(t, conn, "UPDATE 1 COMPLETE", 10*time.Second)
	select {
	case <-s.exited:
		t.Fatalf("server exited after the admin update: %v\n%s", s.exitErr, s.logs.String())
	default:
	}
	st = s.status()
	if st.Status.NumUpdates != 1 || st.Status.NumSegments != 1 || st.Status.Mode != "SERVING" {
		t.Fatalf("after update: %+v", st.Status)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("who")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, conn, "Testers", 5*time.Second)
	// Quiesce / resume round trip and readiness.
	if code, out := s.gbctl("quiesce"); code != 0 {
		t.Fatalf("quiesce: %d %s", code, out)
	}
	if code, body, _ := s.get("/readyz"); code != 503 || body["mode"] != "QUIESCED" {
		t.Fatalf("readyz while quiesced: %d %v", code, body)
	}
	if code, _, _ := s.get("/livez"); code != 200 {
		t.Fatalf("livez while quiesced: %d", code)
	}
	if code, out := s.gbctl("resume"); code != 0 {
		t.Fatalf("resume: %d %s", code, out)
	}
	beforeStop := s.status().Status

	// 5. Orderly SIGTERM with a connected client: 1012 close frame, exit 0.
	exitCh := make(chan int, 1)
	go func() { exitCh <- s.signal(syscall.SIGTERM, 25*time.Second) }()
	var closeCode int
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			var ce *websocket.CloseError
			if errors.As(err, &ce) {
				closeCode = ce.Code
			}
			break
		}
	}
	if closeCode != websocket.CloseServiceRestart {
		t.Fatalf("client close code %d, want 1012", closeCode)
	}
	if code := <-exitCh; code != 0 {
		t.Fatalf("shutdown with client exit %d\n%s", code, s.logs.String())
	}
	if !s.hasLog("shutdown: flushed") {
		t.Fatalf("flush not logged\n%s", s.logs.String())
	}

	// 6. Restart: universe, counters and schedule preserved.
	s = startServer(t, dbPath, nil)
	st = s.status()
	if st.Status.NumUpdates != beforeStop.NumUpdates || st.Status.NumSegments != beforeStop.NumSegments {
		t.Fatalf("counters lost: before %d/%d after %d/%d", beforeStop.NumUpdates, beforeStop.NumSegments, st.Status.NumUpdates, st.Status.NumSegments)
	}
	if !st.Status.NextUpdate.Equal(beforeStop.NextUpdate) || !st.Status.NextSegment.Equal(beforeStop.NextSegment) {
		t.Fatalf("schedule lost: before %v/%v after %v/%v", beforeStop.NextUpdate, beforeStop.NextSegment, st.Status.NextUpdate, st.Status.NextSegment)
	}
	if st.Status.Stars != 6 || st.Status.Universe.Seed != 4242 || st.Status.Races != 1 {
		t.Fatalf("universe changed: %+v", st.Status)
	}
	// Logs are JSON with the expected fields.
	lines := s.logLines()
	if len(lines) == 0 {
		t.Fatalf("no JSON log lines\n%s", s.logs.String())
	}
	sawComponent := false
	for _, l := range lines {
		if _, ok := l["component"]; ok {
			sawComponent = true
		}
		for _, v := range l {
			if sv, ok := v.(string); ok && (strings.Contains(sv, jwtSecret) || strings.Contains(sv, adminToken)) {
				t.Fatalf("log line leaks a secret: %v", l)
			}
		}
	}
	if !sawComponent {
		t.Fatal("log lines lack the component field")
	}
	if code := s.signal(syscall.SIGINT, 25*time.Second); code != 0 {
		t.Fatalf("final shutdown exit %d", code)
	}
}

func TestStartupRefusesInsecureConfig(t *testing.T) {
	serverBin, _ := binaries(t)
	dbPath := filepath.Join(t.TempDir(), "never-created.db")
	for name, env := range map[string]map[string]string{
		"no secret":      {"GB_ADMIN_TOKEN": adminToken},
		"default secret": {"GB_JWT_SECRET": "change-me-in-production", "GB_ADMIN_TOKEN": adminToken},
		"no admin token": {"GB_JWT_SECRET": jwtSecret},
	} {
		cmd := exec.Command(serverBin) //nolint:gosec // G204: binary built by this test
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GB_DB_PATH=" + dbPath, "GB_ADDR=127.0.0.1:0", "GB_ADMIN_ADDR=127.0.0.1:0"}
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		out, err := cmd.CombinedOutput()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 1 {
			t.Fatalf("%s: expected exit 1, got %v\n%s", name, err, out)
		}
		if strings.Contains(string(out), "change-me-in-production") {
			t.Fatalf("%s: error output echoes the secret", name)
		}
		if _, err := os.Stat(dbPath); err == nil {
			t.Fatalf("%s: database was created despite invalid configuration", name)
		}
	}
}

func TestSigtermDuringTurnIsBounded(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gb.db")
	// A turn that takes ~3 s; the shutdown budget (20 s) covers it, so the
	// turn finishes, is flushed, and the exit is clean.
	s := startServer(t, dbPath, map[string]string{"GB_TEST_HOOKS": "1", "GB_TEST_TURN_DELAY": "3s"})
	if code, out := s.gbctl("universe", "init", "-seed", "7", "-stars", "3", "-min-planets", "1", "-max-planets", "1"); code != 0 {
		t.Fatalf("init: %d %s", code, out)
	}
	updateDone := make(chan string, 1)
	go func() { _, out := s.gbctl("update"); updateDone <- out }()
	time.Sleep(700 * time.Millisecond)
	if code, body, _ := s.get("/readyz"); code != 200 || body["turn_running"] != true {
		t.Fatalf("turn not observed running: %d %v\n%s", code, body, s.logs.String())
	}
	start := time.Now()
	code := s.signal(syscall.SIGTERM, 25*time.Second)
	if code != 0 {
		t.Fatalf("exit %d (turn should have completed within the budget)\n%s", code, s.logs.String())
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("shutdown took %s", d)
	}
	<-updateDone
	if !s.hasLog("turn complete") {
		t.Fatalf("turn did not complete before exit\n%s", s.logs.String())
	}
	// Restart: the completed turn is counted exactly once.
	s2 := startServer(t, dbPath, nil)
	if st := s2.status(); st.Status.NumUpdates != 1 {
		t.Fatalf("updates after restart = %d", st.Status.NumUpdates)
	}
	_ = s2.signal(syscall.SIGTERM, 25*time.Second)
}

func TestShutdownTimeoutIsHonest(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gb.db")
	// Turn takes ~8 s, budget 2 s: the server must exit within the budget
	// with code 3 and log that the store may be mid-flush.
	s := startServer(t, dbPath, map[string]string{"GB_TEST_HOOKS": "1", "GB_TEST_TURN_DELAY": "8s", "GB_SHUTDOWN_TIMEOUT": "2"})
	if code, out := s.gbctl("universe", "init", "-seed", "7", "-stars", "3", "-min-planets", "1", "-max-planets", "1"); code != 0 {
		t.Fatalf("init: %d %s", code, out)
	}
	go func() { _, _ = s.gbctl("update") }()
	time.Sleep(700 * time.Millisecond)
	start := time.Now()
	code := s.signal(syscall.SIGTERM, 15*time.Second)
	if code != 3 {
		t.Fatalf("exit %d, want 3\n%s", code, s.logs.String())
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Fatalf("shutdown exceeded the budget: %s", d)
	}
	if !s.hasLog("did not stop within the shutdown budget") {
		t.Fatalf("timeout not reported honestly\n%s", s.logs.String())
	}
	// The store is reopenable; the interrupted turn is NOT counted (its
	// schedule was never persisted). Nothing more is asserted about its
	// consistency (CR-18).
	s2 := startServer(t, dbPath, nil)
	if st := s2.status(); st.Status.NumUpdates != 0 || st.Status.Stars != 3 {
		t.Fatalf("after interrupted turn: %+v", st.Status)
	}
	_ = s2.signal(syscall.SIGTERM, 25*time.Second)
}

func TestIdleSigtermAndMetrics(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gb.db")
	s := startServer(t, dbPath, nil)
	resp, err := http.Get(s.admin + "/metrics") //nolint:gosec // G107: test server
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	for _, want := range []string{`gb_engine_mode{mode="UNINITIALIZED"} 1`, "gb_commands_total", "gb_sessions_connected 0", "gb_admin_operations_total"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("metrics missing %q", want)
		}
	}
	if strings.Contains(string(data), adminToken) || strings.Contains(string(data), jwtSecret) {
		t.Fatal("metrics leak a secret")
	}
	// The admin routes are not on the player listener.
	resp, err = http.Get("http://" + s.addr + "/admin/v1/status") //nolint:gosec // G107: test server
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusGone && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("admin route on player listener: %d", resp.StatusCode)
	}
	start := time.Now()
	if code := s.signal(syscall.SIGTERM, 25*time.Second); code != 0 {
		t.Fatalf("idle shutdown exit %d\n%s", code, s.logs.String())
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("idle shutdown took %s", time.Since(start))
	}
}
