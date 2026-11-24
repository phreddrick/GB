package db

import "github.com/phreddrick/GB/go/internal/commandrules"

// No caller-supplied after-images. The complete root and chain checked under
// the write lock are the ones from which the permitted edits were derived.
func (s *Store) CommitStarPeerUndock(in commandrules.StarPeerUndock) (CommandOutcome, error) {
	return s.commitShipRecords("CommitStarPeerUndock", "peer-undock", nil, func() (preparedFactoryBuild, error) {
		var out preparedFactoryBuild
		a, b, err := commandrules.PlanStarPeerUndock(in)
		if err != nil {
			return out, commandError(CommandInvalid)
		}
		if err := out.add(BucketStars, StarKey(in.Star.Number), in.Star, nil, false); err != nil {
			return out, err
		}
		if err := out.add(BucketRaces, RaceKey(in.Player), in.Race, nil, false); err != nil {
			return out, err
		}
		for _, sh := range in.Orbit {
			after := sh
			if sh.Number == a.Number {
				after = a
			} else if b != nil && sh.Number == b.Number {
				after = b
			}
			if err := out.add(BucketShips, ShipKey(sh.Number), sh, after, after != sh); err != nil {
				return out, err
			}
		}
		if moved := in.MovedPeer; moved != nil {
			if err := out.add(BucketShips, ShipKey(moved.Number), moved, nil, false); err != nil {
				return out, err
			}
		}
		if wreck := in.DetachedWreck; wreck != nil {
			if err := out.add(BucketShips, ShipKey(wreck.Number), wreck, nil, false); err != nil {
				return out, err
			}
			for _, child := range in.WreckChildren {
				if err := out.add(BucketShips, ShipKey(child.Number), child, nil, false); err != nil {
					return out, err
				}
			}
		}
		return out, nil
	})
}
