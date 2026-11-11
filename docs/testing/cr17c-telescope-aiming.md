# CR-17C follow-up: atomic telescope aiming

Baseline: `43f84de0cb6b4125b83a34d4118a75a4cee3205d`.
Isolated branch/worktree: `feature/cr17c-telescope-aiming`, `/tmp/GB-cr17c-telescope`.
CR-17, CR-11 and CR-20 remain open.

## Plan recorded before implementation

Migrate the complete explicit-ID `order #N aim <place>` invocation for survey
and gravity telescopes. Reject selectors containing a telescope before legacy
iteration/output; leave other aim users unchanged. Retire the old telescope
helper/writer and guard direct `giveOrders` entry before mutation. Capture full
private ship, target star/planet and read-only source/target-ship identity and
orbital context, including universe and carrier parents; preserve model addresses.
Use a separate closed typed `Store.CommitTelescopeAim` and one existing bbolt
transaction, sharing the command mutex, fence, outcome classifier, diagnostic
readback and uncertainty guard. Do not generalize dump or factory contracts.
Check every full persisted preimage inside the transaction, then checked writes
in ship/star/planet order. Install all changed records only after confirmed
commit, with all identity checks before non-callback assignments. Bound local
header/survey/aim/order-row effects using the existing workspace limits.

Extract a small pure `internal/commandrules` telescope calculation used by game
and database whitelist validation. It accepts trusted validated config fuel/mass
rates, not client values; no AP, allocator, outbox, metadata, schema, commit-kind,
RNG, protocol, persistent identity or deduplication change is included.

C `source/order.c:469–500,587–588,616–681` confirms:

- Active and crew checks; owner/governor authorization in `order` wrapper.
- Survey telescope uses maneuver fuel and mass even for out-of-range/universe/
  ship targets. Gravity telescope uses neither. No AP is charged.
- Valid aims update four aim fields and reset Notified, preserving intensity.
- In-range (`distance <= range`) star/planet exploration; planet geometry is
  star position plus planet offset. Out-of-range aims still commit the aim.
- Universe/ship targets still aim, reporting no useful exploration.

**Pre-existing range discrepancy, deliberately preserved:** original
`source/shootblast.c:534–540` uses `SYSTEMSIZE/8` and `SYSTEMSIZE/3`, with
`SYSTEMSIZE=2000` in `source/tweakables.h:82` (addends250 and666). Existing Go
uses `SystemGravConst/375` and `/100` (400 and1500). This persistence migration
preserves the existing Go range arithmetic exactly; it does not claim C range
parity or silently rebalance it. Correcting that discrepancy needs separate
review. C fuel/mass defaults .3/.05 and no-AP behavior are retained.

Malformed/overflowing ship IDs must not alias another record. An unrepresentable
ship target cannot be truncated into the uint16 aim field; reject it explicitly.
Source/target aliases deduplicate immutable witnesses without dropping proposals.
Foreign live ship targets and unexplored stars/planets remain accepted by the
existing `getplace(...,true)` visibility policy. No star-governor, damage, On/off,
landed or carried restriction is added to otherwise valid telescope aiming.

## Pre-fix execution evidence

The exact fixture and reproducer are preserved as text under `evidence/`.
Actual State.HandleCommand dispatch reaches the real planet Put, then its
Surveyed responder callback raises the existing write fence before final
PutShip. This is deterministic storage-failure injection, not a claim that a
normal turn overlaps the command or a physical disk failed.

Fresh expected failure:0.145s, exit1. Actor/store fuel9.7/10.0,
planet exploration false/true, star exploration true/false; Surveyed and Aimed
success text already emitted. Segment/update/graceful restart lose the aim and
planet exploration. Complete affected-only ship/star/planet records across six
stages are in `evidence/cr17c-telescope-pre-fix-records.json.gz`; no race,
credential, password, bcrypt or token models occur in that artifact.

Independent review reproduced the same expected failure in a fresh baseline
archive with only the archived test fixture/reproducer added:0.171s, exit1.
Implementation, validation and final review results will be recorded below.

## Participating records and ownership

The only writable records are the selected telescope, target star for star/planet
aims, and target planet for planet aims. Even unchanged out-of-range target
records are checked and written in the same transaction; universe/ship aims
write only the selected ship. Only four aim fields, Fuel/Mass/Notified and the
owning player's in-range exploration bits may change. AP, sectors, stockpiles,
other players, orbit links, allocators and metadata are not writable through
this contract. All full preimages are serialized before transaction admission
and checked again inside the write transaction before any Put.

Read-only witnesses include the current race/governor authority, full source
star/planet or universe root, every ship in the source chain, recursive carrier
parents and sibling chains, and target-ship chains when that target is live.
God-accessible dead ship targets remain read-only witnesses without requiring
them to be in a live chain. Target star/planet records protect their complete
current heads and data; unrelated child chains of those targets are not captured
or rewritten. All witness identities and values remain actor-owned until commit.
The private directory/scope is checked before persistence and installation but
is not a persistent database record. Full-star AP divergence from an earlier
legacy command is refused, never silently merged; ENROLLMENT_PENDING_TURN stays.

The shared pure rule helper preserves current Go geometry/range and configured
maneuver fuel/mass rates. Telescope crew checks use actual MaxCrew minus Troops,
not the factory-only Shipdata rule; robotic/troop-filled cases remain valid.
Exact target IDs must fit the existing uint16 aim field; malformed or larger
IDs are rejected rather than aliased/truncated. Extra arguments and nonnumeric
selectors containing a telescope are rejected before legacy output or iteration.
Other aim users retain their existing implementation and its CR-17 limitations.

## Failure, effects and retry boundary

Validation drops all private copies without actor/store changes. Confirmed
aborts leave every participating record unchanged and halt conservatively on
storage/integrity failure. Committed reporting errors retain the whole stored
and installed proposal, then halt. Uncertain writes never install a proposal;
the shared handle guard and HALTED mode suppress later turn/shutdown flushing.
Visible readback is diagnostic, not fsync proof. No automatic repair, reopening
or retry is supplied. All checks complete before pointer-preserving ship/star/
planet assignments, with no callback between them. Scope/race/root identity
changes at installation halt before any proposed record installs.

Response header, Surveyed/Too-far text, Aimed text and order row use the existing
4,096-byte/16-message local buffer. No success is released before commit and
installation. This is not a durable outbox. A lost response can follow complete
persistence; blindly repeating a survey-telescope aim consumes fuel again. No
command-ID store, durable deduplication or exactly-once acknowledgment exists.

## Review fixes and acknowledgment

Independent review found and corrected a requested-ID alias in the new selector:
pass the parsed ID into staging, never the Number borrowed from its map value.
The real-dispatch corrupt-map regression now halts without writes. Reverting
only that fix in isolated source exports makes the regression fail for the
intended wrong-ship commit (independent0.140s; local0.169s). No production fault
control was added. Independent critical race review also confirmed crew rules,
legacy weapon/mirror/tractor preservation, private copies and all crash phases.

Retiring mkExplAimedAt removes exactly three stale lint entries: two unchecked
Send calls and its narrowing GetPlanet index cast. No finding is added or moved.
The maintainer must apply **known-defect-added**; no labels are applied by the
implementation agent. No protected gate tooling, C source, golden allowance or
known-defect inventory change is part of this PR.

## Test matrix and platform boundary

| Test | Fresh evidence covered |
| --- | --- |
| commandrules.TestTelescopeRulesDefinedSemantics / FiniteInputsFuelAndBoundaries | Pure immutable input, defined fuel/mass/aim/intensity arithmetic, Go range extraction, explicit C offset discrepancy, exact/nextafter range/fuel boundaries, invalid/overflow/NaN/infinite inputs |
| db.TestTelescopeAimAtomicAndReopen | Full record equality, unchanged input/witness ordering and allocator/metadata, three actual write phases, reopen |
| db.TestTelescopeAimFailureAtomicity / CommittedAndUncertain | Before/validated/each write/precommit errors and panics; complete logical-store rollback; committed reporting errors; visible-but-uncertain state, readback unavailable and shared guard across command families |
| db.TestTelescopeAimInvalidAndStale / FenceAndConcurrent | Closed field whitelist, all required identity/authority fields, effective crew, missing/duplicate/cyclic context; actual intervening writes after preparation; in-Tx preimages; both fence checks/accounting and two-adapter race |
| game.TestTelescopeActorCompleteSemanticsAndBoundaries | Both telescopes, star/planet, inside/equal/immediately-outside range, 3–4–5 and planet-offset geometry, full world/aux equality, outer pointer identity, segment/update/restart |
| game.TestTelescopeActorFailureAndHalt | Real actor dispatch, every transaction fault/panic, complete abort snapshots, visible complete commit, actor noninstallation/installation, HALTED/no stale shutdown flush, uncertainty guard |
| game.TestTelescopeActorValidationSelectorsAndAuthority / IdentityAndStalePreimages | Invalid targets/IDs/selectors, owner/governor/active/crew/fuel checks, real map-alias regression and stale actor/store star/planet/race/orbit head |
| game.TestTelescopeActorOtherTargetsLocationsAndAliasing / FuelGovernorAndNoNewRestrictions | Universe/self/foreign/dead-God targets, scope/planet aliases, universe/star/planet source roots, carried/nested carriers, configured fuel/mass, zero AP, exact fuel, robotic/troop crew, damage/off and authorized governor |
| game.TestTelescopeActorEffectsAndPrivateWorldWaitForCommit | Deterministic barrier after star Put: old actor, no output, independent reader sees complete old ship/star/planet; effects only after confirmed installation |
| game.TestTelescopeActorInstallationReadbackAndDeliveryFailures | Unavailable readback, install panic, replaced ship/star/planet/race pointers, scope drift, response error/panic, complete durable records with no unsafe partial actor install/flush |
| game.TestTelescopeActorLegacyUsersBypassAndMixedSelectors / ModesEnrollmentAndAdmittedShutdown | Weapons/mirrors/tractors retain old behavior; mixed prefix rejection, direct legacy guard, cyclic selection termination, output limits; QUIESCED/HALTED refusal, immediate enrollment and admitted shutdown/restart |
| game.TestTelescopeRealProcessCrash | Six actual SIGKILL boundaries: before, after ship, after star, after planet, precommit and confirmed postcommit-before-reply. Complete previous/new world, allocator/metadata/other ships, safe restart and following segment/update |

The new crash harness targets the repository's Linux host/CI environment. It
uses the kernel's fixed `/proc/self/exe` path with fixed Go-test arguments;
there is no variable executable path or lint suppression. Child fault variables
are read only by `_test.go`, not a production binary or network endpoint. No new
test is skipped. A missing Linux procfs mechanism fails the harness; portability
to other operating systems is not claimed. No crash inside bbolt's own page-write
or fsync internals is claimed. Only known precommit or confirmed-postcommit test
oracles are reopened; genuinely uncertain outcomes are never auto-repaired.

## Fresh final validation and verdict

Executed on 2026-11-11 UTC using Go1.27.4linux/amd64 and GOTOOLCHAIN=local.
[The execution ledger](evidence/cr17c-telescope-validation.json) contains the ten
final source/test SHA-256 values, full-run test names/counts, command/output hashes,
pre-fix artifact hashes and independent-review log hashes. Initial successful
runs were repeated after test-only refinements; the ledger uses the final runs.
The final crash harness captures child stderr and fails on Go race warnings.
No source, test assertion, gate or warning was suppressed to obtain these passes.

| Final check | Actual result |
| --- | --- |
| gofmt / build / vet | PASS |
| Normal uncached full suite | 387 top-level passes / 3,691 passing nodes; two existing opt-in long tests skipped |
| GB_LONG_TESTS=1 uncached full race suite | 389 top-level passes / 3,693 passing nodes; zero test skips/failures |
| New telescope coverage | 178 passing nodes / 19 top-level entries (including child harness):2 rules,68 db,108 actor nodes; all six actual SIGKILL cases pass in both full runs |
| Existing C fixture reproduction / golden parity | PASS; fixtures exactly reproduce; known range discrepancy is not a claimed telescope C golden pass |
| All 15 golden mutations | PASS, every expected result/diagnostic retained |
| Known-defect inventory reconciliation | PASS; existing explicit untested entries retained, no inventory change |
| Lint / growth | 1,005 baseline/report/matched; zero new/stale; three exact removals; no protected tooling change; local label simulation only |
| Gate self-tests | PASS; approval, lint and 100-iteration mutation helpers |
| Workflow tests / policy | 188 tests and17policy checks PASS; no workflow modifications |
| actionlint / CI ShellCheck / offline manifests | PASS;21resources valid, no errors/skips |
| Strict govulncheck v1.9.0 | Zero reachable vulnerabilities;17non-reachable module findings remain; no suppression |
| Module verify / tidy diff / working and staged diff checks | PASS; no dependency or module drift |

The full suites freshly rerun CR-07A–F, CR-17A/B, landed-factory, CR-18, CR-43
and WebSocket shutdown/disconnect regressions, including real subprocess,
CLI/HTTP/WebSocket enrollment and maintenance/crash recovery. Existing command
AP/stale-state guards remain conservative; the range discrepancy and deferred
commands above remain limitations, not newly closed findings.

Independent design/source/test review found no remaining blocking issue after
the reviewed fixes. It independently reproduced the baseline defect, verified
the selector regression's counterexample, ran all telescope race tests and vet,
then reran final target/alias and stderr-aware six-SIGKILL cases (16.753s).
Final independent evidence audit passed: ten source/test hashes,21gate logs,
both full JSON runs,four pre-fix artifacts,seven independent logs and exact test/
crash counts agree. No blocking implementation or evidence finding remains.

Verdict: **CR-17C telescope aiming: PASS WITH CONDITIONS** for this exact-ID slice.
Conditions are normal owner review, maintainer-applied known-defect-added and
green hosted CI. Docker/buildx, Trivy, Syft and kind remain hosted checks; none
were installed or run on this host. Database-format impact: **NONE**. No public
API, authentication protocol, commit kind, schema, metadata key, or live game/
Kubernetes/deployment change was introduced. Do not merge/deploy in this task.
