package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"strings"
	"testing"
)

func TestJSONAndLegacyBridge(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo, "gb-server")
	logger.Info("hello", KeyComponent, "actor", KeyTurn, 3)
	log.Printf("legacy %d", 7)
	logger.Debug("hidden")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), buf.String())
	}
	var first, second map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if first["msg"] != "hello" || first["component"] != "actor" || first["service"] != "gb-server" || first["turn"] != float64(3) {
		t.Errorf("first line fields: %v", first)
	}
	if second["msg"] != "legacy 7" || second["level"] != "INFO" {
		t.Errorf("legacy bridge: %v", second)
	}
}

func TestLevelsAndErrClass(t *testing.T) {
	if ParseLevel("WARN") != slog.LevelWarn || ParseLevel("debug") != slog.LevelDebug || ParseLevel("") != slog.LevelInfo {
		t.Error("ParseLevel")
	}
	if got := ErrClass(nil); got != "nil" {
		t.Error(got)
	}
	if got := ErrClass(errors.New("x")); got != "errorString" {
		t.Error(got)
	}
	type myErr struct{ error }
	if got := ErrClass(&myErr{errors.New("x")}); got != "myErr" {
		t.Error(got)
	}
}
