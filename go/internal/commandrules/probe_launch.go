package commandrules

import (
	"errors"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
)

// The numeric selector is independent of directory level. Scope witnesses the
// actual caller directory; Carrier identifies the physical hold.
type ProbeTransit struct {
	PlanetHangar
	Selected, Parent uint32
}

type ProbeTransitChange struct {
	Probe, Carrier, Predecessor *model.Ship
	Planet                      *model.Planet
}

var ErrLaunchRefused = errors.New("cannot launch from this hangar")

func PlanProbeLaunch(in ProbeTransit) (ProbeTransitChange, error) {
	var change ProbeTransitChange
	if in.Player < 1 || in.Player > model.MaxPlayers || in.Governor < 0 || in.Governor > model.MaxGovernors ||
		in.Scope.Level > model.LevelShip || in.Race == nil || in.Race.PlayerNum != in.Player || in.Selected == 0 {
		return change, ErrFactoryBuildInput
	}
	b, err := PlanetCarrierHold(in.Star, in.Planet, in.Orbit, in.Children, in.Parent)
	if err != nil && err != ErrProbeHoldUnsettled {
		return change, err
	}
	var prev *model.Ship
	for _, child := range in.Children {
		if child.Number != in.Selected {
			prev = child
			continue
		}
		if err != nil {
			return change, err
		}
		if child.Type != model.OtypeProbe || !child.Alive || int(child.Owner) != in.Player ||
			(in.Governor != 0 && int(child.Governor) != in.Governor) || !b.Alive || b.Docked ||
			in.Race.Dissolved || !in.Race.Governor[in.Governor].Active {
			return change, ErrLaunchRefused
		}
		if math.IsNaN(b.Mass) || math.IsInf(b.Mass, 0) || math.IsNaN(child.Mass) || math.IsInf(child.Mass, 0) || b.Mass < 0 || child.Mass < 0 {
			return change, ErrFactoryBuildInput
		}
		// Older construction need not have added passenger mass to the
		// Carrier. Decline an unrepresentable debit without repairing it.
		if b.Mass < child.Mass {
			return change, ErrLaunchRefused
		}
		probe, carrier, planet := *child, *b, *in.Planet
		if prev == nil {
			carrier.Ships = child.NextShip
		} else {
			pred := *prev
			pred.NextShip = child.NextShip
			change.Predecessor = &pred
		}
		carrier.Hanger -= child.Size
		carrier.Mass -= child.Mass
		probe.WhatOrbits, probe.StOrbits, probe.PNumOrbits = model.LevelPlan, b.StOrbits, b.PNumOrbits
		probe.XPos, probe.YPos = b.XPos, b.YPos
		probe.Docked, probe.WhatDest = false, model.LevelUniv
		probe.NextShip, planet.Ships = planet.Ships, child.Number
		change.Probe, change.Carrier, change.Planet = &probe, &carrier, &planet
		return change, nil
	}
	return change, ErrFactoryBuildInput
}
