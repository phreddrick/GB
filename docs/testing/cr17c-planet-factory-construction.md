# CR-17C #44: one durable planet-built factory

Local implementation for independent review, **not accepted or committed**.
Base: `8da02c783fa72e4b16333754ce7675fec16e505c` (PR #85 merge).
Tracking: #44, #50, parent #38 remain open. No live operation or schema change.

## Exact scope and compatibility

One ordinary non-God planet-scope `build F x,y`, autoload OFF, with one
effective build. Existing Go first-character type selection, `fmt.Sscanf`
coordinate behavior, ignored trailing arguments, and effective-count parsing
are preserved. Omitted count, explicit 1, nonpositive or invalid counts yielding
the legacy default 1 enter staging. C rejects nonpositive counts; this unit
does not silently change the existing Go behavior. Factory design, resource
cost truncation, mass/size, location, damage 75, inactive factory repair rules,
empty complement, class and all initialization notices match the legacy path.
There is **zero AP cost**: C build ignores its AP argument; Go also has no
AP check/debit. The complete star (including AP) is an immutable witness.

Autoload, God builds, multiple effective builds, every other type (including
special types), ship-scope factory production, cargo/carrier builders, upgrades,
make/mod and commodities remain on their existing legacy handlers. The
classifier returns before staging for those cases. They retain existing CR-17
risks; this PR does not make all factory construction or all build transactional.
No prefix of an eligible one-build command passes through the old writer.

## Original defect and evidence

The read-only audit is preserved at
`/tmp/gb-construction-audit-20261118T043000Z/ASSESSMENT.md` and its SHA256SUMS.
Real actor dispatch showed child persistence before planet resource/link saves,
and a consumed free ID after child-save refusal. Scheduled segments could
discard the unsaved child while persisting its resource cost. Ship-builder
resource/complement and hangar loss are separately confirmed, excluded defects.
The audit's six SIGKILL cases establish split-record crash windows, not torn
bbolt transactions. Its evidence is not overwritten or treated as acceptance.

This implementation's atomic dispatch test is also run with read-only Go
overlays: restoring only baseline `build.go` or omitting only the transaction's
planet write must fail for premature success/incomplete persistent records.
Intentional defects remain external negative-control files, never source edits.

## Closed transaction and ownership

`commandrules.PlanPlanetFactory` is pure and reconstructs the entire permitted
factory and planet proposal. It validates authority, non-God/autoload scope,
star/planet identity, slaving, governor assignment, owned populated nonwasted
sector, checked size/mass/cost/coordinate widths and the complete planet-root
chain. Dead ships awaiting cleanup are permitted immutable witnesses. Stale
destination-order fields are not used to establish actual orbital location.
The result owns its sector slice; no actor mutation occurs during planning.

`db.PlanetFactoryBuild` / `CommitPlanetFactory` can write only:

1. The absent/new factory record, with its exact validated ID and fields.
2. The complete planet: only caller resources and the ship-list head differ.
3. The exact consumed free-list entry, if allocation reuses a free ID.

Race, star/AP and every existing planet-root ship are full immutable persisted
preimages. Actor directory and authenticated player/governor context remain
actor-owned; storage independently checks the supplied exact scope and race
authority. Config coefficients are trusted immutable server inputs, as in the
existing cargo contracts. No sector, population, race, AP, schedule, CR-18
record, news, telegram, enrollment record or allocator sequence is rewritten.
Existing inconsistent aggregates are not silently repaired; this operation
neither reads them for cost nor changes them (autoload is excluded).

The read-only allocator ticket uses construction-specific validation in
`db/factory_build_alloc.go`: free-list sequence/content digest, widths/duplicates,
candidate absence, append-overflow and contradictory structural references.
Construction does not reuse CR-07's more restrictive reference policy. Inside the **same** bbolt Update,
all world preimages are compared and the allocator ticket is recalculated and
compared before any writes. Append IDs are not separately reserved; free IDs
are not popped until this transaction. Sequence metadata is preserved. The
transaction cannot overwrite an existing record or lose a free ID on abort.
Enrollment's closed contract and CR-18 commit-kind set are unchanged.

Structural references reserving an absent ID are ship `NextShip`/`Ships`,
universe/star/planet chain heads, race government-ship identities, existing
enrollment placement government-ship identities, and `DestShipNo` when
`WhatOrbits == LevelShip` (the actual containing parent). Dead records awaiting
cleanup still participate. Record keys, identities and decoding are checked.
`Protect.Ship` (on or off), `Target`, `Special.AimedAt.ShipNo`,
`Special.Transport.Target`, and other `DestShipNo` values are advisory orders,
not allocations or identity reservations. Those fields remain part of complete
preimages for any participating ship; this distinction does not weaken CAS.
No enrollment validator or general allocator implementation is changed.

The bounded path deliberately refuses dissolved races and inactive governors
before proposal installation or persistence. This is explicit authorization for
this slice, including direct actor-dispatch callers; it is not a claim that all
legacy build handlers enforce the same rule. Permanent tests cover both refusals.

The actor uses the existing private commandWorkspace for immutable preimages,
planet proposals and bounded effect buffering. The new factory is a private
new object, not a borrowed actor ship. After confirmed commit it installs the
planet into its existing address and adds the new ship identity with no callback
between those installations. Existing ship addresses are preserved. Only then
are the original registry notices released; absent listeners retain the legacy
best-effort behavior. There are no durable external messages in this operation.

## Pending legacy state versus failures

Read-only admission validates both actor and stored topology. Otherwise valid
race/AP/planet/ship differences, compatible partial ship-list saves and supported
actor-only record evidence defer with a manual retry-after-next-persisted-turn
hint, no writes, no success and no HALT. An actor-occupied allocation candidate
also defers rather than overwriting it. This is not repair or proof of the old
writer's intention, and an unsaved legacy construction is not promised retention
through a later turn. Missing/decoding failures, malformed links, cycles and
contradictory freed records retain conservative safety handling. Late changes
after admission are still strict transactional CAS failures, not deferrals.

The operation shares the command mutex, CR-18 write fence, durable-maintenance
refusal, outcome/error types and store-handle uncertainty guard. Confirmed abort
installs nothing and follows existing conservative HALT/no-shutdown-flush policy.
Confirmed commit with reporting failure remains committed and installed where
installation completed; a failed install HALTs without stale flushing. An
uncertain commit installs no unverified proposal and HALTs. Readback is only
diagnostic visibility, not proof of fsync. No retry, deduplication, exactly-once
delivery, new command ID, outbox or automatic recovery is introduced.

## Tests and validation record

Permanent tests exercise actual dispatch, append and free reuse, complete
snapshot comparisons (including allocator sequence and unrelated records),
legacy parameter/message parity, zero AP, effective-count/coordinate grammar,
authority/refusals, unsupported classification, pure bounds/ownership, stale
preimages and late allocator/world writers, same-ticket concurrency, abort/error/
panic boundaries, uncertainty/readback failure, installation/delivery failure,
real legacy fuel-load deferral, maintenance/fencing, scheduled segment/update,
shutdown/restart, and stable actor object addresses.

There are **20 new real SIGKILL scenarios**: append and free allocation across
before admission, preimage checking, validated proposal, ship write, planet write,
allocator pop, precommit, confirmed postcommit, preinstallation and installed
before effect release. Full logical database snapshots require complete old or
new records, including auxiliary metadata/free-list sequence. Child stderr is
bounded/drained and race reports fail tests. Reopened worlds are checked through
segment/update. These are disposable process crashes, not physical disk-failure
or live Longhorn acceptance. Earlier crash tests remain unchanged.

Local toolchain: Go 1.27.4, GOTOOLCHAIN=local; default 600-second package timeout
unchanged. Validation is recorded under
`/tmp/gb-planet-factory-build-validation-20261118T045600Z/` with final-source
hashes, commands, outputs, initial failures, reruns, complete patch and SHA256SUMS.
The initial sandbox run could not download tools/bind localhost; the initial
permitted lint run found six new findings, corrected with explicit bounds and
safe test I/O/close handling without suppressions or baseline changes. Earlier
runs were stopped only within this task's owned runner trees before final-source
validation. Final results are recorded separately; no earlier pass is substituted.

The original completed local results are preserved below. Claude subsequently
reviewed atomicity and reported blocking finding F-1; the correction and new
evidence are recorded separately below. Final independent re-review is pending;
this document does not claim approval of the corrected source. No acknowledgments are currently indicated:
protected tooling, defect allowances and lint baseline are unchanged. Hosted
container/security/kind acceptance is not executed in this local-only task.


## Completed local results (independent review pending)

- focused-race: 177 pass nodes, 0 skips; game 41.369s; all 20 new SIGKILL cases passed.
- normal: 5480 pass nodes, 2 skips; game 46.181s; all 20 new SIGKILL cases passed.
- race: 5480 pass nodes, 2 skips; game 488.647s; all 20 new SIGKILL cases passed.
- long-rerun: 5482 pass nodes, 0 skips; game 419.653s; all 20 new SIGKILL cases passed.
- All 18 scripted gates and separate full inventory reconciliation passed. Lint 1000 matched/0 new/0 stale. All 15 golden mutations passed; strict govulncheck reports zero affected vulnerabilities (unreachable module advisories remain reported). No timeout, baseline or gate changes.
- The first complete long run failed the unchanged opt-in `TestGB104RealEngineLongTurnKeepsHealthyClient` assertion about collected logs (game package passed). That test uses a one-second delay and an immediate asynchronously collected log check. The original failure lacks diagnostic logs to distinguish admission delay from log-collection timing; no exact root cause or test fix is claimed. Three isolated repetitions with a logging-only overlay retaining the assertion passed; the fresh unchanged full long run passed. All failed/interrupted output is preserved.
- Standalone ShellCheck was unavailable; actionlint/workflow policy/helper tests passed. Hosted Docker/Trivy/Syft/kind/CI were not run or dispatched.
- Local results do not establish hosted timing reliability. Original race game 488.647s left 111.353s below the unchanged 600s limit while other gates also ran.
- Independent review is pending. No staging, commit, push or live operation occurred. Complete source/output hashes and initial failures are retained in the validation ledger and external evidence.

## Claude F-1: advisory orders caused global construction deferral

The owner supplied Claude's independent real-dispatch reproduction: cruiser #79
protects absent #80, then `protect 0` clears only the On flag; `Protect.Ship=80`
persists. The original implementation reused `enrollmentShipTx` and rejected
the next append ID indefinitely, including after segment boundaries, whereas
the legacy build produces #80. Claude approved atomicity but required this
correction. That review is not final acceptance of the revised implementation.

Verified semantics: `commands_order.go`'s protect branch and C `source/order.c`
leave the old target when protection is disabled. Navigation to an existing
ship records `DestShipNo`; changing destination scope does not clear it.
`turn.go`'s orbital-chain reconstruction uses that field as a parent only for
`LevelShip`. Thus simply ignoring all `DestShipNo` references would be unsafe.

New real-dispatch regressions exercise both owners, append and free-ID reuse,
active/cleared protection, active/cleared navigation, immediately and after
three segment invocations (the final invocation may be skipped by the existing
segments-per-update limit). Navigation requires a real live target; creation
and retirement of that target are explicitly fixture setup, not a claimed
real-command destruction reproduction. Orders and final `build F 0,0` all use
actual actor dispatch. All 32 cases failed for construction deferral against
the pre-fix implementation and passed after correction. Full actor/store
agreement and preservation of the advisory ship are asserted.

Direct storage tests additionally cover Target/aim/transporter advisory fields,
retained landed destination fields, unchanged enrollment rejection, structural
conflicts before admission and during transaction admission, malformed records,
and late advisory mutations of a participating immutable witness. They exercise
both append and reuse. No existing failure assertion, baseline or gate is weakened.

Historical evidence remains unchanged at the original path above. New source,
commands, failures, outputs and the complete review patch are under
`/tmp/gb-planet-factory-build-f1-20261119T005000Z/`. The initial new storage-test
compile failure used an incorrect private placement-prefix constant; it was
corrected to the existing constant and the failed output is retained. Final
revision results and source hashes are in
`evidence/cr17c-planet-factory-build-f1-validation.json` and that directory's
validation ledger. Final Claude re-review remains required.

## F-1 corrected-source local results (Claude re-review pending)

- focused-race: 271 pass nodes, 0 skips; game 60.460s; all 20 construction SIGKILL cases passed.
- normal: 5574 pass nodes, 2 skips; game 46.904s; all 20 construction SIGKILL cases passed.
- race: 5574 pass nodes, 2 skips; game 519.367s; all 20 construction SIGKILL cases passed.
- New advisory matrix: 32 real-dispatch cases covering both players, append/reuse, protection and navigation, before/after segment invocations. Navigation targets are created/retired by explicitly labeled fixture setup; the orders and construction use real dispatch. All 32 failed before the correction.
- Direct storage tests cover eight advisory families for append/reuse, eight structural reference families both at admission and as late writers, malformed records, unchanged enrollment rejection, and complete witness CAS even when only an advisory field changes. Existing allocation concurrency, free-list ABA, rollback and uncertainty tests remain.
- Causal negative control: restoring only the original factory allocation helper with a read-only Go overlay compiles and fails all 32 advisory cases for the intended deferral assertion. No intentional defect remains in the worktree.
- All 18 unchanged gates plus inventory reconciliation passed on the corrected source (final build/format/vet/lint rechecks recorded after the fixture-only lint fix). Lint: 1000 matched, 0 new, 0 stale; all 15 golden mutations passed; strict govulncheck passed. Initial compile/lint failures and the superseded interrupted runner are retained. No baseline, policy, timeout or assertion changes.
- Full race game runtime 519.367s, leaving 80.633s below the unchanged 600-second limit. This local measurement does not prove hosted timing reliability.
- Full opt-in long suite was not repeated for this allocation-only revision; its prior pass and unresolved historical WebSocket timing failure remain historical evidence, not fresh validation. Standalone ShellCheck and hosted container/CI checks remain unexecuted locally.
- New evidence: `/tmp/gb-planet-factory-build-f1-20261119T005000Z/`, including complete updated patch, F-1-only delta, source/output hashes, commands, statuses, retained failures and SHA256SUMS. Repository ledger: `docs/testing/evidence/cr17c-planet-factory-build-f1-validation.json`.
- No staging, commit, push or live operation. Claude final re-review is pending; no acknowledgment label is indicated because protected tooling, lint baseline and defect allowances are unchanged.
