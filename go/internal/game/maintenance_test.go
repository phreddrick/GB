package game

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/platform/metrics"
	"github.com/phreddrick/GB/go/internal/universe"
)

// Durable maintenance mode and readiness (CR-43, ADR-0010). Every test here
// asserts the durable state of the store (records, digest), not only the
// in-memory mode.

type maintFixture struct {
	t      *testing.T
	dbPath string
	cfg    *config.Config
}

func newMaintFixture(t *testing.T) *maintFixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "gb.db")
	cfg := testConfig(t, dbPath)
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := universe.Init(store, universe.Params{Stars: 3, MinPlanets: 1, MaxPlanets: 1}, 4242, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return &maintFixture{t: t, dbPath: dbPath, cfg: cfg}
}

// open opens the store and starts an engine on it; stop() performs an
// orderly Stop and closes the store (like a SIGTERM), returning Stop's error.
func (f *maintFixture) open() (*State, *db.Store, func() error) {
	f.t.Helper()
	store, err := db.Open(f.dbPath)
	if err != nil {
		f.t.Fatal(err)
	}
	s, err := New(f.cfg, store, Options{Registry: metrics.NewRegistry()})
	if err != nil {
		_ = store.Close()
		f.t.Fatal(err)
	}
	var once sync.Once
	stop := func() error {
		var serr error
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			serr = s.Stop(ctx)
			_ = store.Close()
		})
		return serr
	}
	f.t.Cleanup(func() { _ = stop() })
	return s, store, stop
}

// inspect opens the store on its own (no engine) and returns the maintenance
// record, the schedule record, the commit record and the world digest.
func (f *maintFixture) inspect() (*db.MaintenanceRecord, *db.ScheduleRecord, *db.CommitRecord, string) {
	f.t.Helper()
	store, err := db.Open(f.dbPath)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	m, err := store.GetMaintenance()
	if err != nil && !errors.Is(err, db.ErrCorruptMaintenanceRecord) {
		f.t.Fatalf("maintenance record: %v", err)
	}
	sc, err := store.GetSchedule()
	if err != nil {
		f.t.Fatalf("schedule record: %v", err)
	}
	cr, err := store.GetCommitRecord()
	if err != nil {
		f.t.Fatalf("commit record: %v", err)
	}
	d, err := store.WorldDigest()
	if err != nil {
		f.t.Fatal(err)
	}
	return m, sc, cr, d
}

// TestQuiesceIsDurableAcrossRestart: a quiesce writes the record before the
// mode changes; an orderly restart (shutdown commit included) starts
// QUIESCED with the same counters and the schedule exactly as persisted,
// writes nothing at startup, keeps the process Ready and the game not
// ready, and refuses turns until resume.
func TestQuiesceIsDurableAcrossRestart(t *testing.T) {
	f := newMaintFixture(t)
	s1, store1, stop1 := f.open()
	if err := s1.TriggerUpdate(ctxT(t, 30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if m, _ := store1.GetMaintenance(); m != nil {
		t.Fatal("no maintenance record expected before quiesce")
	}
	if err := s1.Quiesce(ctxT(t, 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	m, err := store1.GetMaintenance()
	if err != nil || m == nil || !m.Quiesced || m.SchemaVersion != db.MaintenanceSchemaVersion || m.SetAt.IsZero() || m.Reason == "" {
		t.Fatalf("maintenance record after quiesce: %+v %v", m, err)
	}
	st1 := s1.Status()
	if st1.Mode != "QUIESCED" || st1.GameReady || st1.Maintenance == nil || !st1.Maintenance.Durable || st1.Maintenance.Error != "" {
		t.Fatalf("status after quiesce: %+v maint=%+v", st1, st1.Maintenance)
	}
	if h := s1.Health(time.Now()); !h.Ready || h.GameReady || !h.Maintenance || !h.MaintenanceDurable {
		t.Fatalf("health after quiesce: %+v", h)
	}
	if s1.Metrics().MaintenanceDurable.Value() != 1 {
		t.Fatal("gb_maintenance_durable must be 1")
	}
	if err := stop1(); err != nil {
		t.Fatalf("orderly stop while quiesced: %v", err)
	}
	mAfter, scAfter, crAfter, digestAfter := f.inspect()
	if mAfter == nil || !mAfter.SetAt.Equal(m.SetAt) {
		t.Fatalf("shutdown commit must keep the maintenance record: %+v", mAfter)
	}
	if crAfter == nil || crAfter.Kind != db.CommitKindShutdown || crAfter.Turn != st1.NumUpdates {
		t.Fatalf("shutdown commit record: %+v", crAfter)
	}

	s2, store2, stop2 := f.open()
	if s2.Mode() != ModeQuiesced {
		t.Fatalf("mode after restart %s, want QUIESCED", s2.Mode())
	}
	st2 := s2.Status()
	if st2.NumUpdates != st1.NumUpdates || st2.NumSegments != st1.NumSegments {
		t.Fatalf("counters changed across restart: %d/%d -> %d/%d", st1.NumUpdates, st1.NumSegments, st2.NumUpdates, st2.NumSegments)
	}
	if !st2.NextUpdate.Equal(scAfter.NextUpdate) || !st2.NextSegment.Equal(scAfter.NextSegment) {
		t.Fatalf("schedule must be adopted exactly as persisted: %v/%v vs %v/%v", st2.NextUpdate, st2.NextSegment, scAfter.NextUpdate, scAfter.NextSegment)
	}
	if st2.Maintenance == nil || !st2.Maintenance.Durable || st2.Maintenance.SetAt == nil || !st2.Maintenance.SetAt.Equal(m.SetAt) || st2.GameReady {
		t.Fatalf("maintenance status after restart: %+v", st2.Maintenance)
	}
	if h := s2.Health(time.Now()); !h.Ready || h.GameReady || !h.Alive || !h.MaintenanceDurable {
		t.Fatalf("health after quiesced restart: %+v", h)
	}
	if err := s2.TriggerUpdate(ctxT(t, 5*time.Second)); !errors.Is(err, ErrNotServing) {
		t.Fatalf("update while quiesced after restart: %v", err)
	}
	if s2.Metrics().MissedTurns.Value("update") != 0 {
		t.Fatal("a quiesced start applies no overdue policy and counts nothing")
	}
	// Startup wrote nothing: schedule record (including saved_at), commit
	// record and digest are unchanged.
	sc2, err := store2.GetSchedule()
	if err != nil || !sc2.SavedAt.Equal(scAfter.SavedAt) {
		t.Fatalf("quiesced startup must not rewrite the schedule: %+v vs %+v (%v)", sc2, scAfter, err)
	}
	cr2, _ := store2.GetCommitRecord()
	if !sameCommitRecord(cr2, crAfter) {
		t.Fatalf("quiesced startup must not commit: %+v vs %+v", cr2, crAfter)
	}
	if d2, _ := store2.WorldDigest(); d2 != digestAfter {
		t.Fatal("quiesced startup must not change the world")
	}
	if err := stop2(); err != nil {
		t.Fatal(err)
	}
	if m3, _, _, _ := f.inspect(); m3 == nil {
		t.Fatal("a second orderly stop while quiesced must keep the record")
	}
}

// TestQuiescedRestartRunsNoOverdueTurn: a store that carries the record and
// a schedule whose times are long past (the Stage 3 situation: 26 updates
// overdue) starts QUIESCED, runs nothing, writes nothing, and reports the
// past times as they are; Resume applies the no-catch-up policy, removes
// the record and persists the new schedule in one step.
func TestQuiescedRestartRunsNoOverdueTurn(t *testing.T) {
	f := newMaintFixture(t)
	store, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-13 * time.Hour).Truncate(time.Second)
	if err := store.PutSchedule(&db.ScheduleRecord{NextUpdate: past, NextSegment: past, NumUpdates: 17, NumSegments: 0, UpdateMinutes: f.cfg.DefaultUpdateMinutes, MovesPerUpdate: f.cfg.MovesPerUpdate, SavedAt: past}); err != nil {
		t.Fatal(err)
	}
	setAt := past.Add(time.Minute)
	if err := store.PutMaintenance(&db.MaintenanceRecord{SchemaVersion: 1, Quiesced: true, SetAt: setAt, Reason: "operator quiesce (admin API)", EngineVersion: "old"}); err != nil {
		t.Fatal(err)
	}
	digestBefore, _ := store.WorldDigest()
	_ = store.Close()

	s, store2, _ := f.open()
	if s.Mode() != ModeQuiesced {
		t.Fatalf("mode %s", s.Mode())
	}
	time.Sleep(300 * time.Millisecond) // the actor arms no timer outside SERVING; give it a chance to misbehave
	st := s.Status()
	if st.NumUpdates != 17 || st.NumSegments != 0 || !st.NextUpdate.Equal(past) {
		t.Fatalf("quiesced start must neither run nor reschedule: %+v", st)
	}
	if s.Metrics().MissedTurns.Value("update") != 0 {
		t.Fatal("no missed-turn accounting while quiesced")
	}
	sc, _ := store2.GetSchedule()
	if !sc.SavedAt.Equal(past) {
		t.Fatal("quiesced start must not write the schedule")
	}
	if d, _ := store2.WorldDigest(); d != digestBefore {
		t.Fatal("quiesced start must not write the world")
	}
	// Resume: overdue policy applied, record gone, schedule persisted, one write.
	before := time.Now()
	if err := s.Resume(ctxT(t, 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if s.Mode() != ModeServing {
		t.Fatalf("mode after resume %s", s.Mode())
	}
	if m, err := store2.GetMaintenance(); err != nil || m != nil {
		t.Fatalf("resume must remove the record durably: %+v %v", m, err)
	}
	sc, _ = store2.GetSchedule()
	st = s.Status()
	if !sc.NextUpdate.After(before) || !sc.NextUpdate.Equal(st.NextUpdate) || sc.NumUpdates != 17 || sc.NumSegments != 0 {
		t.Fatalf("resumed schedule not persisted with the no-catch-up policy: %+v vs %+v", sc, st)
	}
	if st.Maintenance != nil || !st.GameReady {
		t.Fatalf("status after resume: %+v", st)
	}
	if s.Metrics().MaintenanceDurable.Value() != 0 {
		t.Fatal("gb_maintenance_durable must be 0 after resume")
	}
}

// TestResumeThenRestartServes: after resume the record is gone, so an
// orderly restart serves (the pre-CR-43 behaviour), and a turn commits.
func TestResumeThenRestartServes(t *testing.T) {
	f := newMaintFixture(t)
	s1, store1, stop1 := f.open()
	if err := s1.Quiesce(ctxT(t, 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s1.Resume(ctxT(t, 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if m, _ := store1.GetMaintenance(); m != nil {
		t.Fatal("record must be gone after resume")
	}
	if err := stop1(); err != nil {
		t.Fatal(err)
	}
	if m, _, _, _ := f.inspect(); m != nil {
		t.Fatal("record must stay gone across the shutdown commit")
	}
	s2, store2, _ := f.open()
	if s2.Mode() != ModeServing {
		t.Fatalf("mode after restart %s, want SERVING", s2.Mode())
	}
	seqBefore, _ := store2.GetCommitRecord()
	if err := s2.TriggerUpdate(ctxT(t, 30*time.Second)); err != nil {
		t.Fatal(err)
	}
	seqAfter, _ := store2.GetCommitRecord()
	if seqAfter.CommitSeq != seqBefore.CommitSeq+1 || seqAfter.Kind != db.CommitKindUpdate {
		t.Fatalf("turn after resume+restart did not commit: %+v -> %+v", seqBefore, seqAfter)
	}
}

// TestQuiesceWithFailedMaintenanceWriteIsReportedNotSilent: when the record
// cannot be written the engine still stops turns, but the request fails
// with ErrMaintenanceNotDurable, status and health say the quiesce is not
// durable, a restart (documented) starts SERVING, and a repeated quiesce
// retries the write and becomes durable.
func TestQuiesceWithFailedMaintenanceWriteIsReportedNotSilent(t *testing.T) {
	f := newMaintFixture(t)
	s, store, stop := f.open()
	boom := errors.New("injected store failure")
	store.SetMetaFault(func(op string) error {
		if op == "maintenance.put" {
			return boom
		}
		return nil
	})
	err := s.Quiesce(ctxT(t, 5*time.Second))
	if !errors.Is(err, ErrMaintenanceNotDurable) || !errors.Is(err, boom) {
		t.Fatalf("quiesce with a failing store: %v", err)
	}
	if s.Mode() != ModeQuiesced {
		t.Fatalf("a failed record write must still quiesce in memory: %s", s.Mode())
	}
	if err := s.TriggerSegment(ctxT(t, 5*time.Second)); !errors.Is(err, ErrNotServing) {
		t.Fatalf("turns must be refused while quiesced in memory: %v", err)
	}
	st := s.Status()
	if st.Maintenance == nil || st.Maintenance.Durable || !strings.Contains(st.Maintenance.Error, "injected") {
		t.Fatalf("status must expose the non-durable quiesce: %+v", st.Maintenance)
	}
	if h := s.Health(time.Now()); !h.Ready || !h.Maintenance || h.MaintenanceDurable {
		t.Fatalf("health: %+v", h)
	}
	if m, _ := store.GetMaintenance(); m != nil {
		t.Fatal("no record may exist after a failed write")
	}
	if s.Metrics().MaintenanceDurable.Value() != 0 {
		t.Fatal("gb_maintenance_durable must be 0")
	}
	// Retry path: the store recovers, a second quiesce writes the record.
	store.SetMetaFault(nil)
	if err := s.Quiesce(ctxT(t, 5*time.Second)); err != nil {
		t.Fatalf("retry quiesce: %v", err)
	}
	if m, _ := store.GetMaintenance(); m == nil {
		t.Fatal("retry must write the record")
	}
	if st := s.Status(); st.Maintenance == nil || !st.Maintenance.Durable || st.Maintenance.Error != "" {
		t.Fatalf("status after retry: %+v", st.Maintenance)
	}
	if err := s.Quiesce(ctxT(t, 5*time.Second)); err != nil {
		t.Fatalf("durable double quiesce must be a no-op: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if m, _, _, _ := f.inspect(); m == nil {
		t.Fatal("record must survive the stop")
	}

	// The documented non-durable case: with the record absent, a restart
	// serves. Reproduce it on a copy of the situation (record removed by a
	// resume-like clear with the schedule kept) to make the behaviour explicit.
	store3, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	sc, _ := store3.GetSchedule()
	if err := store3.ClearMaintenanceWithSchedule(sc); err != nil {
		t.Fatal(err)
	}
	_ = store3.Close()
	s4, _, _ := f.open()
	if s4.Mode() != ModeServing {
		t.Fatalf("without a record a restart serves (backward-compatible default): %s", s4.Mode())
	}
}

// TestStoreWithoutMaintenanceRecordKeepsStartupBehaviour: stores written
// before CR-43, with or without a CR-18 commit record, start SERVING with
// the overdue policy applied, exactly as before.
func TestStoreWithoutMaintenanceRecordKeepsStartupBehaviour(t *testing.T) {
	for _, withCommitRecord := range []bool{true, false} {
		f := newMaintFixture(t)
		store, err := db.Open(f.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
		if err := store.PutSchedule(&db.ScheduleRecord{NextUpdate: past, NextSegment: past, NumUpdates: 5, NumSegments: 1, UpdateMinutes: f.cfg.DefaultUpdateMinutes, MovesPerUpdate: f.cfg.MovesPerUpdate, SavedAt: past}); err != nil {
			t.Fatal(err)
		}
		if !withCommitRecord {
			if err := store.DeleteMeta(db.KeyCommit); err != nil {
				t.Fatal(err)
			}
		}
		_ = store.Close()
		s, store2, _ := f.open()
		if s.Mode() != ModeServing {
			t.Fatalf("commitRecord=%v: mode %s", withCommitRecord, s.Mode())
		}
		st := s.Status()
		if st.NumUpdates != 5 || !st.NextUpdate.After(time.Now()) || st.Maintenance != nil || !st.GameReady {
			t.Fatalf("commitRecord=%v: status %+v", withCommitRecord, st)
		}
		if s.Metrics().MissedTurns.Value("update") < 1 {
			t.Fatal("overdue policy must still count the missed update")
		}
		if m, _ := store2.GetMaintenance(); m != nil {
			t.Fatal("no record may appear on its own")
		}
	}
}

// TestCorruptMaintenanceRecordHaltsWithoutWriting: a malformed record is a
// CORRUPT_STORE start: HALTED, alive, not Ready, nothing written, nothing
// repaired, quiesce and resume refused, and the shutdown does not flush.
func TestCorruptMaintenanceRecordHaltsWithoutWriting(t *testing.T) {
	cases := map[string]string{
		"garbage":       `{not json`,
		"unknown field": `{"schema_version":1,"quiesced":true,"set_at":"2026-11-08T19:50:12Z","reason":"x","mode":"SERVING"}`,
		"not quiesced":  `{"schema_version":1,"quiesced":false,"set_at":"2026-11-08T19:50:12Z","reason":"x"}`,
		"null":          `null`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			f := newMaintFixture(t)
			store, err := db.Open(f.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.PutMeta(db.KeyMaintenance, []byte(raw)); err != nil {
				t.Fatal(err)
			}
			digestBefore, _ := store.WorldDigest()
			scBefore, _ := store.GetSchedule()
			crBefore, _ := store.GetCommitRecord()
			_ = store.Close()

			s, store2, stop := f.open()
			if s.Mode() != ModeHalted {
				t.Fatalf("mode %s, want HALTED", s.Mode())
			}
			reason, _ := s.HaltDetails()
			if !strings.HasPrefix(reason, "CORRUPT_STORE: ") || !strings.Contains(reason, "maintenance record") {
				t.Fatalf("halt reason %q", reason)
			}
			h := s.Health(time.Now())
			if h.Ready || h.GameReady || !h.Alive || h.Maintenance {
				t.Fatalf("health %+v", h)
			}
			if st := s.Status(); st.Mode != "HALTED" || st.HaltReason != reason || st.Maintenance != nil {
				t.Fatalf("status %+v", st)
			}
			if err := s.Resume(ctxT(t, 5*time.Second)); !errors.Is(err, ErrHalted) {
				t.Fatalf("resume on a corrupt store: %v", err)
			}
			if err := s.Quiesce(ctxT(t, 5*time.Second)); !errors.Is(err, ErrHalted) {
				t.Fatalf("quiesce on a corrupt store: %v", err)
			}
			if err := s.TriggerUpdate(ctxT(t, 5*time.Second)); !errors.Is(err, ErrHalted) {
				t.Fatalf("update on a corrupt store: %v", err)
			}
			// Nothing written, nothing repaired.
			if rawAfter, _ := store2.GetMeta(db.KeyMaintenance); string(rawAfter) != raw {
				t.Fatalf("the malformed record must be left untouched: %q", rawAfter)
			}
			scAfter, _ := store2.GetSchedule()
			if (scBefore == nil) != (scAfter == nil) || (scBefore != nil && !scAfter.SavedAt.Equal(scBefore.SavedAt)) {
				t.Fatal("startup on a corrupt store must not write the schedule")
			}
			if crAfter, _ := store2.GetCommitRecord(); !sameCommitRecord(crAfter, crBefore) {
				t.Fatal("startup on a corrupt store must not commit")
			}
			if d, _ := store2.WorldDigest(); d != digestBefore {
				t.Fatal("startup on a corrupt store must not change the world")
			}
			if err := stop(); !errors.Is(err, ErrHaltedNotFlushed) {
				t.Fatalf("stop while halted: %v", err)
			}
			if _, _, _, d := f.inspect(); d != digestBefore {
				t.Fatal("halted shutdown must not flush")
			}
		})
	}
}

// TestResumeWriteFailureHaltsAndKeepsRecord: if the resume transaction
// fails the engine halts (GB1-03), the record and the old schedule stay in
// the store, and the next start is QUIESCED, never SERVING.
func TestResumeWriteFailureHaltsAndKeepsRecord(t *testing.T) {
	f := newMaintFixture(t)
	s, store, stop := f.open()
	if err := s.Quiesce(ctxT(t, 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	scBefore, _ := store.GetSchedule()
	boom := errors.New("injected resume failure")
	store.SetMetaFault(func(op string) error {
		if op == "maintenance.clear" {
			return boom
		}
		return nil
	})
	err := s.Resume(ctxT(t, 5*time.Second))
	if err == nil || !errors.Is(err, boom) || !strings.Contains(err.Error(), "HALTED") {
		t.Fatalf("resume with a failing store: %v", err)
	}
	if s.Mode() != ModeHalted {
		t.Fatalf("mode %s, want HALTED", s.Mode())
	}
	if h := s.Health(time.Now()); h.Ready || h.GameReady {
		t.Fatalf("halted health %+v", h)
	}
	if m, _ := store.GetMaintenance(); m == nil {
		t.Fatal("the record must still be in the store after a failed resume")
	}
	if sc, _ := store.GetSchedule(); !sc.SavedAt.Equal(scBefore.SavedAt) {
		t.Fatal("a failed resume must not write the schedule")
	}
	if err := stop(); !errors.Is(err, ErrHaltedNotFlushed) {
		t.Fatalf("stop: %v", err)
	}
	s2, _, _ := f.open()
	if s2.Mode() != ModeQuiesced {
		t.Fatalf("after a failed resume the next start must be QUIESCED, got %s", s2.Mode())
	}
}

// TestQuiesceDuringTurnIsAppliedAfterTheCommit (CR-18 x CR-43): a quiesce
// requested while a turn runs waits for the turn (admin requests are
// serialized on the actor), the turn commits normally, the maintenance
// write happens after the fence is lowered and is never refused, and the
// record is in the store when the request returns.
func TestQuiesceDuringTurnIsAppliedAfterTheCommit(t *testing.T) {
	f := newMaintFixture(t)
	store, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(f.cfg, store, Options{Registry: metrics.NewRegistry(), TestTurnDelay: 800 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop(ctxT(t, 20*time.Second)); _ = store.Close() })
	seqBefore, _ := store.GetCommitRecord()
	turnErr := make(chan error, 1)
	go func() { turnErr <- s.TriggerUpdate(ctxT(t, 30*time.Second)) }()
	deadline := time.Now().Add(5 * time.Second)
	for s.Mode() != ModeTurnInProgress && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Mode() != ModeTurnInProgress {
		t.Fatalf("turn did not start: %s", s.Mode())
	}
	if err := s.Quiesce(ctxT(t, 30*time.Second)); err != nil {
		t.Fatalf("quiesce during a turn: %v", err)
	}
	if err := <-turnErr; err != nil {
		t.Fatalf("turn: %v", err)
	}
	if s.Mode() != ModeQuiesced {
		t.Fatalf("mode %s", s.Mode())
	}
	seqAfter, _ := store.GetCommitRecord()
	if seqAfter.CommitSeq != seqBefore.CommitSeq+1 || seqAfter.Kind != db.CommitKindUpdate {
		t.Fatalf("the turn must have committed normally: %+v -> %+v", seqBefore, seqAfter)
	}
	if m, _ := store.GetMaintenance(); m == nil {
		t.Fatal("the record must be durable when quiesce returns")
	}
	if store.FencedWrites() != 0 {
		t.Fatalf("the maintenance write must never hit the fence: %d refused (%s)", store.FencedWrites(), store.LastFencedWrite())
	}
	if st := s.Status(); st.NumUpdates != seqAfter.Turn || st.Maintenance == nil || !st.Maintenance.Durable {
		t.Fatalf("status %+v", st)
	}
}
