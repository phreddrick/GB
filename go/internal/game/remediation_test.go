package game

// Regression tests for the independent Codex review findings GB1-01 (actor
// request timeouts and cancellation) and GB1-03 (failed resume must fail
// closed). They drive the real actor, the real bbolt store and the real
// request path; nothing here is mocked. Run with -race.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/enroll"
	"github.com/phreddrick/GB/go/internal/platform/metrics"
	"github.com/phreddrick/GB/go/internal/universe"
)

// quietEngine returns an engine whose log goes to a private buffer.
func quietEngine(t *testing.T, initialized bool) (*State, *db.Store, *config.Config) {
	t.Helper()
	return newLoggedEngine(t, &syncBuffer{}, initialized)
}

// blockActor parks the actor inside a test request until release is called,
// so that later requests stay queued. release waits for the parked request
// to finish.
func blockActor(t *testing.T, s *State) (release func()) {
	t.Helper()
	entered, rel, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		_ = s.request(context.Background(), opTest, func() error { close(entered); <-rel; return nil })
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("actor did not pick up the blocking request")
	}
	var once sync.Once
	return func() { once.Do(func() { close(rel); <-done }) }
}

// barrier returns once every request queued before it has been handled.
func barrier(t *testing.T, s *State) {
	t.Helper()
	if err := s.request(ctxT(t, 10*time.Second), opTest, func() error { return nil }); err != nil {
		t.Fatalf("barrier: %v", err)
	}
}

func expectNotExecuted(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, ErrEngineBusy) || !errors.Is(err, ErrNotExecuted) || errors.Is(err, ErrStillRunning) {
		t.Fatalf("%s: want ErrEngineBusy+ErrNotExecuted, got %v", what, err)
	}
}

// TestGB101AuthenticateTimeoutWhileActorBusy: a login that times out while
// queued behind busy work is reported as not executed and is skipped by the
// actor later (it never runs against the races).
func TestGB101AuthenticateTimeoutWhileActorBusy(t *testing.T) {
	s, _, _ := quietEngine(t, true)
	release := blockActor(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	res, err := s.Authenticate(ctx, "Testers", "pw-testers", 0)
	cancel()
	expectNotExecuted(t, "authenticate", err)
	if res.OK {
		t.Fatal("timed-out authentication returned a successful result")
	}
	release()
	barrier(t, s)
	if got := s.metrics.Canceled.Value(string(opAuthenticate)); got != 1 {
		t.Fatalf("canceled authenticate counter = %v, want 1", got)
	}
}

// TestGB101AuthenticateTimeoutRacesReplyDelivery reproduces the Codex race:
// the caller's deadline expires while the actor is computing the result.
// Each outcome must be one of: a delivered result, "not executed", or
// "still running"; the race detector must stay quiet (the old code wrote a
// captured variable the caller was already reading).
func TestGB101AuthenticateTimeoutRacesReplyDelivery(t *testing.T) {
	s, _, _ := quietEngine(t, true)
	// Cost 12 makes one check take long enough for the timeout to fire
	// mid-execution, as in the Codex reproduction.
	slow, err := bcrypt.GenerateFromPassword([]byte("pw-slow"), 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.request(ctxT(t, 10*time.Second), opTest, func() error { s.races[0].Password = string(slow); return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	_, err = s.Authenticate(ctx, "Testers", "pw-slow", 0)
	cancel()
	if !errors.Is(err, ErrStillRunning) {
		t.Fatalf("slow check interrupted by the deadline: want ErrStillRunning, got %v", err)
	}
	barrier(t, s)

	// Now hammer the timeout/reply window with a cheap hash and deadlines
	// around its duration.
	fast := fixtureRace(t, 1, "Testers", "pw-fast")
	if err := s.request(ctxT(t, 10*time.Second), opTest, func() error { s.races[0].Password = fast.Password; return nil }); err != nil {
		t.Fatal(err)
	}
	// Calibrate: deadlines are drawn from [0, 2 x one authentication], so
	// that about half of them expire during execution and half after the
	// reply, on any machine and with or without -race.
	var spent time.Duration
	for i := 0; i < 20; i++ {
		t0 := time.Now()
		if _, err := s.Authenticate(ctxT(t, 10*time.Second), "Testers", "pw-fast", 0); err != nil {
			t.Fatal(err)
		}
		spent += time.Since(t0)
	}
	window := int64(2 * spent / 20)
	// Sequential, so that each deadline falls inside its own request's
	// queue wait or execution rather than behind the other callers.
	rng := rand.New(rand.NewSource(1)) //nolint:gosec // G404: test timing jitter
	var delivered, notRun, running atomic.Int32
	for i := 0; i < 300; i++ {
		barrier(t, s) // the actor is idle at the start of every attempt
		d := time.Duration(rng.Int63n(window) + 1)
		ctx, cancel := context.WithTimeout(context.Background(), d)
		res, err := s.Authenticate(ctx, "Testers", "pw-fast", 0)
		cancel()
		switch {
		case err == nil:
			if !res.OK || res.PlayerNum != 1 {
				t.Errorf("delivered result wrong: %+v", res)
			}
			delivered.Add(1)
		case errors.Is(err, ErrNotExecuted):
			notRun.Add(1)
		case errors.Is(err, ErrStillRunning):
			running.Add(1)
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	barrier(t, s)
	t.Logf("outcomes: delivered=%d not_executed=%d still_running=%d", delivered.Load(), notRun.Load(), running.Load())
	if delivered.Load() == 0 || running.Load() == 0 {
		t.Errorf("the timeout/reply window was not exercised on both sides (delivered=%d still_running=%d)", delivered.Load(), running.Load())
	}
}

// TestGB101CancellationPolicyIsDeterministic checks the tie-break between
// cancellation and the start of execution over many randomized races: a
// request reported as not executed never ran, and a request reported as
// delivered or still running ran exactly once.
func TestGB101CancellationPolicyIsDeterministic(t *testing.T) {
	s, _, _ := quietEngine(t, true)
	const n = 400
	runs := make([]atomic.Int32, n)
	errs := make([]error, n)
	rng := rand.New(rand.NewSource(2)) //nolint:gosec // G404: test timing jitter
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		d := time.Duration(rng.Intn(400)) * time.Microsecond
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), d)
			defer cancel()
			errs[i] = s.request(ctx, opTest, func() error {
				runs[i].Add(1)
				time.Sleep(50 * time.Microsecond)
				return nil
			})
		}(i)
	}
	wg.Wait()
	barrier(t, s)
	var notRun, ran int
	for i := 0; i < n; i++ {
		got := runs[i].Load()
		switch {
		case errs[i] == nil, errors.Is(errs[i], ErrStillRunning):
			ran++
			if got != 1 {
				t.Errorf("request %d reported as run/running (%v) but ran %d times", i, errs[i], got)
			}
		case errors.Is(errs[i], ErrNotExecuted):
			notRun++
			if got != 0 {
				t.Errorf("request %d reported as not executed but ran %d times", i, got)
			}
		default:
			t.Errorf("request %d: unexpected error %v", i, errs[i])
		}
	}
	if notRun == 0 || ran == 0 {
		t.Logf("note: only one outcome observed (not_executed=%d ran=%d); timing did not exercise both branches", notRun, ran)
	}
}

// TestGB101CanceledQueuedUpdateNeverRuns is the Codex reproduction: an
// update queued behind other work whose caller times out must not run when
// the actor gets to it.
func TestGB101CanceledQueuedUpdateNeverRuns(t *testing.T) {
	s, _, _ := quietEngine(t, true)
	before := s.Status().NumUpdates
	turnsBefore := s.metrics.TurnsTotal.Value("update", "ok")
	release := blockActor(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err := s.TriggerUpdate(ctx)
	cancel()
	expectNotExecuted(t, "update", err)
	if !strings.Contains(err.Error(), "canceled while queued") {
		t.Fatalf("error does not say the update was canceled while queued: %v", err)
	}
	release()
	barrier(t, s)
	if got := s.Status().NumUpdates; got != before {
		t.Fatalf("queued update executed AFTER its caller timed out: updates %d -> %d", before, got)
	}
	if got := s.metrics.TurnsTotal.Value("update", "ok"); got != turnsBefore {
		t.Fatalf("turn counter moved: %v -> %v", turnsBefore, got)
	}
	if got := s.metrics.AdminOps.Value(string(opUpdate), "not_executed"); got != 1 {
		t.Fatalf("admin metric for the canceled update = %v, want 1 not_executed", got)
	}
}

// TestGB101AdmissionRespectsCanceledContext: a context that has already
// ended never enqueues anything.
func TestGB101AdmissionRespectsCanceledContext(t *testing.T) {
	s, _, _ := quietEngine(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var ran atomic.Bool
	for i := 0; i < 50; i++ {
		err := s.request(ctx, opTest, func() error { ran.Store(true); return nil })
		expectNotExecuted(t, "pre-canceled request", err)
	}
	expectNotExecuted(t, "pre-canceled update", s.TriggerUpdate(ctx))
	barrier(t, s)
	if ran.Load() {
		t.Fatal("a request with an already canceled context was executed")
	}
	if got := s.Status().NumUpdates; got != 0 {
		t.Fatalf("pre-canceled update ran: %d updates", got)
	}
}

// TestGB101UpdateStartedBeforeTimeoutCompletes: an update that has started
// when its caller times out runs to completion and is persisted; the caller
// is told it is still running (not that it was rolled back).
func TestGB101UpdateStartedBeforeTimeoutCompletes(t *testing.T) {
	s, store, _ := quietEngine(t, true)
	started, rel := make(chan struct{}), make(chan struct{})
	s.testHooks = &testHooks{beforeTurn: func(turnKind) { close(started); <-rel }}
	before := s.Status().NumUpdates
	errc := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		errc <- s.TriggerUpdate(ctx)
	}()
	<-started
	err := <-errc
	if !errors.Is(err, ErrEngineBusy) || !errors.Is(err, ErrStillRunning) || errors.Is(err, ErrNotExecuted) {
		t.Fatalf("want ErrEngineBusy+ErrStillRunning, got %v", err)
	}
	if !strings.Contains(err.Error(), "not rolled back") {
		t.Fatalf("message must not imply a rollback: %v", err)
	}
	close(rel)
	barrier(t, s)
	s.testHooks = nil
	if got := s.Status().NumUpdates; got != before+1 {
		t.Fatalf("started update did not complete: updates %d -> %d", before, got)
	}
	rec, err := store.GetSchedule()
	if err != nil || rec == nil || rec.NumUpdates != before+1 {
		t.Fatalf("completed update not persisted: %+v %v", rec, err)
	}
	if got := s.metrics.AdminOps.Value(string(opUpdate), "still_running"); got != 1 {
		t.Fatalf("admin metric still_running = %v, want 1", got)
	}
}

// TestGB101WelcomeEnrollInitTimeouts covers the remaining request paths:
// each canceled while queued returns promptly, changes nothing, and is
// skipped by the actor.
func TestGB101WelcomeEnrollInitTimeouts(t *testing.T) {
	t.Run("welcome", func(t *testing.T) {
		s, _, _ := quietEngine(t, true)
		release := blockActor(t, s)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		msg := s.WelcomeMessage(ctx, 1, 0)
		cancel()
		if msg != "\nWelcome to Galactic Bloodshed\n" {
			t.Fatalf("timed-out welcome = %q, want the generic greeting", msg)
		}
		release()
		barrier(t, s)
		if got := s.metrics.Canceled.Value(string(opWelcome)); got != 1 {
			t.Fatalf("canceled welcome counter = %v", got)
		}
		if msg := s.WelcomeMessage(ctxT(t, 5*time.Second), 1, 0); !strings.Contains(msg, "Welcome back, Testers") {
			t.Fatalf("normal welcome = %q", msg)
		}
	})
	t.Run("enroll", func(t *testing.T) {
		s, store, cfg := quietEngine(t, true)
		cfg.EnrollPreview = true
		release := blockActor(t, s)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		resp, err := s.Enroll(ctx, &enroll.Request{RaceName: "Latecomers", Password: "pw-latecomers-123"}) //nolint:gosec // G101: test fixture credential
		cancel()
		expectNotExecuted(t, "enroll", err)
		if resp.Success || resp.Message == "" {
			t.Fatalf("timed-out enrollment response: %+v", resp)
		}
		release()
		barrier(t, s)
		races, err := store.AllRaces()
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range races {
			if r != nil && r.Name == "Latecomers" {
				t.Fatal("canceled enrollment stored a race later")
			}
		}
	})
	t.Run("universe-init", func(t *testing.T) {
		s, store, _ := quietEngine(t, false)
		release := blockActor(t, s)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		rec, err := s.InitUniverse(ctx, universe.Params{Stars: 4, MinPlanets: 1, MaxPlanets: 2}, 7)
		cancel()
		expectNotExecuted(t, "universe init", err)
		if rec != nil {
			t.Fatalf("timed-out init returned a record: %+v", rec)
		}
		release()
		barrier(t, s)
		if init, err := store.IsInitialized(); init || err != nil {
			t.Fatalf("canceled universe init initialised the store later: %v %v", init, err)
		}
		if s.Mode() != ModeUninitialized {
			t.Fatalf("mode after canceled init: %s", s.Mode())
		}
	})
}

// TestGB101CanceledQueuedCommandNeverRuns: a player command whose wait
// ended while it was queued is skipped, so the player is never told "not
// run" about a command that later runs.
func TestGB101CanceledQueuedCommandNeverRuns(t *testing.T) {
	s, _, _ := quietEngine(t, true)
	release := blockActor(t, s)
	sess := &recordingResponder{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err := s.HandleCommand(ctx, 1, 0, "who", sess)
	cancel()
	expectNotExecuted(t, "command", err)
	release()
	barrier(t, s)
	if txt := sess.text(); txt != "" {
		t.Fatalf("canceled command produced output later: %q", txt)
	}
	if got := s.metrics.Commands.Value("who", "ok"); got != 0 {
		t.Fatalf("canceled command was dispatched: %v", got)
	}
	if got := s.metrics.Canceled.Value("command"); got != 1 {
		t.Fatalf("canceled command counter = %v", got)
	}
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// TestGB103ResumePersistenceFailureHalts is the Codex reproduction: quiesce,
// make the store unusable, resume. The engine must report failure, enter
// HALTED, never advertise readiness, refuse mutation, and discard memory at
// shutdown instead of flushing it.
func TestGB103ResumePersistenceFailureHalts(t *testing.T) {
	logs := &syncBuffer{}
	s, store, cfg := newLoggedEngine(t, logs, true)

	if err := s.Quiesce(ctxT(t, 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	statusBefore := s.Status()
	raceBefore, err := store.GetRace(1)
	if err != nil {
		t.Fatal(err)
	}
	// Memory change that must never reach the store.
	if err := s.request(ctxT(t, 5*time.Second), opTest, func() error { s.races[0].Motto = "must-not-persist"; return nil }); err != nil {
		t.Fatal(err)
	}
	logMark := len(logs.String())
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	err = s.Resume(ctxT(t, 5*time.Second))
	if err == nil {
		t.Fatal("resume succeeded although the schedule could not be written")
	}
	if !strings.Contains(err.Error(), "HALTED") {
		t.Fatalf("resume error does not say the engine halted: %v", err)
	}
	if s.Mode() != ModeHalted {
		t.Fatalf("mode after failed resume = %s, want HALTED", s.Mode())
	}
	h := s.Health(time.Now())
	if h.GameReady || h.Ready {
		t.Fatalf("readiness after failed resume: ready=%v game_ready=%v", h.Ready, h.GameReady)
	}
	st := s.Status()
	if !st.NextUpdate.Equal(statusBefore.NextUpdate) || !st.NextSegment.Equal(statusBefore.NextSegment) {
		t.Fatalf("status advertises an unpersisted schedule: before %v/%v after %v/%v",
			statusBefore.NextUpdate, statusBefore.NextSegment, st.NextUpdate, st.NextSegment)
	}
	if !errors.Is(s.TriggerUpdate(ctxT(t, 5*time.Second)), ErrHalted) {
		t.Fatal("update not refused after failed resume")
	}
	sess := &recordingResponder{}
	if err := s.HandleCommand(ctxT(t, 5*time.Second), 1, 0, "build", sess); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sess.text(), "HALTED") {
		t.Fatalf("mutating command not refused: %q", sess.text())
	}
	if !errors.Is(s.Resume(ctxT(t, 5*time.Second)), ErrHalted) {
		t.Fatal("second resume not refused as halted")
	}
	if got := s.metrics.AdminOps.Value(string(opResume), "ok"); got != 0 {
		t.Fatalf("success metric recorded for the failed resume: %v", got)
	}
	if got := s.metrics.AdminOps.Value(string(opResume), "error"); got != 1 {
		t.Fatalf("failure metric for the failed resume = %v, want 1", got)
	}
	after := logs.String()[logMark:]
	if strings.Contains(after, `"mode":"SERVING"`) || strings.Contains(after, `"msg":"admin operation","op":"admin.resume"`) {
		t.Fatalf("success logged for the failed resume:\n%s", after)
	}
	if !strings.Contains(after, "engine halted") {
		t.Fatalf("halt not logged:\n%s", after)
	}

	if err := s.Stop(ctxT(t, 5*time.Second)); !errors.Is(err, ErrHaltedNotFlushed) {
		t.Fatalf("stop after failed resume: %v, want ErrHaltedNotFlushed", err)
	}
	// Reopen the store: the memory-only change must not have been flushed.
	reopened, err := db.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	raceAfter, err := reopened.GetRace(1)
	if err != nil {
		t.Fatal(err)
	}
	if raceAfter.Motto != raceBefore.Motto {
		t.Fatalf("halted shutdown flushed memory: motto %q -> %q", raceBefore.Motto, raceAfter.Motto)
	}
	rec, err := reopened.GetSchedule()
	if err != nil || rec == nil || !rec.NextUpdate.Equal(statusBefore.NextUpdate) {
		t.Fatalf("persisted schedule changed: %+v %v", rec, err)
	}
}

// newLoggedEngine is newTestEngine with the engine's log captured in logs
// instead of the shared stderr. The GB1 tests use it everywhere: some of them
// produce hundreds of log lines, which would bloat the package's failure
// output that the golden-mutation self-test greps.
func newLoggedEngine(t *testing.T, logs *syncBuffer, initialized bool) (*State, *db.Store, *config.Config) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "gb.db")
	cfg := testConfig(t, dbPath)
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if initialized {
		if _, err := universe.Init(store, universe.Params{Stars: 4, MinPlanets: 1, MaxPlanets: 2}, 11, "test", time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := store.PutRace(fixtureRace(t, 1, "Testers", "pw-testers")); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(cfg, store, Options{Registry: metrics.NewRegistry(), Logger: slog.New(slog.NewJSONHandler(logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
		_ = store.Close()
	})
	return s, store, cfg
}
