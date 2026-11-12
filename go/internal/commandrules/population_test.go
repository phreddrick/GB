package commandrules

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/phreddrick/GB/go/internal/model"
)

func populationPair() (*model.Ship, *model.Ship, *model.Planet, *model.Race) {
	a, b, _ := cargoPair()
	a.Popn = 10
	a.Troops = 10
	a.MaxCrew = 50
	a.LandX = 0
	a.LandY = 0
	b.Popn = 30
	b.Troops = 20
	b.MaxCrew = 100
	p := &model.Planet{MaxX: 1, MaxY: 1, Popn: 30, Troops: 20, Sectors: []model.Sector{{Owner: 1, Race: 2, Popn: 30, Troops: 20, Mobilization: 7}}}
	p.Info[0] = model.PlInfo{Popn: 30, Troops: 20, NumSectsOwned: 1, MobPoints: 7}
	r := &model.Race{PlayerNum: 1, Mass: 1.25}
	return a, b, p, r
}
func TestPopulationRulesMatrix(t *testing.T) {
	for _, ships := range []bool{false, true} {
		for _, commodity := range []byte{'c', 'm'} {
			for _, unload := range []bool{false, true} {
				for _, amount := range []int64{-2, 0, 2} {
					a, b, p, r := populationPair()
					if ships {
						p = nil
					} else {
						b = nil
					}
					before, _ := json.Marshal([]any{a, b, p, r})
					delta := amount
					if unload {
						delta = -delta
					}
					if amount == 0 {
						if unload {
							delta = -10
						} else if commodity == 'c' {
							delta = 30
						} else {
							delta = 20
						}
					}
					c, e := PlanPopulation(a, b, p, r, PopulationRequest{Commodity: commodity, Amount: amount, Unload: unload})
					stock := int64(30)
					if commodity == 'm' {
						stock = 20
					}
					if e != nil || int64(c.Selected) != 10+delta || int64(c.Other) != stock-delta || c.Amount != delta || c.SelectedMass != 100+float64(delta)*1.25 {
						t.Fatal("C limits/conservation/mass", c, e)
					}
					if b != nil && c.OtherMass != 200-float64(delta)*1.25 {
						t.Fatal("peer mass", c)
					}
					after, _ := json.Marshal([]any{a, b, p, r})
					if string(before) != string(after) {
						t.Fatal("pure rule mutated inputs")
					}
				}
			}
		}
	}
}
func TestPopulationRulesBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		edit    func(*model.Ship, *model.Ship, *model.Planet, *model.Race)
		request PopulationRequest
		planet  bool
		want    error
	}{
		{name: "foreign take", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) { b.Owner = 2 }, request: PopulationRequest{'c', 2, false}, want: ErrPopulationBounds},
		{name: "foreign give", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) { b.Owner = 2 }, request: PopulationRequest{'m', 2, true}, want: ErrPopulationBounds},
		{name: "foreign zero", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) { b.Owner = 2 }, request: PopulationRequest{'c', 0, false}},
		{name: "planet overflow", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) { p.Popn = math.MaxUint64 }, planet: true, request: PopulationRequest{'c', 1, true}, want: ErrPopulationInput},
		{name: "planet wide valid", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) { p.Popn = math.MaxUint64 }, planet: true, request: PopulationRequest{'c', 1, false}},
		{name: "player overflow", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) { p.Info[0].Troops = math.MaxUint32 }, planet: true, request: PopulationRequest{'m', 1, true}, want: ErrPopulationInput},
		{name: "aggregate underflow", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) { p.Popn = 0 }, planet: true, request: PopulationRequest{'c', 1, false}, want: ErrPopulationInput},
		{name: "counter underflow", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) {
			p.Sectors[0].Troops = 0
			p.Info[0].NumSectsOwned = 0
		}, planet: true, request: PopulationRequest{'c', 30, false}, want: ErrPopulationInput},
		{name: "mob underflow", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) {
			p.Sectors[0].Troops = 0
			p.Info[0].MobPoints = 0
		}, planet: true, request: PopulationRequest{'c', 30, false}, want: ErrPopulationInput},
		{name: "neutral claim overflow", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) {
			p.Sectors[0].Owner = 0
			p.Sectors[0].Popn = 0
			p.Sectors[0].Troops = 0
			p.Info[0].NumSectsOwned = math.MaxUint16
		}, planet: true, request: PopulationRequest{'m', 1, true}, want: ErrPopulationInput},
		{name: "neutral mob overflow", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) {
			p.Sectors[0].Owner = 0
			p.Sectors[0].Popn = 0
			p.Sectors[0].Troops = 0
			p.Info[0].MobPoints = math.MaxUint64
		}, planet: true, request: PopulationRequest{'c', 1, true}, want: ErrPopulationInput},
		{name: "sector overflow", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) { p.Sectors[0].Popn = math.MaxUint16 }, planet: true, request: PopulationRequest{'c', 1, true}, want: ErrPopulationInput},
		{name: "foreign sector", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) { p.Sectors[0].Owner = 2 }, planet: true, request: PopulationRequest{'c', 0, true}, want: ErrPopulationDeferred},
		{name: "factory", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) {
			a.Type = model.OtypeFactory
			a.MaxCrew = 65535
			a.Popn = 10
			a.Troops = 10
		}, request: PopulationRequest{'m', 1, false}, want: ErrPopulationBounds},
		{name: "no shuttle exception", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) { a.Type = model.StypeShuttle; a.MaxCrew = 20 }, request: PopulationRequest{'c', 1, false}, want: ErrPopulationBounds},
		{name: "VN explicit", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) { a.Type = model.OtypeVN }, request: PopulationRequest{'c', 1, true}, want: ErrPopulationInput},
		{name: "VN default", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) { a.Type = model.OtypeVN }, request: PopulationRequest{'c', 0, true}},
		{name: "wide sum", edit: func(a, b *model.Ship, p *model.Planet, r *model.Race) {
			p.Sectors[0].Popn = 65535
			p.Sectors[0].Troops = 1
			p.Popn = 65535
			p.Troops = 1
			p.Info[0].Popn = 65535
			p.Info[0].Troops = 1
			a.Popn = 0
			a.Troops = 0
			a.MaxCrew = 65535
		}, planet: true, request: PopulationRequest{'c', 65535, false}},
	}
	for _, x := range tests {
		t.Run(x.name, func(t *testing.T) {
			a, b, p, r := populationPair()
			x.edit(a, b, p, r)
			if x.planet {
				b = nil
			} else {
				p = nil
			}
			_, e := PlanPopulation(a, b, p, r, x.request)
			if !errors.Is(e, x.want) {
				t.Fatal(e, x.want)
			}
		})
	}
	for _, mass := range []float64{math.NaN(), math.Inf(1), -1, math.MaxFloat64} {
		a, b, _, r := populationPair()
		r.Mass = mass
		if _, e := PlanPopulation(a, b, nil, r, PopulationRequest{'c', 2, false}); e == nil {
			t.Fatal("invalid/overflowing mass")
		}
	}
	for _, amount := range []int64{math.MinInt64, math.MaxInt64, math.MinInt32} {
		a, b, _, r := populationPair()
		if _, e := PlanPopulation(a, b, nil, r, PopulationRequest{'m', amount, true}); e == nil {
			t.Fatal("invalid amount")
		}
	}
	a, b, _, r := populationPair()
	a.WhatOrbits = model.LevelShip
	a.DestShipNo = b.Number
	c, e := PlanPopulation(a, b, nil, r, PopulationRequest{'c', 2, false})
	if e != nil || c.OtherMass != b.Mass {
		t.Fatal("aggregate mass exemption", c, e)
	}
	a, _, p, r := populationPair()
	before := *p.SectorAt(0, 0)
	c, e = PlanPopulation(a, nil, p, r, PopulationRequest{'m', 2, true})
	if e != nil || c.Sector.Race != before.Race || !reflect.DeepEqual(c.Sector.Type, before.Type) {
		t.Fatal("unrelated sector fields", e)
	}
}

func TestPopulationWideResidentsNeverAliasEmpty(t *testing.T) {
	a, _, p, r := populationPair()
	p.Sectors[0].Popn = 65535
	p.Sectors[0].Troops = 1
	p.Popn = 65535
	p.Troops = 1
	p.Info[0].Popn = 65535
	p.Info[0].Troops = 1
	a.Popn = 0
	a.Troops = 0
	a.MaxCrew = 0
	c, e := PlanPopulation(a, nil, p, r, PopulationRequest{'c', 0, false})
	if e != nil || c.Evacuated || c.Sector.Owner != 1 || c.Info.NumSectsOwned != 1 || c.Info.MobPoints != 7 {
		t.Fatal("wide resident sum treated as empty", c, e)
	}
	p.Sectors[0].Owner = 0
	p.Info[0].NumSectsOwned = 0
	p.Info[0].MobPoints = 0
	p.Info[0].Popn = 0
	p.Info[0].Troops = 0
	a.Popn = 1
	a.Troops = 10
	a.MaxCrew = 50
	c, e = PlanPopulation(a, nil, p, r, PopulationRequest{'m', 1, true})
	if e != nil || c.Colonized || c.Sector.Owner != 0 || c.Info.NumSectsOwned != 0 || c.Info.MobPoints != 0 || c.Sector.Troops != 2 || c.Info.Troops != 1 {
		t.Fatal("invented neutral capture/accounting", c, e)
	}
}
