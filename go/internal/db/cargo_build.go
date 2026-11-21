package db

import (
	"reflect"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/model"
)

// CargoCarrierBuild accepts one allocation ticket and one child. All debits
// and the orbit-head change are derived here from checked preimages.
type CargoCarrierBuild struct {
	Input      commandrules.CargoBuild
	Allocation FactoryAllocation
	Child      *model.Ship
	Config     *config.Config
}

func (s *Store) CommitCargoCarrier(in CargoCarrierBuild) (CommandOutcome, error) {
	return s.commitConstruction("CommitCargoCarrier", "cargo-build", in.Allocation, func() (preparedFactoryBuild, error) { return prepareCargoBuild(in) })
}

func prepareCargoBuild(in CargoCarrierBuild) (out preparedFactoryBuild, err error) {
	ctx := in.Input
	child, e := commandrules.PlanCargoBuild(ctx, in.Allocation.Number, in.Config)
	if e != nil || !reflect.DeepEqual(child, in.Child) {
		return out, commandError(CommandInvalid)
	}
	if e = out.add(BucketShips, ShipKey(child.Number), nil, child, true); e != nil {
		return out, e
	}
	for _, sh := range ctx.Ships {
		selected := sh.Number == ctx.Scope.ShipNo
		next := *sh
		if selected {
			next.Resource -= child.BuildCost
			if !ctx.Race.God {
				next.Popn -= child.Popn
				next.Fuel -= child.Fuel
			}
		}
		if e = out.add(BucketShips, ShipKey(sh.Number), sh, &next, selected); e != nil {
			return out, e
		}
	}
	if st := ctx.Star; st != nil {
		next := *st
		if ctx.Planet == nil {
			next.Ships = child.Number
		}
		if e = out.add(BucketStars, StarKey(st.Number), st, &next, ctx.Planet == nil); e != nil {
			return out, e
		}
	}
	if pl := ctx.Planet; pl != nil {
		next := *pl
		next.Ships = child.Number
		if e = out.add(BucketPlanets, PlanetKey(pl.StarNum, pl.PlanetNum), pl, &next, true); e != nil {
			return out, e
		}
	}
	if d := ctx.Universe; d != nil {
		next := *d
		next.Ships = child.Number
		if e = out.add(BucketSData, starDataKey, d, &next, true); e != nil {
			return out, e
		}
	}
	if e = out.add(BucketRaces, RaceKey(ctx.Player), ctx.Race, ctx.Race, false); e != nil {
		return out, e
	}
	out.checkFree(in.Allocation)
	return out, nil
}
