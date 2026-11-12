# CR-17C: staged habitat factory activation

Baseline: `97ee43763da31b6b5eec3890e818e394dfcb1bf2` (master after PR #37).
Branch/worktree: `feature/cr17c-habitat-factory`, `/tmp/GB-cr17c-habitat`.
Toolchain: the verified private Go 1.27.4 binary first on PATH, `GOTOOLCHAIN=local`,
`GOCACHE=/tmp/cr17c-habitat-cache`, `TMPDIR` inside this worktree's ignored
`test/ctool/obj/tmp`. No live store, cluster, Argo CD, Longhorn, image or schedule
was touched; every store in every test is disposable.

Verdict: see the final section. CR-17 remains open; no general command
atomicity, durable acknowledgment or exactly-once execution is claimed.

## Scope

The remaining unsafe factory-activation path: `order #<factory> on` for a factory
docked aboard a habitat (`source/order.c:519-551`, the `whatorbits == LEVEL_SHIP`
branch). PR #35 migrated the landed branch and PR #37 telescope aiming; this
change migrates the **complete habitat invocation** (validation, carrier resource
and hanger arithmetic, factory size, factory On/Notified, the response) as one
staged operation with one closed store transaction, and retires the legacy
carrier writer so neither `giveOrders` nor `activateFactory` can bypass staging.
Cargo, movement/navigation, construction and the other direct saves listed in the
PR #35 appendix remain legacy and open.

## Original defect and pre-fix evidence

The legacy branch (`activateFactory`, baseline `commands_order.go:638-681`) located
the carrier through `DestShipNo`, required a habitat, computed `onCost` as a
**uint16** temporary, computed the hanger need with the **uint16** subtraction
`carrier.MaxHanger-carrier.Hanger`, debited the carrier, rewrote its `Hanger`
and the factory's `Size`, **persisted the carrier**, printed the success text and
returned to `giveOrders`, which then set `On=true`/`Notified=false` and **persisted
the factory separately**. Two transactions, success before durability.

Reproduced on the unchanged baseline through the real actor dispatch
(`State.HandleCommand` → `cmdOrder` → `giveOrders` → `activateFactory`): a responder
raises the existing CR-18 write fence when it receives the success text, which is
the actual boundary between the two writes. Preserved, sanitized evidence in
`docs/testing/evidence/`: `cr17c-habitat-pre-fix.txt` (command, expected exit 1,
full output), `cr17c-habitat-pre-fix-records.json.gz` (complete factory and
carrier records at six stages, actor and store), `cr17c-habitat-pre-fix-fixture.go.txt`
and `cr17c-habitat-pre-fix-reproducer.go.txt`. No credential, password hash or
token appears in the records (scan recorded in the session). The reproducer is
preserved as text, not left as a failing test.

| Stage | Carrier resource/hanger actor, store | Factory on/size actor, store | Observation |
| --- | --- | --- | --- |
| Before | 100/2, 100/2 | false/2, false/2 | complete preimages |
| After carrier write (success text already emitted) | 72/1, 72/1 | false/1, false/2 | carrier committed alone; factory size already rescaled in memory |
| After refused factory write | 72/1, 72/1 | true/1, false/2 | partial record set; premature success |
| Segment, update, restart | 72/1 | false/2 | CR-11 ship reload drops the activation; the debit and the hanger change survive: 28 resources lost, hanger counts 1 for a size-2 factory |

## Maintainer follow-up: current ownership contract

PR #73's initial commit `5ebe816dac788ee150a0c5ace20260aa492f722a`
introduced a same-owner habitat restriction. The maintainer rejected that
restriction. Current activation follows C: an authorized factory owner may
activate the factory while legitimately docked aboard another player's habitat;
that habitat pays the resources and supplies hangar capacity. Habitat ownership
or governor authority grants no authority to operate another player's factory.
The original ledgers and review report below describe the initial revision and
remain unchanged; the cross-owner follow-up evidence supersedes that policy.

## Original C semantics and the arithmetic corrections

`source/order.c:519-551` with `HAB_FACT_ON_COST 4`, `HAB_FACT_SIZE 0.2`
(`tweakables.h:118-121`), `ship_size()` (`build.c:1451-1462`) and unsigned-short
record fields (`ships.h:122,137,266-267`):

| C | Go now (`commandrules.PlanHabitatActivation`, `db.prepareHabitatActivation`) |
| --- | --- |
| `oncost = HAB_FACT_ON_COST*ship->build_cost` is `unsigned int`; refused when `s2->resource < oncost` | `uint32` cost; `uint32(resource) < cost` refuses. The old `uint16(4*BuildCost)` temporary wrapped 16384 to 0 (free activation) and 65535 to 65532; now 16384 and above can never be paid from a uint16 stockpile. Tested at 0, 7, 16383 (cost 65532, payable from 65535), 16384, 32768, 65535 |
| `hangerneeded = (1 + (int)(HAB_FACT_SIZE*(double)ship_size(ship))) - ((s2->max_hanger-s2->hanger)+ship->size)`: every operand is an unsigned short **promoted to int**, so the subtraction is signed and never wraps | int64 intermediates with the same truncating `(int)` of the double; an over-full habitat (max 10, hanger 12, docked size 2) yields free space 0 and `Need 1 more`, where the old Go `uint16(MaxHanger-Hanger)` gave 65534 and would have activated |
| `s2->resource -= oncost; s2->hanger -= ship->size; ship->size = 1 + (int)(0.2*ship_size); s2->hanger += ship->size;` | identical values for accepted inputs; the new size must fit uint16. MaxHanger 65535 with default factory statistics gives ship_size 65537 and new size 13108; all contributing fields at their maximum widths with the default C rates give 69519 and 13904 (both tested). Configured rates are checked separately. `hanger - size + newsize` must lie in [0, 65535]; a stored `hanger < size` would only be processed by C through unsigned-short wraparound and is **refused** (`ErrHabitatHangarState`, "hanger accounting is inconsistent") instead of reproduced |
| carrier mass untouched; factory mass untouched | unchanged; asserted by tests |
| no AP charge | none; zero AP succeeds (test) |
| damaged factory allowed (`damage && type!=OTYPE_FACTORY` is the only damage refusal) | unchanged (Damage 31 fixtures) |
| not a habitat → "The factory is currently being transported." | same text, no mutation |
| `ship->notified = 0; putship(ship)` after the branch | factory `Notified=false` is part of the same transaction |
| `doship.c:110 ship->size = ship_size(ship)` every turn | unchanged in Go (`ship.go:304`): the 0.2-scaled size reverts to `ship_size` at the next turn while the habitat keeps the activation-time hanger, exactly as in C. Asserted, not "fixed" |
| C charges the containing habitat without comparing its owner or governor to the factory operator | Preserved: factory owner/governor authorization is required; a living habitat of another player may supply resources and hangar capacity. Docking, actual chain membership, identity and full persisted preimages remain mandatory. Owning a habitat does not authorize its foreign factory |

Owner/governor authorization, crew (`Max_crew - troops > 0 && popn == 0`),
irradiated (`!Active`), already-on and the explicit-ID rule follow the landed
operation. Legacy output order is preserved: header, activation text, order row.

## Complete transaction participants

`db.HabitatActivation{Player, Governor, Factory, Carrier CommandShip, Context,
Size}`; `Context` is the shared `CommandContext` (alias of the telescope context:
race, star data, stars, planets, ships).

| Role | Records |
| --- | --- |
| **Written** (exactly two checked Puts, factory then carrier) | factory: `On=true`, `Notified=false`, `Size=new`; carrier: `Resource-=cost`, `Hanger=hanger-oldsize+newsize`. Every other field of both ships must be unchanged (closed diff) |
| **Read-only witnesses, checked byte-for-byte inside the transaction** | the player's race (active governor, not dissolved); the universe root when the carrier's chain roots there; every ship of the carrier's child chain (the factory must appear exactly once, each member alive, `WhatOrbits == LevelShip`, `DestShipNo` = carrier); the carrier's own location chain, recursively through any parent carriers to its root star, planet or universe (`telescopeContext.location`), with the carrier appearing exactly once and every member agreeing with the declared orbit; the root planet and star records. The selected factory must also be docked with `WhatDest == LevelShip`; these additional two checks are not claimed for unrelated read-only siblings |
| **Identity rules** | factory: alive, active, `OtypeFactory`, not on, owner = player, governor authority, `WhatOrbits == WhatDest == LevelShip`, docked, `DestShipNo` = carrier; carrier: alive, `StypeHabitat`, ownership independent of player, not the factory itself, same `StOrbits/PNumOrbits` as the factory |

Cycles, duplicates, missing records, a dead parent, a planet outside its star or
a member whose orbit disagrees are refused by the actor stage (HALT: inconsistent
world, no write attempted) and independently by the validator (`INVALID`); any
stored byte difference from the captured preimages is `STALE`.

## Actor ownership and staging design

`handleFactoryOrder` (renamed from the landed-only interceptor) runs before
`cmdOrder` prints or iterates: an explicit numeric id is range-checked before the
legacy `Atoi`→`uint32` conversion, and a landed factory goes to
`runFactoryActivation`, a habitat-docked factory (`WhatOrbits == LevelShip &&
Docked`) to `runHabitatActivation`. Both receive the parsed requested ID, so an
actor map key whose ship has a different Number fails the workspace identity
check rather than activating a different ship. Any other factory falls through to the legacy
path, which now refuses every factory activation without writing. A wildcard or
type selector is inspected read-only and bounded by cycle detection; if it would
reach a landed or habitat-docked factory in the current scope chain, the whole
invocation is refused before any prefix ship can be ordered. Wildcard selectors
only see the current scope's chain (`shipsBySpec`), so a factory docked in a
habitat is addressable by wildcard only from the habitat's own scope; at planet
scope `order * on` still switches the habitat itself, which is unchanged legacy
non-factory behaviour.

`habitatStage` uses the private `commandWorkspace`: scope and race witnesses,
full JSON copies of every ship touched, planet and star copies; the proposal is
applied to the private copies only; `owns()` and `habitatReadOnly()` must hold
before persistence and `owns()` again before installation; installation is two
assignments into the original actor objects (`*actor = *proposed`), addresses
preserved, no callback between them; the staged header/activation/order-row text
(bounded) is released only afterwards. The new test hook `afterCommandInstall`
(production never sets it) marks the post-installation, pre-response boundary.

## Transaction fault matrix (actual dispatch, `TestCR17CHabitatWriteFailures` and db tests)

| Fault | Outcome | Store | Actor | Engine |
| --- | --- | --- | --- | --- |
| validation (resources, hanger, inconsistent hanger, auth, inactive, crew, on, transported) | VALIDATION_FAILED | unchanged | unchanged | SERVING |
| `habitat.before`, `.validated`, `.write:1`, `.write:2`, `.before-commit` (error or panic) | NOT_COMMITTED | unchanged (whole-store snapshot equal) | unchanged | HALTED, no shutdown flush, no turn admitted |
| write fence active | NOT_COMMITTED, `COMMAND_FENCED`, counted once | unchanged | unchanged | HALTED |
| `habitat.commit-error` (error after the Update returned) | UNCERTAIN; readback `proposal_visible`/`unavailable`; guard set | both records visible, never promoted to durable | nothing installed | HALTED; a new actor on the same handle stays halted; dump/factory/telescope/habitat operations all refuse |
| `habitat.after-commit`, response error/panic, post-install panic | COMMITTED (reporting error) | both records committed | complete proposal installed (post-install panic: installation kept) | HALTED; no rollback claim |
| identity changed before install (witness pointer replaced) | COMMITTED_ERROR | committed | **nothing** installed | HALTED |
| diagnostic readback unavailable | UNCERTAIN stays UNCERTAIN | visible | nothing installed | HALTED |
| admitted command vs. second activation / quiesce / shutdown / caller cancellation | serialized; the admitted operation completes once; the second activation sees "already activated" | one commit | one install | follows the subsequent action: SERVING, QUIESCED or STOPPED |
| queued behind a running turn and canceled | never executes | unchanged | unchanged | SERVING |

A failed commit never persists only the resource debit or only the activation:
both writes sit in one `bolt.Update` after all preimage checks.

## Six real SIGKILL boundaries, each with same-owner and cross-owner habitats

The child process (`TestCR17CHabitatCrashChild`, only in `_test.go`) opens the
disposable store, blocks at the selected boundary after printing a ready marker,
and is killed with SIGKILL; the parent verifies the kill, reopens the store and
restarts an actor. The Linux host/CI harness executes its fixed `/proc/self/exe`
test binary, captures child stderr and fails if it contains `WARNING: DATA RACE`;
an intentional SIGKILL is not treated as proof that child diagnostics were clean.

| Boundary | Restart shows |
| --- | --- |
| `habitat.before` (before the transaction) | complete original factory/carrier pair; complete logical record snapshot unchanged |
| `habitat.write:1` (after the factory Put) | complete original pair |
| `habitat.write:2` (after the carrier Put, before commit) | complete original pair |
| `habitat.before-commit` | complete original pair |
| `habitat.after-commit` (confirmed commit, before installation) | complete activated pair (factory on, size 1; carrier 72/1), rest of the world and allocator unchanged |
| `habitat.installed` (after installation, before the response; the post-install hook) | complete activated pair |

After each restart: actor and store agree, the planet chain and the carrier's
docked chain are valid, the government ship is found, a segment and a full update
run and preserve the result (factory on, 72 resources, hanger 1; factory size
back to `ship_size` per the turn-time rule), and the ship free list is unchanged.
The six boundaries now run for both carrier owners (12 actual SIGKILL cases).
No kill inside bbolt's own fsync path is claimed; uncertain outcomes are not
restarted as a retry.

## Permanent tests

| Test | What executes / asserts |
| --- | --- |
| `commandrules.TestHabitat*` (4) | defined order.c arithmetic, C-width cost boundaries, signed hanger arithmetic incl. over-full and maximal sizes, malformed-state refusal, invalid inputs |
| `db.TestHabitatActivation*` | atomic commit and reopen (metadata, allocator, mass unchanged); failure atomicity at five stages × error/panic; committed vs uncertain with readback and cross-family guard; 44 invalid field/identity/authority/context cases plus an independent coherent foreign-factory authorization rejection; chain integrity (factory absent/duplicated, witness on other carrier, carrier absent from planet chain, planet chain cycle, dead witness, planet outside star); stale preimages for every participant incl. after preparation and noncanonical bytes; C-width cost/hanger/no-AP; fence and concurrent-adapter conflict |
| `game.TestCR17CHabitat*` (current counts in the cross-owner follow-up evidence) | complete actor/store state and stable addresses, segment/update/restart; write failures at every stage incl. fence and halted shutdown; selectors and validation texts; C arithmetic through the dispatcher; nested habitat, star-orbit and universe-root carriers; stale/inconsistent worlds incl. legacy load, chain cycles, missing/dead carrier, mismatched links, replaced witness; readback/install/post-install failures; response failure after commit; enrollment and mode compatibility plus the landed path; output and private state wait for commit; admitted command vs following actions; queued behind a turn; internal completion classes; legacy bypass, mixed prefix at carrier scope, authorized governor, bounded response, pending-AP admission; requested-ID map-alias refusal with no shutdown flush; the six crash boundaries |

## Authorization, selectors and remaining unsafe paths

Owner and governor (active, authorized) are checked for the factory. Carrier
ownership is independent; an authorized factory can charge its foreign habitat. Numeric overflow and junk ids are refused before
any lookup. `activateFactory` is now a pure refusal for landed, habitat and
other locations; `giveOrders` cannot set `On` for a factory. Not changed and still
open: `land` onto a ship in Go does not set the `WhatOrbits/WhatDest/DestShipNo`
linkage that C's `insert_sh_ship` sets (so a Go-landed factory cannot reach this
branch until that is fixed; the build-by-habitat path sets the linkage and is the
one exercised), the double hanger accounting in `doLandOnShip` (`insertShipInShip`
adds the size and the caller adds it again), cargo and other direct saves (PR #35
appendix), CR-11 ship reload semantics in general, CR-17 group commit, durable
command ids and the outbox (separate design approvals).

## Lint baseline and labels

Exactly five stale entries are removed from `docs/testing/lint-baseline.txt`
(three unchecked `sess.Send` and two G115 conversions of the retired habitat
branch); total 1,005 → 1,000; no baseline allowance is added or moved and no scanner
threshold is weakened. One local gosec annotation documents the resource narrowing
already bounded by `cost <= Resource <= MaxUint16`; the crash harness now uses the
fixed Linux `/proc/self/exe` path without a process-execution annotation. The new
code is lint-clean under the same configuration. The growth gate classifies this as
a pure reduction, but under CLAUDE.md the maintainer must still apply
**`known-defect-added`**; a local simulated label is not acknowledgment. No
protected gate tooling, workflow, golden allowance, original C or known-defect
inventory entry changed, so `gate-change-reviewed` is not required. The agent
applies no label.

## Claude's pre-handoff validation (historical, Go 1.27.4)

The original execution ledger `docs/testing/evidence/cr17c-habitat-validation.json`
is preserved unchanged with its commands, source hashes and output hashes. These
results apply to Claude's original staged source, before the review corrections
below; they are not fresh Codex executions or final-source validation.

| Check | Result |
| --- | --- |
| gofmt / `go build ./...` / `go vet ./...` | PASS |
| `go test -count=1 -json ./...` | PASS; 3,915 passing nodes, 2 opt-in long tests skipped, all 21 packages ok |
| `GB_LONG_TESTS=1 go test -count=1 -race -json ./...` | PASS; 3,917 passing nodes, zero skips, zero failures, no data race reported |
| habitat coverage | 224 nodes / 28 top-level entries pass (rules, db, game incl. the child harness) |
| original C fixture reproduction (`make -C test/ctool check`) | PASS; fixtures reproduce exactly |
| known-defect inventory (`test/scripts/inventory-check.sh`) | PASS; all tested entries reconciled, no inventory change |
| golden mutations (`test/scripts/golden-mutations.sh`) | PASS; all fifteen scenarios behaved as required |
| lint gate | PASS; 1,000 baseline / 1,000 report / 1,000 matched, 0 new, 0 stale (after the five removals) |
| growth gate | PASS; "allowances reduced only (5 removals)", no protected tooling delta; local label simulation only |
| gate self-tests (lint, approval, golden-mutations helpers) | PASS |
| offline manifests (`deploy/kustomize/tests/run.sh --require-published-image`) | PASS; 21 resources valid |
| workflow helper tests / policy | PASS; 188 tests, 17 policy checks |
| strict govulncheck v1.9.0 | PASS; 0 reachable, 17 non-reachable module findings, no suppression |
| `go mod verify` / `go mod tidy -diff` | PASS; no drift |
| `git diff --check` | clean |

Docker/buildx, Trivy, Syft and kind remain hosted GitHub Actions checks.

## Compatibility

Executed by Claude's pre-handoff full suites above (the final Codex runs below repeat these): CR-07A–F enrollment and admission
guards (including `ENROLLMENT_PENDING_TURN`, retained and retested after a
habitat activation), CR-17A route/stockpile transfer, CR-17B atomic exploration
dump, PR #35 landed factory activation (its tests and a fresh landed activation
through the shared interceptor), PR #37 telescope aiming, CR-18 atomic turns and
write fencing (fence case in the fault matrix; turns after every crash scenario),
CR-43 durable maintenance and shutdown (quiesce during an admitted activation,
restart stays QUIESCED), PR #34 WebSocket shutdown close frame (net package
suite). Data format impact: none (existing ship JSON, buckets and keys; no commit
kind, schema, allocator, command-id or reconciliation mechanism added).

## Independent review of initial delivery at `5ebe816`

A separate noninteractive reviewer agent (`/root/habitat_independent_review`),
independent of Claude's implementation and the Codex correction author, inspected
the C source, transaction and actor code, complete tests and historical evidence.
The initial task was bounded to 12 minutes and follow-ups to six minutes. Its
own race subprocesses used explicit 240-second and 180-second `timeout` limits;
both recorded exit 0, first 224 then 227 passing nodes, with all six SIGKILL
cases. The review mechanism has no interactive stdin prompt. A tool-managed
agent reports completion status rather than an invented operating-system exit
code; actual test exit statuses and log hashes are in the Codex ledger.

The earlier CLI attempts produced no final review. The handoff reports an initial
stdin wait; retained `codex-review-1.err` and `codex-review-2.err` show prompts were
received and source inspection occurred before termination. Those logs alone do
not establish why an attempt was reported hung. Neither incomplete attempt is
counted as approval. The bounded replacement completed code and documentation
review; final evidence-audit disposition is recorded in the preserved review
report linked below.

| Finding | Correction and evidence |
| --- | --- |
| P1: selector discarded the requested ID and passed the map value's Number | Pass the parsed ID to both shared factory staging branches. A real-dispatch counterexample with map key 77 pointing at otherwise valid factory 78 (valid carrier membership) activated 78 and debited 28 before the fix, for both `#77` and `77`. `TestCR17CHabitatRequestedShipIdentity` failed before correction (exit 1, 0.170s) and passed afterwards (exit 0, 0.182s); final suites verify HALTED, no success, complete actor/store snapshots unchanged and no shutdown flush. |
| P2: crash-child stderr discarded | Capture stderr, reject race-detector warnings after Wait, retain all six SIGKILL boundaries, and use fixed Linux `/proc/self/exe` without a G204 annotation. Fresh independent and full race suites pass. |
| P3: sibling Docked validation overstated | Participant table now states the actual alive/orbit/parent/membership checks and distinguishes the selected factory's Docked/WhatDest checks. No unrelated telescope validation was widened. |
| P3: default factory misdescribed as all-width maximum | Correct values and add all-contributing-fields maximum assertion: ship_size 69519, activation size 13904. |
| P3: lint prose overstated absence of suppressions | Document the one proven resource-narrowing annotation; no allowance addition or scanner threshold weakening. |

No original assertion, crash phase, golden allowance or safety gate was weakened.
The original staged patch/index and Claude's source hashes were privately preserved
before corrections. The original pre-fix artifacts and original execution ledger
remain unchanged. The additional Codex ledger and independent-review report
preserve the takeover evidence without committing raw transport logs or secrets.

## Codex initial delivery execution at `5ebe816` (2026-11-12 UTC)

[Initial Codex execution ledger](evidence/cr17c-habitat-codex-validation.json) records
source/baseline SHA-256 values, commands, exit statuses and output hashes.
[Independent review](evidence/cr17c-habitat-independent-review.md) retains the
initial findings, correction dispositions and final audit.

| Check | Fresh final result |
| --- | --- |
| Go 1.27.4 / GOTOOLCHAIN=local / gofmt / build / vet | PASS |
| `go test -count=1 -json ./...` | 3,918 passing nodes / 416 top-level passes; two existing opt-in long tests skipped; 21 tested packages pass |
| `GB_LONG_TESTS=1 go test -count=1 -race -json ./...` | 3,920 passing nodes / 418 top-level passes; zero test skips/failures; no race warnings |
| `go test -count=1 -race -json ./internal/db ./internal/game` | 1,375 passing nodes, zero test skips/failures |
| Habitat coverage | 227 passing nodes / 29 top-level entries in full and independent three-package runs; core db/game run covers 214 / 25 (rules excluded); all six real SIGKILL scenarios pass in each |
| C fixtures / known-defect inventory / golden parity | PASS; fixtures reproduce; tested entries reconcile; golden summary 1,248 PASS / 143 XFAIL / 0 XPASS / 0 FAIL |
| All 15 golden mutations / gate self-tests | PASS; expected diagnostics and failures retained; helper cases repeated 100 times |
| Lint / growth | 1,000 baseline/report/matched, zero new/stale; five exact removals; growth run with no simulated acknowledgment label; no protected tooling delta |
| Offline manifests / workflow checks | 21 resources valid, 0 invalid/errors/skips; actionlint and workflow ShellCheck pass; 188 helper tests and 17 policy checks pass |
| Strict govulncheck v1.9.0 | Zero reachable vulnerabilities; 17 non-reachable module findings reported, not suppressed |
| Module verify / tidy diff / staged and working diff checks | PASS; no module drift |

Initial restricted full-suite attempts failed because the sandbox disallowed
loopback listeners; pinned-tool/scanner and lint self-test attempts also failed
on restricted DNS/download access. These failed attempts are retained separately
in the final ledger; authorized reruns supplied disposable loopback networking
and pinned tool/schema downloads and passed. No failing assertion was removed.
A final test-comment arithmetic correction changed no executable behavior;
the complete core race and final pure-rule race checks cover the final files.

The final suites execute CR-07A–F, CR-17A/B, landed factory, telescope, CR-18,
CR-43 and WebSocket shutdown regressions, including actual CLI/HTTP/WebSocket
subprocess acceptance and crash recovery. CR-07's `ENROLLMENT_PENDING_TURN`
remains conservative. No new earlier binary, physical disk failure or kill inside
bbolt fsync is claimed. Docker/buildx, Trivy, Syft and kind remain hosted CI.


## Verdict and remaining CR-17C work

**CR-17C Habitat: PASS WITH CONDITIONS** for exact-ID habitat factory activation.
Independent code/document review found no remaining blocking defect after the
corrections. Final evidence-audit approval is preserved in the review report.
Conditions are normal maintainer review, the maintainer-applied
`known-defect-added` acknowledgment and green hosted CI; this is not permission
to merge or deploy. `gate-change-reviewed` is not required by the actual diff.

The maintainer rejected the initial same-owner carrier restriction; the current
operation restores C's ownership-independent activation. C's turn-time factory-size recomputation
is preserved, including the unchanged activation-time habitat hanger accounting.
Go land-on-ship linkage and double hanger accounting remain deferred; this PR
does not repair those paths or broaden its atomicity guarantee to cargo,
population/assault, transporters, movement/navigation or construction.

CR-17C follow-ups must migrate complete record/effect/AP boundaries for those
commands. Durable command IDs, retention, deduplication, outbox and group commit
need separate reviewed scope. CR-17D must broaden transport/concurrency/crash and
allocator acceptance once those command paths are migrated. CR-17, CR-11 and
CR-20 remain OPEN. Database-format and CR-18 commit-kind impact: NONE.

No live database, running game, Kubernetes, Argo CD, Longhorn, image publication,
DNS, secret, ingress or deployment operation occurred. Other worktrees and the
retained audit branch were preserved.


## PR #73 cross-owner correction — maintainer-directed C parity

The follow-up starts from `5ebe816dac788ee150a0c5ace20260aa492f722a`.
Only two production conditions were removed: `carrier.Owner == player` in the
actor and `c.Owner == in.Player` in the closed database validator. Factory owner,
active governor permission, eligible/live/active factory, living habitat type,
destination/orbit links, actual parent/child membership and all full preimages
remain checked. No carrier-owner or carrier-governor permission replaces those
removed conditions. The shared selector, pure rules and recursive actor/database
context validators were inspected; their relevant ownership checks apply only
to the selected factory/caller, not read-only carrier or ancestor witnesses.

The complete carrier expected/proposed record remains a mandatory participant.
The factory's authorized caller race is the only race context required: the
operation reads no carrier-owner race attributes and changes neither race.
Exactly factory On/Notified/Size and carrier Resource/Hanger still commit in
one transaction. Full canonical preimages (including the foreign Owner field)
are compared inside that transaction. A change to the foreign carrier's owner
or resources after proposal construction rejects without a partial write,
actor installation, success output or stale shutdown flush.

### Fresh regression and authority evidence

The new actor fixture enrolls an actual second empire before installing player
1's factory aboard player 2's habitat; it does not fabricate a dangling owner.
The desired cross-owner actor and database/reopen tests were first run against
the two old restrictions and failed (exit 1). After removal, complete immediate
actor/store proposals agree: factory remains owner 1 and becomes On=true, Notified=false,
Size=1; player 2's habitat remains owner 2, pays 28 resources (100 → 72), and its
hangar changes 2 → 1. Full unrelated world/allocator/placement/metadata snapshots
remain unchanged. Stable object addresses and delayed output assertions remain.
A restart **before any turn** retains this complete cross-owner result.

Positive database/reopen and real actor/turn/restart tests now run for both
carrier owners. The entire actor error/panic/fence/committed-reporting/uncertainty
matrix and all six SIGKILL boundaries also run for both owners (12 actual kill
cases). Every kill reopens only a known precommit or confirmed-postcommit store
and checks the full old or complete new world, unrelated records, allocator,
restart and subsequent segment/update. No genuine uncertainty is auto-repaired.
An authorized nonzero factory governor also activates inside the foreign habitat.

The former negative foreign-carrier test is removed. Its inverse remains
forbidden: player 1 cannot activate player 2's factory inside player 1's habitat.
Actual dispatch, direct staged entry and a separate database request with
coherent expected/proposed factory records independently reject this case.
Changing carrier ownership grants no factory authority.

### Existing CR-13 turn behavior is distinct from activation

The expanded test initially asserted both owners would remain unchanged after
a segment. Fresh execution disproved that new test assumption; the failed runs
are retained in the follow-up ledger and reviewer report. Existing Go
`ship.go:306-312` synchronizes a containing ship's Owner/Governor from a carried
ship during `doShip`, matching the isolated assignment in C `doship.c:112-118`.
But C `doturn.c:202-206` first calls `doown` (`doship.c:223-232`) to propagate
carrier ownership down to children, and Go omits that prepass (already-open
**CR-13**). Therefore a later Go segment in this fixture changes the habitat's
Owner to 1, keeping its Race field 2. Activation itself and the pre-turn restart
keep Owner=2. Tests assert these explicit current-engine values, retained
activation/resource/hangar fields and valid chains after segment/update/restart;
they do not claim whole-turn cross-owner C parity. No turn or ownership-sync
code was changed. CR-13 remains open alongside CR-11/CR-17/CR-20.

### Follow-up validation and review

Fresh execution and independent review are recorded separately in
[evidence/cr17c-habitat-crossowner-validation.json](evidence/cr17c-habitat-crossowner-validation.json)
and [the cross-owner review](evidence/cr17c-habitat-crossowner-review.md).
Claude's original artifacts and both earlier validation ledgers/review history
remain unchanged. The current commit adds no lint allowance or protected tooling
change; maintainer `known-defect-added` remains required for the original five
removals, and the agent applies no label.


Fresh follow-up execution (Go 1.27.4, `GOTOOLCHAIN=local`, disposable stores):

| Check | Actual result |
| --- | --- |
| `go test -count=1 -json ./...` | 3,951 passing nodes / 419 top-level passes; two existing opt-in skips; 21 tested packages pass |
| `GB_LONG_TESTS=1 go test -count=1 -race -json ./...` | 3,953 passing nodes / 421 top-level passes; no test skips/failures/race warnings |
| Habitat normal and independent race coverage | 260 passing nodes / 32 top-level entries; all 12 SIGKILL leaves pass |
| Build / gofmt / vet / C fixtures / module verify and tidy diff | PASS; no drift |
| Inventory / golden parity / all 15 mutations | PASS; 1,248 PASS / 143 XFAIL / 0 XPASS / 0 FAIL golden cases; all expected mutation outcomes retained |
| Lint / growth / gate self-tests | 1,000 baseline/report/matched, zero new/stale; no follow-up baseline change; no simulated acknowledgment label; all self-tests pass |
| Offline manifests / workflow | 21 valid resources; actionlint and ShellCheck pass; 188 helper tests / 17 policy checks pass |
| Strict govulncheck v1.9.0 | Zero reachable vulnerabilities; 17 non-reachable module findings remain reported |
| Working/staged diff checks | Clean |

A fresh independent noninteractive reviewer inspected factory authority, the
foreign carrier and all transaction participants, ownership-independent witness
traversal, inverse authorization, delayed effects, full preimages and failure/
crash behavior. Its own timeout-bounded race run exited 0 with all 260 nodes and
12 actual SIGKILL cases passing. It independently confirmed CR-13's omitted C
prepass and the explicit pre-turn/post-turn distinction; the failed initial
ownership-invariance assumption is preserved rather than rewritten as a pass.
The follow-up review report records code approval and the final evidence audit.

Verdict: **PASS WITH CONDITIONS** for this maintainer-directed ownership fix.
The condition remains normal maintainer review/`known-defect-added` for the
original five lint removals and hosted CI. No `gate-change-reviewed` label is
required; no label was applied. CR-13, CR-11, CR-17 and CR-20 remain open.
No game turn code, schema, commit kind, authentication or public interface was
changed, and no live game/database, deployment, Kubernetes or image publication
operation occurred.
