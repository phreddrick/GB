package commandrules

import (
	"errors"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
)

var ErrPopulationInput = errors.New("invalid population transfer")
var ErrPopulationBounds = errors.New("population amount outside permitted range")
var ErrPopulationDeferred = errors.New("population transfer requires deferred effects")

type PopulationRequest struct {
	Commodity byte
	Amount    int64
	Unload    bool
}
type PopulationChange struct {
	Amount, Lower, Upper     int64
	Selected, Other          uint16
	SelectedMass, OtherMass  float64
	Sector                   model.Sector
	Info                     model.PlInfo
	PlanetPopn, PlanetTroops uint64
	Colonized, Evacuated     bool
}

func populationCapacity(s *model.Ship, troops bool) int64 {
	capacity := int64(s.MaxCrew)
	if s.Type == model.OtypeFactory {
		capacity = model.Shipdata[model.OtypeFactory][model.AbilMaxCrew]
	}
	if troops {
		return capacity - int64(s.Popn)
	}
	return capacity - int64(s.Troops)
}

// populationTotal applies a signed removal without narrowing a uint64 preimage.
func populationTotal(before uint64, removal int64, limit uint64) (uint64, error) {
	if before > limit {
		return 0, ErrPopulationInput
	}
	if removal >= 0 {
		n := uint64(removal)
		if n > before {
			return 0, ErrPopulationInput
		}
		return before - n, nil
	}
	n := uint64(-removal)
	if n > limit-before {
		return 0, ErrPopulationInput
	}
	return before + n, nil
}

// PlanPopulation preserves C load.c limits/default ordering, with the approved
// ownership-transition correction, zero AP and real containing-carrier mass rule.
// Caller race is immutable authority and supplies mass; Sector.Race is preserved.
func PlanPopulation(s, other *model.Ship, planet *model.Planet, race *model.Race, r PopulationRequest) (PopulationChange, error) {
	var c PopulationChange
	invalid := func() (PopulationChange, error) { return c, ErrPopulationInput }
	if s == nil || race == nil || race.PlayerNum < 1 || race.PlayerNum > model.MaxPlayers || int(s.Type) >= len(model.Shipdata) || (other == nil) == (planet == nil) || !finite(race.Mass) || race.Mass < 0 || !finite(s.Mass) || s.Mass < 0 || r.Amount < math.MinInt32 || r.Amount > math.MaxInt32 || (r.Commodity != 'c' && r.Commodity != 'm') {
		return invalid()
	}
	if s.Type == model.OtypeTransDev && s.On && (s.Special.Transport == nil || s.Special.Transport.Target != 0) {
		return c, ErrPopulationDeferred
	}
	amount := r.Amount
	if r.Unload {
		if amount == math.MinInt32 {
			return invalid()
		}
		amount = -amount
	}
	if amount < 0 && s.Type == model.OtypeVN {
		return invalid()
	}
	troops := r.Commodity == 'm'
	a := int64(s.Popn)
	if troops {
		a = int64(s.Troops)
	}
	var b int64
	if other != nil {
		if int(other.Type) >= len(model.Shipdata) || !finite(other.Mass) || other.Mass < 0 {
			return invalid()
		}
		b = int64(other.Popn)
		if troops {
			b = int64(other.Troops)
		}
		c.OtherMass = other.Mass
		if other.Owner == s.Owner {
			c.Upper = min(b, populationCapacity(s, troops)-a)
			c.Lower = -min(a, populationCapacity(other, troops)-b)
		}
	} else {
		if planet.MaxX == 0 || planet.MaxY == 0 || len(planet.Sectors) != int(planet.MaxX)*int(planet.MaxY) || s.LandX >= planet.MaxX || s.LandY >= planet.MaxY {
			return invalid()
		}
		c.Sector = *planet.SectorAt(int(s.LandX), int(s.LandY))
		if c.Sector.Owner != 0 && int(c.Sector.Owner) != race.PlayerNum {
			return c, ErrPopulationDeferred
		}
		c.Info = planet.Info[race.PlayerNum-1]
		c.PlanetPopn = planet.Popn
		c.PlanetTroops = planet.Troops
		b = int64(c.Sector.Popn)
		if troops {
			b = int64(c.Sector.Troops)
		}
		c.Upper = min(b, populationCapacity(s, troops)-a)
		c.Lower = -a
	}
	if amount < c.Lower || amount > c.Upper {
		return c, ErrPopulationBounds
	}
	if amount == 0 {
		if r.Unload {
			amount = c.Lower
		} else {
			amount = c.Upper
		}
	}
	c.Amount = amount
	var e error
	c.Selected, e = cargoWord(a + amount)
	if e != nil {
		return invalid()
	}
	c.Other, e = cargoWord(b - amount)
	if e != nil {
		return invalid()
	}
	c.SelectedMass = s.Mass + float64(amount)*race.Mass
	if other != nil {
		if s.WhatOrbits != model.LevelShip || s.DestShipNo != other.Number {
			c.OtherMass -= float64(amount) * race.Mass
		}
	} else {
		if troops {
			c.Sector.Troops = c.Other
			c.PlanetTroops, e = populationTotal(planet.Troops, amount, math.MaxUint64)
			if e != nil {
				return invalid()
			}
			n, e := populationTotal(uint64(c.Info.Troops), amount, math.MaxUint32)
			if e != nil || n > math.MaxUint32 {
				return invalid()
			}
			c.Info.Troops = uint32(n)
		} else {
			c.Sector.Popn = c.Other
			c.PlanetPopn, e = populationTotal(planet.Popn, amount, math.MaxUint64)
			if e != nil {
				return invalid()
			}
			n, e := populationTotal(uint64(c.Info.Popn), amount, math.MaxUint32)
			if e != nil || n > math.MaxUint32 {
				return invalid()
			}
			c.Info.Popn = uint32(n)
		}
		before := planet.SectorAt(int(s.LandX), int(s.LandY))
		// No new habitability/terrain/Race/sector-owner permission is invented.
		if before.Popn == 0 && before.Troops == 0 && amount < 0 && before.Owner == 0 {
			if c.Info.NumSectsOwned == math.MaxUint16 || c.Info.MobPoints > math.MaxUint64-uint64(before.Mobilization) {
				return invalid()
			}
			c.Sector.Owner = uint8(race.PlayerNum)
			c.Info.NumSectsOwned++
			c.Info.MobPoints += uint64(before.Mobilization)
			c.Colonized = true
		}
		if c.Sector.Popn == 0 && c.Sector.Troops == 0 && c.Sector.Owner == uint8(race.PlayerNum) {
			if c.Info.NumSectsOwned == 0 || c.Info.MobPoints < uint64(before.Mobilization) {
				return invalid()
			}
			c.Sector.Owner = 0
			c.Info.NumSectsOwned--
			c.Info.MobPoints -= uint64(before.Mobilization)
			c.Evacuated = true
		}
	}
	if !finite(c.SelectedMass) || c.SelectedMass < 0 || !finite(c.OtherMass) || c.OtherMass < 0 {
		return invalid()
	}
	return c, nil
}
