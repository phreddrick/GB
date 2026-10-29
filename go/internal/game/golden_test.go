package game

// Golden parity tests: compare Go functions against fixtures generated from
// the unmodified C reference by test/ctool/gb_golden. Each fixture is checked
// as a whole through testutil.Check, so a known divergence is listed once in
// test/knowndefects.yaml (XFAIL) and any other mismatch fails the test.
//
// Floating-point outputs are compared with a relative tolerance of 1e-9
// (libm versus Go's math package can differ in the last bits); integer
// outputs are compared exactly. Rows whose C result depends on the RNG are
// bracketed by two real C executions (see test/ctool/main.c, ap_add).

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/testutil"
)

const floatTol = 1e-9

// cmp is a per-case comparator bound to one inventory id and the input
// columns that identify a row. Every row is reported individually through
// testutil.CheckCase; nothing is aggregated into a single pass/fail.
type cmp struct {
	t      *testing.T
	id     string
	inputs []string
}

func (c cmp) ints(row testutil.Row, what string, got, want int) {
	c.t.Helper()
	testutil.CheckCase(c.t, c.id, testutil.CaseKey(row, c.inputs...), got == want,
		strconv.Itoa(got), testutil.Describe(what, got, want, row))
}

func (c cmp) floats(row testutil.Row, what string, got, want float64) {
	c.t.Helper()
	testutil.CheckCase(c.t, c.id, testutil.CaseKey(row, c.inputs...), testutil.FloatClose(got, want, floatTol),
		strconv.FormatFloat(got, 'g', 17, 64), testutil.Describe(what, got, want, row))
}

func (c cmp) within(row testutil.Row, what string, got, lo, hi int) {
	c.t.Helper()
	testutil.CheckCase(c.t, c.id, testutil.CaseKey(row, c.inputs...), got >= lo && got <= hi,
		strconv.Itoa(got), fmt.Sprintf("%s: go=%d outside C bracket [%d,%d] inputs=%v", what, got, lo, hi, row))
}

func goldenState() *State {
	return &State{
		cfg:   config.Default(),
		ships: map[uint32]*model.Ship{},
		races: make([]*model.Race, model.MaxPlayers),
	}
}

func TestGolden(t *testing.T) {
	t.Run("planet_guns", goldenPlanetGuns)
	t.Run("hit_odds", goldenHitOdds)
	t.Run("cew_hit_odds", goldenCEWHitOdds)
	t.Run("cew_hit", goldenCEWHit)
	t.Run("compatibility", goldenCompatibility)
	t.Run("max_support", goldenMaxSupport)
	t.Run("ship_design", goldenShipDesign)
	t.Run("ground_attack", goldenGroundAttack)
	t.Run("ap_add", goldenAPAdd)
	t.Run("produce", goldenProduce)
	testutil.Finish(t, "TestGolden")
}

func goldenPlanetGuns(t *testing.T) {
	c := cmp{t, "golden/planet_guns", []string{"points"}}
	for _, r := range testutil.LoadGolden(t, "planet_guns") {
		points := r.Int(t, "points")
		if points < 0 {
			t.Fatalf("fixture row with negative points: %v", r)
		}
		c.ints(r, "guns", int(planetGuns(uint64(points))), r.Int(t, "guns")) //nolint:gosec // points checked non-negative above
	}
}

func goldenHitOdds(t *testing.T) {
	in := []string{"range", "tech", "fdam", "fev", "tev", "fspeed", "tspeed", "body", "caliber", "defense"}
	odds := cmp{t, "golden/hit_odds/odds", in}
	factor := cmp{t, "golden/hit_odds/factor", in}
	for _, r := range testutil.LoadGolden(t, "hit_odds") {
		gotFactor := -1
		gotOdds := hitOdds(r.Float(t, "range"), &gotFactor, r.Float(t, "tech"),
			r.Int(t, "fdam"), r.Int(t, "fev"), r.Int(t, "tev"), r.Int(t, "fspeed"),
			r.Int(t, "tspeed"), r.Int(t, "body"), r.Int(t, "caliber"), r.Int(t, "defense"))
		odds.ints(r, "odds", gotOdds, r.Int(t, "odds"))
		factor.ints(r, "factor", gotFactor, r.Int(t, "factor"))
	}
}

func goldenCEWHitOdds(t *testing.T) {
	c := cmp{t, "golden/cew_hit_odds", []string{"dist", "cew_range"}}
	for _, r := range testutil.LoadGolden(t, "cew_hit_odds") {
		c.ints(r, "odds", cewHitOdds(r.Float(t, "dist"), r.Int(t, "cew_range")), r.Int(t, "odds"))
	}
}

func goldenCEWHit(t *testing.T) {
	c := cmp{t, "golden/cew_hit", []string{"dist", "cew_range"}}
	for _, r := range testutil.LoadGolden(t, "cew_hit") {
		c.ints(r, "hits", cewHit(r.Float(t, "dist"), r.Int(t, "cew_range")), r.Int(t, "hits"))
	}
}

func goldenCompatibility(t *testing.T) {
	in := []string{"p0", "p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "p10", "r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8", "r9"}
	c := cmp{t, "golden/compatibility", in}
	for _, r := range testutil.LoadGolden(t, "compatibility") {
		var p model.Planet
		var race model.Race
		for i := 0; i <= model.AtmosToxic; i++ {
			p.Conditions[i] = r.I16(t, fmt.Sprintf("p%d", i))
		}
		for i := 0; i <= model.AtmosOther; i++ {
			race.Conditions[i] = r.I16(t, fmt.Sprintf("r%d", i))
		}
		c.floats(r, "compat", compatibility(&p, &race), r.Float(t, "compat"))
	}
}

func goldenMaxSupport(t *testing.T) {
	c := cmp{t, "golden/max_support", []string{"likes", "eff", "fert", "condition", "compat", "toxic"}}
	for _, r := range testutil.LoadGolden(t, "max_support") {
		var race model.Race
		sec := model.Sector{Eff: r.U8(t, "eff"), Fert: r.U8(t, "fert"), Condition: r.U8(t, "condition")}
		race.Likes[sec.Condition] = r.Float(t, "likes")
		c.ints(r, "val", maxSupport(&race, &sec, r.Float(t, "compat"), r.Int(t, "toxic")), r.Int(t, "val"))
	}
}

func goldenShipDesign(t *testing.T) {
	st := goldenState()
	in := []string{"case", "type", "build_type", "primary", "primtype", "secondary", "sectype", "max_crew", "max_resource", "max_fuel", "max_destruct", "max_speed", "max_hanger", "armor", "hyper", "laser", "cloak", "mount", "cew", "cew_range"}
	size := cmp{t, "golden/ship_design/size", in}
	mass := cmp{t, "golden/ship_design/mass", in}
	cost := cmp{t, "golden/ship_design/cost", in}
	cplx := cmp{t, "golden/ship_design/complexity", in}
	for _, r := range testutil.LoadGolden(t, "ship_design") {
		ship := &model.Ship{
			Type:        model.ShipType(r.U8(t, "type")),
			BuildType:   r.U16(t, "build_type"),
			Primary:     r.U8(t, "primary"),
			PrimType:    r.U8(t, "primtype"),
			Secondary:   r.U8(t, "secondary"),
			SecType:     r.U8(t, "sectype"),
			MaxCrew:     r.U16(t, "max_crew"),
			MaxResource: r.U16(t, "max_resource"),
			MaxFuel:     r.U16(t, "max_fuel"),
			MaxDestruct: r.U16(t, "max_destruct"),
			MaxSpeed:    r.U16(t, "max_speed"),
			MaxHanger:   r.U16(t, "max_hanger"),
			Armor:       r.U8(t, "armor"),
			Laser:       r.U8(t, "laser"),
			Cloak:       r.U8(t, "cloak"),
			Mount:       r.U8(t, "mount"),
			CEW:         r.U8(t, "cew"),
			CEWRange:    r.U16(t, "cew_range"),
		}
		ship.HyperDrive.Has = r.Int(t, "hyper") != 0
		size.ints(r, "size", st.shipSize(ship), r.Int(t, "size"))
		ship.Size = r.U16(t, "size") // mass is defined on the C-computed size
		mass.floats(r, "mass", st.getMass(ship), r.Float(t, "mass"))
		cost.floats(r, "cost", st.shipCostFromDesign(ship), r.Float(t, "cost"))
		cplx.floats(r, "complexity", complexityOf(ship), r.Float(t, "complexity"))
	}
}

func goldenGroundAttack(t *testing.T) {
	in := []string{"people", "what", "civ", "mil", "def1", "def2", "alikes", "dlikes", "afighters", "amorale", "dfighters", "dmorale"}
	as := cmp{t, "golden/ground_attack/astrength", in}
	ds := cmp{t, "golden/ground_attack/dstrength", in}
	for _, r := range testutil.LoadGolden(t, "ground_attack") {
		race := &model.Race{Fighters: r.U8(t, "afighters"), Morale: int64(r.Int(t, "amorale"))}
		alien := &model.Race{Fighters: r.U8(t, "dfighters"), Morale: int64(r.Int(t, "dmorale"))}
		people := r.Int(t, "people")
		civ, mil := r.U16(t, "civ"), r.U16(t, "mil")
		var astr, dstr float64
		var c1, c2, c3 int
		groundAttack(race, alien, &people, r.Int(t, "what"), &civ, &mil,
			r.Int(t, "def1"), r.Int(t, "def2"), r.Float(t, "alikes"), r.Float(t, "dlikes"),
			&astr, &dstr, &c1, &c2, &c3)
		as.floats(r, "astrength", astr, r.Float(t, "astrength"))
		ds.floats(r, "dstrength", dstr, r.Float(t, "dstrength"))
	}
}

func goldenAPAdd(t *testing.T) {
	st := goldenState()
	st.ships[1] = &model.Ship{Alive: true, Docked: true, WhatDest: model.LevelPlan}
	c := cmp{t, "golden/ap_add", []string{"sh", "popn", "governed"}}
	for _, r := range testutil.LoadGolden(t, "ap_add") {
		race := &model.Race{}
		if r.Int(t, "governed") != 0 {
			race.GovShip = 1
		}
		c.within(r, "aps", st.apAdd(r.Int(t, "sh"), r.Int(t, "popn"), race), r.Int(t, "aps_lo"), r.Int(t, "aps_hi"))
	}
}

func goldenProduce(t *testing.T) {
	cols := []string{"eff_out", "condition_out", "popn_out", "owner_out", "avg_mob", "maintain", "prod_res", "prod_fuel", "prod_destruct", "prod_crystals"}
	in := []string{"condition", "type", "mob", "popn", "troops", "tax", "likes", "likes_plated", "metabolism", "birthrate", "sexes", "fertilize", "compat", "toxic"}
	cmps := map[string]cmp{}
	for _, col := range cols {
		cmps[col] = cmp{t, "golden/produce/" + col, in}
	}
	for _, r := range testutil.LoadGolden(t, "produce") {
		st := goldenState()
		cond := r.U8(t, "condition")
		race := &model.Race{PlayerNum: 1, Metabolism: r.Float(t, "metabolism"), BirthRate: r.Float(t, "birthrate"),
			NumberSexes: r.U8(t, "sexes"), Fertilize: r.U8(t, "fertilize")}
		race.Likes[cond] = r.Float(t, "likes")
		race.Likes[model.SectorPlated] = r.Float(t, "likes_plated")
		st.races[0] = race
		planet := &model.Planet{MaxX: 1, MaxY: 1, Sectors: make([]model.Sector, 1)}
		planet.Info[0].MobSet = r.U8(t, "mob")
		planet.Info[0].Tax = r.U8(t, "tax")
		planet.Info[0].Resource = 100
		planet.Conditions[model.AtmosToxic] = r.I16(t, "toxic")
		sec := &planet.Sectors[0]
		*sec = model.Sector{Owner: 1, Race: 1, Type: r.U8(t, "type"), Condition: cond, Eff: 100, Fert: 100,
			Mobilization: r.U8(t, "mob"), Popn: r.U16(t, "popn"), Troops: r.U16(t, "troops")}
		star := &model.Star{}
		compat := make([]float64, model.MaxPlayers)
		compat[0] = r.Float(t, "compat")
		accum := make([]prodAccum, model.MaxPlayers)
		st.produce(star, planet, sec, 0, 0, compat, accum)
		got := map[string]int{
			"eff_out": int(sec.Eff), "condition_out": int(sec.Condition), "popn_out": int(sec.Popn), "owner_out": int(sec.Owner),
			"avg_mob": int(accum[0].avgMob), "maintain": int(race.Governor[0].Maintain), //nolint:gosec // fixture-sized values
			"prod_res": int(accum[0].res), "prod_fuel": int(accum[0].fuel), "prod_destruct": int(accum[0].destruct), "prod_crystals": int(accum[0].crystals),
		}
		for _, col := range cols {
			cmps[col].ints(r, col, got[col], r.Int(t, col))
		}
	}
}
