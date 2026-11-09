package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/admin"
	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/enrollmentapi"
	"github.com/phreddrick/GB/go/internal/game"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/operatorenroll"
	"github.com/phreddrick/GB/go/internal/racegen"
	"github.com/phreddrick/GB/go/internal/universe"
)

// No mock persistence/actor: exercise actual transport timeout and fault stages.
func TestPrivateEnrollmentTimeoutAndUncertainty(t *testing.T) {
	for _, stage := range []string{"timeout", "commit-error", "after-commit", "before-commit"} {
		t.Run(stage, func(t *testing.T) {
			store, e := db.Open(filepath.Join(t.TempDir(), "world.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = store.Close() }()
			u, e := universe.Generate(universe.Params{Stars: 2, MinPlanets: 2, MaxPlanets: 2}, 507)
			if e != nil {
				t.Fatal(e)
			}
			for _, p := range u.Planets {
				p.Type = model.PlanetEarth
				p.Conditions[model.AtmosRTemp] = 0
				p.MaxX = 2
				p.MaxY = 1
				p.Sectors = []model.Sector{{Condition: model.SectorLand, Type: model.SectorLand}, {Condition: model.SectorLand, Type: model.SectorLand}}
			}
			record := &db.UniverseRecord{Seed: 507, Stars: 2, MinPlanets: 2, MaxPlanets: 2, Planets: 4, InitializedAt: time.Now().UTC(), Generator: universe.GeneratorVersion, Tool: "isolated HTTP fault test"}
			if e = store.PutUniverse(u.StarData, u.Stars, u.Planets, record); e != nil {
				t.Fatal(e)
			}
			account, e := operatorenroll.Provision("account", "race-password-123", "governor-password-456")
			if e != nil {
				t.Fatal(e)
			}
			account.GodBootstrap = true
			provider, e := operatorenroll.New(operatorenroll.Manifest{SchemaVersion: 1, OperatorID: "operator", GameID: operatorenroll.GameID(record), BootstrapEnabled: true, Accounts: []operatorenroll.Account{account}})
			if e != nil {
				t.Fatal(e)
			}
			if e = provider.CheckStore(store); e != nil {
				t.Fatal(e)
			}
			cfg := config.Default()
			cfg.DefaultUpdateMinutes = 60
			state, e := game.New(cfg, store, game.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), TrustedEnrollment: provider.Policy(store)})
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = state.Stop(context.Background()) }()
			server, e := admin.New(admin.Options{Token: "fixture-operator-token", Engine: state, Enrollment: provider, OpTimeout: 500 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if e != nil {
				t.Fatal(e)
			}
			ts := httptest.NewServer(server.Handler())
			defer ts.Close()
			client := admin.Client{BaseURL: ts.URL, Token: "fixture-operator-token", HTTP: ts.Client()}
			spec := racegen.Default()
			spec.Name = "Transport species"
			spec.Attributes.Fertilize = .25
			spec.SectorCompat[racegen.Land] = 100
			req := enrollmentapi.OperatorRequest{CredentialVersion: account.CredentialVersion, RequestID: "request", GameID: operatorenroll.GameID(record), AccountID: account.AccountID, Spec: spec, God: true}
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			var once sync.Once
			faultStage := stage
			if stage == "timeout" {
				faultStage = "before-commit"
			}
			store.SetMetaFault(func(op string) error {
				if op == "enrollment."+faultStage {
					if stage == "timeout" {
						once.Do(func() { close(started); <-release })
						return nil
					}
					return errors.New("secret test detail must not escape")
				}
				return nil
			})
			var response *admin.Response
			if stage == "timeout" {
				type answer struct {
					response *admin.Response
					err      error
				}
				replies := make(chan answer, 1)
				go func() { r, e := client.Enrollment(context.Background(), req); replies <- answer{r, e} }()
				select {
				case <-started:
				case <-time.After(10 * time.Second):
					unblock()
					t.Fatal("fault hook not reached by started request")
				}
				received := <-replies
				response, e = received.response, received.err
				unblock()
				if e == nil || response == nil {
					t.Fatal("timeout claimed success")
				}
				view := response.Result.(*enrollmentapi.View)
				if view.Outcome != enrollmentapi.Unknown {
					t.Fatal(view)
				}
				// The actor reply is buffered: original HTTP wait ended, but same-ID status
				// serializes behind the completing transaction and can confirm it.
				status, e := client.EnrollmentStatus(context.Background(), req.Query())
				if e != nil || status.Result.(*enrollmentapi.View).State != enrollmentapi.Confirmed {
					t.Fatal(status, e)
				}
				replay, e := client.Enrollment(context.Background(), req)
				if e != nil || !replay.Result.(*enrollmentapi.View).Replay {
					t.Fatal(replay, e)
				}
			} else {
				response, e = client.Enrollment(context.Background(), req)
				if e == nil || response == nil {
					t.Fatal("injected failure lost")
				}
				view := response.Result.(*enrollmentapi.View)
				switch stage {
				case "commit-error":
					if view.Outcome != "UNCERTAIN" || view.State != enrollmentapi.Reconciliation || state.Mode() != game.ModeHalted {
						t.Fatal(view)
					}
					status, e := client.EnrollmentStatus(context.Background(), req.Query())
					if e != nil || status.Result.(*enrollmentapi.View).State != enrollmentapi.Reconciliation {
						t.Fatal(status, e)
					}
				case "after-commit":
					if view.State != enrollmentapi.Confirmed || view.Outcome != "COMMITTED" || state.Status().Races != 1 {
						t.Fatal(view)
					}
				default:
					if view.Outcome != "NOT_COMMITTED" || state.Status().Races != 0 {
						t.Fatal(view)
					}
				}
			}
			store.SetMetaFault(nil)
			// Every response uses public placement projection, never credential bytes.
			raw, _ := json.Marshal(response)
			for _, secret := range []string{account.RaceHash, account.GovernorHash, "secret test detail must not escape"} {
				if bytes.Contains(raw, []byte(secret)) {
					t.Fatal("secret-bearing transport response")
				}
			}
		})
	}
}
