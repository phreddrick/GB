package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/phreddrick/GB/go/internal/model"
)

// Phase 1 metadata records. Both live in BucketMeta as JSON under fixed keys
// and are read and written as whole records.
// starDataKey is the key GetStarData/PutStarData use in BucketSData.
var starDataKey = []byte("global")

var (
	// KeyUniverse records how the universe was initialised (seed, parameters,
	// time). Its presence is the primary "initialised" marker.
	KeyUniverse = []byte("universe")
	// KeySchedule records the turn schedule so that a restart continues the
	// same schedule and counters (CR-21).
	KeySchedule = []byte("schedule")
)

// UniverseRecord is the initialisation metadata written by universe init.
type UniverseRecord struct {
	Seed          int64     `json:"seed"`
	Stars         int       `json:"stars"`
	MinPlanets    int       `json:"min_planets"`
	MaxPlanets    int       `json:"max_planets"`
	Planets       int       `json:"planets"`
	Generator     string    `json:"generator"` // generator version tag
	InitializedAt time.Time `json:"initialized_at"`
	// Tool records which program performed the initialisation
	// ("gb-server admin", "makeuniv").
	Tool string `json:"tool"`
}

// ScheduleRecord is the persisted turn schedule.
type ScheduleRecord struct {
	NextUpdate     time.Time `json:"next_update"`
	NextSegment    time.Time `json:"next_segment"`
	NumUpdates     int       `json:"num_updates"`
	NumSegments    int       `json:"num_segments"`
	UpdateMinutes  int       `json:"update_minutes"`
	MovesPerUpdate int       `json:"moves_per_update"`
	SavedAt        time.Time `json:"saved_at"`
}

// ErrAlreadyInitialized is returned by PutUniverse when the store already
// holds a universe.
var ErrAlreadyInitialized = errors.New("database already holds an initialised universe")

// GetUniverseRecord returns the initialisation record, or nil, nil when the
// store has none.
func (s *Store) GetUniverseRecord() (*UniverseRecord, error) {
	raw, err := s.GetMeta(KeyUniverse)
	if err != nil || raw == nil {
		return nil, err
	}
	var rec UniverseRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("decode universe record: %w", err)
	}
	return &rec, nil
}

// IsInitialized reports whether the store holds a universe: either the
// Phase 1 initialisation record exists, or (for databases written by the
// pre-Phase-1 makeuniv, which wrote no record) any star records exist or
// StarData reports stars.
func (s *Store) IsInitialized() (bool, error) {
	var initialized bool
	err := s.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(BucketMeta).Get(KeyUniverse) != nil {
			initialized = true
			return nil
		}
		if tx.Bucket(BucketStars).Stats().KeyN > 0 {
			initialized = true
			return nil
		}
		if v := tx.Bucket(BucketSData).Get(starDataKey); v != nil {
			var sd model.StarData
			if err := json.Unmarshal(v, &sd); err == nil && sd.NumStars > 0 {
				initialized = true
			}
		}
		return nil
	})
	return initialized, err
}

// PutUniverse writes a freshly generated universe and its record in ONE
// bbolt transaction, so a crash during initialisation leaves either no
// universe or a complete one (the transaction is atomic; this is the only
// multi-record write in Phase 1 with that property, and it does not extend to
// turn flushes, CR-18). It refuses, inside the same transaction, when the
// store already holds a universe, so two concurrent initialisations cannot
// both succeed.
func (s *Store) PutUniverse(sd *model.StarData, stars []*model.Star, planets []*model.Planet, rec *UniverseRecord) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(BucketMeta)
		if meta.Get(KeyUniverse) != nil || tx.Bucket(BucketStars).Stats().KeyN > 0 {
			return ErrAlreadyInitialized
		}
		if v := tx.Bucket(BucketSData).Get(starDataKey); v != nil {
			var old model.StarData
			if err := json.Unmarshal(v, &old); err == nil && old.NumStars > 0 {
				return ErrAlreadyInitialized
			}
		}
		data, err := json.Marshal(sd)
		if err != nil {
			return err
		}
		if err := tx.Bucket(BucketSData).Put(starDataKey, data); err != nil {
			return err
		}
		starsB := tx.Bucket(BucketStars)
		for _, st := range stars {
			data, err := json.Marshal(st)
			if err != nil {
				return err
			}
			if err := starsB.Put(StarKey(st.Number), data); err != nil {
				return err
			}
		}
		planetsB := tx.Bucket(BucketPlanets)
		for _, p := range planets {
			data, err := json.Marshal(p)
			if err != nil {
				return err
			}
			if err := planetsB.Put(PlanetKey(p.StarNum, p.PlanetNum), data); err != nil {
				return err
			}
		}
		data, err = json.Marshal(rec)
		if err != nil {
			return err
		}
		return meta.Put(KeyUniverse, data)
	})
}

// GetSchedule returns the persisted schedule, or nil, nil when none exists.
func (s *Store) GetSchedule() (*ScheduleRecord, error) {
	raw, err := s.GetMeta(KeySchedule)
	if err != nil || raw == nil {
		return nil, err
	}
	var rec ScheduleRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("decode schedule record: %w", err)
	}
	return &rec, nil
}

// PutSchedule persists the schedule record.
func (s *Store) PutSchedule(rec *ScheduleRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.PutMeta(KeySchedule, data)
}

// Path returns the database file path.
func (s *Store) Path() string { return s.db.Path() }

// Counts returns the number of star, planet, race and ship records (status
// and tests).
func (s *Store) Counts() (stars, planets, races, ships int, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		stars = tx.Bucket(BucketStars).Stats().KeyN
		planets = tx.Bucket(BucketPlanets).Stats().KeyN
		races = tx.Bucket(BucketRaces).Stats().KeyN
		ships = tx.Bucket(BucketShips).Stats().KeyN
		return nil
	})
	return
}
