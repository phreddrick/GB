Independent agent review history. Later dispositions supersede earlier conditions; historical failures are preserved.


---

# Independent CR-17C #41A source review: initial semantics and scope

Reviewer: `/root/population_independent_review`. Read-only repository review at
`/tmp/GB-cr17c-population`, baseline `96570333540c1695b1d4f5fce2838e6b2975dbe1`.
No repository/source/index/branch/worktree edits; private report only. This is
source investigation, not execution or final implementation approval.

Read CLAUDE.md, source/load.c, source/move.c relevant combat branches, ships.h,
vars.h, shlmisc.c authority, land.c overload, model types, Go doLoad/groundMove/
unloadOntoAlienSector, cargo rules/actor/storage/workspace, and prior staging
records. Issue scope relayed by parent: #38 broad persistence remains open;
#41 crew/troop/assault family remains open; #51 independent CR-19 capture defect.

## Defined ordinary C rules

- load.c:73–84 requires selected owner==caller, alive, active and
  `authorized(governor, selected)`; shlmisc.c:292–295 makes governor0 universal
  within own ships, otherwise selected.Governor must match. No new partner
  governor, crew, On, damage-zero, terrain, habitability or tax prerequisite.
- load.c:95–147 requires docked selected. For planet landing, directory star and
  planet coordinates match; it does not additionally require directory LevelPlan.
  For a carrier, selected.WhatOrbits==Ship admits the containing relationship.
  External docking requires reciprocal destination identity. Invalid partner
  repair/undocking in old code must not escape a staged invocation.
- C `overloaded` (land.c:414–417) uses Resource>effective resource cap,
  Fuel>effective fuel cap, Popn+Troops>literal max_crew, Destruct>effective destruct
  cap; no crystal overload. Only a partner itself carried in another ship is
  refused for overload by load.c:133. This existing peculiarity is not replaced
  by an unrelated stricter total-capacity rule.
- Signed C-int amount: unload negates, then VN negative test, then compute
  signed [lower,upper], reject outside interval, then expand effective0 to
  upper(load) or lower(unload). Negative arguments reverse direction. VN default
  unload0 can therefore expand negative after the early VN check, exactly as C.
- c capacity: `(factory Shipdata MAXCREW else MaxCrew)-Troops`; available space
  further subtracts current Popn. m capacity is symmetric, subtract Popn then
  current Troops. No shuttle cargo exception for people. Wide signed arithmetic
  is needed to preserve overfull negative bounds and avoid Go unsigned wrapping.
- Same-owner partner: upper=min(partner relevant people, selected free space),
  lower=-min(selected relevant people, partner free space). Different-owner
  c/m partner: BOTH bounds0, so no nonzero transfer; literal/default0 can no-op.
  Do not introduce new nonzero cross-owner permission or partner-governor gate.
- Planet: upper=min(target sector relevant people, selected free space),
  lower=-selected relevant people. No planet/sector unloading capacity cap.
- Mass changes use `races[Playernum-1].mass`, not Ship.Race or Sector.Race. The
  selected owner species is authoritative. Do not require Race==Owner.
  Selected mass += signed amount*Race.Mass. Partner mass -= that amount except
  owner-approved containing-carrier aggregate exemption. C's Boolean `sh` passed
  to landed_on is an already documented typo; do not reproduce carrier-ID1 bias.
- Original dispatcher supplies APcount0 and load.c never deducts AP. Preserve
  approved0 AP; relevant root records remain complete read-only witnesses.

## Mutation/effects inventory

| Variant | Mutable records/fields | Other effects/risks |
| --- | --- | --- |
| Same-owner contained/peer c | Selected Popn/Mass; partner Popn and applicable Mass | Local location/complement/popn output only; independent partner/source saves currently split |
| Same-owner contained/peer m | Selected Troops/Mass; partner Troops and applicable Mass | Same, no race/sector/AP/allocator mutation |
| Caller-owned planet-sector c | Selected Popn/Mass; target embedded Sector.Popn, optional Owner; Planet.Popn and Info[caller].Popn; optional Info NumSectsOwned/MobPoints | C colonization/evacuation and complement text; Go independent PutPlanet then source save leaves stale actor planet |
| Caller-owned planet-sector m | Selected Troops/Mass; Sector.Troops, optional Owner; Planet.Troops and Info[caller].Troops; same ownership accounting | Same |
| Empty neutral actual deposit c/m | Same writable ship+planet pair; target sector Owner becomes caller; Info NumSectsOwned++, MobPoints+=Mobilization | COLONIZED(c)/OCCUPIED(m) text; no terrain, Race, explored/inhabited or other prerequisites |
| Evacuate last combined resident | Same pair; sector Owner0; Info NumSectsOwned--, MobPoints-=Mobilization | evacuated text; other population type prevents evacuation while nonzero |
| Foreign owned target c/m | Selected people/mass; target sector defenders/capture; planet ownership bookkeeping; BOTH races translation/morale; groundAssaults counter; RNG | Combat output, defender notices/telegram, race puts and later independent ship/planet saves; deliberately EXCLUDED |
| Active targeted selected transporter | May mutate third ship, resource/crew/mass, notices/telegram | EXCLUDED #42; not part of this two-record contract |
| Invalid partner | Old Go can undock/save source before commodity rejection | Must fail closed before legacy repair |

For defined noncombat C, Sector.Race, Planet.Explored, per-player Explored,
star exploration/inhabitation, Race records, terrain/resources, other players,
news/telegram/allocator/directory are unchanged. Writes are only selected ship
plus partner ship or one full planet containing sector/accounting. No schema or
outbox dependency was found for the ordinary slice itself.

## Source bugs that need explicit disposition

C load.c:284–301 and329–346 claims any empty sector on negative amount even if
already caller-owned; it also clears/debits any resulting empty sector regardless
of previous owner or actual quantity. Consequently:

1. Empty neutral load0/unload0 decrements caller ownership/mobility even if caller
   has other sectors and the subtraction does not wrap. Integer-only overflow
   checks do NOT fix that silent accounting corruption.
2. Already caller-owned empty sector deposit increments the count/mobility twice.
3. Already caller-owned empty sector zero command clears/debits it: this can be
   retained as C cleanup if owner wishes, unlike unowned counter loss.

Do not silently hide these with a new crew minimum, habitat requirement, owner
sector exclusion or no-zero restriction. Recommend owner-approved minimal
ownership-transition accounting: claim/increment only neutral empty actual
deposit; evacuate/decrement only a previously caller-owned sector when its
resulting combined population is0. That keeps C's owned-empty cleanup, corrects
the two counter bugs and adds no colonization permission. Await explicit owner
approval before treating this as the implemented semantic contract.

Unowned sectors with positive people are not a stable supported native-population
model: source/doplanet.c:289–291 clears all population/troops when owner0.
The user's wording is *unoccupied neutral*. Such input should be identified as
inconsistent/unsupported, rather than granting ownership or inventing racial
conversion. If the parent intends broader handling, require a separate decision.

All changed numeric results must be checked in their actual widths: ships and
sector people uint16, per-player planet totals uint32, planet totals/MobPoints
uint64, NumSectsOwned uint16. Avoid converting an arbitrary uint64 aggregate to
int64 before checking, and calculate sector combined populations in a promoted
wide type (Go uint16+uint16 wraps; C integer promotions do not). No wrap or
nonfinite/negative mass result may commit.

## Current CR-19 verification, carefully scoped

The historical broad phrase 'ground assault never captures' is not accurate for
all current paths. In commands_move.go `groundMove`, groundAttack at534–539 is
followed by checks of sect2.Owner==caller at544/554, but no assignment on defender
elimination; C move.c:333–334 explicitly assigns caller. That missing ownership
assignment remains visible by current source inspection.

However commands_load.go `unloadOntoAlienSector` DOES assign Owner at954 and
updates both ownership counts at966–969. It still has independent race/ship/
planet persistence and early combat/telegram effects; this is not a closure of
#51/#41. Ship assault and ground movement have distinct correctness questions.
This review executed no combat reproducer yet and does not falsely claim runtime
verification; parent should preserve a disposable current-master reproduction.

## Storage/staging review criteria for follow-up

Use separate closed c/m operation rather than broadening CargoTransfer's allowed
resource/crystal whitelist. Actor private deep proposals, selected explicit-ID
identity and governor authority, full target sector and planet, root/parent/
orbital/child membership and caller race mass must all be witnessed. Read-only
race/AP/context records are not rewritten. DB must recompute exact permitted
fields and perform all full-byte preimage checks inside the same bbolt Update.
Install stable actor addresses only after confirmed commit; no callback between
installing the two writable participants; release local bounded effects later.
Maintain existing command mutex/fence/maintenance refusal/uncertainty guard and
HALT/no-flush policy. Hostile c/m and targeted transporter invocations must never
fall through to old writer, including malformed/mixed selectors.

Final implementation, crash/fault, regression and evidence review remains pending.
No tests run and no final implementation approval is implied by this report.

---

# Independent CR-17C #41A implementation and test review

Reviewer `/root/population_independent_review`; read-only review of
`/tmp/GB-cr17c-population`, baseline9657033. No repository file, original C,
index, branch, existing worktree or live service was modified. Private Go source
and test overlays were used for causal experiments, with explicit timeouts,
recorded commands/exit statuses and logs in `/tmp/cr17c-population-evidence/`.
This review is independent agent review, not maintainer merge authorization.

## Scope and approved decisions

The initial independent source inventory is preserved separately in
`reviewer-initial-semantics.md` (SHA-256
7b2adfb4558f39e2fe3b8655b91575fd7ca18bf0ca446c3fae64afcd8456e780).
Its then-pending ownership-counter question was subsequently explicitly approved
by the owner, as relayed by the parent: counters/mobilization change only on
actual neutral→caller or caller→neutral transitions. Initial recommendations
must not be read as unapproved rules silently implemented later.

Implemented exceptions now are approved zero AP, approved actual containing-
carrier aggregate mass treatment (rather than C's Boolean sh typo), approved
ownership-transition accounting and checked rejection of unrepresentable numeric
results. Original C c/m legality/default/capacity/race-mass semantics otherwise
remain. No extra terrain, slavery, species liking, sector Race==Owner, selected
Ship.Race==Owner or partner-governor permission was introduced.

Populated owner0 sectors retain literal C arithmetic when checked aggregate
results can be represented; no new blanket prohibition was invented. Stable
normal C turns clear such populations (doplanet.c:289–291), but this operation is
not a global imported-world/ownership-accounting repair. It preserves the exact
allowed deltas and fields; full-world consistency is not inferred for an already
inconsistent imported preimage. This is more faithful to the user's no-narrowing
instruction than silently rejecting all such inputs or granting new ownership.

## Findings and dispositions

### R1 — wide-sum test effectiveness gap, corrected

Current production rules already used separate wide emptiness checks correctly.
The initial 'wide residents' regression started with65535+1 but removed one whole
commodity, leaving1 or65535; it did not exercise an overflowing combined *result*
that could falsely trigger evacuation. I independently replaced the final
emptiness expression with Go uint16 `Popn+Troops==0` in a private source overlay.
All then-existing rules Population tests still passed (exit0).

A private added full-hold/no-op case keeping65535+1 residents passed current code
and failed the mutant (exit1) because it cleared Owner and debited counters.
The parent added permanent `TestPopulationWideResidentsNeverAliasEmpty`, plus
actor wide-noop cases. I independently repeated the mutant against the permanent
test: it fails specifically on false evacuation of65536 residents, exit1. The
permanent test also checks neutral populated wide inputs are not falsely claimed.
**R1 RESOLVED.** No current production wide-sum defect was found.

Private evidence:
- reviewer-existing-tests-uint16sum.log/.exit: initial tests missed mutation.
- reviewer-wide-original.log/.exit: added counterexample passes correct source.
- reviewer-wide-mutant.log/.exit: added counterexample fails erroneous source.
- reviewer-wide-mutant-permanent.out/.err/.result.json: permanent causal failure.

### R2 — post-turn regression assertion gap, corrected

The initial permanent original-defect regression captured segment/update/restart
snapshots but checked only immediate quantity conservation and final actor/store
agreement. Actor and store could agree after a command was lost. I requested
explicit retained source/recipient quantities and account totals at every phase.

The parent added `assertPopulationRetained` to after-command, segment, update and
restart captures. It checks exact selected/recipient c/m quantities, complete
actor/store participants, finite mass and sector-derived planet/caller totals
and ownership count. No existing assertion was removed. Fresh normal parent run
and my final focused race run pass the strengthened fixture. Crash tests also
cover full persisted old/new worlds and post-turn retained identities/accounting.
**R2 RESOLVED.** This was test/evidence completeness, not a discovered current
transaction defect.

### R3 — coherent authority negatives, independently demonstrated

The author added direct DB coherent selected-owner/governor cases during review.
For c/m, foreign gifts cannot serve as otherwise-valid payloads, so the selected
owner case sets BOTH selected and partner Owner2 while caller remains1. Whole
expected/proposed/context/stored records and c/m deltas remain coherent. Governor
case enables caller governor1, assigns selected governor2, with legal transfer.

Baseline coherent cases pass their negative assertions (exit0). Independently
removing ONLY the DB source-owner predicate makes `/source_owner` fail because
an unauthorized command COMMITTED with nil error (exit1). Removing ONLY the
selected-governor predicate makes `/source_governor` fail identically (exit1).
These are causal permission regressions rather than generally malformed models.
**R3 SATISFIED.** No missing production authority check was found.

Private evidence: reviewer-authority-original.*,
reviewer-owner-counterexample.*, reviewer-governor-counterexample.* and the
associated private overlays. Every run used `timeout 60s`.

## Required correctness questions

1. **Every mutation included:** write set is precisely selected ship plus one
   partner ship or full planet with embedded target sector. c or m and relevant
   mass are the only writable ship fields; target commodity, matching planet
   total/Info total and approved Owner/count/mobilization transitions are the
   only planet differences. Sector.Race/terrain/other players are unchanged.
2. **C arithmetic:** effective signed amount, bounds-before-default ordering,
   negative reversal, omitted/literal0 and VN early-negative/default behavior
   agree with load.c. Crew and troop capacities subtract the other commodity;
   factories use Shipdata MAXCREW; no shuttle exception is added.
3. **Ownership/accounting:** claim only actual empty-neutral deposit; owned
   empty deposits do not double-count; unowned empty zero does not debit;
   caller-owned empty zero keeps C ownership cleanup. Last combined resident
   evacuation updates Owner/NumSectsOwned/MobPoints together with quantities.
4. **Conservation/widths/mass:** uint16 ship/sector, uint32 Info and uint64 planet/
   mobility results are checked. No int64 narrowing of arbitrary uint64 inputs.
   Selected/recipient commodity changes are equal/opposite; planet/Info totals
   move identically to target sector. Immutable caller Race.Mass is used. Mass
   must remain finite/nonnegative; carrier aggregate exception is approved.
5. **Authority:** selected owner, selected governor, living/active/docked ship,
   active non-dissolved caller/governor and bounded identities are checked in
   actor and DB. No selected Ship.Race or partner governor restriction is added.
   Foreign partner c/m limits are0/0; nonzero transfer is refused both directions.
6. **Witnesses:** map key/Number identity, complete root and containing/parent
   chains, live unique list membership, landing geometry, reciprocal peer IDs,
   caller race and directory are captured. Root star/universe AP context is
   immutable. Missing, dead, duplicate, cyclic or inconsistent context rejects.
   No unrelated descendant/mass audit or navigation repair is claimed.
7. **DB contract:** new PopulationTransfer is closed to this family; CargoTransfer
   and other closed APIs are not broadened. DB recomputes exact permitted changes
   and compares every full persisted preimage inside one bbolt Update before
   either checked Put. Input arrays are copied before sorting; sector proposal
   whitelist owns a new slice rather than mutating the expected preimage.
8. **Actor installation:** workspace ships and sectors are deep private copies;
   no live world mutation before commit. `owns` rechecks identity/content before
   and after persistence. Both existing object addresses are assigned coherently
   without callback between assignments. Read-only witnesses are not installed.
9. **Output/external effects:** bounded local location/complement/claim/evacuation
   text releases after coherent install. Ordinary C c/m has no required durable
   news/telegram. Response error/panic retains confirmed install then HALTs; no
   false rollback/success from failed storage is emitted.
10. **Legacy bypass/exclusions:** first doLoad interceptor captures all c/m-prefixed
    commodity tokens; exact grammar and explicit uint32 ID prevent mixed/wildcard
    or malformed paths executing a prefix. Hostile sectors return deferred before
    helper/combat/translation/morale/groundAssaults/telegram effects. Active
    targeted transporters refuse; invalid docking never runs old undock repair.
11. **Abort/commit/uncertainty:** existing phase classifier, diagnostic readback,
    shared Store guard and HALT/no-stale-flush are preserved. Callback failures
    or panics abort both writes; confirmed postcommit failures retain both;
    uncertain writes install nothing. Visible readback does not imply fsync.
    No retry, reopen or repair is automatic.
12. **CR-18/CR-43:** write fence checks both before admission and in transaction,
    with refusal accounting. Strict durable maintenance is checked inside the
    same transaction. Actor mode/draining/queue semantics remain unchanged;
    admitted request may finish after cancellation, canceled queue entries do not
    run. HALTED shutdown refuses stale flush.
13. **Real crashes:** actual test executable blocks at real hooks; parent verifies
    process SIGKILL. Six boundaries across planet crew colonization, planet troop
    evacuation, ship crew load and ship troop unload:24 actual kills. Reopen
    checks complete old or new world and auxiliary/allocator evidence; subsequent
    segment/update verifies participants and ownership. Only known test-oracle
    abort/confirmed outcomes restart, never genuinely uncertain durability.
14. **Compatibility/deferred defects:** no schema/bucket/allocator/commit kind,
    authentication, public route, actor loop or turn change. Existing CR-07,
    cargo, factory, habitat, telescope, CR-18/43 are not weakened. CR-11/13/20 and
    broad CR-17 remain open; zero AP introduces no new pending-turn disagreement.
    CR-19 movement's missing capture assignment remains source-verified, while
    ship-unload assault already assigns Owner; both are excluded, not closed.
15. **Test reality/effectiveness:** actual actor dispatch and disposable bbolt
    stores, full affected/unrelated snapshots, pre-fix post-real-Put hooks,
    source/authority/wide-sum counterexamples and complete crash assertions.
    Focused tests intentionally do not reconcile full known-defect inventory;
    full suite/inventory/gate validation remains a separate acceptance condition.

## Fresh independent execution and source provenance

Initial focused run used `timeout 480s go test -count=1 -race -json
./internal/commandrules ./internal/db ./internal/game -run Population` via the
private runner, Go1.27.4/GOTOOLCHAIN=local, elapsed148.267s, exit0,345 passing
nodes/22 top-level tests/24 SIGKILL leaves. Its captured DB source hash changed
only in a comment grammar correction during execution; the strengthened retained
assertions were added later. It is preserved as historical evidence, not falsely
represented as final exact-source acceptance. LogSHA:
63f9b39ed9968ac4bff5eedc119db42656e3fc496a25bcd603efd5b840c520f6.

After strengthened assertions and the comment fix, captured eleven relevant
source/test/hook hashes in reviewer-followup-source.json, then reran the same
bounded focused race command. **Exit0,144.444s,345 passing nodes,22 top-level
entries,zero failures/skips,all24 real SIGKILL leaves.** All eleven source hashes
still matched afterwards. Package times: rules1.096s, db5.033s, game138.616s.
Stderr was empty; no race warning occurred. Log reviewer-final-race-followup.out
SHA-256:
a354b6854c505adea0b7bbf0de84cd9fa74c1c9f146df858f115255efd357b82.
The runner sets GOTOOLCHAIN=local, verified Go1.27.4 path and disposable TMPDIR.
No live store or container tool was involved.

Decoded preserved pre-fix archive independently:11,797,378 raw JSON bytes,
no password/credential/token/secret/JWT key names or bcrypt markers.
Compressed SHA0777e506a941114e9129ca497a87acbec7397a879203d1e301b5ec18ffef7f72;
uncompressed SHA68cefdae935b48a835b75d8c345059518f170f243a27b8c3bf7ffb0549cc4da8.
Historical report, overlays, positive passes and expected failures are retained.
No fixture/golden allowance, assertion, timeout or gate was weakened for review.

## Code/test verdict

**APPROVE CODE AND FOCUSED TESTS for normal PR review of this bounded operation.**
No unresolved blocking gameplay, partial-write, ownership, authority, mass,
actor-installation, output or uncertainty defect was found. Review-requested
regression gaps were corrected and independently demonstrated causal.

This is not complete final acceptance: the parent must complete the fresh full
normal/long race suites and all gates, finish documentation/ledger and request a
final actual-evidence audit. Hosted CI and maintainer review remain conditions.
No merge/deployment approval, whole #41/#51 closure, generic command durability
or exactly-once guarantee is implied.

---

# Independent review: parallel scheduling of new population test groups

Read-only source review, after the parent's complete long-race run encountered
the existing default ten-minute game-package deadline, and a second run finished
just below that limit. Those execution results are parent-reported here; they
must remain separately preserved in the final validation ledger. No repository
or source was edited by this reviewer for this scheduling assessment.

**Recommendation: APPROVE adding t.Parallel only to the newly added top-level
game TestPopulation groups, except TestPopulationCrashChild.** Leave every
subcase, assertion, synchronization barrier, fault, crash boundary and deadline
unchanged. This is not timeout relaxation, a test skip or reduced coverage.

Inspected population fixtures/tests and their cargo/factory/enrollment helpers:

- Each fixture owns its State actor, database file under t.TempDir, Config,
  callback policy, log buffer, test hooks, record proposals and responders.
- testConfig uses config.MapEnv, not process-global environment changes.
- Default State options create a private metrics registry, not a global registry.
- Cached bcrypt credentials and binary-version caching use sync.Once; returned
  credential strings are immutable copies. No test mutates the shared cache.
- No new group uses t.Setenv, os.Setenv, Chdir, global model-table mutation or
  rand.Seed. Universe generators own separate seeded rand.Rand instances;
  shared generation/ship/range/defense tables are read-only in these tests.
- Legacy turn helpers use math/rand top-level functions. The verified Go1.27.4
  source explicitly documents these as safe for concurrent use. There is no
  shared unsynchronized rand.Rand instance in these paths.
- The mutable production global crashRoll is used only by crashCheck in the
  navigation command path; population fixtures/commands/turn paths do not call
  that operation. No new population group invokes land/dock repair.
- Each crash child receives a private cmd.Env and database path. The sole
  top-level crash parent keeps its24 subcases sequential. Its child entry must
  stay nonparallel so the targeted subprocess handshake cannot pause waiting on
  unrelated top-level test scheduling.
- Fixture cleanup closes only its own actor/store/process. New groups do not
  signal unrelated processes, share write-fence state, or modify source.
- Go's top-level Parallel scheduling pauses these groups until sequential
  top-level tests finish. Existing tests are not made parallel or altered.
  Group-internal concurrent actor tests preserve their own deterministic barriers.

No shared mutable dependency was found that would make this scheduling change
unsafe. It increases concurrent independent-engine exposure. The expected time
saving is not itself evidence of correctness; new concurrency must pass fresh
normal and full long-race runs under the unchanged default package deadline.
A focused GOMAXPROCS=4 race run is useful to check hosted-runner CPU/deadline
pressure. Do not claim all hosted hardware timing is proved locally.

This approval concerns the proposed test-only scheduling change. Final-source
race execution and full-suite/evidence verification after applying it remain
pending; earlier focused source hashes will differ in the changed test files and
must be preserved as earlier executions rather than silently reused.

## Applied change and fresh independent execution

Verified exactly twelve new top-level game groups now call t.Parallel; the crash
child remains serial and no subcase was made parallel. An initial private
placement checker incorrectly rejected the leading explanatory comment; the
corrected comment-aware checker passes and its complete listing is recorded in
reviewer-parallel-placement.txt. This was a checker heuristic error, not a Go
source/test failure; no repository code was changed by this reviewer.

Captured all eleven relevant runtime/test/hook SHA-256 values before running:

`timeout 480s env GOMAXPROCS=4 go test -count=1 -race -json
./internal/commandrules ./internal/db ./internal/game -run Population`

The private runner pins Go1.27.4 and GOTOOLCHAIN=local. **Exit0,57.798s,345 passing
nodes /22 top-level entries /all24 actual SIGKILL leaves;zero failures/skips or
race warnings.** Stderr is empty; game package elapsed50.992s compared with the
prior serial-focused138.616s. All eleven source hashes match after execution.

Log reviewer-parallel-race-4cpu.out SHA-256:
5159b872cbad81d959ad9225121f5f23523260e30ce5f3b2a2e2a7e87fd4311b.
Commands, timing and exit status: reviewer-parallel-race-4cpu.result.json.

GOMAXPROCS=4 caps this Go runtime's scheduler and testing's default parallelism;
it is not proof of a dedicated physical four-vCPU VM or every hosted timing.
No timeout flag, test deadline, synchronization barrier, assertion or acceptance
case changed. The full combined normal/long-race suite still must pass fresh.

I also independently inspected the first complete long-race failure log: it
contains `panic: test timed out after 10m0s`, with the old authentication-delivery
test listed as running only4s and its actor stack in bcrypt. This is consistent
with cumulative package runtime, not evidence that that old command deadlocked.
The earlier failure and the near-limit serial pass must remain in the ledger;
parallel scheduling does not erase their history.

**Final scheduling/focused-test verdict: APPROVE.** No new shared-state hazard or
weakened validation was found. Complete final-source suite/gate/evidence audit
and hosted CI remain conditions on overall PR acceptance.

---

# Final independent implementation and acceptance-evidence review

Reviewer `/root/population_independent_review`, bounded noninteractive read-only
review of `/tmp/GB-cr17c-population`, baseline
`96570333540c1695b1d4f5fce2838e6b2975dbe1`. No repository source, index, branch,
other worktree or live system was modified by this reviewer. Private source/test
overlays and disposable runtime stores were used for the independently recorded
focused tests and causal experiments. This is independent agent review; it is
not maintainer acknowledgment or merge/deployment authorization.

## Final verdict

**APPROVE implementation and local acceptance evidence for normal PR review.**
No unresolved blocking transaction, authority, population-conservation,
ownership/accounting, actor-installation, premature-output or uncertainty-handling
defect was found in the bounded noncombat c/m operation. The review findings and
the scheduling issue were resolved without weakening any assertion, timeout,
scanner, allowance or gate. The final record should incorporate this verdict,
superseding historical pending-evidence dispositions.

Overall **PASS WITH CONDITIONS** is accurate: all seven hosted CI jobs and
normal maintainer review remain outstanding; the explicitly deferred broader
CR-17, #41, CR-19/#51, CR-11/13/20 work remains open. This does not approve
merging, deployment, public enrollment or any live operation.

Three minor documentation precision corrections were sent to the parent before
publication: source/load.c ends1183, so the cited1038–1219 range should be
1038–1182; 'Existing application commands unchanged' should explicitly say
'Other application commands' to avoid denying this PR's intentional c/m change;
and the earlier pending-final-audit sentence should be marked historical or
superseded by this completed final review. All three corrections were subsequently confirmed in the final document; none
required a Go code/test change. No documentation correction remains open.

## Independent execution, mutation causality and resolved findings

The initial C/source inventory, implementation review and scheduling review are
preserved in the review history. They distinguish original rules, owner-approved
counter corrections and proposed rules that were not silently adopted.

- A private uint16-sum mutation initially escaped the then-existing tests. The
  parent added the permanent wide-noop regression; independently rerunning that
  mutation fails specifically on false evacuation of65536 residents. Current
  source passes. No production wide-sum bug remained in the reviewed source.
- Coherent owner and governor negatives independently fail when only their
  respective DB guard is removed, showing unauthorized COMMITTED results. These
  tests do not merely reject an unrelated malformed proposal.
- Later command/segment/update/restart captures now explicitly assert retained
  source/recipient quantities and complete actor/store/planet accounting. Mere
  final agreement can no longer hide a lost command in that regression.
- The current actual handler is exercised, and restoring only its interception
  in a private overlay reintroduces all eight transfer failures plus the two
  approved ownership-accounting edge failures. The original complete pre-fix
  evidence is retained separately.
- Independent pre-parallel final-source race:345 pass nodes /22 top-level tests /
  all24 real SIGKILL leaves, exit0,144.444s. All eleven captured hashes matched.
- Independently reviewed the later test-only scheduling change: exactly twelve
  NEW top-level game groups are parallel; CrashChild and all subcases remain
  unchanged/nonparallel. Private fixtures, MapEnv, per-child cmd.Env, read-only
  tables and sync.Once caches have no new shared mutable dependency.
- Fresh post-scheduling independent race, `GOMAXPROCS=4`, rules/DB/game `-run
  Population`:345 pass nodes /22 top-level tests /all24 SIGKILL leaves, exit0,
  57.798s, empty stderr and no race warning. All eleven post-scheduling hashes
  matched after execution. This caps Go scheduling/test parallelism; it does not
  purport to prove every dedicated hosted-hardware timing.

Independent final focused log SHA-256:
`5159b872cbad81d959ad9225121f5f23523260e30ce5f3b2a2e2a7e87fd4311b`.
Earlier pre-parallel log and all positive/negative-control commands/results are
preserved, rather than relabeled as final-source execution.

## Final ledger and raw-log audit

I independently parsed the final validation ledger and actual private outputs:

- **All11 final source/test/hook SHA-256 values match current source.**
- **All5 archive/raw-artifact SHA-256 values match.**
- **All52 stdout/stderr log pairs match their recorded hashes.**
- Final complete normal suite:4593 pass nodes,460 passing top-level tests,
  two existing opt-in long test skips,21 tested packages, exit0,49.144s.
- Final complete long race:4595 pass nodes,462 top-level tests,zero test skips,
  21 tested packages, exit0,859.455s. Game elapsed562.097s remains inside the
  unchanged600-second package deadline. Both long tests ran.
- Final population subset within the full logs has345 pass nodes and24 SIGKILL
  leaves. Six no-test-file packages are not misrepresented as skipped acceptance
  cases. Counts are test nodes, not individual assertions.
- Final mutation output states all fifteen scenarios behaved as required, exit0,
  720.855s, including both expected-pass and expected-failure scenarios.
- Lint output: baseline1000/report1000/matched1000/new0/stale0. No allowance edit.
- C fixtures reproduce exactly; inventory reconciles tests actually executed;
  golden PASS1248/XFAIL143/XPASS0/FAIL0 is retained.
- Build/format/vet, module verification/tidy-diff, gate self-tests, actionlint and
  shellcheck/static workflow policy pass. Python workflow helpers188 tests and
  static policy17 checks pass. Offline manifests21 valid,zero errors/skips.
- Strict govulncheck reports0 reachable/imported-package vulnerabilities and17
  noncalled module advisories; the latter were reported, not suppressed.
- Final staged whitespace/growth checks pass. The diff contains no protected
  workflow/gate tooling, lint baseline, inventory or expected-divergence changes.
  Neither known-defect-added nor gate-change-reviewed is required by this diff;
  no maintainer acknowledgment label is applied by the reviewer.

The first complete long-race run genuinely hit the existing10-minute package
alarm. I inspected the actual panic text and bcrypt stack: the old auth-delivery
test was listed as running4s at the cumulative alarm. The near-limit serial pass
and the final scheduling-corrected run remain separate ledger entries. Initial
syntax/helper/factory-expectation/lint failures and deliberately failing
counterexamples are also retained; the report does not falsely call every
historical execution green or increase a timeout to erase that history.

## C behavior, trust and transaction completeness

The current implementation matches the approved C-derived scope:

1. Selected owner/governor, living active docked identity and full caller race
   authority are checked in actor and DB. No partner-governor or Race==Owner
   restriction is invented. Same-owner ship transfers are supported; foreign
   ship nonzero c/m transfers remain prohibited both directions, with C zero
   no-op allowed at0,0 limits.
2. Signed amounts, limits-before-default, negative reversal, factory effective
   shared crew/military capacities and VN ordering are preserved. No shuttle
   resource exception is misapplied to people. Exact caller Race.Mass is used.
3. Planet transfers move exactly selected vs landing-sector population and
   corresponding planet/Info totals. Checked widths prevent wrap and nonfinite
   mass. Approved actual ownership transitions update count/mobility exactly once;
   all unrelated sector/player/race/resource/exploration fields are preserved.
4. No invented neutral habitat/terrain/technology/slavery requirement exists.
   Imported populated-owner0 states retain literal C deltas subject to checked
   bounds; no global ownership repair or new capture permission is claimed.
5. Full required caller, directory, root, parent and orbital-membership witnesses
   are captured; AP is immutable under the approved zero-AP rule. Missing,
   duplicated, dead, cyclic or inconsistent required records refuse safely.
6. Complete preimage checks occur inside one bbolt write transaction before both
   writes. The separate closed operation recomputes allowed changes; expected
   sector slices are not mutated by whitelist construction. Read-only witnesses,
   allocators, metadata, schedules and commit kinds are not rewritten.
7. Private deep actor proposals are installed into stable object addresses only
   after confirmed persistence and complete identity recheck, without a callback
   between the two assignments. Bounded output follows installation.
8. Fault/panic handling preserves confirmed abort, committed and uncertain
   distinctions, shared guard and diagnostic-only readback. HALT prevents stale
   turn/shutdown flushing. No automatic retry/reopen/repair or exactly-once
   command protocol is invented.
9. Foreign-sector and active-targeted-transporter invocations cannot enter the
   legacy writer. All hostile c/m directions/zero cases are explicitly deferred
   before combat/race/groundAssaults/telegram effects. Other operations remain
   separately open; r/x/& cargo contracts are not broadened.
10. Actual24 process kills cover crew colonization, troop evacuation, ship crew
    loading and ship troop unloading at six real storage/install boundaries.
    Complete old/new persistent records and auxiliary/allocator state are checked
    after reopen, then subsequent segment/update checks expose stale overwrites.
    No physical disk/power-loss/fsync-failure guarantee is falsely claimed.
11. Existing enrollment, staged cargo/factory/habitat/telescope, atomic turn,
    maintenance and WebSocket regressions remain in the complete executed suite.
    CR-17A volatile AP and CR-11/13/20 broader state/turn effects remain open.
12. Current CR-19 is accurately scoped by source: movement still lacks C's
    defender-elimination owner assignment; ship-unload assault already assigns
    Owner but has separate durability/effects concerns. Neither is fixed or
    closed by this noncombat PR. #41B needs a separate complete combat/effects
    design and coordination with #51, including any approved durable schema.

No false whole-#41 or #51 closure, schema/protocol/commit-kind change, live system
operation or merge approval was found in the reviewed scope/evidence. Parent
should append this final disposition and retain hosted CI as an explicit pending
condition before normal maintainer review.
