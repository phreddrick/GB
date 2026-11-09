# CR-07B: pure homeworld, colony and government-ship placement

Status: implemented for review; no merge or deployment authorization.
Base: `f4489dc4d3913ad74854290346ee88b451ca607c` (CR-07A, PR #24).
The owner authorized this construction-only slice ahead of Phase 3c.
Database format impact: **NONE**.

## Input/output contract

`game.ProposePlacement` consumes a CR-07A `racegen.Spec`, a player number,
government ship number, borrowed `PlacementUniverse`, `PlacementOrder` and
trusted `PlacementIdentity`. It defensively revalidates the spec before any
numeric conversion. Inputs must be a consistent read-only snapshot; the
caller must exclude concurrent world mutation for the duration of the call.
The function never calls State methods, store methods, a clock or RNG.

Stars are indexed by number; planets use State's nested star/planet map keys.
Every star must have a matching identity and a supported declared planet count.
Scanned planets must exist with matching identities; a candidate's nonzero map
dimensions must exactly describe its sector slice. Coordinates must sum to
finite ship positions. This is validation of the accessed view, not a full
database integrity audit.

Star order must be a full permutation of `[0,len(Stars))`; it is validated
completely before selecting a hit. Planet search always uses C index order.
Sector order must be a full permutation of the selected planet's row-major
indices, validated completely before choosing a capital. Prefixes, duplicate,
negative and out-of-range indices are errors, even after an otherwise valid
hit. If no home exists, sector order is unused. The first eligible home is
selected; a capital/chain error on it is returned rather than silently searching
for a different home. Later callers can derive a sector order from the same
stable snapshot and CR-07A homeworld metadata.

Identifiers are checked against representation limits: player 1..64, ship a
nonzero uint32; existing race slots and existing ship-map keys are refused.
This chooses no race-allocation policy: cap 63 vs 64, God-first, name uniqueness,
ship reservation and reuse remain responsibilities of later units.

Opaque nonempty race and governor credentials are copied verbatim into the
proposal. No hardcoded historical `"0"`, hashing, verification or credential
generation occurs. Nonempty is not proof of a secure representation: a future
authenticated workflow must validate credentials and authorize God/Guest flags
before calling. Contradictory God+Guest flags are refused. Tests use short,
explicitly fake opaque markers, never usable passwords/hashes.

Success returns independent race, star, planet (including a deep sector copy)
and government-ship proposals, plus capital coordinates. All other copied model
fields are values. A valid old planet chain is preserved by setting the new
ship's NextShip to its old head and the proposed planet head to the new number;
old ships are not modified or returned as borrowed output references.

Every failure returns nil plus a typed `PlacementError` with a stable code and
field, and changes nothing. Codes include `INVALID_SPEC`, `INVALID_PLAYER`,
`PLAYER_IN_USE`, `INVALID_SHIP`, `SHIP_IN_USE`, `INVALID_IDENTITY`,
`INVALID_SEARCH_ORDER`, `INVALID_UNIVERSE`, `INVALID_ORBIT_CHAIN`,
`NO_HOME_PLANET` and `NO_CAPITAL_SECTOR`.

## C behavior reproduced

Authority is the built `enroll_valid_race` in **source/GB_racegen.c**. The
unbuilt `source/enroll_valid.c` draft and C++ implementation are not used as
behavioral oracles. Tests contain source-derived literal expectations and
whole-model comparisons; this unit does not claim an executed end-to-end C
enrollment comparison. Existing C-generated golden fixtures remain unchanged.

| Fields/rule | Original reference |
|---|---|
| Home mappings, random-without-replacement search semantics, two planets, uninhabited stars, inclusive RTEMP -200..100 | `GB_racegen.c:30,107-145` |
| Zero race defaults, name/privileges, governor home/default scope, toggles and active status | `GB_racegen.c:150-166,250` |
| Race conditions copied only through OTHER; TOXIC remains zero | `GB_racegen.c:168-169` |
| Translation own/others 100/1; all 100 for player 1 or God | `GB_racegen.c:180-186` (safe Go indexing) |
| Traits, IQ versus IQ_limit, Fertilize multiplied by 100, likes fractions and last common 100% LikesBest | `GB_racegen.c:204-229` |
| Sector owner/race, population=sexes, fertility 100, efficiency 10, troops 0; planet population/troops | `GB_racegen.c:232-248` |
| Government ship, owner/governor, tech 100, weapons/capacities, size/base mass 100, class Standard, crew 10, flags, landing/orbit and links | `GB_racegen.c:265-330`; `shipdata.h:79` |
| Planet per-race owned-sector count/explored, global explored false, MaxPopn | `GB_racegen.c:334-340`; `max.c:28-41` |
| No STARTING_INVENTORY; star explored/inhabited and five home-star AP | `GB_racegen.c:343-353,360-363` |

MaxPopn uses the existing C-matching `maxSupport(race, sector, 100, 0)` after
initializing the capital. Support is 1100, multiplied by map width and height,
then divided by two with integer arithmetic. Planet.Info.Popn/Troops, stockpiles,
taxes and routes are deliberately preserved; they are not synchronized to the
new aggregate or granted starting inventory. Sector geology, condition,
mobilization, resources and crystals remain unchanged. Universe AP is untouched.

Generic ship helpers are inappropriate here: they recompute size/mass/cost,
give God cargo, apply damage/retaliation defaults and emit notices. A dedicated
constructor reads the existing `model.Shipdata` government row and applies C's
enrollment-specific overrides. In particular On/Alive/Active/Docked/Self are
true; damage, retaliation, fuel, destruct, resources and troops are zero.

The proposed ship passes Go `governed()` and C's planet-docked condition.
The existing Go predicate also accepts star destinations, unlike C; this unit
does not alter that unrelated predicate defect.

## Intentional departures and safety invariants

- Owner-approved vacancy strengthening: any Planet.Info owned-sector count
  excludes the candidate despite stale star flags. Direct sector ownership
  also excludes it despite stale counts. An unowned sector with population or
  troops is never overwritten; the search can choose another matching sector.
- Owner-approved capital failure: no matching unoccupied sector gives
  `NO_CAPITAL_SECTOR`; there is no arbitrary `(0,0)` fallback or terrain rewrite.
- Injected permutations replace random selection; no global/turn RNG advances.
  A separately designed isolated, persisted enrollment RNG belongs to integration.
- Existing valid ship chains are prepended to, not replaced. Dangling, cyclic,
  dead-member, wrong-location and wrong-identity chains are rejected. Unlinked
  live ships at the target planet are rejected. Unlinked dead records are
  ignored, matching `rebuildOrbitChains` (`turn.go:529-533`), and remain untouched.
- Invalid orders/shapes/indices/coordinates fail safely. C's out-of-bounds
  translation write is not reproduced; every valid Go player slot receives
  the documented intended translation default.
- Credential and privilege policies are supplied by the trusted caller, not
  inferred from player 1 or accepted from an unauthenticated request.

## Tests and validation

`placement_test.go` covers the required 22-case categories with **82 leaf cases**
across 10 top-level placement tests (89 total test nodes). Whole race/ship
literals assert zero defaults as well as explicitly initialized fields; full
planet/star comparisons assert unrelated field preservation. Cases cover all
home types, temperature edges/outside values, star/planet vacancy, capital order,
traits, privilege/translation cases, malformed orders/universe/chain, model ID
boundaries, rectangular MaxPopn arithmetic, nonfinite/overflow positions,
output mutation, repeatability and parallel calls. Every finite failure fixture
and successful construction snapshots all supplied inputs. Nonfinite position
fixtures check their floating-point bits remain unchanged.

A real generated universe is pinned to `universe.Generate({Stars:8,
MinPlanets:2,MaxPlanets:3}, seed=11)`: Earth at star 6 / planet 1, capital (0,0)
under ascending star and row-major sector order. It uses no database or live
engine. The synthetic field fixture uses player 4, ship 42, and a 3x3 map;
expected MaxPopn is 4950. A 3x2 fixture expects 3300.

Local check results and independent review are recorded in the PR. Required
checks: build, format, vet, full tests/race, known-defect inventory, existing
golden reproduction, lint/growth gates, offline manifest and workflow checks,
and whitespace validation. Hosted Actions owns container builds/scans/kind.
No protected gate tooling, golden allowances, inventory or lint baseline changes
are included; no acknowledgment label is expected.

Local results: all those checks passed. Lint matched all 1,057 baseline findings
with zero new/stale findings. Growth passed with `PR_LABELS_JSON='[]'`. Existing
C fixtures reproduce exactly. Offline manifests validated all 21 resources;
180 Python workflow-helper tests and 17 workflow-policy checks passed. Module
verification and the tidy diff were clean. Test scratch files used an ignored
worktree directory to avoid this host's invalid `/tmp/.git` marker; disposable
listener tests ran with sandbox socket permission. No tests were weakened.

Independent review: **APPROVE for CR-07B; no blocking findings**. The reviewer
checked canonical C fields, selection/copy invariants and edge cases, and
independently passed all 82 cases, targeted race tests, vet and whitespace
checks. Its dead-record handling finding was corrected and re-reviewed with
regressions; its request for a pinned generated fixture is also resolved.
Hosted CI and the owner's normal merge review remain pending.

## Limits and next units

This is not enrollment. No writes, keys, idempotency records, number allocation,
State installation, authentication, actor/admin/API, browser or gate changes
exist. Nothing has touched the running game, Kubernetes, Argo CD or Longhorn.
The separate external TypeScript signup app and TypeScript gameplay client
remain later work under the modular-monolith-first ADR.

CR-07C must allocate/reserve ship numbers and atomically persist the four
entities and idempotency evidence. In particular, existing dead ship keys are
still refused here: free-list reuse needs an explicit reconciled snapshot and
reservation contract, not silent overwriting or removal of a dead record.
Persistence must recheck stale placement preconditions and preserve existing
orbit chains. CR-07D must install only after confirmed commit, handle uncertain
durability/post-commit failures and bind retries to caller/request identity.
CR-17 command/turn interactions remain to be tested in that integration.

Outstanding owner policies: allocation cap/reuse and God-first behavior,
privilege authorization, separate-governor credential workflow, persisted
enrollment RNG and eventual exposure. The current authorization settles the
temperature/vacancy and no-capital-fallback rules for this unit. Existing
plaintext-password fallback and inactive-governor authentication remain
unfixed blockers for any eventual external enrollment.
