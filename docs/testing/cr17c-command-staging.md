# CR-17C: staged landed factory activation

Baseline: `51fd2b45658e28e580e6df70009984149f9bf561`.
Branch/worktree: `feature/cr17c-command-staging`, `/tmp/GB-cr17c`.

Verdict: **CR-17C: PASS WITH CONDITIONS** for the landed-factory slice.
CR-17 remains open; no deployment or live acceptance is claimed.

## Plan recorded before implementation

Implement **landed factory activation for one explicit ship number** (`order #N
on`, also N). Stage the complete operation before `giveOrders` can mutate On or
save the ship. Reject multi/mixed/ambiguous selectors containing a landed factory
before any prefix can run. Invalid/overflowing numeric IDs must not alias another
ship through uint32 conversion. Carrier-based activation, telescope aiming,
cargo, movement and construction retain their legacy implementations and are
explicitly deferred; no broad command atomicity claim is made.

The private actor-owned command workspace captures immutable full preimages and
independent proposed ship/planet copies. Deep-copy every ShipSpecial pointer and
planet sector slice. Capture complete planet orbital-chain witnesses and the
home star read-only; validate owner, governor authority, landing/destination
identities and chain membership. Preserve all actor record addresses. No new
sector ownership, Guest or damage==0 restriction is introduced for this operation.
Original C allows damaged factories; zero AP is allowed because this branch
charges no AP.

Use a **separate typed `Store.CommitFactoryActivation`** operation. Preserve the
closed dump-only `CommandRecords` API unchanged. The new operation admits only
factory On=true/Notified=false and a planet stockpile debit of 2*BuildCost. C's
unsigned-int cost is computed without uint16 truncation; an unaffordable large
cost must not wrap to cheap/free activation. Two checked Puts (ship and planet)
in one existing bbolt transaction follow complete in-transaction preimage checks
of all participants and read-only witnesses. Reject all other record families,
creation, deletion, orbit rewrites, allocations and metadata changes.

Reuse CR-17B's CommandOutcome/CommandError, command mutex, uncertainty guard and
readback diagnostics. Before/callback errors are confirmed aborts; an attempted
commit failure is uncertain; a nil Update confirms commit; later reporting
errors cannot claim rollback. No visible readback proves fsync. New actors on a
tainted handle remain halted through the existing guard. Never reopen/retry to
clear uncertainty. No schema, commit kind, commit-sequence, schedule, RNG or
maintenance change is authorized or needed.

All actor identities and preimages must still match before coherent installation.
No callbacks between installation assignments. Stage a bounded local response
(header, activation text and order row) and release only after confirmed commit
and installation. This is not a durable outbox: failed delivery after commit
remains a committed/reporting error, and no exactly-once retry is claimed.

The independent design reviewer approved this bounded slice and required whole
selector refusal before legacy iteration, explicit governor authorization and
full-width C costs. A prior volatile AP difference can make the read-only full
star preimage stale; fail closed and document that availability effect rather
than merge or weaken ENROLLMENT_PENDING_TURN.

## Pre-code command inventory and classification

The CR-17B baseline inventory and current source identify these persistence
families. Actual baseline call-site counts/lines are reconciled in the final
appendix. A single Put does not imply atomicity when AP/effects or other actor
records also change. Source inspection is not fresh execution evidence.

| Handler/family | Actor reads; mutations / metadata | Existing store order, atomicity, AP / allocation | Effects, failures, stale-overwrite risk and migration boundary |
| --- | --- | --- | --- |
| giveOrders / activateFactory, landed | Factory and planet; On/Notified and planet resources; star/chain identity witnesses | GetPlanet copy -> PutPlanet -> set On/Notified -> saveShip; separate transactions; no AP/allocation | Activation text before ship durability; planet error logged/ignored and factory still enables; stale actor planet later refunds cost. **Migrate exact single landed factory, whole operation.** |
| activateFactory, habitat | Factory/carrier; resources, Hanger, factory Size/On/Notified | save carrier -> print -> outer factory save; no AP or allocation | Partial writes, unsigned subtraction/truncation and turn-time size recalculation need separate review. **Legacy unchanged, deferred.** |
| giveOrders / mkExplAimedAt | Ship aim pointer/fuel/mass/Notified, star/planet exploration | Ship mutates, helper PutStar/PutPlanet, final saveShip; no common transaction | Surveyed/Aimed text early; planet copy stale, planet branch star flag memory-only. **Defer whole aim branch.** |
| doLoad ordinary cargo | Ship, planet and/or partner ship, mass; AP star | Planet Put or partner save before final source save; sometimes earlier undock; separate transactions; no normal allocation | Transfer text before persistence; planet copy stale; generic branch also enters transporter/combat. **Defer isolated cargo design.** |
| doLoad crew/troops / unloadOntoAlienSector | Ship/sector/planet aggregate ownership/population/troops/mobility, sometimes two races/combat counters; AP | Multiple saves/PutRace/PutPlanet; no common transaction | Colonization/evacuation/combat output and telegrams early; stale copy plus partial records. **Defer assault and population staging.** |
| doTransporter | Source and destination ships/cargo/mass, optional linked context | Helper saves target, caller later source; AP in enclosing command | Other-player warnings/live or stored effects; cannot roll those back by bbolt. **Defer.** |
| Movement / movePopn / cmdWalk | Planets/sectors, races, walker ship and AP | Several independent PutPlanet/PutRace/saveShip or memory-only changes | Combat/news/notifications early; atomic conservation and counters not proven. **Defer.** |
| Navigation / land, launch, scrap, capture, dock | Ships/carriers/predecessors, planet/star/SData roots, cargo, races/AP; destruction/free list where relevant | Independent saves and immediate allocator operations | Link corruption/partial inventories possible; requires complete graph participation and held effects. **Defer.** |
| Build / upgrade / make / mod | Ship/builder, planet/sector stockpile, chain roots; ship allocator | Allocator -> actor mutations -> PutShip/install; unrelated planet changes remain memory-only | Success/design text early; build can install despite save error. **Defer.** |
| Sell / bid | Planet stockpile, commodity/allocator and governor/AP | Debit before allocate/PutCommod; independent metadata | Live announcements before write; cannot safely share the factory transaction. **Defer.** |
| CR-17A route / stockpile transfer | One planet, and transfer AP star | Checked single-planet transaction; transfer AP volatile | Existing failure guard retained; no AP repair/dedup claim. **Unchanged.** |
| CR-17B dump | Stars/planets and AP star; chain witnesses | One typed complete invocation transaction | Success after coherent install; uncertainty guard. **Unchanged.** |
| Other direct Put/save handlers | Single race/ship/planet, cursors and sometimes AP | One or more independent writes, unchecked save wrappers | Existing command-wide effects/errors remain outside coverage. **Unchanged; CR-17 stays open.** |

Implementation and fresh evidence follow after pre-fix reproduction. Existing
worktrees and the live game remain untouched.

## Implementation and authority boundary

`commandWorkspace` is private to the actor and this operation. Typed ship,
planet and star accessors capture immutable expected values, original actor
addresses and independent proposed copies on first access. Ship copying uses its
complete JSON model, owning all nine optional ShipSpecial pointers. Planets own
independent sector arrays; stars are value-only copies. Missing or inconsistent
identities fail closed. No race, SData, allocation, creation/deletion, general Tx
callback or external writer capability exists in this workspace.

`cmdOrder` intercepts a landed-factory `on` before printing or entering its legacy
loop. It accepts one explicit positive uint32 ship number (with optional #).
Invalid/overflowing IDs cannot reach the old Atoi-to-uint32 selector conversion.
A bounded, cycle-checked inspection refuses ambiguous/mixed selectors containing
a landed factory before another ship can be changed. Existing carrier-only and
other non-landed paths retain their legacy implementation: this is not a claim
that all `order` operations or all factory activation are safe. The old landed
branch in activateFactory is a non-writing refusal, preventing an alternate
landed writer through giveOrders. There is no public API or protocol change.

Owner and governor authorization are checked by the staged path (the legacy
numeric selector lacks the latter check). Existing transport session admission
still establishes player/governor identity and current permissions. No new
sector-owner, Guest or damage-zero rule is added; original C permits damaged
factories. Crew checks preserve the existing Max_crew-minus-troops behavior.
Current authority checks elsewhere are not declared complete.

A proposal changes only factory On=true/Notified=false and the owning player's
planet Resource minus twice BuildCost. Original `source/order.c:519–577` uses an
unsigned-int cost; the old Go uint16 temporary wrapped high costs. Defined costs
0, 14 and 65,534 are tested, and BuildCost 32,768/65,535 cannot wrap into an
affordable cost against a uint16 stockpile. This corrects defined C arithmetic;
it adds no price, resource cap or balancing rule. Factory activation has **no AP
charge** in this branch; zero AP succeeds and the complete star remains unchanged.

The read-only star and complete planet orbit chain participate as full preimages,
including unrelated ship fields. Destination/orbit coordinates must identify the
same landed planet and the factory must be reachable exactly once. A stale
participant, missing record, cycle, location disagreement or changed canonical
representation is refused, never merged. A full star preimage can expose earlier
volatile AP drift: this availability restriction is intentional, not an AP repair.
The existing ENROLLMENT_PENDING_TURN guard remains unchanged; a normal turn can
reconcile an admissible AP-only condition before a new activation.

## Database transaction and installation

`db.FactoryActivation` accepts only Player/Governor, a factory expected/proposed
ship pair, one planet pair, one read-only star and the complete read-only orbit
preimages. `Store.CommitFactoryActivation` validates the closed diff and checks
all complete serialized preimages **inside the same existing bbolt Update**.
It performs exactly two checked Puts: factory first, planet second. All identity
witnesses are read-only. Canonical JSON comparison is the same conservative
precondition policy as B; unsupported encodings are refused rather than rewritten.

The operation reuses B's CommandOutcome/CommandError, command mutex, per-handle
uncertainty guard and diagnostic readback. B's dump-only API and implementation
are unchanged. No new persistent metadata, bbolt bucket/key, schema, commit kind,
commit sequence, schedule, RNG or maintenance behavior is introduced. Between-turn
transactions coexist with CR-18 whole-world commits because the actor installs
both objects before it can admit a turn. CR-11's subsequent ship reload sees the
same committed ship; no stale source copy remains for this path.

Before persistence and again before installation, all workspace actor pointers
and complete values must still match their expected preimages. Read-only proposed
witnesses must also be unchanged. All checks finish before assignment. Only the
factory and planet install; their outer model addresses are preserved. No callback
occurs between those two assignments. Nested Special/sector storage is privately
owned; no actor cache retains borrowed nested pointers between these commands.
Other commands and their caches are not rewritten.

## Effects and truthful completion

The response header, activation text and order row are staged locally, at most
4,096 bytes and 16 messages. Unsupported JSON effects or overflow fail validation
without world changes. Success text is released only after confirmed persistence
and coherent installation. This operation has no other-player notification,
news, telegram, directory mutation or allocator effect. No global outbox is added.

The internal completion enum is evidence for a future structured result, not a
new text/WebSocket response contract:

| Case | Current behavior |
| --- | --- |
| Canceled while queued / refused before execution | Existing ErrNotExecuted/mode gate; operation does not run |
| Ordinary validation failure, including late budget/effect checks on a private proposal | VALIDATION_FAILED; discard copies; SERVING, no actor/store mutation |
| Confirmed callback abort or pre-transaction failure | NOT_COMMITTED; no installation/success, conservative HALT; old records remain |
| Confirmed nil Update | COMMITTED; install factory and planet, then release staged output |
| Confirmed commit followed by reporting/delivery failure or panic | COMMITTED_ERROR; committed records stand; complete installation when safe, HALT; never claim rollback |
| Commit attempt fails/panics, including new records visible after reported failure | UNCERTAIN; no unverified installation/success, shared guard and HALT |
| Diagnostic readback unavailable | Uncertainty stays uncertainty; no claim that visibility proves fsync |
| Actor identities changed or installation fails after confirmed commit | COMMITTED_ERROR; HALT before any partial proposal installation; no stale turn/shutdown flush |

All failures are sanitized; model values, hook text and response panic values are
not logged as diagnostics. HALTED refuses turns and does not flush on shutdown.
The existing guard prevents starting a new serving actor on the tainted Store
handle or bypassing uncertainty by selecting another command family. The guard
is per handle, not a new durable halt record. Reopening/retrying is not recovery;
preserve evidence for separately authorized operator reconciliation. No automatic
repair, retry, reopen or clear-uncertainty control exists.

`HandleCommand` returning nil still means dispatch completed, not a general durable
acknowledgment. A lost response can follow a committed activation. A repeated
activation normally finds On=true, but that state predicate is not durable
command-ID deduplication, historical result replay or exactly-once execution.
Do not blindly retry an uncertain operation under a fresh identity.

## Pre-fix reproduction and preserved evidence

The desired regression ran on unchanged runtime baseline through real
State.HandleCommand/actor/dispatcher/giveOrders. An existing responder callback
receives the actual activation-success text **after PutPlanet and before the
final saveShip**; it raises the existing write fence to make that subsequent
write fail. No runtime instrumentation or fake storage implementation was needed.
The fence is deterministic storage-failure injection; it does not claim that a
turn normally overlaps an actor command or that a physical disk error was
reproduced. The desired assertions failed for the actual defect:

| Stage | Resource actor/store | Factory On actor/store | Observation |
| --- | --- | --- | --- |
| Before | 100/100 | false/false | Full affected preimages captured |
| After first PutPlanet | 100/86 | false/false | A new stored planet copy disagrees with actor |
| After refused PutShip | 100/86 | true/false | Partial records and premature success |
| Segment, update, graceful restart | 100/100 | false/false | CR-11 reload drops On and CR-18 writes stale planet, refunding cost |

The independent reviewer repeated this from a fresh baseline archive with only
the preserved fixture/reproducer added (expected exit 1, 0.165s). Evidence files:
`cr17c-pre-fix.txt`, `cr17c-pre-fix-fixture.go.txt`,
`cr17c-pre-fix-reproducer.go.txt` and `cr17c-pre-fix-records.json.gz` contain
commands, desired failures and complete six-stage affected models. No credential,
password, bcrypt or token records occur in the compressed evidence. The temporary
reproducer is preserved as text, not left as a failing/disabled test in the suite.

## Permanent tests and crash acceptance

| Automated test | What executes / asserts |
| --- | --- |
| db.TestFactoryActivationAtomicAndReopen | Full records, immutable inputs, unchanged metadata/witnesses, reopen |
| db.TestFactoryActivationFailureAtomicity | Before/validated/each of two writes/before-commit error and panic; entire logical store and sequences unchanged |
| db.TestFactoryActivationCommittedAndUncertain | Committed reporting faults versus uncertain visibility; failed readback and cross-family guard |
| db.TestFactoryActivationInvalidFields | Closed field/identity/authority/chain families; nested pointer and nonfinite input rejection |
| db.TestFactoryActivationStaleAndFinalPreconditions | Planet/factory/star/witness/missing/noncanonical records; real intervening write after proposal preparation |
| db.TestFactoryActivationCWidthCostAndNoAP | Independent C cost boundaries, no overflow truncation, damaged factory and no AP debit |
| db.TestFactoryActivationFenceAndConcurrentConflict | Both fence checks/accounting; competing adapters on one handle yield one commit and one stale abort |
| game.TestCR17CFactoryCompleteStateAndBoundaries | Actual dispatch, full immediate models and stable addresses; segment/update/shutdown/restart retain activation/cost and valid orbit/government |
| game.TestCR17CFactoryWriteFailures | Actual handler reaches every write fault; no early output, no partial records or actor install, shared guard and no HALTED flush |
| game.TestCR17CFactoryValidationAndSelectors | Insufficient late-stage budget, inactive/already-on/no crew/unauthorized governor; numeric overflow alias, wildcard/mixed/extra-arg refusals without mutation |
| game.TestCR17CFactoryOriginalCCostAndRules | Zero and boundary costs, damaged factory and zero AP through dispatcher |
| game.TestCR17CFactoryStalePreimagesAndIntegrity | Actual legacy load, store changes, cycle/missing/orphan/mismatched actors, post-proposal intervening write |
| game.TestCR17CWorkspacePrivateCopiesAndBounds | All nine Special pointers and sectors are independent; readonly proposals/unsupported effects/overflow/nil copy rejected |
| game.TestCR17CFactoryReadbackAndInstallationFailures | Unavailable verification, install panic and replaced witness pointer; no partial install |
| game.TestCR17CFactoryResponseFailureAfterCommit | Responder error/panic preserves complete committed state and sanitized COMMITTED diagnostic |
| game.TestCR17CFactoryEnrollmentAndModeCompatibility | Immediate enrollment after activation; QUIESCED/HALTED refusals |
| game.TestCR17CFactoryOutputAndPrivateStateWaitForCommit | Barrier after first Put: readers see old records and actor/output stay unchanged until commit |
| game.TestCR17CFactoryAdmittedCommandAndFollowingActions | Second activation, quiesce, shutdown and started cancellation serialize; admitted operation completes once before drain |
| game.TestCR17CFactoryQueuedBehindTurnAndCanceled | Queued cancellation never executes; following activation waits behind real segment/fence |
| game.TestCR17CFactoryInternalCompletionClasses | Internal rejected/validation/aborted/committed/report-error/uncertain distinctions, supplementary direct actor calls |
| game.TestCR17CFactoryBoundedEffectsAndLegacyBypass | Huge renderer output rejected, old landed helper cannot write, cyclic wildcard terminates without prefix changes |
| game.TestCR17CFactoryAuthorizedGovernorAndMixedPrefix | Valid governor succeeds; a switchable mine before the factory cannot mutate on mixed-selector refusal |
| game.TestCR17CFactoryRetainsPendingAPAdmission | Real existing transfer -> pending AP admission -> normal update -> activation -> same enrollment retry |
| game.TestCR17CFactoryRealProcessCrash / TestCR17CFactoryCrashChild | Four actual SIGKILL transaction phases, full world/allocator/orbit evidence and post-restart turns |

The child harness exists only in `_test.go`; production ignores CR17C_FACTORY_*
variables and has no new debug/fault route. A fixed ready marker coordinates an
actual SIGKILL at factory.before, factory.write:1, factory.before-commit and
factory.after-commit. Before/during the transaction restart sees the full previous
world; after confirmed nil Update before reply it sees the full new ship/planet
pair. All auxiliary records, allocation and orbital identities remain valid.
Following segment and full update cannot erase the result. The existing injected
clock preserves exact fixture schedule bytes through startup; schedule comparisons
are not removed. No kill inside bbolt's own fsync/page-write code is claimed.
Known pre-/post-commit phase oracles restart; genuinely uncertain outcomes do not.

## Command-ID, deduplication and outbox design — not implemented

This is a proposal for separate owner review, not a new schema/client contract:

- A versioned structured command envelope should carry a stable client command
  UUID and server-validated verb/arguments. The server derives universe identity,
  player, governor and current session/credential epoch from authenticated authority.
- Use a reconnect-stable key such as (immutable universe identity, player,
  governor, command UUID), not ephemeral session ID. Revalidate current governor
  activity, credentials and privileges **before lookup/replay**, preventing a
  revoked session from recovering privileged historical output. Bind the stored
  dedup record to the immutable account/principal and governor authorization/
  credential epoch: a replacement governor in the same slot must not retrieve
  the former principal's output merely by knowing its command UUID.
- Bind a server-owned canonical intent fingerprint to that key. Changed intent,
  principal or universe must conflict. Exclude passwords/tokens and avoid exposing
  low-entropy secret hashes; use opaque credential/version references if relevant.
- Store every intended world mutation, AP debit, durable effect, dedup identity
  and result in the same transaction. No success before that commit. A retry with
  the same authorized key returns its recorded result without executing again.
- Define retention limits, acceptance windows and tombstone/expiry semantics
  before implementation. Expired IDs must return an explicit history-expired or
  unknown/reconciliation result, never silently execute again. Retention duration
  and persistent schema require owner decisions; no arbitrary limit is accepted here.
- Lost responses after confirmed commit require same-ID lookup, not a new ID.
  Uncertain persistence must retain operator reconciliation and may not turn into
  an automatic replay. At-least-once outbox delivery requires consumer event-ID
  deduplication; it is not exactly-once network delivery.
- Legacy text clients lack this identity/result envelope. They retain their
  existing contract and cannot be promised general durable acknowledgment or
  exactly-once retry. An additive negotiated protocol must be designed/reviewed
  with legacy behavior and security rollback limits before shipping.

No dedup table, outbox bucket, new protocol, command-batch commit kind or general
intent lifecycle is implemented. No existing ADR decision is changed: this is a
bounded implementation slice consistent with ADR-0001/0009/0010/0011; a future
schema/protocol decision must be separately reviewed and recorded before coding.
ADR-0009's established turn guarantees are not retroactively rewritten.

## Compatibility, remaining work and labels

CR-07A–F acceptance, CR-17A single-planet behavior/AP limitation, CR-17B complete
dump transactions, CR-18 commit/fence/recovery and CR-43 maintenance/shutdown
must pass freshly. CR-11 ship reload remains and reads the newly committed factory;
other commands can still lose ship mutations. PR #34/GB1-04 session cancellation
and 1012 close-frame behavior are preserved; network/authentication files are
unchanged. No public registration, new identity service or TypeScript work occurs.

Data format impact: **NONE**. Existing ship/planet JSON, buckets and keys only.
Read-only witnesses, metadata, allocators and CR-18's closed kinds remain
unchanged. Current startup and existing regressions provide fresh execution;
no new actual older binary run or production/security rollback claim is made.

Further CR-17C slices must stage complete habitat activation (including signed
hangar arithmetic, authority and turn-time size semantics), full telescope aim
and bounded noncombat cargo before tackling population/assault, transporters,
movement/navigation, construction and allocator participation. Group commit,
checked effects, durable IDs/retention/outbox require separate design approvals.
CR-17D must broaden real transport/concurrency/crash/allocator acceptance after
those foundations. CR-17, CR-11, CR-20 and unrelated inventory entries remain open.

Exactly two stale lint entries are pruned (old landed Planet-not-found Send and
truncating cost cast); total 1,008, no added/moved allowance. Maintainer-applied
**known-defect-added** is required. Local label simulation is not acknowledgment.
No protected gate tooling, workflows, original C, golden allowance or known-defect
inventory changed, so gate-change-reviewed is not required. The agent applies
neither acknowledgment label.

All mutations use disposable worlds. No live game/database, Kubernetes, Argo CD,
Longhorn, schedule, image publication/pinning, DNS, certificates, secrets or
deployment operation occurred. Existing worktrees and audit branch are preserved.

Fresh validation results and final independent review follow below.

## Baseline direct-store/save/allocation call-site inventory

At 51fd2b4 there are 98 sites in commands*.go, plus 14 dispatched build/allocation
sites and three unchecked save wrappers. Lines identify alternate branches, not
all calls executed in one invocation. CR-17B's helper API and CR-17A preimage
reader live outside this table; they remain unchanged. The landed helper's two
GetPlanet/PutPlanet sites are retired here; 96 commands*.go sites remain and are
not declared safe merely by their presence in actor methods.

| File / handler | Baseline call sites |
| --- | --- |
| `commands_cs.go` / `cmdCS` | PutRace:65 |
| `commands_fire.go` / `cmdBombard` | PutPlanet:448 |
| `commands_fire.go` / `cmdDefend` | PutPlanet:663 |
| `commands_load.go` / `doLoad` | GetPlanet:124; saveShip:145; saveShip:309; PutPlanet:310; PutPlanet:333; saveShip:355; PutPlanet:356; PutPlanet:379; PutPlanet:391; PutPlanet:410; PutPlanet:424; PutPlanet:438; saveShip:448; saveShip:456 |
| `commands_load.go` / `cmdJettison` | saveShip:571 |
| `commands_load.go` / `doMount` | saveShip:745 |
| `commands_load.go` / `doTransporter` | saveShip:838 |
| `commands_load.go` / `unloadOntoAlienSector` | PutRace:1031; PutRace:1034 |
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

## Fresh execution results and independent review

Executed on 2026-11-11 UTC (2026-11-10 America/Denver), Go 1.27.3 linux/amd64,
against the frozen source hashes in [the sanitized execution ledger](evidence/cr17c-validation.json).
Raw test logs/credentials are not committed; the ledger records commands, results,
test names and output hashes. No previous reported pass is substituted for these
fresh executions.

| Check | Actual result |
| --- | --- |
| gofmt / build / vet | PASS |
| `go test -count=1 -json ./...` | PASS; 368 top-level passes, 3,513 passing nodes; two opt-in long tests skipped |
| `GB_LONG_TESTS=1 go test -count=1 -race -json ./...` | PASS; 370 top-level passes, 3,515 passing nodes; zero skips/failures |
| New C tests | All 160 nodes / 25 top-level entries (including child harness) pass in both full runs |
| Original C fixture reproduction / golden tests | PASS; exact fixture reproduction; full suites execute golden parity |
| Known-defect inventory | PASS; all tested entries reconcile, no inventory change |
| Golden mutations | PASS; all 15 scenarios retain expected exits/diagnostics |
| Gate approval / lint-gate / golden helper self-tests | PASS; helper cases repeat 100 times |
| Lint | PASS; 1,008 baseline/report/matched, no new or stale findings |
| Growth / staged and working-tree diff checks | PASS; two pure removals, no protected tooling changes; local simulated label only |
| Offline manifests | PASS; structural/mutation/safety suites, 21 resources schema-valid |
| Workflow/helper tests / policy | PASS; 184 tests and 17 policy checks |
| actionlint / shellcheck | PASS; no findings |
| govulncheck v1.9.0 | PASS; zero reachable vulnerabilities; 17 non-reachable module findings reported |
| Module verify / tidy diff | PASS; all modules verified and no drift |

Full suites freshly rerun CR-07A–F, CR-17A/B, CR-18, CR-43 and PR #34 regressions,
including real subprocess/HTTP/CLI/WebSocket acceptance, authentication, complete
empires, idempotency, turn/crash recovery, 1012 close frames and maintenance.
No prior safety guard, allowance or assertion is weakened. Docker builds, Trivy,
Syft and kind are hosted GitHub Actions work; they were not run on this host.
GOCACHE=/tmp/cr07a-cache and TMPDIR=/tmp/GB-cr17c/test/ctool/obj/tmp keep mutation
module VCS stamps legitimate; no VCS/test validation is disabled.

Independent design/source review approves the bounded landed slice, with no
blocking partial-write or state-corruption defect found. The reviewer freshly
reproduced the original failure from an exported baseline and ran focused race
suites (db 2.938s, game 40.316s), final critical race tests (db 1.506s, game 7.651s)
and vet/diff checks. All fourteen requested areas were examined. The mixed-selector
mine-prefix test and explicit principal/epoch binding in the future dedup design
address review suggestions. Final evidence-ledger review passed: all source, execution and artifact hashes
and counts match. No blocking defect remains in the migrated slice. Conditions
are maintainer acknowledgment, normal owner review and green hosted CI.
