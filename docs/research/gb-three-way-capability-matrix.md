# Three-way GB capability matrix — original C, Kaladron C++, Go

**Status:** Research baseline; **source-indexed first pass**, not a claim of tested gameplay parity or an implementation authorization.  
**Recorded:** 2026-11-08/09 UTC.  
**Original C + Go snapshot:** `phreddrick/GB@f54288b561cab302aaa0e164429e4f1dcb00e51b` (`master`, PR #20 merged).  
**C++ snapshot:** `kaladron/galactic-bloodshed@b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4` (`main`).

**Companion inventory:** [gb-three-way-commands.csv](gb-three-way-commands.csv) — **124 command spellings / registry entries** across the three sources, with original C location, source-level guards, matching C++ registration and test-file presence, Go dispatch verb/handler/file, and conservative classification. For the initial research and prioritization, see [Kaladron C++ port comparison](kaladron-cpp-port-comparison.md).

## How to read the evidence

**Authority order:** the original `source/` C files are the historical game behavior reference, backed by golden fixtures; Kaladron's C++ is an independent implementation and useful test/design reference; Go's behavior must be proven with its own source review and tests. A matching command name, handler, test filename, and even a green build **do not establish parity**.

Inventory methodology:

1. Enumerated all `match(args[0], "...")` spellings inside C `process_command` (`source/GB_server.c`), retaining source line numbers and compile-time guard context. This yields **111 raw spellings**, **two `DEBUG`-only**, thus **109 non-DEBUG dispatcher spellings**. Several are aliases and God/admin commands, *not* 109 distinct mechanics.
2. Enumerated **108 `reg(..._cmd)` registration statements** in C++ `gb/commands/registry.cc`, including conditional `MARKET`/`VOTING` entries. Registration is not proof of compiled availability or passing tests.
3. Enumerated **62 direct Go `matchCmd` spellings** from `go/internal/game/state.go` and mapped them to actual `cmdX` handler source files. A mapping is not a behavioral pass. Some legacy spellings can be candidates for differently named Go handlers (`technology`→`tech`, `modify`→`mod`, report family→`rst`); these are marked **candidate**, not equivalent.
4. Added commands found only in C++ and/or Go. This is why the union is **124 entries**, rather than the C baseline count.
5. Go's earlier review (`docs/architecture/go-code-review.md`, 2026-10-27) is a historical issue inventory, **not a fresh re-test**. Correctness claims need revalidation against the pinned Go commit. CR-18 and CR-43 were subsequently implemented; Stage 4 acceptance is separately recorded.

The CSV's `go_dispatch_class` has deliberately narrow meanings:

- **dispatched; behavior unverified** — the Go player dispatcher maps a spelling to a handler; the behavioral outcome, authorization and persistence have not been compared in this study.
- **alternate-name candidate; parity unverified** — another Go command could cover the capability, but the C spelling and precise semantics need testing.
- **typed gbctl admin operation; no player parity** — equivalent operational intent may exist outside the player dispatch.
- **not in Go player dispatch** — *not necessarily missing from Go globally*. Some are aliases, admin commands, features behind guards, or reports consolidated differently.

The C++ matching-test-file column means a same-named `_test.cc` **exists in source**; it does not mean that test was executed or passed. Similarly, a Go handler file is source-location evidence, not a test result. This is intentionally a trustworthy discovery matrix, **not a fabricated green/red game-parity score**.

## High-priority capability and lifecycle matrix

Classification: **PARITY GAP (reported)** means the previously reviewed Go code had a documented discrepancy, not that this survey executed it today. **SOURCE PRESENT / UNVERIFIED** means further semantic tests are required. **IMPLEMENTED + LIVE OBSERVED** uses the separate Stage 4 operator evidence, not an independent test run for this research. **PLANNED** is a design goal, not shipped functionality.

| Capability | Original C (behavioral reference) | Kaladron C++ | Go, current research classification | Next proof / decision |
|---|---|---|---|---|
| Race enrollment and initial colony | `source/enrol.c` selects home planet, owned sector, government ship | `gb/creator/enrollment_service.cc` creates the race, home sector and government ship; `archetypes.cc` adds 11 presets | **PARITY GAP (reported CR-07)**: Go `internal/enroll` and `cmd/racegen` historically omitted placement | P0: verify current Go, then fix original-C placement with homeworld/ownership/AP fixtures. Presets are a separate optional UX proposal |
| Commander/governor authority | `source/GB_server.c` dispatches governors, appoint/revoke, grant/give, treasury | C++ command handlers and central role/scope checks | **NOT DISPATCHED BY SAME NAMES** in Go; not an assertion of data-model absence | P0/P1: list every permission and treasury effect; build governor-specific integration fixtures |
| Command resolution and aliases | Ordered, case-sensitive prefix-based dispatch (`source/GB_server.c`, `match` helper) | Registry of descriptors, explicit aliases and role/scope/AP checks | **SOURCE PRESENT / UNVERIFIED**: Go has a 62-spelling ordered `matchCmd` switch; historical CR-40/41 records collisions/permission issues | P1: declarative Go command descriptors; test every C abbreviation collision before changing behavior |
| Player command atomicity / notification delivery | Direct record operations; no transactional notification guarantee | `gb/commands/dispatch.cc` starts an entity transaction; `gb/services/session_registry.cppm` stages notifications until commit | **PARITY / DURABILITY GAP (CR-17)**: current plan requires staged command changes, committed acknowledgment, dedup and durable outbox cursor | P0: retain Go plan section 8.4's stronger semantics; review C++ approach as a pattern, not a drop-in |
| Market order creation and trade fulfillment | `source/doturn.c` processes lots, market costs/profits, maintenance | `gb/commands/bid.cc`, `sell.cc`, `gb/turn/doturncmd.cc` | **PARITY GAP (reported CR-14)**: Go `bid/sell` exist; original review found missing turn settlement | P0: test inventory/bid/cash/freight across a completed full update |
| Planet and sector production | `source/doturn.c` runs ship processing before `doplanet`; `dosector.c` production | `gb/turn/doturncmd.cc` and `doplanet/dosector` passes | **PARITY GAP (reported CR-10–16)**: Go `turn.go` invokes planet processing before ship processing | P0: deterministic golden tests for mirror/greenhouse, population, resources and ship-to-planet effects |
| Mine and mass/ownership passes | `domine`, `domass`, `doown` in C's turn | Separate mine, ship-mass/ownership and turn passes | **SOURCE PRESENT / ORDER UNVERIFIED**: Go `turn.go` has mine and ship processing, but pass order differs | P0: original-C execution-order trace fixtures |
| Ordered ship movement and combat | Speed-ordered `doship` plus missile/ABM pass | C++ `process_ship_turns`, `process_abms_and_missiles` | **SOURCE PRESENT / PARITY UNVERIFIED** in Go `turn.go`, `ship.go`, `combat.go` | P0: movement and combat fixtures, including special ships and impacts |
| Ground and ship capture | Legacy `move.c` and assault pathways | `gb/commands/assault.cc`, `gb/mechanics/boarding.cc` | **PARITY GAP (reported CR-19)** for ground-sector ownership transitions; Go has `capture` handler, not equivalent proof | P0: ownership transition, defenders removed, post-commit reload |
| Planet guns / CEW | C formulas in original combat sources | C++ tactical/combat mechanics | **KNOWN PARITY RISKS (CR-26, CR-27)** in original Go audit | P0: compare existing golden fixtures, correct any remaining mismatches, add call-site tests |
| Governor maintenance / AP / taxes | C `maintain`, AP distributions and governor costs | C++ maintenance and governor / tax commands | **PARTIAL / UNVERIFIED** in Go, historically CR-14 | P0: AP, treasury deficit, morale, recurring ship maintenance scenarios |
| Alliance / power-block score | Original `doturn` computes block points | C++ `update_alliance_block_vps` | **PARITY GAP (reported CR-16)**; possible accumulated `SystemsOwned` divergence | P1: repeated-update tests showing no runaway block power |
| Technology and discoveries | C `make_discoveries`, `technology` | C++ turn finalization and `technology` | **SOURCE PRESENT / ALIAS DIFFERENCE**: Go uses `tech`; original `technology` spelling isn't accepted directly | P1: test AP/tech/novelty messages and command aliases |
| Persistent telegrams / news | Legacy telegrams survive disconnect | C++ telegram service and staged notifications | **PARTIAL (reported CR-16)**: Go has `telegram/read`; some system notices historically transient | P1: disconnected-recipient delivery and crash/restart tests |
| Automated merchant routes | Original route logic and ship transport | `gb/commands/route.cc` plus `gb/mechanics/navigation.cc` | **SOURCE PRESENT / UNVERIFIED**: Go `cmdRoute` exists | P2: load/unload/destination/fuel and cross-system parity |
| Tactical / economy reports | C `rst` report family, tactical and stock | Rich C++ typed view models and report commands | **ALTERNATE-NAME CANDIDATE / UNVERIFIED**: Go `rst` may cover subreports | P1/P2: test each legacy subreport independently, then add typed API models |
| Covert operations, voting, autonomous AI | C `insurgency`, `vote` under guards, VN/Berserker logic | C++ command implementations and turn AI | **PARTIAL / NOT DISPATCHED BY SAME NAMES**; actual parity not determined | P2/P3: defer until core economy, governance and turn semantics |
| Universe schedule and turn triggers | C persisted update/segment time, admin/God commands | C++ server timer + `do_next_thing` | **IMPLEMENTED + LIVE OBSERVED**, Go `schedule.go`, `gbctl`; Stage 4 observed no-catch-up full update | Maintain deterministic scheduler tests and separate admin permissions |
| Atomic *turn* persistence | C flat-file writes not atomic across world | C++ SQLite/deferred-write model (not certified here) | **IMPLEMENTED + LIVE OBSERVED CR-18**, successful commit sequence 3 after Stage 4 resume | Historical CR-18 acceptance in [Stage 4 observation](../testing/cr18-cr43-stage4-observation-2026-11-09.md); fault-injection remains separate evidence |
| Durable maintenance / Kubernetes Ready | No comparable historical C Kubernetes lifecycle | No analogous verified feature in this survey | **IMPLEMENTED + LIVE OBSERVED CR-43**: QUIESCED is durable and Kubernetes Ready; resume clears maintenance | Next legitimate release must verify automatic OrderedReady rollout on **Longhorn RWOP**, not just kind local-path RWO |
| Typed browser protocol | Original C text + special `client_survey` framing | C++ ASCII/JSON view models and canonical typed JSON envelopes | **PLANNED**: Go currently sends C-formatted WebSocket text; browser API in migration plan | Phase 4c/7: Go-owned versioned OpenAPI + TypeScript-generated types and visibility-filtered events |
| Player graphical interface | Terminal/ASCII and client-specific map output | Python curses maps/status/macros/history are a **UX reference only** | **TYPESCRIPT BROWSER CLIENT REQUIRED**, not Python | Phase 4c/7: star/planet maps, status panels, command UX, contextual help; choose TS stack at design time |
| Persistence / process split | Original flat files, single process | SQLite and C++ n-tier design | **Go bbolt + modular monolith** (ADR-0001); service extraction conditional | Do not migrate DB or split transactional world mechanics merely to match C++ |

## Recommended investigation order

1. **P0 — playable core and correctness:** confirm CR-07 race/homeworld government ship; turn ordering and market settlement; command acknowledgment/persistence (CR-17); capture and combat correctness. This is more valuable than adding new UI screens on top of broken rules.
2. **P1 — completeness and consistent authorization:** metadata command registry with original-C prefix/alias rules, governors/treasury, reports and tactical information, empire accounting and durable notifications.
3. **P2 — TypeScript-first playable interface:** implement the previously approved OpenAPI + structured WebSocket contract, browser maps/command UX and permission/visibility enforcement. Use Python only to study player workflows.
4. **P3 — optional enhancements:** archetype presets, vote-to-advance UX and richer navigation/macro facilities when core gameplay parity is demonstrated.

## Work needed before calling this a *behavioral* parity matrix

The linked CSV is an indexed **triage register**, not evidence that 124 commands were executed. To graduate entries to *parity pass / partial / missing / changed by design / not verified*, do the following for each relevant **canonical C behavior**, rather than each alias spelling:

- Read the actual C handler, flags, prefixes, permission/scope/AP gates and data mutations. Record direct file/function/line references and deterministic fixture inputs.
- Inspect the C++ handler, descriptor, supporting mechanics, and any tests. Distinguish test presence from test execution. Resolve C++ departures from C as intentional changes or unproven differences.
- Inspect the Go handler and any tests; reproduce outcomes with a disposable world and golden comparisons, including persistent state and side-effect delivery across restart.
- Register findings in the existing CR/GD tracking process **only after** independent evidence establishes a defect. This research document alone authorizes no changes to `test/knowndefects.yaml`, golden allowances or lint baselines.
- For unsafe/non-deterministic features (admin reset, God-only commands, RNG, live-player communication), use isolated tests and fixtures; never execute them on `gb-dev` merely to fill the matrix.

## Client direction and architecture guardrails

- **TypeScript graphical web client is the selected product goal.** Do not implement, port, or ship the Python curses client. Reuse its ideas for player-facing maps, history, macros, status displays and usability.
- TypeScript types should be generated from the **Go-owned API contract**, not copied from the C++ schema without checking semantics.
- Maintain engine-side visibility/permissions (fog-of-war, race/governor access), idempotent commands, and committed-before-ack effects.
- Continue with one authoritative engine/transaction per universe; later gateway/identity extraction requires explicit plan gates.
- C original is behavioral authority; C++ is a reference. Follow external Apache-2.0 obligations if copying protectable code; independent Go design inspired by concepts is preferred.

## Source index

- [Original C dispatcher](https://github.com/phreddrick/GB/blob/f54288b561cab302aaa0e164429e4f1dcb00e51b/source/GB_server.c), [original turn](https://github.com/phreddrick/GB/blob/f54288b561cab302aaa0e164429e4f1dcb00e51b/source/doturn.c)
- [Go player dispatcher](https://github.com/phreddrick/GB/blob/f54288b561cab302aaa0e164429e4f1dcb00e51b/go/internal/game/state.go), [Go turn](https://github.com/phreddrick/GB/blob/f54288b561cab302aaa0e164429e4f1dcb00e51b/go/internal/game/turn.go)
- [C++ command registry](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/gb/commands/registry.cc), [descriptor](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/gb/commands/command_spec.cppm), [turn](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/gb/turn/doturncmd.cc)
- [C++ enrollment](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/gb/creator/enrollment_service.cc), [Python client (inspiration only)](https://github.com/kaladron/galactic-bloodshed/tree/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/client/gb_client)
- [Migration plan](../architecture/microservices-migration-plan.md), [dated Go code review](../architecture/go-code-review.md), [known-defect inventory policy](../testing/known-defects.md)

**No program behavior, Kubernetes resource, running server, GitOps image digest or existing acceptance record is changed by this research.**
