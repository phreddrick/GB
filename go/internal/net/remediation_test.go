package net

// Regression tests for the independent Codex review findings GB1-02
// (session publication race) and GB1-04 (healthy WebSocket clients
// disconnected during long commands). They use the real Server, Session,
// registry, writer goroutine and gorilla/websocket client; only the game
// engine is a fake. Run with -race.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/phreddrick/GB/go/internal/engine"
)

// seqEngine wraps fakeEngine with actor-like command semantics: a command
// "sleep:<duration>" runs for that long once started and always completes
// (like a started actor command), and it records whether two commands of the
// same session ever overlapped.
type seqEngine struct {
	*fakeEngine
	inflight atomic.Int32
	overlap  atomic.Bool
	gate     chan struct{} // when non-nil, "gate" commands wait for it (or for ctx while not started)
}

func (e *seqEngine) HandleCommand(ctx context.Context, p, g int, cmd string, sess Responder) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", engine.ErrEngineBusy, engine.ErrNotExecuted)
	}
	if e.inflight.Add(1) > 1 {
		e.overlap.Store(true)
	}
	defer e.inflight.Add(-1)
	e.mu.Lock()
	e.commands = append(e.commands, cmd)
	e.mu.Unlock()
	switch {
	case strings.HasPrefix(cmd, "sleep:"):
		d, err := time.ParseDuration(strings.TrimPrefix(cmd, "sleep:"))
		if err != nil {
			return err
		}
		time.Sleep(d)
	case cmd == "gate" && e.gate != nil:
		<-e.gate
	}
	return sess.Send("echo: " + cmd + "\n")
}

func newSeqServer(t *testing.T, tm wsTiming) (*Server, *seqEngine, string) {
	t.Helper()
	se := &seqEngine{fakeEngine: newFakeEngine()}
	cfg := testCfg(t, nil)
	srv := New(cfg, se, Options{})
	srv.timing = tm
	ts := newHTTPTestServer(t, srv)
	_, token := login(t, ts, "Testers", "pw")
	return srv, se, "ws" + strings.TrimPrefix(ts, "http") + "/ws?token=" + token
}

func newHTTPTestServer(t *testing.T, srv *Server) string {
	t.Helper()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

// reader collects text frames and the terminal error of a client. Reading
// continuously is what makes gorilla's client answer the server's pings.
type reader struct {
	msgs chan string
	errc chan error
}

func readAll(c *websocket.Conn) *reader {
	r := &reader{msgs: make(chan string, 256), errc: make(chan error, 1)}
	go func() {
		for {
			_, b, err := c.ReadMessage()
			if err != nil {
				r.errc <- err
				return
			}
			r.msgs <- string(b)
		}
	}()
	return r
}

func (r *reader) next(t *testing.T, d time.Duration) string {
	t.Helper()
	select {
	case m := <-r.msgs:
		return m
	case err := <-r.errc:
		t.Fatalf("connection ended while waiting for a message: %v", err)
	case <-time.After(d):
		t.Fatal("timed out waiting for a message")
	}
	return ""
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// sessionGoroutines counts goroutines running session code: the WebSocket
// handler, the command worker and the writer. It is the precise leak check
// (a total goroutine count also includes the test server and clients).
func sessionGoroutines() int {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	n := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, ".(*Server).handleWS(") || strings.Contains(g, ".(*Server).commandWorker(") ||
			strings.Contains(g, ".(*Session).writeLoop(") {
			n++
		}
	}
	return n
}

var fastTiming = wsTiming{pongWait: 300 * time.Millisecond, pingInterval: 100 * time.Millisecond, commandWait: 5 * time.Second, commandQueue: 4}

// TestGB104HealthyClientStaysConnectedDuringLongCommand is the controlled
// equivalent of the Codex 65-second reproduction: the command runs for more
// than six read deadlines (pongWait 300 ms, 2 s command). The client keeps
// answering pings, so it must stay connected, and the responses of the
// commands sent behind the long one arrive in order.
func TestGB104HealthyClientStaysConnectedDuringLongCommand(t *testing.T) {
	_, se, url := newSeqServer(t, fastTiming)
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	r := readAll(c)
	if m := r.next(t, 5*time.Second); !strings.Contains(m, "Welcome") {
		t.Fatalf("first message %q, want the welcome", m)
	}
	for _, cmd := range []string{"sleep:2s", "a", "b", "c"} {
		if err := c.WriteMessage(websocket.TextMessage, []byte(cmd)); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"echo: sleep:2s\n", "echo: a\n", "echo: b\n", "echo: c\n"} {
		if got := r.next(t, 10*time.Second); got != want {
			t.Fatalf("response %q, want %q (order not preserved)", got, want)
		}
	}
	select {
	case err := <-r.errc:
		t.Fatalf("healthy client disconnected: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	if se.overlap.Load() {
		t.Fatal("two commands of one session ran concurrently")
	}
}

// TestGB104LongCommandWithProductionTiming reproduces the Codex scenario
// with the production 60 s read deadline: a 65 s command. It takes over a
// minute, so it runs only with GB_LONG_TESTS=1.
func TestGB104LongCommandWithProductionTiming(t *testing.T) {
	if os.Getenv("GB_LONG_TESTS") != "1" {
		t.Skip("set GB_LONG_TESTS=1 to run the 65-second production-timing reproduction")
	}
	_, _, url := newSeqServer(t, defaultWSTiming())
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	r := readAll(c)
	_ = r.next(t, 15*time.Second)
	if err := c.WriteMessage(websocket.TextMessage, []byte("sleep:65s")); err != nil {
		t.Fatal(err)
	}
	if got := r.next(t, 90*time.Second); got != "echo: sleep:65s\n" {
		t.Fatalf("response %q", got)
	}
	select {
	case err := <-r.errc:
		t.Fatalf("healthy client disconnected after the 65 s command: %v", err)
	case <-time.After(2 * time.Second):
	}
}

// TestGB104UnresponsiveClientIsDisconnected: a client that stops reading
// (and therefore stops answering pings) is still disconnected by the read
// deadline, even while one of its commands is running.
func TestGB104UnresponsiveClientIsDisconnected(t *testing.T) {
	srv, _, url := newSeqServer(t, fastTiming)
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.WriteMessage(websocket.TextMessage, []byte("sleep:3s")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, "session registration", func() bool { return srv.registry.Count() == 1 })
	// Never read again: pings go unanswered.
	start := time.Now()
	waitFor(t, 2*time.Second, "unresponsive client to be dropped", func() bool { return srv.registry.Count() == 0 })
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("unresponsive client dropped only after %v", d)
	}
}

// TestGB104CommandQueueLimit: commands beyond the per-session queue are
// refused with a message (not run, not buffered without bound); the rest run
// in order once the blocked command finishes.
func TestGB104CommandQueueLimit(t *testing.T) {
	tm := fastTiming
	tm.commandQueue = 2
	srv, se, url := newSeqServer(t, tm)
	se.gate = make(chan struct{})
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	r := readAll(c)
	_ = r.next(t, 5*time.Second) // welcome
	if err := c.WriteMessage(websocket.TextMessage, []byte("gate")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, "gate command to start", func() bool { return se.inflight.Load() == 1 })
	for i := 1; i <= 6; i++ {
		if err := c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("n%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	rejected := 0
	for i := 0; i < 4; i++ {
		m := r.next(t, 5*time.Second)
		if !strings.Contains(m, "Too many commands waiting (limit 2)") {
			t.Fatalf("message %d = %q, want a queue-full refusal", i, m)
		}
		rejected++
	}
	close(se.gate)
	for _, want := range []string{"echo: gate\n", "echo: n1\n", "echo: n2\n"} {
		if got := r.next(t, 5*time.Second); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if got := srv.metrics.WSCommandsRejected.Value("queue_full"); got != float64(rejected) {
		t.Fatalf("rejected metric = %v, want %d", got, rejected)
	}
	select {
	case m := <-r.msgs:
		t.Fatalf("unexpected extra message %q (a refused command ran)", m)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestGB104QueuedCommandCanceledByDisconnect: when a session closes, its
// commands that never started are canceled (the worker's context ends), and
// the handler and worker goroutines exit.
func TestGB104QueuedCommandCanceledByDisconnect(t *testing.T) {
	srv, se, url := newSeqServer(t, fastTiming)
	se.gate = make(chan struct{})
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := readAll(c)
	_ = r.next(t, 5*time.Second)
	for _, cmd := range []string{"gate", "later1", "later2"} {
		if err := c.WriteMessage(websocket.TextMessage, []byte(cmd)); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 2*time.Second, "gate command to start", func() bool { return se.inflight.Load() == 1 })
	_ = c.Close()
	waitFor(t, 2*time.Second, "session to unregister", func() bool { return srv.registry.Count() == 0 })
	close(se.gate) // the started command completes
	waitFor(t, 3*time.Second, "session goroutines to exit", func() bool { return sessionGoroutines() == 0 })
	se.mu.Lock()
	cmds := append([]string(nil), se.commands...)
	se.mu.Unlock()
	for _, c := range cmds {
		if strings.HasPrefix(c, "later") {
			t.Fatalf("command queued behind a closed session still ran: %v", cmds)
		}
	}
}

// TestGB104ShutdownDuringLongCommandIsBounded: server shutdown while a long
// command runs sends 1012 promptly; the session's goroutines exit once the
// started command completes.
func TestGB104ShutdownDuringLongCommandIsBounded(t *testing.T) {
	srv, _, url := newSeqServer(t, fastTiming)
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	r := readAll(c)
	_ = r.next(t, 5*time.Second)
	if err := c.WriteMessage(websocket.TextMessage, []byte("sleep:1500ms")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	var ce *websocket.CloseError
	for {
		select {
		case <-r.msgs:
			continue
		case err := <-r.errc:
			if !errors.As(err, &ce) || ce.Code != websocket.CloseServiceRestart {
				t.Fatalf("close = %v, want 1012", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no close frame within 2 s of shutdown")
		}
		break
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("close took %v", d)
	}
	waitFor(t, 5*time.Second, "session goroutines to exit", func() bool { return sessionGoroutines() == 0 })
}

// TestGB104ManySessionsNoGoroutineLeak opens and closes sessions with
// commands in flight and checks that every goroutine exits.
func TestGB104ManySessionsNoGoroutineLeak(t *testing.T) {
	srv, _, url := newSeqServer(t, fastTiming)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				t.Error(err)
				return
			}
			_ = c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("sleep:%dms", 10*i)))
			_ = c.WriteMessage(websocket.TextMessage, []byte("x"))
			time.Sleep(time.Duration(i) * 5 * time.Millisecond)
			_ = c.Close()
		}(i)
	}
	wg.Wait()
	waitFor(t, 5*time.Second, "sessions to unregister", func() bool { return srv.registry.Count() == 0 })
	waitFor(t, 5*time.Second, "session goroutines to exit", func() bool { return sessionGoroutines() == 0 })
}

// --- GB1-02 ---

// testSession builds a session with its identity, without a connection
// (the registry never touches the connection).
func testSession(player, governor int) *Session {
	return &Session{PlayerNum: player, Governor: governor, done: make(chan struct{}), outbound: make(chan outMsg, 64)}
}

// TestGB102RegistrationVersusBroadcast is the Codex reproduction with every
// registry reader running concurrently with registration and removal. The
// identity is part of the session before it is published, so the race
// detector must stay quiet.
func TestGB102RegistrationVersusBroadcast(t *testing.T) {
	r := newRegistry(nil)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	readers := []func(){
		func() { r.Broadcast(99, "hello") },
		func() { r.Shout("all") },
		func() { r.NotifyRace(1, "race") },
		func() { _ = r.Notify(1, 0, "gov") },
		func() { _ = r.ConnectedPlayers() },
		func() { _ = r.Count() },
	}
	for _, f := range readers {
		wg.Add(1)
		go func(f func()) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					f()
				}
			}
		}(f)
	}
	for i := 0; i < 2000; i++ {
		s := testSession(1+i%3, i%2)
		if err := r.register(s); err != nil {
			t.Fatal(err)
		}
		r.unregister(s)
	}
	close(stop)
	wg.Wait()
	if n := r.Count(); n != 0 {
		t.Fatalf("%d sessions left registered", n)
	}
}

// TestGB102IdentityVisibleAtPublication: a session is never visible without
// its identity: every reader sees the identity it was created with.
func TestGB102IdentityVisibleAtPublication(t *testing.T) {
	r := newRegistry(nil)
	stop := make(chan struct{})
	var bad atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, info := range r.ConnectedPlayers() {
				if info.PlayerNum != 7 || info.Governor != 3 {
					bad.Add(1)
				}
			}
		}
	}()
	for i := 0; i < 2000; i++ {
		s := testSession(7, 3)
		if err := r.register(s); err != nil {
			t.Fatal(err)
		}
		r.unregister(s)
	}
	close(stop)
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("%d observations of a session without its identity", bad.Load())
	}
}

// TestGB102RegistryRefusesAfterShutdown: once CloseAll has run, a late
// registration is refused (the session would otherwise stay open after
// shutdown closed everything).
func TestGB102RegistryRefusesAfterShutdown(t *testing.T) {
	r := newRegistry(nil)
	s1 := testSession(1, 0)
	if err := r.register(s1); err != nil {
		t.Fatal(err)
	}
	r.CloseAll()
	if s1.IsActive() {
		t.Fatal("registered session not closed by CloseAll")
	}
	if err := r.register(testSession(2, 0)); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("late registration: %v, want ErrRegistryClosed", err)
	}
}

// TestGB102AuthFailureNeverRegisters: a bad or missing token is refused
// before any session exists.
func TestGB102AuthFailureNeverRegisters(t *testing.T) {
	srv, _, url := newSeqServer(t, fastTiming)
	base := url[:strings.Index(url, "?")]
	for _, u := range []string{base, base + "?token=not-a-token"} {
		_, resp, err := websocket.DefaultDialer.Dial(u, nil)
		if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("dial %s: err=%v resp=%v, want 401", u, err, resp)
		}
	}
	if n := srv.registry.Count(); n != 0 {
		t.Fatalf("%d sessions registered after failed authentication", n)
	}
}

// TestGB102DisconnectAndShutdownDuringRegistration: clients that drop
// immediately and a shutdown racing new connections leave no registered or
// open session behind.
func TestGB102DisconnectAndShutdownDuringRegistration(t *testing.T) {
	srv, _, url := newSeqServer(t, fastTiming)
	var wg sync.WaitGroup
	// Immediate disconnects.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c, _, err := websocket.DefaultDialer.Dial(url, nil); err == nil {
				_ = c.Close()
			}
		}()
	}
	wg.Wait()
	waitFor(t, 5*time.Second, "dropped sessions to unregister", func() bool { return srv.registry.Count() == 0 })

	// Shutdown while connections are being established.
	var conns sync.Map
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			time.Sleep(time.Duration(i) * time.Millisecond)
			c, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				return // refused with 503 while draining: fine
			}
			conns.Store(i, readAll(c))
		}(i)
	}
	time.Sleep(10 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	waitFor(t, 5*time.Second, "all sessions to close", func() bool { return srv.registry.Count() == 0 })
	conns.Range(func(k, v any) bool {
		rd := v.(*reader)
		select {
		case err := <-rd.errc:
			var ce *websocket.CloseError
			if !errors.As(err, &ce) || ce.Code != websocket.CloseServiceRestart {
				t.Errorf("connection %v ended with %v, want 1012", k, err)
			}
		case <-time.After(3 * time.Second):
			t.Errorf("connection %v still open after shutdown", k)
		}
		return true
	})
}
