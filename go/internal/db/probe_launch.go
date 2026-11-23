package db

import (
	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
)

// Derive all writes from complete preimages, then compare every preimage
// under the bbolt write lock. The caller cannot supply arbitrary ship edits.
func (s *Store) CommitProbeLaunch(in commandrules.ProbeLaunch) (CommandOutcome, error) {
	return s.commitShipRecords("CommitProbeLaunch", "probe-launch", nil, func() (preparedFactoryBuild, error) {
		var out preparedFactoryBuild
		change, err := commandrules.PlanProbeLaunch(in)
		if err != nil {
			return out, commandError(CommandInvalid)
		}
		if err = out.add(BucketPlanets, PlanetKey(in.Planet.StarNum, in.Planet.PlanetNum), in.Planet, change.Planet, true); err != nil {
			return out, err
		}
		changed := map[uint32]*model.Ship{in.Selected: change.Probe, in.Parent: change.Carrier}
		if pred := change.Predecessor; pred != nil {
			changed[pred.Number] = pred
		}
		for _, chain := range [][]*model.Ship{in.Orbit, in.Children} {
			for _, before := range chain {
				after, write := changed[before.Number]
				if !write {
					after = before
				}
				if err = out.add(BucketShips, ShipKey(before.Number), before, after, write); err != nil {
					return out, err
				}
			}
		}
		if err = out.add(BucketStars, StarKey(in.Star.Number), in.Star, in.Star, false); err != nil {
			return out, err
		}
		err = out.add(BucketRaces, RaceKey(in.Player), in.Race, in.Race, false)
		return out, err
	})
}
