// Exact expectations independently derived during source review.
package commandrules

import (
	"errors"
	"github.com/phreddrick/GB/go/internal/model"
	"math"
	"reflect"
	"testing"
)

func shipRuleFixture() ShipAssaultInput {
	g := assaultRuleFixture(false)
	g.Attacker.Fighters = 1
	g.Attacker.Mass = 2
	g.Defender.Fighters = 1
	g.Planet.Sectors[1].Popn = 100
	assaultRuleTotals(g.Planet)
	sh := &model.Ship{Number: 77, Owner: 1, Alive: true, Active: true, Docked: true, WhatOrbits: model.LevelPlan, WhatDest: model.LevelPlan, LandX: 1, MaxCrew: 400, Popn: 200, Mass: 700, Armor: 1}
	g.Planet.Ships = 77
	return ShipAssaultInput{Player: 1, Scope: g.Scope, Request: PopulationRequest{Commodity: 'c', Unload: true, Amount: 100}, Ship: sh, Planet: g.Planet, Star: g.Star, Attacker: g.Attacker, Defender: g.Defender, Ships: []*model.Ship{sh}}
}
func shipRuleRun(t *testing.T, in ShipAssaultInput, half bool) ShipAssaultChange {
	t.Helper()
	var tape []AssaultDraw
	for {
		c, e := PlanShipAssault(in, tape)
		if e == nil {
			return c
		}
		var n *AssaultNeedDraw
		if !errors.As(e, &n) {
			t.Fatal(e)
		}
		v := n.Draw
		if v.Kind == "round" {
			v.Fraction = .999999
		} else if half {
			v.Integer = v.Bound / 2
		}
		tape = append(tape, v)
	}
}
func TestShipAssaultExactCSemantics(t *testing.T) {
	for _, tc := range []struct {
		name          string
		armor, damage uint8
		as            float64
		al, dl        int
		crew          uint16
		mass          float64
		planetPop     uint64
		ownerPop      uint32
	}{{"C partial", 1, 0, 200, 50, 50, 150, 600, 1050, 50}, {"armor damage", 4, 50, 375, 26, 93, 174, 648, 1007, 7}} {
		t.Run(tc.name, func(t *testing.T) {
			in := shipRuleFixture()
			in.Ship.Armor = tc.armor
			in.Ship.Damage = tc.damage
			c := shipRuleRun(t, in, true)
			if c.AttackStrength != tc.as || c.DefenseStrength != 200 || c.AttackerLoss != tc.al || c.CivilianLoss != tc.dl || c.MilitaryLoss != 0 || c.Ship.Popn != tc.crew || c.Ship.Mass != tc.mass || c.Outcome != "REPULSE" {
				t.Fatalf("strength/casualty/refund: %+v ship=%+v", c, c.Ship)
			}
			if c.Attacker.Morale != -1 || c.Defender.Morale != 1 || c.Defender.Points[1] != 1 || c.Attacker.Translate[1] != 5 || c.Defender.Translate[0] != 5 || c.Planet.Info[0].NumSectsOwned != 1 || c.Planet.Info[1].NumSectsOwned != 1 || c.Planet.Info[0].MobPoints != 3 || c.Planet.Info[1].MobPoints != 7 || c.Planet.Popn != tc.planetPop || c.Planet.Info[1].Popn != tc.ownerPop {
				t.Fatalf("accounting/races mismatch %+v", c)
			}
			if in.Ship.Popn != 200 || in.Ship.Mass != 700 || in.Planet.Sectors[1].Popn != 100 {
				t.Fatal("input alias mutated")
			}
		})
	}
	in := shipRuleFixture()
	in.Ship.Popn = 100
	in.Ship.Mass = 210
	c := shipRuleRun(t, in, false)
	if c.Ship.Mass != 210 || c.Ship.Popn != 100 || c.AttackerLoss != 0 {
		t.Fatal("C negative-mass regression", c)
	}
	in = shipRuleFixture()
	in.Ship.MaxCrew = 100
	in.Request.Amount = 0
	if _, e := PlanShipAssault(in, nil); !errors.Is(e, ErrAssaultInput) {
		t.Fatal("overfull default allowed")
	}
	in.Request.Amount = 100
	if e := ValidateShipAssault(in); e != nil {
		t.Fatal("valid explicit C bound-before-default refused", e)
	}
	in = shipRuleFixture()
	in.Scope.Level = model.LevelUniv
	in.Governor = 1
	in.Ship.Governor = 1
	in.Attacker.Governor[1].Active = true
	in.Star.Governor[0] = 2
	in.Planet.SlavedTo = 2
	if e := ValidateShipAssault(in); e != nil {
		t.Fatal("new movement-only authority added", e)
	}
	in = shipRuleFixture()
	before := *in.Star
	c = shipRuleRun(t, in, true)
	if !reflect.DeepEqual(*in.Star, before) || c.APCost != 0 {
		t.Fatal("AP changed")
	}
}

func TestShipAssaultAbsorptionAndNumericBounds(t *testing.T) {
	for _, mil := range []bool{false, true} {
		in := shipRuleFixture()
		in.Attacker.Absorb = true
		in.Attacker.Fighters = 255
		in.Ship.Popn = 65535
		in.Ship.MaxCrew = 65535
		in.Ship.Mass = 200000
		in.Planet.Sectors[1].Popn = 65535
		in.Planet.Sectors[1].Troops = 1
		in.Request.Amount = 65535
		if mil {
			in.Ship.Popn = 0
			in.Ship.Troops = 65535
			in.Request.Commodity = 'm'
		}
		assaultRuleTotals(in.Planet)
		var tape []AssaultDraw
		var c ShipAssaultChange
		for {
			var e error
			c, e = PlanShipAssault(in, tape)
			if e == nil {
				break
			}
			var need *AssaultNeedDraw
			if !errors.As(e, &need) {
				t.Fatal(e)
			}
			v := need.Draw
			if v.Kind == "round" {
				v.Fraction = .999999
			} else {
				v.Integer = v.Bound
			}
			tape = append(tape, v)
		}
		if c.Outcome != "VICTORY" || tape[len(tape)-1].Bound != 65536 || c.Planet.Sectors[1].Popn != 65535 || (mil && c.Absorbed != 65535) || (!mil && c.Absorbed != c.AttackerLoss) {
			t.Fatal("body pool or capacity saturation incorrect", mil, c.Absorbed, c.AttackerLoss)
		}
		if c.Ship.Mass != 68930 || c.Ship.Popn != 0 || c.Ship.Troops != 0 {
			t.Fatal("absorbed sector bodies changed ship mass/complement")
		}
	}
	for _, mass := range []float64{math.NaN(), math.Inf(1), -1} {
		in := shipRuleFixture()
		in.Attacker.Mass = mass
		if ValidateShipAssault(in) == nil {
			t.Fatal("invalid race mass accepted")
		}
	}
	in := shipRuleFixture()
	in.Ship.Mass = 199
	if ValidateShipAssault(in) == nil {
		t.Fatal("insufficient departing mass accepted")
	}
}

func TestShipAssaultExactVictory(t *testing.T) {
	for _, mil := range []bool{false, true} {
		in := shipRuleFixture()
		in.Attacker.Fighters = 10
		in.Planet.Sectors[1].Popn = 1
		assaultRuleTotals(in.Planet)
		if mil {
			in.Ship.Popn = 0
			in.Ship.Troops = 200
			in.Request.Commodity = 'm'
		}
		var tape []AssaultDraw
		var c ShipAssaultChange
		for {
			var e error
			c, e = PlanShipAssault(in, tape)
			if e == nil {
				break
			}
			var need *AssaultNeedDraw
			if !errors.As(e, &need) {
				t.Fatal(e)
			}
			v := need.Draw
			if v.Kind == "round" {
				v.Fraction = .999999
			} else {
				v.Integer = v.Bound
			}
			tape = append(tape, v)
		}
		wantPop, wantTroops := uint64(1100), uint64(0)
		if mil {
			wantPop = 1000
			wantTroops = 100
		}
		if c.Outcome != "VICTORY" || c.AttackerLoss != 0 || c.CivilianLoss != 1 || c.Planet.Sectors[1].Owner != 1 || c.Planet.Popn != wantPop || c.Planet.Troops != wantTroops || c.Planet.Info[0].Popn != uint32(wantPop) || c.Planet.Info[0].Troops != uint32(wantTroops) || c.Planet.Info[0].NumSectsOwned != 2 || c.Planet.Info[1].NumSectsOwned != 0 || c.Planet.Info[0].MobPoints != 10 || c.Planet.Info[1].MobPoints != 0 || c.Attacker.Morale != 1 || c.Defender.Morale != -1 || c.Attacker.Points[2] != 1 || c.Attacker.Translate[1] != 5 || c.Defender.Translate[0] != 5 || c.Ship.Mass != 500 {
			t.Fatal("defined C victory/accounting", mil, c)
		}
	}
}

func TestShipAssaultCompleteBodyMassFloor(t *testing.T) {
	for _, tc := range []struct {
		name        string
		pop, troops uint16
		mass, unit  float64
		quantity    int64
		valid       bool
	}{
		{"deficient preimage", 200, 0, 300, 2, 100, false},
		{"promoted sum", 40000, 40000, 30000, 1, 20000, false},
		{"fractional", 3, 0, .3, .1, 1, true},
		{"fractional near total", 3, 0, .3, .1, 2, true},
		{"material deficiency", 3, 0, .299, .1, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := shipRuleFixture()
			in.Ship.Popn = tc.pop
			in.Ship.Troops = tc.troops
			in.Ship.Mass = tc.mass
			in.Ship.MaxCrew = 65535
			in.Attacker.Mass = tc.unit
			in.Request.Amount = tc.quantity
			if !tc.valid {
				if ValidateShipAssault(in) == nil {
					t.Fatal("inconsistent complete body mass accepted")
				}
				return
			}
			c := shipRuleRun(t, in, false)
			if c.Ship.Popn != tc.pop || c.Ship.Troops != tc.troops || c.Ship.Mass != tc.mass {
				t.Fatal("no-loss fractional retreat changed mass/complement")
			}
			// An occupation actually debits mass and exercises fractional cancellation.
			in.Planet.Sectors[1].Popn = 0
			assaultRuleTotals(in.Planet)
			c = shipRuleRun(t, in, false)
			if c.Outcome != "UNOPPOSED OCCUPATION" || c.Ship.Mass != tc.mass-float64(tc.quantity)*tc.unit || c.Ship.Mass < 0 {
				t.Fatal("fractional departure mass")
			}
		})
	}
}
