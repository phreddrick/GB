package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/admin"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/engine"
	"github.com/phreddrick/GB/go/internal/universe"
)

type fakeEngine struct{ mode engine.Mode }

func (f *fakeEngine) Status() engine.Status                { return engine.Status{Mode: f.mode.String()} }
func (f *fakeEngine) TriggerUpdate(context.Context) error  { return nil }
func (f *fakeEngine) TriggerSegment(context.Context) error { return nil }
func (f *fakeEngine) Quiesce(context.Context) error        { return nil }
func (f *fakeEngine) Resume(context.Context) error {
	if f.mode == engine.ModeHalted {
		return engine.ErrHalted
	}
	return nil
}
func (f *fakeEngine) InitUniverse(_ context.Context, p universe.Params, seed int64) (*db.UniverseRecord, error) {
	if f.mode != engine.ModeUninitialized {
		return nil, universe.ErrAlreadyInitialized
	}
	// A complete record, as universe.Init writes it (GB1-06R).
	if p.Stars == 0 {
		p.Stars = universe.DefaultStars
	}
	if p.MinPlanets == 0 {
		p.MinPlanets = universe.DefaultMinPlanets
	}
	if p.MaxPlanets == 0 {
		p.MaxPlanets = universe.DefaultMaxPlanets
	}
	return &db.UniverseRecord{Seed: seed, Stars: p.Stars, MinPlanets: p.MinPlanets, MaxPlanets: p.MaxPlanets,
		Planets: p.Stars * p.MinPlanets, Generator: universe.GeneratorVersion, InitializedAt: time.Now().UTC(), Tool: "test"}, nil
}

const token = "admin-token-0123456789"

func start(t *testing.T, mode engine.Mode) string {
	t.Helper()
	s, err := admin.New(admin.Options{Addr: "127.0.0.1:0", Token: token, Engine: &fakeEngine{mode: mode}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestExitCodes(t *testing.T) {
	url := start(t, engine.ModeServing)
	run1 := func(args []string, e map[string]string) (int, string, string) {
		var out, errb bytes.Buffer
		code := run(args, &out, &errb, env(e))
		return code, out.String(), errb.String()
	}
	good := map[string]string{"GBCTL_TOKEN": token, "GBCTL_SERVER": url}

	if code, out, _ := run1([]string{"status"}, good); code != exitOK || !strings.Contains(out, "mode:          SERVING") {
		t.Fatalf("status: %d %q", code, out)
	}
	if code, out, _ := run1([]string{"-json", "update"}, good); code != exitOK || !strings.Contains(out, `"ok": true`) {
		t.Fatalf("update json: %d %q", code, out)
	}
	if code, _, errs := run1([]string{}, good); code != exitUsage || !strings.Contains(errs, "usage") {
		t.Fatalf("no command: %d %q", code, errs)
	}
	if code, _, _ := run1([]string{"frobnicate"}, good); code != exitUsage {
		t.Fatalf("unknown command: %d", code)
	}
	if code, _, errs := run1([]string{"status"}, map[string]string{"GBCTL_SERVER": url}); code != exitUsage || !strings.Contains(errs, "GBCTL_TOKEN") {
		t.Fatalf("missing token: %d %q", code, errs)
	}
	if code, _, errs := run1([]string{"status"}, map[string]string{"GBCTL_TOKEN": "wrong-token-0123456789", "GBCTL_SERVER": url}); code != exitAuth || strings.Contains(errs, "wrong-token") {
		t.Fatalf("bad token: %d %q", code, errs)
	}
	if code, _, _ := run1([]string{"-server", "http://127.0.0.1:1", "status"}, map[string]string{"GBCTL_TOKEN": token}); code != exitConnect {
		t.Fatalf("unreachable: %d", code)
	}
	if code, _, errs := run1([]string{"universe", "init"}, good); code != exitUsage || !strings.Contains(errs, "-seed") {
		t.Fatalf("init without seed: %d %q", code, errs)
	}
	if code, _, errs := run1([]string{"universe", "init", "-seed", "7"}, good); code != exitRefused || !strings.Contains(errs, "already") {
		t.Fatalf("init on initialised: %d %q", code, errs)
	}
	// Token file, never printed.
	dir := t.TempDir()
	tf := filepath.Join(dir, "tok")
	if err := os.WriteFile(tf, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errs := run1([]string{"status"}, map[string]string{"GBCTL_TOKEN_FILE": tf, "GBCTL_SERVER": url}); code != exitOK || strings.Contains(out+errs, token) {
		t.Fatalf("token file: %d %q %q", code, out, errs)
	}
	if code, _, _ := run1([]string{"status"}, map[string]string{"GBCTL_TOKEN": token, "GBCTL_TOKEN_FILE": tf, "GBCTL_SERVER": url}); code != exitUsage {
		t.Fatalf("both token sources: %d", code)
	}

	// Uninitialised engine: init succeeds; halted engine: resume refused.
	url2 := start(t, engine.ModeUninitialized)
	if code, out, _ := run1([]string{"universe", "init", "-seed", "9", "-stars", "4"}, map[string]string{"GBCTL_TOKEN": token, "GBCTL_SERVER": url2}); code != exitOK || !strings.Contains(out, "universe init: ok") || !strings.Contains(out, "seed 9, 4 stars") {
		t.Fatalf("init: %d %q", code, out)
	}
	url3 := start(t, engine.ModeHalted)
	if code, _, errs := run1([]string{"resume"}, map[string]string{"GBCTL_TOKEN": token, "GBCTL_SERVER": url3}); code != exitRefused || !strings.Contains(errs, "HALTED") {
		t.Fatalf("resume halted: %d %q", code, errs)
	}
	_ = http.StatusOK
}

// startHandler returns the admin listener's handler for an uninitialised
// fake engine (used by tests that drive run() through runAgainst).
func startHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	s, err := admin.New(admin.Options{Addr: "127.0.0.1:0", Token: token, Engine: &fakeEngine{mode: engine.ModeUninitialized}})
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler().ServeHTTP
}
