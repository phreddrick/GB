package commandrules

import (
	"errors"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
)

// HAB_FACT_ON_COST and HAB_FACT_SIZE from source/tweakables.h:118-121.
const (
	HabitatFactoryOnCost = 4
	HabitatFactorySize   = 0.2
)

var (
	ErrHabitatResources = errors.New("insufficient habitat resources")
	ErrHabitatHangar    = errors.New("insufficient habitat hanger space")
	// ErrHabitatHangarState marks a stored hanger/size pair that original C
	// would process only through unsigned-short wraparound (hanger smaller than
	// the docked factory, or a result outside the record width). Such a state
	// is refused rather than reproduced.
	ErrHabitatHangarState = errors.New("inconsistent habitat hanger accounting")
	errHabitatInvalid     = errors.New("invalid habitat activation inputs")
)

// SizeFactors are the authoritative server SIZE_* rates (tweakables.h:110-114).
type SizeFactors struct{ Guns, Crew, Resource, Fuel, Destruct float64 }

func (f SizeFactors) valid() bool {
	for _, x := range []float64{f.Guns, f.Crew, f.Resource, f.Fuel, f.Destruct} {
		if !finite(x) || x < 0 {
			return false
		}
	}
	return true
}

// ShipSize mirrors ship_size() in source/build.c:1451-1462 (and State.shipSize):
// a double sum truncated toward zero to int. All operands are unsigned record
// fields, so the sum is finite and non-negative for valid factors.
func ShipSize(sh *model.Ship, f SizeFactors) (int, error) {
	if sh == nil || !f.valid() {
		return 0, errHabitatInvalid
	}
	size := 1.0 + f.Guns*float64(sh.Primary) + f.Guns*float64(sh.Secondary) + f.Crew*float64(sh.MaxCrew) + f.Resource*float64(sh.MaxResource) + f.Fuel*float64(sh.MaxFuel) + f.Destruct*float64(sh.MaxDestruct) + float64(sh.MaxHanger)
	if !finite(size) || size < 0 || size > math.MaxInt32 {
		return 0, errHabitatInvalid
	}
	return int(size), nil
}

// HabitatChange is the complete arithmetic result of one habitat activation:
// the only fields the operation may change are the factory's Size (plus
// On/Notified, set by the caller) and the carrier's Resource and Hanger.
// Carrier mass is unchanged, as in source/order.c:519-551.
type HabitatChange struct {
	Cost         uint32 // HAB_FACT_ON_COST * build_cost, C unsigned int
	HangerNeeded int64  // C int: may be negative (space to spare)
	Size         uint16 // new factory size: 1 + (int)(HAB_FACT_SIZE * ship_size)
	Hanger       uint16 // new carrier hanger: hanger - old size + new size
	Resource     uint16 // new carrier resource: resource - cost
}

// PlanHabitatActivation reproduces the defined arithmetic of order.c:528-551
// with wide signed intermediates. In C every operand is an unsigned short
// promoted to int, so (max_hanger - hanger) is signed and never wraps; the
// old Go uint16 subtraction could. oncost is a C unsigned int, never a uint16
// temporary. Identity and authority are the callers' responsibility; this
// function checks only what the arithmetic depends on.
func PlanHabitatActivation(factory, carrier *model.Ship, f SizeFactors) (HabitatChange, error) {
	var out HabitatChange
	if factory == nil || carrier == nil || factory.Type != model.OtypeFactory || carrier.Type != model.StypeHabitat || factory.Number == carrier.Number || factory.DestShipNo != carrier.Number || factory.WhatOrbits != model.LevelShip || !f.valid() {
		return out, errHabitatInvalid
	}
	out.Cost = uint32(HabitatFactoryOnCost) * uint32(factory.BuildCost)
	if uint32(carrier.Resource) < out.Cost {
		return out, ErrHabitatResources
	}
	size, e := ShipSize(factory, f)
	if e != nil {
		return out, e
	}
	newSize := 1 + int(HabitatFactorySize*float64(size))
	if newSize < 1 || newSize > math.MaxUint16 {
		return out, ErrHabitatHangarState
	}
	free := int64(carrier.MaxHanger) - int64(carrier.Hanger)
	out.HangerNeeded = int64(newSize) - (free + int64(factory.Size))
	if out.HangerNeeded > 0 {
		return out, ErrHabitatHangar
	}
	if carrier.Hanger < factory.Size {
		return out, ErrHabitatHangarState
	}
	hanger := int64(carrier.Hanger) - int64(factory.Size) + int64(newSize)
	if hanger < 0 || hanger > math.MaxUint16 {
		return out, ErrHabitatHangarState
	}
	out.Size = uint16(newSize)
	out.Hanger = uint16(hanger)
	out.Resource = uint16(uint32(carrier.Resource) - out.Cost) //nolint:gosec // cost <= Resource <= MaxUint16 above
	return out, nil
}
