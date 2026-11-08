package game

import (
	"bytes"
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
	return f.openWithClock(nil)
}

// openWithClock is open with an engine clock (nil = time.Now).
func (f *maintFixture) openWithClock(now func() time.Time) (*State, *db.Store, func() error) {
	f.t.Helper()
	store, err := db.Open(f.dbPath)
	if err != nil {
		f.t.Fatal(err)
	}
	s, err := New(f.cfg, store, Options{Registry: metrics.NewRegistry(), Now: now})
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
	if err := store.PutSchedule(&db.ScheduleRecord{NextUpdate: past, NextSegment: past, NumUpdates: 17, NumSegments: 2, UpdateMinutes: f.cfg.DefaultUpdateMinutes, MovesPerUpdate: f.cfg.MovesPerUpdate, SavedAt: past}); err != nil {
		t.Fatal(err)
	}
	setAt := past.Add(time.Minute)
	if err := store.PutMaintenance(&db.MaintenanceRecord{SchemaVersion: 1, Quiesced: true, SetAt: setAt, Reason: "operator quiesce (admin API)", EngineVersion: "old"}); err != nil {
		t.Fatal(err)
	}
	digestBefore, _ := store.WorldDigest()
	_ = store.Close()

	// A fixed engine clock makes the resume's rescheduling exactly checkable.
	clock := time.Now().Truncate(time.Second)
	s, store2, _ := f.openWithClock(func() time.Time { return clock })
	if s.Mode() != ModeQuiesced {
		t.Fatalf("mode %s", s.Mode())
	}
	time.Sleep(300 * time.Millisecond) // the actor arms no timer outside SERVING; give it a chance to misbehave
	st := s.Status()
	if st.NumUpdates != 17 || st.NumSegments != 2 || !st.NextUpdate.Equal(past) || !st.NextSegment.Equal(past) {
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
	// Resume: overdue policy applied exactly (update at now+interval, segment
	// at now+segment interval, segment counter reset), record gone, schedule
	// persisted, all in one transaction.
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
	wantUpdate := clock.Add(time.Duration(f.cfg.DefaultUpdateMinutes) * time.Minute)
	wantSegment := clock.Add(time.Duration(f.cfg.DefaultUpdateMinutes/f.cfg.MovesPerUpdate) * time.Minute)
	if !sc.NextUpdate.Equal(wantUpdate) || !sc.NextSegment.Equal(wantSegment) || sc.NumUpdates != 17 || sc.NumSegments != 0 || !sc.SavedAt.Equal(clock) {
		t.Fatalf("resumed schedule not persisted with the no-catch-up policy: %+v (want update %v, segment %v, 17/0)", sc, wantUpdate, wantSegment)
	}
	if !st.NextUpdate.Equal(wantUpdate) || !st.NextSegment.Equal(wantSegment) || st.NumSegments != 0 {
		t.Fatalf("status after resume: %+v", st)
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
// write fails before anything is written the engine still stops turns, but
// the request fails with ErrMaintenanceNotDurable, status and health say the
// quiesce is not durable and what the store holds, a restart made directly
// from that state starts SERVING (documented), and a repeated quiesce
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
	if !errors.Is(err, ErrMaintenanceNotDurable) || !errors.Is(err, boom) || !strings.Contains(err.Error(), "holds no maintenance record") {
		t.Fatalf("quiesce with a failing store: %v", err)
	}
	if s.Mode() != ModeQuiesced {
		t.Fatalf("a failed record write must still quiesce in memory: %s", s.Mode())
	}
	if err := s.TriggerSegment(ctxT(t, 5*time.Second)); !errors.Is(err, ErrNotServing) {
		t.Fatalf("turns must be refused while quiesced in memory: %v", err)
	}
	st := s.Status()
	if st.Maintenance == nil || st.Maintenance.Durable || !strings.Contains(st.Maintenance.Error, "holds no maintenance record") || !strings.Contains(st.Maintenance.Error, "SERVING") {
		t.Fatalf("status must expose the non-durable quiesce and the predicted restart: %+v", st.Maintenance)
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
	// Restart directly from the failed-write state: SERVING, as reported.
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if m, _, _, _ := f.inspect(); m != nil {
		t.Fatal("the shutdown commit must not create a record")
	}
	s2, store2, stop2 := f.open()
	if s2.Mode() != ModeServing {
		t.Fatalf("restart after a failed (not written) quiesce must serve, got %s", s2.Mode())
	}
	// Retry path on the new engine: the store works, the quiesce becomes durable.
	if err := s2.Quiesce(ctxT(t, 5*time.Second)); err != nil {
		t.Fatalf("quiesce on the recovered store: %v", err)
	}
	if m, _ := store2.GetMaintenance(); m == nil {
		t.Fatal("the retry must write the record")
	}
	if st := s2.Status(); st.Maintenance == nil || !st.Maintenance.Durable || st.Maintenance.Error != "" {
		t.Fatalf("status after the durable quiesce: %+v", st.Maintenance)
	}
	if err := s2.Quiesce(ctxT(t, 5*time.Second)); err != nil {
		t.Fatalf("durable double quiesce must be a no-op: %v", err)
	}
	if err := stop2(); err != nil {
		t.Fatal(err)
	}
	s3, _, _ := f.open()
	if s3.Mode() != ModeQuiesced {
		t.Fatalf("after the durable quiesce the restart must be QUIESCED, got %s", s3.Mode())
	}
}

// TestQuiesceRetryInSameProcessConfirmsDurability: after a failed (not
// written) quiesce, a repeat quiesce in the same process writes the record
// and the claim becomes durable.
func TestQuiesceRetryInSameProcessConfirmsDurability(t *testing.T) {
	f := newMaintFixture(t)
	s, store, _ := f.open()
	boom := errors.New("injected store failure")
	store.SetMetaFault(func(op string) error {
		if op == "maintenance.put" {
			return boom
		}
		return nil
	})
	if err := s.Quiesce(ctxT(t, 5*time.Second)); !errors.Is(err, ErrMaintenanceNotDurable) {
		t.Fatalf("first quiesce: %v", err)
	}
	if st := s.Status(); st.Maintenance == nil || st.Maintenance.Durable {
		t.Fatalf("status after the failed write: %+v", st.Maintenance)
	}
	store.SetMetaFault(nil)
	if err := s.Quiesce(ctxT(t, 5*time.Second)); err != nil {
		t.Fatalf("retry in the same process: %v", err)
	}
	m, err := store.GetMaintenance()
	if err != nil || m == nil {
		t.Fatalf("the retry must write the record: %v %v", m, err)
	}
	if st := s.Status(); st.Maintenance == nil || !st.Maintenance.Durable || st.Maintenance.Error != "" || st.Maintenance.SetAt == nil || !st.Maintenance.SetAt.Equal(m.SetAt) {
		t.Fatalf("status after the retry: %+v", st.Maintenance)
	}
	if s.Metrics().MaintenanceDurable.Value() != 1 {
		t.Fatal("gb_maintenance_durable must be 1 after the retry")
	}
}

// TestMaintenanceClassifierBranches (review rounds 2 and 3): every read-back
// branch of both classifiers is exercised, asserting the message, the
// durability flag and the stored record pair; branches unreachable through
// the actor are exercised on the classifier directly.
func TestMaintenanceClassifierBranches(t *testing.T) {
	boom := errors.New("injected failure")
	faultOn := func(store *db.Store, ops ...string) {
		store.SetMetaFault(func(op string) error {
			for _, o := range ops {
				if op == o {
					return boom
				}
			}
			return nil
		})
	}

	// failReadsAfter returns a hook that fails the named write stage and,
	// from that moment on, every GetMeta read, so the before-snapshot is
	// taken normally and only the read-back fails.
	failReadsAfter := func(stage string) func(string) error {
		failing := false
		return func(op string) error {
			switch {
			case op == stage:
				failing = true
				return boom
			case op == "meta.get" && failing:
				return boom
			}
			return nil
		}
	}

	t.Run("quiesce read-back failure is reported as unknown", func(t *testing.T) {
		f := newMaintFixture(t)
		s, store, _ := f.open()
		store.SetMetaFault(failReadsAfter("maintenance.put"))
		err := s.Quiesce(ctxT(t, 5*time.Second))
		if !errors.Is(err, ErrMaintenanceNotDurable) || !strings.Contains(err.Error(), "could not be read back") || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("quiesce: %v", err)
		}
		if st := s.Status(); st.Maintenance == nil || st.Maintenance.Durable || !strings.Contains(st.Maintenance.Error, "unknown") {
			t.Fatalf("status: %+v", st.Maintenance)
		}
		store.SetMetaFault(nil)
		if m, err := store.GetMaintenance(); err != nil || m != nil {
			t.Fatalf("nothing was written in this scenario: %v %v", m, err)
		}
	})

	t.Run("unconfirmed visible record never earns a QUIESCED promise", func(t *testing.T) {
		f := newMaintFixture(t)
		s, store, stop := f.open()
		// 1. visible but reported failed: unconfirmed
		faultOn(store, "maintenance.put.committed")
		if err := s.Quiesce(ctxT(t, 5*time.Second)); !errors.Is(err, ErrMaintenanceNotDurable) || !strings.Contains(err.Error(), "likely") {
			t.Fatalf("first quiesce: %v", err)
		}
		// 2. a later quiesce fails before writing: the older record is in the
		// store but was never confirmed, so the text must not promise QUIESCED.
		faultOn(store, "maintenance.put")
		err := s.Quiesce(ctxT(t, 5*time.Second))
		if !errors.Is(err, ErrMaintenanceNotDurable) || !strings.Contains(err.Error(), "never confirmed durable") || !strings.Contains(err.Error(), "not guaranteed") {
			t.Fatalf("second quiesce: %v", err)
		}
		if st := s.Status(); st.Maintenance == nil || st.Maintenance.Durable || !strings.Contains(st.Maintenance.Error, "not guaranteed") {
			t.Fatalf("status: %+v", st.Maintenance)
		}
		if s.Metrics().MaintenanceDurable.Value() != 0 {
			t.Fatal("metric must stay 0")
		}
		// 3. a resume that fails before writing with that unconfirmed record:
		// the store is unchanged but QUIESCED is not guaranteed either.
		faultOn(store, "maintenance.clear")
		err = s.Resume(ctxT(t, 5*time.Second))
		if err == nil || !strings.Contains(err.Error(), "never confirmed durable") || !strings.Contains(err.Error(), "likely QUIESCED, not guaranteed") {
			t.Fatalf("resume: %v", err)
		}
		if s.Mode() != ModeHalted {
			t.Fatalf("mode %s", s.Mode())
		}
		if st := s.Status(); st.Maintenance == nil || st.Maintenance.Durable || !strings.Contains(st.Maintenance.Error, "not guaranteed") {
			t.Fatalf("status after the failed resume: %+v", st.Maintenance)
		}
		if m, _ := store.GetMaintenance(); m == nil {
			t.Fatal("the record is still visible in this scenario")
		}
		_ = stop()
	})

	t.Run("resume on an in-memory-only quiesce failing before the write says SERVING", func(t *testing.T) {
		f := newMaintFixture(t)
		s, store, _ := f.open()
		faultOn(store, "maintenance.put")
		if err := s.Quiesce(ctxT(t, 5*time.Second)); !errors.Is(err, ErrMaintenanceNotDurable) {
			t.Fatalf("quiesce: %v", err)
		}
		faultOn(store, "maintenance.clear")
		err := s.Resume(ctxT(t, 5*time.Second))
		if err == nil || !strings.Contains(err.Error(), "holds no maintenance record") || !strings.Contains(err.Error(), "SERVING") {
			t.Fatalf("resume: %v", err)
		}
		if st := s.Status(); st.Maintenance == nil || st.Maintenance.Durable || !strings.Contains(st.Maintenance.Error, "SERVING") {
			t.Fatalf("status: %+v", st.Maintenance)
		}
	})

	t.Run("resume read-back failure is reported as unknown", func(t *testing.T) {
		f := newMaintFixture(t)
		s, store, _ := f.open()
		if err := s.Quiesce(ctxT(t, 5*time.Second)); err != nil {
			t.Fatal(err)
		}
		recBefore, _ := store.GetMeta(db.KeyMaintenance)
		schedBefore, _ := store.GetMeta(db.KeySchedule)
		store.SetMetaFault(failReadsAfter("maintenance.clear"))
		err := s.Resume(ctxT(t, 5*time.Second))
		if err == nil || !strings.Contains(err.Error(), "could not be read back") {
			t.Fatalf("resume: %v", err)
		}
		if st := s.Status(); st.Maintenance == nil || st.Maintenance.Durable || !strings.Contains(st.Maintenance.Error, "unknown") {
			t.Fatalf("status: %+v", st.Maintenance)
		}
		if s.Metrics().MaintenanceDurable.Value() != 0 {
			t.Fatal("metric must be 0 when the outcome is unknown")
		}
		store.SetMetaFault(nil)
		recAfter, _ := store.GetMeta(db.KeyMaintenance)
		schedAfter, _ := store.GetMeta(db.KeySchedule)
		if !bytes.Equal(recAfter, recBefore) || !bytes.Equal(schedAfter, schedBefore) {
			t.Fatal("the store is unchanged in this scenario (the failure preceded the transaction)")
		}
	})

	t.Run("unexpected store state after a failed resume is reported as unpredictable", func(t *testing.T) {
		f := newMaintFixture(t)
		s, store, _ := f.open()
		if err := s.Quiesce(ctxT(t, 5*time.Second)); err != nil {
			t.Fatal(err)
		}
		// The failing transaction is preceded by something else removing the
		// record (not possible on the actor in production; this forces the
		// branch): the store then matches neither the before nor the intended state.
		store.SetMetaFault(func(op string) error {
			if op == "maintenance.clear" {
				store.SetMetaFault(nil)
				if err := store.DeleteMeta(db.KeyMaintenance); err != nil {
					return err
				}
				return boom
			}
			return nil
		})
		schedBefore, _ := store.GetMeta(db.KeySchedule)
		err := s.Resume(ctxT(t, 5*time.Second))
		if err == nil || !strings.Contains(err.Error(), "unexpected state") || !strings.Contains(err.Error(), "cannot be predicted") {
			t.Fatalf("resume: %v", err)
		}
		if st := s.Status(); st.Maintenance == nil || st.Maintenance.Durable || !strings.Contains(st.Maintenance.Error, "unexpected") {
			t.Fatalf("status: %+v", st.Maintenance)
		}
		recAfter, _ := store.GetMeta(db.KeyMaintenance)
		schedAfter, _ := store.GetMeta(db.KeySchedule)
		if recAfter != nil || !bytes.Equal(schedAfter, schedBefore) {
			t.Fatal("expected: record removed by the interfering writer, schedule unchanged")
		}
	})

	t.Run("visible resume after a visible-but-unconfirmed quiesce promises nothing", func(t *testing.T) {
		f := newMaintFixture(t)
		s, store, _ := f.open()
		faultOn(store, "maintenance.put.committed")
		if err := s.Quiesce(ctxT(t, 5*time.Second)); !errors.Is(err, ErrMaintenanceNotDurable) {
			t.Fatalf("quiesce: %v", err)
		}
		faultOn(store, "maintenance.clear.committed")
		err := s.Resume(ctxT(t, 5*time.Second))
		if err == nil || !strings.Contains(err.Error(), "never been confirmed") || !strings.Contains(err.Error(), "possibly QUIESCED, not guaranteed") || strings.Contains(err.Error(), "previously confirmed record") {
			t.Fatalf("resume: %v", err)
		}
		if st := s.Status(); st.Maintenance == nil || st.Maintenance.Durable || !strings.Contains(st.Maintenance.Error, "not guaranteed") {
			t.Fatalf("status: %+v", st.Maintenance)
		}
		store.SetMetaFault(nil)
		if m, _ := store.GetMaintenance(); m != nil {
			t.Fatal("the record is removed in this scenario")
		}
	})

	t.Run("visible resume of an in-memory-only quiesce says SERVING", func(t *testing.T) {
		f := newMaintFixture(t)
		s, store, _ := f.open()
		faultOn(store, "maintenance.put")
		if err := s.Quiesce(ctxT(t, 5*time.Second)); !errors.Is(err, ErrMaintenanceNotDurable) {
			t.Fatalf("quiesce: %v", err)
		}
		faultOn(store, "maintenance.clear.committed")
		err := s.Resume(ctxT(t, 5*time.Second))
		if err == nil || !strings.Contains(err.Error(), "holds no maintenance record") || !strings.Contains(err.Error(), "resumed schedule is visible") || !strings.Contains(err.Error(), "SERVING") {
			t.Fatalf("resume: %v", err)
		}
		store.SetMetaFault(nil)
		if m, _ := store.GetMaintenance(); m != nil {
			t.Fatal("no record in this scenario")
		}
	})

	t.Run("quiesce classifier: previous confirmed record, malformed record, foreign record", func(t *testing.T) {
		// These read-back states cannot be reached through Quiesce on the
		// actor (a confirmed record makes Quiesce a no-op; the store is never
		// written by anyone else), so the classifier is exercised directly.
		f := newMaintFixture(t)
		s, store, _ := f.open()
		now := time.Now().UTC()
		prev := &db.MaintenanceRecord{SchemaVersion: 1, Quiesced: true, SetAt: now.Add(-time.Hour), Reason: "earlier"}
		if err := store.PutMaintenance(prev); err != nil {
			t.Fatal(err)
		}
		prevRaw, _ := store.GetMeta(db.KeyMaintenance)
		intended, _ := db.EncodeMaintenanceRecord(&db.MaintenanceRecord{SchemaVersion: 1, Quiesced: true, SetAt: now, Reason: "now"})
		got := s.classifyMaintenanceWriteFailure(intended, storeSnapshot{maintenance: prevRaw, priorConfirmed: true})
		if !strings.Contains(got, "previously confirmed") || !strings.Contains(got, "would be QUIESCED") {
			t.Fatalf("confirmed previous record: %q", got)
		}
		got = s.classifyMaintenanceWriteFailure(intended, storeSnapshot{maintenance: prevRaw, priorConfirmed: false})
		if !strings.Contains(got, "never confirmed") || !strings.Contains(got, "not guaranteed") {
			t.Fatalf("unconfirmed previous record: %q", got)
		}
		// Bytes matching neither state: valid but foreign.
		got = s.classifyMaintenanceWriteFailure(intended, storeSnapshot{maintenance: []byte("{}"), priorConfirmed: true})
		if !strings.Contains(got, "matches neither") || strings.Contains(got, "QUIESCED from it") {
			t.Fatalf("foreign record: %q", got)
		}
		// Malformed bytes in the store: a restart would halt, and the text says so.
		if err := store.PutMeta(db.KeyMaintenance, []byte(`{"schema_version":1,"quiesced":false}`)); err != nil {
			t.Fatal(err)
		}
		badRaw, _ := store.GetMeta(db.KeyMaintenance)
		for _, snap := range []storeSnapshot{{maintenance: badRaw, priorConfirmed: true}, {maintenance: prevRaw}} {
			got = s.classifyMaintenanceWriteFailure(intended, snap)
			if !strings.Contains(got, "malformed") || !strings.Contains(got, "CORRUPT_STORE") || strings.Contains(got, "likely") {
				t.Fatalf("malformed record (snapshot %v): %q", snap.priorConfirmed, got)
			}
		}
	})

	t.Run("confirmed failure under a fixed clock is not misclassified", func(t *testing.T) {
		// Startup and resume share one timestamp here: the classifier must
		// still recognise the unchanged store (the record is present) and keep
		// the confirmed claim.
		f := newMaintFixture(t)
		clock := time.Now().Truncate(time.Second)
		s, store, _ := f.openWithClock(func() time.Time { return clock })
		if err := s.Quiesce(ctxT(t, 5*time.Second)); err != nil {
			t.Fatal(err)
		}
		faultOn(store, "maintenance.clear.in-tx")
		err := s.Resume(ctxT(t, 5*time.Second))
		if err == nil || !strings.Contains(err.Error(), "next start will be QUIESCED") {
			t.Fatalf("resume: %v", err)
		}
		if st := s.Status(); st.Maintenance == nil || !st.Maintenance.Durable {
			t.Fatalf("the confirmed claim must stand: %+v", st.Maintenance)
		}
	})
}

// TestQuiesceWriteReportedFailedButVisible (review P2): the transaction
// committed but reported an error (a failing final fsync has this shape).
// The engine reads back, finds the record visible, still refuses to claim
// durability (durable=false, the error says "visible"), the request fails,
// and a restart starts QUIESCED from the visible record; a repeated quiesce
// confirms durability.
func TestQuiesceWriteReportedFailedButVisible(t *testing.T) {
	f := newMaintFixture(t)
	s, store, stop := f.open()
	boom := errors.New("fsync reported a failure after the write")
	store.SetMetaFault(func(op string) error {
		if op == "maintenance.put.committed" {
			return boom
		}
		return nil
	})
	err := s.Quiesce(ctxT(t, 5*time.Second))
	if !errors.Is(err, ErrMaintenanceNotDurable) || !errors.Is(err, boom) || !strings.Contains(err.Error(), "visible") || !strings.Contains(err.Error(), "likely") {
		t.Fatalf("quiesce with an ambiguous write: %v", err)
	}
	if m, _ := store.GetMaintenance(); m == nil {
		t.Fatal("the record is visible in this scenario")
	}
	st := s.Status()
	if st.Mode != "QUIESCED" || st.Maintenance == nil || st.Maintenance.Durable || !strings.Contains(st.Maintenance.Error, "visible") || !strings.Contains(st.Maintenance.Error, "not confirmed") {
		t.Fatalf("status must not claim durability after an ambiguous write: %+v", st.Maintenance)
	}
	if s.Metrics().MaintenanceDurable.Value() != 0 {
		t.Fatal("gb_maintenance_durable must be 0 after an ambiguous write")
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	s2, store2, _ := f.open()
	if s2.Mode() != ModeQuiesced {
		t.Fatalf("the visible record makes the restart QUIESCED, got %s", s2.Mode())
	}
	if st := s2.Status(); st.Maintenance == nil || !st.Maintenance.Durable {
		t.Fatalf("a record read at startup is durable by definition: %+v", st.Maintenance)
	}
	// A record read at startup counts as confirmed, so a repeat quiesce is a
	// no-op here; the same-process retry path is TestQuiesceRetryInSameProcessConfirmsDurability.
	store2.SetMetaFault(nil)
	if err := s2.Quiesce(ctxT(t, 5*time.Second)); err != nil {
		t.Fatalf("repeat quiesce: %v", err)
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
// fails before it opens or is rolled back inside, the engine halts (GB1-03),
// the record and the old schedule stay in the store, status keeps saying
// the quiesce is durable, and the next start is QUIESCED, never SERVING.
func TestResumeWriteFailureHaltsAndKeepsRecord(t *testing.T) {
	for _, stage := range []string{"maintenance.clear", "maintenance.clear.in-tx"} {
		t.Run(stage, func(t *testing.T) {
			f := newMaintFixture(t)
			s, store, stop := f.open()
			if err := s.Quiesce(ctxT(t, 5*time.Second)); err != nil {
				t.Fatal(err)
			}
			scBefore, _ := store.GetSchedule()
			boom := errors.New("injected resume failure")
			store.SetMetaFault(func(op string) error {
				if op == stage {
					return boom
				}
				return nil
			})
			err := s.Resume(ctxT(t, 5*time.Second))
			if err == nil || !errors.Is(err, boom) || !strings.Contains(err.Error(), "HALTED") || !strings.Contains(err.Error(), "next start will be QUIESCED") {
				t.Fatalf("resume with a failing store: %v", err)
			}
			if s.Mode() != ModeHalted {
				t.Fatalf("mode %s, want HALTED", s.Mode())
			}
			if h := s.Health(time.Now()); h.Ready || h.GameReady || !h.Maintenance || !h.MaintenanceDurable {
				t.Fatalf("halted health %+v", h)
			}
			if st := s.Status(); st.Maintenance == nil || !st.Maintenance.Durable || st.Maintenance.Error != "" {
				t.Fatalf("a confirmed-failed resume keeps the durable claim: %+v", st.Maintenance)
			}
			if m, _ := store.GetMaintenance(); m == nil {
				t.Fatal("the record must still be in the store after a failed resume")
			}
			if sc, _ := store.GetSchedule(); !sc.SavedAt.Equal(scBefore.SavedAt) {
				t.Fatal("a failed resume must not write the schedule")
			}
			if s.Metrics().MaintenanceDurable.Value() != 1 {
				t.Fatal("gb_maintenance_durable must stay 1")
			}
			if err := stop(); !errors.Is(err, ErrHaltedNotFlushed) {
				t.Fatalf("stop: %v", err)
			}
			s2, _, _ := f.open()
			if s2.Mode() != ModeQuiesced {
				t.Fatalf("after a failed resume the next start must be QUIESCED, got %s", s2.Mode())
			}
		})
	}
}

// TestResumeReportedFailedButApplied (review P2): the resume transaction
// committed but reported an error. The engine halts, reads the store back,
// finds the record removed and the new schedule, withdraws the durability
// claim (durable=false with the explanation, metric 0), says the next start
// will be SERVING, and the next start is SERVING with the rescheduled times.
func TestResumeReportedFailedButApplied(t *testing.T) {
	f := newMaintFixture(t)
	// The engine reads the clock from the actor goroutine; the test advances
	// it under a mutex so the race detector sees a synchronised clock.
	var clockMu sync.Mutex
	clock := time.Now().Truncate(time.Second)
	nowFn := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	s, store, stop := f.openWithClock(nowFn)
	if err := s.Quiesce(ctxT(t, 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	// The resume happens later than the start, so the schedule it writes
	// differs from the one the store held (saved_at and the no-catch-up times).
	clockMu.Lock()
	clock = clock.Add(2 * time.Hour)
	clockMu.Unlock()
	boom := errors.New("fsync reported a failure after the write")
	store.SetMetaFault(func(op string) error {
		if op == "maintenance.clear.committed" {
			return boom
		}
		return nil
	})
	err := s.Resume(ctxT(t, 5*time.Second))
	if err == nil || !errors.Is(err, boom) || !strings.Contains(err.Error(), "HALTED") || !strings.Contains(err.Error(), "SERVING if the change persisted") {
		t.Fatalf("resume with an ambiguous write: %v", err)
	}
	if s.Mode() != ModeHalted {
		t.Fatalf("mode %s, want HALTED", s.Mode())
	}
	reason, _ := s.HaltDetails()
	if !strings.Contains(reason, "SERVING if the change persisted") || !strings.Contains(reason, "not confirmed") {
		t.Fatalf("halt reason must carry the qualified classification: %q", reason)
	}
	st := s.Status()
	if st.Maintenance == nil || st.Maintenance.Durable || !strings.Contains(st.Maintenance.Error, "SERVING if the change persisted") || !strings.Contains(st.Maintenance.Error, "applied") {
		t.Fatalf("status must withdraw the durability claim without promising SERVING: %+v", st.Maintenance)
	}
	if h := s.Health(time.Now()); h.Ready || h.MaintenanceDurable {
		t.Fatalf("health %+v", h)
	}
	if s.Metrics().MaintenanceDurable.Value() != 0 {
		t.Fatal("gb_maintenance_durable must be 0")
	}
	if m, _ := store.GetMaintenance(); m != nil {
		t.Fatal("the record is removed in this scenario")
	}
	sc, _ := store.GetSchedule()
	if !sc.SavedAt.Equal(clock) || !sc.NextUpdate.Equal(clock.Add(time.Duration(f.cfg.DefaultUpdateMinutes)*time.Minute)) {
		t.Fatalf("the resumed schedule is in the store in this scenario: %+v", sc)
	}
	if err := stop(); !errors.Is(err, ErrHaltedNotFlushed) {
		t.Fatalf("stop: %v", err)
	}
	s2, _, _ := f.open()
	if s2.Mode() != ModeServing {
		t.Fatalf("the applied resume makes the next start SERVING, got %s", s2.Mode())
	}
	if st := s2.Status(); st.Maintenance != nil || !st.NextUpdate.Equal(sc.NextUpdate) {
		t.Fatalf("status after restart: %+v", st)
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
