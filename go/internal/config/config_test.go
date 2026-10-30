package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	goodSecret = "0123456789abcdef0123456789abcdef-jwt"
	goodToken  = "admin-token-0123456789"
)

func baseEnv() map[string]string {
	return map[string]string{"GB_JWT_SECRET": goodSecret, "GB_ADMIN_TOKEN": goodToken}
}

func TestDefaultHasNoSecrets(t *testing.T) {
	d := Default()
	if d.JWTSecret != "" || d.AdminToken != "" {
		t.Fatalf("Default must not carry secrets (CR-35): jwt=%q admin=%q", d.JWTSecret, d.AdminToken)
	}
	if err := d.Validate(); err == nil {
		t.Fatal("Default() must not validate without secrets")
	}
}

func TestRefusesMissingAndInsecureJWTSecret(t *testing.T) {
	cases := map[string]string{
		"missing":          "",
		"old default":      "change-me-in-production",
		"short":            "tooshort",
		"repeated":         strings.Repeat("a", 40),
		"placeholder case": "ChangeMe",
	}
	for name, v := range cases {
		env := baseEnv()
		if v == "" {
			delete(env, "GB_JWT_SECRET")
		} else {
			env["GB_JWT_SECRET"] = v
		}
		_, _, err := Load("", MapEnv(env))
		if err == nil {
			t.Errorf("%s: expected refusal", name)
			continue
		}
		if !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: error %v is not ErrInvalidConfig", name, err)
		}
		if v != "" && strings.Contains(err.Error(), v) {
			t.Errorf("%s: error text leaks the secret value: %v", name, err)
		}
	}
}

func TestRefusesMissingAdminTokenWhenAdminEnabled(t *testing.T) {
	env := baseEnv()
	delete(env, "GB_ADMIN_TOKEN")
	if _, _, err := Load("", MapEnv(env)); err == nil || !strings.Contains(err.Error(), "admin token") {
		t.Fatalf("expected admin-token refusal, got %v", err)
	}
	// Disabling the listener removes the requirement.
	env["GB_ADMIN_ADDR"] = ""
	cfg, _, err := Load("", MapEnv(env))
	if err != nil {
		t.Fatalf("admin disabled should load: %v", err)
	}
	if cfg.AdminAddr != "" {
		t.Fatalf("AdminAddr = %q, want empty", cfg.AdminAddr)
	}
}

func TestAdminTokenMustDifferFromJWTSecret(t *testing.T) {
	env := baseEnv()
	env["GB_ADMIN_TOKEN"] = goodSecret
	if _, _, err := Load("", MapEnv(env)); err == nil || !strings.Contains(err.Error(), "differ") {
		t.Fatalf("expected refusal, got %v", err)
	}
}

func TestEnvOverridesFileOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "gb.json")
	if err := os.WriteFile(file, []byte(`{"port": 9000, "db_path": "/from/file.db", "default_update_minutes": 45, "moves_per_update": 2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := baseEnv()
	env["GB_CONFIG"] = file
	env["GB_DB_PATH"] = "/from/env.db"
	env["GB_ADDR"] = "0.0.0.0:7000"
	env["GB_UPDATE_MINUTES"] = "15"
	cfg, warnings, err := Load("", MapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings %v", warnings)
	}
	if cfg.DBPath != "/from/env.db" {
		t.Errorf("env must win over file for db_path: %q", cfg.DBPath)
	}
	if cfg.Host != "0.0.0.0" || cfg.Port != 7000 {
		t.Errorf("GB_ADDR not applied: %s:%d", cfg.Host, cfg.Port)
	}
	if cfg.DefaultUpdateMinutes != 15 {
		t.Errorf("GB_UPDATE_MINUTES not applied: %d", cfg.DefaultUpdateMinutes)
	}
	if cfg.MovesPerUpdate != 2 {
		t.Errorf("file value for moves_per_update lost: %d", cfg.MovesPerUpdate)
	}
	if cfg.Market != true {
		t.Errorf("defaults not preserved under file")
	}
	// -config flag wins over GB_CONFIG.
	other := filepath.Join(dir, "other.json")
	if err := os.WriteFile(other, []byte(`{"moves_per_update": 4}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = Load(other, MapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MovesPerUpdate != 4 {
		t.Errorf("-config flag must win over GB_CONFIG: %d", cfg.MovesPerUpdate)
	}
}

func TestFileSecretsAreRejected(t *testing.T) {
	dir := t.TempDir()
	for _, key := range []string{"jwt_secret", "update_password", "external_trigger"} {
		file := filepath.Join(dir, key+".json")
		if err := os.WriteFile(file, []byte(`{"`+key+`": "x"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := Load(file, MapEnv(baseEnv()))
		if err == nil || !errors.Is(err, ErrConfigFile) || !strings.Contains(err.Error(), key) {
			t.Errorf("%s: expected config-file rejection naming the key, got %v", key, err)
		}
	}
	// Unknown keys are rejected too.
	file := filepath.Join(dir, "typo.json")
	if err := os.WriteFile(file, []byte(`{"db_pathh": "x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(file, MapEnv(baseEnv())); err == nil {
		t.Error("unknown key must be rejected")
	}
}

func TestSecretFiles(t *testing.T) {
	dir := t.TempDir()
	sf := filepath.Join(dir, "jwt")
	if err := os.WriteFile(sf, []byte(goodSecret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := baseEnv()
	delete(env, "GB_JWT_SECRET")
	env["GB_JWT_SECRET_FILE"] = sf
	cfg, _, err := Load("", MapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JWTSecret != goodSecret {
		t.Errorf("secret file not read (trailing newline must be stripped)")
	}
	env["GB_JWT_SECRET"] = goodSecret
	if _, _, err := Load("", MapEnv(env)); err == nil {
		t.Error("both GB_JWT_SECRET and GB_JWT_SECRET_FILE set must be refused")
	}
}

func TestDeprecatedDataPathAlias(t *testing.T) {
	dir := t.TempDir()
	env := baseEnv()
	env["GB_DATA_PATH"] = dir
	cfg, warnings, err := Load("", MapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBPath != filepath.Join(dir, "gb.db") {
		t.Errorf("directory alias should append gb.db: %q", cfg.DBPath)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "deprecated") {
		t.Errorf("expected one deprecation warning, got %v", warnings)
	}
	env["GB_DB_PATH"] = "/explicit.db"
	cfg, warnings, err = Load("", MapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBPath != "/explicit.db" || len(warnings) != 0 {
		t.Errorf("GB_DB_PATH must take precedence over the alias: %q %v", cfg.DBPath, warnings)
	}
}

func TestBadValues(t *testing.T) {
	for key, v := range map[string]string{
		"GB_ADDR":                 "nocolon",
		"GB_UPDATE_MINUTES":       "ten",
		"GB_MOVES_PER_UPDATE":     "0",
		"GB_LOG_LEVEL":            "loud",
		"GB_WS_MAX_MESSAGE_BYTES": "10",
		"GB_WS_ALLOWED_ORIGINS":   "gb.example.org",
		"GB_ENROLL_PREVIEW":       "maybe",
		"GB_ADMIN_ADDR":           "8081",
	} {
		env := baseEnv()
		env[key] = v
		if _, _, err := Load("", MapEnv(env)); err == nil {
			t.Errorf("%s=%q must be refused", key, v)
		}
	}
}

func TestOriginsAndPreview(t *testing.T) {
	env := baseEnv()
	env["GB_WS_ALLOWED_ORIGINS"] = "https://a.example, http://localhost:5173"
	env["GB_ENROLL_PREVIEW"] = "true"
	cfg, _, err := Load("", MapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.WSAllowedOrigins) != 2 || cfg.WSAllowedOrigins[1] != "http://localhost:5173" {
		t.Errorf("origins = %v", cfg.WSAllowedOrigins)
	}
	if !cfg.EnrollPreview {
		t.Error("GB_ENROLL_PREVIEW=true not applied")
	}
	if Default().EnrollPreview {
		t.Error("enrollment preview must be off by default (Decision 9)")
	}
}
