package db

import (
	"reflect"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/model"
)

// CargoStationBuild accepts only the complete affordable prefix of the
// requested build. Allocation tickets are checked again in the write Tx.
type CargoStationBuild struct {
	Input       commandrules.CargoStations
	Allocations []FactoryAllocation
	Ships       []*model.Ship
	Config      *config.Config
}

func (s *Store) CargoStationCandidates(count int) ([]FactoryAllocation, error) {
	return s.constructionShipCandidates(count)
}

func (s *Store) CommitCargoStations(in CargoStationBuild) (CommandOutcome, error) {
	return s.commitShipRecords("CommitCargoStations", "cargo-stations", in.Allocations, func() (preparedFactoryBuild, error) {
		return prepareCargoStations(in)
	})
}

func prepareCargoStations(in CargoStationBuild) (out preparedFactoryBuild, err error) {
	if len(in.Allocations) < 1 || len(in.Allocations) > 2 {
		return out, commandError(CommandInvalid)
	}
	numbers := make([]uint32, 0, len(in.Allocations))
	for _, ticket := range in.Allocations {
		numbers = append(numbers, ticket.Number)
	}
	change, err := commandrules.PlanCargoStations(in.Input, numbers, in.Config)
	if err != nil || !reflect.DeepEqual(change.Ships, in.Ships) {
		return out, commandError(CommandInvalid)
	}
	for _, sh := range change.Ships {
		if err = out.add(BucketShips, ShipKey(sh.Number), nil, sh, true); err != nil {
			return out, err
		}
	}
	ctx := in.Input
	for _, sh := range ctx.Ships {
		after := sh
		selected := sh.Number == ctx.Scope.ShipNo
		if selected {
			after = change.Builder
		}
		if err = out.add(BucketShips, ShipKey(sh.Number), sh, after, selected); err != nil {
			return out, err
		}
	}
	star := *ctx.Star
	star.Ships = numbers[len(numbers)-1]
	if err = out.add(BucketStars, StarKey(star.Number), ctx.Star, &star, true); err != nil {
		return out, err
	}
	if err = out.add(BucketRaces, RaceKey(ctx.Player), ctx.Race, ctx.Race, false); err != nil {
		return out, err
	}
	for _, ticket := range in.Allocations {
		out.checkFree(ticket)
	}
	return out, nil
}
