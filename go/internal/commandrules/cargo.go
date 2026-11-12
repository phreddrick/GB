package commandrules

import (
	"errors"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
)

var ErrCargoInput = errors.New("invalid cargo request")
var ErrCargoBounds = errors.New("cargo amount outside permitted range")
var ErrCargoDeferred = errors.New("active transporter transfer is deferred")

// CargoRequest contains server-derived configuration and a signed C-int request.
// The command grammar, authority, locations and complete transaction witnesses
// are validated separately. Zero AP is owner-approved original-C behavior.
type CargoRequest struct {
	Commodity    byte
	Amount       int64
	Unload       bool
	MassResource float64
}
type CargoChange struct {
	Amount, Lower, Upper    int64
	Selected, Other         uint16
	SelectedMass, OtherMass float64
}

func CargoResourceCapacity(s *model.Ship) int64 {
	if s.Type == model.OtypeFactory {
		return model.Shipdata[model.OtypeFactory][model.AbilCargo]
	}
	return int64(s.MaxResource)
}
func CargoOverloaded(s *model.Ship) bool {
	fuel, destruct := float64(s.MaxFuel), int64(s.MaxDestruct)
	if s.Type == model.OtypeFactory {
		fuel = float64(model.Shipdata[model.OtypeFactory][model.AbilFuelCap])
		destruct = model.Shipdata[model.OtypeFactory][model.AbilDestCap]
	}
	return int64(s.Resource) > CargoResourceCapacity(s) || s.Fuel > fuel || int64(s.Popn)+int64(s.Troops) > int64(s.MaxCrew) || int64(s.Destruct) > destruct
}
func cargoWord(n int64) (uint16, error) {
	if n < 0 || n > math.MaxUint16 {
		return 0, ErrCargoInput
	}
	return uint16(n), nil
}

// PlanCargo mirrors load.c signed limits and default ordering. Crystals use
// ships.h Max_crystals=127; '&' is the explicitly approved working alias for x.
// Resource loading from foreign ships and nonzero foreign crystal transfers
// are forbidden; resource unloading to foreign ships remains legal.
func PlanCargo(s, other *model.Ship, planet *model.Planet, player int, r CargoRequest) (CargoChange, error) {
	var c CargoChange
	if s == nil || int(s.Type) >= len(model.Shipdata) || player < 1 || player > model.MaxPlayers || (other == nil) == (planet == nil) || !finite(r.MassResource) || r.MassResource < 0 || !finite(s.Mass) || s.Mass < 0 || r.Amount < math.MinInt32 || r.Amount > math.MaxInt32 {
		return c, ErrCargoInput
	}
	if r.Commodity != 'r' && r.Commodity != 'x' && r.Commodity != '&' {
		return c, ErrCargoInput
	}
	if s.Type == model.OtypeTransDev && s.On && (s.Special.Transport == nil || s.Special.Transport.Target != 0) {
		return c, ErrCargoDeferred
	}
	amount := r.Amount
	if r.Unload {
		if amount == math.MinInt32 {
			return c, ErrCargoInput
		}
		amount = -amount
	}
	if amount < 0 && s.Type == model.OtypeVN {
		return c, ErrCargoInput
	}
	var a, b, capacity, otherCapacity int64
	if r.Commodity == 'r' {
		a = int64(s.Resource)
		capacity = CargoResourceCapacity(s)
	} else {
		a = int64(s.Crystals)
		capacity = 127
	}
	if other != nil {
		if int(other.Type) >= len(model.Shipdata) || !finite(other.Mass) || other.Mass < 0 {
			return c, ErrCargoInput
		}
		c.OtherMass = other.Mass
		if r.Commodity == 'r' {
			b = int64(other.Resource)
			otherCapacity = CargoResourceCapacity(other)
		} else {
			b = int64(other.Crystals)
			otherCapacity = 127
		}
		if other.Owner != s.Owner {
			c.Upper = 0
		} else if r.Commodity == 'r' && s.Type == model.StypeShuttle && s.WhatOrbits != model.LevelShip {
			c.Upper = b
		} else {
			c.Upper = min(b, capacity-a)
		}
		if r.Commodity != 'r' && other.Owner != s.Owner {
			c.Lower = 0
		} else if r.Commodity == 'r' && other.Type == model.StypeShuttle && s.WhatOrbits != model.LevelShip {
			c.Lower = -a
		} else {
			c.Lower = -min(a, otherCapacity-b)
		}
	} else {
		if r.Commodity == 'r' {
			b = int64(planet.Info[player-1].Resource)
		} else {
			b = int64(planet.Info[player-1].Crystals)
		}
		c.Upper = min(b, capacity-a)
		c.Lower = -a
	}
	if amount < c.Lower || amount > c.Upper {
		return c, ErrCargoBounds
	}
	if amount == 0 {
		if r.Unload {
			amount = c.Lower
		} else {
			amount = c.Upper
		}
	}
	var e error
	c.Selected, e = cargoWord(a + amount)
	if e != nil {
		return c, e
	}
	c.Other, e = cargoWord(b - amount)
	if e != nil {
		return c, e
	}
	c.Amount = amount
	c.SelectedMass = s.Mass
	if r.Commodity == 'r' {
		c.SelectedMass += float64(amount) * r.MassResource
		// Retain the intended Go aggregate exemption, explicitly owner-approved.
		// C passed Boolean sh=1 rather than other.Number to landed_on().
		if other != nil && (s.WhatOrbits != model.LevelShip || s.DestShipNo != other.Number) {
			c.OtherMass -= float64(amount) * r.MassResource
		}
	}
	if !finite(c.SelectedMass) || c.SelectedMass < 0 || !finite(c.OtherMass) || c.OtherMass < 0 {
		return c, ErrCargoInput
	}
	return c, nil
}
