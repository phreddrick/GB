package e2e

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/engine"
	"github.com/phreddrick/GB/go/internal/enrollmentapi"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/operatorenroll"
	"github.com/phreddrick/GB/go/internal/racegen"
	"github.com/phreddrick/GB/go/internal/wirejson"
	"github.com/phreddrick/GB/go/test/acceptance"
)

// Only disposable paths/loopback subprocesses. Unlike the existing E fixture,
// this universe and every credential artifact are created through real gbctl.
type cr07fFixture struct {
	dir, path, authority, game string
	manifest                   operatorenroll.Manifest
	requests                   []enrollmentapi.OperatorRequest
	accounts                   []operatorenroll.Account
	passwords                  [][2]string
	processes                  []*server
	output                     []string
	secrets                    []string
	tokens                     []string
	base                       cr07fWorld
}
type cr07fWorld struct {
	races       []*model.Race
	stars       []*model.Star
	planets     map[uint8]map[uint8]*model.Planet
	ships       map[uint32]*model.Ship
	sdata       *model.StarData
	ticket      db.EnrollmentShip
	placements  []*db.PlacementRecord
	commit      *db.CommitRecord
	schedule    *db.ScheduleRecord
	maintenance *db.MaintenanceRecord
}

func cr07fRead(t *testing.T, path string, requests []enrollmentapi.OperatorRequest) cr07fWorld {
	t.Helper()
	store, e := db.Open(path)
	if e != nil {
		t.Fatal("open closed disposable store", e)
	}
	defer func() { _ = store.Close() }()
	var w cr07fWorld
	w.races, e = store.AllRaces()
	if e != nil {
		t.Fatal(e)
	}
	w.stars, e = store.AllStars()
	if e != nil {
		t.Fatal(e)
	}
	w.planets, e = store.AllPlanets()
	if e != nil {
		t.Fatal(e)
	}
	w.ships, e = store.AllShips()
	if e != nil {
		t.Fatal(e)
	}
	w.sdata, e = store.GetStarData()
	if e != nil {
		t.Fatal(e)
	}
	w.ticket, e = store.EnrollmentShipCandidate()
	if e != nil {
		t.Fatal(e)
	}
	w.commit, e = store.GetCommitRecord()
	if e != nil || w.commit == nil {
		t.Fatal("commit record unavailable", e)
	}
	w.schedule, e = store.GetSchedule()
	if e != nil || w.schedule == nil {
		t.Fatal("schedule unavailable", e)
	}
	w.maintenance, e = store.GetMaintenance()
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range requests {
		p, e := store.GetPlacement(r.AccountID, r.RequestID)
		if e != nil {
			t.Fatal("placement lookup failed", e)
		}
		w.placements = append(w.placements, p)
	}
	return w
}
func (w cr07fWorld) audit(t *testing.T) string {
	t.Helper()
	b, e := json.Marshal([]any{w.races, w.stars, w.planets, w.ships, w.sdata, w.ticket, w.placements})
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func cr07fJSON(t *testing.T, path string, value any) {
	t.Helper()
	b, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func (f *cr07fFixture) stop(t *testing.T, s *server) {
	t.Helper()
	if s.signal(syscall.SIGTERM, 15*time.Second) != 0 {
		t.Fatal("disposable server did not stop gracefully")
	}
}
func (f *cr07fFixture) start(t *testing.T, bootstrap bool, extra map[string]string) *server {
	t.Helper()
	f.manifest.BootstrapEnabled = bootstrap
	cr07fJSON(t, f.authority, f.manifest)
	env := map[string]string{"GB_ENROLL_AUTHORITY_FILE": f.authority, "GB_ENROLL_PREVIEW": "true"}
	for k, v := range extra {
		env[k] = v
	}
	s := startServer(t, f.path, env)
	f.processes = append(f.processes, s)
	return s
}
func (f *cr07fFixture) status(t *testing.T, s *server) engine.Status {
	t.Helper()
	code, out := s.gbctl("status")
	f.output = append(f.output, out)
	if code != 0 {
		t.Fatal("gbctl status failed", code)
	}
	var reply struct {
		Status engine.Status `json:"status"`
	}
	if json.Unmarshal([]byte(out), &reply) != nil {
		t.Fatal("invalid status JSON")
	}
	return reply.Status
}
func (f *cr07fFixture) command(t *testing.T, s *server, args ...string) string {
	t.Helper()
	code, out := s.gbctl(args...)
	f.output = append(f.output, out)
	if code != 0 {
		t.Fatal("operator command failed", args[0], code)
	}
	return out
}
func (f *cr07fFixture) enroll(t *testing.T, s *server, req enrollmentapi.OperatorRequest, wantCode int, wantReason string) enrollmentapi.View {
	t.Helper()
	path := filepath.Join(f.dir, "request.json")
	cr07fJSON(t, path, req)
	code, out := s.gbctl("-timeout", "15s", "enroll", "-f", path)
	f.output = append(f.output, out)
	if code != wantCode || wantReason != "" && !strings.Contains(out, wantReason) {
		t.Fatal("enrollment result mismatch", code, wantReason)
	}
	if code != 0 {
		return enrollmentapi.View{}
	}
	var reply struct {
		Result enrollmentapi.View `json:"result"`
	}
	if json.Unmarshal([]byte(out), &reply) != nil || reply.Result.Validate(req.Query()) != nil || reply.Result.State != enrollmentapi.Confirmed || reply.Result.Outcome != "COMMITTED" {
		t.Fatal("CLI did not confirm complete durable placement")
	}
	return reply.Result
}
func cr07fRandomPassword(t *testing.T) string {
	t.Helper()
	var b [24]byte
	if _, e := rand.Read(b[:]); e != nil {
		t.Fatal(e)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func newCR07FFixture(t *testing.T) *cr07fFixture {
	t.Helper()
	dir := t.TempDir()
	f := &cr07fFixture{dir: dir, path: filepath.Join(dir, "world.db"), authority: filepath.Join(dir, "authority.json")}
	s := startServer(t, f.path, nil)
	f.processes = append(f.processes, s)
	if f.status(t, s).Mode != "UNINITIALIZED" {
		t.Fatal("fresh store did not start uninitialized")
	}
	f.command(t, s, "universe", "init", "-seed", "11", "-stars", "32", "-min-planets", "2", "-max-planets", "3")
	if f.status(t, s).Mode != "SERVING" {
		t.Fatal("operator init did not publish serving world")
	}
	var identity struct {
		Game string `json:"game_id"`
	}
	if json.Unmarshal([]byte(f.command(t, s, "enroll-game-id")), &identity) != nil || identity.Game == "" {
		t.Fatal("game binding unavailable")
	}
	f.game = identity.Game
	f.stop(t, s)
	// Pin a generated fixture whose every eligible Earth candidate has land,
	// so isolated production randomness cannot select a terrain-less test home.
	f.base = cr07fRead(t, f.path, nil)
	eligible := 0
	for _, st := range f.base.stars {
		if st == nil {
			continue
		}
		starEligible := false
		for _, p := range f.base.planets[st.Number] {
			if p.Type != model.PlanetEarth || p.Conditions[0] < -200 || p.Conditions[0] > 100 {
				continue
			}
			land := false
			for _, sec := range p.Sectors {
				land = land || sec.Condition == model.SectorLand
			}
			if !land {
				t.Fatal("pinned generated home lacks capital terrain")
			}
			starEligible = true
		}
		if starEligible {
			eligible++
		}
	}
	if eligible < 2 {
		t.Fatal("pinned generated universe lacks two independent home stars", eligible)
	}
	f.manifest = operatorenroll.Manifest{SchemaVersion: 1, OperatorID: "acceptance-operator", GameID: f.game}
	for i, id := range []string{"first-account", "second-account", "spare-account", "first-account"} {
		pair := [2]string{cr07fRandomPassword(t), cr07fRandomPassword(t)}
		raceFile, govFile, outFile := filepath.Join(dir, fmt.Sprintf("race-%d.secret", i)), filepath.Join(dir, fmt.Sprintf("gov-%d.secret", i)), filepath.Join(dir, fmt.Sprintf("account-%d.json", i))
		if e := os.WriteFile(raceFile, []byte(pair[0]), 0600); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(govFile, []byte(pair[1]), 0600); e != nil {
			t.Fatal(e)
		}
		args := []string{"credentials", "prepare", "-account", id, "-race-file", raceFile, "-governor-file", govFile, "-out", outFile}
		if i == 0 || i == 3 {
			args = append(args, "--god-bootstrap")
		}
		cmd := exec.Command(s.gbctlBin, args...) //nolint:gosec // binary built by this suite; protected paths under t.TempDir
		out, e := cmd.CombinedOutput()
		f.output = append(f.output, string(out))
		if e != nil {
			t.Fatal("protected-file credential CLI failed")
		}
		raw, e := operatorenroll.ReadProtected(outFile)
		if e != nil {
			t.Fatal("credential artifact unavailable")
		}
		var account operatorenroll.Account
		if json.Unmarshal(raw, &account) != nil {
			t.Fatal("invalid account artifact")
		}
		f.manifest.Accounts = append(f.manifest.Accounts, account)
		f.secrets = append(f.secrets, pair[0], pair[1], account.RaceHash, account.GovernorHash, string(raw))
		if i < 3 {
			f.accounts = append(f.accounts, account)
			f.passwords = append(f.passwords, pair)
		}
	}
	for i, account := range f.accounts {
		spec := racegen.Default()
		spec.Name = fmt.Sprintf("Acceptance species %d", i+1)
		spec.Info = "CR-07F disposable species"
		spec.Attributes.Fertilize = .25
		spec.SectorCompat[racegen.Land] = 100
		if i == 1 {
			spec.Attributes.Fighters = 7
			spec.Attributes.IQ = 120
			spec.Attributes.Sexes = 3
			spec.Attributes.Mass = 1.25
			spec.Attributes.Fertilize = .3
		}
		f.requests = append(f.requests, enrollmentapi.OperatorRequest{CredentialVersion: account.CredentialVersion, RequestID: fmt.Sprintf("cr07f-request-%d", i+1), GameID: f.game, AccountID: account.AccountID, Spec: spec, God: i == 0})
	}
	f.base.placements = make([]*db.PlacementRecord, len(f.requests))
	t.Cleanup(func() { f.noSecrets(t) })
	return f
}
func (f *cr07fFixture) noSecrets(t *testing.T) {
	t.Helper()
	outputs := strings.Join(f.output, "\n")
	for _, s := range f.processes {
		outputs += "\n" + s.logs.String()
	}
	for _, secret := range append(append(append([]string{}, f.secrets...), f.tokens...), adminToken) {
		if strings.Contains(outputs, secret) {
			t.Error("credential/token/artifact leaked in captured logs or responses")
			return
		}
	}
}
func (f *cr07fFixture) initial(t *testing.T, w cr07fWorld, views []enrollmentapi.View) {
	t.Helper()
	if !reflect.DeepEqual(f.base.sdata, w.sdata) {
		t.Fatal("enrollment changed global star data/AP")
	}
	for i, view := range views {
		p := view.Placement
		if p == nil {
			t.Fatal("missing placement")
		}
		r := w.races[p.Player-1]
		planet := w.planets[p.Star][p.Planet]
		star := w.stars[p.Star]
		ship := w.ships[p.GovernmentShip]
		d := acceptance.Design{Spec: f.requests[i].Spec, Player: p.Player, Ship: p.GovernmentShip, X: p.CapitalX, Y: p.CapitalY, God: f.requests[i].God, RaceHash: f.accounts[i].RaceHash, GovernorHash: f.accounts[i].GovernorHash}
		differences := acceptance.InitialDifferences(d, f.base.stars[p.Star], f.base.planets[p.Star][p.Planet], r, star, planet, ship)
		if len(differences) > 0 {
			t.Fatal("C initial persisted field mismatch:", differences)
		}
		rec := w.placements[i]
		if rec == nil || rec.Player != p.Player || rec.GovernmentShip != p.GovernmentShip || rec.Star != p.Star || rec.Planet != p.Planet || rec.CapitalX != p.CapitalX || rec.CapitalY != p.CapitalY {
			t.Fatal("placement metadata differs from complete world")
		}
	}
	if len(w.ships) != len(views) {
		t.Fatal("unexpected initial ship count")
	}
}
func (f *cr07fFixture) playable(t *testing.T, w cr07fWorld, views []enrollmentapi.View) {
	t.Helper()
	for _, view := range views {
		p := view.Placement
		r := w.races[p.Player-1]
		pl := w.planets[p.Star][p.Planet]
		sh := w.ships[p.GovernmentShip]
		if r == nil || sh == nil || r.GovShip != p.GovernmentShip || !r.Governor[0].Active || !sh.Alive || !sh.Active || !sh.Docked || sh.Type != model.OtypeGov || sh.WhatOrbits != model.LevelPlan || sh.WhatDest != model.LevelPlan || int(sh.Owner) != p.Player || sh.Governor != 0 || sh.StOrbits != p.Star || sh.PNumOrbits != p.Planet {
			t.Fatal("post-turn government invariant")
		}
		sec := pl.SectorAt(int(p.CapitalX), int(p.CapitalY))
		if sec == nil || int(sec.Owner) != p.Player || sec.Popn == 0 || pl.Info[p.Player-1].NumSectsOwned == 0 || pl.Popn == 0 {
			t.Fatal("post-turn colony invariant")
		}
		seen := map[uint32]bool{}
		found := false
		for n := pl.Ships; n != 0; {
			current := w.ships[n]
			if seen[n] || current == nil {
				t.Fatal("invalid post-turn orbit chain")
			}
			seen[n] = true
			found = found || n == sh.Number
			n = current.NextShip
		}
		if !found {
			t.Fatal("government ship not in planet chain")
		}
	}
}
func (f *cr07fFixture) play(t *testing.T, s *server, i int, view enrollmentapi.View) {
	t.Helper()
	req := f.requests[i]
	pair := f.passwords[i]
	body, e := json.Marshal(map[string]any{"race": req.Spec.Name, "governor": 0, "race_password": pair[0], "governor_password": pair[1]})
	if e != nil {
		t.Fatal(e)
	}
	status, reply := playerLogin(t, s, string(body))
	// The authenticated response may contain the newly issued JWT only. Check
	// known secrets before adding that legitimate token to later leak scans.
	for _, secret := range append(f.secrets, adminToken) {
		if bytes.Contains(reply, []byte(secret)) {
			t.Fatal("credential/token/artifact leaked in login response")
		}
	}
	if status != 200 {
		t.Fatal("dual HTTP login rejected", status)
	}
	var answer struct {
		Token string `json:"token"`
	}
	if wirejson.Decode(reply, &answer) != nil || answer.Token == "" {
		t.Fatal("login did not return JWT")
	}
	parsed, e := jwt.Parse(answer.Token, func(*jwt.Token) (any, error) { return []byte(jwtSecret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if e != nil || !parsed.Valid {
		t.Fatal("invalid issued JWT")
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || claims["player_num"] != float64(view.Placement.Player) || claims["governor"] != float64(0) || claims["god"] != req.God {
		t.Fatal("JWT identity/privilege mismatch")
	}
	f.tokens = append(f.tokens, answer.Token)
	conn := s.connect(answer.Token)
	defer func() { _ = conn.Close() }()
	f.output = append(f.output, readUntil(t, conn, "Welcome back, "+req.Spec.Name, 5*time.Second))
	if e = conn.WriteMessage(websocket.TextMessage, []byte("cs")); e != nil {
		t.Fatal(e)
	}
	p := view.Placement
	star := f.base.stars[p.Star]
	planetName := star.PlanetNames[p.Planet]
	for _, step := range []struct{ command, expected string }{{"survey", "=== /" + star.Name + "/" + planetName}, {"map", "Star /" + star.Name}, {"capital", "Efficiency of governmental center: 100%"}} {
		if e = conn.WriteMessage(websocket.TextMessage, []byte(step.command)); e != nil {
			t.Fatal(e)
		}
		f.output = append(f.output, readUntil(t, conn, step.expected, 5*time.Second))
	}
}

func TestCR07FGeneratedUniverseJourney(t *testing.T) {
	f := newCR07FFixture(t)
	s := f.start(t, false, nil)
	ordinary := f.requests[0]
	ordinary.God = false
	f.enroll(t, s, ordinary, 4, "GOD_BOOTSTRAP_REQUIRED")
	f.enroll(t, s, f.requests[0], 4, "UNAUTHORIZED_ENROLLMENT") // authorized account, disabled bootstrap
	bad := f.requests[1]
	bad.God = true
	f.enroll(t, s, bad, 4, "UNAUTHORIZED_ENROLLMENT")
	bad.God = false
	bad.Guest = true
	f.enroll(t, s, bad, 4, "UNAUTHORIZED_ENROLLMENT")
	f.stop(t, s)
	if f.base.audit(t) != cr07fRead(t, f.path, f.requests).audit(t) {
		t.Fatal("permission denials changed world/allocation")
	}
	s = f.start(t, true, nil)
	before := f.status(t, s)
	views := []enrollmentapi.View{f.enroll(t, s, f.requests[0], 0, ""), f.enroll(t, s, f.requests[1], 0, "")}
	a, b := views[0].Placement, views[1].Placement
	if a.Player != 1 || b.Player != 2 || a.Star == b.Star || a.GovernmentShip == b.GovernmentShip || f.status(t, s).Races != 2 || f.status(t, s).Ships != 2 {
		t.Fatal("two distinct empires not immediately published")
	}
	if f.status(t, s).LastCommitSeq != before.LastCommitSeq {
		t.Fatal("enrollment changed CR18 commit sequence")
	}
	for i, req := range f.requests[:2] {
		replay := f.enroll(t, s, req, 0, "")
		if !replay.Replay || !reflect.DeepEqual(replay.Placement, views[i].Placement) {
			t.Fatal("immediate replay identity changed")
		}
	}
	f.stop(t, s)
	initial := cr07fRead(t, f.path, f.requests)
	f.initial(t, initial, views)
	if initial.placements[2] != nil {
		t.Fatal("unused account acquired placement")
	}
	s = f.start(t, true, nil)
	// Transport refusals, not a fake service. Compare all world entities, every
	// fixture placement and the allocator ticket after a legitimate stop.
	bad = f.requests[0]
	bad.Spec.Name = "Modified intent"
	f.enroll(t, s, bad, 4, "REQUEST_CONFLICT")
	bad = f.requests[0]
	bad.AccountID = f.requests[1].AccountID
	bad.CredentialVersion = f.requests[1].CredentialVersion
	bad.God = false
	f.enroll(t, s, bad, 4, "REQUEST_CONFLICT")
	bad = f.requests[1]
	bad.RequestID = "second-again"
	bad.Spec.Name = "Another species"
	f.enroll(t, s, bad, 4, "ACCOUNT_ALREADY_PLACED")
	bad = f.requests[2]
	bad.Spec.Name = strings.ToUpper(f.requests[1].Spec.Name)
	f.enroll(t, s, bad, 4, "NAME_TAKEN")
	bad = f.requests[0]
	bad.CredentialVersion = f.manifest.Accounts[3].CredentialVersion
	f.enroll(t, s, bad, 4, "REQUEST_CONFLICT")
	bad = f.requests[1]
	bad.CredentialVersion = f.requests[0].CredentialVersion
	f.enroll(t, s, bad, 4, "ENROLLMENT_NOT_AUTHORIZED")
	bad = f.requests[1]
	bad.CredentialVersion = "invalid-version"
	f.enroll(t, s, bad, 4, "ENROLLMENT_NOT_AUTHORIZED")
	bad = f.requests[1]
	bad.GameID = "another-universe"
	f.enroll(t, s, bad, 4, "ENROLLMENT_NOT_AUTHORIZED")
	for _, token := range []string{"", "wrong-token"} {
		status, body := operatorHTTP(t, s, http.MethodPost, "/admin/v1/enroll", token, `{}`)
		f.output = append(f.output, string(body))
		if status != 401 {
			t.Fatal("private admin auth bypass")
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, route := range []string{"/api/enroll", "/enroll", "/admin/v1/enroll", "/api/enroll/options", "/api/enroll/cost"} {
			request, e := http.NewRequest(method, "http://"+s.addr+route, strings.NewReader(`{}`))
			if e != nil {
				t.Fatal(e)
			}
			response, e := http.DefaultClient.Do(request)
			if e != nil {
				t.Fatal(e)
			}
			body := new(bytes.Buffer)
			_, e = body.ReadFrom(response.Body)
			_ = response.Body.Close()
			if e != nil {
				t.Fatal(e)
			}
			f.output = append(f.output, body.String())
			if response.StatusCode < 400 {
				t.Fatal("disabled public enrollment route responded successfully", method, route)
			}
		}
	}
	f.stop(t, s)
	if initial.audit(t) != cr07fRead(t, f.path, f.requests).audit(t) {
		t.Fatal("rejected requests changed complete world/allocator/metadata")
	}
	s = f.start(t, true, nil)
	for i := range views {
		f.play(t, s, i, views[i])
	}
	preTurn := f.status(t, s)
	f.command(t, s, "update")
	updated := f.status(t, s)
	if updated.NumUpdates != preTurn.NumUpdates+1 || updated.NumSegments != 0 || updated.LastCommitSeq != preTurn.LastCommitSeq+1 || !updated.NextUpdate.Equal(preTurn.NextUpdate.Add(time.Hour)) {
		t.Fatal("update schedule/commit transition")
	}
	f.command(t, s, "segment")
	segmented := f.status(t, s)
	if segmented.NumUpdates != updated.NumUpdates || segmented.NumSegments != 1 || segmented.LastCommitSeq != updated.LastCommitSeq+1 || !segmented.NextSegment.After(updated.NextSegment) {
		t.Fatal("movement schedule/commit transition")
	}
	for i, req := range f.requests[:2] {
		replay := f.enroll(t, s, req, 0, "")
		if !replay.Replay || !reflect.DeepEqual(replay.Placement, views[i].Placement) {
			t.Fatal("post-turn replay changed placement")
		}
	}
	f.command(t, s, "quiesce")
	if f.status(t, s).Mode != "QUIESCED" {
		t.Fatal("quiesce failed")
	}
	f.enroll(t, s, f.requests[0], 4, "NOT_SERVING")
	f.stop(t, s)
	postTurn := cr07fRead(t, f.path, f.requests)
	f.playable(t, postTurn, views)
	if postTurn.commit.Kind != "shutdown" || postTurn.commit.CommitSeq != segmented.LastCommitSeq+1 || postTurn.commit.Turn != 1 || postTurn.commit.Segment != 1 || postTurn.schedule.NumUpdates != 1 || postTurn.schedule.NumSegments != 1 || postTurn.maintenance == nil {
		t.Fatal("strict CR18/43 durable records")
	}
	s = f.start(t, false, nil)
	paused := f.status(t, s)
	if paused.Mode != "QUIESCED" || paused.NumUpdates != 1 || paused.NumSegments != 1 || !paused.NextUpdate.Equal(segmented.NextUpdate) {
		t.Fatal("quiesced restart lost schedule")
	}
	f.enroll(t, s, f.requests[0], 4, "NOT_SERVING")
	f.command(t, s, "resume")
	if f.status(t, s).Mode != "SERVING" {
		t.Fatal("resume failed")
	}
	for i, req := range f.requests[:2] {
		replay := f.enroll(t, s, req, 0, "")
		if !replay.Replay || !reflect.DeepEqual(replay.Placement, views[i].Placement) {
			t.Fatal("restart/bootstrap-disabled replay")
		}
		f.play(t, s, i, views[i])
	}
	f.stop(t, s)
	final := cr07fRead(t, f.path, f.requests)
	f.playable(t, final, views)
	if final.maintenance != nil {
		t.Fatal("resume did not remove maintenance")
	}
}
