# ADR-0004: Configuration and secrets through the environment; refuse insecure defaults

Status: Accepted 2026-10-29 (implemented in Phase 1: `go/internal/config/{env,validate}.go`, `go/cmd/gb-server/main.go`, `docs/operations/local-server.md`)

## Context

The pre-Phase-1 server read one JSON file for everything (balance constants, listener address, database path) and started happily with `jwt_secret: "change-me-in-production"` baked into `Default()` (CR-35). Secrets lived next to tuning constants in that file (CR-36), the deployed container ignored the data-path variable the manifest set and opened an empty database in its working directory (CR-06, CR-34), and the update endpoints were protected by passwords in the same file and crashed the process when used (CR-04). A Kubernetes Secret could not inject anything without templating the whole configuration.

## Decision

1. **Three layers, fixed precedence.** Built-in defaults, then the JSON file (`-config` flag, else `GB_CONFIG`), then environment variables. The environment wins so that a deployment can override a shipped file without editing it.
2. **Secrets come only from the environment.** `GB_JWT_SECRET` and `GB_ADMIN_TOKEN`, or `*_FILE` variants naming a file whose first line is the secret (for mounted Secrets). The config struct marks both `json:"-"`; the file loader rejects the keys `jwt_secret`, `admin_token`, `update_password`, `segment_password` and `external_trigger` with an error that explains why, and rejects unknown keys so a typo cannot silently keep a default.
3. **No insecure default is preserved.** `Default()` carries no secret. Validation refuses a missing secret, a value shorter than 32 bytes (JWT) or 16 characters (admin token), a known placeholder (`change-me-in-production`, `secret`, `password`, ...) and a single repeated character, and refuses an admin token equal to the JWT secret. The admin listener is on by default (`127.0.0.1:8081`) and requires the token; disabling it (`GB_ADMIN_ADDR=""`) removes the requirement and, with it, the ability to trigger updates or initialise the universe through the process.
4. **Validate before touching anything.** `config.Load` validates the merged configuration before the database is opened or any listener is bound; a failure exits with status 1 and creates no file. All problems are reported together.
5. **Secrets never leave the process.** They are not logged (startup logs the addresses, paths and intervals only), not included in `/status`, `/livez`, `/readyz`, `/metrics`, admin replies or error messages, and `gbctl` never accepts a token as a command-line argument or prints one.
6. **Compatibility.** `GB_DATA_PATH` (the old manifest's variable) is honoured as a deprecated alias of `GB_DB_PATH` with a warning, so the Phase 2 manifest can migrate without a flag day; the rules configuration in the JSON file keeps its keys.

## Alternatives considered

- Keep the JSON file as the only source and document "change the secret": rejected; it is what produced CR-35, and Kubernetes Secrets cannot populate it.
- Command-line flags for secrets: rejected; they appear in process listings and shell history.
- A configuration library (viper and the like): rejected; the mapping is thirty lines and adds no dependency.
- Generating a random secret at startup when none is given: rejected; tokens would silently stop working on every restart and the operator would not learn that the deployment is misconfigured.

## Consequences

- A fresh checkout does not start until two secrets are set; the operations guide shows how to generate them. This is intended.
- The config file is no longer a complete description of a deployment; the environment is part of it and must be captured by the Phase 2 manifests (Secret `gb-server-secrets`).
- The static admin token is transitional (Phase 4b replaces it with admin-role tokens); the listener separation (player versus admin) is what the later design keeps.

## Validation

`go test ./internal/config` (precedence, refusals, no secret in error text, file-key rejection, secret files, alias); `go/test/e2e` `TestStartupRefusesInsecureConfig` (the real binary exits 1 and creates no database for a missing, placeholder or absent-token configuration); `TestAcceptanceScenario` (no secret appears in any log line).

## References

`docs/architecture/go-code-review.md` CR-04, CR-06, CR-34, CR-35, CR-36; migration plan 14.1 and 16.4 (Phase 1); `docs/operations/local-server.md`.
