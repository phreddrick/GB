package commandrules

import (
	"encoding/json"
	"errors"
	"github.com/phreddrick/GB/go/internal/model"
	"math"
	"reflect"
	"testing"
)

func assaultRuleFixture(military bool) AssaultInput {
	a := &model.Race{PlayerNum: 1, Name: "attacker", Fighters: 10}
	a.Governor[0].Active = true
	a.Likes[1] = 1
	d := &model.Race{PlayerNum: 2, Name: "defender", Fighters: 1}
	d.Governor[0].Active = true
	d.Likes[1] = 1
	p := &model.Planet{MaxX: 3, MaxY: 2, Sectors: make([]model.Sector, 6)}
	for i := range p.Sectors {
		p.Sectors[i].Condition = 1
		p.Sectors[i].Type = 1
	}
	p.Sectors[0] = model.Sector{Condition: 1, Type: 1, Owner: 1, Race: 1, Mobilization: 3, Popn: 1000}
	if military {
		p.Sectors[0].Popn = 0
		p.Sectors[0].Troops = 1000
	}
	p.Sectors[1] = model.Sector{Condition: 1, Type: 1, Owner: 2, Race: 2, Mobilization: 7, Popn: 1}
	assaultRuleTotals(p)
	st := &model.Star{Name: "Home", NumPlanets: 1}
	st.AP[0] = 100
	st.PlanetNames[0] = "World"
	return AssaultInput{Player: 1, Scope: model.Directory{Level: model.LevelPlan}, Request: AssaultRequest{Direction: '6', Quantity: 1000, Military: military, MoveFactor: 1}, Planet: p, Star: st, Attacker: a, Defender: d}
}
func assaultRuleTotals(p *model.Planet) {
	v, pop, mil, e := assaultTotals(p)
	if e != nil {
		panic(e)
	}
	p.Popn = pop
	p.Troops = mil
	for i, n := range v {
		p.Info[i].Popn = n.Popn
		p.Info[i].Troops = n.Troops
		p.Info[i].NumSectsOwned = n.NumSectsOwned
		p.Info[i].MobPoints = n.MobPoints
	}
}
func assaultRuleRun(in AssaultInput, high bool) (AssaultChange, []AssaultDraw, error) {
	var tape []AssaultDraw
	for {
		c, e := PlanAssault(in, tape)
		if e == nil {
			return c, tape, nil
		}
		var need *AssaultNeedDraw
		if !errors.As(e, &need) {
			return c, tape, e
		}
		n := need.Draw
		if n.Kind == "round" {
			n.Fraction = float64(2147483647) / 2147483648
		} else if high {
			n.Integer = n.Bound
		}
		tape = append(tape, n)
	}
}
func TestAssaultRulesDefinedCAndApprovedOutcomes(t *testing.T) {
	for _, mil := range []bool{false, true} {
		t.Run(map[bool]string{false: "civilian", true: "military"}[mil], func(t *testing.T) {
			in := assaultRuleFixture(mil)
			before, _ := json.Marshal(in)
			c, tape, e := assaultRuleRun(in, true)
			if e != nil {
				t.Fatal(e)
			}
			if c.Outcome != "VICTORY" || c.Planet.Sectors[1].Owner != 1 || c.AttackerLoss != 0 || c.CivilianLoss != 1 || c.Attacker.Morale != 1 || c.Defender.Morale != -1 || c.Attacker.Points[2] != 1 || c.Attacker.Translate[1] != 5 || c.Defender.Translate[0] != 5 {
				t.Fatal("defined C victory", c)
			}
			want := uint8(92)
			if mil {
				want = 95
			}
			if c.Star.AP[0] != want || c.Planet.Sectors[0].Owner != 0 || c.Planet.Info[0].NumSectsOwned != 1 || c.Planet.Info[1].NumSectsOwned != 0 || c.Planet.Info[0].MobPoints != 7 || c.Planet.Info[1].MobPoints != 0 {
				t.Fatal("approved accounting", c.Planet)
			}
			if mil {
				if c.Planet.Popn != 0 || c.Planet.Troops != 1000 {
					t.Fatal(c.Planet)
				}
			} else if c.Planet.Popn != 1000 || c.Planet.Troops != 0 {
				t.Fatal(c.Planet)
			}
			after, _ := json.Marshal(in)
			if string(after) != string(before) {
				t.Fatal("mutated input")
			}
			replay, e := PlanAssault(in, tape)
			if e != nil || !reflect.DeepEqual(c, replay) {
				t.Fatal("replay", e)
			}
		})
	}
	for _, kind := range []string{"mutual", "empty", "repulse", "partial", "absorb-cap", "defender-absorb", "no-resurrection"} {
		t.Run(kind, func(t *testing.T) {
			in := assaultRuleFixture(false)
			p := in.Planet
			switch kind {
			case "mutual", "no-resurrection":
				in.Request.Quantity = 1
				p.Sectors[0].Popn = 1
				in.Attacker.Fighters = 1
				if kind == "no-resurrection" {
					in.Attacker.Absorb = true
					in.Defender.Absorb = true
				}
			case "empty":
				p.Sectors[1].Popn = 0
			case "repulse", "defender-absorb":
				in.Attacker.Fighters = 1
				in.Defender.Fighters = 255
				p.Sectors[0].Popn = 10
				in.Request.Quantity = 10
				p.Sectors[1].Popn = 1000
				in.Defender.Absorb = kind == "defender-absorb"
			case "partial":
				in.Attacker.Fighters = 1
				p.Sectors[1].Popn = 1000
			case "absorb-cap":
				p.Sectors[0].Popn = math.MaxUint16
				in.Request.Quantity = math.MaxUint16
				p.Sectors[1].Popn = 60000
				in.Attacker.Fighters = 255
				in.Attacker.Absorb = true
			}
			assaultRuleTotals(p)
			c, tape, e := assaultRuleRun(in, true)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "mutual", "no-resurrection":
				if c.Outcome != "MUTUAL DESTRUCTION" || c.Planet.Sectors[1].Owner != 0 || c.Attacker.Morale != 0 || c.Defender.Morale != 0 || c.Absorbed != 0 || c.Planet.Popn != 0 || c.Planet.Info[1].NumSectsOwned != 0 {
					t.Fatal("mutual", c)
				}
			case "empty":
				if len(tape) != 0 || c.Planet.Sectors[1].Owner != 1 || c.CivilianLoss != 0 || c.AttackerLoss != 0 {
					t.Fatal("empty", c)
				}
			case "repulse", "defender-absorb":
				if c.Outcome != "REPULSE" || c.Planet.Sectors[1].Owner != 2 || c.AttackerLoss != 10 || c.Attacker.Morale != -1 || c.Defender.Morale != 1 {
					t.Fatal("repulse", c)
				}
				if kind == "defender-absorb" && c.Absorbed != 10 {
					t.Fatal("absorption", c)
				}
			case "partial":
				if c.AttackerLoss == 0 || c.CivilianLoss == 0 {
					t.Fatal("missing casualties", c)
				}
			case "absorb-cap":
				if c.Outcome != "VICTORY" || c.Absorbed != c.AttackerLoss || c.Planet.Sectors[1].Popn != math.MaxUint16 {
					t.Fatal("capacity", c)
				}
			}
			want := *c.Planet
			assaultRuleTotals(&want)
			if !reflect.DeepEqual(want, *c.Planet) {
				t.Fatal("aggregate")
			}
		})
	}
}
func TestAssaultRulesRejectInvalidInputsAndTranscripts(t *testing.T) {
	for _, kind := range []string{"zero", "negative-like", "nan", "inf", "quantity", "ap", "governor", "inactive", "points-overflow", "morale-overflow", "zero-fighters", "accounting", "mob", "owner", "sector", "player64", "extra-draw", "bad-draw"} {
		t.Run(kind, func(t *testing.T) {
			in := assaultRuleFixture(false)
			switch kind {
			case "zero":
				in.Request.Quantity = 0
			case "negative-like":
				in.Attacker.Likes[1] = -1
			case "nan":
				in.Request.MoveFactor = math.NaN()
			case "inf":
				in.Attacker.Likes[1] = math.Inf(1)
			case "quantity":
				in.Request.Quantity = math.MaxUint32
			case "ap":
				in.Star.AP[0] = 0
			case "governor":
				in.Governor = 1
				in.Attacker.Governor[1].Active = true
				in.Star.Governor[0] = 2
			case "inactive":
				in.Attacker.Governor[0].Active = false
			case "points-overflow":
				in.Attacker.Points[2] = math.MaxUint32
			case "morale-overflow":
				in.Attacker.Morale = math.MaxInt64
			case "zero-fighters":
				in.Attacker.Fighters = 0
			case "accounting":
				in.Planet.Info[1].NumSectsOwned++
			case "mob":
				in.Planet.Info[0].MobPoints++
			case "owner":
				in.Planet.Sectors[0].Owner = 2
			case "sector":
				in.Planet.Sectors[1].Condition = 255
			case "player64":
				in.Player = 64
				in.Attacker.PlayerNum = 64
			}
			_, tape, e := assaultRuleRun(in, true)
			if kind == "extra-draw" {
				tape = append(tape, AssaultDraw{Kind: "integer", Bound: 1})
				_, e = PlanAssault(in, tape)
			}
			if kind == "bad-draw" {
				tape[0].Fraction = 1
				_, e = PlanAssault(in, tape)
			}
			if e == nil {
				t.Fatal("invalid accepted")
			}
		})
	}
}

func TestAssaultMovementAPBoundaries(t *testing.T) {
	amounts := []uint16{1, 9, 99, 999, 9999, 65535}
	civ := []int{2, 4, 6, 8, 11, 13}
	mil := []int{2, 3, 4, 5, 6, 6}
	for _, military := range []bool{false, true} {
		for i, n := range amounts {
			in := assaultRuleFixture(military)
			in.Request.Quantity = uint32(n)
			if military {
				in.Planet.Sectors[0].Troops = n
			} else {
				in.Planet.Sectors[0].Popn = n
			}
			assaultRuleTotals(in.Planet)
			c, _, e := assaultRuleRun(in, true)
			want := civ[i]
			if military {
				want = mil[i]
			}
			if e != nil || c.APCost != want || int(c.Star.AP[0]) != 100-want {
				t.Fatal("C log/log10 boundary", military, n, c.APCost, want, e)
			}
		}
	}
}

func TestAssaultPartialAndMutualBothUnits(t *testing.T) {
	for _, military := range []bool{false, true} {
		for _, mutual := range []bool{false, true} {
			in := assaultRuleFixture(military)
			in.Attacker.Fighters = 1
			in.Attacker.Absorb = mutual
			in.Defender.Absorb = mutual
			n := uint16(1000)
			if mutual {
				n = 1
			}
			in.Request.Quantity = uint32(n)
			in.Planet.Sectors[0].Popn = 0
			in.Planet.Sectors[0].Troops = 0
			in.Planet.Sectors[1].Popn = 0
			in.Planet.Sectors[1].Troops = 0
			if military {
				in.Planet.Sectors[0].Troops = n
				in.Planet.Sectors[1].Troops = n
			} else {
				in.Planet.Sectors[0].Popn = n
				in.Planet.Sectors[1].Popn = n
			}
			assaultRuleTotals(in.Planet)
			var draws []AssaultDraw
			var c AssaultChange
			for {
				v, e := PlanAssault(in, draws)
				if e == nil {
					c = v
					break
				}
				var need *AssaultNeedDraw
				if !errors.As(e, &need) {
					t.Fatal(e)
				}
				d := need.Draw
				if d.Kind == "round" {
					d.Fraction = 0.999999
				} else {
					d.Integer = d.Bound
					if !mutual {
						d.Integer /= 2
					}
				}
				draws = append(draws, d)
			}
			if mutual {
				if c.Outcome != "MUTUAL DESTRUCTION" || c.Absorbed != 0 || c.Planet.Sectors[0].Owner != 0 || c.Planet.Sectors[1].Owner != 0 || c.Attacker.Morale != 0 || c.Defender.Morale != 0 || c.Planet.Popn+c.Planet.Troops != 0 {
					t.Fatal("both-unit mutual destruction/resurrection", military, c)
				}
			} else {
				if c.Outcome != "REPULSE" || c.AttackerLoss != 500 || c.CivilianLoss+c.MilitaryLoss != 500 || c.Planet.Popn+c.Planet.Troops != 1000 || c.Planet.Sectors[0].Owner != 1 || c.Planet.Sectors[1].Owner != 2 {
					t.Fatal("genuine partial casualties/retreat conservation", military, c)
				}
			}
		}
	}
}
