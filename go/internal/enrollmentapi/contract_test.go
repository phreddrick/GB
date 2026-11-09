package enrollmentapi

import (
	"encoding/json"
	"github.com/phreddrick/GB/go/internal/racegen"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPureRulesContract(t *testing.T) {
	d := Definitions()
	if d.Budget != racegen.Budget || len(d.Attributes) != 11 || len(d.Homeworlds) != 7 || len(d.Sectors) != 8 {
		t.Fatal(d)
	}
	d.Attributes[0].Maximum = 999
	if Definitions().Attributes[0].Maximum == 999 {
		t.Fatal("aliased metadata")
	}
	s := racegen.Default()
	s.SectorCompat[racegen.Land] = 100
	before := s
	response := Calculate(s)
	if !response.Valid || response.Breakdown == nil {
		t.Fatal(response)
	}
	if !reflect.DeepEqual(s, before) {
		t.Fatal("mutated")
	}
	for _, body := range []string{`null`, `{}`, strings.Repeat("x", MaxBody+1)} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		w := httptest.NewRecorder()
		CostHandler(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	b, _ := json.Marshal(s)
	w := httptest.NewRecorder()
	CostHandler(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(b))))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestPlacementResponseValidation(t *testing.T) {
	q := Query{"request", "game", "account"}
	v := NewView(q)
	v.State = Confirmed
	v.Outcome = "COMMITTED"
	v.Placement = &Placement{Player: 1, GovernmentShip: 1, CreatedAt: time.Now()}
	if v.Validate(q) != nil {
		t.Fatal("valid")
	}
	for _, change := range []func(*View){func(v *View) { v.Outcome = "UNCERTAIN" }, func(v *View) { v.Binding = "wrong" }, func(v *View) { v.Placement = nil }, func(v *View) { v.SchemaVersion = 2 }, func(v *View) { v.State = NoKnownPlacement }} {
		bad := v
		change(&bad)
		if bad.Validate(q) == nil {
			t.Fatal("bad response")
		}
	}
}

// The OpenAPI artifact is JSON-form YAML 1.2, readable by both standard JSON
// tooling and YAML consumers, without adding a runtime schema dependency.
func TestOpenAPIRuleMetadataDoesNotDrift(t *testing.T) {
	b, e := os.ReadFile("../../../api/openapi/gb-v1.yaml")
	if e != nil {
		t.Fatal(e)
	}
	var document struct {
		OpenAPI    string `json:"openapi"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Minimum, Maximum, Default float64
					MinItems, MaxItems        int
					Enum                      []string
				}
			}
		}
	}
	if e = json.Unmarshal(b, &document); e != nil {
		t.Fatal(e)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatal("OpenAPI version")
	}
	for _, d := range racegen.AttributeDefinitions() {
		p, ok := document.Components.Schemas["Attributes"].Properties[d.Key]
		if !ok || p.Minimum != d.Minimum || p.Maximum != d.Maximum || p.Default != d.Default {
			t.Fatal("metadata drift", d.Key)
		}
	}
	var raw map[string]any
	if e = json.Unmarshal(b, &raw); e != nil {
		t.Fatal(e)
	}
	schemas := raw["components"].(map[string]any)["schemas"].(map[string]any)
	sector := schemas["SectorCompatibility"].(map[string]any)
	if sector["minItems"] != float64(8) || sector["maxItems"] != float64(8) {
		t.Fatal("sector contract drift")
	}
	for path, ops := range raw["paths"].(map[string]any) {
		if strings.HasPrefix(path, "/api/enroll") {
			for _, op := range ops.(map[string]any) {
				if op.(map[string]any)["x-availability"] != "future-contract-only-unregistered" {
					t.Fatal("public exposure claim")
				}
			}
		}
	}
}
