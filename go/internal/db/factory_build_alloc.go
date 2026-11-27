package db

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

// Construction allocation is deliberately distinct from private enrollment.
// Preserve free-list integrity/sequence and append/absence checks, but only
// structural references reserve an identity. Stale advisory orders must not
// globally block construction. Enrollment's stricter policy is unchanged.
func constructionShipCandidatesTx(tx *bolt.Tx, count int) ([]FactoryAllocation, error) {
	if count < 1 || count > 2 {
		return nil, commandError(CommandInvalid)
	}
	free, ships := tx.Bucket(BucketShipFree), tx.Bucket(BucketShips)
	if free == nil || ships == nil {
		return nil, commandError(CommandCorrupt)
	}
	h := sha256.New()
	_, _ = h.Write(enrollmentFreeKey(free.Sequence()))
	seen := map[uint32]bool{}
	e := free.ForEach(func(k, v []byte) error {
		if len(k) != 8 || len(v) != 4 {
			return commandError(CommandCorrupt)
		}
		seq := binary.BigEndian.Uint64(k)
		n := binary.BigEndian.Uint32(v)
		if seq == 0 || seq > free.Sequence() || n == 0 || seen[n] {
			return commandError(CommandCorrupt)
		}
		seen[n] = true
		_, _ = h.Write(k)
		_, _ = h.Write(v)
		return nil
	})
	if e != nil {
		return nil, e
	}
	digest := hex.EncodeToString(h.Sum(nil))
	var high uint32
	e = ships.ForEach(func(k, v []byte) error {
		if len(k) != 4 || binary.BigEndian.Uint32(k) == 0 {
			return commandError(CommandCorrupt)
		}
		high = max(high, binary.BigEndian.Uint32(k))
		return nil
	})
	if e != nil {
		return nil, e
	}
	tickets := make([]FactoryAllocation, 0, count)
	cursor := free.Cursor()
	k, v := cursor.Last()
	for range count {
		ticket := FactoryAllocation{FreeDigest: digest}
		if k != nil {
			ticket.Number, ticket.FreeKey = binary.BigEndian.Uint32(v), binary.BigEndian.Uint64(k)
			k, v = cursor.Prev()
		} else {
			if high == math.MaxUint32 {
				return nil, commandError(CommandStale)
			}
			ticket.Number = high + 1
		}
		if ships.Get(ShipKey(ticket.Number)) != nil {
			return nil, commandError(CommandStale)
		}
		if e = factoryBuildReferences(tx, ticket.Number); e != nil {
			return nil, e
		}
		// Legacy construction inserts each new row before the next allocation.
		// A reused ID above the existing maximum must also advance the next append.
		high = max(high, ticket.Number)
		tickets = append(tickets, ticket)
	}
	return tickets, nil
}

func factoryBuildReferences(tx *bolt.Tx, n uint32) error {
	conflict := func(ref uint32) error {
		if ref == n {
			return commandError(CommandStale)
		}
		return nil
	}
	e := tx.Bucket(BucketShips).ForEach(func(k, v []byte) error {
		var sh model.Ship
		if enrollmentDecode(v, &sh) != nil || len(k) != 4 || binary.BigEndian.Uint32(k) != sh.Number {
			return commandError(CommandCorrupt)
		}
		// NextShip/Ships are list membership, even on dead records awaiting
		// cleanup. DestShipNo is a structural parent ONLY for contained ships.
		// Protection, targets, aims, transporter orders and other DestShipNo
		// uses are advisory orders, not a reservation of an absent identity.
		refs := []uint32{sh.NextShip, sh.Ships}
		if sh.WhatOrbits == model.LevelShip {
			refs = append(refs, sh.DestShipNo)
		}
		for _, ref := range refs {
			if e := conflict(ref); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	e = tx.Bucket(BucketStars).ForEach(func(k, v []byte) error {
		var st model.Star
		if enrollmentDecode(v, &st) != nil || len(k) != 2 || k[0] != 0 || k[1] != st.Number {
			return commandError(CommandCorrupt)
		}
		return conflict(st.Ships)
	})
	if e != nil {
		return e
	}
	e = tx.Bucket(BucketPlanets).ForEach(func(k, v []byte) error {
		var p model.Planet
		if enrollmentDecode(v, &p) != nil || len(k) != 2 || k[0] != p.StarNum || k[1] != p.PlanetNum {
			return commandError(CommandCorrupt)
		}
		return conflict(p.Ships)
	})
	if e != nil {
		return e
	}
	e = tx.Bucket(BucketRaces).ForEach(func(k, v []byte) error {
		var r model.Race
		if enrollmentDecode(v, &r) != nil || len(k) != 2 || r.PlayerNum < 1 || r.PlayerNum > model.MaxPlayers || binary.BigEndian.Uint16(k) != uint16(r.PlayerNum) {
			return commandError(CommandCorrupt)
		}
		return conflict(r.GovShip)
	})
	if e != nil {
		return e
	}
	if raw := tx.Bucket(BucketSData).Get(starDataKey); raw != nil {
		var sd model.StarData
		if enrollmentDecode(raw, &sd) != nil {
			return commandError(CommandCorrupt)
		}
		if e = conflict(sd.Ships); e != nil {
			return e
		}
	}
	all, e := placementsTx(tx)
	if e != nil {
		return commandError(CommandCorrupt)
	}
	for _, r := range all {
		if e = conflict(r.GovernmentShip); e != nil {
			return e
		}
	}
	return nil
}
