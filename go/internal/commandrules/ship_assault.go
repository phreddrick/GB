package commandrules

import (
	"encoding/json"
	"fmt"
	"github.com/phreddrick/GB/go/internal/model"
	"math"
	"reflect"
)

// ShipAssaultInput is a closed planet-root hostile unload, not sector movement.
// Star/AP and all root/alliance witnesses are immutable. Scope is actor-owned.
type ShipAssaultInput struct {
	Player, Governor   int
	Scope              model.Directory
	Request            PopulationRequest
	Ship               *model.Ship
	Planet             *model.Planet
	Star               *model.Star
	Attacker, Defender *model.Race
	Ships              []*model.Ship
	Races              []*model.Race
}
type ShipAssaultChange struct {
	Ship                                                       *model.Ship
	Planet                                                     *model.Planet
	Attacker, Defender                                         *model.Race
	Outcome                                                    string
	AttackerLoss, CivilianLoss, MilitaryLoss, Absorbed, APCost int
	AttackStrength, DefenseStrength                            float64
}

func shipAssaultQuantity(in ShipAssaultInput) (int, error) {
	sh, p, r := in.Ship, in.Planet, in.Request
	if sh == nil || p == nil || !r.Unload || r.Amount < 0 || r.Amount > math.MaxInt32 || (r.Commodity != 'c' && r.Commodity != 'm') || sh.LandX >= p.MaxX || sh.LandY >= p.MaxY {
		return 0, ErrAssaultInput
	}
	if r.Amount > 0 && sh.Type == model.OtypeVN {
		return 0, ErrAssaultInput
	}
	available := int64(sh.Popn)
	target := int64(p.SectorAt(int(sh.LandX), int(sh.LandY)).Popn)
	if r.Commodity == 'm' {
		available = int64(sh.Troops)
		target = int64(p.SectorAt(int(sh.LandX), int(sh.LandY)).Troops)
	}
	// C checks the signed interval BEFORE expanding omitted/zero defaults.
	signed := -r.Amount
	if signed < -available || signed > min(target, populationCapacity(sh, r.Commodity == 'm')-available) {
		return 0, ErrAssaultInput
	}
	n := r.Amount
	if n == 0 {
		n = available
	}
	if n == 0 || n > math.MaxUint16 {
		return 0, ErrAssaultInput
	}
	return int(n), nil
}

// Every onboard body contributes the authenticated owner's mass. This is a
// necessary floor, not a hull/cargo reconstruction or a repair of CR-13 domass.
// Permit only a bounded floating arithmetic error (operand ULPs), never clamp
// a negative value or accept a materially deficient preimage.
func shipAssaultMassEnough(mass, body float64, operands ...float64) bool {
	if !finite(mass) || mass < 0 || !finite(body) || body < 0 {
		return false
	}
	tolerance := 0.0
	for _, v := range append([]float64{mass, body}, operands...) {
		if !finite(v) || v < 0 {
			return false
		}
		ulp := v - math.Nextafter(v, 0)
		if v == 0 {
			ulp = math.SmallestNonzeroFloat64
		}
		tolerance += ulp
	}
	return mass >= body || body-mass <= 4*tolerance
}

func ValidateShipAssault(in ShipAssaultInput) error {
	p, st, a, d, sh := in.Planet, in.Star, in.Attacker, in.Defender, in.Ship
	if p == nil || st == nil || a == nil || d == nil || sh == nil || in.Player < 1 || in.Player >= model.MaxPlayers || in.Governor < 0 || in.Governor > model.MaxGovernors || a.PlayerNum != in.Player || d.PlayerNum < 1 || d.PlayerNum >= model.MaxPlayers || d.PlayerNum == in.Player || a.Dissolved || d.Dissolved || !a.Governor[in.Governor].Active || in.Scope.StarNum != p.StarNum || in.Scope.PlanetNum != p.PlanetNum || st.Number != p.StarNum || p.PlanetNum >= st.NumPlanets || int(st.NumPlanets) > model.MaxPlanets || sh.Number == 0 || !sh.Alive || !sh.Active || !sh.Docked || int(sh.Owner) != in.Player || (in.Governor != 0 && int(sh.Governor) != in.Governor) || int(sh.Type) >= len(model.Shipdata) || sh.WhatOrbits != model.LevelPlan || sh.WhatDest != model.LevelPlan || sh.StOrbits != p.StarNum || sh.PNumOrbits != p.PlanetNum || sh.DestStar != p.StarNum || sh.DestPNum != p.PlanetNum || sh.LandX >= p.MaxX || sh.LandY >= p.MaxY || !finite(a.Mass) || a.Mass < 0 || !finite(sh.Mass) || sh.Mass < 0 || sh.Damage > 100 {
		return ErrAssaultInput
	}
	if sh.Type == model.OtypeTransDev && sh.On && (sh.Special.Transport == nil || sh.Special.Transport.Target != 0) {
		return ErrPopulationDeferred
	}
	infos, pop, troops, e := assaultTotals(p)
	if e != nil || p.Popn != pop || p.Troops != troops {
		return ErrAssaultInput
	}
	for i, v := range infos {
		b := p.Info[i]
		if v.Popn != b.Popn || v.Troops != b.Troops || v.NumSectsOwned != b.NumSectsOwned || v.MobPoints != b.MobPoints {
			return ErrAssaultInput
		}
	}
	originalBody := float64(uint32(sh.Popn)+uint32(sh.Troops)) * a.Mass
	quantity, e := shipAssaultQuantity(in)
	if e != nil || !shipAssaultMassEnough(sh.Mass, originalBody) || !finite(float64(quantity)*a.Mass) || sh.Mass < float64(quantity)*a.Mass {
		return ErrAssaultInput
	}
	x, y := int(sh.LandX), int(sh.LandY)
	to := p.SectorAt(x, y)
	if int(to.Owner) != d.PlayerNum || int(to.Condition) >= 9 || int(st.Governor[d.PlayerNum-1]) > model.MaxGovernors || !finite(d.Likes[to.Condition]) || d.Likes[to.Condition] < 0 || d.Likes[to.Condition] > 1 || (int(to.Popn)+int(to.Troops) > 0 && (a.Fighters == 0 || d.Fighters == 0)) {
		return ErrAssaultInput
	}
	races := map[int]*model.Race{a.PlayerNum: a, d.PlayerNum: d}
	for _, race := range in.Races {
		if race == nil || race.PlayerNum < 1 || race.PlayerNum > model.MaxPlayers || races[race.PlayerNum] != nil {
			return ErrAssaultInput
		}
		races[race.PlayerNum] = race
	}
	ships := map[uint32]*model.Ship{}
	for _, sh := range in.Ships {
		if sh == nil || sh.Number == 0 || ships[sh.Number] != nil || !sh.Alive || int(sh.Type) >= len(model.Shipdata) || sh.WhatOrbits != model.LevelPlan || sh.StOrbits != p.StarNum || sh.PNumOrbits != p.PlanetNum || sh.Owner == 0 || int(sh.Owner) > model.MaxPlayers {
			return ErrAssaultInput
		}
		ships[sh.Number] = sh
	}
	seen := map[uint32]bool{}
	for n := p.Ships; n != 0; {
		sh := ships[n]
		if sh == nil || seen[n] {
			return ErrAssaultInput
		}
		seen[n] = true
		race := races[int(sh.Owner)]
		if race == nil {
			return ErrAssaultInput
		}
		// Exactly the AFV predicates used by mechDefend/retalStrength; no volley,
		// alliance inference, automatic undock or random draw can escape staging.
		if sh.Type == model.OtypeAFV && int(sh.Owner) != in.Player && sh.Docked && int(sh.LandX) == x && int(sh.LandY) == y && sh.Popn > 0 && sh.Retaliate > 0 && sh.Destruct > 0 && ((sh.Guns == model.GunPrimary && sh.Primary > 0) || (sh.Guns == model.GunSecondary && sh.Secondary > 0)) && (!a.IsAllied(int(sh.Owner)) || !race.IsAllied(in.Player)) {
			return ErrAssaultMechanical
		}
		n = sh.NextShip
	}
	if len(seen) != len(ships) {
		return ErrAssaultInput
	}
	if !reflect.DeepEqual(ships[sh.Number], sh) {
		return ErrAssaultInput
	}
	return nil
}

// PlanShipAssault replays a bounded transcript; it never draws randomness.
func PlanShipAssault(in ShipAssaultInput, draws []AssaultDraw) (ShipAssaultChange, error) {
	var c ShipAssaultChange
	if e := ValidateShipAssault(in); e != nil {
		return c, e
	}
	p, a, d, r := in.Planet, in.Attacker, in.Defender, in.Request
	sh := in.Ship
	military := r.Commodity == 'm'
	x, y := int(sh.LandX), int(sh.LandY)
	next := *p
	next.Sectors = append([]model.Sector(nil), p.Sectors...)
	na, nd := *a, *d
	raw, e := json.Marshal(sh)
	if e != nil {
		return c, ErrAssaultInput
	}
	var selected model.Ship
	if json.Unmarshal(raw, &selected) != nil {
		return c, ErrAssaultInput
	}
	c.Ship = &selected
	c.Planet = &next
	c.Attacker = &na
	c.Defender = &nd
	to := next.SectorAt(x, y)
	oldCiv, oldMil := int(to.Popn), int(to.Troops)
	party, _ := shipAssaultQuantity(in)
	departing := party
	if party < 1 || party > math.MaxUint16 {
		return ShipAssaultChange{}, ErrAssaultInput
	}
	if military {
		selected.Troops -= uint16(party)
	} else {
		selected.Popn -= uint16(party)
	}
	tape := assaultTape{draws: draws}
	if oldCiv+oldMil > 0 {
		if (d.Morale > 0 && a.Morale < math.MinInt64+d.Morale) || (d.Morale < 0 && a.Morale > math.MaxInt64+d.Morale) {
			return ShipAssaultChange{}, ErrAssaultInput
		}
		diff := a.Morale - d.Morale
		if diff == math.MinInt64 {
			return ShipAssaultChange{}, ErrAssaultInput
		}
		defense := [9]int{1, 1, 3, 2, 2, 3, 2, 4, 0}
		mult := 1
		if military {
			mult = 10
		}
		// The original defined C morale_factor constant, not a changed probability.
		morale := func(v int64) float64 { return math.Atan(float64(v)/10000)/3.14159565 + 0.5 }
		c.AttackStrength = float64(party) * float64(a.Fighters) * float64(mult) * (2 - float64(sh.Damage)/100) * (float64(sh.Armor) + 1) * morale(diff)
		c.DefenseStrength = float64((oldCiv+oldMil*10)*int(d.Fighters)) * (d.Likes[to.Condition] + 1) * float64(defense[to.Condition]+1) * morale(-diff)
		if !finite(c.AttackStrength) || !finite(c.DefenseStrength) || c.AttackStrength <= 0 || c.DefenseStrength <= 0 {
			return ShipAssaultChange{}, ErrAssaultInput
		}
		scale := min(party*int(a.Fighters)*mult, (oldCiv+oldMil*10)*int(d.Fighters))
		var e error
		c.AttackerLoss, e = tape.casualty(float64(scale/mult)*c.DefenseStrength/c.AttackStrength, party)
		if e != nil {
			return ShipAssaultChange{}, e
		}
		c.CivilianLoss, e = tape.casualty(float64(scale)*c.AttackStrength/c.DefenseStrength, oldCiv)
		if e != nil {
			return ShipAssaultChange{}, e
		}
		c.MilitaryLoss, e = tape.casualty(float64(scale/10)*c.AttackStrength/c.DefenseStrength, oldMil)
		if e != nil {
			return ShipAssaultChange{}, e
		}
		party -= c.AttackerLoss
		if party < 0 || party > math.MaxUint16 {
			return ShipAssaultChange{}, ErrAssaultInput
		}
		civ, e := cargoWord(int64(c.CivilianLoss))
		if e != nil {
			return ShipAssaultChange{}, ErrAssaultInput
		}
		mil, e := cargoWord(int64(c.MilitaryLoss))
		if e != nil {
			return ShipAssaultChange{}, ErrAssaultInput
		}
		to.Popn -= civ
		to.Troops -= mil
	}
	// Outcome precedes absorption. No survivor can be manufactured to change it.
	switch {
	case to.Popn == 0 && to.Troops == 0 && party == 0:
		c.Outcome = "MUTUAL DESTRUCTION"
		to.Owner = 0
	case to.Popn == 0 && to.Troops == 0:
		c.Outcome = "VICTORY"
		if oldCiv+oldMil == 0 {
			c.Outcome = "UNOPPOSED OCCUPATION"
		}
		player := in.Player
		if player < 1 || player >= model.MaxPlayers {
			return ShipAssaultChange{}, ErrAssaultInput
		}
		to.Owner = uint8(player)
		if military {
			to.Troops = uint16(party)
		} else {
			to.Popn = uint16(party)
		}
		if a.Absorb {
			n, e := tape.integer(oldCiv + oldMil)
			if e != nil {
				return ShipAssaultChange{}, e
			}
			c.Absorbed = min(n, math.MaxUint16-int(to.Popn))
			word, e := cargoWord(int64(c.Absorbed))
			if e != nil {
				return ShipAssaultChange{}, ErrAssaultInput
			}
			to.Popn += word
		}
		if e := assaultMorale(&na, &nd, d.Fighters); e != nil {
			return ShipAssaultChange{}, e
		}
	default:
		c.Outcome = "REPULSE"
		if d.Absorb {
			n, e := tape.integer(c.AttackerLoss)
			if e != nil {
				return ShipAssaultChange{}, e
			}
			c.Absorbed = min(n, math.MaxUint16-int(to.Popn))
			word, e := cargoWord(int64(c.Absorbed))
			if e != nil {
				return ShipAssaultChange{}, ErrAssaultInput
			}
			to.Popn += word
		}
		if military {
			selected.Troops += uint16(party)
		} else {
			selected.Popn += uint16(party)
		}
		if e := assaultMorale(&nd, &na, a.Fighters); e != nil {
			return ShipAssaultChange{}, e
		}
	}
	if tape.used != len(draws) {
		return ShipAssaultChange{}, ErrAssaultInput
	}
	learn := func(v int8) int8 {
		if v >= 95 {
			return 100
		}
		return v + 5
	}
	na.Translate[d.PlayerNum-1] = learn(na.Translate[d.PlayerNum-1])
	nd.Translate[a.PlayerNum-1] = learn(nd.Translate[a.PlayerNum-1])
	// Ship load.c uses landing-sector residents plus the surviving party.
	// Remaining onboard passengers do not participate in this learning test.
	if int(to.Popn)+int(to.Troops)+party == 0 {
		na.Translate[d.PlayerNum-1] = learn(na.Translate[d.PlayerNum-1])
	}
	if party == 0 {
		nd.Translate[a.PlayerNum-1] = learn(nd.Translate[a.PlayerNum-1])
	}
	infos, pop, troops, e := assaultTotals(&next)
	if e != nil {
		return ShipAssaultChange{}, e
	}
	for i, v := range infos {
		next.Info[i].Popn = v.Popn
		next.Info[i].Troops = v.Troops
		next.Info[i].NumSectsOwned = v.NumSectsOwned
		next.Info[i].MobPoints = v.MobPoints
	}
	next.Popn = pop
	next.Troops = troops
	removed := departing
	if c.Outcome == "REPULSE" {
		removed = c.AttackerLoss
	}
	selected.Mass = sh.Mass - float64(removed)*a.Mass
	originalBody := float64(uint32(sh.Popn)+uint32(sh.Troops)) * a.Mass
	finalBody := float64(uint32(selected.Popn)+uint32(selected.Troops)) * a.Mass
	if !shipAssaultMassEnough(selected.Mass, finalBody, sh.Mass, originalBody, float64(removed)*a.Mass) {
		return ShipAssaultChange{}, ErrAssaultInput
	}

	return c, nil
}
func ShipAssaultText(in ShipAssaultInput, c ShipAssaultChange) (string, string) {
	x, y := int(in.Ship.LandX), int(in.Ship.LandY)
	unit := "civ"
	if in.Request.Commodity == 'm' {
		unit = "mil"
	}
	heading := fmt.Sprintf("/%s/%s: %s [%d] ship #%d assaults %s [%d] (%d,%d): %s\n", in.Star.Name, in.Star.PlanetNames[in.Planet.PlanetNum], in.Attacker.Name, in.Player, in.Ship.Number, in.Defender.Name, in.Defender.PlayerNum, x, y, c.Outcome)
	summary := fmt.Sprintf("Casualties: attacker %d %s; defender %d civ/%d mil. Absorbed: %d.\n", c.AttackerLoss, unit, c.CivilianLoss, c.MilitaryLoss, c.Absorbed)
	return heading + summary, heading + summary
}
