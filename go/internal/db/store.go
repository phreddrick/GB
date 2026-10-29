package db

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/phreddrick/GB/go/internal/model"
)

// Store is the game's database handle. All game state is persisted here.
// bbolt gives us ACID writes; concurrent reads are handled by bbolt's
// read-transaction model (multiple readers, one writer at a time).
type Store struct {
	db *bolt.DB
}

// Open opens (or creates) the bbolt database at the given path.
func Open(path string) (*Store, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open db %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.initBuckets(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// initBuckets ensures all required buckets exist.
func (s *Store) initBuckets() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{
			BucketShips, BucketPlanets, BucketStars, BucketRaces,
			BucketSData, BucketPower, BucketBlocks, BucketCommodities,
			BucketShipFree, BucketCommodFree, BucketMeta,
			BucketNews, BucketTelegrams,
		} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("create bucket %s: %w", name, err)
			}
		}
		return nil
	})
}

// --- Ships ---

// GetShip loads a ship by number. Returns nil, nil if not found.
func (s *Store) GetShip(num uint32) (*model.Ship, error) {
	var ship model.Ship
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(BucketShips).Get(ShipKey(num))
		if v == nil {
			return errNotFound
		}
		return json.Unmarshal(v, &ship)
	})
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	return &ship, err
}

// PutShip writes a ship to the database.
func (s *Store) PutShip(ship *model.Ship) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(ship)
		if err != nil {
			return err
		}
		return tx.Bucket(BucketShips).Put(ShipKey(ship.Number), data)
	})
}

// DeleteShip removes a ship record and records its number as free.
func (s *Store) DeleteShip(num uint32) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(BucketShips)
		if err := b.Delete(ShipKey(num)); err != nil {
			return err
		}
		return appendFreeNum(tx.Bucket(BucketShipFree), num)
	})
}

// NumShips returns the count of live ship records.
func (s *Store) NumShips() (int, error) {
	var n int
	err := s.db.View(func(tx *bolt.Tx) error {
		n = tx.Bucket(BucketShips).Stats().KeyN
		return nil
	})
	return n, err
}

// AllShips returns all ships as a map keyed by ship number.
// Called at turn start to load everything into memory.
func (s *Store) AllShips() (map[uint32]*model.Ship, error) {
	ships := make(map[uint32]*model.Ship)
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(BucketShips).ForEach(func(k, v []byte) error {
			var ship model.Ship
			if err := json.Unmarshal(v, &ship); err != nil {
				return err
			}
			ships[ship.Number] = &ship
			return nil
		})
	})
	return ships, err
}

// PutShipsBatch writes many ships in a single transaction (end-of-turn flush).
func (s *Store) PutShipsBatch(ships map[uint32]*model.Ship) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(BucketShips)
		for _, ship := range ships {
			data, err := json.Marshal(ship)
			if err != nil {
				return err
			}
			if err := b.Put(ShipKey(ship.Number), data); err != nil {
				return err
			}
		}
		return nil
	})
}

// AllocShipNum returns the next available ship number, reusing a free slot if
// possible, otherwise incrementing past the current maximum.
func (s *Store) AllocShipNum() (uint32, error) {
	var num uint32
	err := s.db.Update(func(tx *bolt.Tx) error {
		fb := tx.Bucket(BucketShipFree)
		n, err := popFreeNum(fb)
		if err != nil {
			return err
		}
		if n > 0 {
			num = n
			return nil
		}
		// No free slots: find max existing + 1
		cur := tx.Bucket(BucketShips).Cursor()
		k, _ := cur.Last()
		if k == nil {
			num = 1
		} else {
			num = uint32(k[0])<<24 | uint32(k[1])<<16 | uint32(k[2])<<8 | uint32(k[3])
			num++
		}
		return nil
	})
	return num, err
}

// --- Planets ---

// GetPlanet loads a planet by (starNum, planetNum).
func (s *Store) GetPlanet(starNum, planetNum uint8) (*model.Planet, error) {
	var planet model.Planet
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(BucketPlanets).Get(PlanetKey(starNum, planetNum))
		if v == nil {
			return errNotFound
		}
		return json.Unmarshal(v, &planet)
	})
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	return &planet, err
}

// PutPlanet writes a planet (including its embedded sector grid).
func (s *Store) PutPlanet(p *model.Planet) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return tx.Bucket(BucketPlanets).Put(PlanetKey(p.StarNum, p.PlanetNum), data)
	})
}

// AllPlanets returns all planets as a nested map [starNum][planetNum].
func (s *Store) AllPlanets() (map[uint8]map[uint8]*model.Planet, error) {
	result := make(map[uint8]map[uint8]*model.Planet)
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(BucketPlanets).ForEach(func(k, v []byte) error {
			var p model.Planet
			if err := json.Unmarshal(v, &p); err != nil {
				return err
			}
			if result[p.StarNum] == nil {
				result[p.StarNum] = make(map[uint8]*model.Planet)
			}
			result[p.StarNum][p.PlanetNum] = &p
			return nil
		})
	})
	return result, err
}

// --- Stars ---

// GetStar loads a star by number.
func (s *Store) GetStar(num uint8) (*model.Star, error) {
	var star model.Star
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(BucketStars).Get(StarKey(num))
		if v == nil {
			return errNotFound
		}
		return json.Unmarshal(v, &star)
	})
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	return &star, err
}

// PutStar writes a star record.
func (s *Store) PutStar(star *model.Star) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(star)
		if err != nil {
			return err
		}
		return tx.Bucket(BucketStars).Put(StarKey(star.Number), data)
	})
}

// AllStars returns all stars as a slice indexed by star number.
func (s *Store) AllStars() ([]*model.Star, error) {
	stars := make([]*model.Star, model.NumStars)
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(BucketStars).ForEach(func(k, v []byte) error {
			var star model.Star
			if err := json.Unmarshal(v, &star); err != nil {
				return err
			}
			if int(star.Number) < len(stars) {
				stars[star.Number] = &star
			}
			return nil
		})
	})
	return stars, err
}

// --- Races ---

// GetRace loads a race by player number (1-indexed).
func (s *Store) GetRace(playerNum int) (*model.Race, error) {
	var race model.Race
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(BucketRaces).Get(RaceKey(playerNum))
		if v == nil {
			return errNotFound
		}
		return json.Unmarshal(v, &race)
	})
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	return &race, err
}

// PutRace writes a race record.
func (s *Store) PutRace(race *model.Race) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(race)
		if err != nil {
			return err
		}
		return tx.Bucket(BucketRaces).Put(RaceKey(race.PlayerNum), data)
	})
}

// AllRaces returns all races as a slice (index 0 = player 1).
func (s *Store) AllRaces() ([]*model.Race, error) {
	races := make([]*model.Race, model.MaxPlayers)
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(BucketRaces).ForEach(func(k, v []byte) error {
			var race model.Race
			if err := json.Unmarshal(v, &race); err != nil {
				return err
			}
			if race.PlayerNum >= 1 && race.PlayerNum <= model.MaxPlayers {
				races[race.PlayerNum-1] = &race
			}
			return nil
		})
	})
	return races, err
}

// NumRaces returns the count of enrolled races.
func (s *Store) NumRaces() (int, error) {
	var n int
	err := s.db.View(func(tx *bolt.Tx) error {
		n = tx.Bucket(BucketRaces).Stats().KeyN
		return nil
	})
	return n, err
}

// --- StarData (universe global) ---

// GetStarData loads the global StarData record.
func (s *Store) GetStarData() (*model.StarData, error) {
	var sd model.StarData
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(BucketSData).Get([]byte("global"))
		if v == nil {
			return errNotFound
		}
		return json.Unmarshal(v, &sd)
	})
	if errors.Is(err, errNotFound) {
		return &model.StarData{}, nil
	}
	return &sd, err
}

// PutStarData writes the global StarData record.
func (s *Store) PutStarData(sd *model.StarData) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(sd)
		if err != nil {
			return err
		}
		return tx.Bucket(BucketSData).Put([]byte("global"), data)
	})
}

// --- Power report ---

// GetPower loads the power report for all players.
func (s *Store) GetPower() ([model.MaxPlayers]model.Power, error) {
	var power [model.MaxPlayers]model.Power
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(BucketPower).Get([]byte("global"))
		if v == nil {
			return nil
		}
		return json.Unmarshal(v, &power)
	})
	return power, err
}

// PutPower writes the power report.
func (s *Store) PutPower(power [model.MaxPlayers]model.Power) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(power)
		if err != nil {
			return err
		}
		return tx.Bucket(BucketPower).Put([]byte("global"), data)
	})
}

// --- Alliance blocks ---

// GetBlocks loads the per-player alliance block records.
func (s *Store) GetBlocks() ([model.MaxPlayers]model.Block, error) {
	var blocks [model.MaxPlayers]model.Block
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(BucketBlocks).Get([]byte("blocks"))
		if v == nil {
			return nil
		}
		return json.Unmarshal(v, &blocks)
	})
	return blocks, err
}

// PutBlocks writes the alliance block records.
func (s *Store) PutBlocks(blocks [model.MaxPlayers]model.Block) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(blocks)
		if err != nil {
			return err
		}
		return tx.Bucket(BucketBlocks).Put([]byte("blocks"), data)
	})
}

// GetPowerBlocks loads the aggregated block-level power report.
func (s *Store) GetPowerBlocks() (*model.PowerBlocks, error) {
	var pb model.PowerBlocks
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(BucketBlocks).Get([]byte("power_blocks"))
		if v == nil {
			return nil
		}
		return json.Unmarshal(v, &pb)
	})
	return &pb, err
}

// PutPowerBlocks writes the aggregated block-level power report.
func (s *Store) PutPowerBlocks(pb *model.PowerBlocks) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(pb)
		if err != nil {
			return err
		}
		return tx.Bucket(BucketBlocks).Put([]byte("power_blocks"), data)
	})
}

// --- Commodities ---

// GetCommod loads a commodity by number.
func (s *Store) GetCommod(num uint32) (*model.Commodity, error) {
	var c model.Commodity
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(BucketCommodities).Get(CommodKey(num))
		if v == nil {
			return errNotFound
		}
		return json.Unmarshal(v, &c)
	})
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	return &c, err
}

// PutCommod writes a commodity record.
func (s *Store) PutCommod(c *model.Commodity) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(c)
		if err != nil {
			return err
		}
		return tx.Bucket(BucketCommodities).Put(CommodKey(c.Number), data)
	})
}

// AllocCommodNum returns the next available commodity lot number,
// reusing a free slot if possible. Mirrors getdeadcommod() in files_shl.c.
func (s *Store) AllocCommodNum() (uint32, error) {
	var num uint32
	err := s.db.Update(func(tx *bolt.Tx) error {
		fb := tx.Bucket(BucketCommodFree)
		n, err := popFreeNum(fb)
		if err != nil {
			return err
		}
		if n > 0 {
			num = n
			return nil
		}
		// No free slots: find max existing + 1
		cur := tx.Bucket(BucketCommodities).Cursor()
		k, _ := cur.Last()
		if k == nil {
			num = 1
		} else {
			num = uint32(k[0])<<24 | uint32(k[1])<<16 | uint32(k[2])<<8 | uint32(k[3])
			num++
		}
		return nil
	})
	return num, err
}

// DeleteCommod removes a commodity and records its number as free.
func (s *Store) DeleteCommod(num uint32) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(BucketCommodities).Delete(CommodKey(num)); err != nil {
			return err
		}
		return appendFreeNum(tx.Bucket(BucketCommodFree), num)
	})
}

// --- Metadata (timing) ---

// GetMeta reads a single metadata value.
func (s *Store) GetMeta(key []byte) ([]byte, error) {
	var val []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(BucketMeta).Get(key)
		if v != nil {
			val = make([]byte, len(v))
			copy(val, v)
		}
		return nil
	})
	return val, err
}

// PutMeta writes a single metadata value.
func (s *Store) PutMeta(key, value []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(BucketMeta).Put(key, value)
	})
}

// --- Telegrams ---

// PushTelegram appends one telegram to the (playerNum, governor) inbox.
// text should already include a timestamp prefix if desired.
func (s *Store) PushTelegram(playerNum, governor int, text string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(BucketTelegrams)
		seq, err := b.NextSequence()
		if err != nil {
			return err
		}
		return b.Put(TelegramKeyPG(uint8(playerNum), uint8(governor), seq), []byte(text))
	})
}

// ReadAndClearTelegrams returns all pending telegrams for (playerNum, governor)
// and deletes them from the bucket (mirrors the C behaviour of truncating the file).
func (s *Store) ReadAndClearTelegrams(playerNum, governor int) ([]string, error) {
	var msgs []string
	prefix := TelegramPrefix(uint8(playerNum), uint8(governor))
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(BucketTelegrams)
		cur := b.Cursor()
		var toDelete [][]byte
		for k, v := cur.Seek(prefix); k != nil && len(k) >= 2 && k[0] == prefix[0] && k[1] == prefix[1]; k, v = cur.Next() {
			msgs = append(msgs, string(v))
			cp := make([]byte, len(k))
			copy(cp, k)
			toDelete = append(toDelete, cp)
		}
		for _, k := range toDelete {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
	return msgs, err
}

// HasTelegrams returns true if there are any pending telegrams for the given player/governor.
func (s *Store) HasTelegrams(playerNum, governor int) (bool, error) {
	var found bool
	prefix := TelegramPrefix(uint8(playerNum), uint8(governor))
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(BucketTelegrams)
		cur := b.Cursor()
		k, _ := cur.Seek(prefix)
		if k != nil && len(k) >= 2 && k[0] == prefix[0] && k[1] == prefix[1] {
			found = true
		}
		return nil
	})
	return found, err
}

// --- News ---

// AppendNews adds a timestamped news entry of the given type.
// Returns the sequence number of the new entry.
func (s *Store) AppendNews(newsType uint8, text string) (uint64, error) {
	var seq uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(BucketNews)
		// Find the highest existing seq for this type by seeking to the end.
		cur := b.Cursor()
		// Build a key that is just past the maximum possible seq for this type.
		maxKey := NewsKey(newsType, ^uint64(0))
		k, _ := cur.Seek(maxKey)
		if k == nil {
			k, _ = cur.Last()
		} else {
			k, _ = cur.Prev()
		}
		if k != nil && len(k) == 9 && k[0] == newsType {
			seq = binary.BigEndian.Uint64(k[1:])
		}
		seq++
		entry := []byte(text)
		return b.Put(NewsKey(newsType, seq), entry)
	})
	return seq, err
}

// LatestNewsSeq returns the sequence number of the most recent entry for newsType.
// Returns 0 if no entries exist.
func (s *Store) LatestNewsSeq(newsType uint8) (uint64, error) {
	var seq uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(BucketNews)
		cur := b.Cursor()
		maxKey := NewsKey(newsType, ^uint64(0))
		k, _ := cur.Seek(maxKey)
		if k == nil {
			k, _ = cur.Last()
		} else {
			k, _ = cur.Prev()
		}
		if k != nil && len(k) == 9 && k[0] == newsType {
			seq = binary.BigEndian.Uint64(k[1:])
		}
		return nil
	})
	return seq, err
}

// ReadNewsFrom returns all news entries of newsType whose sequence number is
// greater than fromSeq, together with the highest seq seen (for cursor update).
func (s *Store) ReadNewsFrom(newsType uint8, fromSeq uint64) ([]string, uint64, error) {
	var entries []string
	var latest uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(BucketNews)
		cur := b.Cursor()
		startKey := NewsKey(newsType, fromSeq+1)
		for k, v := cur.Seek(startKey); k != nil && len(k) == 9 && k[0] == newsType; k, v = cur.Next() {
			entries = append(entries, string(v))
			seq := binary.BigEndian.Uint64(k[1:])
			if seq > latest {
				latest = seq
			}
		}
		return nil
	})
	return entries, latest, err
}

// PurgeNews deletes all entries for a given news type.
func (s *Store) PurgeNews(newsType uint8) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(BucketNews)
		cur := b.Cursor()
		startKey := NewsKey(newsType, 0)
		var toDelete [][]byte
		for k, _ := cur.Seek(startKey); k != nil && len(k) == 9 && k[0] == newsType; k, _ = cur.Next() {
			cp := make([]byte, len(k))
			copy(cp, k)
			toDelete = append(toDelete, cp)
		}
		for _, k := range toDelete {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// --- Internal helpers ---

var errNotFound = errors.New("not found")

// appendFreeNum JSON-encodes a free-number list entry in the given bucket.
// We store the free list as a simple append-only log; AllocXxxNum pops from it.
func appendFreeNum(b *bolt.Bucket, num uint32) error {
	// Use the next sequence as key so entries are ordered.
	seq, err := b.NextSequence()
	if err != nil {
		return err
	}
	k := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		k[i] = byte(seq)
		seq >>= 8
	}
	v := make([]byte, 4)
	v[0] = byte(num >> 24)
	v[1] = byte(num >> 16)
	v[2] = byte(num >> 8)
	v[3] = byte(num)
	return b.Put(k, v)
}

// popFreeNum removes and returns the last free number, or 0 if none.
func popFreeNum(b *bolt.Bucket) (uint32, error) {
	cur := b.Cursor()
	k, v := cur.Last()
	if k == nil {
		return 0, nil
	}
	num := uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3])
	return num, b.Delete(k)
}

// btoi decodes a big-endian uint64 from up to 8 bytes.
func btoi(b []byte) uint64 {
	var n uint64
	for _, byt := range b {
		n = n<<8 | uint64(byt)
	}
	return n
}
