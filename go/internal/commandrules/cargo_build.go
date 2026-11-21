package commandrules

import (
	"fmt"
	"math"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/model"
)

// CargoBuild is one externally built carrier (X). Ships is the complete
// orbital chain in traversal order, including the selected cargo ship.
type CargoBuild struct {
	Player, Governor int
	Scope            model.Directory
	Race             *model.Race
	Planet           *model.Planet
	Star             *model.Star
	Universe         *model.StarData
	Ships            []*model.Ship
}

func (in CargoBuild) orbitHead(b *model.Ship) (uint32, error) {
	switch b.WhatOrbits {
	case model.LevelPlan:
		if in.Universe == nil && FactoryBuildChain(in.Star, in.Planet, in.Ships) == nil && in.Planet.StarNum == b.StOrbits && in.Planet.PlanetNum == b.PNumOrbits {
			return in.Planet.Ships, nil
		}
	case model.LevelStar:
		if in.Universe == nil && in.Planet == nil && in.Star != nil && in.Star.Number == b.StOrbits && int(in.Star.Number) < model.NumStars && int(in.Star.NumPlanets) <= model.MaxPlanets {
			return in.Star.Ships, nil
		}
	case model.LevelUniv:
		if in.Universe != nil && in.Planet == nil && in.Star == nil {
			return in.Universe.Ships, nil
		}
	}
	return 0, ErrFactoryBuildInput
}

// Builder validates the complete actor or stored orbital list before treating
// a difference between two snapshots as a pending legacy save.
func (in CargoBuild) Builder() (*model.Ship, error) {
	var b *model.Ship
	for _, sh := range in.Ships {
		if sh != nil && sh.Number == in.Scope.ShipNo {
			b = sh
		}
	}
	if b == nil || b.Type != model.StypeCargo {
		return nil, ErrFactoryBuildInput
	}
	head, e := in.orbitHead(b)
	if e != nil {
		return nil, e
	}
	n := head
	seen := map[uint32]bool{}
	for _, sh := range in.Ships {
		if sh == nil || n == 0 || sh.Number != n || seen[n] || sh.WhatOrbits != b.WhatOrbits || sh.Owner == 0 || int(sh.Owner) > model.MaxPlayers || int(sh.Governor) > model.MaxGovernors || int(sh.Type) >= model.NumShipTypes || (b.WhatOrbits != model.LevelUniv && sh.StOrbits != b.StOrbits) {
			return nil, ErrFactoryBuildInput
		}
		seen[n] = true
		n = sh.NextShip
	}
	if n != 0 {
		return nil, ErrFactoryBuildInput
	}
	return b, nil
}

// PlanCargoBuild computes the new child, whose cost and loaded complement determine the builder
// balances. No other builder fields (including Mass and Hanger) change.
func PlanCargoBuild(in CargoBuild, number uint32, cfg *config.Config) (*model.Ship, error) {
	r, p, g := in.Race, in.Player, in.Governor
	if cfg == nil || number == 0 || p < 1 || p > model.MaxPlayers || g < 0 || g > model.MaxGovernors || r == nil || r.PlayerNum != p || in.Scope.Level != model.LevelShip {
		return nil, ErrFactoryBuildInput
	}
	b, err := in.Builder()
	if err != nil {
		return nil, err
	}
	for _, sh := range in.Ships {
		if sh.Number == number {
			return nil, ErrFactoryBuildInput
		}
	}
	if r.Dissolved || !r.Governor[g].Active || int(b.Owner) != p || (g != 0 && int(b.Governor) != g) || !b.Alive || !b.Active || b.Docked || b.Popn == 0 || b.Damage != 0 {
		return nil, &FactoryBuildRefusal{"You can't build here.\n"}
	}
	sd := model.Shipdata[model.StypeCarrier]
	if sd[model.AbilBuild]&model.Shipdata[b.Type][model.AbilConstruct] == 0 {
		return nil, &FactoryBuildRefusal{"This ship type cannot be built here.\n"}
	}
	if !r.God && r.Tech < float64(sd[model.AbilTech]) {
		return nil, &FactoryBuildRefusal{fmt.Sprintf("You are not advanced enough to build this ship.\n%.1f engineering technology needed. You have %.1f.\n", float64(sd[model.AbilTech]), r.Tech)}
	}
	child, e := carrierDesign(cfg, r)
	if e != nil {
		return nil, e
	}
	if b.Resource < child.BuildCost {
		return nil, &FactoryBuildRefusal{fmt.Sprintf("You need %dr to construct the ship.\n", child.BuildCost)}
	}
	child.Number = number
	child.Owner = uint8(p)
	child.Governor = uint8(g)
	child.Tech = r.Tech
	child.Class = "Standard"
	child.Alive = true
	child.Active = true
	child.NextShip = in.Ships[0].Number
	child.WhatOrbits = b.WhatOrbits
	child.StOrbits = b.StOrbits
	child.PNumOrbits = b.PNumOrbits
	child.DestStar = b.DestStar
	child.DestPNum = b.DestPNum
	child.XPos = b.XPos
	child.YPos = b.YPos
	child.LandX = b.LandX
	child.LandY = b.LandY
	if child.MaxSpeed > math.MaxUint8 {
		return nil, ErrFactoryBuildInput
	}
	child.Speed = uint8(child.MaxSpeed)
	child.Retaliate = child.Primary
	child.Protect.Self = child.Guns != 0
	if r.God {
		child.Fuel = float64(child.MaxFuel)
		child.Popn = child.MaxCrew
		child.Resource = child.MaxResource
		child.Destruct = child.MaxDestruct
		child.Mounted = child.Mount != 0
	} else {
		damage := sd[model.AbilDamage]
		if damage < 0 || damage > 255 {
			return nil, ErrFactoryBuildInput
		}
		child.Damage = uint8(damage)
		if r.Governor[g].Toggle.AutoLoad {
			child.Popn = min(child.MaxCrew, b.Popn)
			child.Fuel = min(float64(child.MaxFuel), b.Fuel)
		}
	}
	child.Mass = child.BaseMass + float64(child.Popn)*r.Mass + child.Fuel*cfg.MassFuel + float64(child.Resource)*cfg.MassResource + float64(child.Destruct)*cfg.MassDestruct
	if !finiteCargoMass(r.Tech) || !finiteCargoMass(r.Mass) || !finiteCargoMass(b.Fuel) || !finiteCargoMass(child.Mass) || child.Size < child.MaxHanger {
		return nil, ErrFactoryBuildInput
	}
	return child, nil
}
func finiteCargoMass(x float64) bool { return x >= 0 && !math.IsInf(x, 0) && !math.IsNaN(x) }
