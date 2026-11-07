package game

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
	"github.com/phreddrick/GB/go/internal/platform/metrics"
	"github.com/phreddrick/GB/go/internal/universe"
)

// Regression tests for the independent review of CR-18 (CR18-R01 to R04).

// initStore returns an initialised store on a fresh path (no engine yet).
func initStore(t *testing.T) (*db.Store, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "gb.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := universe.Init(store, universe.Params{Stars: 2, MinPlanets: 1, MaxPlanets: 1}, 5, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.PutRace(fixtureRace(t, 1, "Testers", "pw-testers")); err != nil {
		t.Fatal(err)
	}
	return store, dbPath
}

func rawMeta(t *testing.T, store *db.Store, key []byte) []byte {
	t.Helper()
	raw, err := store.GetMeta(key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestStartupRejectsMalformedCommitMetadata (CR18-R01): every malformed
// commit or schedule record makes the engine start HALTED with a
// CORRUPT_STORE reason, refuse turns, write nothing (no fresh schedule is
// created to paper over it) and stop without flushing.
func TestStartupRejectsMalformedCommitMetadata(t *testing.T) {
	validSchedule := func(t *testing.T, store *db.Store) {
		t.Helper()
		if err := store.PutSchedule(&db.ScheduleRecord{NextUpdate: time.Now().Add(time.Hour), NextSegment: time.Now().Add(time.Hour), UpdateMinutes: 60, MovesPerUpdate: 3, SavedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(fields string) []byte {
		return []byte(`{"schema_version":1,"commit_seq":3,"turn":0,"segment":0,"kind":"update","wall_time":"2026-01-01T00:00:00Z"` + fields + `}`)
	}
	cases := []struct {
		name   string
		setup  func(t *testing.T, store *db.Store)
		expect string // substring of the halt reason
	}{
		{"json null", func(t *testing.T, s *db.Store) { validSchedule(t, s); _ = s.PutMeta(db.KeyCommit, []byte("null")) }, "JSON null"},
		{"empty object", func(t *testing.T, s *db.Store) { validSchedule(t, s); _ = s.PutMeta(db.KeyCommit, []byte("{}")) }, "schema_version 0"},
		{"missing fields", func(t *testing.T, s *db.Store) {
			validSchedule(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(`{"schema_version":1}`))
		}, "commit_seq is 0"},
		{"unknown field", func(t *testing.T, s *db.Store) {
			validSchedule(t, s)
			_ = s.PutMeta(db.KeyCommit, commit(`,"bogus":1`))
		}, "unknown field"},
		{"not json", func(t *testing.T, s *db.Store) { validSchedule(t, s); _ = s.PutMeta(db.KeyCommit, []byte("{garbage")) }, "corrupt commit record"},
		{"unsupported schema", func(t *testing.T, s *db.Store) {
			validSchedule(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(`{"schema_version":2,"commit_seq":3,"turn":0,"segment":0,"kind":"update","wall_time":"2026-01-01T00:00:00Z"}`))
		}, "schema_version 2"},
		{"unknown kind", func(t *testing.T, s *db.Store) {
			validSchedule(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(`{"schema_version":1,"commit_seq":3,"turn":0,"segment":0,"kind":"restore","wall_time":"2026-01-01T00:00:00Z"}`))
		}, `unknown kind "restore"`},
		{"zero sequence", func(t *testing.T, s *db.Store) {
			validSchedule(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(`{"schema_version":1,"commit_seq":0,"turn":0,"segment":0,"kind":"update","wall_time":"2026-01-01T00:00:00Z"}`))
		}, "commit_seq is 0"},
		{"sequence at overflow", func(t *testing.T, s *db.Store) {
			validSchedule(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(fmt.Sprintf(`{"schema_version":1,"commit_seq":%d,"turn":0,"segment":0,"kind":"update","wall_time":"2026-01-01T00:00:00Z"}`, uint64(math.MaxUint64))))
		}, "would overflow"},
		{"negative turn", func(t *testing.T, s *db.Store) {
			validSchedule(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(`{"schema_version":1,"commit_seq":3,"turn":-1,"segment":0,"kind":"update","wall_time":"2026-01-01T00:00:00Z"}`))
		}, "turn -1 is negative"},
		{"negative segment", func(t *testing.T, s *db.Store) {
			validSchedule(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(`{"schema_version":1,"commit_seq":3,"turn":0,"segment":-2,"kind":"segment","wall_time":"2026-01-01T00:00:00Z"}`))
		}, "segment -2 is negative"},
		{"init record with counters", func(t *testing.T, s *db.Store) {
			validSchedule(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(`{"schema_version":1,"commit_seq":1,"turn":2,"segment":0,"kind":"init","wall_time":"2026-01-01T00:00:00Z"}`))
		}, "init record has counters"},
		{"update record with segment", func(t *testing.T, s *db.Store) {
			validSchedule(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(`{"schema_version":1,"commit_seq":3,"turn":0,"segment":1,"kind":"update","wall_time":"2026-01-01T00:00:00Z"}`))
		}, "update record has segment 1"},
		{"missing wall time", func(t *testing.T, s *db.Store) {
			validSchedule(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(`{"schema_version":1,"commit_seq":3,"turn":0,"segment":0,"kind":"update"}`))
		}, "wall_time is missing"},
		{"update record without schedule", func(t *testing.T, s *db.Store) {
			_ = s.PutMeta(db.KeyCommit, commit(""))
			_ = s.DeleteMeta(db.KeySchedule)
		}, "schedule record is missing"},
		{"commit ahead of schedule", func(t *testing.T, s *db.Store) {
			validSchedule(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(`{"schema_version":1,"commit_seq":9,"turn":3,"segment":0,"kind":"update","wall_time":"2026-01-01T00:00:00Z"}`))
		}, "written outside the commit path"},
		{"corrupt schedule json", func(t *testing.T, s *db.Store) { _ = s.PutMeta(db.KeySchedule, []byte("{nope")) }, "corrupt schedule record"},
		{"negative schedule counters", func(t *testing.T, s *db.Store) {
			_ = s.PutSchedule(&db.ScheduleRecord{NextUpdate: time.Now().Add(time.Hour), NextSegment: time.Now().Add(time.Hour), NumUpdates: -1, UpdateMinutes: 60, MovesPerUpdate: 3, SavedAt: time.Now()})
		}, "negative counters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, dbPath := initStore(t)
			cfg := testConfig(t, dbPath)
			tc.setup(t, store)
			before := digest(t, store)
			rawCommit := rawMeta(t, store, db.KeyCommit)
			rawSched := rawMeta(t, store, db.KeySchedule)
			s, err := New(cfg, store, Options{Registry: metrics.NewRegistry()})
			if err != nil {
				t.Fatalf("New must start alive and halted, got error: %v", err)
			}
			if s.Mode() != ModeHalted {
				t.Fatalf("mode %s, want HALTED", s.Mode())
			}
			reason, _ := s.HaltDetails()
			if !strings.HasPrefix(reason, "CORRUPT_STORE") || !strings.Contains(reason, tc.expect) {
				t.Fatalf("halt reason %q (want CORRUPT_STORE and %q)", reason, tc.expect)
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
			if string(rawMeta(t, store, db.KeyCommit)) != string(rawCommit) || string(rawMeta(t, store, db.KeySchedule)) != string(rawSched) {
				t.Fatal("startup rewrote the commit or schedule record of a corrupt store")
			}
		})
	}
}

// TestStartupAcceptsSupportedMetadata: the legacy and fresh-universe cases
// remain accepted: no commit record; an init record without a schedule; a
// schedule ahead of the commit record.
func TestStartupAcceptsSupportedMetadata(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, store *db.Store)
	}{
		{"no commit record", func(t *testing.T, s *db.Store) { _ = s.DeleteMeta(db.KeyCommit) }},
		{"init record, no schedule", func(t *testing.T, s *db.Store) { _ = s.DeleteMeta(db.KeySchedule) }},
		{"schedule ahead of commit", func(t *testing.T, s *db.Store) {
			_ = s.PutSchedule(&db.ScheduleRecord{NextUpdate: time.Now().Add(time.Hour), NextSegment: time.Now().Add(time.Hour), NumUpdates: 4, NumSegments: 1, UpdateMinutes: 60, MovesPerUpdate: 3, SavedAt: time.Now()})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, dbPath := initStore(t)
			cfg := testConfig(t, dbPath)
			tc.setup(t, store)
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
			rec := commitRecord(t, store)
			sched, _ := store.GetSchedule()
			if rec.Turn != sched.NumUpdates || rec.Segment != sched.NumSegments || rec.Kind != "update" {
				t.Fatalf("not resynchronised: %+v vs %+v", rec, sched)
			}
		})
	}
}

// TestPanicInsideCommitTransactionWritesNothing (CR18-R02, error or panic
// inside the transaction): bbolt rolls the transaction back; the engine
// classifies it by reading the record back, reloads and halts saying so.
func TestPanicInsideCommitTransactionWritesNothing(t *testing.T) {
	s, store, cfg := newTestEngine(t, true)
	before := digest(t, store)
	store.SetCommitFault(func(stage string) error {
		if stage == "after-write" {
			panic("injected panic inside the commit transaction")
		}
		return nil
	})
	err := s.TriggerUpdate(ctxT(t, 60*time.Second))
	if !errors.Is(err, ErrHalted) || s.Mode() != ModeHalted {
		t.Fatalf("err=%v mode=%s", err, s.Mode())
	}
	if d := digest(t, store); d != before {
		t.Fatal("a panic inside the transaction changed the store")
	}
	reason, _ := s.HaltDetails()
	if !strings.Contains(reason, "panic: injected panic") || !strings.Contains(reason, "nothing of the turn was written") || !strings.Contains(reason, "reloaded from the last commit") {
		t.Fatalf("halt reason %q", reason)
	}
	if s.Metrics().Commits.Value("update", "panic") != 1 || s.Metrics().TurnsTotal.Value("update", "commit_failed") != 1 {
		t.Fatal("metrics")
	}
	onActor(t, s, func() {
		if s.races[0].Turn != 0 || s.sched.numUpdates != 0 || s.effects.active || len(s.effects.notices) != 0 {
			t.Errorf("memory not reloaded or effects kept: turn=%d sched=%+v effects=%+v", s.races[0].Turn, s.sched, s.effects)
		}
	})
	store.SetCommitFault(nil)
	s2, store2 := reopen(t, s, store, cfg.DBPath)
	if err := s2.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
		t.Fatal(err)
	}
	if rec := commitRecord(t, store2); rec.CommitSeq != 2 || rec.Turn != 1 {
		t.Fatalf("commit after restart %+v", rec)
	}
}

// TestPanicAfterCommitIsNeverReportedAsRollback (CR18-R02, uncertain
// outcome): a panic between the transaction's commit and the return is
// classified by the durable record: the commit stands, the reason says the
// outcome is uncertain, nothing claims a rollback, memory is reloaded from
// the committed state, and a restart continues without a duplicate turn.
func TestPanicAfterCommitIsNeverReportedAsRollback(t *testing.T) {
	s, store, cfg := newTestEngine(t, true)
	reg := &fakeRegistry{}
	s.SetRegistry(reg)
	s.testHooks = &testHooks{afterCommit: func(string) { panic("injected panic after the transaction committed") }}
	err := s.TriggerUpdate(ctxT(t, 60*time.Second))
	if !errors.Is(err, ErrHalted) || s.Mode() != ModeHalted {
		t.Fatalf("err=%v mode=%s", err, s.Mode())
	}
	rec := commitRecord(t, store)
	if rec.CommitSeq != 2 || rec.Kind != "update" || rec.Turn != 1 {
		t.Fatalf("the commit should stand: %+v", rec)
	}
	reason, _ := s.HaltDetails()
	if !strings.Contains(reason, "became visible") || !strings.Contains(reason, "uncertain") || strings.Contains(reason, "rolled back") || strings.Contains(reason, "nothing of the turn was written") {
		t.Fatalf("halt reason %q", reason)
	}
	if strings.Contains(reg.all(), "rolled back") || strings.Contains(reg.all(), "Finished with update") {
		t.Fatalf("players told something false: %q", reg.all())
	}
	if !strings.Contains(reg.all(), "uncertain outcome") {
		t.Fatalf("players not told about the uncertain outcome: %q", reg.all())
	}
	if s.Metrics().Commits.Value("update", "uncertain") != 1 || s.Metrics().TurnsTotal.Value("update", "commit_uncertain") != 1 {
		t.Fatal("metrics")
	}
	st := s.Status()
	if st.LastCommitSeq != 2 || st.NumUpdates != 1 {
		t.Fatalf("status must reflect the visible store after reload: %+v", st)
	}
	onActor(t, s, func() {
		if s.races[0].Turn != 1 {
			t.Errorf("memory not reloaded from the committed state: race turn %d", s.races[0].Turn)
		}
	})
	s.testHooks = nil
	s2, store2 := reopen(t, s, store, cfg.DBPath)
	if st := s2.Status(); st.NumUpdates != 1 || st.LastCommitSeq != 2 {
		t.Fatalf("after restart %+v", st)
	}
	if err := s2.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
		t.Fatal(err)
	}
	if rec := commitRecord(t, store2); rec.CommitSeq != 3 || rec.Turn != 2 {
		t.Fatalf("no duplicate, sequence continues: %+v", rec)
	}
}

// panickingRegistry panics on the first delivery containing a marker.
type panickingRegistry struct {
	fakeRegistry
	marker string
}

func (p *panickingRegistry) NotifyRace(player int, text string) {
	if strings.Contains(text, p.marker) {
		panic("injected notification failure")
	}
	p.fakeRegistry.NotifyRace(player, text)
}

// TestNotificationPanicAfterCommitKeepsTheCommit (CR18-R02, successful
// commit followed by delivery failure): the commit stands, the halt reason
// says so, and nothing is reloaded or rolled back.
func TestNotificationPanicAfterCommitKeepsTheCommit(t *testing.T) {
	s, store, cfg := newTestEngine(t, true)
	reg := &panickingRegistry{marker: "Finished with update"}
	s.SetRegistry(reg)
	err := s.TriggerUpdate(ctxT(t, 60*time.Second))
	if !errors.Is(err, ErrHalted) || s.Mode() != ModeHalted {
		t.Fatalf("err=%v mode=%s", err, s.Mode())
	}
	rec := commitRecord(t, store)
	if rec.CommitSeq != 2 || rec.Turn != 1 {
		t.Fatalf("commit must stand: %+v", rec)
	}
	reason, _ := s.HaltDetails()
	if !strings.Contains(reason, "committed (commit_seq 2)") || !strings.Contains(reason, "notification delivery panicked") || !strings.Contains(reason, "nothing was rolled back") {
		t.Fatalf("halt reason %q", reason)
	}
	if s.Metrics().NoticePanics.Value() != 1 || s.Metrics().TurnsTotal.Value("update", "ok") != 1 || s.Metrics().Commits.Value("update", "ok") != 1 {
		t.Fatal("metrics")
	}
	onActor(t, s, func() {
		if s.races[0].Turn != 1 || s.sched.numUpdates != 1 {
			t.Errorf("memory changed after a committed turn: turn=%d sched=%+v", s.races[0].Turn, s.sched)
		}
	})
	s2, _ := reopen(t, s, store, cfg.DBPath)
	if st := s2.Status(); st.NumUpdates != 1 || st.LastCommitSeq != 2 {
		t.Fatalf("after restart %+v", st)
	}
}

// TestScheduledTurnFailureHaltsWithoutCrashingActor (CR18-R02): a turn
// fired by the schedule (no caller) that panics in its body is handled the
// same way and the actor goroutine survives.
func TestScheduledTurnFailureHaltsWithoutCrashingActor(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	before := digest(t, store)
	fired := make(chan struct{}, 1)
	s.testHooks = &testHooks{midTurn: func(bool) {
		select {
		case fired <- struct{}{}:
		default:
		}
		panic("injected failure in a scheduled turn")
	}}
	onActor(t, s, func() {
		s.sched.nextUpdate = s.now().Add(-time.Second)
		s.sched.nextSegment = s.now().Add(time.Hour)
	})
	select {
	case <-fired:
	case <-time.After(10 * time.Second):
		t.Fatal("scheduled turn did not fire")
	}
	deadline := time.Now().Add(10 * time.Second)
	for s.Mode() != ModeHalted && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Mode() != ModeHalted {
		t.Fatalf("mode %s", s.Mode())
	}
	// The actor still answers.
	if st := s.Status(); st.NumUpdates != 0 {
		t.Fatalf("status after failed scheduled turn %+v", st)
	}
	if d := digest(t, store); d != before {
		t.Fatal("failed scheduled turn changed the store")
	}
	if err := s.Stop(ctxT(t, 10*time.Second)); !errors.Is(err, ErrHaltedNotFlushed) {
		t.Fatalf("Stop: %v", err)
	}
}

// TestShutdownCommitPanicIsClassified (CR18-R02, shutdown persistence): a
// panic inside the shutdown commit transaction leaves the previous commit
// and is reported as failed; a panic after the transaction committed is
// reported as uncertain, never as a rollback.
func TestShutdownCommitPanicIsClassified(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	before := digest(t, store)
	store.SetCommitFault(func(stage string) error {
		if stage == "after-write" {
			panic("injected panic in the shutdown transaction")
		}
		return nil
	})
	err := s.Stop(ctxT(t, 10*time.Second))
	if !errors.Is(err, ErrShutdownCommitFailed) || !strings.Contains(err.Error(), "nothing of the turn was written") {
		t.Fatalf("Stop after a panic inside the transaction: %v", err)
	}
	if d := digest(t, store); d != before {
		t.Fatal("failed shutdown commit changed the store")
	}

	s2, store2, _ := newTestEngine(t, true)
	s2.testHooks = &testHooks{afterCommit: func(string) { panic("injected panic after the shutdown transaction committed") }}
	err = s2.Stop(ctxT(t, 10*time.Second))
	if !errors.Is(err, ErrShutdownCommitUncertain) || strings.Contains(err.Error(), "nothing of the turn was written") {
		t.Fatalf("Stop after a panic following the commit: %v", err)
	}
	if rec := commitRecord(t, store2); rec.Kind != "shutdown" || rec.CommitSeq != 2 {
		t.Fatalf("shutdown commit should stand: %+v", rec)
	}
}

// seedFreeShip stores ships 1..3 and frees ship 2, so the ship free list
// holds [2] and the next fresh number is 4.
func seedFreeShip(t *testing.T, store *db.Store) {
	t.Helper()
	addFixtureShips(t, store, 3)
	if err := store.DeleteShip(2); err != nil {
		t.Fatal(err)
	}
	free, _ := store.FreeShipNums()
	if len(free) != 1 || free[0] != 2 {
		t.Fatalf("free list %v", free)
	}
}

// newShip adds a ship allocated during a turn to the in-memory world.
func (s *State) newShipForTest(num uint32) {
	s.ships[num] = &model.Ship{Number: num, Owner: 1, Race: 1, Name: fmt.Sprintf("T%d", num), Type: model.StypeShuttle, Alive: true, WhatOrbits: model.LevelPlan}
}

// TestStagedAllocationRolledBackOnPanic (CR18-R04): numbers taken from the
// free list and beyond it during a turn that panics are not consumed and
// the new ships do not exist.
func TestStagedAllocationRolledBackOnPanic(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	seedFreeShip(t, store)
	before := digest(t, store)
	var got []uint32
	s.testHooks = &testHooks{midTurn: func(bool) {
		for i := 0; i < 3; i++ {
			n, err := s.allocShipNum()
			if err != nil {
				t.Errorf("allocShipNum: %v", err)
			}
			got = append(got, n)
			s.newShipForTest(n)
		}
		panic("injected failure after allocations")
	}}
	if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); !errors.Is(err, ErrHalted) {
		t.Fatalf("turn: %v", err)
	}
	if len(got) != 3 || got[0] != 2 || got[1] != 4 || got[2] != 5 {
		t.Fatalf("staged allocations %v, want [2 4 5] (free list first, then above the maximum)", got)
	}
	if d := digest(t, store); d != before {
		t.Fatal("failed turn changed the store (allocator state leaked)")
	}
	if free, _ := store.FreeShipNums(); len(free) != 1 || free[0] != 2 {
		t.Fatalf("free list consumed by a failed turn: %v", free)
	}
	for _, n := range got {
		if sh, _ := store.GetShip(n); sh != nil && n != 2 {
			t.Fatalf("ship %d from a failed turn persisted", n)
		}
	}
	onActor(t, s, func() {
		if _, ok := s.ships[4]; ok {
			t.Error("ship from a failed turn still in memory")
		}
	})
}

// TestStagedAllocationRolledBackOnCommitError: same, with the failure
// inside the commit transaction.
func TestStagedAllocationRolledBackOnCommitError(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	seedFreeShip(t, store)
	before := digest(t, store)
	store.SetCommitFault(func(stage string) error {
		if stage == "after-write" {
			return errors.New("injected commit error")
		}
		return nil
	})
	s.testHooks = &testHooks{midTurn: func(bool) {
		n, _ := s.allocShipNum()
		s.newShipForTest(n)
		n, _ = s.allocShipNum()
		s.newShipForTest(n)
	}}
	if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); !errors.Is(err, ErrHalted) {
		t.Fatalf("turn: %v", err)
	}
	if d := digest(t, store); d != before {
		t.Fatal("failed commit changed the store")
	}
	if free, _ := store.FreeShipNums(); len(free) != 1 || free[0] != 2 {
		t.Fatalf("free list consumed by a failed commit: %v", free)
	}
}

// TestStagedAllocationCommitsAndRestartContinues: several allocations in
// one turn are unique, the commit consumes exactly the popped free
// entries, and after a restart between-turn allocation continues above
// them without reuse.
func TestStagedAllocationCommitsAndRestartContinues(t *testing.T) {
	s, store, cfg := newTestEngine(t, true)
	seedFreeShip(t, store)
	var got []uint32
	s.testHooks = &testHooks{midTurn: func(bool) {
		seen := map[uint32]bool{}
		for i := 0; i < 3; i++ {
			n, err := s.allocShipNum()
			if err != nil || seen[n] {
				t.Errorf("allocation %d: n=%d err=%v seen=%v", i, n, err, seen[n])
			}
			seen[n] = true
			got = append(got, n)
			s.newShipForTest(n)
		}
	}}
	if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
		t.Fatal(err)
	}
	if free, _ := store.FreeShipNums(); len(free) != 0 {
		t.Fatalf("popped free entry still listed after the commit: %v", free)
	}
	for _, n := range got {
		if sh, _ := store.GetShip(n); sh == nil {
			t.Fatalf("ship %d not committed", n)
		}
	}
	if rec := commitRecord(t, store); rec.CommitSeq != 2 {
		t.Fatalf("commit %+v", rec)
	}
	s2, store2 := reopen(t, s, store, cfg.DBPath)
	n, err := store2.AllocShipNum() // between turns: the store's own allocator
	if err != nil || n != 6 {
		t.Fatalf("between-turn allocation after restart: n=%d err=%v (want 6, above 1..5)", n, err)
	}
	if err := s2.TriggerSegment(ctxT(t, 60*time.Second)); err != nil {
		t.Fatal(err)
	}
}

// TestStagedCommodityAllocation: the commodity allocator is staged the same
// way (free list first, then above the maximum) and committed atomically.
func TestStagedCommodityAllocation(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	for i := 1; i <= 2; i++ {
		if err := store.PutCommod(&model.Commodity{Number: uint32(i), Owner: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteCommod(1); err != nil {
		t.Fatal(err)
	}
	before := digest(t, store)
	var got []uint32
	s.testHooks = &testHooks{midTurn: func(bool) {
		for i := 0; i < 2; i++ {
			n, err := s.allocCommodNum()
			if err != nil {
				t.Errorf("allocCommodNum: %v", err)
			}
			got = append(got, n)
		}
		panic("injected failure after commodity allocations")
	}}
	if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); !errors.Is(err, ErrHalted) {
		t.Fatalf("turn: %v", err)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("commodity allocations %v, want [1 3]", got)
	}
	if d := digest(t, store); d != before {
		t.Fatal("failed turn changed the commodity free list")
	}
	if free, _ := store.FreeCommodNums(); len(free) != 1 || free[0] != 1 {
		t.Fatalf("commodity free list %v", free)
	}
}

// TestBetweenTurnAllocationUnchanged: outside a turn the allocators behave
// exactly as before (free list popped immediately in the store).
func TestBetweenTurnAllocationUnchanged(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	seedFreeShip(t, store)
	var n1, n2 uint32
	onActor(t, s, func() {
		n1, _ = s.allocShipNum()
		n2, _ = s.allocShipNum()
	})
	if n1 != 2 || n2 != 4 {
		t.Fatalf("between-turn allocations %d %d, want 2 4", n1, n2)
	}
	if free, _ := store.FreeShipNums(); len(free) != 0 {
		t.Fatalf("free list not consumed immediately between turns: %v", free)
	}
}

var _ = net.SessionInfo{}
