# CR-17A: bounded authoritative planet edits

Baseline: `57fa9f1b219d3d2c69e09da544218fdd43801a3c`.
Branch/worktree: `feature/cr17a-planet-state-ownership`, `/tmp/GB-cr17a`.
The shared audit checkout and all earlier worktrees remain untouched.

## Scope decision and status

This implementation corrects **merchant-route edits (`route`) and planet
stockpile transfers (`transfer`)**, two of the six original mutable GetPlanet
paths. It deliberately does not claim to fix load/unload, exploration dump,
factory activation or telescope aiming. Those operations couple planet, ship,
star and sometimes race writes and effects. Converting their reads to actor
pointers alone would preserve partial-write/data-loss hazards. They require a
separately reviewed multi-record/effect boundary, outside this authorization's
safe bounded subset. CR-17 remains open; the other commands are not disabled or
rewritten here.

The owner explicitly allowed a smaller safe implementation instead of a
misleading partial multi-record solution. Independent design review selected
this subset and required a full target-planet preimage check: otherwise a route
edit could immediately erase newer stored cargo changes left by an uncorrected
load. Final implementation/evidence review and hosted CI are recorded below.

## Original defect and fresh reproduction

The actor loads `State.planets` once. The six command paths independently call
`Store.GetPlanet`, edit the returned JSON-decoded object and write it. The actor
retains another object. CR-18 correctly commits its authoritative in-memory
world, so a subsequent segment/update/shutdown overwrites the command's newer
planet with that stale object. CR-18 transaction atomicity does not resolve
ownership disagreement.

The new `TestCR17APlanetEditsSurviveBoundaries` was first executed against
unmodified runtime master, with only its disposable fixture/assertions added.
It uses real `State.HandleCommand` → actor admission → dispatcher → handlers:

| Observation | Original route | Original transfer |
| --- | --- | --- |
| Before command | route disabled in both | crystals 100/10 in both |
| Immediately after command | actor false, store true | actor 100/10, store 93/17 |
| After real movement segment | false/false: edit erased | 100/10: transfer erased |
| After real full update | edit still lost | transfer still lost |
| After graceful stop/reopen/restart | edit still lost | transfer still lost |

Both desired regressions failed for the expected reason. The independent
reviewer reproduced the same failures from a fresh `git archive` of the baseline,
copying only the original fixture and these two desired assertions into it.
[Preserved pre-fix output](evidence/cr17a-pre-fix.txt) includes both executions,
the source revision and command. It contains disposable numeric state only.

The fixture enrolls two real empires through CR-07 components in a seed 404
disposable world. Both players own sectors on the test planet, avoiding normal
turn redistribution of stockpiles belonging to an absent player; crystal deposits
are zero so legitimate mining cannot disguise the tested transfer. Resource/fuel/
destruct production, population and orbital movement remain normal across turns.
Tests compare complete initial planet and ship records, then relevant invariants
across legitimate commits, rather than requiring unchanged world bytes.

## Six-path control-flow inventory

Line references here describe the baseline. Every affected path had no focused
command persistence regression before this work; existing enrollment drift,
formula, lifecycle and turn tests cover adjacent safeguards, not successful
round trips through these handlers.

### L1: load/unload (`doLoad`, commands_load.go:124)

- Reads: actor race, directory, selected/source ship, docked partner, star/AP;
  independent stored planet for landed transfers.
- Actor mutations: ship/partner cargo, crew/troops, mass, sometimes docking;
  AP in star/StarData; alien assault can change both races and combat counters.
- Store mutations/order: eight alternative PutPlanet sites; five saveShip sites
  in doLoad. Normal planet transfers write planet before final source ship;
  ship-to-ship writes partner then source. Alien-assault branches save source
  then planet and continue. Auto-transporter may write another target ship;
  assault writes two races and can generate an offline telegram.
- Failures/output: success/casualty/colonization messages are emitted before all
  writes complete. PutPlanet/PutRace errors log and continue; saveShip discards
  errors. Partial writes, early continuation and panics can split coupled state.
- Turn/restart: stale actor planets overwrite persisted stockpiles, population,
  sector ownership and counts. Ship reload preserves whatever actually reached
  disk, potentially producing cargo/planet imbalance. Graceful shutdown also
  overwrites the planet. Not atomic; deferred to CR-17B/C.
- New evidence: `TestCR17ALegacyLoadCannotBeOverwrittenByPlanetEdit` invokes real
  `load #<government> r 7`: actor resources 1000, stored 993, ship cargo 7. A later
  corrected route/transfer refuses that disagreement, preserves all records/AP/
  allocation/placement evidence, HALTs and skips shutdown flushing. This guards
  the new subset; it does not fix or endorse the legacy load defect.

### L2: exploration dump (`cmdDump`, commands_load.go:679)

- Reads: actor source/target race permissions, known stars and scope/AP;
  independent stored planets to inspect source exploration flags.
- Actor changes: target star explored bits, source AP. Store order: PutStar per
  selected star, then PutPlanet per explored planet, then volatile AP debit and
  live target notification/success.
- Failures/output: failed star write logs/continues; planet errors log while
  remaining writes proceed. A late failure leaves an arbitrary completed prefix;
  final success can still be printed. More than one record must change together.
- Turn/restart: actor star bits survive normal flush, but planet exploration bits
  are lost from stale actor copies. No focused preexisting regression; the CR-07
  non-AP drift suite protects enrollment only. Deferred, not made safe by replacing
  GetPlanet or claiming a completed dump is atomic.

### L3: stockpile transfer (`cmdTransfer`, commands_load.go:725)

- Reads: actor scope, star/AP, target race, actor planet. Before the write, a
  read-only stored planet is compared with that full actor preimage.
- Changes: two players' resource/crystal/fuel/destruct balances in **one** planet
  record. Old code printed sender/recipient success before PutPlanet, logged write
  errors and debited AP anyway; stale actor balances erased the durable edit.
- New order: validate → private actor-derived proposal → checked existing
  PutPlanet → actor installation → AP debit → sender and live-recipient output.
  warnRace→notifyRace is registry-only; no hidden telegram transaction occurs.
- Failure: no balances/AP/success change before confirmed write; precondition
  failures or write error/panic halt and prevent shutdown flush. A visible record
  after an error is never promoted to confirmed durable success.
- Atomicity/restart: both planet balances commit together. AP is still volatile
  until segment/update/shutdown; output delivery is best effort and no command
  deduplication exists. All commodities, self/zero/max transfers and defined
  unsigned destination wrap retain C assignment arithmetic. See tests below.

### O1: factory activation (`activateFactory`, commands_order.go:665)

- Reads: actor factory/carrier and design/capacities; independent stored landing
  planet. Habitat branch debits carrier resource/hangar and changes factory size,
  saves carrier, prints activation, then giveOrders sets On and saves factory.
  Planet branch debits 2×BuildCost, PutPlanet, prints activation, then sets On and
  saves factory. Habitat cost remains 4×BuildCost.
- Failures: saveShip errors discarded; PutPlanet error only logged and function
  still returns nil. Thus the factory can be enabled despite failed payment.
  Early output and coupled records require a transactional/effect solution.
- Turn/restart: stale planet copy can restore paid resources; CR-11's ship reload
  preserves whichever ship writes succeeded. No focused persistence regression;
  source-inspected, unchanged and explicitly deferred.

### O2: telescope aiming (`mkExplAimedAt`, commands_order.go:936)

- Reads: actor ship aim/position/type/technology, actor star and independent
  stored planet. giveOrders modifies the ship aim before this helper.
- Changes/order: star target exploration is written with PutStar on star aim.
  Planet aim sets actor star explored and writes only the independent planet's
  target exploration flag; the actor star is not immediately saved on that
  branch. giveOrders later saves the aimed ship.
- Failure/output: PutStar/PutPlanet errors log and continue; Surveyed is printed
  before final ship persistence. Out-of-range/missing targets and later errors
  do not constitute an all-record rollback.
- Turn/restart: stale planet loses exploration while ship/star can survive on
  different boundaries. No focused prior regression; coupled ship/star/planet
  mutation remains deferred, not silently converted to unsafe actor mutation.

### O3: merchant routes (`cmdRoute`, commands_order.go:1009)

- Reads: actor scope/planet and destination stars/place metadata. Read-only route
  listing now uses actor state; a mutation compares a read-only stored preimage.
- Changes: one merchant route's enablement, destination, landing coordinate or
  load/unload masks inside one planet. Old code wrote a separate planet after
  printing Set, losing the edit on the next actor commit and hiding write errors.
- New order: validate → private actor-derived route edit → preimage check →
  existing atomic PutPlanet → install into actor record → Set. No ship or AP is
  changed. Query/listing performs no write.
- Failure/restart: unchanged actor on failed validation/write; mismatch/error/panic
  HALTs with no success or stale shutdown flush. All route variants, complete
  unaffected planet/sector/cargo/link fields and segment/update/restart durability
  are tested. No full command staging scheduler or new storage operation exists.

## Additional findings and persistence boundary

The static appendix lists 105 direct store/save call sites in `commands*.go`
at the baseline, including all six original mutable GetPlanet paths. These files
use saveShip rather than direct PutShip. `build.go` also implements dispatched
commands and uses direct PutShip/commodity operations; its additional sites are
listed separately below. Shared helpers are important dependencies:

- `helpers.go:484–496`: savePlanet/saveShip/saveStar discard errors. These are
  unchanged for unrelated commands, not safe persistence APIs.
- `commands_load.go:864–950`: transporter changes source and target ships, saves
  only target inside the helper (caller saves source); early live/possibly
  offline warning effects complicate a batch. Alien unloading changes two races
  and battle counters as well as planet/ship. These dependencies block a trivial
  load rewrite in this PR.
- Navigation land/launch/scrap/capture and docking modify chain heads, predecessor
  links, carrier cargo and ships through several saves. They mostly borrow actor
  planets already, but separate/unchecked writes remain a distinct hazard.
- Population move/deploy/walk, bombard/defend, mobilization, enslave, autoreport,
  tech, profile/diplomacy and message-read cursors have additional direct writes
  or memory-only effects. Their presence is inventoried, not declared corrected.
  Existing golden/turn tests do not prove whole-command atomicity for them.
- `build.go` build/upgrade/make/mod and market commands: build changes actor
  planet/sector stockpiles, population and ship-chain heads, reserves a number
  through the immediate allocator, then PutShip and installs the built ship even
  after a reported save error. Upgrade/make/mod mutate actor ships and print
  results before PutShip. Sell debits the actor planet before allocating/writing
  its commodity and notifying players; bid reads/writes the commodity separately.
  These coupled writes and early effects remain outside this subset, with no
  whole-command regression/atomicity claim. A subsequent route/transfer detects
  an uncommitted actor planet difference and refuses to overwrite it.
- `build.go:659–679`, `helpers.go:60–120`, `planet.go:1365`: orbit insertion/removal
  changes actor roots/links; number allocation between turns remains the existing
  immediate allocator. None of these helpers is invoked by corrected route or
  transfer edits. Their ship-number/free-list and cross-record obligations belong
  to later work. CR-07 placement retains its own atomic allocator/preimage checks.

## Implementation rule and limits

`planetEdit` deep-copies actor sectors as well as the value-only planet fields.
`commitPlanetEdit` compares the complete persisted planet with the actor preimage,
then uses **existing** Store.PutPlanet. The read is an admission check, never a
mutable source. The actor remains the only writer; the preimage read/write are
not an atomic compare-and-swap against unsupported foreign writers. No second
writer or reconciliation merge is introduced.

A successful bbolt return confirms the single record; installation uses struct
assignment into the existing actor planet address before success output. Proposals
are private. Generic errors/panics may occur after visibility: remain HALTED with
an explicit unconfirmed outcome, retain old actor state, withhold AP/notifications
and skip shutdown flushing. No automatic retry, repair, reopen or false rollback
claim is added. `HandleCommand` returning nil still means dispatch completed;
the legacy text protocol is not a structured durable acknowledgment. Command
metrics now classify a non-panic HALT as halted instead of ok.

Strict amount/route/coordinate parsing rejects malformed, overflowing, trailing
numeric input and wrong argument counts instead of accepting scanf's partial/zero
defaults. Invalid/insufficient-stockpile transfers no longer spend AP or rewrite
an unchanged planet. This intentional validation departure implements the
authorization's invalid-command/no-mutation requirement. Valid rules/arithmetic
remain as original `source/load.c:707–813` and `source/order.c:818–1004`, including
the existing uint16 destination wrap; a new balance-cap/gameplay rule is not
invented. Existing commodity-mask and prefix conventions remain.

This does **not** provide full command atomicity: transfer planet balances and
its AP debit have different persistence boundaries. A crash after the planet
commit can preserve both balances but lose the debit; a lost response permits
ambiguous retry without deduplication. The regression intentionally shows stored
and actor AP disagree until a normal commit. No full crash-safe acknowledgment,
durable command ID/outbox/group commit or multi-record API is claimed.

## Compatibility and tests

Focused tests use initialized disposable CR-07 worlds and actual actor dispatch.
All intended failure hooks assert reachability; none is configurable through
production options, environment or a public endpoint. The package-private write
hook wraps real Store.PutPlanet for fence and post-visible failures.

| Test | Evidence |
| --- | --- |
| TestCR17APlanetEditsSurviveBoundaries | prefail preserved; full initial planet/ship; correct route and crystal balances through segment/update/graceful reopen |
| TestCR17ALegacyLoadCannotBeOverwrittenByPlanetEdit | real legacy load creates stale target; both corrected operations refuse without overwriting, HALTED shutdown preserves disk |
| TestCR17APlanetEditWriteFailures | before error/panic, real CR-18 fence refusal/accounting, real write then error/panic; no success/AP/install, no allocator or placement change, no turn/shutdown flush |
| TestCR17APlanetEditEffectsWaitForWrite | deterministic write barrier, no premature state/output, independent proposal/sector copy, install before result |
| TestCR17AInvalidPlanetCommandsDoNotMutate | malformed/range/overflow/insufficient inputs preserve actor and full persistent evidence |
| TestCR17ARouteFieldsAndActorReads | every route field, maximum route index and read-only actor reporting |
| TestCR17ATransferCommoditiesAndArithmetic | all commodity aliases, self/zero/source extrema, historical unsigned destination wrap; whole-planet preservation |
| TestCR17AStartedEditFinishesBeforeShutdown | Stop while admitted write is blocked; complete edit precedes shutdown commit, no weakening of mode/cancellation gate |
| TestCR17AUnreadablePreimageHaltsWithoutWrite | real closed-store read error, no proposal installation/flush; stopped-store diagnosis unchanged |
| TestCR17AQuiescedAndHaltedPlanetEditsAreRefused | both operations refused by actual actor admission without changes |
| TestCR17AEnrollmentAdmissionAfterPlanetEdit | route permits immediate enrollment; transfer's AP-only divergence gives ENROLLMENT_PENDING_TURN without writes/HALT; normal update reconciles and same request succeeds |

CR-11's ship reload is unchanged; corrected commands mutate no ships. CR-18
CommitWorld/fence/schema/closed kinds and startup/reload remain unchanged.
CR-43 durable quiescence/resume and normal shutdown remain unchanged. Tests use
the existing write-in-progress boundary, not the global pre-mode-check dispatch
hook, to prove admitted writes finish before Stop. CR-07's full non-AP disagreement
and pending-AP guards are preserved; no conservative enrollment guard is removed.
Database format impact: **NONE**.

## Validation and independent review

Fresh execution on the implementation sources (hashes and results in
[evidence/cr17a-validation.json](evidence/cr17a-validation.json)):

| Command/check | Actual result |
| --- | --- |
| `gofmt -l .`, `go build ./...`, `go vet ./...` | PASS; formatting output empty |
| `go test -count=1 -json ./...` | PASS; 317 top-level tests, 3,214 test nodes; two opt-in production-duration tests skipped |
| `GB_LONG_TESTS=1 go test -count=1 -race -json ./...` | PASS; all 319 top-level tests, 3,216 test nodes; zero skips/failures |
| New CR-17A tests in each full run | PASS; 11 top-level tests, 81 test/subtest nodes |
| `test/scripts/golden-mutations.sh` | PASS; all 15 scenarios retain their expected exits |
| `make -C test/ctool check` | PASS; C fixtures reproduce exactly, including racegen |
| `test/scripts/inventory-check.sh` | PASS; full execution/reconciliation, no allowance changes |
| `test/scripts/lint-gate.sh` | PASS; 1,020 baseline/report/matched, zero new or stale findings |
| `test/scripts/growth-gate.sh origin/master` | PASS on staged tree; 22 unique/32 occurrence removals, no protected tooling changes; simulated known-defect-added label only |
| `deploy/kustomize/tests/run.sh --require-published-image` | PASS; 143 structural checks, mutation/safety suites; all 21 rendered resources valid |
| Python workflow/helper unit tests | PASS; 184 tests |
| Workflow policy / actionlint / shellcheck | PASS; 17 policy assertions; no lint output |
| `govulncheck@v1.9.0 ./...` | PASS; zero reachable vulnerabilities (17 non-reachable module findings reported by tool) |
| `go mod verify`, `go mod tidy -diff` | PASS; all modules verified, no module drift |
| `git diff --check`, `git diff --cached --check` | PASS |

The complete fresh suites include CR-07A C parity, CR-07B placement, CR-07C
atomicity/idempotency and the current-reader compatibility startup fixture,
CR-07D actor integration, CR-07E/F
real subprocess/HTTP/CLI/WebSocket enrollment and crash acceptance, CR-18 turn
failure/reload/startup checks and CR-43 durable maintenance. These are executions
of existing tests, not a claim to add new transport coverage here. Long tests
were enabled in the full race run. Container builds, scanning and kind are hosted
CI work; they were not run on this host. Hosted CI is pending PR creation.

Commands ran from this worktree (`go/` for Go commands), with
`GOCACHE=/tmp/cr07a-cache` and `TMPDIR=/tmp/GB-cr17a/test/ctool/obj/tmp` for Go/test
processes. The temporary directory stays inside the worktree so temporary
mutation modules can obtain legitimate VCS build stamps. No VCS/test validation
was disabled. The JSON ledger records output hashes and only sanitized test
names/results; raw process output with disposable credentials is not committed.

Independent source/design review approves the bounded route/transfer design;
the reviewer freshly reproduced both original failures from the baseline and
passed focused race tests. It corrected a new test's initial barrier placement:
the existing beforeCommandDispatch hook precedes the real mode gate, so the
shutdown test now synchronizes inside the actual write. The actor gate was not
changed. Final independent decision: **APPROVE WITH CONDITIONS** for this
bounded subset, with no blocking finding in the corrected paths. The reviewer
checked all ten requested areas, independently verified the complete run counts,
source/output hashes and inventory, and freshly ran focused race tests (58.346s)
and final build/vet plus mode/effect/shutdown race tests (8.109s). Conditions are
maintainer acknowledgment, normal owner review and green hosted CI. Four deferred
paths and volatile AP durability remain explicit CR-17 follow-up work.

Exactly 32 obsolete errcheck baseline occurrences (three scans, 29 sends) are
removed from the corrected handlers; no allowance is added or moved. The
maintainer must apply **known-defect-added** as required by CLAUDE.md, even if a
technical growth gate accepts a pure shrink. Local label simulation is not owner
acknowledgment. No protected tooling, workflows, golden allowance or inventory
changes occur; gate-change-reviewed is not required. The implementation agent
applies neither label.

## Proposed CR-17B/C/D handoff

- **B:** design/review bounded coupled-record transactions and output handling
  for load/unload/assault/transporter, dump, factory and telescope operations;
  include AP and orbital identities where required. Reproduce interruption at
  each write and define unknown durability/reconciliation before enabling fixes.
- **C:** command staging and group commit, consistent ship/planet ownership,
  checked storage errors, held effects, durable command IDs/outbox and explicitly
  defined acknowledgment/retry semantics under migration plan 8.4. No closed
  CR-18 schema/kind change is implicit authorization.
- **D:** command-wide transport, concurrency, crash/restart/allocator and
  operational acceptance after those foundations, including remaining inventory
  write paths and client/session limits. Public exposure and live rollout remain
  separate authorizations.

These are recommended next units, not claims they are implemented. CR-17,
CR-11/CR-20 and other gameplay defects remain open. No live game/database,
Kubernetes, Argo CD, Longhorn, image, DNS, certificate, secret or deployment
operation occurred; all state changes used disposable local fixtures.

## Complete command call-site inventory at the baseline

Static source inventory at `57fa9f1`; calls on alternate branches are separate
sites, not a claim that every call runs in one invocation. Lines are baseline
references. The six detailed paths above are reviewed for control flow; this
appendix makes other related direct writes and unchecked save wrappers visible.

| File / handler | Store/save call sites (line) |
| --- | --- |
| `commands_cs.go` / `cmdCS` | PutRace:65 |
| `commands_fire.go` / `cmdBombard` | PutPlanet:448 |
| `commands_fire.go` / `cmdDefend` | PutPlanet:663 |
| `commands_load.go` / `doLoad` | GetPlanet:124; saveShip:145; saveShip:309; PutPlanet:310; PutPlanet:333; saveShip:355; PutPlanet:356; PutPlanet:379; PutPlanet:391; PutPlanet:410; PutPlanet:424; PutPlanet:438; saveShip:448; saveShip:456 |
| `commands_load.go` / `cmdJettison` | saveShip:571 |
| `commands_load.go` / `cmdDump` | PutStar:674; GetPlanet:679; PutPlanet:685 |
| `commands_load.go` / `cmdTransfer` | GetPlanet:725; PutPlanet:787 |
| `commands_load.go` / `doMount` | saveShip:857 |
| `commands_load.go` / `doTransporter` | saveShip:950 |
| `commands_load.go` / `unloadOntoAlienSector` | PutRace:1143; PutRace:1146 |
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
| `commands_order.go` / `giveOrders` | saveShip:624 |
| `commands_order.go` / `activateFactory` | saveShip:660; GetPlanet:666; PutPlanet:677 |
| `commands_order.go` / `mkExplAimedAt` | PutStar:924; GetPlanet:937; PutPlanet:947 |
| `commands_order.go` / `cmdRoute` | GetPlanet:1011; PutPlanet:1126 |
| `commands_profile.go` / `cmdToggle` | PutRace:91 |
| `commands_profile.go` / `cmdName` | saveShip:181; saveStar:205 |
| `commands_profile.go` / `doDockOne` | saveShip:532; saveShip:533; saveShip:591; saveShip:592; saveShip:597; saveShip:598; saveShip:644; PutRace:822; PutRace:823; saveShip:842; saveShip:843 |
| `commands_tech.go` / `cmdTech` | savePlanet:60 |
| `commands_tech.go` / `cmdDeclare` | PutRace:197 |
| `commands_teleg.go` / `cmdTelegram` | PutRace:168 |
| `commands_teleg.go` / `readTelegrams` | ReadAndClearTelegrams:204 |
| `commands_teleg.go` / `readNews` | LatestNewsSeq:247; ReadNewsFrom:252; PutRace:277 |
| `commands_teleg.go` / `checkForTelegrams` | HasTelegrams:284 |

Additional dispatched commands and allocation helpers in `build.go` (baseline
lines; not included in the 105 `commands*.go` sites):

| Handler/helper | Store/allocation sites |
| --- | --- |
| `createShipByPlanet` | allocShipNum:531 |
| `createShipByShip` | allocShipNum:597 |
| `buildFromPlanet` | PutShip:887 |
| `buildFromShip` | PutShip:1031; PutShip:1056; PutShip:1082 |
| `cmdUpgrade` | PutShip:1341 |
| `cmdMake` | PutShip:1518 |
| `cmdMod` | PutShip:1751 |
| `cmdSell` | allocCommodNum:1890; PutCommod:1918 |
| `cmdBid` | GetCommod:2010; PutCommod:2051 |
| `listCommodities` | GetCommod:2061 |
