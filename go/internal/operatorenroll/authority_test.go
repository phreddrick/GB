package operatorenroll

import (
	"errors"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/enrollmentapi"
	"golang.org/x/crypto/bcrypt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProvisionAndImmutableAuthority(t *testing.T) {
	for _, p := range []string{"short", strings.Repeat("x", 73), "twelve-chars\x00", "twelve-chars\n", string([]byte{255})} {
		if _, e := Provision("account", p, "governor-credential"); e == nil {
			t.Fatal("bad credential accepted")
		}
	}
	if _, e := Provision("account", "identical-password", "identical-password"); e == nil {
		t.Fatal("equal credentials")
	}
	a, e := Provision("account", "race-password-123", "governor-password-456")
	if e != nil {
		t.Fatal(e)
	}
	if a.RaceHash == a.GovernorHash || bcrypt.CompareHashAndPassword([]byte(a.RaceHash), []byte("race-password-123")) != nil || bcrypt.CompareHashAndPassword([]byte(a.GovernorHash), []byte("governor-password-456")) != nil {
		t.Fatal("hash preparation")
	}
	m := Manifest{SchemaVersion: 1, OperatorID: "operator", GameID: "game", Accounts: []Account{a}}
	p, e := New(m)
	if e != nil {
		t.Fatal(e)
	}
	q := enrollmentapi.Query{RequestID: "request", GameID: "game", AccountID: "account"}
	ref, e := p.BindVersion(q, a.CredentialVersion)
	if e != nil || ref == "" {
		t.Fatal(e)
	}
	q.AccountID = "arbitrary"
	if _, e = p.Bind(q); e == nil {
		t.Fatal("unconfigured account")
	}
	q.AccountID = "account"
	q.GameID = "other"
	if _, e = p.Bind(q); e == nil {
		t.Fatal("unconfigured game")
	}
	rotated := a
	rotated.RaceHash = a.GovernorHash
	rotated.GovernorHash = a.RaceHash
	m.Accounts = append(m.Accounts, rotated)
	if _, e = New(m); e == nil {
		t.Fatal("rebound immutable version")
	}
	rotated.CredentialVersion = Version(rotated)
	m.Accounts[1] = rotated
	p, e = New(m)
	if e != nil {
		t.Fatal(e)
	}
	q.GameID = "game"
	if _, e = p.BindVersion(q, a.CredentialVersion); e != nil {
		t.Fatal("old version lost", e)
	}
	m.Accounts[0].AccountID = "mutated"
	if _, e = p.BindVersion(q, a.CredentialVersion); e != nil {
		t.Fatal("authority aliases input")
	}
	path := filepath.Join(t.TempDir(), "artifact.json")
	if e = WriteAccount(path, a); e != nil {
		t.Fatal(e)
	}
	if e = WriteAccount(path, a); e == nil {
		t.Fatal("overwrote artifact")
	}
	if _, e = ReadProtected(path); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(path, 0644); e != nil { //nolint:gosec // deliberately insecure fixture must be rejected
		t.Fatal(e)
	}
	if _, e = ReadProtected(path); e == nil {
		t.Fatal("public artifact accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if e = os.Symlink(path, link); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadProtected(link); e == nil {
		t.Fatal("symlink accepted")
	}
}

func TestPreparationFailureAndGameBinding(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		calls := 0
		a, e := provision("account", "race-password-123", "governor-password-456", func(_ []byte, _ int) ([]byte, error) {
			calls++
			if calls == failAt {
				return nil, errors.New("secret-bearing error")
			}
			return []byte("dummy"), nil
		})
		if e != ErrCredentials || a != (Account{}) || calls != failAt {
			t.Fatal("hash error did not fail closed")
		}
	}
	record := &db.UniverseRecord{Seed: 123, InitializedAt: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	before := GameID(record)
	record.Seed++
	if before == GameID(record) || GameID(nil) != "" {
		t.Fatal("game binding")
	}
	store, e := db.Open(filepath.Join(t.TempDir(), "empty.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = store.Close() }()
	p := &Provider{manifest: Manifest{GameID: before}}
	if p.CheckStore(store) == nil {
		t.Fatal("uninitialized store accepted")
	}
}
