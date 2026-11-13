package db

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

// GroundAssault is a closed actor-only one-step nonmechanical sector assault.
// Required news/inbox writes are derived by this operation, never optional.
// No allocator, turn/commit metadata, counter, schema or retry record changes.
type GroundAssault struct {
	Input    commandrules.AssaultInput
	Draws    []commandrules.AssaultDraw
	Proposed commandrules.AssaultChange
	At       time.Time
}
type preparedAssault struct {
	checks, writes []commandRecord
	news           NewsEntry
	telegram       TelegramEntry
}

// AssaultMessages returns the exact existing-format durable effects and live
// hint. The timestamp is server-owned preparation context, not a player claim.
func AssaultMessages(in GroundAssault) (NewsEntry, TelegramEntry, error) {
	if in.At.IsZero() || in.At.Year() < 1970 || in.At.Year() > 9999 || in.Input.Star == nil || in.Input.Defender == nil || in.Input.Defender.PlayerNum < 1 || in.Input.Defender.PlayerNum > model.MaxPlayers {
		return NewsEntry{}, TelegramEntry{}, commandError(CommandInvalid)
	}
	news, tele := commandrules.AssaultText(in.Input, in.Proposed)
	at := in.At.UTC()
	prefix := fmt.Sprintf("%2d/%2d %02d:%02d:%02d ", at.Month(), at.Day(), at.Hour(), at.Minute(), at.Second())
	if len(news) > 4000 || len(tele) > 4000 || strings.ContainsRune(news, 0) || strings.ContainsRune(tele, 0) {
		return NewsEntry{}, TelegramEntry{}, commandError(CommandInvalid)
	}
	return NewsEntry{Type: model.NewsCombat, Text: prefix + news}, TelegramEntry{Player: in.Input.Defender.PlayerNum, Governor: int(in.Input.Star.Governor[in.Input.Defender.PlayerNum-1]), Text: prefix + tele}, nil
}
func prepareAssault(in GroundAssault) (preparedAssault, error) {
	var out preparedAssault
	want, e := commandrules.PlanAssault(in.Input, in.Draws)
	if e != nil || !reflect.DeepEqual(want, in.Proposed) {
		return out, commandError(CommandInvalid)
	}
	out.news, out.telegram, e = AssaultMessages(in)
	if e != nil {
		return out, e
	}
	if out.telegram.Governor < 0 || out.telegram.Governor > model.MaxGovernors {
		return out, commandError(CommandInvalid)
	}
	add := func(bucket, key []byte, before, after any, write bool) error {
		b, e := json.Marshal(before)
		if e != nil {
			return e
		}
		a, e := json.Marshal(after)
		if e != nil {
			return e
		}
		rec := commandRecord{bucket, key, b, a}
		out.checks = append(out.checks, rec)
		if write {
			out.writes = append(out.writes, rec)
		}
		return nil
	}
	p := in.Input.Planet
	st := in.Input.Star
	a, d := in.Input.Attacker, in.Input.Defender
	for _, record := range []struct {
		bucket, key   []byte
		before, after any
	}{
		{BucketPlanets, PlanetKey(p.StarNum, p.PlanetNum), p, want.Planet},
		{BucketRaces, RaceKey(a.PlayerNum), a, want.Attacker},
		{BucketRaces, RaceKey(d.PlayerNum), d, want.Defender},
		{BucketStars, StarKey(st.Number), st, want.Star},
	} {
		if e := add(record.bucket, record.key, record.before, record.after, true); e != nil {
			return preparedAssault{}, commandError(CommandInvalid)
		}
	}
	for _, r := range in.Input.Races {
		if e := add(BucketRaces, RaceKey(r.PlayerNum), r, r, false); e != nil {
			return preparedAssault{}, commandError(CommandInvalid)
		}
	}
	for _, sh := range in.Input.Ships {
		if e := add(BucketShips, ShipKey(sh.Number), sh, sh, false); e != nil {
			return preparedAssault{}, commandError(CommandInvalid)
		}
	}
	return out, nil
}

// Existing sequence mechanisms are transaction-local; check exhausted/stale
// sequence state before invoking them, rather than permitting wrap/overwrite.
func (s *Store) writeAssaultEffects(tx *bolt.Tx, p *preparedAssault) error {
	news, tele := tx.Bucket(BucketNews), tx.Bucket(BucketTelegrams)
	if news == nil || tele == nil {
		return commandError(CommandCorrupt)
	}
	if news.Get(NewsKey(p.news.Type, math.MaxUint64)) != nil || tele.Sequence() == math.MaxUint64 {
		return commandError(CommandCorrupt)
	}
	next := tele.Sequence() + 1
	key := TelegramKeyPG(clampByte(p.telegram.Player), clampByte(p.telegram.Governor), next)
	if tele.Get(key) != nil {
		return commandError(CommandCorrupt)
	}
	// A stale global sequence must not be silently reused even in another inbox.
	if e := tele.ForEach(func(k, v []byte) error {
		if len(k) != 10 || v == nil {
			return commandError(CommandCorrupt)
		}
		if bytes.Compare(k[2:], TelegramKeyPG(0, 0, next)[2:]) >= 0 {
			return commandError(CommandCorrupt)
		}
		return nil
	}); e != nil {
		return e
	}
	// appendNewsTx's legacy cursor assumes well-formed keys. Prove that
	// assumption for this stream and the exact next key before calling it.
	var latest uint64
	if e := news.ForEach(func(k, v []byte) error {
		if len(k) > 0 && k[0] == p.news.Type {
			if len(k) != 9 || v == nil {
				return commandError(CommandCorrupt)
			}
			n := binary.BigEndian.Uint64(k[1:])
			if n == 0 {
				return commandError(CommandCorrupt)
			}
			latest = max(latest, n)
		}
		return nil
	}); e != nil {
		return e
	}
	if latest == math.MaxUint64 || news.Get(NewsKey(p.news.Type, latest+1)) != nil {
		return commandError(CommandCorrupt)
	}
	seq, e := appendNewsTx(news, p.news.Type, p.news.Text)
	if e != nil || seq != latest+1 {
		return commandError(CommandIO)
	}
	p.checks = append(p.checks, commandRecord{BucketNews, NewsKey(p.news.Type, seq), nil, []byte(p.news.Text)})
	if e := s.metaFault("assault.news"); e != nil {
		return commandError(CommandIO)
	}
	if e := pushTelegramTx(tele, p.telegram.Player, p.telegram.Governor, p.telegram.Text); e != nil {
		return commandError(CommandIO)
	}
	p.checks = append(p.checks, commandRecord{BucketTelegrams, key, nil, []byte(p.telegram.Text)})
	if e := s.metaFault("assault.telegram"); e != nil {
		return commandError(CommandIO)
	}
	return nil
}

func (s *Store) CommitGroundAssault(in GroundAssault) (outcome CommandOutcome, err error) {
	outcome = CommandNotCommitted
	if e := s.writeAllowed("CommitGroundAssault"); e != nil {
		return outcome, commandError(CommandFenced)
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	if s.CommandWriteUncertain() {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain, Readback: "unavailable"}
	}
	phase := 0
	var prepared preparedAssault
	defer func() {
		if recover() != nil {
			switch phase {
			case 3:
				outcome = CommandCommitted
			case 2:
				outcome = CommandUncertain
			default:
				outcome = CommandNotCommitted
			}
			err = &CommandError{Code: CommandIO, Outcome: outcome}
		}
		if outcome == CommandUncertain {
			s.commandUncertain.Store(true)
			if ce, ok := err.(*CommandError); ok {
				ce.Readback = s.commandReadback(prepared.checks)
			}
		}
	}()
	var e error
	prepared, e = prepareAssault(in)
	if e != nil {
		return outcome, e
	}
	if e = s.metaFault("assault.before"); e != nil {
		return outcome, commandError(CommandIO)
	}
	e = s.db.Update(func(tx *bolt.Tx) error {
		phase = 1
		if e := s.writeAllowed("CommitGroundAssault"); e != nil {
			return commandError(CommandFenced)
		}
		meta := tx.Bucket(BucketMeta)
		if meta == nil {
			return commandError(CommandCorrupt)
		}
		if raw := meta.Get(KeyMaintenance); raw != nil {
			if _, e := DecodeMaintenanceRecord(raw); e != nil {
				return commandError(CommandCorrupt)
			}
			return commandError(CommandInvalid)
		}
		for i, r := range prepared.checks {
			b := tx.Bucket(r.bucket)
			if b == nil {
				return commandError(CommandCorrupt)
			}
			if !bytes.Equal(b.Get(r.key), r.before) {
				return commandError(CommandStale)
			}
			if e := s.metaFault(fmt.Sprintf("assault.check:%d", i+1)); e != nil {
				return commandError(CommandIO)
			}
		}
		// Revalidate under this transaction after all complete preimages matched.
		// Pure replay consumes no additional random values.
		verified, e := prepareAssault(in)
		if e != nil || !reflect.DeepEqual(verified, prepared) {
			return commandError(CommandInvalid)
		}
		if e := s.metaFault("assault.validated"); e != nil {
			return commandError(CommandIO)
		}
		for i, r := range prepared.writes {
			if e := tx.Bucket(r.bucket).Put(r.key, r.after); e != nil {
				return commandError(CommandIO)
			}
			if e := s.metaFault(fmt.Sprintf("assault.write:%d", i+1)); e != nil {
				return commandError(CommandIO)
			}
		}
		if e := s.writeAssaultEffects(tx, &prepared); e != nil {
			return e
		}
		if e := s.metaFault("assault.before-commit"); e != nil {
			return commandError(CommandIO)
		}
		phase = 2
		return nil
	})
	if e != nil {
		if phase == 2 {
			return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain}
		}
		var ce *CommandError
		if errors.As(e, &ce) {
			return outcome, ce
		}
		return outcome, commandError(CommandIO)
	}
	if e = s.metaFault("assault.commit-error"); e != nil {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain}
	}
	phase = 3
	outcome = CommandCommitted
	if e = s.metaFault("assault.after-commit"); e != nil {
		return outcome, &CommandError{Code: CommandIO, Outcome: outcome}
	}
	return outcome, nil
}
