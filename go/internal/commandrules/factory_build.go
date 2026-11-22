package commandrules

import (
	"errors"
	"fmt"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
)

var ErrFactoryBuildInput = errors.New("inconsistent planet factory construction context")

type FactoryBuildRefusal struct{ Message string }

func (e *FactoryBuildRefusal) Error() string { return e.Message }

// PlanetFactoryInput is one ordinary, non-God planet factory.
// Size/mass coefficients are trusted immutable server configuration, as in cargo.
// AP, race, star and existing ships are read-only witnesses, not write proposals.
type PlanetFactoryInput struct {
	Player, Governor, X, Y           int
	Number                           uint32
	Scope                            model.Directory
	Race                             *model.Race
	Star                             *model.Star
	Planet                           *model.Planet
	Ships                            []*model.Ship
	SizeCrew, SizeResource, MassSize float64
}
type PlanetFactoryChange struct {
	Ship   *model.Ship
	Planet *model.Planet
}

// FactoryBuildChain checks the whole unchanged planet-root chain. Dead records
// may remain linked until turn cleanup. No destination-order fields are location.
func FactoryBuildChain(st *model.Star, p *model.Planet, ships []*model.Ship) error {
	bad := ErrFactoryBuildInput
	if st == nil || p == nil || int(st.Number) >= model.NumStars || int(st.NumPlanets) > model.MaxPlanets || p.StarNum != st.Number || p.PlanetNum >= st.NumPlanets || p.MaxX == 0 || p.MaxY == 0 || len(p.Sectors) != int(p.MaxX)*int(p.MaxY) {
		return bad
	}
	byID := map[uint32]*model.Ship{}
	for _, sh := range ships {
		if sh == nil || sh.Number == 0 || byID[sh.Number] != nil || int(sh.Type) >= model.NumShipTypes || sh.Owner == 0 || int(sh.Owner) > model.MaxPlayers || int(sh.Governor) > model.MaxGovernors || sh.WhatOrbits != model.LevelPlan || sh.StOrbits != p.StarNum || sh.PNumOrbits != p.PlanetNum || (sh.Docked && (sh.LandX >= p.MaxX || sh.LandY >= p.MaxY)) {
			return bad
		}
		byID[sh.Number] = sh
	}
	seen := map[uint32]bool{}
	for n := p.Ships; n != 0; {
		sh := byID[n]
		if sh == nil || seen[n] {
			return bad
		}
		seen[n] = true
		n = sh.NextShip
	}
	if len(seen) != len(byID) {
		return bad
	}
	return nil
}

// PlanPlanetFactory reproduces Getship/create_ship_by_planet/initialize_new_ship
// for one ordinary factory. It neither allocates nor emits
// output, and never borrows mutable planet sectors into its result.
func PlanPlanetFactory(in PlanetFactoryInput) (PlanetFactoryChange, error) {
	bad := func() (PlanetFactoryChange, error) { return PlanetFactoryChange{}, ErrFactoryBuildInput }
	refuse := func(s string) (PlanetFactoryChange, error) {
		return PlanetFactoryChange{}, &FactoryBuildRefusal{Message: s}
	}
	p, r, st := in.Planet, in.Race, in.Star
	if in.Player < 1 || in.Player > model.MaxPlayers || in.Governor < 0 || in.Governor > model.MaxGovernors || r == nil || r.PlayerNum != in.Player || in.Number == 0 || FactoryBuildChain(st, p, in.Ships) != nil {
		return bad()
	}
	if in.Scope.Level != model.LevelPlan || in.Scope.StarNum != p.StarNum || in.Scope.PlanetNum != p.PlanetNum || r.God {
		return bad()
	}
	if r.Dissolved || !r.Governor[in.Governor].Active {
		return refuse("You are not authorized to build.\n")
	}
	if p.SlavedTo != 0 && int(p.SlavedTo) != in.Player {
		return refuse(fmt.Sprintf("This planet is enslaved by player %d.\n", p.SlavedTo))
	}
	if in.Governor != 0 && int(st.Governor[in.Player-1]) != in.Governor {
		return refuse("You are not authorized in this system.\n")
	}
	if in.X < 0 || in.Y < 0 || in.X > math.MaxUint8 || in.Y > math.MaxUint8 || in.X >= int(p.MaxX) || in.Y >= int(p.MaxY) {
		return refuse("Illegal sector.\n")
	}
	sec := p.SectorAt(in.X, in.Y)
	if sec.Popn == 0 {
		return refuse("You have no more civs in the sector!\n")
	}
	if sec.Condition == model.SectorWasted {
		return refuse("You can't build on wasted sectors.\n")
	}
	if int(sec.Owner) != in.Player {
		return refuse("You don't own that sector.\n")
	}
	for _, n := range []float64{r.Tech, in.SizeCrew, in.SizeResource, in.MassSize} {
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			return bad()
		}
	}
	for _, n := range []float64{st.XPos, st.YPos, p.XPos, p.YPos} {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return bad()
		}
	}
	for _, sh := range in.Ships {
		if sh.Number == in.Number {
			return bad()
		}
	}
	sd := model.Shipdata[model.OtypeFactory]
	crew, cargo, damage := sd[model.AbilMaxCrew], sd[model.AbilCargo], sd[model.AbilDamage]
	if crew < 0 || crew > math.MaxUint16 || cargo < 0 || cargo > math.MaxUint16 || damage < 0 || damage > math.MaxUint8 {
		return bad()
	}
	if r.Tech < float64(sd[model.AbilTech]) {
		return refuse(fmt.Sprintf("You are not advanced enough to build this ship.\n%.1f engineering technology needed. You have %.1f.\n", float64(sd[model.AbilTech]), r.Tech))
	}
	// Factory design has no guns, armor, fuel, destruct, engines or hangar.
	// Preserve the legacy float->integer truncation, after checking its width.
	size := 1 + in.SizeCrew*float64(sd[model.AbilMaxCrew]) + in.SizeResource*float64(sd[model.AbilCargo])
	cost := float64(sd[model.AbilCost]) + .05*float64(sd[model.AbilMaxCrew]) + .05*float64(sd[model.AbilCargo])
	if math.IsNaN(size) || math.IsInf(size, 0) || size < 1 || size >= 65536 || cost < 0 || cost >= 65536 {
		return bad()
	}
	f := &model.Ship{Type: model.OtypeFactory, BuildType: uint16(model.OtypeFactory), MaxCrew: uint16(crew), MaxResource: uint16(cargo), Size: uint16(size), BuildCost: uint16(cost)}
	f.BaseMass = 1 + in.MassSize*float64(f.Size)
	f.Mass = f.BaseMass
	if math.IsInf(f.Mass, 0) || math.IsNaN(f.Mass) {
		return bad()
	}
	if p.Info[in.Player-1].Resource < f.BuildCost {
		return refuse(fmt.Sprintf("You need %dr to construct this ship.\n", f.BuildCost))
	}
	f.Number = in.Number
	f.Owner = uint8(in.Player)
	f.Governor = uint8(in.Governor)
	f.Tech = r.Tech
	f.XPos = st.XPos + p.XPos
	f.YPos = st.YPos + p.YPos
	if math.IsInf(f.XPos, 0) || math.IsInf(f.YPos, 0) {
		return bad()
	}
	f.LandX = uint8(in.X)
	f.LandY = uint8(in.Y)
	f.Class = "Standard"
	f.WhatOrbits = model.LevelPlan
	f.WhatDest = model.LevelPlan
	f.Docked = true
	f.StOrbits = p.StarNum
	f.PNumOrbits = p.PlanetNum
	f.DestStar = p.StarNum
	f.DestPNum = p.PlanetNum
	f.NextShip = p.Ships
	f.Alive = true
	f.Active = true
	f.Damage = uint8(damage)
	next := *p
	next.Sectors = append([]model.Sector(nil), p.Sectors...)
	if r.Governor[in.Governor].Toggle.AutoLoad {
		// Legacy scrap and homeworld placement can persist sector balances that
		// disagree with the planet summaries. Never spend or repair those totals.
		totals, popn, _, err := assaultTotals(p)
		if err != nil || !finite(r.Mass) || r.Mass < 0 {
			return bad()
		}
		recorded, counted := p.Info[in.Player-1], totals[in.Player-1]
		if p.Popn != popn || recorded.Popn != counted.Popn || recorded.NumSectsOwned != counted.NumSectsOwned || recorded.MobPoints != counted.MobPoints {
			return refuse("Factory autoload refused: planet population or ownership totals disagree with its sectors.\n")
		}
		// A new factory has no fuel capacity. Loading its complement uses the
		// same checked population, mass and evacuation rules as loading civilians.
		load, err := PlanPopulation(f, nil, p, r, PopulationRequest{Commodity: 'c'})
		if err != nil {
			return bad()
		}
		f.Popn, f.Mass = load.Selected, load.SelectedMass
		*next.SectorAt(in.X, in.Y) = load.Sector
		next.Info[in.Player-1], next.Popn = load.Info, load.PlanetPopn
	}
	next.Info[in.Player-1].Resource -= f.BuildCost
	next.Ships = f.Number
	return PlanetFactoryChange{f, &next}, nil
}
