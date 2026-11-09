package main

import (
	"bytes"
	"encoding/json"
	"github.com/phreddrick/GB/go/internal/operatorenroll"
	"golang.org/x/crypto/bcrypt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialCLIProtectedInputsAndNoSecretOutput(t *testing.T) {
	dir := t.TempDir()
	race := filepath.Join(dir, "race.secret")
	gov := filepath.Join(dir, "governor.secret")
	out := filepath.Join(dir, "account.json")
	for path, password := range map[string]string{race: "race-password-123\n", gov: "governor-password-456\n"} {
		if e := os.WriteFile(path, []byte(password), 0600); e != nil {
			t.Fatal(e)
		}
	}
	args := []string{"credentials", "prepare", "-account", "account", "-race-file", race, "-governor-file", gov, "-out", out, "--god-bootstrap"}
	var stdout, stderr bytes.Buffer
	noEnv := func(string) (string, bool) { return "", false }
	if code := run(args, &stdout, &stderr, noEnv); code != exitOK {
		t.Fatal(code, stderr.String())
	}
	data, e := operatorenroll.ReadProtected(out)
	if e != nil {
		t.Fatal(e)
	}
	var a operatorenroll.Account
	if e = json.Unmarshal(data, &a); e != nil {
		t.Fatal(e)
	}
	if !a.GodBootstrap || a.CredentialVersion != operatorenroll.Version(a) || bcrypt.CompareHashAndPassword([]byte(a.RaceHash), []byte("race-password-123")) != nil || bcrypt.CompareHashAndPassword([]byte(a.GovernorHash), []byte("governor-password-456")) != nil {
		t.Fatal("incorrect artifact")
	}
	if code := run(args, &stdout, &stderr, noEnv); code == exitOK {
		t.Fatal("overwrote immutable artifact")
	}
	for _, secret := range []string{"race-password-123", "governor-password-456", a.RaceHash, a.GovernorHash} {
		if strings.Contains(stdout.String()+stderr.String(), secret) || bytes.Contains(data, []byte("race-password-123")) {
			t.Fatal("secret leaked")
		}
	}
	// Equal plaintext is refused before any artifact can be created.
	if e = os.WriteFile(gov, []byte("race-password-123"), 0600); e != nil {
		t.Fatal(e)
	}
	other := filepath.Join(dir, "other.json")
	args[9] = other
	if code := run(args, &stdout, &stderr, noEnv); code != exitUsage {
		t.Fatal(code)
	}
	if _, e = os.Stat(other); !os.IsNotExist(e) {
		t.Fatal("failed preparation created artifact", e)
	}
}
