package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/enrollmentapi"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/operatorenroll"
	"github.com/phreddrick/GB/go/internal/racegen"
	"github.com/phreddrick/GB/go/internal/universe"
)

// Real server + real gbctl + real player transport, all on an isolated world.
func TestCR07EOperatorEnrollmentAndDualLogin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world.db")
	store, e := db.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	u, e := universe.Generate(universe.Params{Stars: 4, MinPlanets: 2, MaxPlanets: 2}, 704)
	if e != nil {
		t.Fatal(e)
	}
	for _, st := range u.Stars {
		st.Inhabited = 0
		st.Explored = 0
	}
	for _, p := range u.Planets {
		p.Type = model.PlanetEarth
		p.Conditions[model.AtmosRTemp] = 0
		p.MaxX = 3
		p.MaxY = 2
		p.Sectors = make([]model.Sector, 6)
		for i := range p.Sectors {
			p.Sectors[i] = model.Sector{Type: model.SectorLand, Condition: model.SectorLand, Fert: 47, Resource: 37}
		}
	}
	record := &db.UniverseRecord{Seed: 704, Stars: 4, MinPlanets: 2, MaxPlanets: 2, Planets: 8, InitializedAt: time.Now().UTC(), Generator: universe.GeneratorVersion, Tool: "isolated CR-07E transport fixture"}
	if e = store.PutUniverse(u.StarData, u.Stars, u.Planets, record); e != nil {
		t.Fatal(e)
	}
	if e = store.Close(); e != nil {
		t.Fatal(e)
	}
	a, e := operatorenroll.Provision("operator-account", "race-password-123", "governor-password-456")
	if e != nil {
		t.Fatal(e)
	}
	a.GodBootstrap = true
	b := a
	b.AccountID = "player-account"
	b.GodBootstrap = false
	b.CredentialVersion = operatorenroll.Version(b)
	manifest := operatorenroll.Manifest{SchemaVersion: 1, OperatorID: "fixture-operator", GameID: operatorenroll.GameID(record), BootstrapEnabled: true, Accounts: []operatorenroll.Account{a, b}}
	authority := filepath.Join(dir, "authority.json")
	writeJSONFile := func(path string, value any) {
		t.Helper()
		data, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	writeJSONFile(authority, manifest)
	env := map[string]string{"GB_ENROLL_AUTHORITY_FILE": authority}
	s := startServer(t, path, env)
	if code, out := s.gbctl("enroll-game-id"); code != 0 || !strings.Contains(out, manifest.GameID) {
		t.Fatal("game binding lookup", code, out)
	}
	spec := racegen.Default()
	spec.Name = "Operator species"
	spec.Attributes.Fertilize = .25
	spec.SectorCompat[racegen.Land] = 100
	req := enrollmentapi.OperatorRequest{RequestID: "integration", AccountID: a.AccountID, GameID: manifest.GameID, CredentialVersion: a.CredentialVersion, Spec: spec}
	requestFile := filepath.Join(dir, "request.json")
	writeJSONFile(requestFile, req)
	// An ordinary first race must not implicitly receive God authority.
	if code, out := s.gbctl("enroll", "-f", requestFile); code != 4 || !strings.Contains(out, "GOD_BOOTSTRAP_REQUIRED") {
		t.Fatal(code, out)
	}
	// Neither the client nor the JSON claim can grant another account God/Guest.
	unauthorized := req
	unauthorized.AccountID = b.AccountID
	unauthorized.CredentialVersion = b.CredentialVersion
	unauthorized.God = true
	badFile := filepath.Join(dir, "bad.json")
	writeJSONFile(badFile, unauthorized)
	if code, out := s.gbctl("enroll", "-f", badFile); code != 4 {
		t.Fatal(code, out)
	}
	unauthorized.God = false
	unauthorized.Guest = true
	writeJSONFile(badFile, unauthorized)
	if code, out := s.gbctl("enroll", "-f", badFile); code != 4 {
		t.Fatal(code, out)
	}
	// Actual CLI bootstrap sets an explicit flag, with server authorization.
	code, out := s.gbctl("enroll", "-f", requestFile, "--god")
	if code != 0 {
		t.Fatal(code, out)
	}
	var reply struct {
		Result enrollmentapi.View `json:"result"`
	}
	if json.Unmarshal([]byte(out), &reply) != nil || reply.Result.State != enrollmentapi.Confirmed || reply.Result.Placement.Player != 1 {
		t.Fatal(out)
	}
	first := *reply.Result.Placement
	if s.status().Status.Races != 1 {
		t.Fatal("actor world not published")
	}
	req.God = true
	writeJSONFile(requestFile, req)
	if code, out = s.gbctl("enroll", "-f", requestFile); code != 0 || !strings.Contains(out, `"replay":true`) {
		t.Fatal(code, out)
	}
	if code := s.signal(syscall.SIGTERM, 10*time.Second); code != 0 {
		t.Fatal(code)
	}
	baseline := enrollmentAudit(t, path)
	s = startServer(t, path, env)
	// Public listener has no private/admin or future signup routes.
	for _, route := range []string{"/admin/v1/enroll", "/api/enroll", "/api/enroll/options", "/api/enroll/cost", "/enroll"} {
		res, e := http.Post("http://"+s.addr+route, "application/json", strings.NewReader(`{}`))
		if e != nil {
			t.Fatal(e)
		}
		_ = res.Body.Close()
		if res.StatusCode == 200 {
			t.Fatalf("public route %s enabled", route)
		}
	}
	// Strict private request parsing and authentication, with no placement change.
	raw, _ := json.Marshal(req)
	invalid := []string{`{}`, `null`, string(raw) + `{}`, strings.Replace(string(raw), `"god":true`, `"god":true,"god":false`, 1), strings.Replace(string(raw), `"homeworld":0`, `"homeworld":null`, 1), strings.Replace(string(raw), `"homeworld":0`, `"homeworld":0.5`, 1), strings.Replace(string(raw), `"sector_compat":[0,100,0,0,0,0,0,100]`, `"sector_compat":[0]`, 1), strings.Repeat("x", enrollmentapi.MaxBody+1), strings.Replace(string(raw), `"god":true`, `"extra":0,"god":true`, 1)}
	for i, body := range invalid {
		if body == string(raw) {
			t.Fatalf("invalid fixture %d unchanged", i)
		}
		status, _ := operatorHTTP(t, s, "POST", "/admin/v1/enroll", adminToken, body)
		if status < 400 {
			t.Fatalf("bad body %d accepted %d", i, status)
		}
	}
	for _, token := range []string{"", "invalid"} {
		status, _ := operatorHTTP(t, s, "POST", "/admin/v1/enroll", token, string(raw))
		if status != 401 {
			t.Fatal(status)
		}
	}
	status, _ := operatorHTTP(t, s, "GET", "/admin/v1/enroll", adminToken, "")
	if status != 405 {
		t.Fatal(status)
	}
	for _, mutate := range []func(*enrollmentapi.OperatorRequest){func(r *enrollmentapi.OperatorRequest) { r.GameID = "other" }, func(r *enrollmentapi.OperatorRequest) { r.AccountID = "arbitrary" }, func(r *enrollmentapi.OperatorRequest) { r.Spec.Attributes.IQ = 999 }, func(r *enrollmentapi.OperatorRequest) { r.Spec.Name = "changed" }, func(r *enrollmentapi.OperatorRequest) { r.CredentialVersion = "unknown" }} {
		bad := req
		mutate(&bad)
		writeJSONFile(badFile, bad)
		if code, out = s.gbctl("enroll", "-f", badFile); code != 4 {
			t.Fatal(code, out)
		}
	}
	if s.status().Status.Races != 1 {
		t.Fatal("negative requests changed actor world")
	}
	if code := s.signal(syscall.SIGTERM, 10*time.Second); code != 0 {
		t.Fatal(code)
	}
	if got := enrollmentAudit(t, path); got != baseline {
		t.Fatal("rejected transport requests changed complete world, placement or allocator")
	}
	s = startServer(t, path, env)
	// Correct dual credentials, generic rejection for either wrong credential,
	// legacy one-password bypass, inactive governor and malformed login contracts.
	login := fmt.Sprintf(`{"race":%q,"race_password":"race-password-123","governor":0,"governor_password":"governor-password-456"}`, spec.Name)
	status, body := playerLogin(t, s, login)
	if status != 200 {
		t.Fatal(status, body)
	}
	var tokenReply struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(body, &tokenReply) != nil || tokenReply.Token == "" {
		t.Fatal("no JWT")
	}
	for _, bad := range []string{strings.Replace(login, "race-password-123", "wrong-race", 1), strings.Replace(login, "governor-password-456", "wrong-governor", 1), fmt.Sprintf(`{"race":%q,"password":"race-password-123","governor":0}`, spec.Name), strings.Replace(login, `"governor":0`, `"governor":1`, 1), strings.Replace(login, `"governor":0`, `"governor":-1`, 1), strings.Replace(login, `"governor":0`, `"governor":999`, 1), strings.Replace(login, `"governor":0`, `"governor":null`, 1), strings.Replace(login, `"governor":0`, `"password":"x","governor":0`, 1), strings.Replace(login, `"governor":0`, `"governor":0,"governor":0`, 1), strings.Replace(login, `"governor":0`, `"extra":true,"governor":0`, 1)} {
		status, _ = playerLogin(t, s, bad)
		if status == 200 {
			t.Fatal("bad login succeeded")
		}
	}
	conn := s.connect(tokenReply.Token)
	readUntil(t, conn, "Welcome back", 5*time.Second)
	if e = conn.WriteMessage(websocket.TextMessage, []byte("cs")); e != nil {
		t.Fatal(e)
	}
	for _, step := range []struct{ command, expected string }{{"survey", "Population:"}, {"map", "Star /"}, {"capital", "Efficiency of governmental center: 100%"}} {
		if e = conn.WriteMessage(websocket.TextMessage, []byte(step.command)); e != nil {
			t.Fatal(e)
		}
		readUntil(t, conn, step.expected, 5*time.Second)
	}
	_ = conn.Close()
	if code, out = s.gbctl("update"); code != 0 {
		t.Fatal(code, out)
	}
	if code, out = s.gbctl("enroll", "-f", requestFile); code != 0 || !strings.Contains(out, `"replay":true`) {
		t.Fatal(code, out)
	}
	if code := s.signal(syscall.SIGTERM, 10*time.Second); code != 0 {
		t.Fatal(code, s.logs.String())
	}
	// Offline reopen verifies storage, credential independence and additive history.
	store, e = db.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	race, e := store.GetRace(1)
	if e != nil || race.Password != a.RaceHash || race.Governor[0].Password != a.GovernorHash || race.GovShip != first.GovernmentShip {
		t.Fatal("stored credentials/ship", e)
	}
	if dual, e := store.EnrollmentRequiresDual(1); e != nil || !dual {
		t.Fatal("dual requirement missing", e)
	}

	if e = store.Close(); e != nil {
		t.Fatal(e)
	}
	// Disabling bootstrap must preserve authorized historical retry recognition.
	manifest.BootstrapEnabled = false
	rotated := a
	rotated.RaceHash, rotated.GovernorHash = a.GovernorHash, a.RaceHash
	rotated.CredentialVersion = operatorenroll.Version(rotated)
	manifest.Accounts = append(manifest.Accounts, rotated)
	writeJSONFile(authority, manifest)
	s = startServer(t, path, env)
	if code, out = s.gbctl("enroll", "-f", requestFile); code != 0 || !strings.Contains(out, `"replay":true`) {
		t.Fatal(code, out)
	}
	if code, out = s.gbctl("enroll-status", "-request-id", req.RequestID, "-game", req.GameID, "-account", req.AccountID); code != 0 || !strings.Contains(out, `"state":"CONFIRMED"`) {
		t.Fatal(code, out)
	}
	// A second account cannot escalate God, but can enroll one ordinary race.
	req2 := req
	req2.RequestID = "second"
	req2.AccountID = b.AccountID
	req2.CredentialVersion = b.CredentialVersion
	req2.God = false
	req2.Spec.Name = "Second species"
	writeJSONFile(badFile, req2)
	if code, out = s.gbctl("enroll", "-f", badFile); code != 0 {
		t.Fatal(code, out)
	}
	req2.RequestID = "third"
	req2.Spec.Name = "renamed second"
	writeJSONFile(badFile, req2)
	if code, out = s.gbctl("enroll", "-f", badFile); code != 4 {
		t.Fatal(code, out)
	}
	if s.status().Status.Races != 2 {
		t.Fatal("wrong player count")
	}
	if code, out = s.gbctl("quiesce"); code != 0 {
		t.Fatal(code, out)
	}
	if code, out = s.gbctl("enroll", "-f", requestFile); code != 4 {
		t.Fatal(code, out)
	}
	if code, out = s.gbctl("enroll-status", "-request-id", "absent", "-game", req.GameID, "-account", req.AccountID); code != 0 || !strings.Contains(out, `"state":"NO_KNOWN_PLACEMENT"`) || !strings.Contains(out, `"outcome":"UNKNOWN"`) {
		t.Fatal(code, out)
	}
	if code := s.signal(syscall.SIGTERM, 10*time.Second); code != 0 {
		t.Fatal(code)
	}
	beforeMismatch := enrollmentAudit(t, path)
	manifest.GameID = "universe-" + strings.Repeat("0", 64)
	writeJSONFile(authority, manifest)
	wrongEnv := baseEnv(t, path)
	wrongEnv["GB_ENROLL_AUTHORITY_FILE"] = authority
	if code, out := runToExit(t, wrongEnv, 10*time.Second); code != 1 || !strings.Contains(out, "authority does not match") {
		t.Fatal("mismatched authority accepted", code, out)
	}
	if enrollmentAudit(t, path) != beforeMismatch {
		t.Fatal("mismatched authority modified game")
	}
	// No credential inputs or stored hashes appeared in server/client diagnostics.
	for _, secret := range []string{"race-password-123", "governor-password-456", a.RaceHash, a.GovernorHash, adminToken} {
		if strings.Contains(s.logs.String(), secret) {
			t.Fatal("secret leaked")
		}
	}
}
func operatorHTTP(t *testing.T, s *server, method, path, token, body string) (int, []byte) {
	t.Helper()
	req, e := http.NewRequest(method, s.admin+path, bytes.NewBufferString(body))
	if e != nil {
		t.Fatal(e)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = res.Body.Close() }()
	b, e := io.ReadAll(res.Body)
	if e != nil {
		t.Fatal(e)
	}
	return res.StatusCode, b
}
func playerLogin(t *testing.T, s *server, body string) (int, []byte) {
	t.Helper()
	res, e := http.Post("http://"+s.addr+"/auth/login", "application/json", strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = res.Body.Close() }()
	b, e := io.ReadAll(res.Body)
	if e != nil {
		t.Fatal(e)
	}
	return res.StatusCode, b
}

func enrollmentAudit(t *testing.T, path string) string {
	t.Helper()
	store, e := db.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = store.Close() }()
	races, e := store.AllRaces()
	if e != nil {
		t.Fatal(e)
	}
	ticket, e := store.EnrollmentShipCandidate()
	if e != nil {
		t.Fatal(e)
	}
	placement, e := store.GetPlacement("operator-account", "integration")
	if e != nil {
		t.Fatal(e)
	}
	stars, e := store.AllStars()
	if e != nil {
		t.Fatal(e)
	}
	planets, e := store.AllPlanets()
	if e != nil {
		t.Fatal(e)
	}
	ships, e := store.AllShips()
	if e != nil {
		t.Fatal(e)
	}
	stardata, e := store.GetStarData()
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal([]any{races, stars, planets, ships, stardata, ticket, placement})
	if e != nil {
		t.Fatal(e)
	}
	return string(raw)
}
