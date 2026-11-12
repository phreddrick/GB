package commandrules

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/phreddrick/GB/go/internal/model"
)

func cargoPair() (*model.Ship, *model.Ship, *model.Planet) {
	s := &model.Ship{Number: 77, Type: model.StypeCargo, Owner: 1, Resource: 10, Crystals: 10, MaxResource: 50, Mass: 100, WhatOrbits: model.LevelPlan}
	o := &model.Ship{Number: 70, Type: model.StypeCargo, Owner: 1, Resource: 30, Crystals: 30, MaxResource: 50, Mass: 200}
	p := &model.Planet{}
	p.Info[0].Resource = 100
	p.Info[0].Crystals = 100
	return s, o, p
}
func TestCargoSignedDefaultsAndConservation(t *testing.T) {
	for _, ships := range []bool{false, true} {
		for _, commodity := range []byte{'r', 'x', '&'} {
			for _, unload := range []bool{false, true} {
				for _, amount := range []int64{-2, 0, 2} {
					s, o, p := cargoPair()
					beforeS, beforeO, beforeP := *s, *o, *p
					req := CargoRequest{Commodity: commodity, Amount: amount, Unload: unload, MassResource: .1}
					target := o
					planet := p
					if ships {
						planet = nil
					} else {
						target = nil
					}
					delta := amount
					if unload {
						delta = -delta
					}
					if amount == 0 {
						if unload {
							delta = -10
						} else if ships {
							delta = 30
						} else if commodity == 'r' {
							delta = 40
						} else {
							delta = 100
						}
					}
					c, e := PlanCargo(s, target, planet, 1, req)
					if e != nil || int64(c.Selected) != 10+delta || int64(c.Other) != (func() int64 {
						if ships {
							return 30
						}
						return 100
					})()-delta || c.Amount != delta {
						t.Fatalf("ships=%t commodity=%c unload=%t amount=%d: %+v %v", ships, commodity, unload, amount, c, e)
					}
					mass := 100.
					otherMass := 0.
					if ships {
						otherMass = 200
					}
					if commodity == 'r' {
						mass += float64(delta) * .1
						if ships {
							otherMass -= float64(delta) * .1
						}
					}
					if c.SelectedMass != mass || c.OtherMass != otherMass {
						t.Fatal("mass", c)
					}
					if !reflect.DeepEqual(*s, beforeS) || !reflect.DeepEqual(*o, beforeO) || !reflect.DeepEqual(*p, beforeP) {
						t.Fatal("pure calculation mutated inputs")
					}
				}
			}
		}
	}
}
func TestCargoCapacityOwnershipAndBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		commodity byte
		unload    bool
		amount    int64
		edit      func(*model.Ship, *model.Ship, *model.Planet)
		planet    bool
		delta     int64
		err       error
	}{
		{name: "crystal127", commodity: 'x', edit: func(s, o *model.Ship, p *model.Planet) { s.Crystals = 126; s.Mount = 0 }, delta: 1},
		{name: "empty", commodity: 'r', edit: func(s, o *model.Ship, p *model.Planet) { o.Resource = 0 }, delta: 0},
		{name: "full", commodity: 'r', edit: func(s, o *model.Ship, p *model.Planet) { s.Resource = 50 }, delta: 0},
		{name: "nearlyfull", commodity: 'r', edit: func(s, o *model.Ship, p *model.Planet) { s.Resource = 49 }, delta: 1},
		{name: "foreigngift", commodity: 'r', unload: true, amount: 2, edit: func(s, o *model.Ship, p *model.Planet) { o.Owner = 2 }, delta: -2},
		{name: "foreignnegativegift", commodity: 'r', amount: -2, edit: func(s, o *model.Ship, p *model.Planet) { o.Owner = 2 }, delta: -2},
		{name: "foreignload", commodity: 'r', amount: 2, edit: func(s, o *model.Ship, p *model.Planet) { o.Owner = 2 }, err: ErrCargoBounds},
		{name: "foreignnegativeunload", commodity: 'r', unload: true, amount: -2, edit: func(s, o *model.Ship, p *model.Planet) { o.Owner = 2 }, err: ErrCargoBounds},
		{name: "foreigncrystalload", commodity: 'x', amount: 1, edit: func(s, o *model.Ship, p *model.Planet) { o.Owner = 2 }, err: ErrCargoBounds},
		{name: "foreigncrystalunload", commodity: '&', unload: true, amount: 1, edit: func(s, o *model.Ship, p *model.Planet) { o.Owner = 2 }, err: ErrCargoBounds},
		{name: "foreignzero", commodity: 'x', edit: func(s, o *model.Ship, p *model.Planet) { o.Owner = 2 }, delta: 0},
		{name: "overfullzero", commodity: 'r', edit: func(s, o *model.Ship, p *model.Planet) { s.Resource = 55 }, err: ErrCargoBounds},
		{name: "overfullunload", commodity: 'r', amount: -5, edit: func(s, o *model.Ship, p *model.Planet) { s.Resource = 55 }, delta: -5},
		{name: "shuttleexternal", commodity: 'r', edit: func(s, o *model.Ship, p *model.Planet) { s.Type = model.StypeShuttle; s.MaxResource = 5 }, delta: 30},
		{name: "shuttlecontained", commodity: 'r', edit: func(s, o *model.Ship, p *model.Planet) {
			s.Type = model.StypeShuttle
			s.WhatOrbits = model.LevelShip
			s.MaxResource = 5
		}, err: ErrCargoBounds},
		{name: "shuttleplanet", commodity: 'r', planet: true, edit: func(s, o *model.Ship, p *model.Planet) { s.Type = model.StypeShuttle; s.MaxResource = 5 }, err: ErrCargoBounds},
		{name: "partnerShuttle", commodity: 'r', unload: true, edit: func(s, o *model.Ship, p *model.Planet) { o.Type = model.StypeShuttle; o.MaxResource = 0 }, delta: -10},
		{name: "planetoverflow", commodity: 'r', unload: true, amount: 1, planet: true, edit: func(s, o *model.Ship, p *model.Planet) { p.Info[0].Resource = 65535 }, err: ErrCargoInput},
		{name: "shuttlefieldoverflow", commodity: 'r', edit: func(s, o *model.Ship, p *model.Planet) {
			s.Type = model.StypeShuttle
			s.Resource = 65535
			o.Resource = 1
		}, err: ErrCargoInput},
		{name: "VNnegative", commodity: 'r', amount: -1, edit: func(s, o *model.Ship, p *model.Planet) { s.Type = model.OtypeVN }, err: ErrCargoInput},
		{name: "VNdefaultunload", commodity: 'r', unload: true, edit: func(s, o *model.Ship, p *model.Planet) { s.Type = model.OtypeVN }, delta: -10},
		{name: "factorycapacity", commodity: 'r', edit: func(s, o *model.Ship, p *model.Planet) { s.Type = model.OtypeFactory; s.MaxResource = 0 }, delta: 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, o, p := cargoPair()
			tt.edit(s, o, p)
			before := s.Resource
			if tt.commodity != 'r' {
				before = s.Crystals
			}
			if tt.planet {
				o = nil
			} else {
				p = nil
			}
			c, e := PlanCargo(s, o, p, 1, CargoRequest{Commodity: tt.commodity, Amount: tt.amount, Unload: tt.unload, MassResource: .1})
			if tt.err != nil {
				if !errors.Is(e, tt.err) {
					t.Fatal(c, e, tt.err)
				}
			} else if e != nil || c.Amount != tt.delta || int64(c.Selected) != int64(before)+tt.delta {
				t.Fatal(c, e)
			}
		})
	}
	for _, x := range []float64{math.NaN(), math.Inf(1), -1} {
		s, o, _ := cargoPair()
		if _, e := PlanCargo(s, o, nil, 1, CargoRequest{Commodity: 'r', MassResource: x}); e == nil {
			t.Fatal("invalid rate")
		}
	}
	for _, amount := range []int64{math.MinInt64, math.MaxInt64, math.MinInt32, math.MaxInt32} {
		s, o, _ := cargoPair()
		if _, e := PlanCargo(s, o, nil, 1, CargoRequest{Commodity: 'r', Amount: amount, Unload: true, MassResource: .1}); e == nil {
			t.Fatal("overflow/range accepted")
		}
	}
	s, o, _ := cargoPair()
	s.WhatOrbits = model.LevelShip
	s.DestShipNo = o.Number
	c, e := PlanCargo(s, o, nil, 1, CargoRequest{Commodity: 'r', Amount: 2, MassResource: .1})
	if e != nil || c.OtherMass != o.Mass {
		t.Fatal("owner-approved containing-carrier aggregate exemption", c, e)
	}
}
