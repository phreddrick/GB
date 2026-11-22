package db

import (
	"reflect"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/model"
)

// CarrierProbeBuild accepts only one robotic probe inside a planet-orbiting
// Carrier. The transaction derives the Carrier writes; AP and orbit are CAS only.
type CarrierProbeBuild struct {
	Input      commandrules.ProbeBuild
	Allocation FactoryAllocation
	Child      *model.Ship
	Config     *config.Config
}

func (s *Store) CommitCarrierProbe(in CarrierProbeBuild) (CommandOutcome, error) {
	return s.commitConstruction("CommitCarrierProbe", "probe-build", in.Allocation, func() (preparedFactoryBuild, error) { return prepareProbeBuild(in) })
}
func prepareProbeBuild(in CarrierProbeBuild) (out preparedFactoryBuild, err error) {
	ctx := in.Input
	c, e := commandrules.PlanProbeBuild(ctx, in.Allocation.Number, in.Config)
	if e != nil || !reflect.DeepEqual(c, in.Child) {
		return out, commandError(CommandInvalid)
	}
	if e = out.add(BucketShips, ShipKey(c.Number), nil, c, true); e != nil {
		return out, e
	}
	for _, b := range ctx.Orbit {
		after := *b
		selected := b.Number == c.DestShipNo
		if selected {
			commandrules.InstallProbeDebits(&after, c)
		}
		if e = out.add(BucketShips, ShipKey(b.Number), b, &after, selected); e != nil {
			return out, e
		}
	}
	for _, sh := range ctx.Children {
		if e = out.add(BucketShips, ShipKey(sh.Number), sh, sh, false); e != nil {
			return out, e
		}
	}
	if e = out.add(BucketPlanets, PlanetKey(ctx.Planet.StarNum, ctx.Planet.PlanetNum), ctx.Planet, ctx.Planet, false); e != nil {
		return out, e
	}
	if e = out.add(BucketStars, StarKey(ctx.Star.Number), ctx.Star, ctx.Star, false); e != nil {
		return out, e
	}
	if e = out.add(BucketRaces, RaceKey(ctx.Player), ctx.Race, ctx.Race, false); e != nil {
		return out, e
	}
	out.checkFree(in.Allocation)
	return out, nil
}
