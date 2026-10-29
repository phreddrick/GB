# Galactic Bloodshed - Go Port

A port of the classic multi-player space strategy game **Galactic Bloodshed** to Go, with WebSocket networking and an embedded (bbolt) database.

> **Status (2026-10-28): incomplete and not playable.** This port builds and starts, but an independent review found that it cannot run a correct game: enrolled races have no home planet, ground assault never captures, combat damage is lost between turns, the admin update endpoint crashes the process, and the Kubernetes manifest in this repository would wipe the universe on every restart. 45 of the 109 original commands have no Go equivalent. See **`docs/architecture/go-code-review.md`** (findings `CR-nn`) and the modernization plan **`docs/architecture/microservices-migration-plan.md`** before relying on anything below. The earlier notes that described the port as finished and deployable are archived under `docs/archive/earlier-model-notes/` and are superseded.

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
make makeuniv     # Universe generator
make racegen      # Player enrollment
```

### Running

#### Start the Universe Generator (first time only)

```bash
./makeuniv
```

This creates the initial game data files with procedurally-generated stars and planets.

#### Start the Game Server

```bash
./gb-server
```

The server listens on the configured port (default: 8080) and accepts WebSocket connections.

#### Connect a Client

```bash
./gb-client
```

The client connects to the server via WebSocket and provides the game interface.

#### Register New Players

```bash
./racegen
```

Enroll new players and create races.

## Project Structure

```
go/
├── cmd/
│   ├── gb-server/       Main server entry point
│   ├── gb-client/       Client entry point
│   ├── makeuniv/        Universe generator
│   └── racegen/         Player enrollment utility
├── internal/
│   ├── config/          Runtime configuration (JSON-based)
│   ├── db/              Persistence layer (bbolt key-value store)
│   ├── game/            Game logic (state, commands, combat, turns)
│   ├── model/           Data structures (Race, Star, Planet, Ship, etc.)
│   └── net/             Network layer (WebSocket server, sessions)
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

The server reads a JSON configuration file (default: `config.json`):

```json
{
  "port": 8080,
  "debug": false,
  "features": {
    "market": true,
    "victory": true,
    "defense": true,
    "voting": false
  },
  "game": {
    "turns_per_update": 10,
    "movement_segments": 5,
    "initial_tech": 5
  }
}
```

See `internal/config/config.go` for the full configuration structure.

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

- **Builds, compiles cleanly, `go vet` clean.** Phase 0 of the migration plan added CI, golden parity tests against the C reference, and a known-defect inventory; see `docs/testing/`.
- **Command coverage:** 62 verbs are dispatched; the C server has 109 reachable commands. The 45 missing ones include governors, money, power blocks, most reports, `assault`, `undock`, `repair` and the operator commands (review section 4.1). The dispatcher's prefix order also differs from C (`c` fires a CEW instead of changing scope, CR-40).
- **Known-wrong game logic:** planetary guns (CR-26), CEW damage (CR-27), assault capture (CR-19), turn order and missing turn steps (CR-10 to CR-16), enrollment placement (CR-07), plus GD-01 and GD-02 found by the golden tests (`docs/testing/known-defects.md`).
- **Persistence:** two sources of truth and non-atomic flushes (CR-11, CR-17, CR-18); a crash mid-flush can leave a mixed-turn database.
- **Networking:** output is C-formatted text in WebSocket frames; there is **no structured JSON protocol** yet, the prompt is never sent (CR-42), and the default JWT secret is accepted (CR-35).
- **Kubernetes:** `../KUBERNETES.md`, `../k8s-deployment.yaml` and the root `Dockerfile` are not usable as they stand (CR-01 to CR-06); the plan replaces them in Phase 2.
- **Tests:** the golden parity suite (`internal/game/golden_test.go`, fixtures from `test/ctool`; ship-design rows cover `shipSize`, `getMass`, `shipCostFromDesign` and `complexityOf` only, not `getshipDefaults` or the whole `Shipdata` table), the CR-27 call-site test, and the tool tests under `tools/`. There are no behavioural, persistence or network tests yet (plan Phase 1 onward).

Refer to the C source under `source/` as the behavioural reference and to the plan's phases for what comes next. Do not add features before Phase 1's stabilisation work.

## Troubleshooting

### Port Already in Use

If the server fails to start with "address already in use", either:
1. Kill the existing server: `pkill -f gb-server`
2. Change the port in `config.json` and restart

### Database Corruption

If you encounter bbolt errors, delete the database file and re-run `makeuniv`:

```bash
rm -f data/*.db
./makeuniv
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
