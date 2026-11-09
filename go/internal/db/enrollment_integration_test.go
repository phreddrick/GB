package db_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/game"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/racegen"
)

// This external-package test exercises the real CR-07B output without making
// db import game. CR07C_OLD_READER optionally points at a separately built
// baseline reader (see the implementation record); it reads this disposable
// fixture BEFORE the current actor starts. No live database is ever opened.
func TestEnrollmentCR07BAndCompatibleStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "world.db")
	s, e := db.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close() }()
	st := &model.Star{NumPlanets: 2, Name: "isolated"}
	p := &model.Planet{MaxX: 2, MaxY: 1, Type: model.PlanetEarth, Sectors: []model.Sector{{Condition: model.SectorLand, Resource: 17}, {Condition: model.SectorLand}}, Ships: 7}
	tail := &model.Ship{Number: 9, Alive: true, WhatOrbits: model.LevelPlan}
	head := &model.Ship{Number: 7, Alive: true, WhatOrbits: model.LevelPlan, NextShip: 9}
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	if e = s.PutUniverse(&model.StarData{NumStars: 1}, []*model.Star{st}, []*model.Planet{p, {PlanetNum: 1}}, &db.UniverseRecord{Stars: 1, Planets: 2, InitializedAt: now}); e != nil {
		t.Fatal(e)
	}
	for _, sh := range []*model.Ship{head, tail} {
		if e = s.PutShip(sh); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.PutSchedule(&db.ScheduleRecord{NextUpdate: now.Add(time.Hour), NextSegment: now.Add(20 * time.Minute), UpdateMinutes: 60, MovesPerUpdate: 3, SavedAt: now}); e != nil {
		t.Fatal(e)
	}
	if e = s.PutMaintenance(&db.MaintenanceRecord{SchemaVersion: 1, Quiesced: true, SetAt: now, Reason: "isolated compatibility fixture"}); e != nil {
		t.Fatal(e)
	}
	spec := racegen.Default()
	spec.Name = "Pure storage integration"
	spec.Attributes.Fertilize = .25
	spec.SectorCompat[racegen.Land] = 100
	ticket, e := s.EnrollmentShipCandidate()
	if e != nil {
		t.Fatal(e)
	}
	w := game.PlacementUniverse{Stars: []*model.Star{st}, Planets: map[uint8]map[uint8]*model.Planet{0: {0: p, 1: {PlanetNum: 1}}}, Ships: map[uint32]*model.Ship{7: head, 9: tail}}
	proposal, e := game.ProposePlacement(spec, 1, ticket.Number, w, game.PlacementOrder{Stars: []int{0}, Sectors: []int{1, 0}}, game.PlacementIdentity{RaceCredential: "opaque-r", GovernorCredential: "opaque-g"})
	if e != nil {
		t.Fatal(e)
	}
	in := db.Enrollment{RequestID: "integration", CallerID: "fixture-account", RequestHash: strings.Repeat("a", 64), CreatedAt: now, Race: proposal.Race, Planet: proposal.Planet, Star: proposal.Star, Ship: proposal.Ship, ExpectedPlanet: p, ExpectedStar: st, ExpectedChain: []*model.Ship{head, tail}, CapitalX: proposal.CapitalX, CapitalY: proposal.CapitalY, Ticket: ticket}
	before := map[string][]byte{}
	for _, k := range [][]byte{db.KeyCommit, db.KeySchedule, db.KeyMaintenance, db.KeyUniverse} {
		before[string(k)], _ = s.GetMeta(k)
	}
	rec, e := s.PutEnrollment(in)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	if reader := os.Getenv("CR07C_OLD_READER"); reader != "" {
		//nolint:gosec // Explicit local test executable, only this t.TempDir database is passed.
		cmd := exec.Command(reader, "-test.run=^TestCR07CLegacyReader$", "-test.v")
		cmd.Env = append(os.Environ(), "CR07C_COMPAT_PATH="+path)
		output, e := cmd.CombinedOutput()
		if e != nil || !strings.Contains(string(output), "CR07C_OLD_READER_PASS") {
			t.Fatalf("baseline reader: %s %v", output, e)
		}
	}
	s, e = db.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	gotR, e := s.GetRace(1)
	if e != nil || !reflect.DeepEqual(gotR, proposal.Race) {
		t.Fatal("full race mismatch", e)
	}
	gotP, e := s.GetPlanet(0, 0)
	if e != nil || !reflect.DeepEqual(gotP, proposal.Planet) {
		t.Fatal("full planet mismatch", e)
	}
	gotS, e := s.GetStar(0)
	if e != nil || !reflect.DeepEqual(gotS, proposal.Star) {
		t.Fatal("full star mismatch", e)
	}
	gotShip, e := s.GetShip(ticket.Number)
	if e != nil || !reflect.DeepEqual(gotShip, proposal.Ship) {
		t.Fatal("full government ship mismatch", e)
	}
	ships, e := s.AllShips()
	if e != nil || len(ships) != 3 || !reflect.DeepEqual(ships[7], head) || !reflect.DeepEqual(ships[9], tail) || gotShip.NextShip != 7 || gotP.Ships != gotShip.Number || gotR.GovShip != gotShip.Number {
		t.Fatal("changed existing orbit chain", e)
	}
	gotRec, e := s.GetPlacement(in.CallerID, in.RequestID)
	if e != nil || !reflect.DeepEqual(gotRec, rec) {
		t.Fatal("metadata mismatch", e)
	}
	digest, e := s.WorldDigest()
	if e != nil {
		t.Fatal(e)
	}
	state, e := game.New(config.Default(), s, game.Options{Now: func() time.Time { return now }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if e != nil {
		t.Fatal(e)
	}
	// Only startup validation runs in an isolated QUIESCED actor. Stop's existing
	// shutdown flush is aborted by the existing test hook, preserving the fixture.
	defer func() {
		s.SetCommitFault(func(stage string) error { return errors.New("isolated stop intentionally aborted") })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = state.Stop(ctx)
		s.SetCommitFault(nil)
	}()
	if state.Mode() != game.ModeQuiesced || strings.Contains(state.Status().HaltReason, "CORRUPT_STORE") {
		t.Fatal("startup rejected additive metadata", state.Mode())
	}
	after, e := s.WorldDigest()
	if e != nil || after != digest {
		t.Fatal("startup wrote world", e)
	}
	for k, v := range before {
		raw, e := s.GetMeta([]byte(k))
		if e != nil || !bytes.Equal(v, raw) {
			t.Fatal("changed existing metadata", k, e)
		}
	}
}
