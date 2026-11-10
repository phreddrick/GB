package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"

	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

func commandFixture(t *testing.T) (*Store, string, CommandRecords) {
	t.Helper()
	s, path, _ := fixtureStore(t)
	before := &model.Star{Number: 0, Name: "Sol", NumPlanets: 2, Explored: 1}
	before.AP[0] = 5
	after := *before
	after.Explored = 3
	after.AP[0] = 4
	in := CommandRecords{Player: 1, Recipient: 2, APStar: 0, Stars: []CommandStar{{Expected: before, Proposed: &after, Share: true}}}
	if e := s.PutStar(before); e != nil {
		t.Fatal(e)
	}
	for n := uint8(0); n < 2; n++ {
		p := &model.Planet{StarNum: 0, PlanetNum: n, MaxX: 2, MaxY: 1, Popn: 3, Sectors: []model.Sector{{Owner: 1, Race: 1, Popn: 3, Resource: 42, Fert: 100}, {Type: model.SectorLand}}}
		p.Info[0].Explored = n == 0
		p.Info[0].Resource = 77
		p.Info[0].NumSectsOwned = 1
		q := *p
		q.Sectors = append([]model.Sector(nil), p.Sectors...)
		if n == 0 {
			q.Info[1].Explored = true
		}
		if n == 0 {
			p.Ships = 1
			q.Ships = 1
			sh := &model.Ship{Number: 1, Alive: true, Active: true, Owner: 1, Race: 1, WhatOrbits: model.LevelPlan, StOrbits: 0, PNumOrbits: 0, Docked: true, Resource: 19}
			if e := s.PutShip(sh); e != nil {
				t.Fatal(e)
			}
		}
		if e := s.PutPlanet(p); e != nil {
			t.Fatal(e)
		}
		in.Planets = append(in.Planets, CommandPlanet{Expected: p, Proposed: &q})
	}
	if e := s.PutRace(&model.Race{PlayerNum: 2, Name: "Recipient"}); e != nil {
		t.Fatal(e)
	}
	return s, path, in
}
func assertCommandError(t *testing.T, outcome CommandOutcome, e error, code CommandCode, want CommandOutcome) {
	t.Helper()
	var ce *CommandError
	if !errors.As(e, &ce) || outcome != want || ce.Outcome != want || ce.Code != code {
		t.Fatalf("outcome=%s error=%v; want %s/%s", outcome, e, want, code)
	}
}
func assertCommandRecords(t *testing.T, s *Store, in CommandRecords, after bool) {
	t.Helper()
	for _, ed := range in.Stars {
		want := ed.Expected
		if after {
			want = ed.Proposed
		}
		got, e := s.GetStar(want.Number)
		if e != nil || !reflect.DeepEqual(want, got) {
			t.Fatal("complete star differs", e)
		}
	}
	for _, ed := range in.Planets {
		want := ed.Expected
		if after {
			want = ed.Proposed
		}
		got, e := s.GetPlanet(want.StarNum, want.PlanetNum)
		if e != nil || !reflect.DeepEqual(want, got) {
			t.Fatal("complete planet differs", e)
		}
	}
}
func TestCommandRecordsAtomicAndReopen(t *testing.T) {
	s, path, in := commandFixture(t)
	b, _ := json.Marshal(in)
	before := enrollmentSnapshot(t, s)
	meta := map[string][]byte{}
	for _, k := range [][]byte{KeyCommit, KeySchedule, KeyMaintenance, KeyUniverse} {
		v, e := s.GetMeta(k)
		if e != nil {
			t.Fatal(e)
		}
		meta[string(k)] = v
	}
	sh, e := s.GetShip(1)
	if e != nil {
		t.Fatal(e)
	}
	out, e := s.CommitCommandRecords(in)
	if e != nil || out != CommandCommitted {
		t.Fatal(out, e)
	}
	assertCommandRecords(t, s, in, true)
	a, _ := json.Marshal(in)
	if !bytes.Equal(a, b) {
		t.Fatal("mutated input")
	}
	if before == enrollmentSnapshot(t, s) {
		t.Fatal("transaction not exercised")
	}
	for k, v := range meta {
		got, e := s.GetMeta([]byte(k))
		if e != nil || !bytes.Equal(v, got) {
			t.Fatal("metadata changed", k, e)
		}
	}
	got, e := s.GetShip(1)
	if e != nil || !reflect.DeepEqual(sh, got) {
		t.Fatal("ship/link/cargo changed", e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = reopened.Close() }()
	assertCommandRecords(t, reopened, in, true)
}

func TestCommandRecordsRollbackFailures(t *testing.T) {
	for _, stage := range []string{"command.before", "command.validated", "command.write:1", "command.write:2", "command.write:3", "command.before-commit"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				s, _, in := commandFixture(t)
				before := enrollmentSnapshot(t, s)
				reached := false
				s.SetMetaFault(func(op string) error {
					if op == stage {
						reached = true
						if panics {
							panic("secret fault text must not escape")
						}
						return errors.New("secret fault text must not escape")
					}
					return nil
				})
				out, e := s.CommitCommandRecords(in)
				assertCommandError(t, out, e, CommandIO, CommandNotCommitted)
				if !reached || s.CommandWriteUncertain() || before != enrollmentSnapshot(t, s) {
					t.Fatal("fault not reached, partial write or wrong uncertainty guard")
				}
				assertCommandRecords(t, s, in, false)
			})
		}
	}
}

func TestCommandRecordsCommittedAndUncertain(t *testing.T) {
	for _, stage := range []string{"command.commit-error", "command.after-commit"} {
		for _, panics := range []bool{false, true} {
			for _, readFail := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/panic=%t/readfail=%t", stage, panics, readFail), func(t *testing.T) {
					s, _, in := commandFixture(t)
					reached := false
					s.SetMetaFault(func(op string) error {
						if op == "command.readback" && readFail {
							return errors.New("read unavailable")
						}
						if op == stage {
							reached = true
							if panics {
								panic("report fault")
							}
							return errors.New("report fault")
						}
						return nil
					})
					want := CommandCommitted
					if stage == "command.commit-error" {
						want = CommandUncertain
					}
					out, e := s.CommitCommandRecords(in)
					assertCommandError(t, out, e, CommandIO, want)
					if !reached {
						t.Fatal("intended fault not reached")
					}
					assertCommandRecords(t, s, in, true)
					if want == CommandUncertain {
						ce := e.(*CommandError)
						visibility := "proposal_visible"
						if readFail {
							visibility = "unavailable"
						}
						if ce.Readback != visibility || !s.CommandWriteUncertain() {
							t.Fatal("incorrect evidence/guard", ce.Readback)
						}
						before := enrollmentSnapshot(t, s)
						s.SetMetaFault(nil)
						out, e = s.CommitCommandRecords(in)
						assertCommandError(t, out, e, CommandIO, CommandUncertain)
						if before != enrollmentSnapshot(t, s) {
							t.Fatal("uncertainty guard bypassed")
						}
					} else if s.CommandWriteUncertain() {
						t.Fatal("confirmed commit misclassified uncertain")
					}
				})
			}
		}
	}
}

func TestCommandRecordsValidation(t *testing.T) {
	cases := map[string]func(*CommandRecords){
		"source": func(i *CommandRecords) { i.Player = 0 }, "recipient": func(i *CommandRecords) { i.Recipient = 65 }, "missing AP star": func(i *CommandRecords) { i.APStar = 1 }, "no stars": func(i *CommandRecords) { i.Stars = nil },
		"nil expected star": func(i *CommandRecords) { i.Stars[0].Expected = nil }, "nil proposed star": func(i *CommandRecords) { i.Stars[0].Proposed = nil }, "duplicate star": func(i *CommandRecords) { i.Stars = append(i.Stars, i.Stars[0]) }, "unknown star": func(i *CommandRecords) { i.Stars[0].Expected.Explored = 0 },
		"wrong AP": func(i *CommandRecords) { i.Stars[0].Proposed.AP[0] = 3 }, "star identity": func(i *CommandRecords) { i.Stars[0].Proposed.Number = 1 }, "orbit head": func(i *CommandRecords) { i.Stars[0].Proposed.Ships = 8 }, "unsupported stockpile": func(i *CommandRecords) { i.Planets[0].Proposed.Info[0].Resource++ },
		"sector alias mutation": func(i *CommandRecords) { i.Planets[0].Proposed.Sectors[0].Popn++ }, "nil planet": func(i *CommandRecords) { i.Planets[0].Expected = nil }, "missing read-only planet": func(i *CommandRecords) { i.Planets = i.Planets[:1] }, "duplicate planet": func(i *CommandRecords) { i.Planets = append(i.Planets, i.Planets[0]) },
		"planet identity": func(i *CommandRecords) { i.Planets[0].Proposed.PlanetNum = 1 }, "planet outside star": func(i *CommandRecords) { i.Planets[0].Expected.StarNum = 1 }, "unshared planet": func(i *CommandRecords) { i.Planets[1].Proposed.Info[1].Explored = true }, "geometry": func(i *CommandRecords) { i.Planets[0].Expected.Sectors = nil },
		"NaN": func(i *CommandRecords) { i.Stars[0].Expected.XPos = math.NaN(); i.Stars[0].Proposed.XPos = math.NaN() },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s, _, in := commandFixture(t)
			before := enrollmentSnapshot(t, s)
			change(&in)
			out, e := s.CommitCommandRecords(in)
			assertCommandError(t, out, e, CommandInvalid, CommandNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("invalid proposal wrote records")
			}
		})
	}
}

func TestCommandRecordsStalePreimages(t *testing.T) {
	for _, kind := range []string{"star AP", "star flag", "planet resource", "read-only planet", "missing planet", "noncanonical JSON"} {
		t.Run(kind, func(t *testing.T) {
			s, _, in := commandFixture(t)
			switch kind {
			case "star AP", "star flag":
				st := *in.Stars[0].Expected
				if kind == "star AP" {
					st.AP[0]--
				} else {
					st.Inhabited = 2
				}
				if e := s.PutStar(&st); e != nil {
					t.Fatal(e)
				}
			case "planet resource", "read-only planet":
				n := 0
				if kind == "read-only planet" {
					n = 1
				}
				p := *in.Planets[n].Expected
				p.Info[0].Resource++
				if e := s.PutPlanet(&p); e != nil {
					t.Fatal(e)
				}
			default:
				if e := s.db.Update(func(tx *bolt.Tx) error {
					b := tx.Bucket(BucketPlanets)
					key := PlanetKey(0, 0)
					if kind == "missing planet" {
						return b.Delete(key)
					}
					v := append([]byte(" \n"), b.Get(key)...)
					return b.Put(key, v)
				}); e != nil {
					t.Fatal(e)
				}
			}
			before := enrollmentSnapshot(t, s)
			reached := false
			s.SetMetaFault(func(op string) error {
				if op == "command.validated" {
					reached = true
				}
				return nil
			})
			out, e := s.CommitCommandRecords(in)
			assertCommandError(t, out, e, CommandStale, CommandNotCommitted)
			if reached || before != enrollmentSnapshot(t, s) {
				t.Fatal("stale precondition checked too late or overwrote records")
			}
		})
	}
}

func TestCommandRecordsOrbitalIntegrity(t *testing.T) {
	for _, kind := range []string{"missing", "cycle", "wrong orbit", "dead"} {
		t.Run(kind, func(t *testing.T) {
			s, _, in := commandFixture(t)
			sh, e := s.GetShip(1)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "missing":
				e = s.DeleteShip(1)
			case "cycle":
				sh.NextShip = 1
				e = s.PutShip(sh)
			case "wrong orbit":
				sh.PNumOrbits = 1
				e = s.PutShip(sh)
			case "dead":
				sh.Alive = false
				e = s.PutShip(sh)
			}
			if e != nil {
				t.Fatal(e)
			}
			before := enrollmentSnapshot(t, s)
			out, e := s.CommitCommandRecords(in)
			assertCommandError(t, out, e, CommandCorrupt, CommandNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("corrupt orbit allowed a write")
			}
		})
	}
}

func TestCommandRecordsFenceAndConcurrentConflict(t *testing.T) {
	t.Run("fence", func(t *testing.T) {
		s, _, in := commandFixture(t)
		before := enrollmentSnapshot(t, s)
		s.SetWriteFence(true)
		out, e := s.CommitCommandRecords(in)
		assertCommandError(t, out, e, CommandFenced, CommandNotCommitted)
		if !errors.Is(e, ErrWriteFenced) || s.FencedWrites() != 1 || s.LastFencedWrite() != "CommitCommandRecords" || before != enrollmentSnapshot(t, s) {
			t.Fatal("fence bypassed")
		}
	})
	t.Run("second check", func(t *testing.T) {
		s, _, in := commandFixture(t)
		before := enrollmentSnapshot(t, s)
		s.SetMetaFault(func(op string) error {
			if op == "command.before" {
				s.SetWriteFence(true)
			}
			return nil
		})
		out, e := s.CommitCommandRecords(in)
		assertCommandError(t, out, e, CommandFenced, CommandNotCommitted)
		if s.FencedWrites() != 1 || before != enrollmentSnapshot(t, s) {
			t.Fatal("transaction fence not enforced")
		}
	})
	t.Run("one handle", func(t *testing.T) {
		s, _, in := commandFixture(t)
		var wg sync.WaitGroup
		out := make(chan CommandOutcome, 2)
		errs := make(chan error, 2)
		for n := 0; n < 2; n++ {
			wg.Add(1)
			go func() { defer wg.Done(); o, e := s.CommitCommandRecords(in); out <- o; errs <- e }()
		}
		wg.Wait()
		close(out)
		close(errs)
		commits, aborts := 0, 0
		for o := range out {
			switch o {
			case CommandCommitted:
				commits++
			case CommandNotCommitted:
				aborts++
			}
		}
		if commits != 1 || aborts != 1 {
			t.Fatal("concurrent stale proposals both committed")
		}
		for e := range errs {
			if e != nil {
				var ce *CommandError
				if !errors.As(e, &ce) || ce.Code != CommandStale {
					t.Fatal(e)
				}
			}
		}
		assertCommandRecords(t, s, in, true)
	})
}

func TestCommandRecordsFinalPreconditionInsideTransaction(t *testing.T) {
	s, _, in := commandFixture(t)
	var afterForeignWrite string
	reached := false
	s.SetMetaFault(func(op string) error {
		if op == "command.before" {
			// This is after proposal serialization and before Update opens. A real
			// intervening write invalidates the ticket; a preflight-only check loses it.
			p := *in.Planets[1].Expected
			p.Info[0].Resource++
			if e := s.PutPlanet(&p); e != nil {
				t.Error(e)
			}
			afterForeignWrite = enrollmentSnapshot(t, s)
			reached = true
		}
		return nil
	})
	out, e := s.CommitCommandRecords(in)
	assertCommandError(t, out, e, CommandStale, CommandNotCommitted)
	if !reached || afterForeignWrite == "" || afterForeignWrite != enrollmentSnapshot(t, s) {
		t.Fatal("final transactional precondition overwrote intervening change")
	}
}

func TestCommandRecordsCompetingAdaptersUseOneTransaction(t *testing.T) {
	s, _, in := commandFixture(t)
	// Same bbolt handle, separate adapter mutex: the transaction is the final
	// authority, even without the actor's usual serialization. No second DB opens.
	adapter := &Store{db: s.db}
	var wg sync.WaitGroup
	start := make(chan struct{})
	outcomes := make(chan CommandOutcome, 2)
	for _, store := range []*Store{s, adapter} {
		wg.Add(1)
		go func(store *Store) {
			defer wg.Done()
			<-start
			o, e := store.CommitCommandRecords(in)
			if e != nil {
				var ce *CommandError
				if !errors.As(e, &ce) || ce.Code != CommandStale {
					t.Error("unexpected transaction result", e)
				}
			}
			outcomes <- o
		}(store)
	}
	close(start)
	wg.Wait()
	close(outcomes)
	commits, aborts := 0, 0
	for o := range outcomes {
		switch o {
		case CommandCommitted:
			commits++
		case CommandNotCommitted:
			aborts++
		}
	}
	if commits != 1 || aborts != 1 {
		t.Fatal("atomic preconditions lost a concurrent conflict")
	}
	assertCommandRecords(t, s, in, true)
}
