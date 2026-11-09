// Package operatorenroll provides one private operator's configured authority.
// It is not a player identity service, account-registration endpoint or DB writer.
package operatorenroll

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/enrollmentapi"
	"github.com/phreddrick/GB/go/internal/game"
	"github.com/phreddrick/GB/go/internal/wirejson"
	"golang.org/x/crypto/bcrypt"
)

var ErrAuthority = errors.New("operator enrollment authority unavailable or unauthorized")
var ErrCredentials = errors.New("credentials must be distinct valid UTF-8 strings of 12..72 bytes without NUL or line endings")

const MaxFile = 1 << 20

// Manifest is a protected provisioning artifact, never a request/response.
// The private admin bearer authenticates this one configured service principal.
type Manifest struct {
	SchemaVersion    int       `json:"schema_version"`
	OperatorID       string    `json:"operator_id"`
	GameID           string    `json:"game_id"`
	BootstrapEnabled bool      `json:"bootstrap_enabled"`
	Accounts         []Account `json:"accounts"`
}
type Account struct {
	CredentialVersion string `json:"credential_version"`
	AccountID         string `json:"account_id"`
	GodBootstrap      bool   `json:"god_bootstrap"`
	AllowGuest        bool   `json:"allow_guest"`
	RaceHash          string `json:"race_hash"`
	GovernorHash      string `json:"governor_hash"`
}
type Provider struct {
	manifest Manifest
	accounts map[string]Account
	refs     map[string]string
}

// ReadProtected refuses symlinks, nonregular files and any group/other access.
// Opened-file identity is compared with lstat to fail closed on replacement.
func ReadProtected(path string) ([]byte, error) {
	before, e := os.Lstat(path)
	if e != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 {
		return nil, ErrAuthority
	}
	f, e := os.Open(path) //nolint:gosec // operator-selected protected file, checked before/after open
	if e != nil {
		return nil, ErrAuthority
	}
	defer func() { _ = f.Close() }()
	after, e := f.Stat()
	if e != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || after.Mode().Perm()&0077 != 0 {
		return nil, ErrAuthority
	}
	data, e := io.ReadAll(io.LimitReader(f, MaxFile+1))
	if e != nil || len(data) > MaxFile {
		return nil, ErrAuthority
	}
	return data, nil
}
func Load(path string) (*Provider, error) {
	data, e := ReadProtected(path)
	if e != nil {
		return nil, e
	}
	var manifest Manifest
	if wirejson.Decode(data, &manifest) != nil {
		return nil, ErrAuthority
	}
	return New(manifest)
}
func New(m Manifest) (*Provider, error) {
	if m.SchemaVersion != 1 || !enrollmentapi.ID(m.OperatorID, 64) || !enrollmentapi.ID(m.GameID, 128) || len(m.Accounts) < 1 || len(m.Accounts) > 256 {
		return nil, ErrAuthority
	}
	p := &Provider{manifest: m, accounts: map[string]Account{}, refs: map[string]string{}}
	p.manifest.Accounts = append([]Account(nil), m.Accounts...)
	godCount := 0
	roles := map[string]Account{}
	for _, a := range m.Accounts {
		if !enrollmentapi.ID(a.AccountID, 128) || !game.ValidEnrollmentCredentials(game.EnrollmentCredentials{RaceHash: a.RaceHash, GovernorHash: a.GovernorHash}) {
			return nil, ErrAuthority
		}
		for _, hash := range []string{a.RaceHash, a.GovernorHash} {
			cost, e := bcrypt.Cost([]byte(hash))
			if e != nil || cost > 14 {
				return nil, ErrAuthority
			}
		}
		key := a.AccountID + ":" + a.CredentialVersion
		if a.CredentialVersion != Version(a) {
			return nil, ErrAuthority
		}
		if _, exists := p.accounts[key]; exists {
			return nil, ErrAuthority
		}
		if prior, ok := roles[a.AccountID]; ok {
			if prior.GodBootstrap != a.GodBootstrap || prior.AllowGuest != a.AllowGuest {
				return nil, ErrAuthority
			}
		} else {
			roles[a.AccountID] = a
			if a.GodBootstrap {
				godCount++
			}
		}
		p.accounts[key] = a
		p.refs[key] = bindingRef(m.OperatorID, m.GameID, key)
	}
	if godCount > 1 || len(roles) > 63 {
		return nil, ErrAuthority
	}
	return p, nil
}
func bindingRef(operator, gameID, account string) string {
	sum := sha256.Sum256([]byte(operator + "\x00" + gameID + "\x00" + account))
	return "operator-" + hex.EncodeToString(sum[:])
}
func credentialRef(gameID, account, kind, hash string) string {
	sum := sha256.Sum256([]byte(gameID + "\x00" + account + "\x00" + kind + "\x00" + hash))
	return "bcrypt-v1-" + hex.EncodeToString(sum[:])
}

// Bind is called ONLY after admin bearer authentication. An account selection
// must exist in this immutable server authority; JSON alone grants nothing.
func (p *Provider) Bind(q enrollmentapi.Query) (string, error) {
	if p == nil || !q.Valid() || q.GameID != p.manifest.GameID {
		return "", ErrAuthority
	}
	chosen := ""
	for key, a := range p.accounts {
		if a.AccountID == q.AccountID && (chosen == "" || key < chosen) {
			chosen = key
		}
	}
	if chosen == "" {
		return "", ErrAuthority
	}
	return p.refs[chosen], nil
}
func (p *Provider) BindVersion(q enrollmentapi.Query, version string) (string, error) {
	if _, e := p.Bind(q); e != nil {
		return "", e
	}
	key := q.AccountID + ":" + version
	reference, ok := p.refs[key]
	if !ok {
		return "", ErrAuthority
	}
	return reference, nil
}

// Version addresses the complete salted bcrypt pair plus account context. It
// cannot be rebound: New recomputes it. Retain old versions for existing intents.
func Version(a Account) string {
	raw, _ := json.Marshal([]string{a.AccountID, a.RaceHash, a.GovernorHash})
	sum := sha256.Sum256(raw)
	return "v1-" + hex.EncodeToString(sum[:])
}
func (p *Provider) Policy(store *db.Store) *game.EnrollmentPolicy {
	if p == nil {
		return nil
	}
	return &game.EnrollmentPolicy{GameID: p.manifest.GameID, Authorize: func(req game.EnrollmentRequest) (game.EnrollmentAuthorization, error) {
		if req.GameID != p.manifest.GameID || p.CheckStore(store) != nil {
			return game.EnrollmentAuthorization{}, ErrAuthority
		}
		var account Account
		found := false
		for id, ref := range p.refs {
			if ref == req.CallerReference {
				account = p.accounts[id]
				found = true
				break
			}
		}
		if !found {
			return game.EnrollmentAuthorization{}, ErrAuthority
		}
		allowGod := false
		if req.God && account.GodBootstrap {
			// Historical first-God retries remain recognizable after disabling bootstrap.
			rec, e := store.GetPlacement(account.AccountID, req.RequestID)
			if e != nil {
				return game.EnrollmentAuthorization{}, ErrAuthority
			}
			if rec != nil && rec.Player == 1 {
				allowGod = true
			} else if p.manifest.BootstrapEnabled {
				admission, e := store.EnrollmentAdmission(account.AccountID)
				if e != nil {
					return game.EnrollmentAuthorization{}, ErrAuthority
				}
				allowGod = admission.NextPlayer == 1
			}
		}
		return game.EnrollmentAuthorization{CallerID: account.AccountID, GameID: p.manifest.GameID, RaceCredentialRef: credentialRef(p.manifest.GameID, account.AccountID, "race", account.RaceHash), GovernorCredentialRef: credentialRef(p.manifest.GameID, account.AccountID, "governor", account.GovernorHash), AllowGod: allowGod, AllowGuest: account.AllowGuest}, nil
	}, Prepare: func(auth game.EnrollmentAuthorization) (game.EnrollmentCredentials, error) {
		var a Account
		ok := false
		for _, candidate := range p.accounts {
			if candidate.AccountID == auth.CallerID && auth.RaceCredentialRef == credentialRef(p.manifest.GameID, candidate.AccountID, "race", candidate.RaceHash) && auth.GovernorCredentialRef == credentialRef(p.manifest.GameID, candidate.AccountID, "governor", candidate.GovernorHash) {
				a = candidate
				ok = true
				break
			}
		}
		if !ok || auth.GameID != p.manifest.GameID || auth.RaceCredentialRef != credentialRef(p.manifest.GameID, a.AccountID, "race", a.RaceHash) || auth.GovernorCredentialRef != credentialRef(p.manifest.GameID, a.AccountID, "governor", a.GovernorHash) {
			return game.EnrollmentCredentials{}, ErrAuthority
		}
		return game.EnrollmentCredentials{RaceHash: a.RaceHash, GovernorHash: a.GovernorHash}, nil
	}}
}
func passwordOK(s string) bool {
	return utf8.ValidString(s) && len(s) >= 12 && len(s) <= 72 && !strings.ContainsAny(s, "\x00\r\n")
}

// Provision rejects equal plaintext before independently hashing. Output must be
// written to a protected artifact, never an HTTP response or console diagnostic.
func Provision(account, racePassword, governorPassword string) (Account, error) {
	return provision(account, racePassword, governorPassword, bcrypt.GenerateFromPassword)
}
func provision(account, racePassword, governorPassword string, hash func([]byte, int) ([]byte, error)) (Account, error) {
	if !enrollmentapi.ID(account, 128) || !passwordOK(racePassword) || !passwordOK(governorPassword) || racePassword == governorPassword {
		return Account{}, ErrCredentials
	}
	r, e := hash([]byte(racePassword), bcrypt.DefaultCost)
	if e != nil {
		return Account{}, ErrCredentials
	}
	g, e := hash([]byte(governorPassword), bcrypt.DefaultCost)
	if e != nil {
		return Account{}, ErrCredentials
	}
	a := Account{AccountID: account, RaceHash: string(r), GovernorHash: string(g)}
	a.CredentialVersion = Version(a)
	return a, nil
}

// WriteAccount creates an owner-only artifact without overwriting prior versions.
func WriteAccount(path string, a Account) error {
	data, e := json.MarshalIndent(a, "", "  ")
	if e != nil {
		return ErrAuthority
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) //nolint:gosec // explicitly selected output, exclusive creation, owner-only mode
	if e != nil {
		return ErrAuthority
	}
	_, writeErr := f.Write(append(data, '\n'))
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return ErrAuthority
	}
	return nil
}

// GameID binds operator authority to the immutable universe initialization
// record rather than a rebindable deployment nickname. It survives turns.
func GameID(record *db.UniverseRecord) string {
	if record == nil {
		return ""
	}
	raw, e := json.Marshal(record)
	if e != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "universe-" + hex.EncodeToString(sum[:])
}
func (p *Provider) CheckStore(store *db.Store) error {
	if p == nil || store == nil {
		return ErrAuthority
	}
	record, e := store.GetUniverseRecord()
	if e != nil || record == nil || p.manifest.GameID != GameID(record) {
		return ErrAuthority
	}
	return nil
}
