package db

import (
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/model"
)

// fixtureStore returns an initialised store with one star, two planets and
// one race, plus the WorldCommit that mirrors its content.
func fixtureStore(t *testing.T) (*Store, string, *WorldCommit) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gb.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	star := &model.Star{Number: 0, Name: "Sol", NumPlanets: 2}
	planets := []*model.Planet{{StarNum: 0, PlanetNum: 0}, {StarNum: 0, PlanetNum: 1}}
	sd := &model.StarData{NumStars: 1}
	rec := &UniverseRecord{Seed: 7, Stars: 1, MinPlanets: 2, MaxPlanets: 2, Planets: 2, Generator: "test", InitializedAt: time.Now(), Tool: "test"}
	if err := s.PutUniverse(sd, []*model.Star{star}, planets, rec); err != nil {
		t.Fatal(err)
	}
	race := &model.Race{PlayerNum: 1, Name: "Testers", Governors: 1}
	if err := s.PutRace(race); err != nil {
		t.Fatal(err)
	}
	c := &WorldCommit{
		Kind: "update", EngineVersion: "test", WallTime: time.Now(),
		Ships:     map[uint32]*model.Ship{1: {Number: 1, Alive: true, Owner: 1}},
		Stars:     []*model.Star{star},
		Planets:   map[uint8]map[uint8]*model.Planet{0: {0: planets[0], 1: planets[1]}},
		Races:     []*model.Race{race},
		StarData:  sd,
		Schedule:  &ScheduleRecord{NumUpdates: 1, NumSegments: 0, NextUpdate: time.Now().Add(time.Hour), NextSegment: time.Now().Add(20 * time.Minute), UpdateMinutes: 60, MovesPerUpdate: 3, SavedAt: time.Now()},
		News:      []NewsEntry{{Type: 0, Text: "news one"}},
		Telegrams: []TelegramEntry{{Player: 1, Governor: 0, Text: "tele one"}},
	}
	return s, path, c
}

func TestPutUniverseWritesInitCommitRecord(t *testing.T) {
	s, _, _ := fixtureStore(t)
	rec, err := s.GetCommitRecord()
	if err != nil || rec == nil {
		t.Fatalf("commit record after init: %+v %v", rec, err)
	}
	if rec.CommitSeq != 1 || rec.Kind != "init" || rec.Turn != 0 || rec.Segment != 0 || rec.SchemaVersion != CommitSchemaVersion {
		t.Fatalf("init commit record %+v", rec)
	}
}

func TestCommitWorldWritesEverythingOnce(t *testing.T) {
	s, _, c := fixtureStore(t)
	before, _ := s.WorldDigest()
	c.Races[0].Motto = "committed"
	c.Planets[0][1].Info[0].Resource = 42
	progress := 0
	c.Progress = func() { progress++ }
	rec, err := s.CommitWorld(c)
	if err != nil {
		t.Fatal(err)
	}
	if rec.CommitSeq != 2 || rec.Kind != "update" || rec.Turn != 1 || rec.Segment != 0 || rec.EngineVersion != "test" {
		t.Fatalf("commit record %+v", rec)
	}
	if progress < 1+1+2+1+1+1+1 { // ship, star, planets, race, sdata, schedule, commit
		t.Fatalf("progress callbacks %d", progress)
	}
	after, _ := s.WorldDigest()
	if after == before {
		t.Fatal("digest did not change after a commit that changed records")
	}
	r, _ := s.GetRace(1)
	p, _ := s.GetPlanet(0, 1)
	sh, _ := s.GetShip(1)
	sched, _ := s.GetSchedule()
	stored, _ := s.GetCommitRecord()
	if r.Motto != "committed" || p.Info[0].Resource != 42 || sh == nil || sched.NumUpdates != 1 || stored.CommitSeq != 2 {
		t.Fatalf("committed state not readable: race=%+v planet=%d ship=%v sched=%+v commit=%+v", r.Motto, p.Info[0].Resource, sh != nil, sched, stored)
	}
	news, _, _ := s.ReadNewsFrom(0, 0)
	tele, _ := s.ReadAndClearTelegrams(1, 0)
	if len(news) != 1 || news[0] != "news one" || len(tele) != 1 || tele[0] != "tele one" {
		t.Fatalf("effects not committed: news=%v tele=%v", news, tele)
	}
	// A second commit continues the sequence.
	c.Schedule.NumUpdates = 2
	rec2, err := s.CommitWorld(c)
	if err != nil || rec2.CommitSeq != 3 || rec2.Turn != 2 {
		t.Fatalf("second commit %+v %v", rec2, err)
	}
}

func TestCommitWorldFaultWritesNothing(t *testing.T) {
	for _, stage := range []string{"before-write", "after-write"} {
		t.Run(stage, func(t *testing.T) {
			s, _, c := fixtureStore(t)
			before, _ := s.WorldDigest()
			c.Races[0].Motto = "must-not-persist"
			injected := errors.New("injected " + stage)
			s.SetCommitFault(func(st string) error {
				if st == stage {
					return injected
				}
				return nil
			})
			rec, err := s.CommitWorld(c)
			if !errors.Is(err, injected) || rec != nil {
				t.Fatalf("commit with fault at %s: rec=%v err=%v", stage, rec, err)
			}
			after, _ := s.WorldDigest()
			if after != before {
				t.Fatal("a failed commit changed the store")
			}
			r, _ := s.GetRace(1)
			if r.Motto == "must-not-persist" {
				t.Fatal("record from a failed commit is visible")
			}
			if cr, _ := s.GetCommitRecord(); cr.CommitSeq != 1 {
				t.Fatalf("commit record advanced by a failed commit: %+v", cr)
			}
			if news, _, _ := s.ReadNewsFrom(0, 0); len(news) != 0 {
				t.Fatalf("news from a failed commit visible: %v", news)
			}
			// Clearing the fault makes the same commit succeed.
			s.SetCommitFault(nil)
			if rec, err := s.CommitWorld(c); err != nil || rec.CommitSeq != 2 {
				t.Fatalf("commit after clearing the fault: %+v %v", rec, err)
			}
		})
	}
}

func TestCommitWorldValidation(t *testing.T) {
	s, _, c := fixtureStore(t)
	bad := *c
	bad.Schedule = nil
	if _, err := s.CommitWorld(&bad); !errors.Is(err, ErrInvalidCommit) {
		t.Fatalf("nil schedule accepted: %v", err)
	}
	bad = *c
	bad.Kind = ""
	if _, err := s.CommitWorld(&bad); !errors.Is(err, ErrInvalidCommit) {
		t.Fatalf("empty kind accepted: %v", err)
	}
	bad = *c
	bad.StarData = nil
	if _, err := s.CommitWorld(&bad); !errors.Is(err, ErrInvalidCommit) {
		t.Fatalf("nil star data accepted: %v", err)
	}
	if cr, _ := s.GetCommitRecord(); cr.CommitSeq != 1 {
		t.Fatalf("invalid commits touched the record: %+v", cr)
	}
}

func TestWriteFenceRefusesOrdinaryWritesAndCountsThem(t *testing.T) {
	s, _, c := fixtureStore(t)
	before, _ := s.WorldDigest()
	s.SetWriteFence(true)
	defer s.SetWriteFence(false)
	writes := []struct {
		name string
		fn   func() error
	}{
		{"PutShip", func() error { return s.PutShip(&model.Ship{Number: 9}) }},
		{"DeleteShip", func() error { return s.DeleteShip(9) }},
		{"PutShipsBatch", func() error { return s.PutShipsBatch(c.Ships) }},
		{"PutPlanet", func() error { return s.PutPlanet(c.Planets[0][0]) }},
		{"PutStar", func() error { return s.PutStar(c.Stars[0]) }},
		{"PutRace", func() error { return s.PutRace(c.Races[0]) }},
		{"PutStarData", func() error { return s.PutStarData(c.StarData) }},
		{"PutPower", func() error { return s.PutPower([model.MaxPlayers]model.Power{}) }},
		{"PutBlocks", func() error { return s.PutBlocks([model.MaxPlayers]model.Block{}) }},
		{"PutPowerBlocks", func() error { return s.PutPowerBlocks(&model.PowerBlocks{}) }},
		{"PutCommod", func() error { return s.PutCommod(&model.Commodity{Number: 1}) }},
		{"DeleteCommod", func() error { return s.DeleteCommod(1) }},
		{"PutMeta", func() error { return s.PutMeta([]byte("x"), []byte("y")) }},
		{"DeleteMeta", func() error { return s.DeleteMeta([]byte("x")) }},
		{"PutSchedule", func() error { return s.PutSchedule(c.Schedule) }},
		{"PushTelegram", func() error { return s.PushTelegram(1, 0, "t") }},
		{"ReadAndClearTelegrams", func() error { _, err := s.ReadAndClearTelegrams(1, 0); return err }},
		{"AppendNews", func() error { _, err := s.AppendNews(0, "n"); return err }},
		{"PurgeNews", func() error { return s.PurgeNews(0) }},
		{"PutUniverse", func() error { return s.PutUniverse(c.StarData, c.Stars, nil, &UniverseRecord{}) }},
		{"AllocShipNum", func() error { _, err := s.AllocShipNum(); return err }},
		{"AllocCommodNum", func() error { _, err := s.AllocCommodNum(); return err }},
	}
	for _, w := range writes {
		if err := w.fn(); !errors.Is(err, ErrWriteFenced) {
			t.Errorf("%s under the fence: %v", w.name, err)
		}
	}
	if got := s.FencedWrites(); got != uint64(len(writes)) {
		t.Fatalf("fenced writes counted %d, want %d", got, len(writes))
	}
	if got := s.LastFencedWrite(); got != "AllocCommodNum" {
		t.Fatalf("last fenced write %q", got)
	}
	// Reads work, nothing changed, and the commit itself is the fence's
	// only sanctioned writer.
	if _, err := s.GetRace(1); err != nil {
		t.Fatal(err)
	}
	if mid, _ := s.WorldDigest(); mid != before {
		t.Fatal("a fenced write changed the store")
	}
	if _, err := s.CommitWorld(c); err != nil {
		t.Fatalf("CommitWorld under the fence: %v", err)
	}
	s.SetWriteFence(false)
	if err := s.PutRace(c.Races[0]); err != nil {
		t.Fatalf("write after lowering the fence: %v", err)
	}
	after, _ := s.WorldDigest()
	if after == before {
		t.Fatal("digest unchanged although the commit wrote")
	}
}

func TestWorldDigestIsStableAcrossReopenAndSensitiveToContent(t *testing.T) {
	s, path, c := fixtureStore(t)
	if _, err := s.CommitWorld(c); err != nil {
		t.Fatal(err)
	}
	d1, err := s.WorldDigest()
	if err != nil {
		t.Fatal(err)
	}
	// Rewriting the schedule with a different SavedAt does not change it.
	sc := *c.Schedule
	sc.SavedAt = sc.SavedAt.Add(time.Hour)
	if err := s.PutSchedule(&sc); err != nil {
		t.Fatal(err)
	}
	d2, _ := s.WorldDigest()
	if d1 != d2 {
		t.Fatal("digest depends on schedule SavedAt")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	d3, _ := s2.WorldDigest()
	if d3 != d1 {
		t.Fatal("digest changed across reopen")
	}
	if err := s2.PutRace(&model.Race{PlayerNum: 1, Name: "Testers", Motto: "changed"}); err != nil {
		t.Fatal(err)
	}
	d4, _ := s2.WorldDigest()
	if d4 == d1 {
		t.Fatal("digest blind to a record change")
	}
}

// TestCommitRecordValidate (CR18-R01) covers every format rule.
func TestCommitRecordValidate(t *testing.T) {
	good := CommitRecord{SchemaVersion: 1, CommitSeq: 5, Turn: 2, Segment: 0, Kind: "update", WallTime: time.Now()}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}
	cases := map[string]func(r *CommitRecord){
		"schema 0":         func(r *CommitRecord) { r.SchemaVersion = 0 },
		"schema 2":         func(r *CommitRecord) { r.SchemaVersion = 2 },
		"unknown kind":     func(r *CommitRecord) { r.Kind = "restore" },
		"empty kind":       func(r *CommitRecord) { r.Kind = "" },
		"seq 0":            func(r *CommitRecord) { r.CommitSeq = 0 },
		"seq overflow":     func(r *CommitRecord) { r.CommitSeq = math.MaxUint64 },
		"negative turn":    func(r *CommitRecord) { r.Turn = -1 },
		"negative segment": func(r *CommitRecord) { r.Segment = -1 },
		"init counters":    func(r *CommitRecord) { r.Kind = "init"; r.Turn = 1 },
		"update segment":   func(r *CommitRecord) { r.Segment = 2 },
		"zero wall time":   func(r *CommitRecord) { r.WallTime = time.Time{} },
	}
	for name, mutate := range cases {
		r := good
		mutate(&r)
		if err := r.Validate(); !errors.Is(err, ErrCorruptCommitRecord) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Segment commits may carry a segment counter; shutdown commits too.
	seg := good
	seg.Kind = "segment"
	seg.Segment = 2
	if err := seg.Validate(); err != nil {
		t.Errorf("segment record rejected: %v", err)
	}
}

// TestGetCommitRecordDistinguishesAbsentFromMalformed (CR18-R01).
func TestGetCommitRecordDistinguishesAbsentFromMalformed(t *testing.T) {
	s, _, _ := fixtureStore(t)
	if err := s.DeleteMeta(KeyCommit); err != nil {
		t.Fatal(err)
	}
	if rec, err := s.GetCommitRecord(); rec != nil || err != nil {
		t.Fatalf("absent record: %+v %v", rec, err)
	}
	for name, raw := range map[string]string{
		"null":          "null",
		"empty object":  "{}",
		"missing":       `{"schema_version":1}`,
		"unknown field": `{"schema_version":1,"commit_seq":1,"turn":0,"segment":0,"kind":"init","wall_time":"2026-01-01T00:00:00Z","extra":true}`,
		"garbage":       "{",
	} {
		if err := s.PutMeta(KeyCommit, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		if rec, err := s.GetCommitRecord(); !errors.Is(err, ErrCorruptCommitRecord) || rec != nil {
			t.Errorf("%s: rec=%+v err=%v", name, rec, err)
		}
	}
	// CommitWorld refuses to build on a malformed previous record.
	_, _, c := fixtureStore(t)
	if _, err := s.CommitWorld(c); !errors.Is(err, ErrCorruptCommitRecord) {
		t.Fatalf("commit on a malformed previous record: %v", err)
	}
}

// TestCommitWorldPopsExactlyTheStagedFreeNumbers (CR18-R04): the commit
// deletes the staged free entries atomically and refuses a mismatch.
func TestCommitWorldPopsExactlyTheStagedFreeNumbers(t *testing.T) {
	s, _, c := fixtureStore(t)
	for _, n := range []uint32{7, 8, 9} {
		if err := s.PutShip(&model.Ship{Number: n}); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []uint32{7, 9} { // free list in pop order: [9 7]
		if err := s.DeleteShip(n); err != nil {
			t.Fatal(err)
		}
	}
	free, _ := s.FreeShipNums()
	if len(free) != 2 || free[0] != 9 || free[1] != 7 {
		t.Fatalf("free list %v", free)
	}
	if max, _ := s.MaxShipNum(); max != 8 {
		t.Fatalf("max ship %d", max)
	}
	before, _ := s.WorldDigest()
	// A mismatch (claiming 7 was popped first) writes nothing.
	bad := *c
	bad.ShipFreePops = []uint32{7}
	if _, err := s.CommitWorld(&bad); !errors.Is(err, ErrFreeListMismatch) {
		t.Fatalf("mismatched pops: %v", err)
	}
	if d, _ := s.WorldDigest(); d != before {
		t.Fatal("mismatched commit changed the store")
	}
	// Claiming more than the list holds writes nothing.
	bad.ShipFreePops = []uint32{9, 7, 1}
	if _, err := s.CommitWorld(&bad); !errors.Is(err, ErrFreeListMismatch) {
		t.Fatalf("over-popped: %v", err)
	}
	// The correct pop order is consumed with the commit.
	c.ShipFreePops = []uint32{9}
	if _, err := s.CommitWorld(c); err != nil {
		t.Fatal(err)
	}
	free, _ = s.FreeShipNums()
	if len(free) != 1 || free[0] != 7 {
		t.Fatalf("free list after commit %v", free)
	}
	if n, _ := s.AllocShipNum(); n != 7 {
		t.Fatalf("store allocator after commit gave %d, want 7", n)
	}
}

// TestStrictDecodeOfMetaRecords (CR18-R01, second review): presence of
// every required field, no null fields, no unknown fields, complete input
// consumption, and the schedule record's value validation.
func TestStrictDecodeOfMetaRecords(t *testing.T) {
	s, _, _ := fixtureStore(t)
	validSchedule := `{"next_update":"2026-01-01T01:00:00Z","next_segment":"2026-01-01T00:20:00Z","num_updates":0,"num_segments":0,"update_minutes":60,"moves_per_update":3,"saved_at":"2026-01-01T00:00:00Z"}`
	if err := s.PutMeta(KeySchedule, []byte(validSchedule)); err != nil {
		t.Fatal(err)
	}
	if rec, err := s.GetSchedule(); err != nil || rec == nil || rec.UpdateMinutes != 60 {
		t.Fatalf("valid schedule: %+v %v", rec, err)
	}
	if err := s.DeleteMeta(KeySchedule); err != nil {
		t.Fatal(err)
	}
	if rec, err := s.GetSchedule(); rec != nil || err != nil {
		t.Fatalf("absent schedule must be nil, nil: %+v %v", rec, err)
	}
	bad := map[string]string{
		"null":            "null",
		"empty":           "{}",
		"array":           "[]",
		"missing counter": `{"next_update":"2026-01-01T01:00:00Z","next_segment":"2026-01-01T00:20:00Z","num_updates":0,"update_minutes":60,"moves_per_update":3,"saved_at":"2026-01-01T00:00:00Z"}`,
		"null field":      `{"next_update":null,"next_segment":"2026-01-01T00:20:00Z","num_updates":0,"num_segments":0,"update_minutes":60,"moves_per_update":3,"saved_at":"2026-01-01T00:00:00Z"}`,
		"trailing":        validSchedule + "]",
		"second value":    validSchedule + " 1",
		"unknown field":   validSchedule[:len(validSchedule)-1] + `,"x":1}`,
		"zero interval":   `{"next_update":"2026-01-01T01:00:00Z","next_segment":"2026-01-01T00:20:00Z","num_updates":0,"num_segments":0,"update_minutes":0,"moves_per_update":3,"saved_at":"2026-01-01T00:00:00Z"}`,
		"negative":        `{"next_update":"2026-01-01T01:00:00Z","next_segment":"2026-01-01T00:20:00Z","num_updates":-1,"num_segments":0,"update_minutes":60,"moves_per_update":3,"saved_at":"2026-01-01T00:00:00Z"}`,
		"whitespace only": "   ",
	}
	for name, raw := range bad {
		if err := s.PutMeta(KeySchedule, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		if rec, err := s.GetSchedule(); !errors.Is(err, ErrCorruptScheduleRecord) || rec != nil {
			t.Errorf("schedule %s: rec=%+v err=%v", name, rec, err)
		}
	}
	validCommit := `{"schema_version":1,"commit_seq":1,"turn":0,"segment":0,"kind":"init","wall_time":"2026-01-01T00:00:00Z"}`
	for name, raw := range map[string]string{
		"missing turn and segment": `{"schema_version":1,"commit_seq":1,"kind":"init","wall_time":"2026-01-01T00:00:00Z"}`,
		"trailing bracket":         validCommit + "]",
		"null field":               `{"schema_version":1,"commit_seq":1,"turn":0,"segment":null,"kind":"init","wall_time":"2026-01-01T00:00:00Z"}`,
		"array":                    "[1]",
	} {
		if err := s.PutMeta(KeyCommit, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		if rec, err := s.GetCommitRecord(); !errors.Is(err, ErrCorruptCommitRecord) || rec != nil {
			t.Errorf("commit %s: rec=%+v err=%v", name, rec, err)
		}
	}
	// Whitespace around a valid document is fine; engine_version is optional.
	if err := s.PutMeta(KeyCommit, []byte("  "+validCommit+"\n")); err != nil {
		t.Fatal(err)
	}
	if rec, err := s.GetCommitRecord(); err != nil || rec == nil || rec.Kind != "init" {
		t.Fatalf("valid commit with whitespace: %+v %v", rec, err)
	}
}
