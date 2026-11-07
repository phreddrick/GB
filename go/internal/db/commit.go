package db

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/phreddrick/GB/go/internal/model"
)

// Atomic world commits (CR-18, migration plan section 8.3 and 8.5, first
// slice of Phase 3a).
//
// A turn, a movement segment and the orderly-shutdown flush each persist the
// whole in-memory world, the schedule, the effects the turn produced (news,
// stored telegrams) and the free-list numbers the turn consumed in ONE
// bbolt transaction that also writes the commit record below. bbolt's
// transaction is atomic and fsynced, so the store always holds either the
// state before the commit or the state after it, never a mixture. A failed
// or interrupted transaction writes nothing.
//
// Between a turn's start and its commit the engine refuses every other
// write to the store (the write fence, SetWriteFence): turn code that
// reaches the store by any path other than the commit gets ErrWriteFenced,
// the refusal is counted and named, and the engine aborts the turn instead
// of committing it (fail closed). Number allocation during a turn is staged
// by the engine (game.State.allocShipNum) and persisted by the commit, so
// the allocators are fenced like every other write.

// KeyCommit is the meta key of the commit record.
var KeyCommit = []byte("commit")

// CommitSchemaVersion identifies the layout of the commit record.
const CommitSchemaVersion = 1

// Commit kinds. Every record carries one of these.
const (
	CommitKindInit     = "init"
	CommitKindUpdate   = "update"
	CommitKindSegment  = "segment"
	CommitKindShutdown = "shutdown"
)

// knownCommitKinds is the closed set accepted by Validate.
var knownCommitKinds = map[string]bool{CommitKindInit: true, CommitKindUpdate: true, CommitKindSegment: true, CommitKindShutdown: true}

// CommitRecord is written in the same transaction as every commit. Its
// turn and segment counters are the schedule counters after the commit, so
// a reader can tell which turn the world records belong to.
type CommitRecord struct {
	SchemaVersion int       `json:"schema_version"`
	CommitSeq     uint64    `json:"commit_seq"` // monotonic, +1 per commit, never 0
	Turn          int       `json:"turn"`       // schedule NumUpdates after this commit
	Segment       int       `json:"segment"`    // schedule NumSegments after this commit
	Kind          string    `json:"kind"`       // one of the CommitKind* constants
	WallTime      time.Time `json:"wall_time"`
	EngineVersion string    `json:"engine_version,omitempty"`
}

// ErrCorruptCommitRecord wraps every defect found in an existing commit
// record (a missing record is not corrupt: GetCommitRecord returns nil, nil).
var ErrCorruptCommitRecord = errors.New("corrupt commit record")

// Validate checks a record's format: schema version, kind, sequence (non
// zero, not at the overflow boundary), counters (non negative; an init
// record is 0/0; an update record has segment 0) and wall time.
func (r *CommitRecord) Validate() error {
	var problems []string
	add := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }
	if r.SchemaVersion != CommitSchemaVersion {
		add("schema_version %d (supported: %d)", r.SchemaVersion, CommitSchemaVersion)
	}
	if !knownCommitKinds[r.Kind] {
		add("unknown kind %q", r.Kind)
	}
	switch r.CommitSeq {
	case 0:
		add("commit_seq is 0")
	case math.MaxUint64:
		add("commit_seq %d would overflow at the next commit", r.CommitSeq)
	}
	if r.Turn < 0 {
		add("turn %d is negative", r.Turn)
	}
	if r.Segment < 0 {
		add("segment %d is negative", r.Segment)
	}
	if r.Kind == CommitKindInit && (r.Turn != 0 || r.Segment != 0) {
		add("init record has counters %d/%d (want 0/0)", r.Turn, r.Segment)
	}
	if r.Kind == CommitKindUpdate && r.Segment != 0 {
		add("update record has segment %d (an update resets the segment counter)", r.Segment)
	}
	if r.WallTime.IsZero() {
		add("wall_time is missing")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrCorruptCommitRecord, problems)
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

	// ShipFreePops and CommodFreePops are the free-list numbers the turn
	// consumed, in pop order (most recent free entry first). The commit
	// deletes exactly these entries from the tail of the free lists and
	// fails if the lists do not end with them.
	ShipFreePops   []uint32
	CommodFreePops []uint32

	// Progress, when set, is called once per record written (liveness).
	Progress func()
}

// ErrWriteFenced is returned by every ordinary write while the write fence
// is raised (a turn is running and only its commit may write).
var ErrWriteFenced = errors.New("store write refused: a turn is in progress and only its commit may write (CR-18 write fence)")

// ErrInvalidCommit is returned when a WorldCommit is not self-consistent.
var ErrInvalidCommit = errors.New("invalid world commit")

// ErrFreeListMismatch is returned when the free list does not end with the
// numbers a turn claims to have popped (another writer changed it).
var ErrFreeListMismatch = errors.New("free list does not end with the numbers the turn allocated")

// SetWriteFence raises or lowers the write fence. While raised, every write
// method except CommitWorld returns ErrWriteFenced and counts the refusal
// (FencedWrites, LastFencedWrite).
func (s *Store) SetWriteFence(on bool) { s.fenced.Store(on) }

// FencedWrites returns how many writes the fence has refused since Open.
func (s *Store) FencedWrites() uint64 { return s.fencedWrites.Load() }

// LastFencedWrite names the most recent write method the fence refused
// (empty when none).
func (s *Store) LastFencedWrite() string {
	s.fenceMu.Lock()
	defer s.fenceMu.Unlock()
	return s.lastFenced
}

// writeAllowed is the fence check at the top of every ordinary write; op
// names the method for diagnostics.
func (s *Store) writeAllowed(op string) error {
	if s.fenced.Load() {
		s.fencedWrites.Add(1)
		s.fenceMu.Lock()
		s.lastFenced = op
		s.fenceMu.Unlock()
		return ErrWriteFenced
	}
	return nil
}

// SetCommitFault installs a test-only fault hook called inside the commit
// transaction: with stage "before-write" before any record is written and
// "after-write" after every record, the schedule and the commit record have
// been put but before the transaction commits. A non-nil error aborts the
// transaction (bbolt rolls it back); the hook may also panic or exit the
// process to simulate a crash with an open write transaction. Production
// code never sets it.
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
// none (a store written only by pre-commit binaries). A record that is
// present but undecodable, JSON null, empty or invalid (Validate) is
// reported as ErrCorruptCommitRecord: an absent record and a malformed one
// are different conditions.
func (s *Store) GetCommitRecord() (*CommitRecord, error) {
	raw, err := s.GetMeta(KeyCommit)
	if err != nil || raw == nil {
		return nil, err
	}
	return decodeCommitRecord(raw)
}

func decodeCommitRecord(raw []byte) (*CommitRecord, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("%w: record is JSON null", ErrCorruptCommitRecord)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var rec CommitRecord
	if err := dec.Decode(&rec); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptCommitRecord, err)
	}
	if err := rec.Validate(); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (c *WorldCommit) validate() error {
	switch {
	case !knownCommitKinds[c.Kind] || c.Kind == CommitKindInit:
		return fmt.Errorf("%w: kind %q", ErrInvalidCommit, c.Kind)
	case c.Schedule == nil:
		return fmt.Errorf("%w: schedule is nil", ErrInvalidCommit)
	case c.Schedule.NumUpdates < 0 || c.Schedule.NumSegments < 0:
		return fmt.Errorf("%w: negative schedule counters", ErrInvalidCommit)
	case c.Kind == CommitKindUpdate && c.Schedule.NumSegments != 0:
		return fmt.Errorf("%w: update commit with segment counter %d", ErrInvalidCommit, c.Schedule.NumSegments)
	case c.Stars == nil || c.Planets == nil || c.Races == nil || c.Ships == nil:
		return fmt.Errorf("%w: world collections are nil", ErrInvalidCommit)
	case c.StarData == nil:
		return fmt.Errorf("%w: star data is nil", ErrInvalidCommit)
	case c.WallTime.IsZero():
		return fmt.Errorf("%w: wall time is zero", ErrInvalidCommit)
	}
	return nil
}

// CommitWorld writes c in one transaction and returns the commit record it
// wrote. On any error nothing is written and the previous commit stays the
// store's content. It bypasses the write fence by design: it is the fence's
// only sanctioned writer. The previous record, if present, must be valid
// and its sequence must not be at the overflow boundary.
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
		var prevSeq uint64
		if raw := meta.Get(KeyCommit); raw != nil {
			prev, err := decodeCommitRecord(raw)
			if err != nil {
				return fmt.Errorf("previous commit record: %w", err)
			}
			prevSeq = prev.CommitSeq
		}
		if prevSeq == math.MaxUint64 {
			return fmt.Errorf("%w: commit sequence would overflow", ErrInvalidCommit)
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
		if err := popFreeNumsTx(tx.Bucket(BucketShipFree), c.ShipFreePops); err != nil {
			return fmt.Errorf("ship free list: %w", err)
		}
		if err := popFreeNumsTx(tx.Bucket(BucketCommodFree), c.CommodFreePops); err != nil {
			return fmt.Errorf("commodity free list: %w", err)
		}
		if err := put(meta, KeySchedule, c.Schedule); err != nil {
			return fmt.Errorf("schedule: %w", err)
		}
		rec = CommitRecord{
			SchemaVersion: CommitSchemaVersion,
			CommitSeq:     prevSeq + 1,
			Turn:          c.Schedule.NumUpdates,
			Segment:       c.Schedule.NumSegments,
			Kind:          c.Kind,
			WallTime:      c.WallTime.UTC(),
			EngineVersion: c.EngineVersion,
		}
		if err := rec.Validate(); err != nil {
			return err
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

// popFreeNumsTx deletes the last len(expected) entries of a free-list
// bucket, verifying that they are exactly the expected numbers in pop
// order (last entry first). It is the commit-time counterpart of the
// engine's staged allocation during a turn.
func popFreeNumsTx(b *bolt.Bucket, expected []uint32) error {
	if len(expected) == 0 {
		return nil
	}
	cur := b.Cursor()
	k, v := cur.Last()
	for i, want := range expected {
		if k == nil {
			return fmt.Errorf("%w: list exhausted after %d of %d", ErrFreeListMismatch, i, len(expected))
		}
		got := binary.BigEndian.Uint32(v)
		if got != want {
			return fmt.Errorf("%w: entry %d is %d, turn popped %d", ErrFreeListMismatch, i, got, want)
		}
		key := append([]byte(nil), k...)
		k, v = cur.Prev()
		if err := b.Delete(key); err != nil {
			return err
		}
	}
	return nil
}

// FreeShipNums returns the ship free list in pop order (most recently
// freed first), without changing it.
func (s *Store) FreeShipNums() ([]uint32, error) { return s.freeNums(BucketShipFree) }

// FreeCommodNums returns the commodity free list in pop order.
func (s *Store) FreeCommodNums() ([]uint32, error) { return s.freeNums(BucketCommodFree) }

func (s *Store) freeNums(bucket []byte) ([]uint32, error) {
	var out []uint32
	err := s.db.View(func(tx *bolt.Tx) error {
		cur := tx.Bucket(bucket).Cursor()
		for k, v := cur.Last(); k != nil; k, v = cur.Prev() {
			out = append(out, binary.BigEndian.Uint32(v))
		}
		return nil
	})
	return out, err
}

// MaxShipNum returns the highest ship number stored (0 when none).
func (s *Store) MaxShipNum() (uint32, error) { return s.maxKey(BucketShips) }

// MaxCommodNum returns the highest commodity number stored (0 when none).
func (s *Store) MaxCommodNum() (uint32, error) { return s.maxKey(BucketCommodities) }

func (s *Store) maxKey(bucket []byte) (uint32, error) {
	var num uint32
	err := s.db.View(func(tx *bolt.Tx) error {
		k, _ := tx.Bucket(bucket).Cursor().Last()
		if len(k) == 4 {
			num = binary.BigEndian.Uint32(k)
		}
		return nil
	})
	return num, err
}

// putInitCommitRecord writes the first commit record (kind "init") inside the
// universe-initialisation transaction, so a freshly initialised store starts
// at commit sequence 1 with counters 0/0.
func putInitCommitRecord(meta *bolt.Bucket, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	rec := CommitRecord{SchemaVersion: CommitSchemaVersion, CommitSeq: 1, Kind: CommitKindInit, WallTime: now.UTC()}
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
// (never its wall time). Undecodable meta records are hashed raw. Two
// stores with the same digest hold the same game; the physical files are
// never compared because bbolt's page layout is not deterministic. Tests
// use it to assert "nothing changed" and "exactly the committed state".
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
			if err := json.Unmarshal(raw, &sc); err == nil {
				sc.SavedAt = time.Time{}
				if canon, err := json.Marshal(&sc); err == nil {
					write(KeySchedule, canon)
				} else {
					write(KeySchedule, raw)
				}
			} else {
				write(KeySchedule, raw)
			}
		}
		if raw := meta.Get(KeyCommit); raw != nil {
			if rec, err := decodeCommitRecord(raw); err == nil {
				write(KeyCommit, []byte(fmt.Sprintf("%d/%d/%d/%s", rec.CommitSeq, rec.Turn, rec.Segment, rec.Kind)))
			} else {
				write(KeyCommit, raw)
			}
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
	fenceMu      sync.Mutex
	lastFenced   string
	commitFault  atomic.Pointer[func(stage string) error]
}
