package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Configuration precedence (lowest to highest):
//
//  1. Default() (tweakables.h values and safe operational defaults).
//  2. The JSON config file named by GB_CONFIG, or by the -config flag, which
//     wins over GB_CONFIG when both are set. Secrets are never read from the
//     file: Config.JWTSecret and Config.AdminToken carry json:"-".
//  3. Environment variables (table below), which override the file.
//
// Validation (validate.go) runs after all three layers and before the
// database is opened; a configuration that fails validation never touches
// the store.
//
// Environment variables:
//
//	GB_ADDR                 host:port of the player listener (default localhost:8080)
//	GB_ADMIN_ADDR           host:port of the admin listener (default 127.0.0.1:8081; empty disables it)
//	GB_DB_PATH              bbolt database file (default ./gb.db); GB_DATA_PATH is a deprecated alias
//	                        (a directory value gets /gb.db appended, matching the old manifest)
//	GB_JWT_SECRET           JWT signing secret (required; or GB_JWT_SECRET_FILE)
//	GB_ADMIN_TOKEN          static admin bearer token (required when the admin listener is enabled;
//	                        or GB_ADMIN_TOKEN_FILE)
//	GB_CONFIG               path of the JSON config file
//	GB_UPDATE_MINUTES       minutes between full updates
//	GB_MOVES_PER_UPDATE     movement segments per update
//	GB_LOG_LEVEL            debug|info|warn|error
//	GB_SHUTDOWN_TIMEOUT     orderly shutdown budget in seconds
//	GB_TURN_STALL_TIMEOUT   liveness: seconds without turn progress before the actor is considered stuck
//	GB_WS_ALLOWED_ORIGINS   comma-separated browser origins allowed to open WebSockets (empty = same-origin only)
//	GB_WS_MAX_MESSAGE_BYTES per-frame read limit
//	GB_ENROLL_PREVIEW       "true" enables the incomplete enrollment preview (CR-07); default off
//	GB_DOCS_DIR             help-file directory
//	GB_TLS_CERT, GB_TLS_KEY TLS certificate and key for the player listener

// ErrConfigFile wraps errors reading or parsing the JSON config file.
var ErrConfigFile = errors.New("config file")

// LookupEnv is the environment accessor Load uses: os.LookupEnv for the real
// process, or a map-backed function in tests. A variable that is present
// but empty is "set to empty", which matters for GB_ADMIN_ADDR (disables the
// admin listener) and GB_WS_ALLOWED_ORIGINS (clears the list).
type LookupEnv func(key string) (string, bool)

// MapEnv returns a LookupEnv backed by m (for tests).
func MapEnv(m map[string]string) LookupEnv {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// Load builds the configuration from defaults, the optional JSON file and the
// environment, in that order, then validates it. flagConfigPath is the value
// of the -config flag ("" when not given). The returned warnings describe
// deprecated settings that were honoured.
func Load(flagConfigPath string, getenv LookupEnv) (*Config, []string, error) {
	cfg := Default()
	var warnings []string

	path := flagConfigPath
	if path == "" {
		path, _ = getenv("GB_CONFIG")
	}
	if path != "" {
		if err := loadFile(cfg, path); err != nil {
			return nil, nil, err
		}
	}

	w, err := applyEnv(cfg, getenv)
	warnings = append(warnings, w...)
	if err != nil {
		return nil, nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	return cfg, warnings, nil
}

// loadFile decodes a JSON config file over cfg. Unknown fields are rejected
// so that a mistyped key cannot silently keep a default; the removed
// pre-Phase-1 keys (jwt_secret, update_password, segment_password,
// external_trigger) are reported with the reason they are no longer accepted.
func loadFile(cfg *Config, path string) error {
	f, err := os.Open(path) //nolint:gosec // G304: the operator names the config file on purpose
	if err != nil {
		return fmt.Errorf("%w %s: %w", ErrConfigFile, path, err)
	}
	defer func() { _ = f.Close() }()

	// First pass: detect removed keys so the error is actionable.
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(f).Decode(&raw); err != nil {
		return fmt.Errorf("%w %s: %w", ErrConfigFile, path, err)
	}
	for _, k := range []string{"jwt_secret", "admin_token", "update_password", "segment_password", "external_trigger"} {
		if _, ok := raw[k]; ok {
			return fmt.Errorf("%w %s: key %q is no longer accepted in the config file: secrets come from the environment (GB_JWT_SECRET, GB_ADMIN_TOKEN) and the password-triggered update endpoints were removed (CR-04, CR-36)", ErrConfigFile, path, k)
		}
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("%w %s: %w", ErrConfigFile, path, err)
	}
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return fmt.Errorf("%w %s: %w", ErrConfigFile, path, err)
	}
	return nil
}

// applyEnv overrides cfg from the environment. It returns warnings for
// deprecated variables that were honoured.
func applyEnv(cfg *Config, getenv LookupEnv) ([]string, error) {
	var warnings []string
	var errs []error

	str := func(key string, dst *string) {
		if v, ok := lookup(getenv, key); ok {
			*dst = v
		}
	}
	integer := func(key string, dst *int) {
		if v, ok := lookup(getenv, key); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %q is not an integer", key, v))
				return
			}
			*dst = n
		}
	}
	boolean := func(key string, dst *bool) {
		if v, ok := lookup(getenv, key); ok {
			b, err := strconv.ParseBool(strings.TrimSpace(v))
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %q is not a boolean", key, v))
				return
			}
			*dst = b
		}
	}

	if v, ok := lookup(getenv, "GB_ADDR"); ok {
		host, port, err := splitHostPort(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("GB_ADDR: %w", err))
		} else {
			cfg.Host, cfg.Port = host, port
		}
	}
	// GB_ADMIN_ADDR set to the empty string disables the admin listener;
	// unset keeps the default.
	if v, ok := lookupAllowEmpty(getenv, "GB_ADMIN_ADDR"); ok {
		cfg.AdminAddr = strings.TrimSpace(v)
	}

	if v, ok := lookup(getenv, "GB_DB_PATH"); ok {
		cfg.DBPath = v
	} else if v, ok := lookup(getenv, "GB_DATA_PATH"); ok {
		// Deprecated alias used by the pre-Phase-1 manifest (CR-06, CR-34).
		if st, err := os.Stat(v); err == nil && st.IsDir() {
			cfg.DBPath = strings.TrimRight(v, "/") + "/gb.db"
		} else {
			cfg.DBPath = v
		}
		warnings = append(warnings, fmt.Sprintf("GB_DATA_PATH is deprecated; using database path %s (set GB_DB_PATH instead)", cfg.DBPath))
	}

	secret, err := secretFromEnv(getenv, "GB_JWT_SECRET")
	if err != nil {
		errs = append(errs, err)
	} else if secret != "" {
		cfg.JWTSecret = secret
	}
	token, err := secretFromEnv(getenv, "GB_ADMIN_TOKEN")
	if err != nil {
		errs = append(errs, err)
	} else if token != "" {
		cfg.AdminToken = token
	}

	integer("GB_UPDATE_MINUTES", &cfg.DefaultUpdateMinutes)
	integer("GB_MOVES_PER_UPDATE", &cfg.MovesPerUpdate)
	str("GB_LOG_LEVEL", &cfg.LogLevel)
	integer("GB_SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeoutSeconds)
	integer("GB_TURN_STALL_TIMEOUT", &cfg.TurnStallTimeoutSecs)
	if v, ok := lookupAllowEmpty(getenv, "GB_WS_ALLOWED_ORIGINS"); ok {
		cfg.WSAllowedOrigins = nil
		for _, o := range strings.Split(v, ",") {
			if o = strings.TrimSpace(o); o != "" {
				cfg.WSAllowedOrigins = append(cfg.WSAllowedOrigins, o)
			}
		}
	}
	if v, ok := lookup(getenv, "GB_WS_MAX_MESSAGE_BYTES"); ok {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("GB_WS_MAX_MESSAGE_BYTES: %q is not an integer", v))
		} else {
			cfg.WSMaxMessageBytes = n
		}
	}
	boolean("GB_ENROLL_PREVIEW", &cfg.EnrollPreview)
	str("GB_DOCS_DIR", &cfg.DocsDir)
	str("GB_TLS_CERT", &cfg.TLSCert)
	str("GB_TLS_KEY", &cfg.TLSKey)

	return warnings, errors.Join(errs...)
}

// secretFromEnv reads KEY, or the first line of the file named by KEY_FILE.
// Setting both is an error (ambiguous). The value is trimmed of a trailing
// newline only, so that a secret with deliberate leading spaces survives.
func secretFromEnv(getenv LookupEnv, key string) (string, error) {
	direct, hasDirect := lookup(getenv, key)
	file, hasFile := lookup(getenv, key+"_FILE")
	switch {
	case hasDirect && hasFile:
		return "", fmt.Errorf("%s and %s_FILE are both set; set exactly one", key, key)
	case hasDirect:
		return direct, nil
	case hasFile:
		data, err := os.ReadFile(file) //nolint:gosec // G304: the operator names the secret file on purpose
		if err != nil {
			return "", fmt.Errorf("%s_FILE: %w", key, err)
		}
		s := strings.TrimRight(string(data), "\r\n")
		if s == "" {
			return "", fmt.Errorf("%s_FILE %s is empty", key, file)
		}
		return s, nil
	}
	return "", nil
}

// lookup returns a variable that is set and non-empty.
func lookup(getenv LookupEnv, key string) (string, bool) {
	v, ok := getenv(key)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// lookupAllowEmpty returns a variable that is set, even to the empty string.
func lookupAllowEmpty(getenv LookupEnv, key string) (string, bool) {
	return getenv(key)
}

func splitHostPort(v string) (string, int, error) {
	v = strings.TrimSpace(v)
	i := strings.LastIndex(v, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("%q must be host:port", v)
	}
	host := strings.Trim(v[:i], "[]")
	port, err := strconv.Atoi(v[i+1:])
	if err != nil || port < 0 || port > 65535 {
		return "", 0, fmt.Errorf("%q has an invalid port", v)
	}
	return host, port, nil
}
