# CR-17C #41A: atomic noncombat population and troop transfers

Baseline `96570333540c1695b1d4f5fce2838e6b2975dbe1`; issue #41, parent #38.
Worktree `/tmp/GB-cr17c-population`, branch `feature/cr17c-noncombat-population`.
Original C is authoritative. All mutations/tests use disposable initialized stores.

## Operation inventory recorded before production implementation

| Path | Records and mutations | Existing Go persistence/effects | Selected disposition |
| --- | --- | --- | --- |
| Same-owner containing ship, c/m load/unload | Selected and partner Popn or Troops; selected Mass; peer Mass except containing-carrier exemption; race mass is read-only | Early complement text; independent partner save then selected save; optional automatic transporter; final volatile AP 1 | New closed two-ship transaction; no transporter; approved AP 0 |
| Reciprocal same-owner peers | Same fields, both mass deltas; root/list/reciprocal IDs and caller/governor witnesses | Old Go compares partner destination to selected child-list head, may independently undock; same split writes | Validate actual selected number, no repair; whole invocation staged |
| Foreign-owned ship | C limits0,0 for both c/m; nonzero either direction disallowed; zero no-op allowed | Same early output and independent writes if0 | Preserve no nonzero permission; no extra partner-governor requirement |
| Player-owned planet sector | Selected Popn/Troops/Mass; landing sector commodity; planet-wide matching total; caller Planet.Info matching total | GetPlanet copy, sector inline update, early text, PutPlanet then selected save; actor planet stale; volatile AP 1 | Actor planet proposal plus selected ship; all embedded sector/accounting fields atomic |
| Empty neutral deposit | Above, sector Owner becomes player, caller NumSectsOwned+1 and MobPoints+Mobilization | COLONIZED/OCCUPIED before writes; no Race/terrain/exploration changes | Complete colonization/occupation; no invented habitability/slavery/tech restriction |
| Last resident evacuation | Above, Owner becomes0, caller NumSectsOwned-1 and MobPoints-Mobilization | Evacuated text before persistence; independent planet/ship | Atomic complete evacuation; other resident commodity prevents clearing |
| Empty-sector zero/default and already-owned empty deposit | C emptiness-only predicates decrement even unowned empty/no-op, and increment already-owned empty sector | Same original accounting bugs, possible unsigned wrap | Explicit owner decision requested before defining these corrections; no silent policy |
| Unowned populated sector | C normal planet processing clears owner0 Popn/Troops (doplanet.c); load arithmetic can underflow caller Info if malformed | Independent copies can preserve inconsistent totals | Verify/prevent wrapping; do not invent new cross-race permissions |
| Foreign planet sector c/m, including load/zero | Assault helper may modify selected ship, sector, both races' morale/translation, planet per-owner accounting, groundAssaults, telegrams/notifications; RNG | Calls helper then independent ship/planet/race/telegram writes with early effects | All such c/m invocations fail closed before legacy mutation; #41B/#51 deferred |
| Active targeted selected transporter | Additional ship, messages and possible AP effects | doTransporter before selected save | Fail closed, deferred #42 |
| Move/deploy ground movement | Source/destination sectors, planet, both races, AP, AFVs, combat notices/telegrams | Independent writes and effects | Not migrated; CR-19 source remains open |

Sources: load.c:54–480, ships.h:299–300 (factory effective crew capacity),
shlmisc.c:292–295 (authorized), load.c:1038–1182 (landing/assault), move.c:arm,
move_popn and ground_attack; corresponding current commands_load.go and
commands_move.go; existing cargo/workspace/storage/outcome/effects contracts.

C amount is signed int, unload negates, VN negative refusal precedes default,
limits are checked before amount0 expands to load upper/unload lower. Crew free
space is effective MaxCrew minus onboard troops minus onboard population;
troops symmetric. Factories use Shipdata MAXCREW. Ship foreign-owner limits0,0.
No shuttle exception for c/m. AP 0 and actual containing-carrier mass exemption
are already owner-approved. Mass uses caller Race.Mass, not ship race or a
client/config claim. No race model or sector Race change is made in noncombat.
No C noncombat telegram/news/other-player effect requires an outbox.

## Current CR-19 verification (not a fix)

The historical blanket 'assault never captures' finding is not accurate for all
current paths. commands_load.go unloadOntoAlienSector assigns Owner on victory
and changes both owners' sector counters. commands_move.go ground movement calls
groundAttack but tests Owner==player without the C move.c defender-elimination
ownership assignment. Both still have independent persistence/external effects.
Issue #51 remains OPEN; this unit will not change either combat implementation.

Original defect reproduction and implementation evidence will be appended after
fresh execution; this inventory is not a completed validation claim.

## Pre-fix execution and approved correction

Fresh actor dispatch at baseline plus private post-write observation hooks failed
all eight c/m × load/unload × ship/planet atomicity leaves. After the first real
write, crew transfers left selected 10/recipient28 (two lost) or10/32 (two
created), versus the original10/30. Troops left10/18 or10/22 versus10/20.
Full actor/store/sector/star snapshots were captured before, after first write,
after command, segment, update and restart. AP also diverged (Go1 debit).
The two ownership regressions separately showed neutral empty zero wrapping
NumSectsOwned 0→65535, and already-owned empty deposit double-counting 1→2.
The exact fixture and hooks are archived losslessly with sanitized snapshots.

**Owner-approved deviation:** ownership counters and mobilization points now
change only on actual neutral→player/player→neutral transitions. Empty neutral
zero stays unowned without a debit; already-owned empty deposits do not claim
again. Owned-empty zero retains C's ownership cleanup. Sector.Race remains
unchanged. This explicit approval does not change colonization eligibility,
terrain, exploration, race mass or other C rules.

## Implemented rules and exact scope

`load/unload #N c|m [amount]` accepts one explicit positive uint32 ship number
(optional `#`), exactly c or m, and an optional signed int32 decimal amount.
The complete invocation is intercepted before legacy selection/output/writes.
Malformed eligible tokens, wildcards, ship types, lists, extra arguments and
numeric overflow do not fall back to legacy code. Existing r/x/& staging is
unchanged; fuel/destruct retain their separately open legacy limitations.

Same-owner containing carriers and reciprocal peers are supported, including
star/universe roots. C permits a zero no-op with a differently owned partner
(bounds 0,0), but no nonzero crew or troop transfer in either direction. The
selected ship must belong to the authenticated caller and satisfy C's selected
ship governor authorization. The recipient does not acquire a new governor
permission requirement. Ship.Race and Sector.Race need not equal Owner and are
never changed. Caller Race.Mass is authoritative, finite and nonnegative; the
transaction checks the entire caller race, including active governor authority.
No new crew requirement, damage-zero check, On switch, terrain, habitability,
slavery, technology or exploration prerequisite is introduced.

Planet transfers affect the actual landing sector, not a planet-wide cargo
stockpile. For c, sector Popn, Planet.Popn and caller Info.Popn all lose the
signed amount the selected ship gains. For m the corresponding Troops fields
change instead. Crew and troops share C's effective MaxCrew capacity (factory
Shipdata override); neither has the shuttle resource exception. Signed limits
are checked before zero/default expansion and VN negative-direction rejection
stays before default expansion. Overfull signed limits are preserved, not
clamped into a new gameplay rule.

All uint16 quantities, uint32 player totals, uint64 planet totals and
mobilization points are checked without signed narrowing or unsigned wrapping.
Mass changes use the effective signed amount and caller race mass. The selected
ship gains it; an external peer loses it; a containing carrier keeps its
aggregate mass under the previously approved rule. Crystals/resources and all
other ship fields are immutable within this operation. C's Boolean `sh`
argument typo is not reintroduced.

The approved ownership correction claims an empty neutral sector once on an
actual deposit; an already-owned empty sector does not increment again. Empty
neutral zero does not debit. When the last resident leaves an owned sector,
ownership clears and sector count/mobilization debit once, even for owned-empty
zero cleanup as C does. A surviving civilian or troop prevents evacuation.
Emptiness checks do not add two uint16 fields, avoiding a false zero at 65,536.
Sector.Race, terrain, fertility, resources, crystals and all unrelated fields
stay unchanged; no additional C noncombat exploration flags are invented.

Unowned sectors containing people are not given an invented prohibition or
capture policy: C's literal deltas remain subject to checked numeric bounds.
Normal C planet processing clears owner0 people, but imported/inconsistent
preimages are not globally repaired or audited here. A delta preserves any
pre-existing accounting discrepancy rather than silently merging it; actual
underflow/overflow refuses the command. Complete snapshot comparison still
prevents stale actor copies from overwriting current stored records.

**Temporary gameplay limitation:** every foreign-owned planet-sector c/m
invocation, including positive loading or zero, is refused before legacy assault
or saves. Active targeted selected transporters are also refused. These are
explicitly deferred effects (#41B/#51 and #42), never alternate unsafe writers.
The existing move/deploy assault path is unchanged and remains open.

## Transaction/write/witness inventory

A separate closed `db.PopulationTransfer` does not broaden the dump/factory/
habitat/telescope/cargo contracts. `commandrules.PlanPopulation` is pure and
non-mutating; both actor preparation and storage independently recompute its
permitted deltas. The existing private workspace and checked location helpers
are reused, not an arbitrary storage callback.

| Participant | Writable fields | Preconditions / preserved evidence |
| --- | --- | --- |
| Selected ship | Popn **or** Troops; Mass | Full expected record, exact map/key/Number, owner/governor/alive/active/docked, supported numeric request |
| Partner ship, if used | Matching population field; applicable Mass | Full expected record, live valid identity, containing membership or reciprocal docking, C overload/capacity/ownership rules |
| Planet, if used | Embedded landing-sector Popn/Troops/Owner; matching planet total and caller Info total; approved ownership NumSectsOwned/MobPoints delta | Full expected planet, sector coordinates and dimensions; exact whole proposed model equality against independently calculated permitted fields |
| Caller race | None | Full persisted preimage, caller number, active governor, unchanged mass/traits/credentials/status |
| Root star / universe data | None | Full preimage including all AP; approved zero AP, no debit or AP write |
| Root planet / full orbit lists / containing parent chains / sibling witnesses | None unless that planet is the participant above | Unique live chains, identities and complete persisted records, no repair, no unrelated child-tree audit |
| Actor directory | None | Consistent actor-captured star/planet scope; checked before commit/install, no separately persisted directory record |
| Metadata and allocator | None | CR-18 commit, schedule, RNG, CR-43 maintenance, placements, free lists, news/telegrams preserved |

One bbolt transaction first checks the strict durable maintenance record and
all complete canonical preimages, then writes selected ship and one recipient.
Every Put is checked. The existing command mutex, shared uncertainty guard,
write fence and refusal accounting remain in use, with fence checks before and
inside the transaction. No reservation, new bucket/key/schema, commit kind,
commit sequence, WorldDigest or protocol change is introduced.

The actor retains its world while validating private deep copies. Confirmed
persistence is followed by complete identity rechecks and two assignments to
existing actor object addresses, with no callback between them. Read-only
witnesses are not installed. Bounded local location/claim/evacuation/complement
output is released afterwards. No noncombat news/telegram/other-player effect
needs durable representation; such assault/transporter effects are excluded.

| Outcome | Required behavior verified |
| --- | --- |
| Validation rejection | No world, store or AP mutation; no successful text |
| Confirmed abort / precommit panic | Complete old records, no proposal install or success; persistence/integrity failures HALT conservatively |
| Confirmed commit | Complete durable pair and coherent actor installation before success |
| Confirmed commit plus reporting/delivery failure | Committed records remain; install when identity is valid, HALT without rollback claims or stale shutdown flush |
| Uncertain write / unavailable readback | No unverified installation; retained shared handle guard; HALT for operator reconciliation; readback is diagnostic, not fsync proof |
| Committed but invalid installation identity | No partial proposal install; HALT before a turn/shutdown can overwrite durable evidence |

There is no automatic retry/reopen/repair, durable command ID, deduplication or
outbox. Lost replies may follow a committed transfer; clients must not blindly
repeat it as if it were idempotent.

## Permanent gameplay, fault and crash acceptance

| Requirement | Tests / actual boundary |
| --- | --- |
| C signed/default quantities, both shared capacities, factory override, no shuttle exception, VN order, finite mass, maximum fields and checked aggregates | commandrules.TestPopulationRulesMatrix; TestPopulationRulesBoundaries |
| uint16 sum65536 remains nonempty; literal neutral populated deltas | TestPopulationWideResidentsNeverAliasEmpty; game wide no-op cases |
| c/m × load/unload × omitted/zero/positive/negative × planet/contained/peer | TestPopulationGameplayMatrix,48 leaf cases; complete world and pointer comparisons, AP 0 |
| Neutral claim, already-owned empty deposit, zero cleanup, last resident, survivor prevents evacuation, both commodities | TestPopulationSectorTransitions; TestPopulationOwnershipEdgeCases |
| Foreign-ship limits, selected owner/governor, recipient governor independence, different Ship.Race, hostile-sector refusal, star/universe roots | TestPopulationAuthorityForeignAndAssaultBoundary; direct DB coherent authority tests |
| Original split writes and subsequent stale overwrite | TestPopulationAtomicTransfer,8 cases with explicit selected/recipient quantities and actor/store/planet accounting assertions after command, segment, update and restart |
| Before/check/validated/write1/write2/precommit/commit-report/aftercommit errors and panics | TestPopulationStoreFailuresAndOutcomes; TestPopulationActorFailureOutcomes |
| Input immutability, exact whitelist including ownership/mobilization and other players, concurrent conflicts, stale race mass | TestPopulationStoreAtomicReopenAndInputs; TestPopulationStoreFullMatrix; TestPopulationStoreCoherentAuthorityAndWhitelist |
| Stale/missing/broken orbital and participant records; malformed selectors; transporter | TestPopulationInvalidSelectorsAndDeferredPaths; TestPopulationStoreClosedContract; TestPopulationStoreStalePreimagesFenceAndMaintenance |
| Fence/maintenance/shared guard, readback, installation and response faults | DB fence/maintenance tests; TestPopulationInstallationReadbackAndDelivery |
| Serialized commands, started cancellation, queued cancellation, draining, quiesce/restart, HALTED refusal | TestPopulationAdmittedOperationAndModes; TestPopulationQueuedBehindTurnAndCanceled; TestPopulationHaltedAdmission |
| Actual SIGKILL, complete old/new durable state, restart plus following segment/update | TestPopulationRealProcessCrash,24 leaf cases |

The 24 actual process kills comprise six boundaries for each of four variants:
planet civilian colonization, planet troop evacuation, ship civilian loading,
and ship troop unloading. Boundaries: before transaction, after selected write,
after recipient write, immediately before commit, after confirmed commit before
installation, and after installation before response. The exact test executable
blocks at a private test-only marker, and the parent verifies SIGKILL status.
Production has no fault environment setting, endpoint or alternate writer.

Known-aborted boundaries compare the entire original logical database, including
all auxiliary/allocator records. Confirmed boundaries compare the complete
expected world and ownership/counter deltas, not just record counts. Reopen
loads actor/store agreement; subsequent segment/update checks transferred people,
colony/evacuation ownership and sector counts plus linked ship and allocator
invariants. Legitimate turn-derived fields need not stay byte-identical. These
are process-crash tests, not physical disk/power-loss/fsync-failure claims.
Genuinely uncertain in-process outcomes are never reopened as known-aborted
recovery tests.

## Independent review and causal evidence

Independent read-only review found no remaining production blocker. Three
regression-effectiveness gaps were corrected without weakening assertions:

1. A wide-resident no-op test is needed to catch a reintroduced uint16-sum empty
   check; the original 'wide residents' case removed a whole commodity and did
   not leave 65,536 people. The permanent wide no-op now fails that mutation.
2. Source-owner/governor negatives must otherwise be legal, coherent proposals.
   Both selected and partner can belong to player2 while caller1 is forbidden
   to operate them; removing only that source-owner predicate now commits an
   unauthorized pair and fails the test. The governor predicate has an
   independent equivalent counterexample.
3. Capturing later state is not asserting persistence. The original regression
   now asserts quantities, full actor/store equality and sector/planet/player
   accounting at each command/segment/update/restart boundary. A fresh run passes.

Removing only the staged c/m interception in a private Go overlay restores the
actual legacy handlers and their archived observation hooks. All eight atomicity
leaves and both accounting edge cases fail again. Historical pre-fix records and
review attempts remain distinct from the final corrected execution.

The reviewer independently ran final-source race tests across rules, DB and game:
345 passing nodes / 22 top-level entries / 24 actual SIGKILL leaves, exit 0, no
failures/skips/race warnings. All 11 source/test/hook hashes stayed unchanged.
The earlier review run and comment-only drift are preserved as history, not
misrepresented as final-source evidence. Those preliminary review conditions are superseded by the final complete
validation and acceptance audit recorded below.

## Compatibility and deferred work

CR-07 enrollment/admission and pending-turn guard, CR-17A route/stockpile edits,
CR-17B dump, landed/habitat factory (including cross-owner activation), telescope,
r/x/& cargo, CR-18 turn/fence/recovery, CR-43 maintenance, WebSocket shutdown and
session cancellation remain separate regression suites. Their existing tests
and safety conditions are retained. CR-17A stockpile transfer AP remains volatile;
legacy actor/store drift can still cause conservative stale-preimage refusal or
HALT. No blind merge or broad CR-11/13 state/mass/turn repair is attempted.

Issue #41 is only partially addressed by this noncombat slice. A proposed #41B
must independently define complete assault ship/sector/planet/race, RNG, morale,
translation, reporting and durable-notification participation, and coordinate
verified ground-movement capture correctness with #51. New persistent effects,
command IDs or outbox require separately scoped approval. Transporters #42,
fuel/destruct, navigation/docking repairs, construction/allocators and broader
CR-17D acceptance remain deferred. CR-17/CR-11/CR-13/CR-20 and issues #38/#41/#51
remain OPEN. No combat implementation is silently marked complete.

## Test scheduling and the unchanged timeout

The first complete long race execution hit Go's default 10-minute **package**
alarm: the game package had accumulated 600 seconds and stopped during the
pre-existing TestGB101AuthenticateTimeoutRacesReplyDelivery, which had run only
four seconds and was actively computing bcrypt. No individual test failure or
data race was reported. The added population groups take about 145 seconds
serially; this was cumulative throughput, not evidence of a stuck transaction.
A subsequent serial execution completed the game package with essentially no
headroom. Neither result is hidden or relabeled as the final robust acceptance.

The independently reviewed correction marks **only the new top-level game
TestPopulation groups** parallel. Their actors, stores, temporary directories,
configs, policies, metrics, hooks and responses are independent. Cached test
hashes/engine version use sync.Once; config uses MapEnv rather than changing
process environment; crash children receive private cmd.Env. The child entry
TestPopulationCrashChild remains nonparallel. Existing test scheduling, all
subcases, expectations, deadline values and the default package timeout remain
unchanged. Old serial tests finish before the new parallel groups resume.
This improves throughput and exercises isolation rather than reducing coverage.
Fresh complete normal/race/mutation runs and a reviewer four-processor focused
race run are recorded separately from the earlier serial evidence.

## Fresh complete validation

Environment: Linux/amd64, 2026-11-12 UTC, executable
`/tmp/gb-go1274-evidence/go/bin/go` reporting go1.27.4, GOTOOLCHAIN=local.
`evidence/cr17c-population-validation.json` records 52 executions, exact commands,
timestamps, durations, statuses, stdout/stderr SHA-256, all 11 final source hashes
and the archived raw/compressed pre-fix evidence hashes. Raw logs are retained
privately under `/tmp/cr17c-population-evidence/`; full transport fixtures can
contain disposable authentication material and are not committed as artifacts.

| Actual check | Result |
| --- | --- |
| `go build ./...`; strict gofmt; `go vet ./...` | PASS |
| Complete normal suite on final parallel source | PASS, 4,593 nodes / 460 top-level tests; 2 existing opt-in long tests skipped;49.144s |
| `GB_LONG_TESTS=1 go test -count=1 -race -p=1 -json ./...` on final source | PASS, 4,595 nodes / 462 top-level tests; 0 test skips; 21 tested packages;859.455s |
| Final game package in combined long race | PASS, 562.097s within unchanged default 600s package budget |
| Independent `GOMAXPROCS=4` rules/DB/game population race | PASS, 345 nodes / 22 top-level entries / 24 SIGKILL cases;57.798s |
| Original C fixture reproduction and golden tests | PASS, fixtures identical; golden PASS 1,248 / XFAIL 143 / XPASS 0 / FAIL 0 |
| All 15 golden mutations on final parallel source | PASS, 720.855s; every expected positive/negative behavior retained |
| Known-defect inventory reconciliation | PASS, tests actually executed and existing owning files reconciled |
| Lint gate | PASS, 1,000 matched findings; new 0 / stale 0; no allowance changes |
| Growth gate against 9657033, no labels | PASS, baseline/defect allowances unchanged, no protected tooling changes |
| Golden helper, lint and approval gate self-tests | PASS all 3 scripts |
| Workflow helpers / static policy / actionlint / shellcheck | PASS, 188 Python tests and 17 policy checks |
| Offline structural/mutation/safety/schema manifest checks | PASS, 21 resources; 0 invalid/errors/skipped; no cluster contact |
| Strict govulncheck v1.9.0 under Go 1.27.4 | PASS, 0 reachable/imported-package vulnerabilities; 17 noncalled module advisories reported, not suppressed |
| `go mod verify`; `go mod tidy -diff` | PASS, no module changes |
| Complete staged diff whitespace check | PASS |

Counts are test nodes, not individual assertions. The six packages with no test
files are distinct from the two opt-in test skips in normal execution. Both
long tests executed in the complete long race suite. Hosted Docker builds,
Trivy, Syft and kind remain GitHub Actions responsibilities; no container runtime
was installed or executed on igor.

Historical results remain explicit: the first observation-hook edit had a syntax
error, the initial actor adapter had a copied helper name, and the initial
factory test expectation did not model the C 20-person effective crew table
correctly. They were corrected before acceptance. The final factory regression
uses a full C table capacity despite a large stored MaxCrew, so it detects
ignoring the factory override. Lint initially requested explicit uint32 range
proof and removal of an unused test assignment; those were fixed without
annotations or baseline edits. The 52-record ledger retains those attempts,
pre-fix expected failures, causal guard/wide-sum mutations, the cumulative 10m
package timeout, the narrow serial pass and the final corrected runs separately.
No timeout, assertion, scanner policy or golden allowance was weakened.

## Delivery boundaries and remaining conditions

Production changes: pure population rules, closed population storage operation,
private actor adapter and early c/m interception. A private after-real-PutPlanet
hook in the existing testHooks infrastructure preserves causal legacy evidence;
it is never production-configured or called by the new writer. Tests include
new rules/DB/actor gameplay, failure/lifecycle and subprocess suites. Other
application commands, resource/crystal rules, original C and protected tooling
are unchanged. Documentation includes this inventory/record, CR-17 progress,
pre-fix artifacts, validation ledger and complete independent-review history.

No database format, schema, key/bucket, commit kind, protocol, public route,
credential or deployment change. No lint-baseline, known-defect allowance or
protected gate tooling change: neither `known-defect-added` nor
`gate-change-reviewed` is required by this diff, and no label is applied.
The PR references #41/#38; it does not close #41 or #51. Normal maintainer review
and all seven hosted CI checks remain required before any merge authorization.
No merge, deployment, image publication, live database access, Kubernetes,
Argo CD, Longhorn, secrets or recovery-point operation occurred. Other branches,
worktrees and agent sessions were preserved.


**Final verdict: CR-17C #41A — PASS WITH CONDITIONS.** Independent final source,
test and actual-evidence review approves this bounded implementation for normal
PR review. All review-requested regression gaps and documentation corrections
are resolved; no blocking finding remains. Hosted CI and maintainer review are
pending. This does not close #38/#41/#51 or approve merge/deployment.
Final independent report SHA-256:
`fee669fd4e69c5681a227f5c31588693b5abbc13ab3f9403d11fc89689d48903`.
