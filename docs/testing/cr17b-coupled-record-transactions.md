# CR-17B: atomic exploration dump

Baseline: `d9a90e9da0ceeb81bc35ff032ff3fa30d070c90b`.
Isolated branch/worktree: `feature/cr17b-coupled-record-transactions`, `/tmp/GB-cr17b`.

Verdict: **CR-17B: PASS WITH CONDITIONS** for the complete dump invocation.
Other CR-17 command paths remain open; no live acceptance or deployment is claimed.

## Selected scope and design

This design was recorded and independently reviewed before implementation.

Migrate **the complete `dump` invocation**, not one star at a time. Introduce a
narrow `Store.CommitCommandRecords` contract for exploration-dump star and planet
records only. It has explicit source/recipient, AP-bearing star, full expected
and proposed records, and selected-star indicators. It accepts no ships, races,
star data, allocator, auxiliary writes, creation or deletion. Record identities,
participation and permitted exploration/AP changes are validated. Every planet
of every selected explored star participates, including unchanged/read-only
planets whose source exploration flag determines whether it is shared. The
current scope star participates for the one-AP debit even if not selected.
Repeated star arguments are deduplicated. Invalid named/numeric arguments reject
without mutation (the old Go code silently skipped them and still charged AP).

Private actor-derived records are serialized before opening one existing bbolt
write transaction. Inside that transaction, check the write fence, all complete
persisted preimages and all record identities before any write. Exact JSON
preimages use the existing store's canonical model encoding: a noncanonical or
unexpected representation is conservatively refused, never silently merged.
Check every Put in the same transaction. No CommitWorld call, new commit kind,
sequence increment, schedule/RNG/maintenance update, schema or metadata key.

Use the established transaction phases: before callback and callback failures
are confirmed aborts (bbolt rolls back); attempting commit errors/panics are
uncertain; a nil transaction return confirms commit. A later reporting failure
cannot turn a confirmed commit into an abort. Diagnostic readback never proves
fsync or promotes an uncertain result. Unknown outcomes set a per-handle guard;
the actor halts without flushing and new actors on that handle refuse startup.
No automatic retry, reopening, repair or recovery operation is provided.

Before persistence and again before installation, verify all actor object
identities. Preserve star/planet addresses by assignment; independently copy
planet sectors; no callback or output between installation assignments. Install
all records only after confirmed commit. Send the bounded success/notification
texts afterward. AP is in the same transaction for this command. There is no
outbox, deduplication, group commit or delivery guarantee after a lost response.
CR-17A transfer's separate volatile AP boundary remains unchanged.

Initial independent design review approved this subset with these conditions.

## Deferred paths

Factory activation and telescope aiming interleave ship modifications, early
output and the final `giveOrders` ship save; changing their inner planet write
alone is unsafe. Ordinary load/unload also includes ship/planet/AP writes and
shared undock, assault, transporter, crew/troop and mass branches. Navigation,
movement, docking, construction and orbital/allocator updates need their own
complete participation and effect boundaries. Preserve those paths unchanged;
CR-17C must stage and review each complete operation. CR-11 and CR-17 remain open.

Implementation, inventory and fresh execution evidence follow below. No live
game or deployment operation is authorized or performed.

## Inventory and disposition

Source inventory at the baseline contains **101 store/save call sites in
`commands*.go`**, plus 14 store/allocation sites in dispatched `build.go` helpers
and three unchecked store calls in `helpers.go`. These are sites across alternate
branches, not 118 writes in one command. The appendix gives exact source lines.
CR-17A's older 105-site inventory predates its four removed command call sites.
Only dump is migrated here; the inventory is not a claim of complete command
atomicity. The four original paths left after A are evaluated explicitly:

| Operation / actual handlers | Participating records and AP | Persistence/effects before this PR; disposition |
| --- | --- | --- |
| Exploration dump / `cmdDump` | Selected stars, every planet whose source exploration flag is read, current scope AP star; read-only ship chains | Old loop writes star then independent planet copies, logs/continues on failure, debits AP only in memory and announces success. **Fixed for the complete invocation**, including multi-star selection. |
| Ordinary cargo / `cmdLoad`, `cmdUnload`, `doLoad` | Planet stockpiles and embedded sector aggregates; source ship and possibly partner/carrier ship; AP star | Planet Put precedes final saveShip; independent planet copies, unchecked save wrappers, output before both writes. Undock and transporter branches share the handler. **Deferred to C**; cannot migrate an inner Put alone. |
| Crew/troop loading / `doLoad`, `unloadOntoAlienSector` | Sector ownership/population/troops/mobility, planet totals/Info, ship crew/mass, possibly two races and combat counters, AP | Several success/combat paths save independently and continue; assault sends messages/telegrams and can change diplomacy/counters. **Deferred to C**; scope exceeds ordinary cargo. |
| Factory activation / `giveOrders`, `activateFactory` | Landed planet resources plus factory On/Notified; or habitat resources/hangar and factory size/On | Inner helper saves planet/carrier and prints activation before outer giveOrders sets On and finally saves factory. A planet write failure is logged then treated as activation success. **Deferred to C**; whole outer operation must participate. |
| Telescope aiming / `giveOrders`, `mkExplAimedAt` | Ship aim pointer/fuel/mass/Notified; star and optional planet exploration | Ship is changed before helper; star/planet writes and Surveyed text precede final independent ship save; planet branch leaves the star flag memory-only. **Deferred to C**; do not fix inner helper only. |
| Population movement / `movePopn`, `cmdWalk` | Planet sectors/stockpiles/totals/ownership; sometimes two races, walker ship, AP | Actor planet generally used, but independent unchecked planet/race saves and early combat output. **Deferred**. |
| Land/launch/scrap/capture/docking / navigation and profile handlers | Moving ship, carrier/target/predecessor ships, planet/star/SData roots, races/AP; some destruction/free-list work | Linked structures and inventories saved separately through unchecked wrappers; allocator and side effects cannot be rolled back together. **Deferred**. |
| Build/upgrade/make/mod / `build.go` | New/modified ship, builder ship, planet/sector inventories, orbit roots, immediate ship allocator | Allocator and PutShip separate from actor stockpile/chain mutations; build may install a ship after reporting PutShip error. Design changes print before saving. **Deferred**. |
| Sell/bid / `build.go` | Planet stockpiles, commodity record and allocator, race/governor/AP where applicable | Sell debits actor planet before allocating a lot, notifies before commodity Put; bid writes a commodity independently and notifies previous bidder. **Deferred**. |
| Route and stockpile transfer / CR-17A | One planet; transfer additionally volatile AP star | Existing checked actor-derived proposal path retained. No new transaction migration or AP correction claimed. |
| Other single-record commands / appendix | Planet, race, ship or read cursors depending on command; AP may add another record | Even a single Put is not a proof of command atomicity if AP, effects or other memory changed. Existing wrappers discard errors; **not migrated**. |

News, stored telegrams, live registry output and external responder callbacks
cannot be rolled back by bbolt after emission. Dump's `warnRace` uses only
`notifyRace` live delivery; it creates no news/telegram record. Exactly one
bounded recipient message and one success text are released after installation.
No directory/scope change, allocator reservation, race mutation or external
service call belongs to dump. Source/recipient permissions are read from the
serialized actor; no second identity service/writer is introduced.

## Original failure and pre-fix evidence

The temporary instrumentation ran immediately after **the actual old PutStar**,
then raised CR-18's existing write fence so the subsequent actual PutPlanet calls
failed. It did not replace the handler or storage function with an invented
implementation. The desired atomic assertions failed (exit 1):

| Boundary | Observed durable / actor state |
| --- | --- |
| Before | Recipient star and both planet flags false; AP 50 in actor and store |
| After first PutStar | Recipient star true; both planet flags false |
| After refused planet writes | Star true, planets false; actor AP 49, store AP 50; ordinary success was sent |
| Movement segment | Partial exploration retained; AP 49 becomes persisted |
| Full update and orderly restart | Star-only knowledge remains; legitimate update AP becomes 63 |

Evidence under `docs/testing/evidence/`:

- `cr17b-pre-fix.txt`: baseline, exact command, expected failure and observations.
- `cr17b-pre-fix-instrumentation.patch`: the five temporary instrumentation lines
  as a zero-context patch (apply with `git apply --unidiff-zero`).
- `cr17b-pre-fix-reproducer.go.txt`: real actor reproduction source, executable by
  copying to the baseline game test package and applying the preserved patch.
- `cr17b-pre-fix-records.json.gz`: complete sanitized star and both planet records
  (actor/store) at all six stages, including sectors/Info/AP and premature output.
  These records contain no credentials, account secrets or tokens. Decompress as
  ordinary gzip JSON for field-level inspection.

The temporary hook and reproduction test are **removed from runtime/test source**;
the permanent tests use the real new transaction path. No original C file,
golden fixture, protected script or allowance was modified for this reproduction.

## Implemented transaction contract

`db.CommandRecords` has explicit Player, Recipient, APStar and typed
CommandStar/CommandPlanet edits. A star edit has Expected, Proposed and Share;
a planet edit has Expected and Proposed. This is a dump-only capability despite
the general method name. There is no untyped record escape hatch. It rejects:

- Invalid player numbers, nil records, duplicate keys, absent AP star, excessive
  participant counts, unshared non-AP stars, incomplete/extra planet sets,
  inconsistent IDs/map dimensions, or proposals other than the permitted changes.
- Any change to geometry, stockpiles, sectors, ownership, race/ship identities,
  orbital heads or unrelated flags. NaN/unencodable models fail construction.
- Missing or changed persisted preimages, including **unchanged** planets read
  to decide what exploration may be shared. All comparisons happen inside the
  same write transaction and precede every Put.
- Referenced orbital chains with missing/dead ships, cycles or inconsistent
  record IDs/locations. Those ship records are read only; no links or allocation
  change. This is not a repair or comprehensive unrelated-ship audit.

Private serialized before/after records use existing model JSON and existing
bucket/key conventions. Exact canonical byte comparison intentionally refuses
unexpected encoding/whitespace/unknown data rather than normalizing it. This may
reduce availability for noncanonical historical records; no rewrite/migration or
relaxed comparison is attempted. The actor is the sole production writer.

`dumpProposal` keeps independent expected/proposed planets (including sector
slices), value-only stars and original actor addresses. Identity and complete
actor values are checked before persistence and again before installation.
After confirmed persistence, all checks finish before any assignment; no external
callback occurs between pointer-preserving star/planet assignments. No additional
index/cache stores these exploration/AP values separately. Ships, directory,
SData, enrollment history, credentials and allocator state remain untouched.
The package-private pre-install test hook has no production configuration.

## Outcomes, effects and recovery boundary

| Evidence | Classification and behavior |
| --- | --- |
| Before Update, or callback error/panic | NOT_COMMITTED; bbolt rolls back the callback; no actor installation, AP debit, recipient notice or success. Actor conservatively HALTs. |
| Update returns nil | COMMITTED; install all participants, then deliver bounded live texts. |
| Confirmed Update followed by reporting error/panic | COMMITTED remains committed; install complete proposal, HALT and report committed/error without ordinary success. |
| Commit attempt reports failure/panic, including error despite visible new records | UNCERTAIN; no installation or success, diagnostic readback never proves fsync, retain per-handle uncertainty guard and HALT. |
| Readback fails/panics | Visibility unavailable; uncertainty remains uncertainty. |
| Post-commit identity/install failure | COMMITTED, halt for reconciliation before installing any proposal; no turn or shutdown flush can overwrite the durable result. |
| Post-install notification panic | COMMITTED; complete actor/store result stands, explicit diagnostic says nothing rolled back, HALT. |

`Store.CommandWriteUncertain` also prevents a new actor on that same handle from
writing a startup schedule or implicitly resuming. Shutdown in HALTED does not
flush. No automatic retry/reopen/repair or clearing API exists. Preserve the
halt reason, participating records, original intent and store evidence for a
separately authorized operator reconciliation; a visible record alone cannot
justify claiming durability. Existing generic text-command return semantics are
unchanged: `HandleCommand` completion alone is not a structured durable result.

The write fence is checked before and inside the transaction, with the existing
refusal count/method accounting. Actor serialization prevents turns from running
concurrently. Once a command is admitted past the mode gate, draining cannot
withdraw it mid-transaction: it completes before quiesce/shutdown processing.
New mutating commands in QUIESCED/HALTED or draining are refused as before.
Canceled queued commands never run; started cancellation waits for the real
result under the existing lifecycle contract.

Dump's one AP debit now commits with its exploration changes. Duplicate star
arguments and self-dump still debit exactly once. Repeating the **command** after
a lost reply is not deduplicated and may debit another AP; durable command IDs
and an outbox remain C. A prior volatile AP difference from another command
causes a strict precondition refusal/HALT, not a blind merge. CR-17A transfer AP
and CR-07's ENROLLMENT_PENDING_TURN safeguard remain unchanged. Invalid star
arguments now reject without AP/success instead of silently skipping them; valid
star-name/number/all-known behavior and C dump field semantics are retained.
The original C reference is `source/load.c:625–707` (`dump`); C writes in a
different per-record order, which this atomic transaction intentionally replaces.
The existing Go selector grammar (star names or one-based numbers) is retained;
C's richer Getplace star/planet path selectors are not newly implemented. This
is C-derived field/AP parity, not a claim of complete original-C command parity.

## Permanent tests and fresh execution

| Automated test | Evidence |
| --- | --- |
| db.TestCommandRecordsAtomicAndReopen | Full records, original inputs, ships and all metadata remain correct; reopen preserves result |
| db.TestCommandRecordsRollbackFailures | Error/panic at before/validated/each write/before-commit; entire logical database including bucket sequences unchanged |
| db.TestCommandRecordsCommittedAndUncertain | Post-commit/report faults distinguished from uncertain visibility; failed diagnostic reads never prove durability; guard blocks retry |
| db.TestCommandRecordsValidation | Invalid/unsupported field changes and participant shapes cannot write |
| db.TestCommandRecordsStalePreimages | Star, planet, unchanged planet, missing record and noncanonical representation refused before writes |
| db.TestCommandRecordsFinalPreconditionInsideTransaction | Real intervening PutPlanet after proposal serialization is not overwritten |
| db.TestCommandRecordsCompetingAdaptersUseOneTransaction | Two adapters on one bbolt handle, distinct mutexes: one commit and one stale abort |
| db.TestCommandRecordsOrbitalIntegrity | Missing/cyclic/dead/wrong-orbit chain references refused |
| db.TestCommandRecordsFenceAndConcurrentConflict | Both fence boundaries/accounting and concurrent stale conflict |
| game.TestCR17BDumpGameplayAndBoundaries | One/all/multiple/unexplored/duplicate/self/AP-star-not-selected cases; complete actor/store fields, preserved object addresses and unrelated records; segment/update/shutdown/restart and governed races |
| game.TestCR17BDumpFailures | Actual actor dispatch reaches every transaction fault; no early success; all-or-none records, correct committed installation and no stale HALTED flush |
| game.TestCR17BDumpAllStarsRollback | Failure/panic after star/planet prefixes and ninth/final write rolls back the complete invocation |
| game.TestCR17BDumpUncertainReadbackUnavailable | Failed verification cannot become success/rollback |
| game.TestCR17BDumpInstallationAndDeliveryFailures | No partial actor installation; committed outcomes remain explicit |
| game.TestCR17BDumpInvalidAndModeRefusal | Malformed/unknown/overflowing args, permission/AP and QUIESCED/HALTED refusals do not mutate records |
| game.TestCR17BDumpStaleAndMissingActorState | Actual legacy load disagreement, AP/read-only-planet drift and missing/misidentified actor records fail closed |
| game.TestCR17BDumpOutputWaitsForCommit | Barrier after first in-transaction Put: readers still see the complete old world; no actor mutation/output; after commit all agree |
| game.TestCR17BDumpSerializationAndShutdown | Queued second dump, quiesce and drain; admitted write completes before following action; durable QUIESCED restart |
| game.TestCR17BDumpQueuedCancellationAndTurn | Actual queued cancellation behind a blocked turn never executes; following command waits until turn/fence ends |
| game.TestCR17BDumpStartedCancellation | Cancel after first Put does not guess an outcome or withdraw an executing operation |
| game.TestCR17BDumpEnrollmentAndAPBoundary | Atomic dump permits immediate enrollment; existing transfer still gives AP-only pending-turn admission without HALT |
| game.TestCR17BDumpStarNamesAndReadOnlyPlanets | Case-insensitive names; source-unknown planet is not spuriously shared |
| game.TestCR17BDumpRealProcessCrash / TestCR17BDumpCrashChild | Four actual SIGKILL boundaries in disposable Go test subprocesses using the real actor and bbolt; details below |

The crash child is in `_test.go`, so no production binary interprets its
CR17B_CRASH_* variables. It starts the real actor against a closed disposable
fixture, configures the existing test-only storage hook, and blocks at an exact
boundary. The parent waits for a fixed marker before SIGKILL, drains the pipe,
verifies actual SIGKILL exit and inspects every affected record plus allocator,
placement and metadata. It does not use arbitrary sleeps to choose the kill.
The child injects the existing Options.Now clock at the fixture's stored SavedAt,
so normal startup schedule rewriting produces identical bytes; raw schedule and
all other metadata comparisons remain enforced.

The command shares three stars and six planets. Before transaction, after first
Put and after all nine Puts but before commit: the full previous state survives.
After confirmed nil Update but before reply/installation: the full new state,
including AP, survives. Known complete stores restart SERVING and governed.
These are known test phase oracles, **not** permission to restart a genuinely
uncertain write. No kill is injected inside bbolt's own fsync/page-write code;
physical disk/fsync failure is modeled conservatively, not claimed reproduced.

Fresh execution (Go 1.27.3, linux/amd64, 2026-11-10 UTC) is recorded in
[evidence/cr17b-validation.json](evidence/cr17b-validation.json), including frozen
source hashes, every full-run result, test names and output hashes. Raw logs
containing transient transport/session data are not published.

| Executed command/check | Actual result |
| --- | --- |
| `gofmt -l .`, `go build ./...`, `go vet ./...` | PASS |
| `go test -count=1 -json ./...` | PASS: 342 top-level tests, 3,352 test/subtest nodes; two opt-in long tests skipped |
| First `GB_LONG_TESTS=1 go test -count=1 -race -json ./...` | FAIL: one unchanged WebSocket shutdown test (1006 instead of 1012); 3,353 nodes pass, including all 137 new nodes |
| Fresh identical full race command | PASS: all 344 top-level tests / 3,354 nodes, zero skips/failures |
| New database/game CR-17B tests | 24 top-level entries including the child harness, 137 passing nodes in all three full runs |
| C fixture reproduction / golden parity | PASS; fixtures reproduce exactly, golden tests run in both full suites |
| Inventory reconciliation | PASS; no inventory/expected allowance change |
| Golden mutations | PASS: all 15 scenarios retain expected behavior |
| Gate approval, lint gate and golden-mutation helper self-tests | PASS; golden helpers exercised 100 iterations per case |
| Lint | PASS: 1,010 baseline/report/matched, zero new/stale |
| Growth gate / both diff checks | PASS on the staged tree: 8 unique / 10 occurrence removals, no protected tooling changes; local simulated label only |
| Offline manifest validation | PASS: 143 structural checks, mutation/safety suites, all 21 resources schema-valid |
| Workflow/helper unit tests / policy | PASS: 184 tests / 17 policy checks |
| actionlint / shellcheck | PASS; no output |
| govulncheck v1.9.0 | PASS: zero reachable vulnerabilities; tool reports 17 non-reachable module findings |
| `go mod verify`, `go mod tidy -diff` | PASS: modules verified, no drift |

The unchanged transport failure also reproduced on an isolated archive of the
exact baseline during 100 repeats; separate 20-repeat baseline/current and
independent 30-repeat runs passed. See
[evidence/cr17b-transport-validation-note.txt](evidence/cr17b-transport-validation-note.txt).
The test uses the unchanged net fake engine and does not call game/db. Source
review found a plausible pre-existing close-frame omission in the outbound/done
branch. No transport assertion or source was changed. The fresh full pass does
not close that intermittent defect; a separate focused transport fix is advised.

The complete suites freshly execute existing CR-17A, CR-07A–F, CR-18 and CR-43
regressions, including subprocess/CLI/HTTP/WebSocket enrollment, initial empires,
crash recovery, turns, startup validation, maintenance and shutdown. This is
reuse of existing acceptance, not new public/API/transport implementation.
Tests use disposable stores and no live server/volume access. Go/test commands
used GOCACHE=/tmp/cr07a-cache and TMPDIR=/tmp/GB-cr17b/test/ctool/obj/tmp, keeping
mutation VCS build stamps legitimate. Initial sandbox restrictions on pinned-tool
dependency downloads were resolved by authorized execution; no check was skipped.
Docker, Trivy, Syft and kind remain hosted GitHub Actions work, pending PR.

Independent source, design, reproduction and critical test review found no
blocking defect in the migrated dump. The reviewer checked all twelve requested
areas, independently reconciled the inventory/hashes and reran final critical
race tests (db 1.103s; game 13.868s), plus prior focused race suites (db 3.215s;
game 55.111s). Final evidence-ledger review passed: all source/run/check hashes,
counts and preserved artifacts match. No blocking finding remains in the
migrated operation. Acceptance remains
conditional on maintainer acknowledgment, normal owner review and hosted CI.

## Compatibility and remaining CR-17C/D work

- **CR-07:** Current caller-bound placement records, credentials, allocator and
  admission safeguards are unchanged. Atomic dump does not leave new AP drift;
  earlier volatile transfers still require a normal turn for enrollment.
- **CR-11:** Ship reload at turn start/shutdown remains. Dump modifies no ship,
  cargo or orbit link; complete ship maps and governed/orbital identity checks
  prove preservation. Other ship commands can still have independent write risks.
- **CR-18:** No CommitWorld reuse, new kind, sequence increment, schedule/RNG change,
  digest alteration or metadata format. Existing `init/update/segment/shutdown`
  kinds remain closed. Commands commit between turns; the next world commit sees
  fully installed results. No per-command commit stamp is introduced.
- **CR-43:** Modes, durable maintenance records, resumed schedule and shutdown
  semantics unchanged; new tests prove admitted writes finish before drain and
  QUIESCED survives restart. No automatic recovery/resume mechanism exists.
- **Data format:** NONE. Existing star/planet JSON buckets and keys only. No schema,
  new metadata key, bbolt bucket, migration or persistent intent/status is added.
  Current-reader startup and existing regressions are freshly exercised; an actual
  earlier binary is not newly executed. Source data compatibility is distinct
  from authentication/security rollback safety.

C must design complete coupled ship/planet/star/race/SData/allocator participation
for each deferred operation, checked errors and held external effects, then stage
commands/group commits and define AP durability, deduplication and lost-reply
semantics. D must extend transport/concurrent/crash/allocator acceptance over
those migrated commands before broad closure. Do not close CR-17, CR-11, CR-20
or unrelated inventory entries on this bounded evidence.

Exactly ten stale lint occurrences are pruned (eight Send, one Sscanf, one G115).
The new baseline is 1,010 with no additions/moved allowances. The maintainer must
apply **known-defect-added**; no acknowledgment is inferred from local simulation.
Protected scripts, workflows, original C, fixtures and known-defect inventory are
unchanged, so gate-change-reviewed is not required. No label is applied by the
implementation agent.

No live game/database, Kubernetes, Argo CD, Longhorn, image publication/pinning,
DNS, secrets or deployment operation occurred. Existing checkouts/worktrees and
the retained audit branch remain untouched.

## Baseline direct-store and save/allocation call-site appendix

| File / handler | Baseline call sites |
| --- | --- |
| `commands_cs.go` / `cmdCS` | PutRace:65 |
| `commands_fire.go` / `cmdBombard` | PutPlanet:448 |
| `commands_fire.go` / `cmdDefend` | PutPlanet:663 |
| `commands_load.go` / `doLoad` | GetPlanet:125; saveShip:146; saveShip:310; PutPlanet:311; PutPlanet:334; saveShip:356; PutPlanet:357; PutPlanet:380; PutPlanet:392; PutPlanet:411; PutPlanet:425; PutPlanet:439; saveShip:449; saveShip:457 |
| `commands_load.go` / `cmdJettison` | saveShip:572 |
| `commands_load.go` / `cmdDump` | PutStar:675; GetPlanet:680; PutPlanet:686 |
| `commands_load.go` / `doMount` | saveShip:844 |
| `commands_load.go` / `doTransporter` | saveShip:937 |
| `commands_load.go` / `unloadOntoAlienSector` | PutRace:1130; PutRace:1133 |
| `commands_misc.go` / `cmdCapital` | PutRace:75 |
| `commands_misc.go` / `cmdAutoreport` | savePlanet:256 |
| `commands_misc.go` / `cmdMobilize` | savePlanet:308 |
| `commands_misc.go` / `cmdDissolve` | PutRace:340 |
| `commands_misc.go` / `cmdEnslave` | savePlanet:422 |
| `commands_move.go` / `movePopn` | PutPlanet:396; PutPlanet:402; PutPlanet:411; PutPlanet:417; PutPlanet:454; PutPlanet:467; PutPlanet:485; PutRace:611; PutRace:612; PutPlanet:652 |
| `commands_move.go` / `cmdWalk` | PutRace:849; PutRace:850; PutPlanet:852 |
| `commands_nav.go` / `doLandOnShip` | savePlanet:105; saveShip:112; saveShip:113; savePlanet:149; saveStar:154; saveShip:166; saveShip:167 |
| `commands_nav.go` / `doLandOnPlanet` | savePlanet:271; saveShip:272; savePlanet:335; saveShip:354 |
| `commands_nav.go` / `doLaunchOne` | saveShip:436; saveShip:437; savePlanet:486; saveShip:508 |
| `commands_nav.go` / `launchFromCarrier` | savePlanet:520; savePlanet:544; saveStar:559; saveShip:570; saveShip:579; saveShip:580 |
| `commands_nav.go` / `doScrapOne` | saveShip:737; saveShip:764; savePlanet:785; saveShip:793 |
| `commands_nav.go` / `cmdCapture` | saveShip:909; saveShip:948; saveShip:949; savePlanet:950 |
| `commands_order.go` / `giveOrders` | saveShip:625 |
| `commands_order.go` / `activateFactory` | saveShip:661; GetPlanet:667; PutPlanet:678 |
| `commands_order.go` / `mkExplAimedAt` | PutStar:925; GetPlanet:938; PutPlanet:948 |
| `commands_profile.go` / `cmdToggle` | PutRace:91 |
| `commands_profile.go` / `cmdName` | saveShip:181; saveStar:205 |
| `commands_profile.go` / `doDockOne` | saveShip:532; saveShip:533; saveShip:591; saveShip:592; saveShip:597; saveShip:598; saveShip:644; PutRace:822; PutRace:823; saveShip:842; saveShip:843 |
| `commands_tech.go` / `cmdTech` | savePlanet:60 |
| `commands_tech.go` / `cmdDeclare` | PutRace:197 |
| `commands_teleg.go` / `cmdTelegram` | PutRace:168 |
| `commands_teleg.go` / `readTelegrams` | ReadAndClearTelegrams:204 |
| `commands_teleg.go` / `readNews` | LatestNewsSeq:247; ReadNewsFrom:252; PutRace:277 |
| `commands_teleg.go` / `checkForTelegrams` | HasTelegrams:284 |
| `build.go` / `createShipByPlanet` | allocShipNum:531 |
| `build.go` / `createShipByShip` | allocShipNum:597 |
| `build.go` / `buildFromPlanet` | PutShip:887 |
| `build.go` / `buildFromShip` | PutShip:1031; PutShip:1056; PutShip:1082 |
| `build.go` / `cmdUpgrade` | PutShip:1341 |
| `build.go` / `cmdMake` | PutShip:1518 |
| `build.go` / `cmdMod` | PutShip:1751 |
| `build.go` / `cmdSell` | allocCommodNum:1890; PutCommod:1918 |
| `build.go` / `cmdBid` | GetCommod:2010; PutCommod:2051 |
| `build.go` / `listCommodities` | GetCommod:2061 |
| `helpers.go` / `savePlanet` | PutPlanet:485 |
| `helpers.go` / `saveShip` | PutShip:490 |
| `helpers.go` / `saveStar` | PutStar:495 |
