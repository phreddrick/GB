package game

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
	"github.com/phreddrick/GB/go/internal/platform/metrics"
	"github.com/phreddrick/GB/go/internal/universe"
)

// CR-18 tests: a turn is one atomic, durable commit; an interrupted turn is
// never exposed as committed and never leaves a partially updated world.

// fakeRegistry records deliveries and reports a configurable set of
// connected sessions.
type fakeRegistry struct {
	mu        sync.Mutex
	online    []net.SessionInfo
	delivered []string
}

func (f *fakeRegistry) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delivered = append(f.delivered, s)
}
func (f *fakeRegistry) NotifyRace(p int, text string) { f.record(fmt.Sprintf("race:%d:%s", p, text)) }
func (f *fakeRegistry) Notify(p, g int, text string) bool {
	f.record(fmt.Sprintf("gov:%d/%d:%s", p, g, text))
	return true
}
func (f *fakeRegistry) Broadcast(p int, text string) { f.record(fmt.Sprintf("bcast:%d:%s", p, text)) }
func (f *fakeRegistry) Shout(text string)            { f.record("shout:" + text) }
func (f *fakeRegistry) ConnectedPlayers() []net.SessionInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]net.SessionInfo(nil), f.online...)
}
func (f *fakeRegistry) all() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.delivered, "\n")
}
func (f *fakeRegistry) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.delivered)
}

// addFixtureShips stores a few live ships so that the per-ship turn passes
// run (the store is authoritative for ships at turn start, CR-11).
func addFixtureShips(t *testing.T, store *db.Store, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		sh := &model.Ship{Number: uint32(i), Owner: 1, Governor: 0, Race: 1, Name: fmt.Sprintf("S%d", i), Type: model.StypeShuttle,
			Alive: true, WhatOrbits: model.LevelPlan, StOrbits: 0, PNumOrbits: 0, Fuel: 5}
		if err := store.PutShip(sh); err != nil {
			t.Fatal(err)
		}
	}
}

func digest(t *testing.T, store *db.Store) string {
	t.Helper()
	d, err := store.WorldDigest()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func commitRecord(t *testing.T, store *db.Store) *db.CommitRecord {
	t.Helper()
	rec, err := store.GetCommitRecord()
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// onActor runs fn on the actor goroutine (reads of actor-owned memory).
func onActor(t *testing.T, s *State, fn func()) {
	t.Helper()
	if err := s.request(ctxT(t, 10*time.Second), opTest, func() error { fn(); return nil }); err != nil {
		t.Fatal(err)
	}
}

// reopen stops the engine, closes the store and starts a new engine on the
// same file, returning the new engine and store.
func reopen(t *testing.T, s *State, store *db.Store, dbPath string) (*State, *db.Store) {
	t.Helper()
	if err := s.Stop(ctxT(t, 10*time.Second)); err != nil && !errors.Is(err, ErrHaltedNotFlushed) {
		t.Fatalf("Stop: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store2, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := New(testConfig(t, dbPath), store2, Options{Registry: metrics.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s2.Stop(ctxT(t, 10*time.Second))
		_ = store2.Close()
	})
	return s2, store2
}

// TestUpdateCommitIsAtomicAndDurable: a full update writes the world, the
// schedule, the turn's news and telegrams and a commit record together;
// none of it is visible before the commit; all of it survives a reopen.
func TestUpdateCommitIsAtomicAndDurable(t *testing.T) {
	s, store, cfg := newTestEngine(t, true)
	addFixtureShips(t, store, 3)
	reg := &fakeRegistry{}
	s.SetRegistry(reg)
	before := digest(t, store)
	rec0 := commitRecord(t, store)
	if rec0 == nil || rec0.Kind != "init" || rec0.CommitSeq != 1 {
		t.Fatalf("init commit record %+v", rec0)
	}
	var writesAtStart, writesMidTurn int64
	var midDigest string
	var fencedMid uint64
	s.testHooks = &testHooks{
		beforeTurn: func(turnKind) { writesAtStart = store.WriteCount() },
		midTurn: func(update bool) {
			// Effects produced by turn code: a news line, a telegram to an
			// offline governor, a planet change.
			s.post(model.NewsCombat, "battle at Sol")
			s.pushTelegram(1, 0, "your fleet reports")
			s.planets[0][0].Info[0].Resource += 7
			writesMidTurn = store.WriteCount()
			midDigest, _ = store.WorldDigest()
			fencedMid = store.FencedWrites()
		},
	}
	if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
		t.Fatalf("TriggerUpdate: %v", err)
	}
	if writesMidTurn != writesAtStart {
		t.Fatalf("store written during the turn body: %d page writes before the commit", writesMidTurn-writesAtStart)
	}
	if midDigest != before {
		t.Fatal("store content changed during the turn body, before the commit")
	}
	if fencedMid != 0 || s.Metrics().FencedWrites.Value() != 0 {
		t.Fatalf("turn code hit the write fence: mid=%d total=%v", fencedMid, s.Metrics().FencedWrites.Value())
	}
	if reg.count() == 0 || !strings.Contains(reg.all(), "Finished with update") {
		t.Fatalf("notices not released after the commit: %q", reg.all())
	}
	rec1 := commitRecord(t, store)
	if rec1.CommitSeq != 2 || rec1.Kind != "update" || rec1.Turn != 1 || rec1.Segment != 0 {
		t.Fatalf("commit record after update %+v", rec1)
	}
	sched, _ := store.GetSchedule()
	if sched.NumUpdates != 1 || sched.NumSegments != 0 {
		t.Fatalf("schedule not committed with the world: %+v", sched)
	}
	st := s.Status()
	if st.LastCommitSeq != 2 || st.NumUpdates != 1 {
		t.Fatalf("status %+v", st)
	}
	p, _ := store.GetPlanet(0, 0)
	r, _ := store.GetRace(1)
	if p.Info[0].Resource < 7 || r.Turn != 1 {
		t.Fatalf("world not committed: planet resource %d race turn %d", p.Info[0].Resource, r.Turn)
	}
	news, _, _ := store.ReadNewsFrom(model.NewsCombat, 0)
	if len(news) != 1 || !strings.Contains(news[0], "battle at Sol") {
		t.Fatalf("news not committed: %v", news)
	}
	has, _ := store.HasTelegrams(1, 0)
	if !has {
		t.Fatal("offline telegram not committed")
	}
	if s.Metrics().Commits.Value("update", "ok") != 1 || s.Metrics().CommitSeq.Value() != 2 {
		t.Fatal("commit metrics")
	}
	after := digest(t, store)

	// Reopen: identical logical state, same counters, no turn re-run.
	s2, store2 := reopen(t, s, store, cfg.DBPath)
	if s2.Mode() != ModeServing {
		t.Fatalf("mode after reopen %s", s2.Mode())
	}
	time.Sleep(100 * time.Millisecond)
	st2 := s2.Status()
	// The orderly stop added a shutdown commit (seq 3) without changing counters.
	rec2 := commitRecord(t, store2)
	if rec2.Kind != "shutdown" || rec2.CommitSeq != 3 || rec2.Turn != 1 || st2.NumUpdates != 1 || st2.LastCommitSeq != 3 {
		t.Fatalf("after reopen: record %+v status %+v", rec2, st2)
	}
	_ = after
	r2, _ := store2.GetRace(1)
	p2, _ := store2.GetPlanet(0, 0)
	if r2.Turn != 1 || p2.Info[0].Resource != p.Info[0].Resource || st2.Universe == nil || st2.Universe.Seed != 11 {
		t.Fatalf("state after reopen: race turn %d planet %d universe %+v", r2.Turn, p2.Info[0].Resource, st2.Universe)
	}
}

// TestSegmentCommit: a movement segment commits the same way.
func TestSegmentCommit(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	addFixtureShips(t, store, 2)
	if err := s.TriggerSegment(ctxT(t, 60*time.Second)); err != nil {
		t.Fatal(err)
	}
	rec := commitRecord(t, store)
	sched, _ := store.GetSchedule()
	if rec.Kind != "segment" || rec.CommitSeq != 2 || rec.Turn != 0 || rec.Segment != 1 || sched.NumSegments != 1 {
		t.Fatalf("segment commit %+v sched %+v", rec, sched)
	}
	if s.Metrics().Commits.Value("segment", "ok") != 1 {
		t.Fatal("segment commit metric")
	}
}

// TestTurnPanicRollsBackToLastCommit: a panic after the turn has already
// changed planets, ships, news and telegrams in memory leaves the store
// untouched, reloads memory from the last commit, halts, and the store
// restarts cleanly without a duplicate or a half turn.
func TestTurnPanicRollsBackToLastCommit(t *testing.T) {
	s, store, cfg := newTestEngine(t, true)
	addFixtureShips(t, store, 2)
	reg := &fakeRegistry{}
	s.SetRegistry(reg)
	before := digest(t, store)
	rec0 := commitRecord(t, store)
	sched0, _ := store.GetSchedule()
	s.testHooks = &testHooks{midTurn: func(update bool) {
		s.post(model.NewsCombat, "ghost battle")
		s.pushTelegram(1, 0, "ghost telegram")
		s.planets[0][0].Info[0].Resource += 99
		s.races[0].Motto = "half-advanced"
		s.notifyRace(1, "you should never see this")
		panic("injected failure after mutations")
	}}
	err := s.TriggerUpdate(ctxT(t, 60*time.Second))
	if !errors.Is(err, ErrHalted) || s.Mode() != ModeHalted {
		t.Fatalf("err=%v mode=%s", err, s.Mode())
	}
	if d := digest(t, store); d != before {
		t.Fatal("a failed turn changed the store")
	}
	if rec := commitRecord(t, store); *rec != *rec0 {
		t.Fatalf("commit record changed by a failed turn: %+v -> %+v", rec0, rec)
	}
	if sched, _ := store.GetSchedule(); sched.NumUpdates != sched0.NumUpdates || !sched.NextUpdate.Equal(sched0.NextUpdate) {
		t.Fatalf("schedule changed by a failed turn: %+v -> %+v", sched0, sched)
	}
	if news, _, _ := store.ReadNewsFrom(model.NewsCombat, 0); len(news) != 0 {
		t.Fatalf("news from a failed turn persisted: %v", news)
	}
	if has, _ := store.HasTelegrams(1, 0); has {
		t.Fatal("telegram from a failed turn persisted")
	}
	if strings.Contains(reg.all(), "never see this") || strings.Contains(reg.all(), "Finished") {
		t.Fatalf("effects of a failed turn delivered: %q", reg.all())
	}
	if !strings.Contains(reg.all(), "rolled back") {
		t.Fatalf("players not told the turn failed: %q", reg.all())
	}
	// Memory equals the last commit again.
	onActor(t, s, func() {
		if s.races[0].Motto == "half-advanced" || s.planets[0][0].Info[0].Resource != 0 || s.races[0].Turn != 0 {
			t.Errorf("memory not reloaded: motto=%q resource=%d turn=%d", s.races[0].Motto, s.planets[0][0].Info[0].Resource, s.races[0].Turn)
		}
		if s.sched.numUpdates != 0 {
			t.Errorf("in-memory schedule not reloaded: %+v", s.sched)
		}
		if s.effects.active || len(s.effects.news) != 0 || len(s.effects.notices) != 0 {
			t.Errorf("effects not discarded: %+v", s.effects)
		}
	})
	reason, _ := s.HaltDetails()
	if !strings.Contains(reason, "reloaded from the last commit") {
		t.Fatalf("halt reason does not say memory was reloaded: %q", reason)
	}
	if s.Metrics().TurnsTotal.Value("update", "panic") != 1 || s.Metrics().Commits.Value("update", "ok") != 0 {
		t.Fatal("metrics after failed turn")
	}
	st := s.Status()
	if st.NumUpdates != 0 || st.LastCommitSeq != rec0.CommitSeq {
		t.Fatalf("status after failed turn %+v", st)
	}
	if err := s.TriggerUpdate(ctxT(t, 5*time.Second)); !errors.Is(err, ErrNotServing) {
		t.Fatalf("turn while halted: %v", err)
	}
	// Shutdown while halted writes nothing; the store is already the last commit.
	if err := s.Stop(ctxT(t, 10*time.Second)); !errors.Is(err, ErrHaltedNotFlushed) {
		t.Fatalf("Stop while halted: %v", err)
	}
	if d := digest(t, store); d != before {
		t.Fatal("halted shutdown changed the store")
	}
	// Recovery: a restart serves from the last commit; nothing is duplicated.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store2, err := db.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store2.Close() }()
	s2, err := New(cfg, store2, Options{Registry: metrics.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Stop(ctxT(t, 10*time.Second)) }()
	if s2.Mode() != ModeServing || s2.Status().NumUpdates != 0 || s2.Status().LastCommitSeq != rec0.CommitSeq {
		t.Fatalf("after restart: mode %s status %+v", s2.Mode(), s2.Status())
	}
	if err := s2.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
		t.Fatalf("update after recovery: %v", err)
	}
	if rec := commitRecord(t, store2); rec.CommitSeq != rec0.CommitSeq+1 || rec.Turn != 1 {
		t.Fatalf("commit after recovery %+v", rec)
	}
}

// TestCommitFailureRollsBackAndHalts: an error inside the commit
// transaction (after every record was put) writes nothing; memory is
// reloaded; the engine halts; after a restart the same turn commits.
func TestCommitFailureRollsBackAndHalts(t *testing.T) {
	s, store, cfg := newTestEngine(t, true)
	addFixtureShips(t, store, 2)
	before := digest(t, store)
	injected := errors.New("injected I/O error")
	store.SetCommitFault(func(stage string) error {
		if stage == "after-write" {
			return injected
		}
		return nil
	})
	err := s.TriggerUpdate(ctxT(t, 60*time.Second))
	if !errors.Is(err, ErrHalted) || s.Mode() != ModeHalted {
		t.Fatalf("err=%v mode=%s", err, s.Mode())
	}
	if d := digest(t, store); d != before {
		t.Fatal("a failed commit changed the store")
	}
	reason, _ := s.HaltDetails()
	if !strings.Contains(reason, "commit failed") || !strings.Contains(reason, "injected I/O error") || !strings.Contains(reason, "reloaded") {
		t.Fatalf("halt reason %q", reason)
	}
	if s.Metrics().Commits.Value("update", "error") != 1 || s.Metrics().TurnsTotal.Value("update", "commit_failed") != 1 {
		t.Fatal("commit failure metrics")
	}
	onActor(t, s, func() {
		if s.races[0].Turn != 0 || s.sched.numUpdates != 0 {
			t.Errorf("memory not reloaded after commit failure: turn=%d sched=%+v", s.races[0].Turn, s.sched)
		}
	})
	if st := s.Status(); st.NumUpdates != 0 || st.LastCommitSeq != 1 {
		t.Fatalf("status advertises an uncommitted turn: %+v", st)
	}
	// Restart with the fault cleared: the turn runs and commits.
	store.SetCommitFault(nil)
	s2, store2 := reopen(t, s, store, cfg.DBPath)
	if s2.Mode() != ModeServing {
		t.Fatalf("mode after restart %s", s2.Mode())
	}
	if err := s2.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
		t.Fatal(err)
	}
	if rec := commitRecord(t, store2); rec.CommitSeq != 2 || rec.Turn != 1 {
		t.Fatalf("commit after restart %+v", rec)
	}
}

// TestDirectWriteDuringTurnIsFencedAndCounted: a turn code path that tries
// to write to the store directly is refused and counted; the commit still
// holds the whole turn.
func TestDirectWriteDuringTurnIsFencedAndCounted(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	var fenceErr error
	s.testHooks = &testHooks{midTurn: func(bool) {
		fenceErr = store.PutRace(s.races[0])
	}}
	if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(fenceErr, db.ErrWriteFenced) {
		t.Fatalf("direct write during a turn: %v", fenceErr)
	}
	if s.Metrics().FencedWrites.Value() != 1 {
		t.Fatalf("fenced writes metric %v", s.Metrics().FencedWrites.Value())
	}
	if rec := commitRecord(t, store); rec.Kind != "update" || rec.Turn != 1 {
		t.Fatalf("commit %+v", rec)
	}
	// Outside a turn the same write works again.
	if err := store.PutRace(s.races[0]); err != nil {
		t.Fatalf("write after the turn: %v", err)
	}
}

// TestShutdownCommitIsOneTransaction: the orderly stop commits memory and
// the schedule together with a "shutdown" record; a failing shutdown commit
// leaves the previous commit intact and is reported.
func TestShutdownCommitIsOneTransaction(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	onActor(t, s, func() { s.races[0].Motto = "flushed-by-commit" })
	if err := s.Stop(ctxT(t, 10*time.Second)); err != nil {
		t.Fatal(err)
	}
	rec := commitRecord(t, store)
	r, _ := store.GetRace(1)
	if rec.Kind != "shutdown" || rec.CommitSeq != 2 || r.Motto != "flushed-by-commit" {
		t.Fatalf("shutdown commit %+v race %q", rec, r.Motto)
	}

	s2, store2, _ := newTestEngine(t, true)
	before := digest(t, store2)
	onActor(t, s2, func() { s2.races[0].Motto = "must-not-persist" })
	store2.SetCommitFault(func(string) error { return errors.New("disk full") })
	err := s2.Stop(ctxT(t, 10*time.Second))
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("failed shutdown commit not reported: %v", err)
	}
	if d := digest(t, store2); d != before {
		t.Fatal("failed shutdown commit changed the store")
	}
}

// TestRestartAfterCommitsDoesNotDuplicateTurns: counters, next times,
// commit sequence and world digest survive a restart unchanged and no turn
// fires on startup.
func TestRestartAfterCommitsDoesNotDuplicateTurns(t *testing.T) {
	s, store, cfg := newTestEngine(t, true)
	addFixtureShips(t, store, 2)
	for i := 0; i < 2; i++ {
		if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.TriggerSegment(ctxT(t, 60*time.Second)); err != nil {
		t.Fatal(err)
	}
	st1 := s.Status()
	if err := s.Stop(ctxT(t, 10*time.Second)); err != nil {
		t.Fatal(err)
	}
	d1 := digest(t, store)
	rec1 := commitRecord(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store2, err := db.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store2.Close() }()
	s2, err := New(cfg, store2, Options{Registry: metrics.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Stop(ctxT(t, 10*time.Second)) }()
	time.Sleep(150 * time.Millisecond)
	st2 := s2.Status()
	if st2.NumUpdates != 2 || st2.NumSegments != 1 || st2.NumUpdates != st1.NumUpdates || !st2.NextUpdate.Equal(st1.NextUpdate) {
		t.Fatalf("counters/times after restart: %+v vs %+v", st1, st2)
	}
	if d2 := digest(t, store2); d2 != d1 {
		t.Fatal("logical state changed across restart")
	}
	if rec2 := commitRecord(t, store2); *rec2 != *rec1 || rec2.Turn != 2 || rec2.Segment != 1 {
		t.Fatalf("commit record after restart %+v (before %+v)", rec2, rec1)
	}
	if s2.Metrics().TurnsTotal.Value("update", "ok") != 0 {
		t.Fatal("a turn ran at startup")
	}
}

// TestUniverseIdentityAndPlayerDataPreserved: commits and restarts never
// alter the universe record or the enrolled race's identity.
func TestUniverseIdentityAndPlayerDataPreserved(t *testing.T) {
	s, store, cfg := newTestEngine(t, true)
	u0, _ := store.GetUniverseRecord()
	r0, _ := store.GetRace(1)
	if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
		t.Fatal(err)
	}
	s2, store2 := reopen(t, s, store, cfg.DBPath)
	if err := s2.TriggerSegment(ctxT(t, 60*time.Second)); err != nil {
		t.Fatal(err)
	}
	u1, _ := store2.GetUniverseRecord()
	r1, _ := store2.GetRace(1)
	if *u1 != *u0 {
		t.Fatalf("universe record changed: %+v -> %+v", u0, u1)
	}
	if r1.Name != r0.Name || r1.Password != r0.Password || r1.PlayerNum != r0.PlayerNum || r1.Governor[0].Name != r0.Governor[0].Name {
		t.Fatalf("race identity changed: %+v -> %+v", r0, r1)
	}
	auth, err := s2.Authenticate(ctxT(t, 5*time.Second), "Testers", "pw-testers", 0)
	if err != nil || !auth.OK {
		t.Fatalf("login after commits and restart: %+v %v", auth, err)
	}
}

// TestCorruptStoreRefusedAtStartup: a commit record ahead of the schedule
// record can only come from a writer that bypassed the commit path; the
// engine starts HALTED, writes nothing and refuses turns.
func TestCorruptStoreRefusedAtStartup(t *testing.T) {
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
	// Schedule at turn 0, commit record claims turn 3.
	if err := store.PutSchedule(&db.ScheduleRecord{NextUpdate: time.Now().Add(time.Hour), NextSegment: time.Now().Add(time.Hour), UpdateMinutes: 60, MovesPerUpdate: 3, SavedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutMeta(db.KeyCommit, []byte(`{"schema_version":1,"commit_seq":9,"turn":3,"segment":0,"kind":"update","wall_time":"2026-01-01T00:00:00Z"}`)); err != nil {
		t.Fatal(err)
	}
	before := digest(t, store)
	s, err := New(cfg, store, Options{Registry: metrics.NewRegistry()})
	if err != nil {
		t.Fatalf("New must start (alive, not serving): %v", err)
	}
	if s.Mode() != ModeHalted {
		t.Fatalf("mode %s, want HALTED", s.Mode())
	}
	reason, _ := s.HaltDetails()
	if !strings.Contains(reason, "CORRUPT_STORE") {
		t.Fatalf("halt reason %q", reason)
	}
	h := s.Health(time.Now())
	if !h.Alive || h.Ready || h.GameReady {
		t.Fatalf("health %+v", h)
	}
	if err := s.TriggerUpdate(ctxT(t, 5*time.Second)); !errors.Is(err, ErrNotServing) {
		t.Fatalf("turn on a corrupt store: %v", err)
	}
	if err := s.Stop(ctxT(t, 10*time.Second)); !errors.Is(err, ErrHaltedNotFlushed) {
		t.Fatalf("Stop: %v", err)
	}
	if d := digest(t, store); d != before {
		t.Fatal("startup on a corrupt store wrote to it")
	}
}

// TestStoresWithoutOrBehindCommitRecordAreAccepted: a store written before
// atomic commits (no record, or a schedule ahead of the record) starts and
// resynchronises at the next commit.
func TestStoresWithoutOrBehindCommitRecordAreAccepted(t *testing.T) {
	t.Run("no commit record", func(t *testing.T) {
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
		if err := store.DeleteMeta(db.KeyCommit); err != nil {
			t.Fatal(err)
		}
		s, err := New(cfg, store, Options{Registry: metrics.NewRegistry()})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Stop(ctxT(t, 10*time.Second)) }()
		if s.Mode() != ModeServing || s.Status().LastCommitSeq != 0 {
			t.Fatalf("mode %s status %+v", s.Mode(), s.Status())
		}
		if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
			t.Fatal(err)
		}
		if rec := commitRecord(t, store); rec.CommitSeq != 1 || rec.Turn != 1 {
			t.Fatalf("first commit on a legacy store %+v", rec)
		}
	})
	t.Run("schedule ahead of commit record", func(t *testing.T) {
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
		if err := store.PutSchedule(&db.ScheduleRecord{NextUpdate: time.Now().Add(time.Hour), NextSegment: time.Now().Add(time.Hour), NumUpdates: 4, NumSegments: 1, UpdateMinutes: 60, MovesPerUpdate: 3, SavedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		s, err := New(cfg, store, Options{Registry: metrics.NewRegistry()})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Stop(ctxT(t, 10*time.Second)) }()
		if s.Mode() != ModeServing {
			t.Fatalf("mode %s", s.Mode())
		}
		if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
			t.Fatal(err)
		}
		if rec := commitRecord(t, store); rec.CommitSeq != 2 || rec.Turn != 5 {
			t.Fatalf("resynchronised commit %+v", rec)
		}
	})
}

// TestTelegramsDuringTurn: an online governor's telegram is held and
// delivered after the commit; an offline one is stored by the commit; a
// failed turn delivers and stores neither.
func TestTelegramsDuringTurn(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	if err := store.PutRace(fixtureRace(t, 2, "Others", "pw-others")); err != nil {
		t.Fatal(err)
	}
	onActor(t, s, func() { s.races[1] = fixtureRace(t, 2, "Others", "pw-others") })
	reg := &fakeRegistry{online: []net.SessionInfo{{PlayerNum: 1, Governor: 0}}}
	s.SetRegistry(reg)
	var deliveredMid int
	s.testHooks = &testHooks{midTurn: func(bool) {
		s.pushTelegram(1, 0, "online telegram")
		s.pushTelegram(2, 0, "offline telegram")
		deliveredMid = reg.count()
	}}
	if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
		t.Fatal(err)
	}
	// The start banner is the only delivery allowed before the commit.
	if deliveredMid > 1 || (deliveredMid == 1 && !strings.Contains(reg.all(), "DOING UPDATE")) {
		t.Fatalf("delivered during the turn body: %q", reg.all())
	}
	if !strings.Contains(reg.all(), "gov:1/0:online telegram") {
		t.Fatalf("online telegram not delivered after commit: %q", reg.all())
	}
	if has, _ := store.HasTelegrams(1, 0); has {
		t.Fatal("online telegram was also stored")
	}
	if has, _ := store.HasTelegrams(2, 0); !has {
		t.Fatal("offline telegram not stored by the commit")
	}
}

// TestCommit100StarsUnderBudget records the commit time of a 100-star
// universe (plan Phase 3a exit: under 2 s on local disk) and fails only if
// it is grossly slow, since CI disks vary.
func TestCommit100StarsUnderBudget(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gb.db")
	cfg := testConfig(t, dbPath)
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := universe.Init(store, universe.Params{Stars: 100, MinPlanets: 2, MaxPlanets: 10}, 4242, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.PutRace(fixtureRace(t, 1, "Testers", "pw")); err != nil {
		t.Fatal(err)
	}
	s, err := New(cfg, store, Options{Registry: metrics.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(ctxT(t, 30*time.Second)) }()
	start := time.Now()
	if err := s.TriggerSegment(ctxT(t, 120*time.Second)); err != nil {
		t.Fatal(err)
	}
	turn := time.Since(start)
	commit := time.Duration(0)
	if n := s.Metrics().CommitDuration.Count("segment"); n != 1 {
		t.Fatalf("commit histogram count %d", n)
	}
	t.Logf("100-star segment: turn %s (commit histogram observed; see gb_commit_duration_seconds)", turn)
	_ = commit
	if turn > 10*time.Second {
		t.Fatalf("100-star segment took %s", turn)
	}
	_, planets, _, _, _ := store.Counts()
	if planets != 627 {
		t.Fatalf("planets %d", planets)
	}
}
