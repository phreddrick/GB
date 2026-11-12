package commandrules

import (
	"errors"
	"math"
	"testing"

	"github.com/phreddrick/GB/go/internal/model"
)

var rates = SizeFactors{Guns: .1, Crew: .01, Resource: .02, Fuel: .01, Destruct: .02}

func habitatPair() (*model.Ship, *model.Ship) {
	f := &model.Ship{Number: 77, Type: model.OtypeFactory, WhatOrbits: model.LevelShip, Docked: true, DestShipNo: 70, BuildCost: 7, MaxCrew: 20, MaxResource: 50, Size: 2}
	c := &model.Ship{Number: 70, Type: model.StypeHabitat, MaxHanger: 10, Hanger: 2, Resource: 100}
	return f, c
}

// Defined order.c:528-551 arithmetic: cost 4*7, ship_size 2.2 -> 2, new size
// 1 + (int)(0.2*2) = 1, hanger 2-2+1 = 1, resource 100-28 = 72; mass untouched.
func TestHabitatDefinedSemantics(t *testing.T) {
	f, c := habitatPair()
	size, e := ShipSize(f, rates)
	if e != nil || size != 2 {
		t.Fatal("ship_size", size, e)
	}
	got, e := PlanHabitatActivation(f, c, rates)
	if e != nil || got != (HabitatChange{Cost: 28, HangerNeeded: -9, Size: 1, Hanger: 1, Resource: 72}) {
		t.Fatalf("defined arithmetic changed: %+v %v", got, e)
	}
	if f.Size != 2 || c.Hanger != 2 || c.Resource != 100 {
		t.Fatal("inputs mutated")
	}
}

func TestHabitatCWidthBoundaries(t *testing.T) {
	// oncost is a C unsigned int: 4*65535 = 262140 can never be paid from a
	// uint16 stockpile; a uint16 temporary would have wrapped 4*16384 to 0.
	for _, cost := range []uint16{16384, 32768, 65535} {
		f, c := habitatPair()
		f.BuildCost = cost
		c.Resource = 65535
		got, e := PlanHabitatActivation(f, c, rates)
		if !errors.Is(e, ErrHabitatResources) || got.Cost != uint32(cost)*4 {
			t.Fatal("overflow wrapped to an affordable cost", cost, got, e)
		}
	}
	f, c := habitatPair()
	f.BuildCost = 16383
	c.Resource = 65535
	if got, e := PlanHabitatActivation(f, c, rates); e != nil || got.Cost != 65532 || got.Resource != 3 {
		t.Fatal("largest payable cost", got, e)
	}
	f, c = habitatPair()
	f.BuildCost = 0
	c.Resource = 0
	if got, e := PlanHabitatActivation(f, c, rates); e != nil || got.Cost != 0 || got.Resource != 0 {
		t.Fatal("zero cost activation", got, e)
	}
}

func TestHabitatHangerSignedArithmetic(t *testing.T) {
	// Over-full habitat: C (10-12)+2 = 0 free -> needed 1 -> refused with the
	// C number; uint16 subtraction would have produced 65534 free.
	f, c := habitatPair()
	c.Hanger = 12
	if got, e := PlanHabitatActivation(f, c, rates); !errors.Is(e, ErrHabitatHangar) || got.HangerNeeded != 1 {
		t.Fatal("over-full hanger not refused with C arithmetic", got, e)
	}
	// Exactly full: free 0 + own 2 = 2 >= 1.
	f, c = habitatPair()
	c.Hanger = 10
	if got, e := PlanHabitatActivation(f, c, rates); e != nil || got.Hanger != 9 {
		t.Fatal("exactly full habitat", got, e)
	}
	// Large factory: ship_size with max hanger 65535 is 65536+... -> size
	// bounded; new size must still fit uint16 and the free-space test holds.
	f, c = habitatPair()
	f.MaxHanger = 65535
	f.Size = 65535
	c.MaxHanger = 65535
	c.Hanger = 65535
	size, e := ShipSize(f, rates)
	if e != nil || size != 65537 {
		t.Fatal("ship_size", size, e)
	}
	got, e := PlanHabitatActivation(f, c, rates)
	if e != nil || got.Size != 13108 || got.Hanger != got.Size {
		t.Fatal("large factory", got, e)
	}
	// Every input width at its maximum, with the default C SIZE_* rates:
	// 1 + 2*(.1*255) + (.01+.02+.01+.02)*65535 + 65535
	// = 69519.1 -> 69519; 1 + int(.2*69519) = 13904.
	f.Primary, f.Secondary = 255, 255
	f.MaxCrew, f.MaxResource, f.MaxFuel, f.MaxDestruct = 65535, 65535, 65535, 65535
	size, e = ShipSize(f, rates)
	if e != nil || size != 69519 {
		t.Fatal("maximum-width ship_size", size, e)
	}
	got, e = PlanHabitatActivation(f, c, rates)
	if e != nil || got != (HabitatChange{Cost: 28, HangerNeeded: -51631, Size: 13904, Hanger: 13904, Resource: 72}) {
		t.Fatal("maximum-width factory", got, e)
	}
}

func TestHabitatMalformedStateRefused(t *testing.T) {
	// hanger smaller than the docked factory's size: C would wrap unsigned short.
	f, c := habitatPair()
	c.Hanger = 1
	if _, e := PlanHabitatActivation(f, c, rates); !errors.Is(e, ErrHabitatHangarState) {
		t.Fatal("hanger below docked size accepted", e)
	}
	// Zero-size stored factory is defined: new size 1 from free space.
	f, c = habitatPair()
	f.Size = 0
	c.Hanger = 0
	if got, e := PlanHabitatActivation(f, c, rates); e != nil || got.Hanger != 1 {
		t.Fatal("zero size", got, e)
	}
	for name, mutate := range map[string]func(f, c *model.Ship) SizeFactors{
		"nil factory":   func(f, c *model.Ship) SizeFactors { return rates },
		"not a factory": func(f, c *model.Ship) SizeFactors { f.Type = model.StypeCargo; return rates },
		"not a habitat": func(f, c *model.Ship) SizeFactors { c.Type = model.StypeCargo; return rates },
		"other carrier": func(f, c *model.Ship) SizeFactors { f.DestShipNo = 71; return rates },
		"self":          func(f, c *model.Ship) SizeFactors { c.Number = f.Number; f.DestShipNo = f.Number; return rates },
		"not carried":   func(f, c *model.Ship) SizeFactors { f.WhatOrbits = model.LevelPlan; return rates },
		"nan rate":      func(f, c *model.Ship) SizeFactors { r := rates; r.Guns = math.NaN(); return r },
		"negative rate": func(f, c *model.Ship) SizeFactors { r := rates; r.Crew = -1; return r },
		"inf rate":      func(f, c *model.Ship) SizeFactors { r := rates; r.Resource = math.Inf(1); return r },
	} {
		t.Run(name, func(t *testing.T) {
			f, c := habitatPair()
			r := mutate(f, c)
			if name == "nil factory" {
				f = nil
			}
			if _, e := PlanHabitatActivation(f, c, r); e == nil || errors.Is(e, ErrHabitatResources) || errors.Is(e, ErrHabitatHangar) {
				t.Fatal("invalid input accepted", e)
			}
		})
	}
	if _, e := ShipSize(nil, rates); e == nil {
		t.Fatal("nil ship size")
	}
}
