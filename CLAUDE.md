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
go test ./...           # golden parity tests (fixtures in ../test/golden/testdata)
```

The module path is `github.com/phreddrick/GB/go` (renamed in Phase 0 from `github.com/gbgame/gb`).

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

**Read `docs/architecture/go-code-review.md` and `docs/architecture/microservices-migration-plan.md` first.** The Go port is a **work in progress that is not yet a working game**: the independent review of 2026-10-27 found critical defects in enrollment (no home planet), ground combat (assault never captures), turn processing (ships processed after planets, combat damage discarded, market never settles), persistence (two sources of truth, non-atomic flush), the admin endpoint (process panic), and the deployment assets (universe regenerated on every restart). 62 verbs are dispatched versus 109 reachable C commands. Earlier notes claiming the port was finished and deployable are archived under `docs/archive/earlier-model-notes/` and are superseded.

Work is organised by the migration plan's phases; only the phase the owner has authorised may be implemented. Phase 0 (foundation) added:

- CI (`.github/workflows/ci.yml`): strict build, gofmt, vet, tests, race, govulncheck, Docker build; `golangci-lint` baselined against `docs/testing/lint-baseline.md` (new findings fail; the 1,132 pre-existing ones are documented, not hidden).
- Golden parity tests: `test/ctool` compiles the unmodified C functions and writes `test/golden/testdata/*.csv`; `internal/game/golden_test.go` compares the Go functions. Regenerate with `make -C test/ctool fixtures`, verify with `make -C test/ctool check`.
- Known-defect inventory: `test/knowndefects.yaml`, per-case expected divergences in `test/golden/expected/*.csv`, policy in `docs/testing/known-defects.md`. Every case is classified individually; an unexpected mismatch, a drifted divergence, an unexpected pass (XPASS) or a stale entry fails the build. Changes to the inventory, the expected files or the lint baseline need the pull-request label `known-defect-added`, and changes to the gate tooling need `gate-change-reviewed`; both are acknowledgments applied by the sole maintainer on the pull request, never inferred from commit messages; pushes run technical validation only (`test/scripts/growth-gate.sh`). Self-checks: `test/scripts/golden-mutations.sh` (and `golden-mutations-selftest.sh` for its helpers), `test/scripts/lint-gate-selftest.sh`, `test/scripts/gate-approval-selftest.sh`, `test/scripts/inventory-check.sh`.
- ADRs under `docs/architecture/adr/`.

Phase 1 (safe, locally runnable server; implemented 2026-10-29, under independent review) added, without changing game rules:

- Configuration from defaults, JSON file and environment with insecure defaults refused (`internal/config/env.go`, `validate.go`; ADR-0004); secrets only from `GB_JWT_SECRET` / `GB_ADMIN_TOKEN` (or `*_FILE`).
- Typed administrative requests on the actor (`internal/game/admin.go`): status, update, segment, quiesce, resume, universe init, enrollment, authentication; the `@@update` text path is gone (CR-04). Admin HTTP listener with a static bearer token (`internal/admin`, loopback by default) and the `gbctl` tool (`cmd/gbctl`).
- Engine modes, heartbeat and progress counter, `/livez` and `/readyz` (`internal/engine`, `internal/game/lifecycle.go`, `internal/net/health.go`); panic recovery that **halts** the engine (interim; Phase 3a replaces it); orderly shutdown with documented exit codes; persisted turn schedule with a no-catch-up policy (`internal/game/schedule.go`).
- Seeded universe generation as a library with single-transaction initialisation that refuses an initialised store (`internal/universe`, `db.PutUniverse`); `makeuniv` is an offline wrapper.
- Enrollment through the actor, disabled by default (`GB_ENROLL_PREVIEW`); WebSocket read limit, write deadlines, origin allow-list, per-session outbound queue (`internal/net/session.go`, `ws.go`); `log/slog` JSON logging and a dependency-free Prometheus registry (`internal/platform`).
- Tests: `internal/game/lifecycle_test.go`, `internal/net/server_test.go`, `internal/admin`, `internal/config`, `internal/universe`, `cmd/gbctl`, `cmd/makeuniv`, and the subprocess suite `go/test/e2e` (acceptance scenario, insecure-config refusal, SIGTERM idle, with clients, during a turn, shutdown timeout). Operator guide: `docs/operations/local-server.md`; results: `docs/testing/phase1-validation.md`; handoff: `docs/testing/phase1-handoff.md`.
- Independent review remediation (GB1-01 to GB1-09, `docs/testing/phase1-handoff.md` section 14): actor requests return results only through their reply channel and a request canceled while queued never runs; session identity is immutable before registration; a failed resume halts; each WebSocket session has a bounded command queue and one worker so pongs are processed during long commands; listener failure exits 5 and a second shutdown signal exits 6; `gbctl` validates reply envelopes and, for `universe init`, the typed universe record against the request (GB1-06R); `Gauge.Add` is atomic. Long production-timing tests run with `GB_LONG_TESTS=1`. The second review's GB1-10 replaced the SIGPIPE-prone `echo | grep -q` matcher in `test/scripts/golden-mutations.sh` with a here-string (protected gate tooling, `gate-change-reviewed`), with `test/scripts/golden-mutations-selftest.sh` exercising the helpers on large outputs.
- Still open and recorded: CR-18 (non-atomic turn flush; inventory entry `persistence/non-atomic-turn-flush`, Phase 3a), CR-17, CR-20 (interim: login runs on the actor), the query-string token, and every gameplay finding.

Rules for agents working here: do not change game rules, the actor loop, persistence or networking outside the authorised phase; cite `CR-nn` / `GD-nn` ids; add inventory entries only with a finding; never modify `source/`.

The C source under `source/` remains the authoritative reference for game logic during the port.
