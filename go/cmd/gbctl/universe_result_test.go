package main

// GB1-06R regression: a universe.init reply is confirmed only when its
// "result" is the universe record the server contract defines
// (db.UniverseRecord as written by universe.Init), complete, in range and
// consistent with the request. These tests go through run(), that is through
// the production admin.Client path, against literal HTTP replies and against
// the admin listener with a fake engine.

import (
	"strings"
	"testing"
)

const initArgsJSON = `"seed":5,"stars":3,"min_planets":1,"max_planets":2,"planets":4,"generator":"makeuniv-go/1","initialized_at":"2026-10-29T00:00:00Z","tool":"gb-server admin"`

func initReply(result string) string {
	return `{"ok":true,"op":"universe.init","result":` + result + `,` + statusJSON + `}`
}

var initArgs = []string{"universe", "init", "-seed", "5", "-stars", "3", "-min-planets", "1", "-max-planets", "2"}

// TestReviewRejectWrongUniverseResult is the independent reviewer's
// reproduction (second Codex review, GB1-06R), kept verbatim in substance:
// a string, false, an empty array and an empty object were all accepted
// with exit 0 and "universe init: ok" before the correction.
func TestReviewRejectWrongUniverseResult(t *testing.T) {
	for _, result := range []string{`"not a universe record"`, `false`, `[]`, `{}`} {
		code, out, errs := runAgainst(t, reply(200, `{"ok":true,"op":"universe.init","result":`+result+`,`+statusJSON+`}`), "universe", "init", "-seed", "5")
		if code == 0 || strings.Contains(out, ": ok") {
			t.Errorf("invalid result %s accepted: exit=%d stdout=%q stderr=%q", result, code, out, errs)
		}
	}
}

func TestGB106RRejectsInvalidUniverseRecords(t *testing.T) {
	cases := []struct {
		name    string
		result  string
		args    []string
		errHint string
	}{
		{"string", `"not a universe record"`, initArgs, "not a JSON object (got a string)"},
		{"false", `false`, initArgs, "not a JSON object (got a boolean)"},
		{"true", `true`, initArgs, "not a JSON object (got a boolean)"},
		{"empty array", `[]`, initArgs, "not a JSON object (got an array)"},
		{"array of objects", `[{` + initArgsJSON + `}]`, initArgs, "not a JSON object (got an array)"},
		{"empty object", `{}`, initArgs, "seed is missing or zero"},
		{"null", `null`, initArgs, "lacks the universe record"},
		{"number", `0`, initArgs, "not a JSON object (got a number)"},
		{"seed only", `{"seed":5}`, initArgs, "stars is missing or zero"},
		{"seed as string", `{"seed":"5","stars":3}`, initArgs, "cannot unmarshal string"},
		{"seed zero", `{` + strings.Replace(initArgsJSON, `"seed":5`, `"seed":0`, 1) + `}`, initArgs, "seed is missing or zero"},
		{"seed differs from request", `{` + strings.Replace(initArgsJSON, `"seed":5`, `"seed":6`, 1) + `}`, initArgs, "seed 6 does not match the requested seed 5"},
		{"stars differ from request", `{` + strings.Replace(initArgsJSON, `"stars":3`, `"stars":4`, 1) + `}`, initArgs, "stars 4 does not match the requested 3"},
		{"min_planets differs", `{` + strings.Replace(initArgsJSON, `"min_planets":1`, `"min_planets":2`, 1) + `}`, initArgs, "min_planets 2 does not match the requested 1"},
		{"max_planets differs", `{` + strings.Replace(initArgsJSON, `"max_planets":2`, `"max_planets":3`, 1) + `}`, initArgs, "max_planets 3 does not match the requested 2"},
		{"min_planets missing", `{` + strings.Replace(initArgsJSON, `"min_planets":1,`, ``, 1) + `}`, initArgs, "min_planets is missing or zero"},
		{"max_planets missing", `{` + strings.Replace(initArgsJSON, `"max_planets":2,`, ``, 1) + `}`, initArgs, "max_planets is missing or zero"},
		{"too few planets", `{` + strings.Replace(initArgsJSON, `"planets":4`, `"planets":2`, 1) + `}`, initArgs, "planets 2 is outside 3..6"},
		{"too many planets", `{` + strings.Replace(initArgsJSON, `"planets":4`, `"planets":7`, 1) + `}`, initArgs, "planets 7 is outside 3..6"},
		{"planets missing", `{` + strings.Replace(initArgsJSON, `"planets":4,`, ``, 1) + `}`, initArgs, "planets 0 is outside 3..6"},
		{"stars out of range", `{` + strings.Replace(initArgsJSON, `"stars":3`, `"stars":300`, 1) + `}`, []string{"universe", "init", "-seed", "5"}, "stars must be 1..255"},
		{"max below min", `{` + strings.Replace(initArgsJSON, `"min_planets":1`, `"min_planets":3`, 1) + `}`, []string{"universe", "init", "-seed", "5"}, "max planets must be 3..10 (got 2)"},
		{"generator empty", `{` + strings.Replace(initArgsJSON, `"generator":"makeuniv-go/1"`, `"generator":" "`, 1) + `}`, initArgs, "generator is missing"},
		{"generator missing", `{` + strings.Replace(initArgsJSON, `"generator":"makeuniv-go/1",`, ``, 1) + `}`, initArgs, "generator is missing"},
		{"initialized_at missing", `{` + strings.Replace(initArgsJSON, `"initialized_at":"2026-10-29T00:00:00Z",`, ``, 1) + `}`, initArgs, "initialized_at is missing"},
		{"initialized_at not a time", `{` + strings.Replace(initArgsJSON, `"initialized_at":"2026-10-29T00:00:00Z"`, `"initialized_at":"yesterday"`, 1) + `}`, initArgs, "parsing time"},
		{"tool empty", `{` + strings.Replace(initArgsJSON, `"tool":"gb-server admin"`, `"tool":""`, 1) + `}`, initArgs, "tool is missing"},
		{"unknown member", `{` + initArgsJSON + `,"extra":1}`, initArgs, `unknown field "extra"`},
		{"record nested one level too deep", `{"record":{` + initArgsJSON + `}}`, initArgs, `unknown field "record"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errs := runAgainst(t, reply(200, initReply(c.result)), c.args...)
			if code != exitServer {
				t.Fatalf("exit %d, want %d; stdout=%q stderr=%q", code, exitServer, out, errs)
			}
			if strings.Contains(out, ": ok") || out != "" {
				t.Fatalf("stdout must stay empty for an unconfirmed init: %q", out)
			}
			if !strings.Contains(errs, c.errHint) {
				t.Fatalf("stderr %q lacks %q", errs, c.errHint)
			}
			if !strings.Contains(errs, "the outcome is unknown") {
				t.Fatalf("stderr %q lacks the outcome warning", errs)
			}
			if strings.Contains(out+errs, token) {
				t.Fatal("token printed")
			}
		})
	}
	// The same input with -json must not print the envelope either.
	code, out, _ := runAgainst(t, reply(200, initReply(`{}`)), append([]string{"-json"}, initArgs...)...)
	if code != exitServer || out != "" {
		t.Fatalf("-json with an invalid record: exit %d stdout=%q", code, out)
	}
}

// TestGB106RRejectsResultOnOtherOperations: operations that define no
// result must not carry one; a stray result is not silently ignored.
func TestGB106RRejectsResultOnOtherOperations(t *testing.T) {
	for _, op := range []string{"update", "segment", "quiesce", "resume"} {
		code, out, errs := runAgainst(t, reply(200, `{"ok":true,"op":"`+op+`","result":{"seed":5},`+statusJSON+`}`), op)
		if code != exitServer || strings.Contains(out, ": ok") || !strings.Contains(errs, "unexpected result") {
			t.Fatalf("%s with a result: exit %d stdout=%q stderr=%q", op, code, out, errs)
		}
	}
	// An explicit null result is the same as none.
	code, out, _ := runAgainst(t, reply(200, `{"ok":true,"op":"update","result":null,`+statusJSON+`}`), "update")
	if code != exitOK || !strings.Contains(out, "update: ok") {
		t.Fatalf("update with null result: exit %d stdout=%q", code, out)
	}
}

// TestGB106RAcceptsValidUniverseRecords: genuine records are accepted and
// reported, with explicit parameters, with server defaults, and from the
// admin listener itself (the e2e suite covers the real gb-server).
func TestGB106RAcceptsValidUniverseRecords(t *testing.T) {
	code, out, errs := runAgainst(t, reply(200, initReply(`{`+initArgsJSON+`}`)), initArgs...)
	if code != exitOK || !strings.Contains(out, "universe init: ok") || !strings.Contains(out, "seed 5, 3 stars, 4 planets (1..2 per star), generator makeuniv-go/1") {
		t.Fatalf("valid record: exit %d stdout=%q stderr=%q", code, out, errs)
	}
	// Request without optional parameters: the record carries the server
	// defaults (100 stars, 2..10 planets per star).
	defaults := `{"seed":5,"stars":100,"min_planets":2,"max_planets":10,"planets":500,"generator":"makeuniv-go/1","initialized_at":"2026-10-29T00:00:00Z","tool":"makeuniv"}`
	code, out, errs = runAgainst(t, reply(200, initReply(defaults)), "universe", "init", "-seed", "5")
	if code != exitOK || !strings.Contains(out, "universe init: ok") {
		t.Fatalf("defaults record: exit %d stdout=%q stderr=%q", code, out, errs)
	}
	// -json prints the envelope with the validated record.
	code, out, errs = runAgainst(t, reply(200, initReply(`{`+initArgsJSON+`}`)), append([]string{"-json"}, initArgs...)...)
	if code != exitOK || !strings.Contains(out, `"generator": "makeuniv-go/1"`) || !strings.Contains(out, `"seed": 5`) {
		t.Fatalf("-json valid record: exit %d stdout=%q stderr=%q", code, out, errs)
	}
	// Against the admin listener (fake engine returning a complete record),
	// with and without optional parameters.
	for _, args := range [][]string{initArgs, {"universe", "init", "-seed", "11"}} {
		code, out, errs = runAgainst(t, startHandler(t), args...)
		if code != exitOK || !strings.Contains(out, "universe init: ok") {
			t.Fatalf("%v against the listener: exit %d stdout=%q stderr=%q", args, code, out, errs)
		}
	}
}
