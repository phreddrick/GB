package net

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/engine"
	"github.com/phreddrick/GB/go/internal/enroll"
	"github.com/phreddrick/GB/go/internal/platform/metrics"
)

// fakeEngine is a controllable GameIface for transport tests.
type fakeEngine struct {
	mu       sync.Mutex
	mode     engine.Mode
	alive    bool
	halt     string
	commands []string
	enrolled []string
	block    chan struct{} // when non-nil, HandleCommand blocks until closed or ctx ends
	echo     bool
}

func newFakeEngine() *fakeEngine { return &fakeEngine{mode: engine.ModeServing, alive: true} }

func (f *fakeEngine) Authenticate(ctx context.Context, race, password string, governor int) (AuthResult, error) {
	if race == "Testers" && password == "pw" {
		return AuthResult{OK: true, PlayerNum: 1}, nil
	}
	if race == "busy" {
		return AuthResult{}, engine.ErrEngineBusy
	}
	return AuthResult{}, nil
}

func (f *fakeEngine) HandleCommand(ctx context.Context, p, g int, cmd string, sess Responder) error {
	f.mu.Lock()
	f.commands = append(f.commands, cmd)
	block := f.block
	echo := f.echo
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return engine.ErrEngineBusy
		}
	}
	if echo {
		return sess.Send("echo: " + cmd + "\n")
	}
	return nil
}

func (f *fakeEngine) PlayerDisconnect(int, int) {}
func (f *fakeEngine) WelcomeMessage(context.Context, int, int) string {
	return "Welcome back, Testers.\n"
}
func (f *fakeEngine) Enroll(ctx context.Context, req *enroll.Request) (enroll.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enrolled = append(f.enrolled, req.RaceName)
	return enroll.Response{Success: true, PlayerNum: 2, Message: "PREVIEW"}, nil
}
func (f *fakeEngine) Health(time.Time) engine.Health {
	f.mu.Lock()
	defer f.mu.Unlock()
	return engine.Health{Mode: f.mode, Alive: f.alive, Ready: f.mode.Ready(), GameReady: f.mode == engine.ModeServing || f.mode == engine.ModeTurnInProgress, HaltReason: f.halt, LastHeartbeat: time.Now()}
}
func (f *fakeEngine) Status() engine.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return engine.Status{Mode: f.mode.String(), Stars: 3, Races: 1, Persistence: engine.PersistenceNote}
}
func (f *fakeEngine) setMode(m engine.Mode) {
	f.mu.Lock()
	f.mode = m
	f.mu.Unlock()
}

func testCfg(t *testing.T, extra map[string]string) *config.Config {
	t.Helper()
	env := map[string]string{
		"GB_JWT_SECRET":  "0123456789abcdef0123456789abcdef-test",
		"GB_ADMIN_TOKEN": "admin-token-0123456789",
		"GB_ADDR":        "127.0.0.1:0",
	}
	for k, v := range extra {
		env[k] = v
	}
	cfg, _, err := config.Load("", config.MapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newTestServer(t *testing.T, fe *fakeEngine, extra map[string]string) (*Server, *httptest.Server) {
	t.Helper()
	cfg := testCfg(t, extra)
	srv := New(cfg, fe, Options{Registry: metrics.NewRegistry()})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func get(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // G107: test server URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &body)
	return resp.StatusCode, body
}

func TestProbesPerMode(t *testing.T) {
	fe := newFakeEngine()
	_, ts := newTestServer(t, fe, nil)
	cases := []struct {
		mode             engine.Mode
		readyCode        int
		gameReady, alive bool
	}{
		{engine.ModeStarting, 503, false, true},
		{engine.ModeUninitialized, 200, false, true},
		{engine.ModeServing, 200, true, true},
		{engine.ModeTurnInProgress, 200, true, true},
		{engine.ModeQuiesced, 503, false, true},
		{engine.ModeHalted, 503, false, true},
		{engine.ModeStopped, 503, false, false},
	}
	for _, c := range cases {
		fe.setMode(c.mode)
		fe.mu.Lock()
		fe.alive = c.alive
		fe.halt = ""
		if c.mode == engine.ModeHalted {
			fe.halt = "panic during command"
		}
		fe.mu.Unlock()
		code, body := get(t, ts.URL+"/readyz")
		if code != c.readyCode || body["mode"] != c.mode.String() || body["game_ready"] != c.gameReady {
			t.Errorf("%s: readyz code=%d body=%v", c.mode, code, body)
		}
		if c.mode == engine.ModeHalted && body["halt_reason"] != "panic during command" {
			t.Errorf("halted readyz must carry the reason: %v", body)
		}
		code, body = get(t, ts.URL+"/livez")
		wantLive := 200
		if !c.alive {
			wantLive = 503
		}
		if code != wantLive || body["mode"] != c.mode.String() {
			t.Errorf("%s: livez code=%d body=%v", c.mode, code, body)
		}
		for k := range body {
			if strings.Contains(k, "secret") || strings.Contains(k, "token") || strings.Contains(k, "player") {
				t.Errorf("probe body leaks field %q", k)
			}
		}
	}
	// A stuck actor is not alive even while SERVING.
	fe.setMode(engine.ModeServing)
	fe.mu.Lock()
	fe.alive = false
	fe.mu.Unlock()
	if code, _ := get(t, ts.URL+"/livez"); code != 503 {
		t.Errorf("stuck actor livez = %d", code)
	}
}

func TestDrainingNotReady(t *testing.T) {
	fe := newFakeEngine()
	srv, ts := newTestServer(t, fe, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// Shutdown the real http.Server embedded (not the httptest one) only
	// flips the draining flag here; the handler is still reachable via ts.
	srv.draining.Store(true)
	code, body := get(t, ts.URL+"/readyz")
	if code != 503 || body["draining"] != true {
		t.Fatalf("draining readyz code=%d body=%v", code, body)
	}
	_ = ctx
}

func TestStatusHasNoPlayerList(t *testing.T) {
	fe := newFakeEngine()
	_, ts := newTestServer(t, fe, nil)
	code, body := get(t, ts.URL+"/status")
	if code != 200 || body["mode"] != "SERVING" || body["connected"] != float64(0) {
		t.Fatalf("status %d %v", code, body)
	}
	if _, ok := body["players"]; ok {
		t.Fatal("status must not list players")
	}
}

func TestOldAdminEndpointsAreGone(t *testing.T) {
	fe := newFakeEngine()
	_, ts := newTestServer(t, fe, nil)
	for _, p := range []string{"/admin/update", "/admin/segment"} {
		resp, err := http.Post(ts.URL+p, "application/json", strings.NewReader(`{"password":"x"}`)) //nolint:gosec // G107: test URL
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusGone {
			t.Errorf("%s: status %d, want 410", p, resp.StatusCode)
		}
	}
	fe.mu.Lock()
	n := len(fe.commands)
	fe.mu.Unlock()
	if n != 0 {
		t.Fatal("removed endpoints reached the engine")
	}
}

func TestEnrollmentDisabledByDefault(t *testing.T) {
	fe := newFakeEngine()
	_, ts := newTestServer(t, fe, nil)
	resp, err := http.Post(ts.URL+"/api/enroll", "application/json", strings.NewReader(`{"race_name":"X","password":"abc"}`)) //nolint:gosec // G107: test URL
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(data), "disabled") {
		t.Fatalf("enroll disabled: %d %s", resp.StatusCode, data)
	}
	code, _ := get(t, ts.URL+"/enroll")
	if code != http.StatusForbidden {
		t.Fatalf("enroll page while disabled: %d", code)
	}
	fe.mu.Lock()
	n := len(fe.enrolled)
	fe.mu.Unlock()
	if n != 0 {
		t.Fatal("disabled enrollment reached the engine")
	}
	// Preview enabled routes through the engine.
	_, ts2 := newTestServer(t, fe, map[string]string{"GB_ENROLL_PREVIEW": "true"})
	resp, err = http.Post(ts2.URL+"/api/enroll", "application/json", strings.NewReader(`{"race_name":"Y","password":"abc"}`)) //nolint:gosec // G107: test URL
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preview enroll: %d", resp.StatusCode)
	}
	if code, _ := get(t, ts2.URL+"/enroll"); code != 200 {
		t.Fatalf("enroll page while enabled: %d", code)
	}
}

func login(t *testing.T, url, race, pw string) (int, string) {
	t.Helper()
	resp, err := http.Post(url+"/auth/login", "application/json", strings.NewReader(`{"race":"`+race+`","password":"`+pw+`","governor":0}`)) //nolint:gosec // G107: test URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Token string `json:"token"`
	}
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &body)
	return resp.StatusCode, body.Token
}

func TestLoginAndWebSocketRoundTrip(t *testing.T) {
	fe := newFakeEngine()
	fe.echo = true
	srv, ts := newTestServer(t, fe, nil)
	if code, _ := login(t, ts.URL, "Testers", "wrong"); code != 401 {
		t.Fatalf("bad login %d", code)
	}
	if code, _ := login(t, ts.URL, "busy", "x"); code != 503 {
		t.Fatalf("busy login %d", code)
	}
	code, token := login(t, ts.URL, "Testers", "pw")
	if code != 200 || token == "" {
		t.Fatalf("login %d %q", code, token)
	}
	if srv.metrics.Logins.Value("ok") != 1 || srv.metrics.Logins.Value("denied") != 1 {
		t.Fatal("login metrics")
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + token
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v (%v)", err, resp)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil || !strings.Contains(string(msg), "Welcome back") {
		t.Fatalf("welcome: %q %v", msg, err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("survey")); err != nil {
		t.Fatal(err)
	}
	_, msg, err = conn.ReadMessage()
	if err != nil || string(msg) != "echo: survey\n" {
		t.Fatalf("echo: %q %v", msg, err)
	}
	if srv.Registry().Count() != 1 {
		t.Fatalf("sessions %d", srv.Registry().Count())
	}
	// Unauthenticated upgrade is refused.
	if _, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatalf("unauthenticated ws: %v %v", err, resp)
	}
	// Closing the client cleans up the session.
	_ = conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for srv.Registry().Count() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.Registry().Count() != 0 {
		t.Fatal("session not unregistered after close")
	}
}

func TestOriginAllowList(t *testing.T) {
	fe := newFakeEngine()
	_, ts := newTestServer(t, fe, map[string]string{"GB_WS_ALLOWED_ORIGINS": "https://gb.example.org"})
	_, token := login(t, ts.URL, "Testers", "pw")
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + token
	try := func(origin string) int {
		h := http.Header{}
		if origin != "" {
			h.Set("Origin", origin)
		}
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, h)
		if err == nil {
			_ = conn.Close()
			return 101
		}
		if resp == nil {
			t.Fatalf("dial %q: %v", origin, err)
		}
		return resp.StatusCode
	}
	if c := try(""); c != 101 {
		t.Errorf("no origin: %d", c)
	}
	if c := try("http://" + strings.TrimPrefix(ts.URL, "http://")); c != 101 {
		t.Errorf("same origin: %d", c)
	}
	if c := try("https://gb.example.org"); c != 101 {
		t.Errorf("allow-listed origin: %d", c)
	}
	if c := try("https://evil.example.org"); c != 403 {
		t.Errorf("foreign origin: %d, want 403", c)
	}
}

func TestReadLimitClosesOversizedFrame(t *testing.T) {
	fe := newFakeEngine()
	srv, ts := newTestServer(t, fe, map[string]string{"GB_WS_MAX_MESSAGE_BYTES": "128"})
	_, token := login(t, ts.URL, "Testers", "pw")
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws?token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, _ = conn.ReadMessage() // welcome
	if err := conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", 1024))); err != nil {
		t.Fatal(err)
	}
	// The server closes the connection; the next read fails.
	deadline := time.Now().Add(5 * time.Second)
	closed := false
	for time.Now().Before(deadline) {
		if _, _, err := conn.ReadMessage(); err != nil {
			closed = true
			break
		}
	}
	if !closed {
		t.Fatal("oversized frame did not close the connection")
	}
	fe.mu.Lock()
	n := len(fe.commands)
	fe.mu.Unlock()
	if n != 0 {
		t.Fatal("oversized frame reached the engine")
	}
	for time.Now().Before(deadline) && srv.metrics.WSDropped.Value("frame_too_big") != 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.metrics.WSDropped.Value("frame_too_big") != 1 {
		t.Fatalf("frame_too_big metric = %v", srv.metrics.WSDropped.Value("frame_too_big"))
	}
}

func TestSlowClientIsDroppedWithoutBlockingSender(t *testing.T) {
	fe := newFakeEngine()
	srv, ts := newTestServer(t, fe, nil)
	_, token := login(t, ts.URL, "Testers", "pw")
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws?token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// The dial returns when the upgrade completes; the handler registers the
	// session just after. Wait for it before flooding.
	waitFor := time.Now().Add(5 * time.Second)
	for len(srv.Registry().ConnectedPlayers()) == 0 && time.Now().Before(waitFor) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(srv.Registry().ConnectedPlayers()) == 0 {
		t.Fatal("session never registered")
	}
	// Never read from the client. Flood far more than the queue plus the
	// kernel buffers can hold; Send must return promptly every time and
	// eventually report overflow.
	start := time.Now()
	var overflow bool
	for i := 0; i < 200000 && !overflow; i++ {
		err := srv.Registry().Notify(1, 0, strings.Repeat("y", 4000)+"\n")
		if !err {
			break // session already gone
		}
		if time.Since(start) > 20*time.Second {
			t.Fatal("sender blocked on a slow client")
		}
		sessions := srv.Registry()
		sessions.mu.RLock()
		for _, s := range sessions.all {
			if s.Dropped() {
				overflow = true
			}
		}
		sessions.mu.RUnlock()
	}
	if !overflow {
		t.Fatal("slow client was never dropped")
	}
	if time.Since(start) > 20*time.Second {
		t.Fatal("dropping the slow client took too long")
	}
}

func TestCommandWaitIsBoundedAndShutdownClosesSessions(t *testing.T) {
	fe := newFakeEngine()
	fe.block = make(chan struct{})
	srv, ts := newTestServer(t, fe, nil)
	_, token := login(t, ts.URL, "Testers", "pw")
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws?token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, _, _ = conn.ReadMessage() // welcome
	if err := conn.WriteMessage(websocket.TextMessage, []byte("survey")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	// Server shutdown: sessions get a 1012 close frame even though the
	// command is parked on the (blocked) engine.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Logf("shutdown: %v", err)
	}
	var closeCode int
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
		t.Fatalf("close code %d, want 1012", closeCode)
	}
	close(fe.block)
	// New upgrades are refused while draining.
	resp, err := http.Get(ts.URL + "/ws?token=" + token) //nolint:gosec // G107: test URL
	if err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("ws while draining: %d", resp.StatusCode)
		}
	}
}

func TestSessionSendNeverBlocks(t *testing.T) {
	// Direct unit test of the queue semantics without a network.
	srvConn, cliConn := pipeWS(t)
	defer func() { _ = cliConn.Close() }()
	s := newSession(srvConn, "t", Identity{PlayerNum: 1}, 2, time.Second, time.Hour)
	// Fill the queue without a reader on the writer side: the writer
	// goroutine drains into the socket until the socket buffer is full.
	deadline := time.Now().Add(5 * time.Second)
	var got error
	for time.Now().Before(deadline) {
		if err := s.Send(strings.Repeat("z", 65536)); err != nil {
			got = err
			break
		}
	}
	if !errors.Is(got, ErrSessionOverflow) {
		t.Fatalf("expected overflow, got %v", got)
	}
	if s.IsActive() || !s.Dropped() {
		t.Fatal("overflowed session must be closed and marked dropped")
	}
	if err := s.Send("x"); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("send after close: %v", err)
	}
	s.Close() // idempotent
}

// pipeWS creates a connected WebSocket pair over an httptest server.
func pipeWS(t *testing.T) (server, client *websocket.Conn) {
	t.Helper()
	up := websocket.Upgrader{}
	ch := make(chan *websocket.Conn, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		ch <- c
	}))
	t.Cleanup(ts.Close)
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-ch:
		return s, c
	case <-time.After(5 * time.Second):
		t.Fatal("no server conn")
	}
	return nil, nil
}
