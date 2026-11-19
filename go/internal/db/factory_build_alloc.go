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
func factoryBuildShipTx(tx *bolt.Tx) (FactoryAllocation, error) {
	var ticket FactoryAllocation
	b := tx.Bucket(BucketShipFree)
	h := sha256.New()
	_, _ = h.Write(enrollmentFreeKey(b.Sequence()))
	seen := map[uint32]bool{}
	e := b.ForEach(func(k, v []byte) error {
		if len(k) != 8 || len(v) != 4 {
			return commandError(CommandCorrupt)
		}
		seq := binary.BigEndian.Uint64(k)
		n := binary.BigEndian.Uint32(v)
		if seq == 0 || seq > b.Sequence() || n == 0 || seen[n] {
			return commandError(CommandCorrupt)
		}
		seen[n] = true
		_, _ = h.Write(k)
		_, _ = h.Write(v)
		ticket.Number = n
		ticket.FreeKey = seq
		return nil
	})
	if e != nil {
		return ticket, e
	}
	ticket.FreeDigest = hex.EncodeToString(h.Sum(nil))
	var max uint32
	e = tx.Bucket(BucketShips).ForEach(func(k, v []byte) error {
		if len(k) != 4 {
			return commandError(CommandCorrupt)
		}
		n := binary.BigEndian.Uint32(k)
		if n == 0 {
			return commandError(CommandCorrupt)
		}
		if n > max {
			max = n
		}
		return nil
	})
	if e != nil {
		return ticket, e
	}
	if ticket.Number == 0 {
		if max == math.MaxUint32 {
			return ticket, commandError(CommandStale)
		}
		ticket.Number = max + 1
	}
	if tx.Bucket(BucketShips).Get(ShipKey(ticket.Number)) != nil {
		return ticket, commandError(CommandStale)
	}
	if e = factoryBuildReferences(tx, ticket.Number); e != nil {
		return ticket, e
	}
	return ticket, nil
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
