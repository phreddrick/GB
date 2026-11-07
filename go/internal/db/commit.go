package db

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/phreddrick/GB/go/internal/model"
)

// Atomic world commits (CR-18, migration plan section 8.3 and 8.5, first
// slice of Phase 3a).
//
// A turn, a movement segment and the orderly-shutdown flush each persist the
// whole in-memory world, the schedule and the effects the turn produced
// (news, stored telegrams) in ONE bbolt transaction that also writes the
// commit record below. bbolt's transaction is atomic and fsynced, so the
// store always holds either the state before the commit or the state after
// it, never a mixture. A failed or interrupted transaction writes nothing.
//
// Between commits the engine refuses every other write to the store (the
// write fence, SetWriteFence): turn code that reaches the store by any path
// other than the commit gets ErrWriteFenced instead of leaking part of an
// uncommitted turn. Ship and commodity number allocation is exempt
// (AllocShipNum, AllocCommodNum): consuming a free number during a turn that
// later fails leaves a gap in the number space, never an inconsistent world.

// KeyCommit is the meta key of the commit record.
var KeyCommit = []byte("commit")

// CommitSchemaVersion identifies the layout of the commit record.
const CommitSchemaVersion = 1

// CommitRecord is written in the same transaction as every commit. Its
// turn and segment counters are the schedule counters after the commit, so
// a reader can tell which turn the world records belong to.
type CommitRecord struct {
	SchemaVersion int       `json:"schema_version"`
	CommitSeq     uint64    `json:"commit_seq"` // monotonic, +1 per commit
	Turn          int       `json:"turn"`       // schedule NumUpdates after this commit
	Segment       int       `json:"segment"`    // schedule NumSegments after this commit
	Kind          string    `json:"kind"`       // "init", "update", "segment", "shutdown"
	WallTime      time.Time `json:"wall_time"`
	EngineVersion string    `json:"engine_version,omitempty"`
}

// NewsEntry is a news line produced during a turn and written by its commit.
type NewsEntry struct {
	Type uint8
	Text string
}

// TelegramEntry is a stored telegram produced during a turn and written by
// its commit.
type TelegramEntry struct {
	Player   int
	Governor int
	Text     string
}

// WorldCommit is everything one commit writes. Every world collection is
// written in full (memory is the truth at a commit; dirty tracking is a
// later Phase 3a step), and Schedule is mandatory because the schedule
// counters must move with the world they describe.
type WorldCommit struct {
	Kind          string
	EngineVersion string
	WallTime      time.Time

	Ships       map[uint32]*model.Ship
	Stars       []*model.Star
	Planets     map[uint8]map[uint8]*model.Planet
	Races       []*model.Race
	StarData    *model.StarData
	Power       *[model.MaxPlayers]model.Power
	Blocks      *[model.MaxPlayers]model.Block
	PowerBlocks *model.PowerBlocks

	Schedule  *ScheduleRecord
	News      []NewsEntry
	Telegrams []TelegramEntry

	// Progress, when set, is called once per record written (liveness).
	Progress func()
}

// ErrWriteFenced is returned by every ordinary write while the write fence
// is raised (a turn is running and only its commit may write).
var ErrWriteFenced = errors.New("store write refused: a turn is in progress and only its commit may write (CR-18 write fence)")

// ErrInvalidCommit is returned when a WorldCommit is not self-consistent.
var ErrInvalidCommit = errors.New("invalid world commit")

// SetWriteFence raises or lowers the write fence. While raised, every write
// method except the number allocators and CommitWorld returns
// ErrWriteFenced and counts the refusal (FencedWrites).
func (s *Store) SetWriteFence(on bool) { s.fenced.Store(on) }

// FencedWrites returns how many writes the fence has refused since Open.
func (s *Store) FencedWrites() uint64 { return s.fencedWrites.Load() }

// writeAllowed is the fence check at the top of every ordinary write.
func (s *Store) writeAllowed() error {
	if s.fenced.Load() {
		s.fencedWrites.Add(1)
		return ErrWriteFenced
	}
	return nil
}

// SetCommitFault installs a test-only fault hook called inside the commit
// transaction: with stage "before-write" before any record is written and
// "after-write" after every record, the schedule and the commit record have
// been put but before the transaction commits. A non-nil error aborts the
// transaction (bbolt rolls it back). Production code never sets it.
func (s *Store) SetCommitFault(f func(stage string) error) {
	s.commitFault.Store(&f)
}

func (s *Store) fault(stage string) error {
	p := s.commitFault.Load()
	if p == nil || *p == nil {
		return nil
	}
	return (*p)(stage)
}

// GetCommitRecord returns the commit record, or nil, nil when the store has
// none (a store written only by pre-commit binaries).
func (s *Store) GetCommitRecord() (*CommitRecord, error) {
	raw, err := s.GetMeta(KeyCommit)
	if err != nil || raw == nil {
		return nil, err
	}
	var rec CommitRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("decode commit record: %w", err)
	}
	return &rec, nil
}

func (c *WorldCommit) validate() error {
	switch {
	case c.Kind == "":
		return fmt.Errorf("%w: kind is empty", ErrInvalidCommit)
	case c.Schedule == nil:
		return fmt.Errorf("%w: schedule is nil", ErrInvalidCommit)
	case c.Stars == nil || c.Planets == nil || c.Races == nil || c.Ships == nil:
		return fmt.Errorf("%w: world collections are nil", ErrInvalidCommit)
	case c.StarData == nil:
		return fmt.Errorf("%w: star data is nil", ErrInvalidCommit)
	}
	return nil
}

// CommitWorld writes c in one transaction and returns the commit record it
// wrote. On any error nothing is written and the previous commit stays the
// store's content. It bypasses the write fence by design: it is the fence's
// only sanctioned writer.
func (s *Store) CommitWorld(c *WorldCommit) (*CommitRecord, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	progress := c.Progress
	if progress == nil {
		progress = func() {}
	}
	var rec CommitRecord
	err := s.db.Update(func(tx *bolt.Tx) error {
		if err := s.fault("before-write"); err != nil {
			return err
		}
		meta := tx.Bucket(BucketMeta)
		var prev CommitRecord
		if raw := meta.Get(KeyCommit); raw != nil {
			if err := json.Unmarshal(raw, &prev); err != nil {
				return fmt.Errorf("decode previous commit record: %w", err)
			}
		}
		put := func(b *bolt.Bucket, key []byte, v any) error {
			data, err := json.Marshal(v)
			if err != nil {
				return err
			}
			if err := b.Put(key, data); err != nil {
				return err
			}
			progress()
			return nil
		}
		ships := tx.Bucket(BucketShips)
		for _, sh := range c.Ships {
			if sh == nil {
				continue
			}
			if err := put(ships, ShipKey(sh.Number), sh); err != nil {
				return fmt.Errorf("ship %d: %w", sh.Number, err)
			}
		}
		stars := tx.Bucket(BucketStars)
		for _, st := range c.Stars {
			if st == nil {
				continue
			}
			if err := put(stars, StarKey(st.Number), st); err != nil {
				return fmt.Errorf("star %d: %w", st.Number, err)
			}
		}
		planets := tx.Bucket(BucketPlanets)
		for _, byPlanet := range c.Planets {
			for _, p := range byPlanet {
				if p == nil {
					continue
				}
				if err := put(planets, PlanetKey(p.StarNum, p.PlanetNum), p); err != nil {
					return fmt.Errorf("planet %d/%d: %w", p.StarNum, p.PlanetNum, err)
				}
			}
		}
		races := tx.Bucket(BucketRaces)
		for _, r := range c.Races {
			if r == nil {
				continue
			}
			if err := put(races, RaceKey(r.PlayerNum), r); err != nil {
				return fmt.Errorf("race %d: %w", r.PlayerNum, err)
			}
		}
		if err := put(tx.Bucket(BucketSData), starDataKey, c.StarData); err != nil {
			return fmt.Errorf("star data: %w", err)
		}
		if c.Power != nil {
			if err := put(tx.Bucket(BucketPower), []byte("global"), c.Power); err != nil {
				return fmt.Errorf("power: %w", err)
			}
		}
		if c.Blocks != nil {
			if err := put(tx.Bucket(BucketBlocks), []byte("blocks"), c.Blocks); err != nil {
				return fmt.Errorf("blocks: %w", err)
			}
		}
		if c.PowerBlocks != nil {
			if err := put(tx.Bucket(BucketBlocks), []byte("power_blocks"), c.PowerBlocks); err != nil {
				return fmt.Errorf("power blocks: %w", err)
			}
		}
		news := tx.Bucket(BucketNews)
		for _, n := range c.News {
			if _, err := appendNewsTx(news, n.Type, n.Text); err != nil {
				return fmt.Errorf("news: %w", err)
			}
		}
		tele := tx.Bucket(BucketTelegrams)
		for _, t := range c.Telegrams {
			if err := pushTelegramTx(tele, t.Player, t.Governor, t.Text); err != nil {
				return fmt.Errorf("telegram: %w", err)
			}
		}
		if err := put(meta, KeySchedule, c.Schedule); err != nil {
			return fmt.Errorf("schedule: %w", err)
		}
		rec = CommitRecord{
			SchemaVersion: CommitSchemaVersion,
			CommitSeq:     prev.CommitSeq + 1,
			Turn:          c.Schedule.NumUpdates,
			Segment:       c.Schedule.NumSegments,
			Kind:          c.Kind,
			WallTime:      c.WallTime.UTC(),
			EngineVersion: c.EngineVersion,
		}
		if err := put(meta, KeyCommit, &rec); err != nil {
			return fmt.Errorf("commit record: %w", err)
		}
		return s.fault("after-write")
	})
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// putInitCommitRecord writes the first commit record (kind "init") inside the
// universe-initialisation transaction, so a freshly initialised store starts
// at commit sequence 1 with counters 0/0.
func putInitCommitRecord(meta *bolt.Bucket, now time.Time) error {
	rec := CommitRecord{SchemaVersion: CommitSchemaVersion, CommitSeq: 1, Kind: "init", WallTime: now.UTC()}
	data, err := json.Marshal(&rec)
	if err != nil {
		return err
	}
	return meta.Put(KeyCommit, data)
}

// digestBuckets are the buckets whose full content defines the logical
// world state (plan 8.8). BucketMeta is handled separately so that the
// commit record's wall time is excluded.
var digestBuckets = [][]byte{
	BucketShips, BucketPlanets, BucketStars, BucketRaces, BucketSData,
	BucketPower, BucketBlocks, BucketCommodities, BucketShipFree, BucketCommodFree,
	BucketNews, BucketTelegrams,
}

// WorldDigest returns a hex SHA-256 over every record of the world buckets
// in key order, the universe record, the schedule record without its
// SavedAt bookkeeping, and the commit record's sequence, counters and kind
// (never its wall time). Two stores with the
// same digest hold the same game; the physical files are never compared
// because bbolt's page layout is not deterministic. Tests use it to assert
// "nothing changed" and "exactly the committed state".
func (s *Store) WorldDigest() (string, error) {
	h := sha256.New()
	var lenBuf [8]byte
	write := func(parts ...[]byte) {
		for _, p := range parts {
			binary.BigEndian.PutUint64(lenBuf[:], uint64(len(p)))
			_, _ = h.Write(lenBuf[:])
			_, _ = h.Write(p)
		}
	}
	err := s.db.View(func(tx *bolt.Tx) error {
		for _, name := range digestBuckets {
			b := tx.Bucket(name)
			if b == nil {
				continue
			}
			write(name)
			if err := b.ForEach(func(k, v []byte) error {
				write(k, v)
				return nil
			}); err != nil {
				return err
			}
		}
		meta := tx.Bucket(BucketMeta)
		write(KeyUniverse, meta.Get(KeyUniverse))
		if raw := meta.Get(KeySchedule); raw != nil {
			// SavedAt is bookkeeping (rewritten at every restart), not game
			// state: hash the schedule without it.
			var sc ScheduleRecord
			if err := json.Unmarshal(raw, &sc); err != nil {
				return err
			}
			sc.SavedAt = time.Time{}
			canon, err := json.Marshal(&sc)
			if err != nil {
				return err
			}
			write(KeySchedule, canon)
		}
		if raw := meta.Get(KeyCommit); raw != nil {
			var rec CommitRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				return err
			}
			write(KeyCommit, []byte(fmt.Sprintf("%d/%d/%d/%s", rec.CommitSeq, rec.Turn, rec.Segment, rec.Kind)))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// WriteCount returns bbolt's cumulative count of page writes performed by
// this handle (tests use it to prove that a turn body performs no store
// write before its commit).
func (s *Store) WriteCount() int64 {
	st := s.db.Stats()
	return st.TxStats.GetWrite()
}

// fenceState holds the atomics behind the write fence; embedded in Store.
type fenceState struct {
	fenced       atomic.Bool
	fencedWrites atomic.Uint64
	commitFault  atomic.Pointer[func(stage string) error]
}
