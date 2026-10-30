package e2e

// Subprocess regression tests for the independent Codex review findings
// GB1-05 (a listener failure exited 0) and GB1-09 (a second SIGTERM killed
// the process by signal). They run the real gb-server binary on loopback
// with temporary files and databases.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
)

// runToExit starts gb-server with env and waits for it to exit on its own.
func runToExit(t *testing.T, env map[string]string, wait time.Duration) (int, string) {
	t.Helper()
	serverBin, _ := binaries(t)
	cmd := exec.Command(serverBin) //nolint:gosec // G204: binary built by this test
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	logs := &logBuffer{}
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(wait):
		_ = cmd.Process.Kill()
		t.Fatalf("server still running after %s (killed: %v)\n%s", wait, <-done, logs.String())
	}
	return exitCode(t, err), logs.String()
}

// exitCode returns the process exit status and fails if the process was
// killed by a signal instead of exiting.
func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("wait: %v", err)
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		t.Fatalf("process was killed by signal %v instead of exiting with a status", ws.Signal())
	}
	return ee.ExitCode()
}

func baseEnv(t *testing.T, dbPath string) map[string]string {
	return map[string]string{
		"GB_ADDR":        fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		"GB_ADMIN_ADDR":  fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		"GB_DB_PATH":     dbPath,
		"GB_JWT_SECRET":  jwtSecret,
		"GB_ADMIN_TOKEN": adminToken,
	}
}

// writeCertPair writes a throwaway self-signed certificate and key.
func writeCertPair(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath = filepath.Join(dir, "test.crt"), filepath.Join(dir, "test.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

// TestGB105ListenerFailureExitCode: a missing TLS certificate or key makes
// the player listener fail after startup. The shutdown sequence runs and
// succeeds, but the process must exit 5, not 0, and the store must be
// closed cleanly (reopenable at once).
func TestGB105ListenerFailureExitCode(t *testing.T) {
	dir := t.TempDir()
	cert, key := writeCertPair(t, dir)
	missing := filepath.Join(dir, "missing.pem")
	for name, tls := range map[string][2]string{
		"missing certificate": {missing, key},
		"missing private key": {cert, missing},
	} {
		t.Run(name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "gb.db")
			env := baseEnv(t, dbPath)
			env["GB_TLS_CERT"], env["GB_TLS_KEY"] = tls[0], tls[1]
			code, logs := runToExit(t, env, 20*time.Second)
			if code != 5 {
				t.Fatalf("exit %d, want 5 (listener failure)\n%s", code, logs)
			}
			for _, want := range []string{`"listener failed; shutting down"`, "missing.pem", `"shutdown complete"`,
				`"exiting with failure because a listener failed"`, `"shutdown_exit_code":0`, `"exit_code":5`} {
				if !strings.Contains(logs, want) {
					t.Fatalf("log lacks %s\n%s", want, logs)
				}
			}
			if strings.Contains(logs, jwtSecret) || strings.Contains(logs, adminToken) {
				t.Fatal("log contains a secret")
			}
			store, err := db.Open(dbPath)
			if err != nil {
				t.Fatalf("store not closed cleanly after the listener failure: %v", err)
			}
			_ = store.Close()
		})
	}
}

// TestGB105PortBindingFailure: a port that cannot be bound is a startup
// error: exit 1, and the store is closed again.
func TestGB105PortBindingFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	dbPath := filepath.Join(t.TempDir(), "gb.db")
	env := baseEnv(t, dbPath)
	env["GB_ADDR"] = ln.Addr().String()
	code, logs := runToExit(t, env, 20*time.Second)
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, logs)
	}
	if !strings.Contains(logs, "address already in use") {
		t.Fatalf("bind failure not logged\n%s", logs)
	}
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("store left locked: %v", err)
	}
	_ = store.Close()

	// The admin listener's port too.
	dbPath2 := filepath.Join(t.TempDir(), "gb.db")
	env2 := baseEnv(t, dbPath2)
	env2["GB_ADMIN_ADDR"] = ln.Addr().String()
	if code, logs := runToExit(t, env2, 20*time.Second); code != 1 || !strings.Contains(logs, "admin listener") {
		t.Fatalf("admin bind failure: exit %d\n%s", code, logs)
	}
}

// TestGB105OrdinarySigtermStillExitsZero: the signal path keeps exit 0.
func TestGB105OrdinarySigtermStillExitsZero(t *testing.T) {
	s := startServer(t, filepath.Join(t.TempDir(), "gb.db"), nil)
	if code := s.signal(syscall.SIGTERM, 20*time.Second); code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, s.logs.String())
	}
	if !s.hasLog("shutdown complete") || s.hasLog("listener failed") {
		t.Fatalf("unexpected shutdown log\n%s", s.logs.String())
	}
}

// TestGB109SecondSignalForcesExit distinguishes orderly shutdown from
// second-signal interruption: during a long turn, one SIGTERM waits for the
// turn (exit 0, covered by TestSigtermDuringTurnIsBounded); a second SIGTERM
// forces an immediate, logged exit 6 instead of death by signal 15.
func TestGB109SecondSignalForcesExit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gb.db")
	s := startServer(t, dbPath, map[string]string{"GB_TEST_HOOKS": "1", "GB_TEST_TURN_DELAY": "8s"})
	if code, out := s.gbctl("universe", "init", "-seed", "7", "-stars", "3", "-min-planets", "1", "-max-planets", "1"); code != 0 {
		t.Fatalf("init: %d %s", code, out)
	}
	go func() { _, _ = s.gbctl("update") }()
	time.Sleep(700 * time.Millisecond)
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	select {
	case <-s.exited:
		t.Fatalf("server exited on the first SIGTERM during a turn\n%s", s.logs.String())
	default:
	}
	start := time.Now()
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.exited:
	case <-time.After(5 * time.Second):
		t.Fatalf("second SIGTERM did not force an exit\n%s", s.logs.String())
	}
	if code := exitCode(t, s.exitErr); code != 6 {
		t.Fatalf("exit %d, want 6 (forced)\n%s", code, s.logs.String())
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("forced exit took %s", d)
	}
	if !s.hasLog("second signal during shutdown: forced exit") || !s.hasLog("no clean-shutdown guarantee") {
		t.Fatalf("forced exit not logged\n%s", s.logs.String())
	}
	if s.hasLog("shutdown complete") {
		t.Fatalf("forced exit claims a completed shutdown\n%s", s.logs.String())
	}
}

// TestGB104RealEngineLongTurnKeepsHealthyClient is the Codex GB1-04
// scenario end to end: real binary, real engine, production ping/pong
// timing (60 s read deadline), a 70 s turn, and a client whose command is
// queued behind the turn. The client must stay connected and receive the
// command's output after the turn. It takes over a minute: GB_LONG_TESTS=1.
func TestGB104RealEngineLongTurnKeepsHealthyClient(t *testing.T) {
	if os.Getenv("GB_LONG_TESTS") != "1" {
		t.Skip("set GB_LONG_TESTS=1 to run the 70-second real-engine reproduction")
	}
	dbPath := filepath.Join(t.TempDir(), "gb.db")
	s1 := startServer(t, dbPath, nil)
	if code, out := s1.gbctl("universe", "init", "-seed", "7", "-stars", "3", "-min-planets", "1", "-max-planets", "1"); code != 0 {
		t.Fatalf("init: %d %s", code, out)
	}
	if code := s1.signal(syscall.SIGTERM, 20*time.Second); code != 0 {
		t.Fatalf("stop: %d", code)
	}
	addFixtureRace(t, dbPath, 1, "Testers", "pw-testers")
	s := startServer(t, dbPath, map[string]string{"GB_TEST_HOOKS": "1", "GB_TEST_TURN_DELAY": "70s", "GB_SHUTDOWN_TIMEOUT": "90"})
	conn := s.connect(s.login("Testers", "pw-testers"))
	_ = readUntil(t, conn, "Welcome", 15*time.Second)
	_ = conn.SetReadDeadline(time.Time{}) // readUntil leaves a client-side deadline behind

	// Read continuously (which answers pings) and record how it ends.
	msgs, errc := make(chan string, 64), make(chan error, 1)
	go func() {
		for {
			_, b, err := conn.ReadMessage()
			if err != nil {
				errc <- err
				return
			}
			msgs <- string(b)
		}
	}()
	go func() { _, _ = s.gbctl("update") }()
	time.Sleep(time.Second)
	if err := conn.WriteMessage(1, []byte("who")); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(100 * time.Second)
	for {
		select {
		case m := <-msgs:
			if strings.Contains(m, "Testers") {
				if !s.hasLog("turn complete") {
					t.Fatalf("command output arrived before the turn finished: %q", m)
				}
				if code := s.signal(syscall.SIGTERM, 30*time.Second); code != 0 {
					t.Fatalf("exit %d", code)
				}
				return
			}
		case err := <-errc:
			t.Fatalf("healthy client disconnected during the long turn: %v\n%s", err, s.logs.String())
		case <-deadline:
			t.Fatalf("no command output within 100 s\n%s", s.logs.String())
		}
	}
}
