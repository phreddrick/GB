package db

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

func enrollmentFreeKey(n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	return b[:]
}

// EnrollmentShipCandidate does not reserve or write anything. Unlike the legacy
// allocator it validates the whole free list and refuses occupied/dead/dangling
// IDs. A conflicting tail is never skipped. CR-07D must build a new proposal when
// the ticket goes stale; no global allocator semantics are changed here.
func (s *Store) EnrollmentShipCandidate() (EnrollmentShip, error) {
	var ticket EnrollmentShip
	e := s.db.View(func(tx *bolt.Tx) error { var e error; ticket, e = enrollmentShipTx(tx); return e })
	return ticket, asEnrollmentOptionalError(e)
}
func asEnrollmentOptionalError(e error) error {
	if e == nil {
		return nil
	}
	return asEnrollmentError(e)
}
func enrollmentShipTx(tx *bolt.Tx) (EnrollmentShip, error) {
	var ticket EnrollmentShip
	b := tx.Bucket(BucketShipFree)
	h := sha256.New()
	_, _ = h.Write(enrollmentFreeKey(b.Sequence()))
	seen := map[uint32]bool{}
	e := b.ForEach(func(k, v []byte) error {
		if len(k) != 8 || len(v) != 4 {
			return enrollmentError(EnrollmentCorrupt)
		}
		seq := binary.BigEndian.Uint64(k)
		n := binary.BigEndian.Uint32(v)
		if seq == 0 || seq > b.Sequence() || n == 0 || seen[n] {
			return enrollmentError(EnrollmentCorrupt)
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
			return enrollmentError(EnrollmentCorrupt)
		}
		n := binary.BigEndian.Uint32(k)
		if n == 0 {
			return enrollmentError(EnrollmentCorrupt)
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
			return ticket, enrollmentError(EnrollmentConflict)
		}
		ticket.Number = max + 1
	}
	if tx.Bucket(BucketShips).Get(ShipKey(ticket.Number)) != nil {
		return ticket, enrollmentError(EnrollmentConflict)
	}
	if e = enrollmentReferences(tx, ticket.Number); e != nil {
		return ticket, e
	}
	return ticket, nil
}
func enrollmentReferences(tx *bolt.Tx, n uint32) error {
	conflict := func(ref uint32) error {
		if ref == n {
			return enrollmentError(EnrollmentConflict)
		}
		return nil
	}
	e := tx.Bucket(BucketShips).ForEach(func(k, v []byte) error {
		var sh model.Ship
		if enrollmentDecode(v, &sh) != nil || len(k) != 4 || binary.BigEndian.Uint32(k) != sh.Number {
			return enrollmentError(EnrollmentCorrupt)
		}
		refs := []uint32{sh.NextShip, sh.Ships, sh.DestShipNo, uint32(sh.Protect.Ship), uint32(sh.Target)}
		if sh.Special.AimedAt != nil {
			refs = append(refs, uint32(sh.Special.AimedAt.ShipNo))
		}
		if sh.Special.Transport != nil {
			refs = append(refs, uint32(sh.Special.Transport.Target))
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
			return enrollmentError(EnrollmentCorrupt)
		}
		return conflict(st.Ships)
	})
	if e != nil {
		return e
	}
	e = tx.Bucket(BucketPlanets).ForEach(func(k, v []byte) error {
		var p model.Planet
		if enrollmentDecode(v, &p) != nil || len(k) != 2 || k[0] != p.StarNum || k[1] != p.PlanetNum {
			return enrollmentError(EnrollmentCorrupt)
		}
		return conflict(p.Ships)
	})
	if e != nil {
		return e
	}
	e = tx.Bucket(BucketRaces).ForEach(func(k, v []byte) error {
		var r model.Race
		if enrollmentDecode(v, &r) != nil || len(k) != 2 || r.PlayerNum < 1 || r.PlayerNum > model.MaxPlayers || binary.BigEndian.Uint16(k) != uint16(r.PlayerNum) {
			return enrollmentError(EnrollmentCorrupt)
		}
		return conflict(r.GovShip)
	})
	if e != nil {
		return e
	}
	if raw := tx.Bucket(BucketSData).Get(starDataKey); raw != nil {
		var sd model.StarData
		if enrollmentDecode(raw, &sd) != nil {
			return enrollmentError(EnrollmentCorrupt)
		}
		if e = conflict(sd.Ships); e != nil {
			return e
		}
	}
	all, e := placementsTx(tx)
	if e != nil {
		return e
	}
	for _, r := range all {
		if e = conflict(r.GovernmentShip); e != nil {
			return e
		}
	}
	return nil
}

// Reject dangling ownership of the proposed player even when its race record
// is absent. Append allocation alone cannot detect damage by a legacy writer.
func enrollmentPlayerUnused(tx *bolt.Tx, player int) error {
	for _, bucket := range [][]byte{BucketShips, BucketStars, BucketPlanets} {
		if e := tx.Bucket(bucket).ForEach(func(k, v []byte) error {
			switch string(bucket) {
			case string(BucketShips):
				var sh model.Ship
				if e := enrollmentDecode(v, &sh); e != nil {
					return e
				}
				if int(sh.Owner) == player || int(sh.Race) == player {
					return enrollmentError(EnrollmentConflict)
				}
			case string(BucketStars):
				var st model.Star
				if e := enrollmentDecode(v, &st); e != nil {
					return e
				}
				if st.IsInhabited(player) {
					return enrollmentError(EnrollmentConflict)
				}
			case string(BucketPlanets):
				var p model.Planet
				if e := enrollmentDecode(v, &p); e != nil {
					return e
				}
				if p.Info[player-1].NumSectsOwned != 0 {
					return enrollmentError(EnrollmentConflict)
				}
				for _, sec := range p.Sectors {
					if int(sec.Owner) == player || int(sec.Race) == player {
						return enrollmentError(EnrollmentConflict)
					}
				}
			}
			return nil
		}); e != nil {
			return e
		}
	}
	return nil
}
