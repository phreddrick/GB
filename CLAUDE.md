# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is **Galactic Bloodshed** (GB), a multi-player space strategy game originally written by Robert P. Chansky (~1989–1992). It is a networked, telnet-based game server written in C. Players manage races, colonize planets, build fleets of ships, and wage war across a procedurally-generated galaxy.

## Build Commands

All source is under `source/`. Build from that directory:

```sh
cd source
make          # builds all: GB enrol racegen makeuniv guardian
make clean    # remove binaries and .o files
```

Individual targets:
- `GB` — the main game server (the shell/daemon players connect to)
- `makeuniv` — universe creation utility (run once to generate data files)
- `enrol` — original player enrollment program
- `racegen` — newer enrollment program (`-DPRIV` flag used during compile)
- `guardian` — watchdog/guardian process

Compiler: `gcc` with `-g` (debug). Links against `-lcurses -ltermcap -lm`.

There is no automated test suite.

## Architecture

### Executables and Their Roles

| Binary | Source entry | Purpose |
|--------|-------------|---------|
| `GB` | `GB_server.c` | Main server; listens on TCP (default port 2010), handles player connections, schedules updates/movement segments |
| `makeuniv` | `makeuniv.c` | One-time universe initializer; calls `makestar.c` / `makeplanet.c` to generate binary data files |
| `enrol` / `racegen` | `enrol.c` / `racegen.c` + `enroll.c` | Player registration, writes race data files |
| `guardian` | `guardian.c` | Process monitor/watchdog |

### Key Header Files

| Header | Purpose |
|--------|---------|
| `config.h` | Server host/port, feature flags (`#define MARKET`, `#define DEFENSE`, etc.), tunable timing constants |
| `tweakables.h` | Game-balance constants (ship costs, fuel rates, population growth, combat factors, map dimensions) |
| `vars.h` | Core game types and global variable declarations. Includes `files.h`, `tweakables.h`, `config.h`. Defines the `EXTERN` pattern. |
| `ships.h` | Ship/object type constants (`STYPE_*`, `OTYPE_*`) and ability indices (`ABIL_*`) |
| `races.h` | `struct race` (racetype) and `toggletype` definitions |
| `files.h` | Data file path macros (`PATH()`, `DATA()`) and `Files[]` array index constants |
| `doturn.h` | Per-turn transient state structs (`stinfo`, `vnbrain`, `sectinfo`) |
| `power.h` | Power block / alliance data |
| `buffers.h` | Output buffer definitions |
| `interface.h` | TinyMUD-derived network interface contract |

### The `EXTERN` Pattern

Global variables are declared in `vars.h` (and `doturn.h`) using the `EXTERN` macro. Exactly one `.c` file defines `#define EXTERN` before including these headers (e.g., `GB_server.c`, `makeuniv.c`). All other `.c` files define `#define EXTERN extern` before including them. This is the mechanism for a single definition across the entire program.

### `.p` Files — Prototype Includes

Each `.c` file has a paired `.p` file containing `extern` function prototype declarations. These `.p` files are `#include`d directly into `.c` files (not compiled separately). This is the project's substitute for separate header files per module.

### Game Loop / Update Cycle

- `GB_server.c` runs the event loop: accepts connections, dispatches player commands via `process_command()`, and fires scheduled updates/movement segments.
- `doturn.c` — `do_turn()` executes a full game update (production, combat resolution, victory checks).
- `doship.c` — processes per-ship actions during a turn.
- `doplanet.c` — processes per-planet actions during a turn.
- `moveship.c` / `moveplanet.c` — handle movement segments between full updates.
- `dosector.c` — sector-level processing.

### Feature Flags in `config.h`

Compile-time features are toggled by commenting/uncommenting `#define` lines:
- `MARKET` — commodity market system
- `VICTORY` — victory conditions
- `DEFENSE` — planetary gun emplacements
- `VOTING` — player voting
- `DISSOLVE` — allow players to dissolve their race
- `MONITOR` — deity monitoring of player activity
- `EXTERNAL_TRIGGER` — allow external password-triggered updates
- `DEBUG` — memory debugging

### Data Files

The game uses binary flat-file databases under a `Data/` directory (path set by `PATH()` macro in `files.h`). Files include star data, sector data, planet data, race data, ship data, commodity data, and news/telegram files. The `Files[]` array (populated in `files.c`) maps symbolic constants to runtime paths.

---

## Go Conversion

An in-progress port of GB to Go lives under `go/`. The goal is a modernized server that replaces telnet with WebSocket connections, replaces the flat-file binary database with an embedded key-value store, and structures the code as idiomatic Go packages.

### Build

```sh
cd go
go build ./...          # build all commands
go build ./cmd/gb-server   # main server binary
go build ./cmd/makeuniv    # universe initializer
go build ./cmd/racegen     # player enrollment
```

### Package Layout

| Package | Path | Purpose |
|---------|------|---------|
| `config` | `internal/config/` | JSON-based runtime config (replaces `config.h` compile-time flags) |
| `model` | `internal/model/` | Go structs mirroring the C game types: `Race`, `Star`, `Planet`, `Ship`, `Power`, etc. |
| `db` | `internal/db/` | Persistence layer using [bbolt](https://github.com/etcd-io/bbolt) (ACID, embedded key-value store; replaces binary flat files) |
| `game` | `internal/game/` | Game logic: `State`, turn processing, command handlers, combat, build, navigation |
| `net` | `internal/net/` | Network layer: HTTP/WebSocket server (gorilla/websocket), session registry, authentication (JWT) |
| `cmd/gb-server` | `cmd/gb-server/` | Main server entry point |
| `cmd/makeuniv` | `cmd/makeuniv/` | Universe generation utility |
| `cmd/racegen` | `cmd/racegen/` | Player enrollment utility |

### Key Design Differences from the C Version

- **Networking**: Telnet → WebSocket (`gorilla/websocket`). Players connect via WebSocket rather than raw TCP.
- **Database**: Binary flat files → `bbolt` embedded key-value store. All game state is JSON-encoded and stored in bbolt buckets. ACID writes replace manual file locking.
- **Config**: Compile-time `#define` flags → runtime JSON config file (`-config` flag). Feature flags and tuning constants are fields in `config.Config`.
- **Authentication**: JWT tokens issued at login, verified per-session.
- **Concurrency**: bbolt's read-transaction model (multiple readers, single writer) replaces the C server's single-threaded update loop.
- **Command dispatch**: `game.State` methods (`cmdBuild`, `cmdCS`, `cmdTech`, etc.) replace the C `process_command()` dispatch table.

### Port Status

The Go port is a **work in progress**. Many command handlers in `internal/game/commands.go` are stubs that return "not yet implemented". Implemented subsystems include:

- Build / upgrade / sell / bid (`build.go`)
- Combat resolution (`combat.go`)
- CS (colony/sector) commands (`commands_cs.go`)
- Navigation (`commands_nav.go`)
- Tech / examine (`commands_tech.go`)
- Profile commands (`commands_profile.go`)
- Turn processing (`turn.go`)
- Planet and ship processing (`planet.go`, `ship.go`)

The C source under `source/` remains the authoritative reference for game logic during the port.
