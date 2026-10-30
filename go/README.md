# Galactic Bloodshed - Go Port

A port of the classic multi-player space strategy game **Galactic Bloodshed** to Go, with WebSocket networking and an embedded (bbolt) database.

> **Status (2026-10-29): safe to run locally, still not playable.** Phase 1 made the process operable: configuration and secrets from the environment with insecure defaults refused, a token-protected admin listener and `gbctl` (typed update/segment/quiesce/resume/universe-init, no more crashing `@@update`), health and readiness probes, Prometheus metrics, JSON logs, WebSocket limits, a persisted turn schedule, explicit seeded universe initialisation that never overwrites an existing world, panic isolation that halts rather than continues, and an orderly shutdown. The game logic is unchanged: enrolled races have no home planet, ground assault never captures, combat damage is lost between turns, 45 of the 109 original commands have no Go equivalent, and a crash **during** a turn flush can still leave a mixed-turn database (CR-18, Phase 3a). See **`docs/operations/local-server.md`** to run it, **`docs/architecture/go-code-review.md`** (findings `CR-nn`) and **`docs/architecture/microservices-migration-plan.md`** before relying on anything below. The earlier notes that described the port as finished and deployable are archived under `docs/archive/earlier-model-notes/` and are superseded.

## Quick Start

### Prerequisites

- **Go 1.25.0** or later
- `make` (optional, but recommended)

### Building

From the `go/` directory:

```bash
make all          # Build all binaries
# or
go build ./...    # Build all packages
```

Build individual binaries:

```bash
make gb-server    # Main game server
make gb-client    # Client application
make gbctl        # Admin command-line tool
make makeuniv     # Offline universe generator
make racegen      # Player enrollment
```

### Running

Full guide: `../docs/operations/local-server.md`.

#### Start the Game Server

```bash
export GB_JWT_SECRET="$(openssl rand -base64 48)"   # required, >= 32 bytes, no placeholders
export GB_ADMIN_TOKEN="$(openssl rand -base64 24)"  # required while the admin listener is enabled
export GB_DB_PATH=./gb.db
./gb-server
```

The server refuses to start without valid secrets, validates its configuration before opening the database, and listens on `GB_ADDR` (default `localhost:8080`, players) and `GB_ADMIN_ADDR` (default `127.0.0.1:8081`, admin). A fresh database starts in mode `UNINITIALIZED`; ordinary startup never generates a universe.

#### Initialise the Universe (first time only, explicit)

```bash
export GBCTL_TOKEN="$GB_ADMIN_TOKEN"
./gbctl universe init -seed 4242 -stars 100 -min-planets 2 -max-planets 10
./gbctl status
```

The seed is recorded; the same seed regenerates the same universe. Initialisation is refused on a database that already holds one. (`./makeuniv -db PATH -seed N` does the same offline against a database no server has open.)

#### Connect a Client

```bash
./gb-client
```

The client connects to the server via WebSocket and provides the game interface.

#### Register New Players

```bash
./racegen -db ./gb.db      # offline, with the server stopped
```

Web enrollment is disabled by default because enrolled races are not playable yet (CR-07, Phase 3c).

#### Administer

```bash
./gbctl update      # run an update now; the process survives (CR-04 fixed)
./gbctl quiesce     # pause turns and mutating commands
./gbctl resume
curl -s localhost:8080/readyz
curl -s 127.0.0.1:8081/metrics
```

## Project Structure

```
go/
├── cmd/
│   ├── gb-server/       Main server entry point (config, listeners, shutdown)
│   ├── gb-client/       Terminal client
│   ├── gbctl/           Admin command-line tool (status, update, segment, quiesce, resume, universe init)
│   ├── makeuniv/        Offline wrapper around internal/universe
│   └── racegen/         Player enrollment utility
├── internal/
│   ├── admin/           Token-protected admin HTTP listener and its client
│   ├── config/          Defaults, JSON file, environment, validation
│   ├── db/              Persistence layer (bbolt key-value store) and metadata records
│   ├── engine/          Mode, health, status types and sentinel errors shared by game, net and admin
│   ├── enroll/          Enrollment request validation
│   ├── game/            Game logic (state actor, lifecycle, admin requests, schedule, commands, combat, turns)
│   ├── model/           Data structures (Race, Star, Planet, Ship, etc.)
│   ├── net/             Player HTTP/WebSocket server, sessions, probes
│   ├── platform/        slog setup and the Prometheus text registry
│   ├── testutil/        Known-defect inventory mechanism
│   └── universe/        Seeded universe generation and initialisation
├── test/e2e/            Subprocess acceptance and signal tests
├── tools/               CI gate tools (lintdiff, growthgate, inventorycheck)
├── go.mod              Go module definition
├── go.sum              Dependency checksums
└── Makefile            Build system
```

## Architecture Overview

### Key Design Differences from C Version

| Aspect | C Original | Go Port |
|--------|-----------|---------|
| **Networking** | Telnet (raw TCP) | WebSocket (HTTP upgrade) |
| **Database** | Binary flat-files | bbolt embedded key-value store (ACID) |
| **Config** | Compile-time `#define` flags | Runtime JSON config file |
| **Authentication** | Session-based | JWT tokens |
| **Concurrency** | Single-threaded event loop | Go goroutines + bbolt transactions |
| **Command Dispatch** | C function pointers | Go methods on `State` type |

### Core Packages

**`config`** — Configuration management
- Loads JSON config file
- Provides access to game tuning constants, feature flags, port settings

**`model`** — Game data structures
- `Race`, `Star`, `Planet`, `Ship`, `Power`, `Sector`, etc.
- Mirrors the C `struct` types from `vars.h`, `ships.h`, `races.h`

**`db`** — Data persistence
- `Store` interface abstracts data access
- bbolt implementation using buckets (collections) for each entity type
- JSON serialization of models
- ACID transactions for consistency

**`game`** — Game logic
- `State` type holds the current game state
- Methods for turn processing, command handling, combat, build actions
- Implements game rules: production, navigation, combat resolution, victory conditions

**`net`** — Network layer
- WebSocket server using gorilla/websocket
- Session registry and authentication (JWT)
- Connection lifecycle management
- Message routing to game command handlers

## Configuration

Defaults, then an optional JSON file (`-config` or `GB_CONFIG`, flat keys such as `"default_update_minutes": 30`, `"moves_per_update": 3`, `"market": true`), then environment variables (`GB_ADDR`, `GB_ADMIN_ADDR`, `GB_DB_PATH`, `GB_JWT_SECRET`, `GB_ADMIN_TOKEN`, `GB_UPDATE_MINUTES`, ...). Secrets are accepted from the environment only. See `internal/config/env.go` for the table and `../docs/operations/local-server.md` for the operational meaning.

## Development

### Running Tests

```bash
make test
# or
go test ./...
```

### Watching for Changes

Install and use a file watcher like `air`:

```bash
go install github.com/cosmtrek/air@latest
air
```

### Building with Optimizations

```bash
go build -ldflags="-s -w" ./cmd/gb-server
```

## Implementation Status

The honest status is in `docs/architecture/go-code-review.md` (sections 1, 4 and 5). In short:

- **Builds, compiles cleanly, `go vet` clean.** Phase 0 added CI, golden parity tests against the C reference and a known-defect inventory; Phase 1 added the lifecycle, configuration, admin, health, logging, metrics and WebSocket-safety work with behavioural tests (`internal/game`, `internal/net`, `internal/admin`, `internal/config`, `internal/universe`, `test/e2e`); see `docs/testing/phase1-validation.md`.
- **Command coverage:** 62 verbs are dispatched; the C server has 109 reachable commands. The 45 missing ones include governors, money, power blocks, most reports, `assault`, `undock`, `repair` and the operator commands (review section 4.1). The dispatcher's prefix order also differs from C (`c` fires a CEW instead of changing scope, CR-40).
- **Known-wrong game logic:** planetary guns (CR-26), CEW damage (CR-27), assault capture (CR-19), turn order and missing turn steps (CR-10 to CR-16), enrollment placement (CR-07), plus GD-01 and GD-02 found by the golden tests (`docs/testing/known-defects.md`).
- **Persistence:** two sources of truth and non-atomic flushes (CR-11, CR-17, CR-18); an orderly shutdown now preserves the universe and the schedule, but a crash or kill mid-flush can still leave a mixed-turn database (inventory entry `persistence/non-atomic-turn-flush`, Phase 3a).
- **Networking:** output is C-formatted text in WebSocket frames; there is **no structured JSON protocol** yet and the prompt is never sent (CR-42). Frame size, origin, write deadlines and slow-client handling are enforced; tokens are still accepted in the upgrade URL.
- **Operations:** environment configuration with refused insecure defaults (CR-06, CR-34, CR-35, CR-36), typed admin operations through `gbctl` (CR-04), `/livez` and `/readyz` (CR-02), enrollment through the actor and disabled by default (CR-03, CR-07), explicit seeded universe initialisation (CR-05), schedule restore (CR-21), panic isolation that halts (CR-22 interim), orderly shutdown (CR-24).
- **Kubernetes:** `../KUBERNETES.md`, `../k8s-deployment.yaml` and the root `Dockerfile` are not usable as they stand (CR-01 to CR-06); the plan replaces them in Phase 2.
- **Tests:** the golden parity suite (`internal/game/golden_test.go`, fixtures from `test/ctool`; ship-design rows cover `shipSize`, `getMass`, `shipCostFromDesign` and `complexityOf` only, not `getshipDefaults` or the whole `Shipdata` table), the CR-27 call-site test, the Phase 1 lifecycle, transport, admin, configuration and universe tests, the subprocess tests under `test/e2e`, and the tool tests under `tools/`. Game-rule behaviour (turn order, combat, enrollment placement) is still untested beyond the golden formulas.

Refer to the C source under `source/` as the behavioural reference and to the plan's phases for what comes next. Do not add gameplay features before the phase that authorises them.

## Troubleshooting

### Port Already in Use

If the server fails to start with "address already in use", either:
1. Kill the existing server: `pkill -f gb-server`
2. Change `GB_ADDR` / `GB_ADMIN_ADDR` and restart

### Server refuses to start

Exit status 1 with a message naming the setting: fix `GB_JWT_SECRET` / `GB_ADMIN_TOKEN` (length and placeholder rules in `docs/operations/local-server.md`) or the config-file key it names. Nothing is created until the configuration is valid.

### Server is HALTED

`gbctl status` shows `HALTED` and a `halt_reason` after a recovered panic; restart the process (it reloads the store). In-memory changes since the last flush are lost; see the operations guide, sections 9 and 10.

### Database Corruption (disposable development data only)

If you encounter bbolt errors on a throwaway database, stop the server, delete the file and initialise again:

```bash
rm -f ./gb.db
./gb-server &
./gbctl universe init -seed 4242
```

### Connection Refused

Verify the server is running and listening:

```bash
netstat -tulpn | grep :8080
# or
ss -tulpn | grep :8080
```

## Resources

- **Game Logic**: See `source/` directory for the original C implementation
- **Protocol**: currently raw text frames over WebSocket (a structured JSON API is planned, see the migration plan section 6)
- **Database Schema**: See `internal/db/keys.go` for bucket names and key structures

## Contributing

Refer to `CLAUDE.md` in the root directory for architecture notes, and to `docs/architecture/microservices-migration-plan.md` for the phase currently authorised for implementation. Run `make -C test/ctool check` and `go test ./...` before proposing a change.
