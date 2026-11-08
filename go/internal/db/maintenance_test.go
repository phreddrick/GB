package db

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "gb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func testSchedule(now time.Time) *ScheduleRecord {
	return &ScheduleRecord{NextUpdate: now.Add(time.Hour), NextSegment: now.Add(20 * time.Minute), NumUpdates: 17, NumSegments: 0, UpdateMinutes: 30, MovesPerUpdate: 3, SavedAt: now}
}

// TestMaintenanceRecordRoundTrip (CR-43): absent -> nil; put -> read back
// equal; the resume write removes it and writes the schedule in one
// transaction; a second clear is not an error.
func TestMaintenanceRecordRoundTrip(t *testing.T) {
	s := openTestStore(t)
	if rec, err := s.GetMaintenance(); err != nil || rec != nil {
		t.Fatalf("fresh store: rec=%v err=%v", rec, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	want := &MaintenanceRecord{SchemaVersion: MaintenanceSchemaVersion, Quiesced: true, SetAt: now, Reason: "test", EngineVersion: "v0@abc"}
	if err := s.PutMaintenance(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMaintenance()
	if err != nil || got == nil {
		t.Fatalf("after put: rec=%v err=%v", got, err)
	}
	if *got != *want {
		t.Fatalf("round trip: got %+v want %+v", *got, *want)
	}
	before := testSchedule(now)
	if err := s.ClearMaintenanceWithSchedule(before); err != nil {
		t.Fatal(err)
	}
	if rec, err := s.GetMaintenance(); err != nil || rec != nil {
		t.Fatalf("after clear: rec=%v err=%v", rec, err)
	}
	sched, err := s.GetSchedule()
	if err != nil || sched == nil || sched.NumUpdates != 17 || !sched.NextUpdate.Equal(before.NextUpdate) {
		t.Fatalf("schedule not written with the clear: %+v %v", sched, err)
	}
	if err := s.ClearMaintenanceWithSchedule(before); err != nil {
		t.Fatalf("clearing an absent record must succeed: %v", err)
	}
}

// TestMaintenanceRecordStrictness: every malformed form is
// ErrCorruptMaintenanceRecord, never nil and never a lenient zero value.
func TestMaintenanceRecordStrictness(t *testing.T) {
	cases := map[string]string{
		"empty":           "",
		"null":            "null",
		"array":           "[]",
		"string":          `"quiesced"`,
		"missing set_at":  `{"schema_version":1,"quiesced":true,"reason":"x"}`,
		"null reason":     `{"schema_version":1,"quiesced":true,"set_at":"2026-11-08T19:50:12Z","reason":null}`,
		"unknown field":   `{"schema_version":1,"quiesced":true,"set_at":"2026-11-08T19:50:12Z","reason":"x","extra":1}`,
		"trailing data":   `{"schema_version":1,"quiesced":true,"set_at":"2026-11-08T19:50:12Z","reason":"x"} {}`,
		"schema 2":        `{"schema_version":2,"quiesced":true,"set_at":"2026-11-08T19:50:12Z","reason":"x"}`,
		"quiesced false":  `{"schema_version":1,"quiesced":false,"set_at":"2026-11-08T19:50:12Z","reason":"x"}`,
		"zero set_at":     `{"schema_version":1,"quiesced":true,"set_at":"0001-01-01T00:00:00Z","reason":"x"}`,
		"wrong type":      `{"schema_version":"1","quiesced":true,"set_at":"2026-11-08T19:50:12Z","reason":"x"}`,
		"garbage":         `{not json`,
		"optional null":   `{"schema_version":1,"quiesced":true,"set_at":"2026-11-08T19:50:12Z","reason":"x","engine_version":null}`,
		"whitespace only": "   \n",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			s := openTestStore(t)
			if err := s.PutMeta(KeyMaintenance, []byte(raw)); err != nil {
				t.Fatal(err)
			}
			rec, err := s.GetMaintenance()
			if !errors.Is(err, ErrCorruptMaintenanceRecord) || rec != nil {
				t.Fatalf("%s: rec=%v err=%v (want ErrCorruptMaintenanceRecord)", name, rec, err)
			}
		})
	}
	// The optional field may be absent.
	s := openTestStore(t)
	if err := s.PutMeta(KeyMaintenance, []byte(`{"schema_version":1,"quiesced":true,"set_at":"2026-11-08T19:50:12Z","reason":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if rec, err := s.GetMaintenance(); err != nil || rec == nil || rec.EngineVersion != "" {
		t.Fatalf("minimal valid record: %+v %v", rec, err)
	}
	// Put refuses an invalid record without touching the store.
	if err := s.PutMaintenance(&MaintenanceRecord{SchemaVersion: 1, Quiesced: false, SetAt: time.Now()}); !errors.Is(err, ErrCorruptMaintenanceRecord) {
		t.Fatalf("put of an invalid record: %v", err)
	}
	if err := s.ClearMaintenanceWithSchedule(&ScheduleRecord{}); !errors.Is(err, ErrCorruptScheduleRecord) {
		t.Fatalf("clear with an invalid schedule: %v", err)
	}
	if err := s.ClearMaintenanceWithSchedule(nil); err == nil {
		t.Fatal("clear with a nil schedule must fail")
	}
}

// TestMaintenanceWritesAreFencedAndIndependentOfCommits (CR-18 x CR-43):
// both maintenance writes are refused while a turn body runs, the fence
// counts them, CommitWorld neither removes nor creates the record (so a
// shutdown commit while quiesced keeps the quiesce), and the record is
// operational state outside the logical world digest.
func TestMaintenanceWritesAreFencedAndIndependentOfCommits(t *testing.T) {
	s, _, c := fixtureStore(t)
	now := time.Now().UTC()
	rec := &MaintenanceRecord{SchemaVersion: 1, Quiesced: true, SetAt: now, Reason: "fence test"}
	s.SetWriteFence(true)
	if err := s.PutMaintenance(rec); !errors.Is(err, ErrWriteFenced) {
		t.Fatalf("fenced put: %v", err)
	}
	if err := s.ClearMaintenanceWithSchedule(c.Schedule); !errors.Is(err, ErrWriteFenced) {
		t.Fatalf("fenced clear: %v", err)
	}
	if s.FencedWrites() != 2 || s.LastFencedWrite() != "ClearMaintenanceWithSchedule" {
		t.Fatalf("fence accounting: %d %q", s.FencedWrites(), s.LastFencedWrite())
	}
	if got, _ := s.GetMaintenance(); got != nil {
		t.Fatal("fenced put must write nothing")
	}
	s.SetWriteFence(false)
	if err := s.PutMaintenance(rec); err != nil {
		t.Fatal(err)
	}
	// The shutdown flush performed while quiesced is a commit: it must keep the record.
	c.Kind = CommitKindShutdown
	if _, err := s.CommitWorld(c); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMaintenance()
	if err != nil || got == nil || !got.SetAt.Equal(now) {
		t.Fatalf("maintenance record after a commit: %+v %v", got, err)
	}
	// Digest: a clear/put pair leaves the logical world digest unchanged.
	if err := s.ClearMaintenanceWithSchedule(c.Schedule); err != nil {
		t.Fatal(err)
	}
	d1, _ := s.WorldDigest()
	if err := s.PutMaintenance(rec); err != nil {
		t.Fatal(err)
	}
	d2, _ := s.WorldDigest()
	if d1 != d2 {
		t.Fatal("the maintenance record must not change the world digest")
	}
	// And a commit after the clear does not resurrect it.
	if err := s.ClearMaintenanceWithSchedule(c.Schedule); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitWorld(c); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetMaintenance(); got != nil {
		t.Fatal("a commit must not create a maintenance record")
	}
}

// TestMetaFaultHook: the test-only fault returns the error to the caller
// before any transaction, so a failed quiesce write leaves no record and a
// failed resume write leaves the record and the old schedule.
func TestMetaFaultHook(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC()
	boom := errors.New("disk on fire")
	s.SetMetaFault(func(op string) error {
		if op == "maintenance.put" {
			return boom
		}
		return nil
	})
	if err := s.PutMaintenance(&MaintenanceRecord{SchemaVersion: 1, Quiesced: true, SetAt: now, Reason: "x"}); !errors.Is(err, boom) {
		t.Fatalf("put with fault: %v", err)
	}
	if rec, _ := s.GetMaintenance(); rec != nil {
		t.Fatal("a failed put must leave no record")
	}
	s.SetMetaFault(nil)
	if err := s.PutMaintenance(&MaintenanceRecord{SchemaVersion: 1, Quiesced: true, SetAt: now, Reason: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSchedule(testSchedule(now.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	s.SetMetaFault(func(op string) error {
		if op == "maintenance.clear" {
			return boom
		}
		return nil
	})
	if err := s.ClearMaintenanceWithSchedule(testSchedule(now)); !errors.Is(err, boom) {
		t.Fatalf("clear with fault: %v", err)
	}
	rec, _ := s.GetMaintenance()
	sched, _ := s.GetSchedule()
	if rec == nil || sched == nil || !sched.SavedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("a failed clear must leave the record and the old schedule: rec=%v sched=%+v", rec, sched)
	}
}
