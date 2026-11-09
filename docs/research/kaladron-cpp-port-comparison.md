# Galactic Bloodshed: C++ reference port survey for the Go migration

**Status:** Research / candidate backlog, not an implementation authorization or accepted ADR.  
**Survey date:** 2026-11-08. **Go reference:** `phreddrick/GB@4acc145e3abb372ff643773a2c2ee3f3e7a713ef`.  
**C++ reference:** `kaladron/galactic-bloodshed@b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4` (`main`).  
**Canonical legacy behavior:** this repository's `source/` C sources and its golden/parity fixtures.  
**External repository:** https://github.com/kaladron/galactic-bloodshed (reports Apache-2.0 licensing).

## Purpose and constraints

Treat Kaladron's C++ port as a **second, non-authoritative implementation and design reference**. We surveyed its source and documentation; we did **not** build, execute, or independently certify its game behavior. A source file or test being present does not establish behavioral parity with the original C.

This document gathers ideas for the existing Go migration plan, not a proposal to copy the C++ architecture, replace bbolt with SQLite, or extract game mechanics into microservices. Keep one authoritative simulation/transaction boundary per universe (ADR-0001). Classify proposed changes as:

- **Legacy parity:** an original GB feature already present in C and missing, incomplete, or not yet proven in Go.
- **Implementation pattern:** a useful design or testing approach in the C++ version that Go can implement independently.
- **Possible enhancement:** player-facing convenience beyond baseline parity, requiring a separate design and acceptance decision.

**Client direction is already decided in the migration plan:** a **TypeScript browser/web client**, not a Python client. Use the C++ repository's Python terminal client solely as inspiration for UI behavior (maps, status, command history, keyboard UX). Do **not** plan a Python port or make Python a web-client dependency. The `microservices-migration-plan.md` (revision 2.2, section 0/7 and Phase 4c/7) calls for a versioned structured HTTPS/WebSocket API, OpenAPI-generated TypeScript types, engine-enforced authorization/fog-of-war, and a graphical browser experience. Preserve that direction.

## What was actually inspected

- C++ command specification, registry and dispatcher: `gb/commands/command_spec.cppm`, `registry.cc`, `dispatch.cc`
- C++ player onboarding: `gb/creator/enrollment_service.cc`, `archetypes.cc`, `racegen_session.cc`
- C++ turn and feature paths: `gb/turn/doturncmd.cc`, `gb/commands/{bid,sell,route,tactical,governors,insurgency,vote}.cc`, `gb/mechanics/navigation.cc`
- C++ presentation and sessions: `gb/presentation/core.cppm`, `gb/presentation/empire_views.cppm`, `gb/services/session_registry.cppm`, `gb/server/auth.cc`
- Python client only as UX evidence: `client/gb_client/{ui,client,protocol,commands}.py`
- C++ gameplay guides: `docs/{race_generation,turn_cycle,ships,combat,covert_ops}.md`
- Go dispatcher `go/internal/game/state.go`, Go command implementations, `go/README.md`, `docs/architecture/go-code-review.md` and migration plan.

C++ registry contains **108 `reg(..._cmd)` calls** (some compile-time-feature-gated), versus **62 literal `matchCmd` names** in the inspected Go dispatch switch. The historical C review reports **109 reachable C commands** with default flags. **Do not turn these counts into a parity percentage:** aliases, name differences (`tech`/`technology`, `mod`/`modify`), admin routes, compile-time flags, and stub/partial implementations require per-command behavioral analysis. The October 27 Go review is a *dated baseline*, not proof every finding is still open in this November 8 tree.

## Prioritized opportunities

| Priority | Area | Type | Evidence and possible benefit | Required Go follow-up |
|---|---|---|---|---|
| P0 | Race enrollment/homeworld placement | Legacy parity | C++ `EnrollmentService::enroll_player` finds a homeworld, colonizable starting sector, builds a capital government ship and links it to the race. Its generator provides **11 archetypes** as a quick-start UX option. | Revalidate CR-07 against current Go. Port the **legacy placement semantics** with tests first; evaluate archetypes as a separate optional UI feature. |
| P0 | Turn pipeline and market settlement | Legacy parity | C++ `do_turn` runs market transactions on full updates and separates planet, ship, race, and finalization steps. Go review identifies market settlement, maintenance, ordering and other missing turn steps (CR-10–CR-16). | Compare original `source/doturn.c`, C++ `gb/turn/doturncmd.cc`, and Go turn functions; add golden behavioral tests before modifying the scheduler. **C remains the tie-breaker.** |
| P0 | Per-command persistence and notifications | Implementation pattern + known Go gap | C++ dispatcher wraps transactional commands in an entity transaction; a `SessionRegistry` stages socket notifications and emits them only after commit. | Address Go CR-17 / plan section 8.4 with staged changes, durable command IDs and post-commit effects. C++ **does not prove a durable outbox**; follow the existing Go plan's stronger idempotency and retry guarantees. |
| P1 | Declarative command registry | Implementation pattern | C++ `CommandDescriptor` defines aliases, roles, scopes, AP model, min args, syntax, handler, transactional flag. `dispatch_command` enforces checks centrally. | Follow planned CR-40/CR-41 work. Preserve C prefix-resolution semantics while making authorization, AP deduction and help metadata testable and consistent. |
| P1 | Governors, treasuries and ownership | Legacy parity | C++ implements governors/appoint/revoke, allocation, taxes, transfers, and supporting reports. | Audit each C command's data changes and security checks; add governor-scoped command parity fixtures. Needed for meaningful multiplayer enrollment. |
| P1 | Combat and tactical reporting | Legacy parity + presentation pattern | `tactical.cc` builds target/range/hit-probability reports; `assault.cc`, `repair.cc` and mechanics files provide testable reference paths. | First repair known Go combat/capture parity (e.g. CR-19, CR-26, CR-27); then add structured tactical report view models. Do not import alternate formulas without C fixtures. |
| P1 | Typed client protocol and view models | Implementation pattern / modernization | `gb/presentation/core.cppm` defines typed JSON envelopes, messages and errors; other modules project races, planets, maps, etc.; command dispatch can emit dirty-entity JSON after a successful command. | Build **Go-owned, versioned HTTPS + WebSocket API** and OpenAPI-generated TS models under existing Phase 4c/7. Engine must filter visibility per player/governor; avoid leaking hidden entities through dirty deltas. |
| P2 | Browser UI interaction features | Possible enhancement, **TypeScript only** | Python client offers terminal maps, orbital views, survey parsing, status bar, history, editable command input, macros and variables. | Re-imagine these in TS web components: interactive sector/star maps, contextual panels, command palette/history, shortcuts, optional safe client-side macros. Do not port Python or require curses/CSP in the browser. |
| P2 | Comprehensive help and discoverability | Implementation pattern | C++ has per-command help, long gameplay guides, and typed command syntax/description. | Derive contextual, permission-aware help and TS UI tooltips from Go's future command registry; document user-visible behavior rather than duplicating large static manuals. |
| P2 | Merchant routing, diplomacy, insurgency, voting | Mostly legacy parity; voting optional | C++ includes route automation, bids/sales, power blocks, insurgency funding, and unanimous advance-vote support. | Compare each against original C and current Go; add after core economy/governance and combat correctness. Vote-to-advance needs an explicit server policy and security decision. |
| P3 | Specialized ship/planet AI and mechanics | Legacy parity | C++ references Von Neumann and Berserker behaviors, nested carriers, repair, missile combat and planetary processes. | Defer until turn/order, enrollment and core combat parity are dependable. Use deterministic fixtures for RNG-heavy cases. |

## Concrete file references

External paths below are anchored to the inspected C++ commit; corresponding Go paths are on `master` as of the surveyed commit.

| Subject | C++ source | Go starting point |
|---|---|---|
| Player enrollment / homeworld | [enrollment_service.cc](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/gb/creator/enrollment_service.cc) | `go/internal/enroll/`, `go/cmd/racegen/`; CR-07 |
| Archetypes | [archetypes.cc](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/gb/creator/archetypes.cc) | Phase 7 onboarding UX, after parity |
| Dispatch metadata | [command_spec.cppm](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/gb/commands/command_spec.cppm) and [dispatch.cc](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/gb/commands/dispatch.cc) | `go/internal/game/state.go`; CR-40/41 |
| Turn processing / market | [doturncmd.cc](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/gb/turn/doturncmd.cc) | `source/doturn.c`, `go/internal/game/turn.go` |
| Post-commit notifications | [session_registry.cppm](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/gb/services/session_registry.cppm) | CR-17, plan 8.4 / 8.8 |
| Tactical reporting | [tactical.cc](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/gb/commands/tactical.cc) | `go/internal/game/combat.go`, `commands_fire.go` |
| Typed JSON output | [core.cppm](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/gb/presentation/core.cppm) | Go API plan sections 6–7 / Phase 4c |
| Maps and interaction *inspiration only* | [Python UI](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/client/gb_client/ui.py), [client commands](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/client/gb_client/commands.py) | **TypeScript web UI** Phase 4c / 7 |
| Architecture and gameplay docs | [ARCHITECTURE.md](https://github.com/kaladron/galactic-bloodshed/blob/b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4/ARCHITECTURE.md) | `docs/architecture/microservices-migration-plan.md` |

## Proposed next research deliverable: three-way behavioral capability matrix

Create a **separate, evidence-backed** matrix against (1) original C, (2) this pinned C++ commit, (3) Go current `master`. For every command and major turn pass, record:

1. Canonical C name, prefixes/aliases and permission/AP/scope semantics.
2. C++ name, file, implementation depth and tests (do not equate `_test.cc` presence with a passing test).
3. Go handler, current test coverage and classification: **parity pass / partial / missing / changed by design / not verified**.
4. Whether a difference is **legacy parity**, an **optional C++ enhancement**, or a **Go architecture choice**.
5. Fixture/reproduction steps, relevant CR/GD IDs, dependencies and proposed phase.

Start with enrollment, the ordered turn pipeline, market/governor accounting, command dispatch and combat. Avoid broad unreviewed gameplay rewrites. Any proposed new feature, protocol contract or ship mechanic needs normal design approval, independent review and CI.

## Guardrails

- **No code changes, game-rule changes, browser framework selection or deployment are authorized by this survey.**
- Do not infer that C++ is more correct than C; resolve conflicting formulas by original C behavior, golden tests and explicit owner choices.
- Do not introduce SQLite solely because the C++ port uses it. Keep Go's bbolt + existing CR-18/CR-43 approach.
- Keep the one-engine-per-universe modular monolith; microservice extraction stays behind the migration plan's gates.
- TypeScript browser UI is a standing requirement. The Python client is **research only**, never a planned deliverable.
- Respect the external project's Apache-2.0 license if code or substantial expressive content is reused, including applicable attribution and notices.
