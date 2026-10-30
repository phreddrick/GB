package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// Secret policy (CR-35, CR-36, ADR-0004).
const (
	// MinJWTSecretLen is the minimum length in bytes of the JWT signing
	// secret. HS256 keys shorter than the hash output weaken the MAC.
	MinJWTSecretLen = 32
	// MinAdminTokenLen is the minimum length of the static admin token.
	MinAdminTokenLen = 16
)

// insecureSecrets are values that must never be accepted, whatever their
// length: the pre-Phase-1 default and the usual placeholders.
var insecureSecrets = map[string]bool{
	"change-me-in-production": true,
	"change-me":               true,
	"changeme":                true,
	"secret":                  true,
	"password":                true,
	"admin":                   true,
	"token":                   true,
	"test":                    true,
	"dev":                     true,
	"development":             true,
	"example":                 true,
	"xxx":                     true,
}

// ErrInvalidConfig is wrapped by every validation error.
var ErrInvalidConfig = errors.New("invalid configuration")

// Validate checks the configuration for values that must stop the server
// before it opens the database. All problems are reported together.
func (c *Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if strings.TrimSpace(c.Host) == "" {
		add("host must not be empty")
	}
	if c.Port < 0 || c.Port > 65535 { // 0 = ephemeral (tests)
		add("port %d is out of range", c.Port)
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		add("tls_cert and tls_key must be set together")
	}
	if strings.TrimSpace(c.DBPath) == "" {
		add("db_path must not be empty")
	}

	if err := CheckSecret("JWT secret (GB_JWT_SECRET)", c.JWTSecret, MinJWTSecretLen); err != nil {
		errs = append(errs, err)
	}

	if c.AdminAddr != "" {
		if _, _, err := net.SplitHostPort(c.AdminAddr); err != nil {
			add("admin_addr %q must be host:port: %v", c.AdminAddr, err)
		}
		if err := CheckSecret("admin token (GB_ADMIN_TOKEN)", c.AdminToken, MinAdminTokenLen); err != nil {
			errs = append(errs, fmt.Errorf("%w (set GB_ADMIN_ADDR to the empty string to run without the admin listener)", err))
		}
		if c.JWTSecret != "" && c.AdminToken != "" && c.JWTSecret == c.AdminToken {
			add("the admin token must differ from the JWT secret")
		}
	}

	if c.DefaultUpdateMinutes < 1 {
		add("default_update_minutes must be at least 1 (got %d)", c.DefaultUpdateMinutes)
	}
	if c.MovesPerUpdate < 1 {
		add("moves_per_update must be at least 1 (got %d)", c.MovesPerUpdate)
	}
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "warning", "error":
	default:
		add("log_level %q must be debug, info, warn or error", c.LogLevel)
	}
	if c.ShutdownTimeoutSeconds < 1 {
		add("shutdown_timeout_seconds must be at least 1 (got %d)", c.ShutdownTimeoutSeconds)
	}
	if c.TurnStallTimeoutSecs < 1 {
		add("turn_stall_timeout_seconds must be at least 1 (got %d)", c.TurnStallTimeoutSecs)
	}
	if c.WSMaxMessageBytes < 64 {
		add("ws_max_message_bytes must be at least 64 (got %d)", c.WSMaxMessageBytes)
	}
	if c.WSWriteTimeoutSeconds < 1 {
		add("ws_write_timeout_seconds must be at least 1 (got %d)", c.WSWriteTimeoutSeconds)
	}
	if c.WSOutboundQueue < 1 {
		add("ws_outbound_queue must be at least 1 (got %d)", c.WSOutboundQueue)
	}
	for _, o := range c.WSAllowedOrigins {
		if !strings.HasPrefix(o, "http://") && !strings.HasPrefix(o, "https://") {
			add("ws_allowed_origins entry %q must be a full origin such as https://gb.example.org", o)
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrInvalidConfig, errors.Join(errs...))
}

// CheckSecret applies the secret policy to one value. It never includes the
// value in the error. Validate wraps the result in ErrInvalidConfig.
func CheckSecret(what, value string, minLen int) error {
	switch {
	case value == "":
		return fmt.Errorf("%s is not set", what)
	case insecureSecrets[strings.ToLower(strings.TrimSpace(value))]:
		return fmt.Errorf("%s is a known placeholder value and is refused", what)
	case len(value) < minLen:
		return fmt.Errorf("%s is shorter than %d bytes", what, minLen)
	case strings.Count(value, string(value[0])) == len(value):
		return fmt.Errorf("%s is a single repeated character and is refused", what)
	}
	return nil
}
