package main

// GB1-06 regression: gbctl must not report success for an HTTP 2xx reply
// that does not confirm a successful result of the requested operation.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/phreddrick/GB/go/internal/engine"
)

func runAgainst(t *testing.T, handler http.HandlerFunc, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()
	var out, errs bytes.Buffer
	code := run(append([]string{"-server", srv.URL}, args...), &out, &errs, func(k string) (string, bool) {
		if k == "GBCTL_TOKEN" {
			return token, true
		}
		return "", false
	})
	return code, out.String(), errs.String()
}

func reply(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}
}

// truncated declares a longer body than it sends, so the client's read
// fails with an unexpected EOF.
func truncated(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Length", "500")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true,"op":"update","status":{"mode":"SERV`))
}

const statusJSON = `"status":{"mode":"SERVING","draining":false,"next_update":"2026-10-29T00:00:00Z","next_segment":"2026-10-29T00:00:00Z","num_updates":1,"num_segments":0,"update_minutes":30,"moves_per_update":3,"stars":3,"races":1,"ships":0,"turn_running":false,"queue_depth":0,"persistence":"x"}`

func TestGB106RejectsUnconfirmedReplies(t *testing.T) {
	cases := []struct {
		name    string
		h       http.HandlerFunc
		args    []string
		code    int
		errHint string
	}{
		{"not JSON", reply(200, "not JSON"), []string{"update"}, exitServer, "not a JSON object"},
		{"empty body", reply(200, ""), []string{"update"}, exitServer, "empty body"},
		{"truncated JSON", truncated, []string{"update"}, exitServer, "reading the reply failed"},
		{"truncated object", reply(200, `{"ok":true,"op":"update",`), []string{"update"}, exitServer, "not a JSON object"},
		{"JSON array", reply(200, `[1,2]`), []string{"update"}, exitServer, "not a JSON object"},
		{"wrong type for ok", reply(200, `{"ok":"yes","op":"update",`+statusJSON+`}`), []string{"update"}, exitServer, "unexpected envelope"},
		{"missing ok", reply(200, `{"op":"update",`+statusJSON+`}`), []string{"update"}, exitServer, `missing required field "ok"`},
		{"ok false on 200", reply(200, `{"ok":false,"op":"update","error":"turn failed",`+statusJSON+`}`), []string{"update"}, exitServer, "not confirmed"},
		{"wrong operation", reply(200, `{"ok":true,"op":"segment",`+statusJSON+`}`), []string{"update"}, exitServer, `expected "update"`},
		{"no status", reply(200, `{"ok":true,"op":"update"}`), []string{"update"}, exitServer, "lacks the engine status"},
		{"unknown field", reply(200, `{"ok":true,"op":"update","surprise":1,`+statusJSON+`}`), []string{"update"}, exitServer, "unexpected envelope"},
		{"trailing data", reply(200, `{"ok":true,"op":"update",`+statusJSON+`} {}`), []string{"update"}, exitServer, "after top-level value"},
		{"init without record", reply(200, `{"ok":true,"op":"universe.init",`+statusJSON+`}`), []string{"universe", "init", "-seed", "5"}, exitServer, "lacks the universe record"},
		{"error status keeps its code", reply(409, `{"ok":false,"op":"update","error":"engine halted"}`), []string{"update"}, exitRefused, "engine halted"},
		{"error status, malformed body", reply(503, `<html>busy</html>`), []string{"update"}, exitServer, "HTTP 503"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errs := runAgainst(t, c.h, c.args...)
			if code != c.code {
				t.Fatalf("exit %d, want %d; stdout=%q stderr=%q", code, c.code, out, errs)
			}
			if strings.Contains(out, ": ok") {
				t.Fatalf("success printed for an unconfirmed reply: %q", out)
			}
			if !strings.Contains(errs, c.errHint) {
				t.Fatalf("stderr %q lacks %q", errs, c.errHint)
			}
			if strings.Contains(out+errs, token) {
				t.Fatal("token printed")
			}
		})
	}
}

// TestGB106AcceptsValidReplies: well-formed confirmations still succeed,
// against the real admin listener (start) and a literal envelope.
func TestGB106AcceptsValidReplies(t *testing.T) {
	code, out, errs := runAgainst(t, reply(200, `{"ok":true,"op":"update",`+statusJSON+`}`), "update")
	if code != exitOK || !strings.Contains(out, "update: ok") {
		t.Fatalf("valid literal reply: exit %d stdout=%q stderr=%q", code, out, errs)
	}
	url := start(t, engine.ModeUninitialized)
	for _, args := range [][]string{{"status"}, {"universe", "init", "-seed", "9", "-stars", "3"}} {
		var o, e bytes.Buffer
		c := run(append([]string{"-server", url}, args...), &o, &e, func(k string) (string, bool) {
			if k == "GBCTL_TOKEN" {
				return token, true
			}
			return "", false
		})
		if c != exitOK {
			t.Fatalf("%v against the real listener: exit %d stdout=%q stderr=%q", args, c, o.String(), e.String())
		}
	}
	urlServing := start(t, engine.ModeServing)
	for _, args := range [][]string{{"update"}, {"segment"}, {"quiesce"}, {"resume"}} {
		var o, e bytes.Buffer
		c := run(append([]string{"-server", urlServing}, args...), &o, &e, func(k string) (string, bool) {
			if k == "GBCTL_TOKEN" {
				return token, true
			}
			return "", false
		})
		if c != exitOK || !strings.Contains(o.String(), args[0]+": ok") {
			t.Fatalf("%v against the real listener: exit %d stdout=%q stderr=%q", args, c, o.String(), e.String())
		}
	}
}
