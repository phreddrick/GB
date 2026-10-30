package admin

// GB1-06R: unit coverage of the universe-record validation the client
// applies to universe.init replies (the gbctl tests cover the full path).

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGB106RUniverseRecordValidation(t *testing.T) {
	req := InitRequest{Seed: 7, Stars: 2, MinPlanets: 1, MaxPlanets: 3}
	valid := `{"seed":7,"stars":2,"min_planets":1,"max_planets":3,"planets":5,"generator":"makeuniv-go/1","initialized_at":"2026-10-29T10:00:00Z","tool":"gb-server admin"}`
	rec, err := universeRecordFromReply(json.RawMessage(valid), req)
	if err != nil || rec.Seed != 7 || rec.Planets != 5 || !rec.InitializedAt.Equal(time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("valid record rejected: %+v %v", rec, err)
	}
	// Whitespace around the object is tolerated; leading garbage is not.
	if _, err := universeRecordFromReply(json.RawMessage(" \n"+valid+" "), req); err != nil {
		t.Fatalf("padded record rejected: %v", err)
	}
	bad := map[string]string{
		"":                      "lacks the universe record",
		"null":                  "lacks the universe record",
		`"x"`:                   "got a string",
		"false":                 "got a boolean",
		"[]":                    "got an array",
		"12":                    "got a number",
		"{}":                    "seed is missing or zero",
		`{"seed":8}`:            "does not match the requested seed",
		`{"seed":7,"stars":-1}`: "min_planets is missing or zero",
		`{"seed":7,"stars":2,"min_planets":1,"max_planets":3,"planets":5,"generator":"g","tool":"t"}`:                                                  "initialized_at is missing",
		`{"seed":7,"stars":2,"min_planets":1,"max_planets":3,"planets":99,"generator":"g","initialized_at":"2026-10-29T10:00:00Z","tool":"t"}`:         "planets 99 is outside 2..6",
		`{"seed":7,"stars":2,"min_planets":1,"max_planets":3,"planets":5,"generator":"g","initialized_at":"2026-10-29T10:00:00Z","tool":"t","x":true}`: `unknown field "x"`,
	}
	for raw, hint := range bad {
		_, err := universeRecordFromReply(json.RawMessage(raw), req)
		if err == nil || !strings.Contains(err.Error(), hint) {
			t.Errorf("%q: err %v, want %q", raw, err, hint)
		}
	}
	// Server defaults are accepted when the request left the parameters open.
	open := InitRequest{Seed: 7}
	if _, err := universeRecordFromReply(json.RawMessage(`{"seed":7,"stars":100,"min_planets":2,"max_planets":10,"planets":321,"generator":"g","initialized_at":"2026-10-29T10:00:00Z","tool":"t"}`), open); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}
}
