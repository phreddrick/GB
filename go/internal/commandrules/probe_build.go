package commandrules

import (
	"fmt"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/model"
)

// PlanetHangar captures a caller and the complete exterior and interior chains.
type PlanetHangar struct {
	Player, Governor int
	Scope            model.Directory
	Race             *model.Race
	Star             *model.Star
	Planet           *model.Planet
	Orbit, Children  []*model.Ship
}

type ProbeBuild = PlanetHangar

// ErrProbeHoldUnsettled means the chain is structurally accounted for, but
// cannot supply a reliable insertion point and capacity for a new build.
var ErrProbeHoldUnsettled = &FactoryBuildRefusal{"Cannot build: this Carrier's hangar records are unsettled.\n"}

// ProbeChildren checks the entire chain before reporting a gameplay refusal.
// Parent identity is structural. A dead child can remain linked until a turn,
// and older land/launch commands left planet-orbit fields on contained ships.
func ProbeChildren(b *model.Ship, children []*model.Ship) (map[uint32]bool, error) {
	n := b.Ships
	seen := make(map[uint32]bool, len(children))
	unsettled := false
	var occupied uint64
	for _, sh := range children {
		if sh == nil || n == 0 || sh.Number != n || n == b.Number || seen[n] || sh.Owner == 0 || int(sh.Owner) > model.MaxPlayers || int(sh.Governor) > model.MaxGovernors || int(sh.Type) >= model.NumShipTypes || sh.WhatDest > model.LevelShip {
			return nil, ErrFactoryBuildInput
		}
		switch sh.WhatOrbits {
		case model.LevelShip:
			if sh.DestShipNo != b.Number {
				return nil, ErrFactoryBuildInput
			}
			if !sh.Docked || sh.WhatDest != model.LevelShip {
				unsettled = true
			}
		case model.LevelPlan:
			// Ownership can also have changed since the old landing. The list
			// and disjoint orbit roots establish membership, not the owners.
			if b.WhatOrbits != model.LevelPlan {
				return nil, ErrFactoryBuildInput
			}
			unsettled = true
		default:
			return nil, ErrFactoryBuildInput
		}
		if sh.Alive {
			occupied += uint64(sh.Size)
		} else {
			unsettled = true
		}
		seen[n] = true
		n = sh.NextShip
	}
	if n != 0 {
		return nil, ErrFactoryBuildInput
	}
	if unsettled || occupied != uint64(b.Hanger) {
		return seen, ErrProbeHoldUnsettled
	}
	return seen, nil
}

func (in ProbeBuild) Carrier() (*model.Ship, error) {
	if in.Scope.Level != model.LevelShip {
		return nil, ErrFactoryBuildInput
	}
	return PlanetCarrierHold(in.Star, in.Planet, in.Orbit, in.Children, in.Scope.ShipNo)
}

// PlanetCarrierHold validates both complete lists and their disjoint membership.
func PlanetCarrierHold(st *model.Star, pl *model.Planet, orbit, children []*model.Ship, carrier uint32) (*model.Ship, error) {
	if FactoryBuildChain(st, pl, orbit) != nil {
		return nil, ErrFactoryBuildInput
	}
	for _, b := range orbit {
		if b.Number == carrier && b.Type == model.StypeCarrier {
			hold, err := ProbeChildren(b, children)
			if err == ErrProbeHoldUnsettled {
				for _, sh := range orbit {
					if hold[sh.Number] {
						return nil, ErrFactoryBuildInput
					}
				}
			}
			return b, err
		}
	}
	return nil, ErrFactoryBuildInput
}

func PlanProbeBuild(in ProbeBuild, number uint32, cfg *config.Config) (*model.Ship, error) {
	b, e := in.Carrier()
	if e != nil {
		return nil, e
	}
	p, g, r := in.Player, in.Governor, in.Race
	if number == 0 || cfg == nil || p < 1 || p > model.MaxPlayers || g < 0 || g > model.MaxGovernors || r == nil || r.PlayerNum != p || r.God {
		return nil, ErrFactoryBuildInput
	}
	for _, sh := range in.Orbit {
		if sh.Number == number {
			return nil, ErrFactoryBuildInput
		}
	}
	for _, sh := range in.Children {
		if sh.Number == number {
			return nil, ErrFactoryBuildInput
		}
	}
	if r.Dissolved || !r.Governor[g].Active || int(b.Owner) != p || (g != 0 && int(b.Governor) != g) || !b.Alive || !b.Active || b.Docked || b.Popn == 0 || b.Damage != 0 {
		return nil, &FactoryBuildRefusal{"You can't build here.\n"}
	}
	sd := model.Shipdata[model.OtypeProbe]
	if sd[model.AbilBuild]&model.Shipdata[b.Type][model.AbilConstruct] == 0 {
		return nil, &FactoryBuildRefusal{"This ship type cannot be built here.\n"}
	}
	if !finiteCargoMass(r.Tech) {
		return nil, ErrFactoryBuildInput
	}
	if r.Tech < float64(sd[model.AbilTech]) {
		return nil, &FactoryBuildRefusal{fmt.Sprintf("You are not advanced enough to build this ship.\n%.1f engineering technology needed. You have %.1f.\n", float64(sd[model.AbilTech]), r.Tech)}
	}
	c, e := constructionDesign(cfg, r, model.OtypeProbe)
	if e != nil {
		return nil, e
	}
	if int(b.Hanger)+int(c.Size) > int(b.MaxHanger) {
		return nil, &FactoryBuildRefusal{"Not enough hanger space.\n"}
	}
	if b.Resource < c.BuildCost {
		return nil, &FactoryBuildRefusal{fmt.Sprintf("You need %dr to construct the ship.\n", c.BuildCost)}
	}
	c.Number, c.Owner, c.Governor = number, uint8(p), uint8(g)
	c.Tech, c.Class = r.Tech, "Standard"
	c.Alive, c.Active, c.Docked = true, true, true
	c.StOrbits, c.PNumOrbits = b.StOrbits, b.PNumOrbits
	c.DestStar, c.DestPNum = b.DestStar, b.DestPNum
	c.WhatOrbits, c.WhatDest = model.LevelShip, model.LevelShip
	c.DestShipNo, c.NextShip = b.Number, b.Ships
	c.XPos, c.YPos = b.XPos, b.YPos
	c.LandX, c.LandY = b.LandX, b.LandY
	c.Mass = c.BaseMass
	if r.Governor[g].Toggle.AutoLoad {
		// Space Probes have no crew capacity. Fuel moves from the Carrier
		// to its hold without changing the Carrier's aggregate mass.
		if !finiteCargoMass(b.Fuel) || !finiteCargoMass(cfg.MassFuel) {
			return nil, ErrFactoryBuildInput
		}
		c.Fuel = min(float64(c.MaxFuel), b.Fuel)
		// A malformed, very large balance may round away part of the debit.
		remaining := b.Fuel - c.Fuel
		if b.Fuel-remaining != c.Fuel {
			return nil, ErrFactoryBuildInput
		}
		c.Mass += c.Fuel * cfg.MassFuel
	}
	if !finiteCargoMass(c.Mass) || c.MaxSpeed > 255 {
		return nil, ErrFactoryBuildInput
	}
	c.Speed = uint8(c.MaxSpeed)
	return c, nil
}

// InstallProbeDebits applies only fields derived by PlanProbeBuild.
func InstallProbeDebits(b, c *model.Ship) {
	b.Resource -= c.BuildCost
	b.Fuel -= c.Fuel
	b.Hanger += c.Size
	b.Ships = c.Number
}
