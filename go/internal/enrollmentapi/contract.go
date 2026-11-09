// Package enrollmentapi owns strict enrollment wire contracts, never authority.
// Its pure handlers are deliberately NOT registered on the player listener.
package enrollmentapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/phreddrick/GB/go/internal/racegen"
	"github.com/phreddrick/GB/go/internal/wirejson"
)

const MaxBody = 64 << 10
const (
	Confirmed        = "CONFIRMED"
	NoKnownPlacement = "NO_KNOWN_PLACEMENT"
	NotPermitted     = "NOT_PERMITTED"
	Conflict         = "CONFLICT"
	Reconciliation   = "RECONCILIATION_REQUIRED"
	Unknown          = "UNKNOWN"
)

// OperatorRequest contains selection of a configured binding, not a player
// identity assertion. Credentials/references are resolved only by the server.
type OperatorRequest struct {
	CredentialVersion string       `json:"credential_version"`
	RequestID         string       `json:"request_id"`
	GameID            string       `json:"game_id"`
	AccountID         string       `json:"account_id"`
	Spec              racegen.Spec `json:"spec"`
	God               bool         `json:"god"`
	Guest             bool         `json:"guest"`
}
type Query struct {
	RequestID string `json:"request_id"`
	GameID    string `json:"game_id"`
	AccountID string `json:"account_id"`
}
type Placement struct {
	Player         int       `json:"player_num"`
	Star           uint8     `json:"home_star"`
	Planet         uint8     `json:"home_planet"`
	CapitalX       uint8     `json:"capital_x"`
	CapitalY       uint8     `json:"capital_y"`
	GovernmentShip uint32    `json:"government_ship"`
	CreatedAt      time.Time `json:"created_at"`
}

// View exposes placement identity, never account IDs, credential refs or hashes.
// Binding is an integrity echo of the non-secret authorized request/account/game
// selection; it is NOT authentication. NO_KNOWN_PLACEMENT is not rollback proof.
type View struct {
	IntentDigest  string     `json:"intent_digest,omitempty"`
	SchemaVersion int        `json:"schema_version"`
	RequestID     string     `json:"request_id"`
	GameID        string     `json:"game_id"`
	Binding       string     `json:"binding"`
	State         string     `json:"state"`
	Outcome       string     `json:"outcome"`
	Replay        bool       `json:"replay"`
	Permitted     bool       `json:"permitted"`
	Placement     *Placement `json:"placement,omitempty"`
}

func ID(s string, max int) bool {
	if len(s) < 1 || len(s) > max {
		return false
	}
	for _, c := range s {
		allowed := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
		if !allowed {
			return false
		}
	}
	return true
}
func (q Query) Valid() bool { return ID(q.RequestID, 64) && ID(q.GameID, 128) && ID(q.AccountID, 128) }
func Binding(q Query) string {
	b, _ := json.Marshal(q)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func NewView(q Query) View {
	return View{SchemaVersion: 1, RequestID: q.RequestID, GameID: q.GameID, Binding: Binding(q), State: Unknown, Outcome: Unknown}
}
func (r OperatorRequest) Query() Query { return Query{r.RequestID, r.GameID, r.AccountID} }
func (v View) Validate(q Query) error {
	bad := errors.New("unconfirmed enrollment response")
	if v.SchemaVersion != 1 || !q.Valid() || v.RequestID != q.RequestID || v.GameID != q.GameID || v.Binding != Binding(q) {
		return bad
	}
	switch v.State {
	case Confirmed:
		if v.Outcome != "COMMITTED" || v.Placement == nil {
			return bad
		}
	case NoKnownPlacement, NotPermitted, Conflict, Reconciliation, Unknown:
		if v.Placement != nil {
			return bad
		}
	default:
		return bad
	}
	switch v.Outcome {
	case "COMMITTED", "NOT_COMMITTED", "UNCERTAIN", Unknown:
	default:
		return bad
	}
	if v.State != Confirmed && v.Outcome == "COMMITTED" && v.State != Reconciliation {
		return bad
	}
	if v.Outcome == "UNCERTAIN" && v.State != Reconciliation {
		return bad
	}
	if v.State == NoKnownPlacement && v.Outcome != Unknown {
		return bad
	}
	if v.Replay && v.State != Confirmed {
		return bad
	}
	if p := v.Placement; p != nil {
		if p.Player < 1 || p.Player > 63 || p.Planet >= 10 || p.GovernmentShip == 0 || p.CapitalX == 255 || p.CapitalY == 255 || p.CreatedAt.IsZero() || p.CreatedAt.Year() < 1 || p.CreatedAt.Year() > 9999 {
			return bad
		}
	}
	return nil
}
func DecodeBody(r io.Reader, dst any) error {
	data, e := io.ReadAll(io.LimitReader(r, MaxBody+1))
	if e != nil || len(data) > MaxBody {
		return wirejson.ErrInvalid
	}
	return wirejson.Decode(data, dst)
}

type CostResponse struct {
	Valid     bool               `json:"valid"`
	Breakdown *racegen.Breakdown `json:"breakdown,omitempty"`
	Errors    racegen.Errors     `json:"errors"`
}

func Calculate(spec racegen.Spec) CostResponse {
	b, costErrors := racegen.Cost(spec)
	errs := racegen.Validate(spec)
	out := CostResponse{Valid: len(errs) == 0, Errors: errs}
	if out.Errors == nil {
		out.Errors = racegen.Errors{}
	}
	if len(costErrors) == 0 {
		out.Breakdown = &b
	}
	return out
}

type Attribute struct {
	ID      int     `json:"id"`
	Key     string  `json:"key"`
	Name    string  `json:"name"`
	Minimum float64 `json:"minimum"`
	Default float64 `json:"default"`
	Maximum float64 `json:"maximum"`
	Kind    string  `json:"kind"`
}
type Homeworld struct {
	ID            int                       `json:"id"`
	Name          string                    `json:"name"`
	PlanetType    uint8                     `json:"planet_type"`
	Cost          int                       `json:"cost"`
	CommonSectors [racegen.SectorCount]bool `json:"common_sectors"`
}
type Sector struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
type Options struct {
	SchemaVersion int         `json:"schema_version"`
	Budget        int         `json:"budget"`
	Attributes    []Attribute `json:"attributes"`
	Homeworlds    []Homeworld `json:"homeworlds"`
	Sectors       []Sector    `json:"sectors"`
	RaceTypes     [2]string   `json:"race_types"`
}

func Definitions() Options {
	out := Options{SchemaVersion: 1, Budget: racegen.Budget, RaceTypes: [2]string{"normal", "metamorph"}}
	for _, d := range racegen.AttributeDefinitions() {
		out.Attributes = append(out.Attributes, Attribute{int(d.ID), d.Key, d.Name, d.Minimum, d.Default, d.Maximum, string(d.Kind)})
	}
	for _, h := range racegen.Homeworlds() {
		out.Homeworlds = append(out.Homeworlds, Homeworld{int(h.ID), h.Name, h.PlanetType, h.Cost, h.CommonSectors})
	}
	for _, s := range racegen.Sectors() {
		out.Sectors = append(out.Sectors, Sector{int(s.ID), s.Name})
	}
	return out
}
func OptionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		wireError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Definitions())
}
func CostHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		wireError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	var spec racegen.Spec
	if DecodeBody(r.Body, &spec) != nil {
		wireError(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Calculate(spec))
}
func IsHexDigest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}

// IntentDigest binds a response to the complete operator request without
// disclosing account/ref values; it is an integrity check, not authentication.
func IntentDigest(req OperatorRequest) string {
	raw, _ := json.Marshal(req)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func wireError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code string `json:"code"`
	}{code})
}
