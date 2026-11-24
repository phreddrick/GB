package db

import (
	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
)

// No caller-supplied after-images. The complete root and chain checked under
// the write lock are the ones from which the permitted edits were derived.
func (s *Store) CommitStarPeerUndock(in commandrules.StarPeerUndock) (CommandOutcome, error) {
	return s.commitShipRecords("CommitStarPeerUndock", "peer-undock", nil, func() (preparedFactoryBuild, error) {
		var out preparedFactoryBuild
		a, b, err := commandrules.PlanStarPeerUndock(in)
		if err != nil {
			return out, commandError(CommandInvalid)
		}
		if err := out.addStarPeers(in.Star, in.Race, in.Orbit, a, b); err != nil {
			return out, err
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

func (out *preparedFactoryBuild) addStarPeers(st *model.Star, race *model.Race, orbit []*model.Ship, a, b *model.Ship) error {
	if err := out.add(BucketStars, StarKey(st.Number), st, nil, false); err != nil {
		return err
	}
	if err := out.add(BucketRaces, RaceKey(race.PlayerNum), race, nil, false); err != nil {
		return err
	}
	for _, sh := range orbit {
		after := sh
		if sh.Number == a.Number {
			after = a
		} else if b != nil && sh.Number == b.Number {
			after = b
		}
		if err := out.add(BucketShips, ShipKey(sh.Number), sh, after, after != sh); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CommitFriendlyDock(in commandrules.FriendlyDock) (CommandOutcome, error) {
	return s.commitShipRecords("CommitFriendlyDock", "friendly-dock", nil, func() (preparedFactoryBuild, error) {
		var out preparedFactoryBuild
		a, b, err := commandrules.PlanFriendlyDock(in)
		if err != nil {
			return out, commandError(CommandInvalid)
		}
		err = out.addStarPeers(in.Star, in.Race, in.Orbit, a, b)
		return out, err
	})
}
