package db_test

// Copy ONLY this test into an exported pre-CR-07 tree and build its db test
// binary. It uses exclusively the old public APIs against the disposable path
// supplied by TestEnrollmentCR07BAndCompatibleStartup. It is not a game tool.
import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/game"
)

func TestCR07CLegacyReader(t *testing.T) {
	path := os.Getenv("CR07C_COMPAT_PATH")
	if path == "" {
		t.Skip("requires disposable CR-07C fixture")
	}
	s, e := db.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close() }()
	raw, e := s.GetMeta([]byte("enrollment/v1/placement/integration"))
	if e != nil || len(raw) == 0 {
		t.Fatal("missing additive placement fixture", e)
	}
	if r, e := s.GetCommitRecord(); e != nil || r == nil {
		t.Fatal("commit", e)
	}
	if r, e := s.GetSchedule(); e != nil || r == nil {
		t.Fatal("schedule", e)
	}
	if r, e := s.GetMaintenance(); e != nil || r == nil {
		t.Fatal("maintenance", e)
	}
	before := map[string][]byte{}
	for _, k := range [][]byte{db.KeyCommit, db.KeySchedule, db.KeyMaintenance, db.KeyUniverse} {
		before[string(k)], _ = s.GetMeta(k)
	}
	digest, e := s.WorldDigest()
	if e != nil {
		t.Fatal(e)
	}
	state, e := game.New(config.Default(), s, game.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) }})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		s.SetCommitFault(func(stage string) error { return errors.New("isolated stop intentionally aborted") })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = state.Stop(ctx)
	}()
	if state.Mode() != game.ModeQuiesced {
		t.Fatal("legacy startup rejected additive metadata", state.Mode())
	}
	after, e := s.WorldDigest()
	if e != nil || digest != after {
		t.Fatal("legacy startup wrote world", e)
	}
	for k, v := range before {
		raw, e := s.GetMeta([]byte(k))
		if e != nil || !bytes.Equal(v, raw) {
			t.Fatal("legacy startup changed metadata", k, e)
		}
	}
	t.Log("CR07C_OLD_READER_PASS")
}
