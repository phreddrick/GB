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

// ErrCorruptScheduleRecord wraps a schedule record that is present but
// undecodable, incomplete or carries invalid values. An absent record is
// not corrupt: GetSchedule returns nil, nil for it.
var ErrCorruptScheduleRecord = errors.New("corrupt schedule record")

// scheduleRecordFields are the JSON keys a schedule record must carry.
var scheduleRecordFields = requiredFields{
	required: []string{"next_update", "next_segment", "num_updates", "num_segments", "update_minutes", "moves_per_update", "saved_at"},
}

// Validate checks a schedule record's values: non-negative counters, a
// positive update interval, at least one move per update and non-zero
// next times.
func (r *ScheduleRecord) Validate() error {
	var problems []string
	add := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }
	if r.NumUpdates < 0 || r.NumSegments < 0 {
		add("negative counters %d/%d", r.NumUpdates, r.NumSegments)
	}
	if r.UpdateMinutes <= 0 {
		add("update_minutes %d is not positive", r.UpdateMinutes)
	}
	if r.MovesPerUpdate < 1 {
		add("moves_per_update %d is below 1", r.MovesPerUpdate)
	}
	if r.NextUpdate.IsZero() || r.NextSegment.IsZero() {
		add("next_update or next_segment is zero")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrCorruptScheduleRecord, problems)
}

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
// universe or a complete one (turn commits have the same property since
// CR-18 was closed, see commit.go). It refuses, inside the same transaction, when the
// store already holds a universe, so two concurrent initialisations cannot
// both succeed.
func (s *Store) PutUniverse(sd *model.StarData, stars []*model.Star, planets []*model.Planet, rec *UniverseRecord) error {
	if err := s.writeAllowed("PutUniverse"); err != nil {
		return err
	}
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
		if err := meta.Put(KeyUniverse, data); err != nil {
			return err
		}
		// The initialisation is the first commit (CR-18): sequence 1,
		// counters 0/0, written atomically with the universe.
		return putInitCommitRecord(meta, rec.InitializedAt)
	})
}

// GetSchedule returns the persisted schedule, or nil, nil when none exists.
// A record that is present but JSON null, empty, incomplete, followed by
// trailing data or invalid is ErrCorruptScheduleRecord.
func (s *Store) GetSchedule() (*ScheduleRecord, error) {
	raw, err := s.GetMeta(KeySchedule)
	if err != nil || raw == nil {
		return nil, err
	}
	var rec ScheduleRecord
	if err := strictDecode(raw, scheduleRecordFields, &rec); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptScheduleRecord, err)
	}
	if err := rec.Validate(); err != nil {
		return nil, err
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

// Maintenance mode record (CR-43, ADR-0010). Its presence under KeyMaintenance
// means an operator quiesced the engine and the quiesce is to survive a
// restart: a server that finds it starts QUIESCED instead of SERVING and runs
// no scheduled turn until `gbctl resume`, which removes the record in the
// same transaction that persists the resumed schedule. A store without the
// record behaves exactly as before this change (SERVING after start). The
// record is written only by Quiesce, outside turns (the write fence applies),
// and is never touched by CommitWorld, so the quiesced state and the world
// commits are independent: a shutdown commit while quiesced keeps the record.

// KeyMaintenance is the meta key of the maintenance record.
var KeyMaintenance = []byte("maintenance")

// MaintenanceSchemaVersion identifies the layout of the maintenance record.
const MaintenanceSchemaVersion = 1

// MaintenanceRecord is the durable form of an operator quiesce.
type MaintenanceRecord struct {
	SchemaVersion int       `json:"schema_version"`
	Quiesced      bool      `json:"quiesced"` // always true while the record exists
	SetAt         time.Time `json:"set_at"`
	Reason        string    `json:"reason"`
	EngineVersion string    `json:"engine_version,omitempty"`
}

// ErrCorruptMaintenanceRecord wraps a maintenance record that is present but
// undecodable, incomplete, carries unknown fields or invalid values. An
// absent record is not corrupt: GetMaintenance returns nil, nil for it.
var ErrCorruptMaintenanceRecord = errors.New("corrupt maintenance record")

var maintenanceRecordFields = requiredFields{
	required: []string{"schema_version", "quiesced", "set_at", "reason"},
	optional: []string{"engine_version"},
}

// Validate checks the record's values: the supported schema version, the
// quiesced flag set (a record that says "not quiesced" is not a valid way to
// express the absence of maintenance; the key is deleted instead) and a
// non-zero time.
func (r *MaintenanceRecord) Validate() error {
	var problems []string
	add := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }
	if r.SchemaVersion != MaintenanceSchemaVersion {
		add("schema_version %d (supported: %d)", r.SchemaVersion, MaintenanceSchemaVersion)
	}
	if !r.Quiesced {
		add("quiesced is false (an absent record means not quiesced)")
	}
	if r.SetAt.IsZero() {
		add("set_at is missing")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrCorruptMaintenanceRecord, problems)
}

// GetMaintenance returns the maintenance record, or nil, nil when the store
// has none. A record that is present but malformed is
// ErrCorruptMaintenanceRecord.
func (s *Store) GetMaintenance() (*MaintenanceRecord, error) {
	raw, err := s.GetMeta(KeyMaintenance)
	if err != nil || raw == nil {
		return nil, err
	}
	var rec MaintenanceRecord
	if err := strictDecode(raw, maintenanceRecordFields, &rec); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptMaintenanceRecord, err)
	}
	if err := rec.Validate(); err != nil {
		return nil, err
	}
	return &rec, nil
}

// PutMaintenance writes the maintenance record in one transaction (fsynced
// by bbolt). It is refused while the write fence is raised and refuses an
// invalid record before touching the store.
func (s *Store) PutMaintenance(rec *MaintenanceRecord) error {
	if err := rec.Validate(); err != nil {
		return err
	}
	if err := s.writeAllowed("PutMaintenance"); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := s.metaFault("maintenance.put"); err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(BucketMeta).Put(KeyMaintenance, data)
	})
}

// ClearMaintenanceWithSchedule removes the maintenance record and writes the
// schedule record in ONE transaction: the resumed schedule and the end of
// maintenance become durable together, so a crash leaves the store either
// quiesced with the old schedule or serving with the new one. Deleting an
// absent record is not an error (an engine quiesced in memory only resumes
// the same way). Refused while the write fence is raised.
func (s *Store) ClearMaintenanceWithSchedule(sched *ScheduleRecord) error {
	if sched == nil {
		return errors.New("resume: schedule record is nil")
	}
	if err := sched.Validate(); err != nil {
		return err
	}
	if err := s.writeAllowed("ClearMaintenanceWithSchedule"); err != nil {
		return err
	}
	data, err := json.Marshal(sched)
	if err != nil {
		return err
	}
	if err := s.metaFault("maintenance.clear"); err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(BucketMeta)
		if err := meta.Delete(KeyMaintenance); err != nil {
			return err
		}
		return meta.Put(KeySchedule, data)
	})
}

// SetMetaFault installs a test-only fault hook consulted by PutMaintenance
// ("maintenance.put") and ClearMaintenanceWithSchedule ("maintenance.clear")
// before their transaction opens; a non-nil error is returned to the caller
// and nothing is written. Production code never sets it.
func (s *Store) SetMetaFault(f func(op string) error) {
	s.metaFaultFn.Store(&f)
}

func (s *Store) metaFault(op string) error {
	p := s.metaFaultFn.Load()
	if p == nil || *p == nil {
		return nil
	}
	return (*p)(op)
}
