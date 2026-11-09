package admin

import (
	"context"
	"encoding/json"
	"github.com/phreddrick/GB/go/internal/engine"
	"github.com/phreddrick/GB/go/internal/enrollmentapi"
	"github.com/phreddrick/GB/go/internal/racegen"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEnrollmentClientRequiresCompleteBoundConfirmation(t *testing.T) {
	req := enrollmentapi.OperatorRequest{CredentialVersion: "version", RequestID: "request", GameID: "game", AccountID: "account", Spec: racegen.Default()}
	view := enrollmentapi.NewView(req.Query())
	view.IntentDigest = enrollmentapi.IntentDigest(req)
	view.State = enrollmentapi.Confirmed
	view.Outcome = "COMMITTED"
	view.Placement = &enrollmentapi.Placement{Player: 1, GovernmentShip: 1, CreatedAt: time.Now().UTC()}
	good, _ := json.Marshal(Response{OK: true, Op: "enroll", Status: &engine.Status{Mode: "SERVING"}, Result: &view})
	text := string(good)
	cases := []string{strings.Replace(text, `"ok":true`, `"validation":[{"code":"OVER_BUDGET","field":"cost","message":"over budget"}],"ok":true`, 1), "", `{}`, text[:len(text)-1], text + `{}`, strings.Replace(text, `"player_num":1`, `"player_num":0`, 1), strings.Replace(text, `"state":"CONFIRMED"`, `"state":"UNKNOWN"`, 1), strings.Replace(text, `"outcome":"COMMITTED"`, `"outcome":"UNCERTAIN"`, 1), strings.Replace(text, `"ok":true`, `"ok":true,"ok":true`, 1), strings.Replace(text, view.Binding, strings.Repeat("a", 64), 1), strings.Replace(text, view.IntentDigest, strings.Repeat("a", 64), 1), strings.Replace(text, `"op":"enroll"`, `"op":"update"`, 1), strings.Replace(text, `"government_ship":1`, `"government_ship":null`, 1)}
	for i, body := range append(cases, text) {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer ts.Close()
			c := Client{BaseURL: ts.URL, Token: token, HTTP: ts.Client()}
			response, e := c.Enrollment(context.Background(), req)
			if i < len(cases) {
				if e == nil || response != nil {
					t.Fatal("unconfirmed response accepted")
				}
			} else if e != nil || response == nil {
				t.Fatal("valid confirmation rejected", e)
			}
		})
	}
}
func TestEnrollmentDefaultDisabled(t *testing.T) {
	_, ts, _ := newTest(t, &fakeEngine{mode: engine.ModeServing})
	r, _ := http.NewRequest(http.MethodPost, ts.URL+"/admin/v1/enroll", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+token)
	res, e := ts.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 403 {
		t.Fatal(res.StatusCode)
	}
}
