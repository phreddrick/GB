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
		{"empty object", func(t *testing.T, s *db.Store) { validSchedule(t, s); _ = s.PutMeta(db.KeyCommit, []byte("{}")) }, `missing field "schema_version"`},
		{"missing fields", func(t *testing.T, s *db.Store) {
			validSchedule(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(`{"schema_version":1}`))
		}, `missing field "commit_seq"`},
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
		}, `missing field "wall_time"`},
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

// ---- Second independent review (R01, R02, R04 corrections) ----

// TestStartupRejectsMetadataReproducedByReview (CR18-R01, second review):
// the four independently reproduced states plus the schedule variants.
func TestStartupRejectsMetadataReproducedByReview(t *testing.T) {
	sched := func(t *testing.T, store *db.Store) {
		t.Helper()
		if err := store.PutSchedule(&db.ScheduleRecord{NextUpdate: time.Now().Add(time.Hour), NextSegment: time.Now().Add(time.Hour), UpdateMinutes: 60, MovesPerUpdate: 3, SavedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	validCommit := `{"schema_version":1,"commit_seq":1,"turn":0,"segment":0,"kind":"init","wall_time":"2026-01-01T00:00:00Z"}`
	validSchedule := `{"next_update":"2026-01-01T01:00:00Z","next_segment":"2026-01-01T00:20:00Z","num_updates":0,"num_segments":0,"update_minutes":60,"moves_per_update":3,"saved_at":"2026-01-01T00:00:00Z"}`
	cases := []struct {
		name   string
		setup  func(t *testing.T, store *db.Store)
		expect string
	}{
		{"commit missing turn and segment", func(t *testing.T, s *db.Store) {
			sched(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(`{"schema_version":1,"commit_seq":1,"kind":"init","wall_time":"2026-01-01T00:00:00Z"}`))
		}, `missing field "turn"`},
		{"commit missing segment only", func(t *testing.T, s *db.Store) {
			sched(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(`{"schema_version":1,"commit_seq":1,"turn":0,"kind":"init","wall_time":"2026-01-01T00:00:00Z"}`))
		}, `missing field "segment"`},
		{"commit followed by bracket", func(t *testing.T, s *db.Store) { sched(t, s); _ = s.PutMeta(db.KeyCommit, []byte(validCommit+"]")) }, "trailing data"},
		{"commit followed by second object", func(t *testing.T, s *db.Store) {
			sched(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(validCommit+" {}"))
		}, "trailing data"},
		{"commit field null", func(t *testing.T, s *db.Store) {
			sched(t, s)
			_ = s.PutMeta(db.KeyCommit, []byte(`{"schema_version":1,"commit_seq":1,"turn":null,"segment":0,"kind":"init","wall_time":"2026-01-01T00:00:00Z"}`))
		}, `field "turn" is JSON null`},
		{"commit is an array", func(t *testing.T, s *db.Store) { sched(t, s); _ = s.PutMeta(db.KeyCommit, []byte("[]")) }, "corrupt commit record"},
		{"schedule null", func(t *testing.T, s *db.Store) { _ = s.PutMeta(db.KeySchedule, []byte("null")) }, "corrupt schedule record: record is JSON null"},
		{"schedule empty object", func(t *testing.T, s *db.Store) { _ = s.PutMeta(db.KeySchedule, []byte("{}")) }, `corrupt schedule record: missing field "next_update"`},
		{"schedule missing counters", func(t *testing.T, s *db.Store) {
			_ = s.PutMeta(db.KeySchedule, []byte(`{"next_update":"2026-01-01T01:00:00Z","next_segment":"2026-01-01T00:20:00Z","update_minutes":60,"moves_per_update":3,"saved_at":"2026-01-01T00:00:00Z"}`))
		}, `missing field "num_updates"`},
		{"schedule trailing data", func(t *testing.T, s *db.Store) { _ = s.PutMeta(db.KeySchedule, []byte(validSchedule+"]")) }, "trailing data"},
		{"schedule unknown field", func(t *testing.T, s *db.Store) {
			_ = s.PutMeta(db.KeySchedule, []byte(validSchedule[:len(validSchedule)-1]+`,"paused":true}`))
		}, `unknown field "paused"`},
		{"schedule zero update interval", func(t *testing.T, s *db.Store) {
			_ = s.PutSchedule(&db.ScheduleRecord{NextUpdate: time.Now().Add(time.Hour), NextSegment: time.Now().Add(time.Hour), UpdateMinutes: 0, MovesPerUpdate: 3, SavedAt: time.Now()})
		}, "update_minutes 0 is not positive"},
		{"schedule zero next times", func(t *testing.T, s *db.Store) {
			_ = s.PutSchedule(&db.ScheduleRecord{UpdateMinutes: 60, MovesPerUpdate: 3, SavedAt: time.Now()})
		}, "next_update or next_segment is zero"},
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
				t.Fatalf("mode %s, want HALTED (SERVING would accept corrupt metadata)", s.Mode())
			}
			reason, _ := s.HaltDetails()
			if !strings.HasPrefix(reason, "CORRUPT_STORE") || !strings.Contains(reason, tc.expect) {
				t.Fatalf("halt reason %q (want CORRUPT_STORE and %q)", reason, tc.expect)
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
				t.Fatal("startup replaced the commit or schedule record of a corrupt store")
			}
		})
	}
}

// TestValidMetadataStillStarts: representative valid records (including a
// record written by this binary and the legacy cases) start SERVING.
func TestValidMetadataStillStarts(t *testing.T) {
	store, dbPath := initStore(t)
	cfg := testConfig(t, dbPath)
	s, err := New(cfg, store, Options{Registry: metrics.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctxT(t, 10*time.Second)); err != nil {
		t.Fatal(err)
	}
	s2, err := New(cfg, store, Options{Registry: metrics.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Stop(ctxT(t, 10*time.Second)) }()
	if s2.Mode() != ModeServing || s2.Status().NumUpdates != 1 || s2.Status().LastCommitSeq != 3 {
		t.Fatalf("valid store refused or misread: mode %s status %+v", s2.Mode(), s2.Status())
	}
}

// TestCommitRecordDisappearedIsUncertainWithoutPanic (CR18-R02, second
// review): the record is removed after the transaction committed and the
// store then panics; classification must not dereference a nil record and
// must report uncertainty, deliver nothing and halt.
func TestCommitRecordDisappearedIsUncertainWithoutPanic(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	reg := &fakeRegistry{}
	s.SetRegistry(reg)
	s.testHooks = &testHooks{afterCommit: func(string) {
		if err := store.DeleteMeta(db.KeyCommit); err != nil {
			t.Errorf("delete record: %v", err)
		}
		panic("injected panic after the record was removed")
	}}
	err := s.TriggerUpdate(ctxT(t, 60*time.Second))
	if !errors.Is(err, ErrHalted) || s.Mode() != ModeHalted {
		t.Fatalf("err=%v mode=%s", err, s.Mode())
	}
	reason, _ := s.HaltDetails()
	if !strings.Contains(reason, "disappeared") || !strings.Contains(reason, "uncertain") || strings.Contains(reason, "nothing of the turn was written") || strings.Contains(reason, "orchestration") {
		t.Fatalf("halt reason %q", reason)
	}
	if strings.Contains(reg.all(), "Finished with update") || strings.Contains(reg.all(), "rolled back") {
		t.Fatalf("notifications delivered for an unconfirmed turn: %q", reg.all())
	}
	if s.Metrics().Commits.Value("update", "uncertain") != 1 || s.Metrics().TurnsTotal.Value("update", "commit_uncertain") != 1 {
		t.Fatal("metrics")
	}
	if s.Metrics().TurnsTotal.Value("update", "ok") != 0 {
		t.Fatal("turn reported ok")
	}
	if err := s.TriggerUpdate(ctxT(t, 5*time.Second)); !errors.Is(err, ErrNotServing) {
		t.Fatalf("gameplay resumed after an uncertain outcome: %v", err)
	}
}

// TestCommitRecordMalformedAfterCommitIsUncertainAndUntrusted: an
// unreadable record after the failure is uncertain, and because the reload
// cannot establish the commit either, memory is reported untrusted.
func TestCommitRecordMalformedAfterCommitIsUncertainAndUntrusted(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	reg := &fakeRegistry{}
	s.SetRegistry(reg)
	s.testHooks = &testHooks{afterCommit: func(string) {
		_ = store.PutMeta(db.KeyCommit, []byte("{garbage"))
		panic("injected panic after the record was corrupted")
	}}
	if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); !errors.Is(err, ErrHalted) {
		t.Fatalf("err=%v", err)
	}
	reason, _ := s.HaltDetails()
	if !strings.Contains(reason, "could not be read back") || !strings.Contains(reason, "uncertain") || !strings.Contains(reason, "untrusted") || strings.Contains(reason, "nothing of the turn was written") {
		t.Fatalf("halt reason %q", reason)
	}
	if strings.Contains(reg.all(), "Finished with update") {
		t.Fatalf("notifications delivered: %q", reg.all())
	}
	if err := s.Stop(ctxT(t, 10*time.Second)); !errors.Is(err, ErrHaltedNotFlushed) {
		t.Fatalf("Stop: %v", err)
	}
}

// TestCommitRecordAlteredWithSameSequenceIsNotUnchanged: a record rewritten
// with the previous sequence but different content must not be classified
// as "nothing written".
func TestCommitRecordAlteredWithSameSequenceIsNotUnchanged(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	s.testHooks = &testHooks{afterCommit: func(string) {
		// Same sequence as before the turn (1) but a different identity.
		_ = store.PutMeta(db.KeyCommit, []byte(`{"schema_version":1,"commit_seq":1,"turn":0,"segment":0,"kind":"shutdown","wall_time":"2026-02-02T00:00:00Z"}`))
		panic("injected panic after the record was altered")
	}}
	if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); !errors.Is(err, ErrHalted) {
		t.Fatalf("err=%v", err)
	}
	reason, _ := s.HaltDetails()
	if !strings.Contains(reason, "changed unexpectedly") || strings.Contains(reason, "nothing of the turn was written") {
		t.Fatalf("halt reason %q", reason)
	}
	if s.Metrics().Commits.Value("update", "uncertain") != 1 {
		t.Fatal("metrics")
	}
}

// TestReloadFailureAfterTurnFailureCannotResume: when the reload after a
// failed turn cannot establish the store's state, memory is untrusted,
// nothing is delivered and gameplay does not resume.
func TestReloadFailureAfterTurnFailureCannotResume(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	reg := &fakeRegistry{}
	s.SetRegistry(reg)
	s.testHooks = &testHooks{midTurn: func(bool) {
		s.store.SetWriteFence(false) // test-only: corrupt the schedule record under the turn
		_ = store.PutMeta(db.KeySchedule, []byte("{broken"))
		s.store.SetWriteFence(true)
		s.notifyRace(1, "must not be delivered")
		panic("injected failure before commit")
	}}
	if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); !errors.Is(err, ErrHalted) {
		t.Fatalf("err=%v", err)
	}
	reason, _ := s.HaltDetails()
	if !strings.Contains(reason, "untrusted") || !strings.Contains(reason, "corrupt schedule record") {
		t.Fatalf("halt reason %q", reason)
	}
	if strings.Contains(reg.all(), "must not be delivered") {
		t.Fatalf("buffered notice delivered after a failed turn: %q", reg.all())
	}
	if err := s.TriggerUpdate(ctxT(t, 5*time.Second)); !errors.Is(err, ErrNotServing) {
		t.Fatalf("gameplay resumed with untrusted memory: %v", err)
	}
	if err := s.Resume(ctxT(t, 5*time.Second)); !errors.Is(err, ErrHalted) {
		t.Fatalf("resume with untrusted memory: %v", err)
	}
	if err := s.Stop(ctxT(t, 10*time.Second)); !errors.Is(err, ErrHaltedNotFlushed) {
		t.Fatalf("Stop: %v", err)
	}
}

// allocScenario drives the staged allocators through the review's
// scenarios for one number space.
type allocScenario struct {
	name     string
	existing []uint32 // objects persisted before the turn
	freed    []uint32 // objects deleted before the turn, in deletion order (free list is LIFO)
	count    int      // allocations in the turn, none inserted
	want     []uint32 // expected numbers in order
}

var allocScenarios = []allocScenario{
	{"highest freed, two allocations before insertion", []uint32{1, 2}, []uint32{2}, 2, []uint32{2, 3}},
	{"multiple free entries consumed LIFO", []uint32{1, 2, 3, 4, 5}, []uint32{3, 5}, 3, []uint32{5, 3, 6}},
	{"free list exhausted then above the maximum", []uint32{1, 2, 3}, []uint32{2}, 4, []uint32{2, 4, 5, 6}},
	{"no free list", []uint32{1, 2, 3}, nil, 3, []uint32{4, 5, 6}},
	{"freed highest two then more", []uint32{1, 2, 3, 4}, []uint32{3, 4}, 4, []uint32{4, 3, 5, 6}},
}

func seedShips(t *testing.T, store *db.Store, sc allocScenario) {
	t.Helper()
	for _, n := range sc.existing {
		if err := store.PutShip(&model.Ship{Number: n, Owner: 1, Race: 1, Type: model.StypeShuttle, Alive: true, WhatOrbits: model.LevelPlan}); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range sc.freed {
		if err := store.DeleteShip(n); err != nil {
			t.Fatal(err)
		}
	}
}

func seedCommods(t *testing.T, store *db.Store, sc allocScenario) {
	t.Helper()
	for _, n := range sc.existing {
		if err := store.PutCommod(&model.Commodity{Number: n, Owner: 1}); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range sc.freed {
		if err := store.DeleteCommod(n); err != nil {
			t.Fatal(err)
		}
	}
}

func uniqueAll(ns []uint32) bool {
	seen := map[uint32]bool{}
	for _, n := range ns {
		if seen[n] {
			return false
		}
		seen[n] = true
	}
	return true
}

// TestStagedAllocationUniqueWithoutInsertion (CR18-R04, second review):
// both allocators issue distinct numbers in LIFO free-list order even when
// nothing is inserted between allocations; a failed turn leaves the free
// list intact; a committed turn consumes exactly the popped entries and a
// restart continues above everything issued.
func TestStagedAllocationUniqueWithoutInsertion(t *testing.T) {
	for _, space := range []string{"ship", "commodity"} {
		for _, sc := range allocScenarios {
			for _, outcome := range []string{"panic", "commit"} {
				t.Run(space+"/"+sc.name+"/"+outcome, func(t *testing.T) {
					s, store, cfg := newTestEngine(t, true)
					if space == "ship" {
						seedShips(t, store, sc)
					} else {
						seedCommods(t, store, sc)
					}
					freeBefore := freeList(t, store, space)
					before := digest(t, store)
					var got []uint32
					s.testHooks = &testHooks{midTurn: func(bool) {
						for i := 0; i < sc.count; i++ {
							var n uint32
							var err error
							if space == "ship" {
								n, err = s.allocShipNum()
							} else {
								n, err = s.allocCommodNum()
							}
							if err != nil {
								t.Errorf("allocation %d: %v", i, err)
							}
							got = append(got, n) // deliberately NOT inserted
						}
						if outcome == "panic" {
							panic("injected failure after allocations")
						}
					}}
					err := s.TriggerUpdate(ctxT(t, 60*time.Second))
					if fmt.Sprint(got) != fmt.Sprint(sc.want) || !uniqueAll(got) {
						t.Fatalf("allocations %v, want %v (unique, LIFO reuse first)", got, sc.want)
					}
					if outcome == "panic" {
						if !errors.Is(err, ErrHalted) {
							t.Fatalf("turn: %v", err)
						}
						if d := digest(t, store); d != before {
							t.Fatal("failed turn changed the store")
						}
						if fl := freeList(t, store, space); fmt.Sprint(fl) != fmt.Sprint(freeBefore) {
							t.Fatalf("free list changed by a failed turn: %v -> %v", freeBefore, fl)
						}
						return
					}
					if err != nil {
						t.Fatalf("turn: %v", err)
					}
					// The commit consumed exactly the popped entries (the first
					// min(count, len(freed)) allocations) and nothing else.
					popped := len(sc.freed)
					if sc.count < popped {
						popped = sc.count
					}
					if fl := freeList(t, store, space); len(fl) != len(freeBefore)-popped {
						t.Fatalf("free list after commit %v (before %v, popped %d)", fl, freeBefore, popped)
					}
					// After a restart the store allocator continues above
					// every number the store knows; numbers issued but never
					// inserted were not persisted, so they may be reissued,
					// but never a duplicate of a persisted object.
					s2, store2 := reopen(t, s, store, cfg.DBPath)
					n, err := storeAlloc(store2, space)
					if err != nil {
						t.Fatal(err)
					}
					for _, e := range sc.existing {
						if n == e && !contains(sc.freed, e) {
							t.Fatalf("restart allocation %d duplicates a persisted object", n)
						}
					}
					if err := s2.TriggerSegment(ctxT(t, 60*time.Second)); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func freeList(t *testing.T, store *db.Store, space string) []uint32 {
	t.Helper()
	var fl []uint32
	var err error
	if space == "ship" {
		fl, err = store.FreeShipNums()
	} else {
		fl, err = store.FreeCommodNums()
	}
	if err != nil {
		t.Fatal(err)
	}
	return fl
}

func storeAlloc(store *db.Store, space string) (uint32, error) {
	if space == "ship" {
		return store.AllocShipNum()
	}
	return store.AllocCommodNum()
}

func contains(ns []uint32, n uint32) bool {
	for _, x := range ns {
		if x == n {
			return true
		}
	}
	return false
}

// TestStagedAllocationWithInsertionAndCommitPersistsAll: numbers issued and
// inserted in one turn (free-list reuse plus fresh numbers) all persist,
// the free list is consumed, and the restarted store allocates above them.
func TestStagedAllocationWithInsertionAndCommitPersistsAll(t *testing.T) {
	s, store, cfg := newTestEngine(t, true)
	seedShips(t, store, allocScenario{existing: []uint32{1, 2, 3, 4}, freed: []uint32{2, 4}})
	var got []uint32
	s.testHooks = &testHooks{midTurn: func(bool) {
		for i := 0; i < 4; i++ {
			n, err := s.allocShipNum()
			if err != nil {
				t.Errorf("alloc: %v", err)
			}
			got = append(got, n)
			s.newShipForTest(n)
		}
	}}
	if err := s.TriggerUpdate(ctxT(t, 60*time.Second)); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "[4 2 5 6]" {
		t.Fatalf("allocations %v, want [4 2 5 6]", got)
	}
	for _, n := range got {
		if sh, _ := store.GetShip(n); sh == nil {
			t.Fatalf("ship %d not committed", n)
		}
	}
	if fl, _ := store.FreeShipNums(); len(fl) != 0 {
		t.Fatalf("free list after commit %v", fl)
	}
	_, store2 := reopen(t, s, store, cfg.DBPath)
	if n, _ := store2.AllocShipNum(); n != 7 {
		t.Fatalf("restart allocation %d, want 7", n)
	}
}
