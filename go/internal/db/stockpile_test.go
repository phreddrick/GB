package db

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/model"
)

func stockpileFixture(t *testing.T) (*Store, string, StockpileTransfer) {
	t.Helper()
	s, path, e := enrollmentFixture(t)
	p, st := e.ExpectedPlanet, e.ExpectedStar
	p.Info[0].Resource = 1000
	p.Info[1].Resource = 10
	st.AP[0] = 50
	r := &model.Race{PlayerNum: 1}
	r.Governor[0].Active = true
	target := &model.Race{PlayerNum: 2}
	for _, v := range []*model.Race{r, target} {
		if err := s.PutRace(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutPlanet(p); err != nil {
		t.Fatal(err)
	}
	if err := s.PutStar(st); err != nil {
		t.Fatal(err)
	}
	q := *p
	q.Info[0].Resource -= 7
	q.Info[1].Resource += 7
	next := *st
	next.AP[0]--
	return s, path, StockpileTransfer{Player: 1, Recipient: 2, Commodity: 'r', Amount: 7, Scope: model.Directory{Level: model.LevelPlan, StarNum: st.Number, PlanetNum: p.PlanetNum}, Planet: CommandPlanet{p, &q}, Star: CommandStar{Expected: st, Proposed: &next}, Caller: r, Target: target}
}
func assertStockpile(t *testing.T, s *Store, in StockpileTransfer, committed bool) {
	t.Helper()
	p, err := s.GetPlanet(in.Planet.Expected.StarNum, in.Planet.Expected.PlanetNum)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.GetStar(in.Star.Expected.Number)
	if err != nil {
		t.Fatal(err)
	}
	wantP, wantS := in.Planet.Expected, in.Star.Expected
	if committed {
		wantP, wantS = in.Planet.Proposed, in.Star.Proposed
	}
	if !reflect.DeepEqual(p, wantP) || !reflect.DeepEqual(st, wantS) {
		t.Fatal("partial or incorrect planet/AP record set")
	}
}
func TestStockpileContractCommitAndReopen(t *testing.T) {
	s, path, in := stockpileFixture(t)
	out, e := s.CommitStockpileTransfer(in)
	if e != nil || out != CommandCommitted {
		t.Fatal(out, e)
	}
	assertStockpile(t, s, in, true)
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	r, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = r.Close() }()
	assertStockpile(t, r, in, true)
}
func TestStockpileContractRollback(t *testing.T) {
	for _, stage := range []string{"stockpile.before", "stockpile.check:1", "stockpile.check:2", "stockpile.check:3", "stockpile.check:4", "stockpile.validated", "stockpile.write:1", "stockpile.write:2", "stockpile.before-commit"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				s, _, in := stockpileFixture(t)
				before := enrollmentSnapshot(t, s)
				reached := false
				s.SetMetaFault(func(op string) error {
					if op == stage {
						reached = true
						if panics {
							panic("private fault")
						}
						return errors.New("private fault")
					}
					return nil
				})
				out, e := s.CommitStockpileTransfer(in)
				if !reached || out != CommandNotCommitted || e == nil || enrollmentSnapshot(t, s) != before {
					t.Fatal("confirmed abort changed logical DB records", out, e)
				}
			})
		}
	}
}
func TestStockpileContractOutcomes(t *testing.T) {
	for _, stage := range []string{"stockpile.after-commit", "stockpile.commit-error"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				s, _, in := stockpileFixture(t)
				s.SetMetaFault(func(op string) error {
					if op == stage || op == "command.readback" {
						if panics {
							panic("private outcome fault")
						}
						return errors.New("private outcome fault")
					}
					return nil
				})
				out, e := s.CommitStockpileTransfer(in)
				want := CommandCommitted
				if stage == "stockpile.commit-error" {
					want = CommandUncertain
				}
				if out != want || e == nil {
					t.Fatal(out, e)
				}
				assertStockpile(t, s, in, true)
				if want == CommandUncertain {
					if !s.CommandWriteUncertain() {
						t.Fatal("handle guard lost")
					}
					s.SetMetaFault(nil)
					out, e = s.CommitStockpileTransfer(in)
					if out != CommandUncertain || e == nil {
						t.Fatal("uncertainty automatically retried")
					}
				}
			})
		}
	}
}
func TestStockpileContractRejectsUnsupportedEdits(t *testing.T) {
	cases := map[string]func(*StockpileTransfer){
		"unsupported share": func(x *StockpileTransfer) { x.Star.Share = true },
		"commodity":         func(x *StockpileTransfer) { x.Commodity = 'c' }, "amount": func(x *StockpileTransfer) { x.Amount = 1001 }, "scope": func(x *StockpileTransfer) { x.Scope.Level = model.LevelUniv }, "planet identity": func(x *StockpileTransfer) { x.Planet.Proposed.PlanetNum++ }, "unrelated planet": func(x *StockpileTransfer) { x.Planet.Proposed.Popn++ }, "sector alias": func(x *StockpileTransfer) {
			x.Planet.Proposed.Sectors = append([]model.Sector(nil), x.Planet.Proposed.Sectors...)
			x.Planet.Proposed.Sectors[0].Fert++
		}, "extra AP": func(x *StockpileTransfer) { x.Star.Proposed.AP[0]-- }, "other AP": func(x *StockpileTransfer) { x.Star.Proposed.AP[1]++ }, "unrelated star": func(x *StockpileTransfer) { x.Star.Proposed.Ships++ }, "caller identity": func(x *StockpileTransfer) { x.Player = 2 }, "governor": func(x *StockpileTransfer) { x.Governor = 1 }, "target identity": func(x *StockpileTransfer) { x.Recipient = 3 },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			s, _, in := stockpileFixture(t)
			edit(&in)
			before := enrollmentSnapshot(t, s)
			out, e := s.CommitStockpileTransfer(in)
			if out != CommandNotCommitted || e == nil || before != enrollmentSnapshot(t, s) {
				t.Fatal("unsupported edit accepted", out, e)
			}
		})
	}
}
func TestStockpileContractFinalPreimages(t *testing.T) {
	for _, record := range []string{"planet", "star", "caller", "target"} {
		t.Run(record, func(t *testing.T) {
			s, _, in := stockpileFixture(t)
			// An external writer intervenes AFTER preparation, before opening the
			// transaction. Only the checks inside Update can detect this conflict.
			s.SetMetaFault(func(op string) error {
				if op != "stockpile.before" {
					return nil
				}
				switch record {
				case "planet":
					p := *in.Planet.Expected
					p.Info[2].Resource++
					return s.PutPlanet(&p)
				case "star":
					p := *in.Star.Expected
					p.AP[1]++
					return s.PutStar(&p)
				case "caller":
					p := *in.Caller
					p.Morale++
					return s.PutRace(&p)
				default:
					p := *in.Target
					p.Morale++
					return s.PutRace(&p)
				}
			})
			out, e := s.CommitStockpileTransfer(in)
			var ce *CommandError
			if out != CommandNotCommitted || !errors.As(e, &ce) || ce.Code != CommandStale {
				t.Fatal(out, e)
			}
			p, _ := s.GetPlanet(in.Planet.Expected.StarNum, in.Planet.Expected.PlanetNum)
			st, _ := s.GetStar(in.Star.Expected.Number)
			if p.Info[0].Resource != 1000 || p.Info[1].Resource != 10 || st.AP[0] != 50 {
				t.Fatal("stale preimage changed transfer/AP")
			}
		})
	}
}
func TestStockpileContractFenceAndMaintenance(t *testing.T) {
	s, _, in := stockpileFixture(t)
	before := enrollmentSnapshot(t, s)
	s.SetWriteFence(true)
	out, e := s.CommitStockpileTransfer(in)
	s.SetWriteFence(false)
	if out != CommandNotCommitted || !errors.Is(e, ErrWriteFenced) || enrollmentSnapshot(t, s) != before {
		t.Fatal(out, e)
	}
	if e := s.PutMaintenance(&MaintenanceRecord{SchemaVersion: MaintenanceSchemaVersion, Quiesced: true, SetAt: time.Now().UTC(), Reason: "test"}); e != nil {
		t.Fatal(e)
	}
	before = enrollmentSnapshot(t, s)
	out, e = s.CommitStockpileTransfer(in)
	if out != CommandNotCommitted || e == nil || enrollmentSnapshot(t, s) != before {
		t.Fatal("durable maintenance bypass", out, e)
	}
}
