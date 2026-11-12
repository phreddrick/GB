# CR-17C: atomic noncombat cargo — implementation record

Baseline: `5c3c8912330f77fbc81781069cff39cbb3014321`.
Branch/worktree: `feature/cr17c-noncombat-cargo`, `/tmp/GB-cr17c-cargo`.
Issue #40, parent #38. All stores/processes are disposable. No live operations.

## Pre-code inventory and design

The actual `doLoad` (commands_load.go:59–458) reads actor race, directory,
selected ship, partner, star/StarData AP, but obtains an independent mutable
planet from `Store.GetPlanet`. Numeric selection checks owner/alive but lacks
C's `authorized(governor, ship)` guard. No valid path is atomic as an invocation.

| Variant | Actor reads/mutations | Store order | AP and externally visible effects | Failure/stale consequence |
| --- | --- | --- | --- | --- |
| Planet → selected ship `r` | Selected Resource/Mass; actor planet untouched; independent planet Info[player].Resource debited | PutPlanet, final saveShip | Location and transferred text before ship write; Go checks 1 AP then deducts 1 in memory after saves | Stored planet/ship split; stale actor planet later refunds stockpile; AP not durable |
| Selected ship → planet `r` | Selected Resource/Mass; independent planet player's Resource credited | PutPlanet, final saveShip | Same early output/AP | Lost or duplicated resources after a failed later write/turn |
| Planet ↔ selected `x` or `&` | Selected Crystals; independent planet player's Crystals; no mass change | PutPlanet, final saveShip | Early transferred text, volatile Go AP | Same split/stale/acknowledgment risk |
| Partner → selected `r` | Both Resource; selected Mass increased; partner Mass decreases except selected carried inside partner | saveShip(partner), optional transporter, saveShip(selected) | Partner/location/owner text and transfer before saves; volatile Go AP after | Cargo/mass split; transporter adds third ship/effects |
| Selected → partner `r` | Both Resource; opposite mass deltas with same containing-carrier exemption | Same independent saves | Same | Resource gifting to foreign partner allowed; loading foreign stock forbidden |
| Ship ↔ ship crystals | Both Crystals; neither mass changes; different-owner limits both0 | Same independent saves | Same | Cross-owner nonzero crystals forbidden; default0 may be a no-op |
| Invalid docked partner | Legacy can clear Docked/WhatDest and save selected alone | Separate saveShip before rejection | Undock text | Navigation mutation must not escape a staged invocation |
| Active targeted transporter selected | doTransporter can change another ship and emit/live/store effects | Additional independent writes before final selected save | Extra notices/effects | Exclude, fail closed; deferred issue #42 |
| Crew/troops/fuel/destruct and other commands | Additional sector/population/combat/race/AP, transporter, news/telegram effects | Several independent writes | Unbuffered effects | Remain legacy; no general command guarantee |

Original C `source/load.c:54–480` authorizes the selected owner/governor; requires
active/docked selected ship; validates containing membership or reciprocal peer
docking, partner alive and not overloaded when carried. Planet stockpiles always
belong to the selected operator, independent of sector ownership. Planet scope
checks only star/planet coordinates, not an extra LevelPlan requirement. C uses
signed int amount: unload negates input, then checks [lolim,uplim], then expands
zero/omitted to uplim (load) or lolim (unload). Negative inputs reverse direction.
For resources, foreign partner uplim=0 but unloading is permitted; foreign
crystals have uplim=lolim=0. Full/overfull limits are not blindly clamped to zero.
Shuttle resource capacity exemptions apply only to external ship/ship docking,
not planet loading or selected ships contained in another ship.

Important source discrepancies discovered before implementation:

- C dispatcher GB_server.c:1557/1625 passes APcount=0, and load.c never calls
  deductAPs. Current Go checks/debits1. The owner explicitly approved
  original-C zero AP; no AP charge or AP write is added. Star/universe context
  remains a complete read-only preimage. After-AP-write crash tests therefore do
  not apply; after-all-cargo-writes and post-installation boundaries replace them.
- C ships.h:302 crystal capacity is127; Go uses Mount (0/1). Supported crystal
  calculations need C capacity 127, separately documented from persistence.
- C resource partner-mass test calls landed_on(s, sh) with Boolean sh=1 rather
  than partner ID; Go uses the real partner ID. The owner explicitly approved retaining
  Go's intended containing-carrier aggregate exemption; document the C Boolean
  typo rather than reproducing ship-#1-dependent mass behavior.
- C accepts '&' for limits but its mutation switch only handles'x'. User explicitly
  authorizes the working crystal alias; retain it as equivalent to'x'.
- C peer check compares partner.DestShipNo to selected.Number; Go compares to
  selected.Ships and may wrongly undock. New staging must validate actual links,
  reject broken links without repair, and document this correction.

The proposed closed typed API will capture full immutable actor preimages,
private ship/planet proposals and read-only AP witnesses, caller/scope and complete context chains, check
all preimages inside one bbolt transaction, permit only supported cargo/mass
changes and no AP changes, install stable actor objects after confirmed persistence, and release
bounded effects afterwards. Existing mutex/fence/outcome/readback/uncertainty
mechanisms remain intact. No generic writer, schema, allocator, command ID,
outbox, commit kind or automatic recovery is authorized.

Implementation, reproduced failures, reviewed final scope and fresh results follow.

## Migrated operation and C behavior matrix

`doLoad` intercepts all commodity tokens beginning with `r`, `x` or `&` before
legacy selection, output, repair or mutation. Only an exact supported commodity,
one positive uint32 ship ID (optional `#`) and an optional signed int32 decimal
amount are accepted. Extra arguments, malformed aliases, overflow, wildcard/type/
list selections and unsupported locations never fall back to the old writer.
The selected ship must be alive, active, docked, owned by the caller and
operable by that governor (governor0 or its assigned governor); the caller's race
and governor must be active. No extra crew, damage, On or partner-governor rule
is introduced. The partner can have a different owner.

| Commodity | Recipient/context | Load positive | Unload positive | Omitted / literal0 | Negative argument |
| --- | --- | --- | --- | --- | --- |
| Resource `r` | Planet | Debit caller's stockpile, credit ship within capacity | Debit ship, credit caller's stockpile | Maximum load / all unload after C limit check | Reverse direction with the same signed limits |
| Resource `r` | Own contained carrier or reciprocal peer | Debit partner, credit selected | Debit selected, credit partner | C upper / lower limit | Reverse direction |
| Resource `r` | Foreign contained carrier or reciprocal peer | Forbidden if nonzero taking | Gift permitted within recipient limit | Load 0 no-op; unload maximum permitted gift | Negative load gifts; negative unload cannot take |
| Crystal `x` or `&` | Planet | Caller stockpile → selected, capacity 127 | Selected → caller stockpile | C upper / lower limit | Reverse direction |
| Crystal `x` or `&` | Own carrier/peer | Partner → selected, capacity 127 | Selected → partner, capacity 127 | C upper / lower limit | Reverse direction |
| Crystal `x` or `&` | Foreign carrier/peer | Nonzero forbidden | Nonzero forbidden | Zero/no-op allowed at limits 0,0 | Nonzero forbidden |

The signed limits are calculated before default expansion, as in
`load.c:170–256`. Over-full holds retain C's signed limits rather than being
silently clamped. VN negative-direction refusal occurs before default expansion;
that ordering is preserved too. Factories use `ships.h`'s effective cargo
capacity. External ship/ship shuttles have C's resource-capacity exception;
planet loading and selected ships carried inside another ship do not.
Resource mass follows the effective signed quantity and server MassResource;
crystals do not change mass. The containing carrier retains its aggregate mass,
as explicitly approved by the owner. All results must fit the actual uint16
fields; stockpile/shuttle overflow and invalid/nonfinite/negative resulting mass
are rejected without mutation. C's unsigned wrapping, atoi overflow and INT_MIN
negation are not reproduced. Tests cover these boundaries without changing C.

Separate behavior corrections are explicit: original-C zero AP replaces Go's
old 1 AP; crystals use C capacity 127 rather than Go's Mount flag; `&` works as requested;
selected-governor authority is enforced; reciprocal docking uses the selected
ship's actual number rather than Go's erroneous child-list head. Star-level
peers compare their meaningful star coordinate, not stale planet numbers.
Invalid links are refused without legacy undocking. C's Boolean `sh` mass typo
is intentionally not reproduced, per the owner's decision above.

## Transaction and actor ownership

`commandrules.PlanCargo` is pure, checked arithmetic shared by actor preparation
and the independent storage whitelist. `State.cargoStage` reuses the existing
private typed `commandWorkspace`, with complete deep copies of ships and planet
sectors. `db.CargoTransfer` is a new closed operation; existing dump, factory,
habitat and telescope contracts are unchanged.

Writable participants are exactly:

1. The selected ship: Resource or Crystals, and Mass for resources.
2. One recipient: partner ship's same commodity and applicable Mass, **or** the
   caller's commodity field in Planet.Info. No other player or sector changes.

Read-only participants are the caller race/governor; root star or universe data;
root planet where relevant; complete root orbital and contained child lists;
recursive containing parents for both ships; and every linked witness needed to
prove live unique membership. The actor also captures and rechecks its directory.
Unrelated descendants are preserved but are not inputs to cargo arithmetic;
this is complete required location/membership validation, not a global ship-tree
or aggregate-mass audit. Map keys must agree with record numbers. Missing, dead, cyclic, duplicate,
nonreciprocal or inconsistent contexts fail closed. Full star/universe AP fields
are checked but never written. No artificial AP write, allocator operation,
news/telegram, directory mutation or transporter effect is part of this command.

The database rechecks caller authority, complete context, exact capacities,
conservation and full proposed-record equality against the allowed changes.
Complete canonical persisted preimages are compared **inside the same bbolt
write transaction**, before either Put. Write order is selected ship then
recipient. Every Put is checked; no partial participant set can commit. The
existing command mutex, shared uncertainty guard and CR-18 fence are retained.
The fence is checked both before and inside the transaction; refusal accounting
is preserved. Strict durable maintenance metadata is checked inside it too.

After confirmed commit, the actor rechecks all borrowed identities/preimages,
then assigns both complete private proposals to the existing object addresses,
with no callback between assignments. Read-only witnesses are never installed.
Local output is bounded by the existing commandEffects limits and released only
after coherent installation. No success output escapes an aborted operation.

| Outcome | Durable/actor behavior | Caller and recovery behavior |
| --- | --- | --- |
| Validation failure | No write or installation, no AP change | Error only, no transfer confirmation |
| Confirmed abort | All old records and actor objects retained | No success; persistence/integrity failure HALTs conservatively |
| Confirmed commit | Both records durable, installed together if identities agree | Success only after installation |
| Confirmed commit, reporting/delivery failure | Confirmed records remain; installed state retained | HALT, no rollback claim or stale shutdown flush |
| Commit outcome uncertain | No unverified installation; shared Store guard remains set | HALT for operator reconciliation; diagnostic readback is not fsync proof |
| Commit confirmed, installation identity invalid | Durable pair retained; no partial installation | HALT before any turn/shutdown can overwrite it |

There is no automatic retry, reopen, repair, durable command ID or outbox. A lost
response can follow a complete commit. Clients must not blindly repeat a
non-idempotent cargo command; operator diagnosis is required for ambiguity.
Database format, buckets, metadata schema, CR-18 kinds/sequences and WorldDigest:
**UNCHANGED**. Between-turn writes retain the already established command model.

## Pre-fix reproduction and causal regressions

Before implementation, `TestCargoAtomicTransfer` exercised real actor dispatch
for 12 combinations: planet/contained partner × r/x/& × load/unload. A responder
raised the existing write fence after the real planet write; a private
`afterLegacyShipWrite` hook raised it after the real partner PutShip. The latter
is only test-configurable and is never used by the new cargo operation. Neither
hook invents the original independent-write ordering.

All12 expected atomicity tests failed at baseline. Example resource loading:
planet stock100→99 persisted while selected stock10 stayed10 (one lost);
unloading left10/101 (one duplicated). Contained partner loading left10/29;
unloading left10/31. Actor AP became4 while stored AP remained5. The original
actor planet was stale and later segment/update/shutdown/restart snapshots show
the resulting disagreement and overwrite. Full affected records, including
sectors and orbit links, are preserved in the sanitized evidence below.
The fixture sets Mount127 only to isolate the old persistence defect from the
separate old crystal-capacity defect; new gameplay tests use Mount0 and prove127.

Historical evidence is preserved, not retroactively rewritten:

- `evidence/cr17c-cargo-pre-fix.txt`: boundary table and baseline.
- `evidence/cr17c-cargo-pre-fix-fixture.go.txt`: exact reproduction fixture.
- `evidence/cr17c-cargo-pre-fix-hook.patch.gz`: exact test-only instrumentation,
  losslessly compressed because unified-diff context whitespace is itself
  flagged when a patch is added as text; original bytes remain unchanged.
- `evidence/cr17c-cargo-pre-fix-records.json.gz`: complete six-boundary snapshots
  (6,389,297 uncompressed bytes), containing ships/planet/star, no race secrets.

The permanent regression now passes through segment, update, graceful shutdown
and restart. A fresh Go overlay replacing **only** commands_load.go with the
baseline file makes all 12 cases fail again, exit1, specifically on cargo loss/
duplication. Owner/governor guard-deletion overlays independently make coherent
DB authority tests fail because an unauthorized command commits. Review also
found and reproduced an artificial star-peer planet-number check; the permanent
positive star-peer test failed before correction and passes afterwards.

## Automated acceptance and failure matrix

| Requirement | Fresh automated evidence |
| --- | --- |
| Signed/default amounts, minima/caps, overfull, VN, shuttle, mass, overflow/NaN/Inf | commandrules.TestCargoSignedDefaultsAndConservation; TestCargoCapacityOwnershipAndBoundaries |
| Complete r/x/& × load/unload × omitted/0/positive/negative × planet/carried/peer matrix | game.TestCargoGameplayMatrix (72 leaf cases), whole-world expected-record assertions, stable pointers, AP 0 |
| Foreign gifts/restricted taking, foreign crystals, selected/governor authority; star/universe roots | TestCargoForeignOwnershipAndUniverse; db.TestCargoStoreCoherentAuthorityAndCrystalContracts |
| Real original defect and later persistence | TestCargoAtomicTransfer,12 cases; source-overlay counterexample |
| Missing/broken/stale contexts, mixed selectors, invalid IDs, transporter exclusion | TestCargoInvalidSelectorsAndDeferredPaths; db.TestCargoStoreClosedContract; TestCargoStoreStalePreimagesFenceAndMaintenance |
| Before/check/validated/write1/write2/precommit/commit-report/aftercommit errors and panics | TestCargoStoreFailuresAndOutcomes; TestCargoActorFailureOutcomes, both recipient families; complete old/new snapshots |
| Readback, identity/install, reply errors/panics, no stale HALTED flush | TestCargoInstallationReadbackAndDelivery; shared guard/refusal assertions |
| Concurrent admitted command, quiesce/shutdown, cancel after start | TestCargoAdmittedOperationAndModes, channel barrier inside open transaction |
| Queued canceled command and command following a segment | TestCargoQueuedBehindTurnAndCanceled |
| QUIESCED restart/refusal, HALTED admission | TestCargoAdmittedOperationAndModes; TestCargoHaltedAdmission |
| Immediate actor coherence and enrollment, adjacent factory edit | TestCargoFollowingEditsAndEnrollment |
| Disposable reopen, full world/auxiliary preservation, concurrent stale proposals | db.TestCargoStoreAtomicReopenAndInputs; TestCargoStoreStalePreimagesFenceAndMaintenance |
| Actual process interruption, restart and following segment/update | TestCargoRealProcessCrash,12 SIGKILL leaves |

Every supported transfer has two writes. The six real SIGKILL boundaries are:
before transaction, after selected write, after recipient write, immediately
before commit, after confirmed commit before actor install, and after install
before response. Each runs for planet and contained-partner recipients (12 real
kills). Child code exists only in `_test.go`, executes this exact test binary,
waits on a boundary marker and is verified terminated by SIGKILL. Aborted
boundaries compare the entire original logical database; confirmed boundaries
compare the complete expected world and auxiliary/allocator evidence. Restart
then performs a segment and full update and checks both cargo participants and
orbit references. This proves process-crash atomicity, not hardware disk/fsync
failure tolerance. Uncertain in-process test outcomes are never reopened as
known-aborted recovery.

## Compatibility and deferred work

Existing CR-17A/B, landed/habitat factory, telescope, CR-07, CR-18, CR-43 and
WebSocket tests run unchanged in semantics. Four older stale-world tests used a
resource load to manufacture the still-open legacy disagreement. Their setup
now uses **fuel**, explicitly outside this migration, with valid capacity and
stock; every existing HALT, no-write and no-stale-flush assertion is retained.
New positive resource tests prove the corrected operation no longer creates
that disagreement. CR-07's ENROLLMENT_PENDING_TURN guard remains intact; zero-AP
cargo does not create a new AP discrepancy. CR-17A stockpile transfer's volatile
AP is not fixed here. Existing legacy actor/store disagreement, including AP
drift, can still trigger conservative stale-preimage refusal/HALT; cargo does
not merge or repair that disagreement.

Deferred to CR-17C follow-ups/CR-17D acceptance: crew/troops/assault/colonization,
fuel/destruct, transporter #42, jettison/mount, stockpile transfer AP, navigation/
repair, construction/allocators, broad staging, durable IDs/dedup/outbox and
failure-aware client result contracts. Supported r/x/& requests that require
excluded effects fail closed; they cannot escape through the unsafe legacy
writer. Existing land/dock commands can produce incomplete carrier links; this
operation refuses those worlds instead of repairing them. CR-11 ship reload and
CR-13 turn/ownership/mass behavior remain independently open. Cross-owner cargo
is valid at commit/restart; this migration does not promise whole-turn C parity
for a carrier whose owner can later be recomputed from contained ships. CR-17,
CR-11, CR-13 and CR-20 remain OPEN. No public enrollment/authentication, original
C, live database, Kubernetes, image or deployment change is included.

## Independent review and evidence handling

An independent agent performed bounded, noninteractive read-only C/source/test
review, fresh race execution and archive/evidence inspection. The full rounds
and original conditional verdicts are preserved in
`evidence/cr17c-cargo-review.md`; raw private logs remain under
`/tmp/cr17c-cargo-evidence/`. This is independent agent review, not the
maintainer's merge approval or acknowledgment label.

- **R1, blocking gameplay regression:** same-star peers were compared using an
  irrelevant planet number. The reviewer reproduced a legitimate actor command
  HALT; the location check now compares only meaningful root-level coordinates.
  The permanent positive regression fails before the fix and passes after it.
- **R2, test effectiveness:** initial malformed authority cases had unrelated
  reasons to fail. They were replaced with otherwise-valid coherent proposals;
  independent guard-deletion overlays now specifically expose unauthorized
  COMMITTED writes. No missing production authority check was found.
- **R3, diagnostic:** a copied habitat name in an error was corrected to cargo.
- The reviewer independently passed 297 cargo nodes / 19 top-level entries,
  including all 12 SIGKILL leaves, under the race detector. It checked all 15
  blocking review areas and found no remaining partial-write/authority/output/
  uncertainty defect in the bounded implementation.
- A separate descendant-context review confirmed that required containing/root
  membership witnesses are complete for this arithmetic. Unrelated descendant
  state is not a transaction dependency or a claimed global mass audit.

The instrumentation patch was compressed losslessly after the staged whitespace
check correctly rejected unified-diff context whitespace. No whitespace checker
was weakened; the archived original bytes and their hashes are retained. Raw
full-suite transport logs are not committed because other test fixtures can
contain disposable authentication data. The validation ledger records command,
exit status, timestamps, durations and output hashes; counters count test nodes,
not independent assertions. Historical failures and deliberate counterexamples
remain distinct from current successful execution.

## Fresh local validation

Executed on Linux/amd64, 2026-11-12 UTC, against the exact Go source hashes in
`evidence/cr17c-cargo-validation.json`. Verified executable:
`/tmp/gb-go1274-evidence/go/bin/go` (`go1.27.4`); `GOTOOLCHAIN=local`.
The ledger includes exact commands, timings and SHA-256 hashes of stdout/stderr.
No previous work unit's pass is substituted for this execution.

| Executed check | Actual result |
| --- | --- |
| `go build ./...`; strict gofmt; `go vet ./...` | PASS |
| `go test -count=1 -json ./...` | PASS, 4,248 passing nodes;2 existing opt-in long tests skipped;438 passing top-level tests |
| Initial `GB_LONG_TESTS=1 go test -count=1 -race -json ./...` | FAIL only in existing admin 500 ms timeout;4248 passing nodes, one failed leaf plus its failed parent |
| Exact admin timeout test `-count=3 -race` | PASS all 3 executions, unchanged source/assertions |
| Complete `GB_LONG_TESTS=1 go test -count=1 -race -p=1 -json ./...` | PASS, 4,250 nodes / 440 top-level tests,0 test skips, 21 tested packages;760.843s |
| Focused final DB/game/rules + compatibility regressions | PASS, 337 nodes, including 297 cargo nodes and 12 SIGKILL leaves |
| Independent reviewer DB/game/rules `-race -run Cargo` | PASS, 297 nodes / 19 top-level entries /12 SIGKILL leaves |
| `make -C test/ctool check` | PASS, original C fixtures reproduce exactly |
| Inventory reconciliation and existing golden tests | PASS; golden PASS 1,248 / XFAIL 143 / XPASS 0 / FAIL 0 |
| `test/scripts/golden-mutations.sh` | PASS all 15 expected scenarios;713.901s |
| Lint gate | PASS, 1,000 matched findings; new 0 / stale 0; no baseline edit |
| Growth gate against baseline, no labels | PASS, allowances unchanged, no protected tooling changed |
| Golden helper, lint and acknowledgment self-tests | PASS all 3 scripts |
| Offline manifests, structural/mutation/safety/schema checks | PASS, 21 resources,0 invalid/errors/skipped |
| Workflow helpers/static policy/actionlint/shellcheck | PASS, 188 Python tests; 17 policy checks |
| Strict `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...` | PASS, 0 reachable/imported-package vulnerabilities; 17 noncalled module advisories reported, not suppressed |
| `go mod verify`; `go mod tidy -diff` | PASS, no module changes |
| Final full staged `git diff --cached --check` | PASS after exact patch-byte preservation in gzip |

The initial race run was launched concurrently with normal tests, inventory,
lint, mutations and review. Its unchanged
`admin.TestPrivateEnrollmentTimeoutAndUncertainty/after-commit` received the
existing truthful UNKNOWN response at its 500 ms HTTP deadline instead of the
test's expected confirmed response. There was no data-race report or cargo
failure. Three targeted race repetitions passed, and the complete serial-package
rerun passed every test, including both opt-in production-timing tests. The
original failure/log hashes remain in the ledger; no timeout, assertion or
production behavior was changed to hide it. This pre-existing timing sensitivity
remains a hosted-CI observation risk.

Normal suite's two test skips are the existing opt-in long WebSocket/real-engine
checks. The complete long race rerun executed both. Six packages have no test
files; they are not reported as skipped acceptance tests.

The original-handler overlay and owner/governor guard-deletion overlays are
**expected failures**, proving causal regressions. The earlier staged whitespace
error is a packaging failure corrected without altering the archived original
patch bytes or weakening the checker. Earlier development logs remain private;
only current runs and explicitly listed historical/counterexample runs are
counted in this final ledger.

Hosted Docker builds, Trivy, Syft and kind remain GitHub Actions responsibilities;
no container runtime was installed or executed locally. No scanner suppression,
test relaxation, workflow/gate change, defect allowance or lint-baseline change
is included. Neither `known-defect-added` nor `gate-change-reviewed` is required
by this diff; no acknowledgment label was applied.

## Files and acceptance scope

Production: new `internal/commandrules/cargo.go`, `internal/db/cargo.go`,
`internal/game/cargo_command.go`; three-line interception in commands_load.go;
private legacy-reproduction hook in helpers.go/lifecycle.go. Existing workspace,
other closed operations and actor loop remain unchanged.

Tests: new cargo tests in commandrules, db and game (including staging and crash
harness); the four existing stale-world test files described above now use
legacy fuel. Documentation: this record, the appended CR-17 progress note in
go-code-review.md, four preserved pre-fix artifacts, validation ledger and
independent review record.

The bounded resource/crystal invocation and all acceptance rows above pass fresh
local execution. Remaining conditions are hosted CI and normal maintainer PR
review, plus the explicitly deferred broader CR-17 work. This is not a claim of
complete command durability, exactly-once execution, all-load atomicity or a
closed CR-11/13/17/20 finding. No merge, deployment, image publication, live DB
access or Kubernetes/Argo CD/Longhorn operation occurred.


**Final verdict: CR-17C Cargo — PASS WITH CONDITIONS.** The independent final
implementation/evidence audit approved the bounded operation for normal PR review,
with all source/log hashes and the clean full long-race results verified. R1/R2/R3
are resolved; no blocking transaction or state-consistency finding remains.
Hosted CI and maintainer review are pending; the broader deferred CR-17 work
remains open. Final review report SHA-256:
`838ff84f1ffe6115088ebe9d57cada2dc7c45a466768ac558c58f2a6671960b6`.
