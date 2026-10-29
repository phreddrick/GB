package game

// CR-27 regression coverage at the combat call site.
//
// C (shootblast.c, shoot_ship_to_ship): hits = strength * CEW_hit(dist, range).
// Go (combat.go, shootShipToShip):      hits = cewHit(dist, range).
//
// The golden fixtures cover cew_hit_odds and CEW_hit themselves; this test
// drives the call site deterministically: the target is exactly at CEW range
// (odds 100, so CEW_hit is 1 without consulting the RNG), the attacker fires in
// radiation mode so the damage path is doRadiation, the target has no armour
// (arm 0, so every hit penetrates regardless of the RNG) and a body of 40 (so
// the dose 40*penetrate/body is integral and roundRand returns it exactly).
// The observable, RNG-free result is the returned dose and the "Hits: n" line:
// with C semantics both equal strength; Go currently yields 1 for any strength.
// The reference values come from the C formula at the call site, not from a
// generated fixture (the harness cannot run shoot_ship_to_ship without the
// whole server state); that limitation is recorded in test/ctool/README.md.

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/testutil"
)

func TestCEWCallSite(t *testing.T) {
	const cewRange = 300
	for _, strength := range []int{1, 2, 5, 20} {
		st := goldenState()
		st.stars = []*model.Star{{Name: "Sol"}}
		from := &model.Ship{Number: 1, Owner: 1, Type: model.StypeCruiser, Alive: true, Active: true, On: true,
			WhatOrbits: model.LevelStar, StOrbits: 0, XPos: 0, YPos: 0, Tech: 99, Mode: true,
			CEW: 50, CEWRange: cewRange, Guns: model.GunPrimary, PrimType: 2}
		to := &model.Ship{Number: 2, Owner: 2, Type: model.StypeCruiser, Alive: true, Active: true,
			WhatOrbits: model.LevelStar, StOrbits: 0, XPos: cewRange, YPos: 0, Tech: 1,
			Armor: 0, Size: 40, MaxHanger: 0}
		var long, short string
		dose := st.shootShipToShip(from, to, strength, 1, 0, &long, &short)
		wantC := strength // C: strength * CEW_hit, with CEW_hit == 1 at exact range
		key := fmt.Sprintf("strength=%d;dist=%d;cew_range=%d;armor=0;body=40", strength, cewRange, cewRange)
		got := strconv.Itoa(dose)
		hitsLine := fmt.Sprintf("Hits: %d\n", dose)
		matched := dose == wantC && strings.Contains(long, hitsLine)
		testutil.CheckCase(t, "combat/cew-strength-multiplier", key, matched, got,
			fmt.Sprintf("dose/hits: go=%d c=%d (long message: %q)", dose, wantC, strings.TrimSpace(long)))
	}
	testutil.Finish(t, "TestCEWCallSite")
}
