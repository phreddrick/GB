package commandrules

import (
	"errors"
	"fmt"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
)

var ErrAssaultInput = errors.New("invalid assault state or request")
var ErrAssaultMechanical = errors.New("mechanical assault is deferred")

// AssaultDraw is an exact bounded transcript, not a seed or an RNG callback.
// Round uses Fraction; integer uses Bound (inclusive) and Integer. Unused fields
// must be zero. Storage replays this transcript without generating any draws.
type AssaultDraw struct {
	Kind           string
	Bound, Integer uint32
	Fraction       float64
}
type AssaultNeedDraw struct{ Draw AssaultDraw }

func (e *AssaultNeedDraw) Error() string { return "assault requires prepared random draw" }

type AssaultRequest struct {
	X, Y       int
	Direction  byte
	Quantity   uint32
	Military   bool
	MoveFactor float64
}
type AssaultInput struct {
	Player, Governor   int
	Scope              model.Directory
	Request            AssaultRequest
	Planet             *model.Planet
	Star               *model.Star
	Attacker, Defender *model.Race
	Ships              []*model.Ship
	Races              []*model.Race // immutable third-party mechanical/alliance witnesses
}
type AssaultChange struct {
	Planet                                                     *model.Planet
	Star                                                       *model.Star
	Attacker, Defender                                         *model.Race
	Outcome                                                    string
	AttackerLoss, CivilianLoss, MilitaryLoss, Absorbed, APCost int
	AttackStrength, DefenseStrength                            float64
}

// AssaultDestination preserves move.c's direction aliases and cylindrical map.
func AssaultDestination(p *model.Planet, r AssaultRequest) (int, int, bool) {
	if p == nil || p.MaxX == 0 || p.MaxY == 0 || r.X < 0 || r.Y < 0 || r.X >= int(p.MaxX) || r.Y >= int(p.MaxY) {
		return 0, 0, false
	}
	x, y := r.X, r.Y
	switch r.Direction {
	case '1', 'b':
		x--
		y++
	case '2', 'k':
		y++
	case '3', 'n':
		x++
		y++
	case '4', 'h':
		x--
	case '6', 'l':
		x++
	case '7', 'y':
		x--
		y--
	case '8', 'j':
		y--
	case '9', 'u':
		x++
		y--
	default:
		return 0, 0, false
	}
	if x < 0 {
		x = int(p.MaxX) - 1
	}
	if x == int(p.MaxX) {
		x = 0
	}
	return x, y, y >= 0 && y < int(p.MaxY) && (x != r.X || y != r.Y)
}

// assaultTotals rejects incoherent preimages; it never repairs other sectors.
func assaultTotals(p *model.Planet) ([model.MaxPlayers]model.PlInfo, uint64, uint64, error) {
	var infos [model.MaxPlayers]model.PlInfo
	var pop, troops uint64
	if p == nil || p.MaxX == 0 || p.MaxY == 0 || len(p.Sectors) != int(p.MaxX)*int(p.MaxY) {
		return infos, 0, 0, ErrAssaultInput
	}
	for _, s := range p.Sectors {
		if int(s.Owner) > model.MaxPlayers {
			return infos, 0, 0, ErrAssaultInput
		}
		pop += uint64(s.Popn)
		troops += uint64(s.Troops)
		if s.Owner != 0 {
			i := &infos[s.Owner-1]
			if uint64(i.Popn)+uint64(s.Popn) > math.MaxUint32 || uint64(i.Troops)+uint64(s.Troops) > math.MaxUint32 || i.NumSectsOwned == math.MaxUint16 || i.MobPoints > math.MaxUint64-uint64(s.Mobilization) {
				return infos, 0, 0, ErrAssaultInput
			}
			i.Popn += uint32(s.Popn)
			i.Troops += uint32(s.Troops)
			i.NumSectsOwned++
			i.MobPoints += uint64(s.Mobilization)
		}
	}
	return infos, pop, troops, nil
}

func ValidateAssault(in AssaultInput) error {
	p, st, a, d, r := in.Planet, in.Star, in.Attacker, in.Defender, in.Request
	if p == nil || st == nil || a == nil || d == nil || in.Player < 1 || in.Player >= model.MaxPlayers || in.Governor < 0 || in.Governor > model.MaxGovernors || a.PlayerNum != in.Player || d.PlayerNum < 1 || d.PlayerNum >= model.MaxPlayers || d.PlayerNum == in.Player || a.Dissolved || d.Dissolved || !a.Governor[in.Governor].Active || (in.Governor != 0 && int(st.Governor[in.Player-1]) != in.Governor) || in.Scope.Level != model.LevelPlan || in.Scope.StarNum != p.StarNum || in.Scope.PlanetNum != p.PlanetNum || st.Number != p.StarNum || p.PlanetNum >= st.NumPlanets || int(st.NumPlanets) > model.MaxPlanets || (p.SlavedTo != 0 && int(p.SlavedTo) != in.Player) || r.Quantity == 0 || r.Quantity > math.MaxUint16 || !finite(r.MoveFactor) || r.MoveFactor < 0 || r.MoveFactor > math.MaxInt32 {
		return ErrAssaultInput
	}
	x, y, ok := AssaultDestination(p, r)
	if !ok {
		return ErrAssaultInput
	}
	infos, pop, troops, e := assaultTotals(p)
	if e != nil {
		return e
	}
	if p.Popn != pop || p.Troops != troops {
		return ErrAssaultInput
	}
	for i, v := range infos {
		b := p.Info[i]
		if v.Popn != b.Popn || v.Troops != b.Troops || v.NumSectsOwned != b.NumSectsOwned || v.MobPoints != b.MobPoints {
			return ErrAssaultInput
		}
	}
	from, to := p.SectorAt(r.X, r.Y), p.SectorAt(x, y)
	if int(from.Owner) != in.Player || int(to.Owner) != d.PlayerNum || int(from.Condition) >= 9 || int(to.Condition) >= 9 || int(st.Governor[d.PlayerNum-1]) > model.MaxGovernors {
		return ErrAssaultInput
	}
	available := from.Popn
	if r.Military {
		available = from.Troops
	}
	if r.Quantity > uint32(available) {
		return ErrAssaultInput
	}
	for _, like := range []float64{a.Likes[from.Condition], d.Likes[to.Condition]} {
		if !finite(like) || like < 0 || like > 1 {
			return ErrAssaultInput
		}
	}
	if int(to.Popn)+int(to.Troops) > 0 && (a.Fighters == 0 || d.Fighters == 0) {
		return ErrAssaultInput
	}
	ap := assaultAP(r)
	if ap < 1 || ap > 255 || ap > int(st.AP[in.Player-1]) {
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
	return nil
}
func assaultAP(r AssaultRequest) int {
	v := math.Log1p(float64(r.Quantity))
	if r.Military {
		v = math.Log10(1 + float64(r.Quantity))
	}
	// Cast MoveFactor before multiplication, as the existing C-derived command.
	cost := float64(int64(r.MoveFactor))*float64(int(v)+1) + 1
	if !finite(cost) || cost > 255 {
		return -1
	}
	return int(cost)
}

type assaultTape struct {
	draws []AssaultDraw
	used  int
}

func (t *assaultTape) next(want AssaultDraw) (AssaultDraw, error) {
	if t.used == len(t.draws) {
		return AssaultDraw{}, &AssaultNeedDraw{want}
	}
	got := t.draws[t.used]
	t.used++
	if got.Kind != want.Kind || got.Bound != want.Bound || !finite(got.Fraction) || (got.Kind == "round" && (got.Integer != 0 || got.Fraction < 0 || got.Fraction >= 1)) || (got.Kind == "integer" && (got.Fraction != 0 || got.Integer > got.Bound)) {
		return AssaultDraw{}, ErrAssaultInput
	}
	return got, nil
}
func (t *assaultTape) integer(upper int) (int, error) {
	if upper <= 0 {
		return 0, nil
	}
	if upper > math.MaxInt32 {
		return 0, ErrAssaultInput
	}
	v, e := t.next(AssaultDraw{Kind: "integer", Bound: uint32(upper)})
	return int(v.Integer), e
}
func (t *assaultTape) casualty(value float64, available int) (int, error) {
	if !finite(value) || value < 0 || value > math.MaxInt32-1 {
		return 0, ErrAssaultInput
	}
	v, e := t.next(AssaultDraw{Kind: "round"})
	if e != nil {
		return 0, e
	}
	n := int(value)
	if v.Fraction <= value-float64(n) {
		n++
	}
	n, e = t.integer(n)
	return min(n, available), e
}

// PlanAssault is pure and deterministic. A missing transcript entry returns a
// typed draw request after all static admission checks. Replaying a complete
// transcript performs no RNG calls and validates every permitted proposed field.
func PlanAssault(in AssaultInput, draws []AssaultDraw) (AssaultChange, error) {
	var c AssaultChange
	if e := ValidateAssault(in); e != nil {
		return c, e
	}
	p, st, a, d, r := in.Planet, in.Star, in.Attacker, in.Defender, in.Request
	x, y, _ := AssaultDestination(p, r)
	next := *p
	next.Sectors = append([]model.Sector(nil), p.Sectors...)
	ns, na, nd := *st, *a, *d
	c.Planet = &next
	c.Star = &ns
	c.Attacker = &na
	c.Defender = &nd
	c.APCost = assaultAP(r)
	from, to := next.SectorAt(r.X, r.Y), next.SectorAt(x, y)
	oldCiv, oldMil := int(to.Popn), int(to.Troops)
	party := int(r.Quantity)
	if party < 1 || party > math.MaxUint16 {
		return AssaultChange{}, ErrAssaultInput
	}
	if r.Military {
		from.Troops -= uint16(party)
	} else {
		from.Popn -= uint16(party)
	}
	tape := assaultTape{draws: draws}
	if oldCiv+oldMil > 0 {
		if (d.Morale > 0 && a.Morale < math.MinInt64+d.Morale) || (d.Morale < 0 && a.Morale > math.MaxInt64+d.Morale) {
			return AssaultChange{}, ErrAssaultInput
		}
		diff := a.Morale - d.Morale
		if diff == math.MinInt64 {
			return AssaultChange{}, ErrAssaultInput
		}
		defense := [9]int{1, 1, 3, 2, 2, 3, 2, 4, 0}
		mult := 1
		if r.Military {
			mult = 10
		}
		// The original defined C morale_factor constant, not a changed probability.
		morale := func(v int64) float64 { return math.Atan(float64(v)/10000)/3.14159565 + 0.5 }
		c.AttackStrength = float64(party) * float64(a.Fighters) * float64(mult) * (a.Likes[from.Condition] + 1) * float64(defense[from.Condition]+1) * morale(diff)
		c.DefenseStrength = float64((oldCiv+oldMil*10)*int(d.Fighters)) * (d.Likes[to.Condition] + 1) * float64(defense[to.Condition]+1) * morale(-diff)
		if !finite(c.AttackStrength) || !finite(c.DefenseStrength) || c.AttackStrength <= 0 || c.DefenseStrength <= 0 {
			return AssaultChange{}, ErrAssaultInput
		}
		scale := min(party*int(a.Fighters)*mult, (oldCiv+oldMil*10)*int(d.Fighters))
		var e error
		c.AttackerLoss, e = tape.casualty(float64(scale/mult)*c.DefenseStrength/c.AttackStrength, party)
		if e != nil {
			return AssaultChange{}, e
		}
		c.CivilianLoss, e = tape.casualty(float64(scale)*c.AttackStrength/c.DefenseStrength, oldCiv)
		if e != nil {
			return AssaultChange{}, e
		}
		c.MilitaryLoss, e = tape.casualty(float64(scale/10)*c.AttackStrength/c.DefenseStrength, oldMil)
		if e != nil {
			return AssaultChange{}, e
		}
		party -= c.AttackerLoss
		if party < 0 || party > math.MaxUint16 {
			return AssaultChange{}, ErrAssaultInput
		}
		civ, e := cargoWord(int64(c.CivilianLoss))
		if e != nil {
			return AssaultChange{}, ErrAssaultInput
		}
		mil, e := cargoWord(int64(c.MilitaryLoss))
		if e != nil {
			return AssaultChange{}, ErrAssaultInput
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
			return AssaultChange{}, ErrAssaultInput
		}
		to.Owner = uint8(player)
		if r.Military {
			to.Troops = uint16(party)
		} else {
			to.Popn = uint16(party)
		}
		if a.Absorb {
			n, e := tape.integer(oldCiv + oldMil)
			if e != nil {
				return AssaultChange{}, e
			}
			c.Absorbed = min(n, math.MaxUint16-int(to.Popn))
			word, e := cargoWord(int64(c.Absorbed))
			if e != nil {
				return AssaultChange{}, ErrAssaultInput
			}
			to.Popn += word
		}
		if e := assaultMorale(&na, &nd, d.Fighters); e != nil {
			return AssaultChange{}, e
		}
	default:
		c.Outcome = "REPULSE"
		if d.Absorb {
			n, e := tape.integer(c.AttackerLoss)
			if e != nil {
				return AssaultChange{}, e
			}
			c.Absorbed = min(n, math.MaxUint16-int(to.Popn))
			word, e := cargoWord(int64(c.Absorbed))
			if e != nil {
				return AssaultChange{}, ErrAssaultInput
			}
			to.Popn += word
		}
		if r.Military {
			from.Troops += uint16(party)
		} else {
			from.Popn += uint16(party)
		}
		if e := assaultMorale(&nd, &na, a.Fighters); e != nil {
			return AssaultChange{}, e
		}
	}
	if tape.used != len(draws) {
		return AssaultChange{}, ErrAssaultInput
	}
	learn := func(v int8) int8 {
		if v >= 95 {
			return 100
		}
		return v + 5
	}
	na.Translate[d.PlayerNum-1] = learn(na.Translate[d.PlayerNum-1])
	nd.Translate[a.PlayerNum-1] = learn(nd.Translate[a.PlayerNum-1])
	// C uses origin + surviving party, not Go's historical missing-party check.
	if int(from.Popn)+int(from.Troops)+party == 0 {
		na.Translate[d.PlayerNum-1] = learn(na.Translate[d.PlayerNum-1])
	}
	if party == 0 {
		nd.Translate[a.PlayerNum-1] = learn(nd.Translate[a.PlayerNum-1])
	}
	if from.Popn == 0 && from.Troops == 0 {
		from.Owner = 0
	}
	infos, pop, troops, e := assaultTotals(&next)
	if e != nil {
		return AssaultChange{}, e
	}
	for i, v := range infos {
		next.Info[i].Popn = v.Popn
		next.Info[i].Troops = v.Troops
		next.Info[i].NumSectsOwned = v.NumSectsOwned
		next.Info[i].MobPoints = v.MobPoints
	}
	next.Popn = pop
	next.Troops = troops
	ap := c.APCost
	if ap < 1 || ap > math.MaxUint8 {
		return AssaultChange{}, ErrAssaultInput
	}
	ns.AP[in.Player-1] -= uint8(ap)
	return c, nil
}
func assaultMorale(winner, loser *model.Race, amount uint8) error {
	n := int64(amount)
	if winner.Morale > math.MaxInt64-n || loser.Morale < math.MinInt64+n || uint64(winner.Points[loser.PlayerNum])+uint64(amount) > math.MaxUint32 {
		return ErrAssaultInput
	}
	winner.Morale += n
	loser.Morale -= n
	winner.Points[loser.PlayerNum] += uint32(amount)
	return nil
}

// AssaultText is computed identically by actor and storage from checked models.
// One durable summary includes absorption and both casualty totals; live delivery
// of the same defender inbox message remains best-effort after commit.
func AssaultText(in AssaultInput, c AssaultChange) (string, string) {
	x, y, _ := AssaultDestination(in.Planet, in.Request)
	unit := "civ"
	if in.Request.Military {
		unit = "mil"
	}
	heading := fmt.Sprintf("/%s/%s: %s [%d] (%d,%d) assaults %s [%d] (%d,%d): %s\n", in.Star.Name, in.Star.PlanetNames[in.Planet.PlanetNum], in.Attacker.Name, in.Player, in.Request.X, in.Request.Y, in.Defender.Name, in.Defender.PlayerNum, x, y, c.Outcome)
	summary := fmt.Sprintf("Casualties: attacker %d %s; defender %d civ/%d mil. Absorbed: %d.\n", c.AttackerLoss, unit, c.CivilianLoss, c.MilitaryLoss, c.Absorbed)
	return heading + summary, heading + summary
}
