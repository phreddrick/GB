package db

import "github.com/phreddrick/GB/go/internal/commandrules"

// CommitProbeScrap replays the only permitted death and AP edits from full
// preimages. The turn owns later orbit rebuilding and allocation.
func (s *Store) CommitProbeScrap(in commandrules.ProbeScrap) (CommandOutcome, error) {
	return s.commitShipRecords("CommitProbeScrap", "probe-scrap", nil, func() (preparedFactoryBuild, error) {
		var out preparedFactoryBuild
		dead, star, err := commandrules.PlanProbeScrap(in)
		if err != nil {
			return out, commandError(CommandInvalid)
		}
		if err := out.add(BucketRaces, RaceKey(in.Player), in.Race, nil, false); err != nil {
			return out, err
		}
		if err := out.add(BucketStars, StarKey(star.Number), in.Star, star, true); err != nil {
			return out, err
		}
		for _, sh := range in.Orbit {
			if sh.Number == dead.Number {
				err = out.add(BucketShips, ShipKey(sh.Number), sh, dead, true)
			} else {
				err = out.add(BucketShips, ShipKey(sh.Number), sh, nil, false)
			}
			if err != nil {
				return out, err
			}
		}
		return out, nil
	})
}
