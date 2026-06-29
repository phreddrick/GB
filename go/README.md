# Galactic Bloodshed - Go Port

A modernized port of the classic multi-player space strategy game **Galactic Bloodshed** to Go, featuring WebSocket networking, embedded database, and idiomatic Go architecture.

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

The Go port is substantially complete. The following subsystems are **fully or near-fully implemented**:

### Core Game Engine (100%)
- ✅ Turn processing and game loop
- ✅ **Economy/Production system** — sector production, population dynamics, resource aggregation
- ✅ Ship processing (movement, fuel, damage)
- ✅ Planet processing (orbital mechanics, toxicity, slavery)

### Player Commands (90%)
- ✅ Build/upgrade/sell/bid system
- ✅ Combat resolution and resolution system
- ✅ CS (colony/sector) commands
- ✅ Navigation commands (land, launch, scrap, capture)
- ✅ Tech examination and declarations
- ✅ Profile commands (toggle, name, max, survey, map, victory, RST, dock)
- ✅ Order system (ship orders, routes, merchant shipping)
- ✅ Telegram/messaging system
- ✅ Fire/bombardment commands
- ✅ Power/alliance system
- ✅ Load/unload/transfer commands
- ✅ Miscellaneous commands (capital, zoom, orbit, autoreport, mobilize, dissolve, enslave, fuel, analysis, explore)

### Areas Needing Work (10%)
- ⚠️ Some command stubs remain in `internal/game/commands.go`
- ⚠️ Voting/democratic system incomplete
- ⚠️ Advanced deity/admin commands
- ⚠️ Some esoteric gameplay features from C version

Refer to the C source under `source/` for reference during development.

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
- **Protocol**: WebSocket messages use JSON encoding
- **Database Schema**: See `internal/db/keys.go` for bucket names and key structures

## Contributing

Refer to `CLAUDE.md` in the root directory for detailed architecture and contribution guidelines.
