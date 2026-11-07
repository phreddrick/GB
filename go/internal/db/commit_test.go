package db

import (
	"errors"
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
	}
	for _, w := range writes {
		if err := w.fn(); !errors.Is(err, ErrWriteFenced) {
			t.Errorf("%s under the fence: %v", w.name, err)
		}
	}
	if got := s.FencedWrites(); got != uint64(len(writes)) {
		t.Fatalf("fenced writes counted %d, want %d", got, len(writes))
	}
	// Allocators are exempt (documented), reads work, and the commit itself
	// is the fence's sanctioned writer.
	if _, err := s.AllocShipNum(); err != nil {
		t.Fatalf("AllocShipNum under the fence: %v", err)
	}
	if _, err := s.AllocCommodNum(); err != nil {
		t.Fatalf("AllocCommodNum under the fence: %v", err)
	}
	if _, err := s.GetRace(1); err != nil {
		t.Fatal(err)
	}
	mid, _ := s.WorldDigest()
	// Only the allocator calls changed the store (free-list / counters).
	_ = mid
	if _, err := s.CommitWorld(c); err != nil {
		t.Fatalf("CommitWorld under the fence: %v", err)
	}
	s.SetWriteFence(false)
	if err := s.PutRace(c.Races[0]); err != nil {
		t.Fatalf("write after lowering the fence: %v", err)
	}
	after, _ := s.WorldDigest()
	if after == before {
		t.Fatal("digest unchanged although the commit and allocators wrote")
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
