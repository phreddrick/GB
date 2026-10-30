package game

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/enroll"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/platform/metrics"
	"github.com/phreddrick/GB/go/internal/universe"
)

// testConfig returns a validated configuration for an isolated server.
func testConfig(t *testing.T, dbPath string) *config.Config {
	t.Helper()
	cfg, _, err := config.Load("", config.MapEnv(map[string]string{
		"GB_JWT_SECRET":  "0123456789abcdef0123456789abcdef-test",
		"GB_ADMIN_TOKEN": "admin-token-0123456789",
		"GB_DB_PATH":     dbPath,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// newTestEngine opens a temporary store, optionally initialises a small
// seeded universe, and starts the actor. It is stopped at test end.
func newTestEngine(t *testing.T, initialized bool) (*State, *db.Store, *config.Config) {
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
	s, err := New(cfg, store, Options{Registry: metrics.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
		_ = store.Close()
	})
	return s, store, cfg
}

func fixtureRace(t *testing.T, num int, name, password string) *model.Race {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	r := &model.Race{PlayerNum: num, Name: name, Password: string(hash), Governors: 1, IQ: 100, Tech: 1}
	r.Governor[0] = model.Governor{Name: name, Active: true, Money: 1000}
	return r
}

type recordingResponder struct {
	mu    sync.Mutex
	lines []string
}

func (r *recordingResponder) Send(text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, text)
	return nil
}
func (r *recordingResponder) SendJSON(v interface{}) error { return nil }
func (r *recordingResponder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "")
}

func ctxT(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

// TestAdminUpdateDoesNotCrash reproduces CR-04: the pre-Phase-1 server
// enqueued an "@@update" text command with a nil responder and panicked.
// The typed request must run a real update and leave the actor serving.
func TestAdminUpdateDoesNotCrash(t *testing.T) {
	s, _, _ := newTestEngine(t, true)
	if s.Mode() != ModeServing {
		t.Fatalf("mode %s", s.Mode())
	}
	before := s.Status()
	if err := s.TriggerUpdate(ctxT(t, 30*time.Second)); err != nil {
		t.Fatalf("TriggerUpdate: %v", err)
	}
	if err := s.TriggerSegment(ctxT(t, 30*time.Second)); err != nil {
		t.Fatalf("TriggerSegment: %v", err)
	}
	after := s.Status()
	if after.NumUpdates != before.NumUpdates+1 || after.NumSegments != 1 {
		t.Fatalf("counters before=%+v after=%+v", before, after)
	}
	if !after.NextUpdate.After(before.NextUpdate) {
		t.Fatal("next update not advanced")
	}
	if s.Mode() != ModeServing {
		t.Fatalf("mode after turns %s", s.Mode())
	}
	// The actor still serves commands afterwards.
	resp := &recordingResponder{}
	if err := s.HandleCommand(ctxT(t, 5*time.Second), 1, 0, "who", resp); err != nil {
		t.Fatal(err)
	}
	if s.Metrics().TurnsTotal.Value("update", "ok") != 1 || s.Metrics().TurnsTotal.Value("segment", "ok") != 1 {
		t.Fatal("turn metrics not recorded")
	}
	if s.Metrics().AdminOps.Value("update", "ok") != 1 {
		t.Fatal("admin metric not recorded")
	}
}

// TestHandlerPanicHaltsEngine injects a panic into command dispatch and
// checks the fail-closed policy: the actor survives, the engine is HALTED,
// mutating operations are refused, and Resume does not clear it.
func TestHandlerPanicHaltsEngine(t *testing.T) {
	s, _, _ := newTestEngine(t, true)
	s.testHooks = &testHooks{panicVerb: "@@panic"}
	resp := &recordingResponder{}
	if err := s.HandleCommand(ctxT(t, 5*time.Second), 1, 0, "@@panic", resp); err != nil {
		t.Fatalf("HandleCommand returned %v (the done channel must still close)", err)
	}
	if s.Mode() != ModeHalted {
		t.Fatalf("mode %s, want HALTED", s.Mode())
	}
	if !strings.Contains(resp.text(), "halted") {
		t.Fatalf("player not told: %q", resp.text())
	}
	reason, stack := s.HaltDetails()
	if !strings.Contains(reason, "panic during command") || !strings.Contains(stack, "dispatchCommand") {
		t.Fatalf("halt details reason=%q stack has dispatch=%v", reason, strings.Contains(stack, "dispatchCommand"))
	}
	if s.Metrics().CommandPanics.Value() != 1 || s.Metrics().Commands.Value("unknown", "panic") != 1 {
		t.Fatal("panic metrics")
	}
	// Mutating work is refused; the actor is alive.
	if err := s.TriggerUpdate(ctxT(t, 5*time.Second)); !errors.Is(err, ErrNotServing) {
		t.Fatalf("update while halted: %v", err)
	}
	if err := s.Resume(ctxT(t, 5*time.Second)); !errors.Is(err, ErrHalted) {
		t.Fatalf("resume while halted: %v", err)
	}
	resp2 := &recordingResponder{}
	if err := s.HandleCommand(ctxT(t, 5*time.Second), 1, 0, "survey", resp2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp2.text(), "HALTED") {
		t.Fatalf("command while halted answered %q", resp2.text())
	}
	h := s.Health(time.Now())
	if !h.Alive || h.Ready || h.GameReady || h.HaltReason == "" {
		t.Fatalf("health while halted: %+v", h)
	}
	st := s.Status()
	if st.Mode != "HALTED" || st.HaltReason == "" {
		t.Fatalf("status %+v", st)
	}
	// Stop reports that nothing was flushed.
	if err := s.Stop(ctxT(t, 5*time.Second)); !errors.Is(err, ErrHaltedNotFlushed) {
		t.Fatalf("Stop while halted: %v", err)
	}
}

// TestTurnPanicHaltsAndKeepsSchedule: a panic inside a turn halts the engine
// and the persisted schedule is not advanced past the failed turn.
func TestTurnPanicHaltsAndKeepsSchedule(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	saved, err := store.GetSchedule()
	if err != nil || saved == nil {
		t.Fatalf("schedule not persisted at start: %v", err)
	}
	s.testHooks = &testHooks{beforeTurn: func(turnKind) { panic("injected turn failure") }}
	err = s.TriggerUpdate(ctxT(t, 10*time.Second))
	if !errors.Is(err, ErrHalted) {
		t.Fatalf("TriggerUpdate: %v", err)
	}
	if s.Mode() != ModeHalted {
		t.Fatalf("mode %s", s.Mode())
	}
	if s.Metrics().TurnPanics.Value() != 1 || s.Metrics().TurnsTotal.Value("update", "panic") != 1 {
		t.Fatal("turn panic metrics")
	}
	after, _ := store.GetSchedule()
	if after.NumUpdates != saved.NumUpdates || !after.NextUpdate.Equal(saved.NextUpdate) {
		t.Fatalf("schedule advanced after a failed turn: before %+v after %+v", saved, after)
	}
}

// TestQuiesceResume checks the pause semantics and the refusal of commands.
func TestQuiesceResume(t *testing.T) {
	s, _, _ := newTestEngine(t, true)
	if err := s.Quiesce(ctxT(t, 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if s.Mode() != ModeQuiesced {
		t.Fatalf("mode %s", s.Mode())
	}
	h := s.Health(time.Now())
	if !h.Alive || h.Ready {
		t.Fatalf("quiesced health %+v", h)
	}
	if err := s.TriggerUpdate(ctxT(t, 5*time.Second)); !errors.Is(err, ErrNotServing) {
		t.Fatalf("update while quiesced: %v", err)
	}
	resp := &recordingResponder{}
	_ = s.HandleCommand(ctxT(t, 5*time.Second), 1, 0, "survey", resp)
	if !strings.Contains(resp.text(), "paused") {
		t.Fatalf("command while quiesced: %q", resp.text())
	}
	// Safe verbs still work.
	resp2 := &recordingResponder{}
	_ = s.HandleCommand(ctxT(t, 5*time.Second), 1, 0, "who", resp2)
	if strings.Contains(resp2.text(), "paused") {
		t.Fatalf("who must work while quiesced: %q", resp2.text())
	}
	if err := s.Quiesce(ctxT(t, 5*time.Second)); err != nil {
		t.Fatalf("double quiesce must be a no-op: %v", err)
	}
	if err := s.Resume(ctxT(t, 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if s.Mode() != ModeServing {
		t.Fatalf("mode after resume %s", s.Mode())
	}
	if err := s.TriggerUpdate(ctxT(t, 30*time.Second)); err != nil {
		t.Fatalf("update after resume: %v", err)
	}
}

// TestAdminRequestsAreBoundedAndSerialized: a request that cannot be
// accepted before the caller's deadline returns ErrEngineBusy instead of
// blocking, and two concurrent update requests never run turns concurrently.
func TestAdminRequestsAreBoundedAndSerialized(t *testing.T) {
	s, _, _ := newTestEngine(t, true)
	var inTurn, maxInTurn int32
	var mu sync.Mutex
	release := make(chan struct{})
	s.testHooks = &testHooks{beforeTurn: func(turnKind) {
		mu.Lock()
		inTurn++
		if inTurn > maxInTurn {
			maxInTurn = inTurn
		}
		mu.Unlock()
		<-release
		mu.Lock()
		inTurn--
		mu.Unlock()
	}}
	errs := make(chan error, 2)
	go func() { errs <- s.TriggerUpdate(ctxT(t, 30*time.Second)) }()
	go func() { errs <- s.TriggerSegment(ctxT(t, 30*time.Second)) }()
	// While the first turn blocks, a short-deadline request reports busy
	// (the actor is occupied) rather than hanging.
	time.Sleep(50 * time.Millisecond)
	if err := s.Quiesce(ctxT(t, 100*time.Millisecond)); !errors.Is(err, ErrEngineBusy) {
		t.Fatalf("expected ErrEngineBusy, got %v", err)
	}
	// Health during the turn: alive via progress/turn start, mode TURN_IN_PROGRESS.
	h := s.Health(time.Now())
	if !h.TurnRunning || h.Mode != ModeTurnInProgress || !h.Alive || !h.Ready {
		t.Fatalf("health during turn %+v", h)
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}
	if maxInTurn != 1 {
		t.Fatalf("turns overlapped: max concurrent %d", maxInTurn)
	}
	// The quiesce request that timed out was never executed (the actor
	// dropped nothing: it was simply not accepted) or, if accepted late,
	// its effect is visible; either way the engine is in a defined state.
	mode := s.Mode()
	if mode != ModeServing && mode != ModeQuiesced {
		t.Fatalf("undefined mode %s", mode)
	}
}

// TestLivenessWatchdog: an actor stuck without progress is reported not
// alive; a turn that advances progress stays alive.
func TestLivenessWatchdog(t *testing.T) {
	s, _, cfg := newTestEngine(t, true)
	now := time.Now()
	// Idle actor: alive within HeartbeatStale, not after.
	if !s.Health(now).Alive {
		t.Fatal("fresh actor must be alive")
	}
	if s.Health(now.Add(HeartbeatStale + time.Second)).Alive {
		t.Fatal("stale heartbeat without a turn must be not alive")
	}
	// Simulate a running turn: start long ago, progress recently.
	s.turnStartNs.Store(now.Add(-2 * time.Hour).UnixNano())
	s.progressNs.Store(now.Add(-time.Minute).UnixNano())
	stall := time.Duration(cfg.TurnStallTimeoutSecs) * time.Second
	if !s.Health(now.Add(HeartbeatStale + time.Second)).Alive {
		t.Fatal("a turn with recent progress must be alive")
	}
	if s.Health(now.Add(stall + 2*time.Minute)).Alive {
		t.Fatal("a turn without progress for longer than the stall timeout must be not alive")
	}
	s.turnStartNs.Store(0)
}

// TestScheduleRestoredAcrossRestart (CR-21): counters and next times
// survive an orderly stop and a new State on the same store.
func TestScheduleRestoredAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gb.db")
	cfg := testConfig(t, dbPath)
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := universe.Init(store, universe.Params{Stars: 3, MinPlanets: 1, MaxPlanets: 1}, 5, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	s1, err := New(cfg, store, Options{Registry: metrics.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.TriggerUpdate(ctxT(t, 30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s1.TriggerSegment(ctxT(t, 30*time.Second)); err != nil {
		t.Fatal(err)
	}
	st1 := s1.Status()
	if err := s1.Stop(ctxT(t, 10*time.Second)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if s1.Mode() != ModeStopped {
		t.Fatalf("mode after stop %s", s1.Mode())
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store2, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store2.Close() }()
	s2, err := New(cfg, store2, Options{Registry: metrics.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Stop(ctxT(t, 10*time.Second)) }()
	st2 := s2.Status()
	if st2.NumUpdates != st1.NumUpdates || st2.NumSegments != st1.NumSegments {
		t.Fatalf("counters not restored: before %d/%d after %d/%d", st1.NumUpdates, st1.NumSegments, st2.NumUpdates, st2.NumSegments)
	}
	if !st2.NextUpdate.Equal(st1.NextUpdate) || !st2.NextSegment.Equal(st1.NextSegment) {
		t.Fatalf("next times not restored: before %v/%v after %v/%v", st1.NextUpdate, st1.NextSegment, st2.NextUpdate, st2.NextSegment)
	}
	if st2.Universe == nil || st2.Universe.Seed != 5 || st2.Stars != 3 {
		t.Fatalf("universe not the same: %+v", st2)
	}
	if s2.Metrics().MissedTurns.Value("update") != 0 {
		t.Fatal("no missed turns expected")
	}
}

// TestOverdueScheduleIsNotCaughtUp: a persisted next-update in the past is
// moved forward and counted, not executed.
func TestOverdueScheduleIsNotCaughtUp(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gb.db")
	cfg := testConfig(t, dbPath)
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := universe.Init(store, universe.Params{Stars: 2, MinPlanets: 1, MaxPlanets: 1}, 5, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	if err := store.PutSchedule(&db.ScheduleRecord{NextUpdate: past, NextSegment: past, NumUpdates: 7, NumSegments: 2, UpdateMinutes: cfg.DefaultUpdateMinutes, MovesPerUpdate: cfg.MovesPerUpdate, SavedAt: past}); err != nil {
		t.Fatal(err)
	}
	s, err := New(cfg, store, Options{Registry: metrics.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(ctxT(t, 10*time.Second)) }()
	time.Sleep(100 * time.Millisecond) // give the actor loop a chance to misbehave
	st := s.Status()
	if st.NumUpdates != 7 {
		t.Fatalf("missed turns were executed or counter lost: %d", st.NumUpdates)
	}
	if !st.NextUpdate.After(time.Now()) {
		t.Fatalf("overdue update not rescheduled: %v", st.NextUpdate)
	}
	if st.NumSegments != 0 {
		t.Fatalf("segment counter should restart with the new cycle: %d", st.NumSegments)
	}
	if s.Metrics().MissedTurns.Value("update") < 1 {
		t.Fatal("missed turns not counted")
	}
	rec, _ := store.GetSchedule()
	if !rec.NextUpdate.Equal(st.NextUpdate) {
		t.Fatal("rescheduled time not persisted")
	}
}

// TestUninitializedStoreAndAdminInit (CR-05): a server on an empty store
// serves admin only, refuses player commands, initialises exactly once
// through the actor, and then serves. Ordinary startup never generates.
func TestUninitializedStoreAndAdminInit(t *testing.T) {
	s, store, _ := newTestEngine(t, false)
	if s.Mode() != ModeUninitialized {
		t.Fatalf("mode %s", s.Mode())
	}
	h := s.Health(time.Now())
	if !h.Alive || !h.Ready || h.GameReady {
		t.Fatalf("uninitialized health %+v", h)
	}
	resp := &recordingResponder{}
	_ = s.HandleCommand(ctxT(t, 5*time.Second), 1, 0, "survey", resp)
	if !strings.Contains(resp.text(), "not been initialised") {
		t.Fatalf("command on uninitialised: %q", resp.text())
	}
	if err := s.TriggerUpdate(ctxT(t, 5*time.Second)); !errors.Is(err, ErrNotServing) {
		t.Fatalf("update on uninitialised: %v", err)
	}
	stars0, _, _, _, _ := store.Counts()
	if stars0 != 0 {
		t.Fatal("startup generated a universe")
	}

	// Two concurrent init requests: exactly one succeeds.
	type res struct {
		rec *db.UniverseRecord
		err error
	}
	results := make(chan res, 2)
	for i := 0; i < 2; i++ {
		go func(seed int64) {
			rec, err := s.InitUniverse(ctxT(t, 60*time.Second), universe.Params{Stars: 3, MinPlanets: 1, MaxPlanets: 2}, seed)
			results <- res{rec, err}
		}(int64(100 + i))
	}
	okCount, refused := 0, 0
	for i := 0; i < 2; i++ {
		r := <-results
		switch {
		case r.err == nil && r.rec != nil:
			okCount++
		case errors.Is(r.err, universe.ErrAlreadyInitialized):
			refused++
		default:
			t.Fatalf("unexpected init result %+v %v", r.rec, r.err)
		}
	}
	if okCount != 1 || refused != 1 {
		t.Fatalf("ok=%d refused=%d", okCount, refused)
	}
	if s.Mode() != ModeServing {
		t.Fatalf("mode after init %s", s.Mode())
	}
	st := s.Status()
	if st.Stars != 3 || st.Universe == nil || st.NextUpdate.IsZero() {
		t.Fatalf("status after init %+v", st)
	}
	if err := s.TriggerUpdate(ctxT(t, 30*time.Second)); err != nil {
		t.Fatalf("update after init: %v", err)
	}
	// A third request on the now-initialised engine is refused.
	if _, err := s.InitUniverse(ctxT(t, 5*time.Second), universe.Params{}, 9); !errors.Is(err, universe.ErrAlreadyInitialized) {
		t.Fatalf("re-init: %v", err)
	}
}

// TestEnrollmentDisabledDoesNotTouchStore (CR-03, Decision 9): the default
// refuses enrollment without reading or writing, and the preview path uses
// the actor's store handle (no second open, no five-second lock timeout).
func TestEnrollmentDisabledDoesNotTouchStore(t *testing.T) {
	s, store, cfg := newTestEngine(t, true)
	req := &enroll.Request{RaceName: "Newcomers", Password: "secret1", IQ: 50, Fertilize: 50, BirthRate: 0.5, Mass: 1, Metabolism: 1, NumberSexes: 2, Adventurism: 50}
	_, _, races0, _, _ := store.Counts()
	start := time.Now()
	resp, err := s.Enroll(ctxT(t, 5*time.Second), req)
	if !errors.Is(err, ErrEnrollmentDisabled) || resp.Success {
		t.Fatalf("disabled enrollment: %+v %v", resp, err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("disabled enrollment took %s (lock timeout?)", time.Since(start))
	}
	_, _, races1, _, _ := store.Counts()
	if races1 != races0 {
		t.Fatal("disabled enrollment mutated the store")
	}

	// Preview enabled: runs through the actor against the shared handle.
	cfg.EnrollPreview = true
	start = time.Now()
	resp, err = s.Enroll(ctxT(t, 5*time.Second), req)
	if err != nil || !resp.Success || resp.PlayerNum != 2 {
		t.Fatalf("preview enrollment: %+v %v", resp, err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("preview enrollment took %s; the old path timed out after 5 s on the bbolt lock", d)
	}
	if !strings.Contains(resp.Message, "PREVIEW ONLY") {
		t.Fatalf("preview response must say it is incomplete: %q", resp.Message)
	}
	_, _, races2, _, _ := store.Counts()
	if races2 != races0+1 {
		t.Fatalf("race not stored: %d -> %d", races0, races2)
	}
	// The new race can authenticate without a restart (memory updated too).
	auth, err := s.Authenticate(ctxT(t, 5*time.Second), "Newcomers", "secret1", 0)
	if err != nil || !auth.OK || auth.PlayerNum != 2 {
		t.Fatalf("auth after enrollment: %+v %v", auth, err)
	}
	// Duplicate name is rejected without an error.
	resp, err = s.Enroll(ctxT(t, 5*time.Second), req)
	if err != nil || resp.Success {
		t.Fatalf("duplicate enrollment: %+v %v", resp, err)
	}
	// Validation failures are reported.
	bad := *req
	bad.RaceName = "Bad"
	bad.IQ = 0
	resp, err = s.Enroll(ctxT(t, 5*time.Second), &bad)
	if err != nil || resp.Success || len(resp.Errors) == 0 {
		t.Fatalf("invalid enrollment: %+v %v", resp, err)
	}
}

// TestAuthenticateThroughActor: credentials are checked on the actor, so a
// login during a turn waits (bounded) instead of racing with the turn.
func TestAuthenticateThroughActor(t *testing.T) {
	s, _, _ := newTestEngine(t, true)
	auth, err := s.Authenticate(ctxT(t, 5*time.Second), "testers", "pw-testers", 0)
	if err != nil || !auth.OK || auth.PlayerNum != 1 {
		t.Fatalf("auth %+v %v", auth, err)
	}
	auth, err = s.Authenticate(ctxT(t, 5*time.Second), "Testers", "wrong", 0)
	if err != nil || auth.OK {
		t.Fatalf("wrong password accepted: %+v %v", auth, err)
	}
	if !strings.Contains(s.WelcomeMessage(ctxT(t, 5*time.Second), 1, 0), "Testers") {
		t.Fatal("welcome message")
	}
	// Concurrent logins during updates (race detector coverage for CR-20).
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				_, _ = s.Authenticate(ctxT(t, 30*time.Second), "Testers", "pw-testers", 0)
			}
		}()
	}
	for i := 0; i < 3; i++ {
		if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}
	wg.Wait()
}

// TestStopFlushesAndClosesCleanly: orderly stop flushes memory, persists
// the schedule and refuses work afterwards; a second Stop is harmless.
func TestStopFlushesAndClosesCleanly(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	// Mutate an in-memory race (memory-authoritative record) via the actor.
	if err := s.request(ctxT(t, 5*time.Second), opTest, func() error {
		s.races[0].Motto = "flushed-on-shutdown"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctxT(t, 10*time.Second)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := s.Stop(ctxT(t, time.Second)); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	r, err := store.GetRace(1)
	if err != nil || r == nil || r.Motto != "flushed-on-shutdown" {
		t.Fatalf("race not flushed on shutdown: %+v %v", r, err)
	}
	if err := s.HandleCommand(ctxT(t, time.Second), 1, 0, "who", &recordingResponder{}); !errors.Is(err, ErrEngineStopped) {
		t.Fatalf("command after stop: %v", err)
	}
	if err := s.TriggerUpdate(ctxT(t, time.Second)); !errors.Is(err, ErrEngineStopped) {
		t.Fatalf("admin after stop: %v", err)
	}
	if s.Health(time.Now()).Alive {
		t.Fatal("stopped engine must not report alive")
	}
}

// TestStopTimeoutDuringTurn: Stop returns ErrShutdownTimeout when a turn
// outlives the budget and the turn still completes afterwards (nothing is
// torn down under it).
func TestStopTimeoutDuringTurn(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	release := make(chan struct{})
	s.testHooks = &testHooks{beforeTurn: func(turnKind) { <-release }}
	turnErr := make(chan error, 1)
	go func() { turnErr <- s.TriggerUpdate(ctxT(t, 60*time.Second)) }()
	time.Sleep(50 * time.Millisecond)
	err := s.Stop(ctxT(t, 200*time.Millisecond))
	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("Stop during turn: %v", err)
	}
	if s.Health(time.Now()).Ready {
		t.Fatal("draining engine must not be ready")
	}
	close(release)
	if err := <-turnErr; err != nil {
		t.Fatalf("turn after timed-out stop: %v", err)
	}
	select {
	case <-s.Stopped():
	case <-time.After(10 * time.Second):
		t.Fatal("actor did not stop after the turn finished")
	}
	if s.Mode() != ModeStopped {
		t.Fatalf("mode %s", s.Mode())
	}
	rec, _ := store.GetSchedule()
	if rec.NumUpdates != 1 {
		t.Fatalf("turn that finished after the timeout was not persisted: %+v", rec)
	}
}

// TestStopReportsFlushFailure: a store that cannot be written during the
// shutdown flush yields an error, not a silent success.
func TestStopReportsFlushFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gb.db")
	cfg := testConfig(t, dbPath)
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := universe.Init(store, universe.Params{Stars: 2, MinPlanets: 1, MaxPlanets: 1}, 5, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	s, err := New(cfg, store, Options{Registry: metrics.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close() // simulate an unusable store at shutdown
	err = s.Stop(ctxT(t, 10*time.Second))
	if err == nil || errors.Is(err, ErrShutdownTimeout) || errors.Is(err, ErrHaltedNotFlushed) {
		t.Fatalf("expected a flush error, got %v", err)
	}
}

// TestCanonicalVerbMatchesDispatchOrder guards the metrics label table
// against drift from the dispatch switch.
func TestCanonicalVerbMatchesDispatchOrder(t *testing.T) {
	for in, want := range map[string]string{"wh": "who", "s": "shout", "su": "survey", "se": "sell", "send": "telegram", "zzz": "unknown", "": "empty", "MAP x": "map"} {
		if got := canonicalVerb(in); got != want {
			t.Errorf("canonicalVerb(%q) = %q, want %q", in, got, want)
		}
	}
}
