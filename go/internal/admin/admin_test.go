package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/engine"
	"github.com/phreddrick/GB/go/internal/platform/metrics"
	"github.com/phreddrick/GB/go/internal/universe"
)

type fakeEngine struct {
	mu    sync.Mutex
	mode  engine.Mode
	calls []string
	err   error
	init  *db.UniverseRecord
	slow  time.Duration
}

func (f *fakeEngine) record(op string, ctx context.Context) error {
	f.mu.Lock()
	f.calls = append(f.calls, op)
	err, slow := f.err, f.slow
	f.mu.Unlock()
	if slow > 0 {
		select {
		case <-time.After(slow):
		case <-ctx.Done():
			return engine.ErrEngineBusy
		}
	}
	return err
}
func (f *fakeEngine) Status() engine.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return engine.Status{Mode: f.mode.String(), Persistence: engine.PersistenceNote}
}
func (f *fakeEngine) TriggerUpdate(ctx context.Context) error  { return f.record("update", ctx) }
func (f *fakeEngine) TriggerSegment(ctx context.Context) error { return f.record("segment", ctx) }
func (f *fakeEngine) Quiesce(ctx context.Context) error        { return f.record("quiesce", ctx) }
func (f *fakeEngine) Resume(ctx context.Context) error         { return f.record("resume", ctx) }
func (f *fakeEngine) InitUniverse(ctx context.Context, p universe.Params, seed int64) (*db.UniverseRecord, error) {
	if err := f.record("init", ctx); err != nil {
		return nil, err
	}
	return completeRecord(p, seed), nil
}

// completeRecord mirrors what universe.Init records (every field set): the
// client validates the record against that contract (GB1-06R).
func completeRecord(p universe.Params, seed int64) *db.UniverseRecord {
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
		Planets: p.Stars * p.MinPlanets, Generator: universe.GeneratorVersion, InitializedAt: time.Now().UTC(), Tool: "test"}
}

const token = "admin-token-0123456789"

func newTest(t *testing.T, fe *fakeEngine) (*Server, *httptest.Server, *Client) {
	t.Helper()
	reg := metrics.NewRegistry()
	metrics.NewCounter(reg, "gb_test_total", "test").Inc()
	s, err := New(Options{Addr: "127.0.0.1:0", Token: token, Engine: fe, Registry: reg,
		Livez: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }, OpTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts, &Client{BaseURL: ts.URL, Token: token, HTTP: ts.Client()}
}

func TestNewRequiresToken(t *testing.T) {
	if _, err := New(Options{Addr: "127.0.0.1:0", Token: " ", Engine: &fakeEngine{}}); err == nil {
		t.Fatal("empty token accepted")
	}
	if _, err := New(Options{Addr: "127.0.0.1:0", Token: token}); err == nil {
		t.Fatal("nil engine accepted")
	}
}

func TestAuthRequired(t *testing.T) {
	fe := &fakeEngine{mode: engine.ModeServing}
	s, ts, _ := newTest(t, fe)
	for _, hdr := range []string{"", "Bearer ", "Bearer wrong", "Basic abc", "Bearer " + token + "x", "bearer " + token} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/admin/v1/update", nil)
		if hdr != "" {
			req.Header.Set("Authorization", hdr)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("header %q: status %d, want 401", hdr, resp.StatusCode)
		}
	}
	fe.mu.Lock()
	n := len(fe.calls)
	fe.mu.Unlock()
	if n != 0 {
		t.Fatal("unauthenticated requests reached the engine")
	}
	if !s.TokenMatches(token) || s.TokenMatches(token[:len(token)-1]) || s.TokenMatches("") {
		t.Fatal("TokenMatches")
	}
	// Wrong method with a valid token.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/admin/v1/update", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ := ts.Client().Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET update: %d", resp.StatusCode)
	}
	// Client surfaces 401 distinctly.
	bad := &Client{BaseURL: ts.URL, Token: "nope", HTTP: ts.Client()}
	if _, err := bad.Status(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("client 401: %v", err)
	}
}

func TestOperationsAndStatusCodes(t *testing.T) {
	fe := &fakeEngine{mode: engine.ModeServing}
	_, ts, c := newTest(t, fe)
	ctx := context.Background()
	for _, f := range []func(context.Context) (*Response, error){c.Status, c.Update, c.Segment, c.Quiesce, c.Resume} {
		r, err := f(ctx)
		if err != nil || !r.OK || r.Status == nil || r.Status.Mode != "SERVING" {
			t.Fatalf("op: %+v %v", r, err)
		}
	}
	r, err := c.UniverseInit(ctx, InitRequest{Seed: 42, Stars: 5, MinPlanets: 1, MaxPlanets: 2})
	if err != nil || !r.OK {
		t.Fatalf("init: %+v %v", r, err)
	}
	if rec, ok := r.Result.(*db.UniverseRecord); !ok || rec.Seed != 42 || rec.Stars != 5 || rec.Generator != universe.GeneratorVersion {
		t.Fatalf("init result %#v", r.Result)
	}
	fe.mu.Lock()
	calls := strings.Join(fe.calls, ",")
	fe.mu.Unlock()
	if calls != "update,segment,quiesce,resume,init" {
		t.Fatalf("calls %s", calls)
	}

	// Error mapping.
	cases := map[error]int{
		engine.ErrHalted:               409,
		engine.ErrNotServing:           409,
		universe.ErrAlreadyInitialized: 409,
		engine.ErrEngineBusy:           503,
		engine.ErrEngineStopped:        503,
		errors.New("boom"):             500,
	}
	for e, want := range cases {
		fe.mu.Lock()
		fe.err = e
		fe.mu.Unlock()
		_, err := c.Update(ctx)
		var ae *Error
		if !errors.As(err, &ae) || ae.Code != want || !strings.Contains(ae.Msg, e.Error()) {
			t.Errorf("error %v: got %v, want code %d", e, err, want)
		}
	}
	fe.mu.Lock()
	fe.err = nil
	fe.mu.Unlock()

	// Bad init bodies.
	for _, body := range []string{`{}`, `{"seed":1,"stars":999}`, `{"seed":1,"bogus":2}`, `not json`} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/admin/v1/universe/init", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, _ := ts.Client().Do(req)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %s: %d, want 400", body, resp.StatusCode)
		}
	}
}

func TestOpTimeoutAndMetrics(t *testing.T) {
	fe := &fakeEngine{mode: engine.ModeServing, slow: 10 * time.Second}
	_, ts, c := newTest(t, fe)
	start := time.Now()
	_, err := c.Update(context.Background())
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != 503 || time.Since(start) > 8*time.Second {
		t.Fatalf("slow op: %v after %s", err, time.Since(start))
	}
	resp, err := http.Get(ts.URL + "/metrics") //nolint:gosec // G107: test URL
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(data), "gb_test_total 1") || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("metrics: %d %q %s", resp.StatusCode, resp.Header.Get("Content-Type"), data)
	}
	if resp, err := http.Get(ts.URL + "/livez"); err != nil || resp.StatusCode != 200 { //nolint:gosec // G107: test URL
		t.Fatalf("livez on admin listener: %v", err)
	} else {
		_ = resp.Body.Close()
	}
	// Status reply is well-formed JSON with the persistence note.
	r, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), "CR-18") {
		t.Fatalf("status lacks persistence note: %s", b)
	}
}
