package game

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/enrollmentapi"
	gbnet "github.com/phreddrick/GB/go/internal/net"
)

func TestEnrollmentCurrentSessionAuthority(t *testing.T) {
	f := newEnrollmentActor(t)
	enrollActor(t, f, actorEnrollmentRequest("operator"))
	srv := gbnet.New(f.cfg, f.state, gbnet.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	onEnrollmentActor(t, f, func() { f.state.SetRegistry(srv.Registry()) })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	body := `{"race":"operator species","race_password":"fixture-r","governor_password":"fixture-g","governor":0}`
	res, e := http.Post(ts.URL+"/auth/login", "application/json", strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	var answer struct {
		Token string `json:"token"`
	}
	e = json.NewDecoder(res.Body).Decode(&answer)
	_ = res.Body.Close()
	if e != nil || res.StatusCode != 200 || answer.Token == "" {
		t.Fatal("real dual login", res.StatusCode, e)
	}
	auth, e := f.state.AuthenticateDual(context.Background(), "operator species", "fixture-r", "fixture-g", 0)
	if e != nil || !auth.OK {
		t.Fatal(auth, e)
	}
	identity := gbnet.Identity{PlayerNum: 1, Governor: 0, God: true, Revision: auth.Revision, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	for _, mutate := range []func(*gbnet.Identity){func(i *gbnet.Identity) { i.God = false }, func(i *gbnet.Identity) { i.Governor = 1 }, func(i *gbnet.Identity) { i.Governor = -1 }, func(i *gbnet.Identity) { i.ExpiresAt = 1 }, func(i *gbnet.Identity) { i.Revision = "" }} {
		bad := identity
		mutate(&bad)
		if ok, e := f.state.ValidateSession(context.Background(), bad); e != nil || ok {
			t.Fatal("forged/stale identity", e)
		}
	}
	header := http.Header{"Authorization": []string{"Bearer " + answer.Token}}
	conn, _, e := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", header)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, b, e := conn.ReadMessage(); e != nil || !bytes.Contains(b, []byte("Welcome")) {
		t.Fatal("welcome", e)
	}
	// Actor changes revoke both an existing socket and a later upgrade. No
	// transport hint can re-enable an inactive governor.
	onEnrollmentActor(t, f, func() { f.state.races[0].Governor[0].Active = false })
	if ok, e := f.state.ValidateSession(context.Background(), identity); e != nil || ok {
		t.Fatal("revoked session", e)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, e = conn.ReadMessage(); e == nil {
		t.Fatal("revoked socket still receives output")
	}
	conn2, response, e := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", header)
	if conn2 != nil {
		_ = conn2.Close()
	}
	if e == nil || response == nil || response.StatusCode != 401 {
		t.Fatal("revoked JWT admitted", e)
	}
	for _, dual := range []bool{false, true} {
		var a gbnet.AuthResult
		if dual {
			a, e = f.state.AuthenticateDual(context.Background(), "operator species", "fixture-r", "fixture-g", 0)
		} else {
			a, e = f.state.Authenticate(context.Background(), "operator species", "fixture-r", 0)
		}
		if e != nil || a.OK {
			t.Fatal("inactive auth", e)
		}
	}
	onEnrollmentActor(t, f, func() { f.state.races[0].Governor[0].Active = true; f.state.races[0].God = false })
	if ok, e := f.state.ValidateSession(context.Background(), identity); e != nil || ok {
		t.Fatal("privilege revision ignored", e)
	}
}
func TestLegacyLoginAndStrictEnrollmentHistory(t *testing.T) {
	s, store, _ := newTestEngine(t, true)
	a, e := s.Authenticate(context.Background(), "Testers", "pw-testers", 0)
	if e != nil || !a.OK {
		t.Fatal("safe legacy login", a, e)
	}
	// An unknown corrupt record may not turn a new placement into legacy mode.
	if e = store.PutMeta([]byte("enrollment/v1/placement/corrupt"), []byte(`null`)); e != nil {
		t.Fatal(e)
	}
	a, e = s.Authenticate(context.Background(), "Testers", "pw-testers", 0)
	if e == nil || a.OK {
		t.Fatal("corrupt history treated as legacy")
	}
}
func TestEnrollmentStatusFailureEvidence(t *testing.T) {
	for _, stage := range []string{"before-commit", "commit-error", "after-commit"} {
		t.Run(stage, func(t *testing.T) {
			f := newEnrollmentActor(t)
			req := actorEnrollmentRequest("operator")
			hit := false
			f.store.SetMetaFault(func(op string) error {
				if op == "enrollment."+stage {
					hit = true
					return errors.New("private fixture error")
				}
				return nil
			})
			_, e := f.state.EnrollTrusted(context.Background(), req)
			f.store.SetMetaFault(nil)
			if e == nil || !hit {
				t.Fatal("fault not reached")
			}
			view, e := f.state.EnrollmentLookup(context.Background(), req)
			if e != nil {
				t.Fatal(e)
			}
			switch stage {
			case "before-commit":
				if view.State != enrollmentapi.NoKnownPlacement || view.Outcome != enrollmentapi.Unknown {
					t.Fatal(view)
				}
			case "commit-error":
				if view.State != enrollmentapi.Reconciliation || view.Outcome != string(db.EnrollmentUncertain) || f.state.Mode() != ModeHalted {
					t.Fatal(view)
				}
			case "after-commit":
				if view.State != enrollmentapi.Confirmed || view.Outcome != string(db.EnrollmentCommitted) {
					t.Fatal(view)
				}
			}
		})
	}
	f := newEnrollmentActor(t)
	req := actorEnrollmentRequest("operator")
	enrollActor(t, f, req)
	f.store.SetMetaFault(func(op string) error {
		if strings.HasPrefix(op, "meta.get:enrollment/") {
			return errors.New("read failed")
		}
		return nil
	})
	view, e := f.state.EnrollmentLookup(context.Background(), req)
	f.store.SetMetaFault(nil)
	if e != nil || view.State != enrollmentapi.Unknown || view.Placement != nil {
		t.Fatal("failed read became success", view, e)
	}
}
