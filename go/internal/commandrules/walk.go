package commandrules

import (
	"errors"
	"fmt"
	"math"
	"reflect"

	"github.com/phreddrick/GB/go/internal/model"
)

var ErrWalkInput = errors.New("inconsistent walk context")
var ErrWalkLegacy = errors.New("walk requires the existing combat or alternate-location path")

// WalkRefusal is an ordinary no-mutation gameplay refusal.
type WalkRefusal struct {
	Message  string
	Registry bool // enoughAP has always delivered through the session registry
}

func (e *WalkRefusal) Error() string { return e.Message }

// WalkInput describes one noncombat surface step. Planet, races and the complete
// planet-root chain are immutable witnesses. Only Ship and Star may change.
type WalkInput struct {
	Player, Governor int
	RecipientCount   int // actor race slice length; legacy notification bound
	Ship             *model.Ship
	Star             *model.Star
	Planet           *model.Planet
	Races            []*model.Race
	Ships            []*model.Ship
	Direction        byte
	MassFuel         float64 // trusted server configuration, as in cargo contracts
}

type WalkChange struct {
	Ship *model.Ship
	Star *model.Star
}

// WalkDestination retains get_move's aliases, X wrapping, pole refusal and
// one-column no-displacement steps (which still cost fuel/AP in the C command).
func WalkDestination(p *model.Planet, x, y int, direction byte) (int, int, bool) {
	if p == nil || p.MaxX == 0 || p.MaxY == 0 || x < 0 || y < 0 || x >= int(p.MaxX) || y >= int(p.MaxY) {
		return 0, 0, false
	}
	switch direction {
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
	return x, y, y >= 0 && y < int(p.MaxY)
}

// PlanWalk is pure: no RNG, callbacks, persistence, sector ownership changes or
// borrowed actor mutation. ErrWalkLegacy is a classification, never permission
// for the database operation to write a combat proposal.
func PlanWalk(in WalkInput) (WalkChange, error) {
	bad := func() (WalkChange, error) { return WalkChange{}, ErrWalkInput }
	refuse := func(s string) (WalkChange, error) { return WalkChange{}, &WalkRefusal{Message: s} }
	f, p, st := in.Ship, in.Planet, in.Star
	if in.RecipientCount < in.Player || in.RecipientCount > model.MaxPlayers {
		return bad()
	}
	if in.Player < 1 || in.Player > model.MaxPlayers || in.Governor < 0 || in.Governor > model.MaxGovernors || f == nil || p == nil || st == nil || f.Number == 0 || !f.Alive || int(f.Type) >= len(model.Shipdata) || f.Type != model.OtypeAFV {
		return bad()
	}
	races := map[int]*model.Race{}
	for _, r := range in.Races {
		if r == nil || r.PlayerNum < 1 || r.PlayerNum > model.MaxPlayers || races[r.PlayerNum] != nil {
			return bad()
		}
		races[r.PlayerNum] = r
	}
	r := races[in.Player]
	if r == nil {
		return bad()
	}
	if r.Dissolved || !r.Governor[in.Governor].Active || int(f.Owner) != in.Player || (in.Governor != 0 && int(f.Governor) != in.Governor) {
		return refuse("You do not control this ship.\n")
	}
	if !f.Docked || f.WhatOrbits != model.LevelPlan {
		return WalkChange{}, ErrWalkLegacy
	}
	if int(st.Number) >= model.NumStars || int(st.NumPlanets) > model.MaxPlanets || st.Number != f.StOrbits || p.StarNum != f.StOrbits || p.PlanetNum != f.PNumOrbits || p.PlanetNum >= st.NumPlanets || p.MaxX == 0 || p.MaxY == 0 || len(p.Sectors) != int(p.MaxX)*int(p.MaxY) || f.LandX >= p.MaxX || f.LandY >= p.MaxY {
		return bad()
	}
	if f.Popn == 0 {
		return refuse("No crew.\n")
	}
	if math.IsNaN(f.Fuel) || math.IsInf(f.Fuel, 0) || math.IsNaN(f.Mass) || math.IsInf(f.Mass, 0) || f.Mass < 0 || !finite(in.MassFuel) || in.MassFuel < 0 || f.Fuel < 0 {
		return bad()
	}
	if f.Fuel < 1 {
		return refuse("You don't have 1.0 fuel to move it.\n")
	}
	if st.AP[in.Player-1] < 1 {
		return WalkChange{}, &WalkRefusal{Message: "You don't have 1 action points there.\n", Registry: true}
	}
	x, y, ok := WalkDestination(p, int(f.LandX), int(f.LandY), in.Direction)
	if !ok {
		if validWalkDirection(in.Direction) {
			return refuse(fmt.Sprintf("Illegal coordinates %d,%d.\n", x, y))
		}
		return refuse("Illegal move.\n")
	}
	// Explicit storage-width guards also make the subsequent conversions
	// independently auditable rather than relying on map-dimension inference.
	if x < 0 || x > math.MaxUint8 || y < 0 || y > math.MaxUint8 {
		return bad()
	}
	sect := p.SectorAt(x, y)
	if int(sect.Condition) >= len(r.Likes) || int(sect.Owner) > model.MaxPlayers {
		return bad()
	}
	if r.Likes[sect.Condition] == 0 {
		return refuse("Your ships cannot walk into that sector type!\n")
	}
	if !finite(r.Likes[sect.Condition]) {
		return bad()
	}
	ships := map[uint32]*model.Ship{}
	for _, sh := range in.Ships {
		if sh == nil || sh.Number == 0 || ships[sh.Number] != nil || int(sh.Type) >= len(model.Shipdata) || sh.WhatOrbits != model.LevelPlan || sh.StOrbits != p.StarNum || sh.PNumOrbits != p.PlanetNum || races[int(sh.Owner)] == nil {
			return bad()
		}
		ships[sh.Number] = sh
	}
	seen := map[uint32]bool{}
	found := false
	combat := false
	for n := p.Ships; n != 0; {
		sh := ships[n]
		if sh == nil || seen[n] {
			return bad()
		}
		seen[n] = true
		if n == f.Number {
			found = true
		}
		// Exact cmdWalk/retalStrength predicates for an AFV: Active and On
		// do not suppress this legacy defense. Both alliances must hold.
		armed := sh.Alive && sh.Popn > 0 && sh.Retaliate > 0 && sh.Destruct > 0 && ((sh.Guns == model.GunPrimary && sh.Primary > 0) || (sh.Guns == model.GunSecondary && sh.Secondary > 0))
		if sh.Type == model.OtypeAFV && int(sh.Owner) != in.Player && sh.Docked && int(sh.LandX) == x && int(sh.LandY) == y && armed && (!r.IsAllied(int(sh.Owner)) || !races[int(sh.Owner)].IsAllied(in.Player)) {
			combat = true
		}
		n = sh.NextShip
	}
	if !found || len(seen) != len(ships) || !reflect.DeepEqual(ships[f.Number], f) {
		return bad()
	}
	if combat {
		return WalkChange{}, ErrWalkLegacy
	}
	if sect.Owner != 0 && int(sect.Owner) != in.Player {
		other := races[int(sect.Owner)]
		if other == nil {
			return bad()
		}
		if !r.IsAllied(int(sect.Owner)) || !other.IsAllied(in.Player) {
			return WalkChange{}, ErrWalkLegacy
		}
	}
	for i, info := range p.Info[:in.RecipientCount] {
		if info.NumSectsOwned > 0 && i+1 != in.Player && int(st.Governor[i]) > model.MaxGovernors {
			return bad()
		}
	}
	ship := *f
	ship.LandX, ship.LandY = uint8(x), uint8(y) // bounded by uint8 map dimensions
	ship.Fuel--
	ship.Mass -= in.MassFuel
	if !finite(ship.Mass) || ship.Mass < 0 {
		return bad()
	}
	star := *st
	star.AP[in.Player-1]--
	return WalkChange{&ship, &star}, nil
}

func validWalkDirection(c byte) bool {
	switch c {
	case '1', 'b', '2', 'k', '3', 'n', '4', 'h', '6', 'l', '7', 'y', '8', 'j', '9', 'u':
		return true
	}
	return false
}
