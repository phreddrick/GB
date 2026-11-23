package commandrules

import (
	"fmt"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
)

// ProbeLanding uses the running engine's docking distance and fuel mass factor.
// The caller supplies preimages only. Storage replays the same bounded move.
type ProbeLanding struct {
	ProbeTransit
	DistToDock, MassFuel float64
}

func PlanProbeLanding(in ProbeLanding) (ProbeTransitChange, float64, error) {
	var change ProbeTransitChange
	bad := ErrFactoryBuildInput
	if in.Player < 1 || in.Player > model.MaxPlayers || in.Governor < 0 || in.Governor > model.MaxGovernors ||
		in.Race == nil || in.Race.PlayerNum != in.Player || in.Scope.Level > model.LevelShip ||
		in.Selected == 0 || in.Parent == in.Selected || !finiteCargoMass(in.DistToDock) || !finiteCargoMass(in.MassFuel) {
		return change, 0, bad
	}
	b, err := PlanetCarrierHold(in.Star, in.Planet, in.Orbit, in.Children, in.Parent)
	if err != nil && err != ErrProbeHoldUnsettled {
		return change, 0, err
	}
	var c, pred *model.Ship
	for _, sh := range in.Orbit {
		if sh.Number == in.Selected {
			c = sh
		}
		if sh.NextShip == in.Selected {
			pred = sh
		}
	}
	if c == nil && err == ErrProbeHoldUnsettled {
		for _, held := range in.Children {
			if held.Number == in.Selected {
				return change, 0, err
			}
		}
	}
	if c == nil || c.Type != model.OtypeProbe || !c.Alive || c.Docked || !b.Alive || b.Docked {
		return change, 0, bad
	}
	if err != nil {
		return change, 0, err
	}
	if in.Race.Dissolved || !in.Race.Governor[in.Governor].Active || int(c.Owner) != in.Player ||
		int(b.Owner) != in.Player || !b.Active || in.Governor != 0 && (int(c.Governor) != in.Governor || int(b.Governor) != in.Governor) {
		return change, 0, &FactoryBuildRefusal{"You are not authorized to land this probe on that Carrier.\n"}
	}
	if !finiteCargoMass(c.Fuel) || !finiteCargoMass(c.Mass) || !finiteCargoMass(b.Mass) {
		return change, 0, bad
	}
	if c.Fuel > float64(c.MaxFuel) || c.Resource > c.MaxResource || c.Destruct > c.MaxDestruct || int(c.Popn)+int(c.Troops) > int(c.MaxCrew) {
		return change, 0, &FactoryBuildRefusal{fmt.Sprintf(":%d is too overloaded to land.\n", c.Number)}
	}
	dx, dy := c.XPos-b.XPos, c.YPos-b.YPos
	d := math.Sqrt(dx*dx + dy*dy)
	if !finiteCargoMass(d) {
		return change, 0, bad
	}
	if d > in.DistToDock {
		return change, 0, &FactoryBuildRefusal{fmt.Sprintf(":%d must be %.2f or closer to X%d.\n", c.Number, in.DistToDock, b.Number)}
	}
	fuel := 0.05 + d*0.025*math.Sqrt(c.Mass)
	if c.Fuel < fuel {
		return change, 0, &FactoryBuildRefusal{"Not enough fuel.\n"}
	}
	if int(b.Hanger)+int(c.Size) > int(b.MaxHanger) {
		return change, 0, &FactoryBuildRefusal{fmt.Sprintf("Mothership does not have %d hanger space available to load ship.\n", c.Size)}
	}
	probe, carrier, planet := *c, *b, *in.Planet
	probe.Fuel -= fuel
	probe.Mass -= fuel * in.MassFuel
	carrier.Mass += probe.Mass
	if !finiteCargoMass(probe.Mass) || !finiteCargoMass(carrier.Mass) {
		return change, 0, &FactoryBuildRefusal{"Cannot account for this landing's mass.\n"}
	}
	switch {
	case pred == nil:
		planet.Ships = c.NextShip
	case pred.Number == b.Number:
		carrier.NextShip = c.NextShip
	default:
		before := *pred
		before.NextShip = c.NextShip
		change.Predecessor = &before
	}
	probe.Docked, probe.WhatOrbits, probe.WhatDest = true, model.LevelShip, model.LevelShip
	probe.DestShipNo, probe.NextShip = b.Number, b.Ships
	carrier.Ships, carrier.Hanger = c.Number, carrier.Hanger+c.Size
	change.Probe, change.Carrier, change.Planet = &probe, &carrier, &planet
	return change, fuel, nil
}
