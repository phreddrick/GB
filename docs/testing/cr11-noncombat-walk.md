# CR-11 / #50: local atomic noncombat AFV walk

Local implementation for review only. Base `93f613c33459dd433ae406d9cd7149c83ca3b4d6`;
branch `feature/cr11-noncombat-walk`; worktree `/tmp/GB-cr11-noncombat-walk`.
Uncommitted/unpushed; no PR, issue change, deployment or live operation authorized.
CR-11/#50 and broader CR-17 remain OPEN.

## Reproduction

The preserved read-only audit is
`/tmp/gb-cr11-authoritative-ship-audit-20261115T234506Z/ASSESSMENT.md`.
Its source/tree hashes and evidence remain untouched. A permanent real actor
dispatch regression, TestWalkDurableShipAndAP, also failed on this work unit's
fresh base before production changes: walk #77 6 moved the actor from0,0 to1,0,
fuel10→9, mass100→99.95, AP50→49; stored ship stayed0,0/fuel10/mass100 and AP50.
The engine remained SERVING. The audit separately proved that the next scheduled
segment or orderly shutdown reload discarded the ship change while retaining the
AP debit. Original C source/move.c ends this path with putship then deductAPs.
Pre-fix log and execution status are preserved in the validation directory and
a portable compressed copy accompanies this record.

## Exact dispatch boundary and retained rules

The existing cmdWalk now first calls handleNoncombatWalk. All legacy code after
that call is byte-identical to the base. Read-only walk admission defers valid
actor/store differences under the owner-approved policy below. Parsing still accepts one explicit bare or # ship number
(including the existing plus/leading-zero forms), uses only the first direction
byte and ignores subsequent arguments, as original C/current Go do. Direction
aliases, cylindrical X wrap, pole refusal, and one-column steps that consume fuel
and AP despite ending at the same coordinate are preserved. No artificial
planet-directory or star-governor permission rule is introduced: walk uses the
selected ship's actual planet/star. Inactive-but-living AFVs are not newly banned.

| Variant | Writer/result |
|---|---|
| Properly planet-landed living AFV, valid caller/governor, no mechanical combat, neutral/own/mutually allied destination | New closed transaction |
| Hostile/nonmutually allied sector, or actual legacy armed nonallied AFV defense predicate at destination | Classified privately first; unchanged legacy walk, including its remaining CR-11/17 limitations |
| Non-AFV or uncrewed/nonlanded ship | Existing legacy validation/behavior |
| Invalid direction, terrain, fuel/AP or inactive caller governor on staged path | Refuse without write or AP charge |
| Missing/dead/overflowed/malformed selected ID | Safe refusal; no uint32 aliasing |
| Map-key/record-number disagreement, malformed/missing/cyclic orbit/planet/race context, nonfinite/negative mass/fuel or invalid result | HALT before any proposed mutation/persistence |

Two safety corrections are explicit: overflowing numeric IDs cannot truncate to a
different ship, and a dead selected ship cannot spend AP through the old no-op
tail. Neither authorizes operation of another player's/governor's ship.
Neutral walking does not colonize, occupy or change sector ownership.
Fuel cost remains1; mass decreases by cfg.MassFuel; star AP cost remains1.
The noncombat path consumes no random values.

Mechanical classification matches current cmdWalk/retalStrength, including the
fact that Active/On do not suppress AFV retaliation, and reciprocal alliance is
required. The full planet-root chain is captured before classification. The new
database operation rejects combat inputs independently. Combat/alternate-scope
variants never enter its writer. They have NOT been made atomic or gameplay-fixed.

## Transaction, witnesses and actor ownership

commandrules.PlanWalk is a pure bounded calculation. db.NoncombatWalk recomputes
and compares the complete permitted proposal before opening one bbolt Update.

| Record | Participation |
|---|---|
| Selected AFV | Write: LandX, LandY, Fuel, Mass only |
| Actual source star | Write: only caller AP minus1 |
| Complete actual planet, including all sectors, stocks, ownership/accounting and root | Read-only full preimage |
| Caller race/governor and every root-ship owner/target-sector owner race | Read-only full preimages, including alliance/terrain preferences |
| Every ship in complete planet root chain | Read-only full preimages, except selected ship; numbers, actual scope, membership, cycle/duplicate/missing checks; dead siblings retained as immutable witnesses |
| Current caller directory | Actor-local immutable workspace witness; no new directory restriction or persisted record |
| Durable maintenance and CR-18 write fence | Checked at admission/inside transaction as established by prior closed command patterns |

The complete canonical serialized record checks occur INSIDE the transaction,
after preparation; an intervening external writer is detected there. Only ship
then star are written, checking every Put result. Planet/root/predecessor, races,
allocators, bucket sequences, SData/universe AP, news/telegrams and commit/schedule
metadata are not rewritten. No new schema/bucket/commit kind/protocol is introduced.

Existing commandWorkspace deeply owns ship/special/planet copies, and captures
stable actor objects and contents. Before storage and again after confirmed
persistence, owns() checks all captured identities/values and directory context.
Two noncallback assignments install the ship and star into their existing actor
addresses. No callback/fallible operation occurs between them. Global ship reload
and unrelated orders/carrier landing/build code remain unchanged.

Original walk has no caller success text. The same private immutable other-player
movement message is released via registry-only notify AFTER commit/install, with
no new name-size limit. No telegram/news writer or aggregate combat counter is
added. Best-effort delivery is not a durable outbox or exactly-once guarantee.

## Failure policy

The operation shares commandMu, CommandOutcome/CommandError, write fencing,
maintenance refusal, command uncertainty taint and diagnostic-only readback.
Known transaction aborts/panics roll back all records and install nothing.
Otherwise valid actor/store differences at read-only admission defer with a
retry-after-next-persisted-turn hint, without writes, successful effects or HALT.
Invalid actor structures, incompatible/malformed stored records, unexplained
missing context, read failures and in-transaction preimage failures conservatively HALT and prohibit stale turn/shutdown flushes. Committed reporting faults keep the committed/installable state and HALT.
Uncertainty installs no unverified proposal, taints the handle for closed command
admission and HALTs; it is never automatically retried/reopened/reconciled.
Readback is diagnostic, not evidence of fsync durability.

Installation identity failure or panic before installation leaves actor proposals
uninstalled; postinstallation/delivery panic keeps both records installed.
HALTED shutdown performs no stale flush. This does not claim every raw legacy
Store.Put* method independently enforces the uncertainty taint; the actor HALT and
startup/closed-command guards are the existing policy, not a new global writer
architecture.

## Tests and acceptance evidence

Permanent tests include real dispatch; direction/numeric compatibility and
alliance/defense rules; complete readonly world preservation; low fuel/AP and
authority refusals; stale ship/star/planet/race/orbit and late external writers;
errors/panics before/inside/after transaction; diagnostic-readback failure and
uncertainty; actor identity/install/delivery faults; queued cancellation/draining,
concurrent commands, quiesced mode; scheduled segment/update and direct
shutdown/restart. Update AP income uses randomized rounding: the test asserts the
exact49 debit before income, then actor/store equality and nondecreasing post-update AP, rather
than inventing one fixed final income value.

Nine real subprocess SIGKILL boundaries: walk.before, check:1, validated, write:1,
write:2, before-commit, after-commit, preinstall, installed. Tests verify an actual
SIGKILL exit, reopen disposable bbolt and compare every logical record and bucket
sequence with the complete old or independently expected new ship/AP world.
Restart and segment/update retain that result. This is process-crash coverage,
not physical disk/power-loss/fsync fault coverage.

The original audit's order-write failure and carrier-landing defects remain
outside this work unit and are not accidentally repaired. A legacy mechanical
case is explicitly tested to retain its old in-memory-only behavior, preventing
a false claim that combat was migrated. Existing CR-07/17A/B/C/18/43 regressions
remain in the unchanged full suites; no existing crash test is removed.

## Validation history and review limits

Actual commands, exit statuses, node counts, runtimes and SHA-256 values are in
the validation ledger; raw logs are under `/tmp/gb-cr11-walk-validation/`.
Go1.27.4 with GOTOOLCHAIN=local; the default600-second package timeout is unchanged.

Preserved initial failures: the deliberate original regression; a new DB test's
incorrect assumption that all raw legacy PutShip calls honor the command taint;
one unused test import; a new test's incorrect fixed64 AP-income assumption;
and two gosec conversion findings. Tests were corrected to assert the existing
closed-command/actor guards and actual randomized update policy. Conversion
guards/fixture expectations were made explicit without adding suppressions or
changing allowances. Original output is not rewritten as a fresh pass.

Independent review and hosted CI are NOT performed or claimed for this local-only
handoff. Docker/Trivy/Syft/kind are not run on igor. No lint baseline, golden
allowance, inventory or protected tooling change is intended; acknowledgment-label
requirements are determined from actual final gate results. No labels are applied.
Remaining general authoritative-ship unification, combat walking, navigation,
construction/allocators, generic staging, dedup/outbox and CR-17D acceptance remain
separately scoped work.



## Final local results (2026-11-16 UTC)

| Check | Actual result |
|---|---|
| Complete normal | 5240 passing nodes, 2 pre-existing opt-in skips; game46.836s |
| Complete ordinary race | 5240 passing nodes, 2 pre-existing skips; game407.118s/600s |
| Complete long race | 5242 passing nodes, zero skips/failures; game436.962s/600s |
| New walk coverage | 153 passing nodes across rules, database and actual actor dispatch in each final complete run |
| Real process crashes | 9 new SIGKILL boundaries pass in every final run; all existing suites retained |
| C/golden | C fixture check and all15 mutations pass; PASS1248/XFAIL143/XPASS0/FAIL0 |
| Inventory | completion records reconcile; existing explicit untested inventory item unchanged |
| Lint/growth | 1000 matched/zero new/stale; allowances and protected tooling unchanged |
| Other local gates | build/gofmt/vet, gate self-tests, workflow policy/helper tests, pinned actionlint, offline manifests, strict govulncheck, module verify/tidy-diff, diff checks pass |
| Explicit gaps | ShellCheck unavailable locally; hosted container/Syft/Trivy/kind and independent review not run |

The long-race and normal mutation runs overlapped on this16-CPU host; measured
long-game runtime leaves27.2% of the unchanged600s package limit. Ordinary race
leaves32.1%. These local measurements do not prove future hosted timing. No Go
source/test file changed during the final complete runs. The omitted-AP overlay
causal control compiled and failed BOTH the real-handler and database complete-
record assertions for the intended reason; the intentional defect remains solely
in the external evidence directory.

See [source/log hashes and execution ledger](evidence/cr11-walk-validation.json).
No maintainer acknowledgment label is indicated by this diff; none applied.
The first implementation was locally validated; the subsequent Claude review
requested corrections recorded below. Neither run is whole CR-11/CR-17 acceptance.


## Initial Claude review corrections — 2026-11-16 UTC (historical)

This section preserves the original validation history above and the original
`cr11-walk-validation.json` ledger unchanged. The owner relayed five independent
Claude review findings; this correction round is for Claude re-review, **not a
new independent approval**. New execution evidence is retained privately at
`/tmp/gb-cr11-walk-review-fixes-20261116T013059Z/`.

The provenance-tracking policy in item3 below was subsequently superseded by
the owner decision recorded in the next section. Its original evidence and
acceptance ledger remain preserved unchanged.

1. **Destination fields:** `WhatDest`, `DestStar`, `DestPNum` and `DestShipNo`
   are order/destination fields, not authoritative planet location for an AFV
   with `Docked=true` and `WhatOrbits=LevelPlan`. Classification and the closed
   rules now ignore them, preserve them unchanged and still validate actual
   planet/star/coordinates and complete root membership. Real actor dispatch
   launches an AFV from a landed carrier, retains `DestShipNo=70`, then durably
   walks. Four destination-level cases with stale destination coordinates and
   ship ID also commit correctly.

   **Different disposition for direct land-then-walk:** the actual AFV ability
   table has `AbilCanLand=0`. Real `land #77 0,0` from orbit refuses with
   `This ship is not equipped to land.`; the subsequent `walk` refuses because
   it is not landed. A permanent dispatch regression preserves both refusals.
   We do not change abilities or fabricate a successful direct AFV landing.
   The separate known carrier-landing graph defect remains excluded. Positive
   carrier-launch-to-surface and stale-destination cases cover valid landed
   states without weakening actual membership checks.

2. **Dead linked witnesses:** a walk-specific complete planet-root collector
   accepts dead ships awaiting turn cleanup without changing telescope or other
   command contracts. Full preimages remain mandatory. Dead ships cannot
   retaliate. Landed and orbiting dead foreign armed AFVs are accepted and never
   rewritten; missing, cyclic and wrong-orbit witnesses remain HALT cases. A
   direct DB contract regression also checks a dead foreign armed witness.

3. **Known legacy pending state:** like enrollment's pending-turn admission,
   ordinary legitimate actor-owned changes must defer rather than HALT. Walk
   uses a deliberately stricter provenance test to preserve its unexplained
   staleness safeguards: the existing `deductAPs` observes a full agreeing star
   preimage (or an already proven pending pair), then records the resulting
   private actor copy. The legacy combat walk similarly observes complete root
   ship preimages before its existing gameplay and records actor-only results
   only if the stored record stayed unchanged. These actor-owned transient maps
   neither persist nor repair legacy operations. They authorize no writes.

   Admission compares **every** participating stored record before deferring.
   Only exact known stored/actor pairs receive `Walk deferred: pending legacy
   changes; retry after the next persisted turn.` No AP/cargo/location mutation,
   success notification, store write or HALT follows a deferral. Equality at a
   normal persisted boundary retires that record's pending entry. External or
   unexplained actor changes, errors, corruption and in-transaction stale
   preimages still HALT. The bbolt CAS, fence, maintenance and uncertainty guard
   remain intact; there is no automatic retry. Tests use real `tech 1` then
   walk, a legacy mechanical-classified walk followed by a peaceful walk,
   conflicting actor/store changes even when AP is pending, and a writer after
   admission. An actual segment permits a later successful durable retry.
   Legacy combat's original ship-reload loss itself remains unfixed and is
   stated explicitly in its regression.

4. **Legacy effects:** pole refusal is `Illegal coordinates x,y.`; invalid
   direction remains `Illegal move.` AP shortage goes through registry-only
   `notify`, matching `enoughAP`, with no responder fallback. Movement recipients
   are bounded by `len(s.races)`, matching legacy walk, including governor
   validation only for those possible recipients. Tests check the exact text,
   route and recipient bound. Walk still has no caller success text.

5. **Crash stderr:** child stderr is drained into a synchronized 1 MiB bounded
   buffer; overflow or a `DATA RACE` report fails the crash test. Early child
   exits/timeouts are reaped and include captured diagnostics. A permanent test
   covers retained race text, byte accounting and overflow. The stockpile crash
   harness currently discards stderr; the correction follows the existing
   cargo/population/assault capture-and-race-rejection pattern, with an added
   bound, rather than claiming stockpile already provided that protection.

The correction adds only narrow legacy provenance observations to `State`,
`deductAPs` and the old walk tail; no legacy writer is repaired, no global reload
is changed, and no protected tooling/baseline is edited. The final correction
ledger, full patch and test summaries are recorded after all acceptance runs.


### Correction-round acceptance results

Go1.27.4, `GOTOOLCHAIN=local`, final Go source unchanged throughout all final
runs. Each complete suite retained all existing regressions and all nine walk
SIGKILL boundaries:

| Run | Passing test nodes | Existing opt-in skips | Game package seconds |
|---|---:|---:|---:|
| normal | 5269 | 2 | 43.459 |
| race | 5269 | 2 | 433.662 |
| long-race | 5271 | 0 | 430.323 |
| focused-final | 182 | 0 | 24.566 |

All 15 golden mutations passed (PASS1248/XFAIL143/XPASS0/FAIL0); full normal-run
inventory records reconciled. Build/gofmt/vet, C fixture verification, lint
(1000 matched, zero new/stale), growth, gate helper/self-tests, workflow checks,
pinned actionlint, offline manifests, strict govulncheck, module verification,
`go mod tidy -diff` and diff checks passed. ShellCheck is unavailable, including
its actionlint integration. No hosted container checks or CI were executed.
No prior test, assertion, timeout, scanner policy, allowance or protected gate
was weakened. No new acknowledgment label is indicated.

Unsuccessful correction-development runs remain preserved: a nonexistent
`model.ScopeLevel` test type caused compilation failure; new enrollment fixture
account IDs were not allowlisted; landing setup initially overloaded the AFV
before revealing its actual no-planet-landing capability; an intended stale-AP
case accidentally made the store equal the pending actor balance; and a new
registry assertion initially expected the wrong fake-registry prefix. Corrected
tests use authorized fixture accounts, the real capability boundary, an actually
different stored AP balance, and the established registry prefix. These are
recorded separately from the successful final-source acceptance executions.

See [the supplemental correction ledger](evidence/cr11-walk-review-fixes-validation.json).
The private evidence directory contains every failed/successful execution, full
updated `complete.patch`, final file hashes, `ASSESSMENT.md` and `SHA256SUMS` for
Claude re-review. The original pre-fix log, ledger, patch and validation evidence
remain unchanged. No fresh independent implementation approval is claimed.


## Owner-approved admission revision — 2026-11-16 UTC (historical; extended below)

The owner accepted Claude's re-review recommendation: an otherwise valid
noncombat AFV walk **defers on any actor/store record disagreement found at
read-only admission**, without guessing which writer created the difference.
The operation returns `Walk deferred: actor and stored records differ; retry
after the next persisted turn.` It does not enter the transaction, install
proposals, debit AP/fuel, notify movement or HALT.

This decision supersedes the earlier provenance-only pending policy. All
per-writer maps, `State` fields, `deductAPs` reads and legacy-walk observers have
been removed. `build.go` and `state.go` are again byte-identical to the base;
`commands_move.go` contains only the three-line bounded walk dispatch hook.
No legacy writer, global ship reload or turn authority policy changes.

The admission comparison still examines every required preimage. Missing or
malformed record identities, invalid selected mass/fuel, invalid planet shape,
missing races, cyclic/missing/wrong-scope orbital links and inconsistent living
ship membership remain safety failures. It validates the persisted root as well
as the actor root; changed stored locations are checked through their actual
star/planet/universe/carrier membership, including parent-cycle detection. Dead
linked witnesses remain permitted, and dead unlinked stored records may await
cleanup. These read-only checks do not add records to the writable set.

If admission agrees, complete preimages are still checked independently INSIDE
the bbolt transaction. A writer between admission and transaction is a stale
transaction failure and HALTs; there is no fallback to deferral or legacy retry.
Write fence, durable maintenance, command mutex, store uncertainty guard,
confirmed-commit installation, buffered notifications, and HALTED shutdown's
no-stale-flush behavior are unchanged. Deferral does not reconcile records or
promise that the existing normal persisted boundary preserves arbitrary
out-of-band edits; it preserves existing authority/reload behavior and allows a
manual retry only when records agree and the command is still eligible.

New and revised regressions cover:

- Real `tech 1` AP spending followed by deferral, no writes or HALT, then an
  explicitly requested isolated movement segment and successful manual retry.
- Real `name #77 ...` and `toggle color` dispatch with deliberately **known
  write-fenced legacy saves**. Those existing handlers ignore the confirmed
  refusal and leave valid actor/store ship or race differences. Tests label the
  injection explicitly; they do not claim an ordinary successful save diverges.
  Walk then defers, with zero transaction admissions or movement notifications,
  and a later agreeing persisted boundary permits a successful retry.
- Deterministic actor/stored race probes, stored ship/planet/AP differences and
  the previously retained legacy mechanical-classification walk. Probes are not
  described as natural gameplay reproductions; the mechanical fixture has no
  volleys and does not claim combat persistence is fixed.
- Stored missing/cyclic/wrong-orbit roots, malformed planet shape, missing race
  or carrier, and negative selected fuel/mass still HALT without command writes.
- Real late writers for ship, star, race and planet after admission. Every
  command HALTs on transactional CAS failure, retains exactly the external
  writer's complete world image and installs no proposal.

A read-only Go overlay restoring strict race-divergence HALT compiled and failed
both the real legacy-toggle and deterministic actor-race regressions at their
intended no-HALT assertions. Intentional defects exist only in the new private
evidence directory; no production source was mutated for the control.

Original audit and correction evidence remain untouched. New execution logs,
source hashes, validation ledger and complete patch for Claude re-review are
under `/tmp/gb-cr11-walk-admission-review-20261116T021932Z/`. Fresh results follow
below after acceptance completes. No fresh independent approval is claimed.


### Admission-revision final validation

Final Go1.27.4 source remained unchanged throughout the final runs;
`GOTOOLCHAIN=local`, default600-second package timeout unchanged.

| Run | Passing nodes | Existing opt-in skips | Game seconds |
|---|---:|---:|---:|
| normal | 5290 | 2 | 48.065 |
| race | 5290 | 2 | 447.944 |
| long-race | 5292 | 0 | 405.601 |
| focused-final | 203 | 0 | 32.201 |

All nine walk real SIGKILL cases passed in each run. All15 golden mutations,
normal-run inventory reconciliation, build/gofmt/vet, C fixtures, lint(1000
matched/0new/0stale), growth, gate self-tests, workflow policy/helper checks,
pinned actionlint, offline manifests, strict govulncheck, module verification,
tidy-diff and diff checks passed. ShellCheck is unavailable, including actionlint
integration; hosted container/CI checks and fresh independent review are unrun.
No test, timeout, assertion, baseline, security policy or gate was weakened.

Both focused executions passed. The strict-race overlay compiled and failed its
intended assertions; it is preserved as an expected negative control. An initial
setup command used the prior evidence cwd for a relative source read and raised
FileNotFoundError before source mutation; the corrected setup and all actual
executions are recorded, without altering older evidence.

[The final admission ledger](evidence/cr11-walk-admission-validation.json)
contains commands, exits, runtimes, source and log hashes. The private evidence
folder contains the full current `complete.patch`, per-file hashes, assessment
and `SHA256SUMS` for Claude re-review. Per-writer tracking is absent; `build.go`
and `state.go` match base exactly. No fresh independent approval, full CR-11
acceptance, commit, staging, push, PR, deployment or live operation is claimed.


## Partial legacy-save admission extension — 2026-11-17 UTC

The owner authorized this extension after Claude reproduced `build c 0,0` then
`walk #77 6`. Legacy build persists a new child but leaves the planet's new head
only in actor memory. The earlier membership validator HALTed on that valid
pending topology. Real ship-scope cargo construction and an actor-only linked
ship probe reproduced the same refusal. All three permanent dispatch regressions
failed on the preceding source for the intended HALT-versus-deferral assertion;
raw logs and the preceding admission source are preserved separately.

The bounded extension is read-only and uses no legacy-writer tracking:

- `GetShip` returning `nil,nil` means absent. It may defer only when the exact
  ID has a private preimage in the already validated complete actor chain, with
  compatible actual location and no contradictory ship-free-list marker. The
  immutable actor copy permits further structural checking only; the local
  deferral flag is already set. It is never installed, written or admitted as a
  missing bbolt preimage. No unknown missing link is filled in.
- A present, well-formed stored ship omitted from the stored root may similarly
  defer when its validated actor counterpart belongs to the same actual
  location. The stored root must still be readable and acyclic. The unlinked
  ship's tail is also traversed: cycles, unknown links or incompatible locations
  are safety failures, even though the actor has a valid version.
- Read/decode errors, null or wrong-identity records, missing races/unknown
  carriers, invalid actor links, malformed planet shape and invalid selected
  fuel/mass remain HALT cases. So does a free-list entry contradicting an active
  actor witness; no allocator state is changed.

The old `empty-root` stored-side HALT assertion is deliberately reclassified:
an empty or prefix-omitting stored head with a complete valid actor chain and
compatible records is exactly the pending-head situation, so new regressions
require deferral and manual retry after an agreeing persisted boundary. Invalid
**actor** roots still HALT. `DeleteShip`-based absence remains a negative test
because it records the ID as free; an unexplained missing stored link still
HALTs. A deterministic selected-record absence without deletion evidence now
requires deferral, while malformed JSON, JSON null, wrong identity, closed-store
reads, freed actor-only records and invalid actor-only links require HALT.

Deferral does not prove writer intent or certify a store as globally uncorrupted;
with no provenance tracking, a silent loss observationally identical to a partial
save cannot be distinguished. It refuses the command without writing either
version. It does not promise recovery, reconcile the world or broaden legacy
build persistence. Complete CAS remains strict inside bbolt: a record deleted
**after** admission is still a stale transaction/HALT, not a deferral. The
supplemental late-delete test-only overlay verifies that boundary against the
final production source.

Actual dispatch evidence uses disposable God-race/high-resource fixtures:
planet build creates persisted child78 with actor root78/stored root77;
ship-scope build creates persisted child80 with actor root80/stored root77.
Both defer without transaction admission, AP/fuel writes or movement success,
then a fixture movement-segment commit permits a successful manual walk retry.
The actor-only child88 probe is explicitly synthetic. Existing turn startup
reloads ships; a fixture-only delayed save completes that deliberately omitted
record before its segment. The test does **not** claim a never-saved ship
survives reload or add automatic repair. The absent-selected-record probe uses
an equally explicit fixture completion before retry.

Further safety investigation found that checking only the stored root could
hide a corrupt unlinked tail. Two new negative tests first reproduced that
incorrect deferral, then passed after unlinked-tail validation. Preliminary
suite/gate executions are preserved as historical; final-source acceptance is
recorded separately in the `acceptance/` evidence subdirectory; root-level and
`final/` executions remain historical. No final run reuses a
historical pass.

All prior evidence remains unchanged. New material is under
`/tmp/gb-cr11-walk-partial-save-review-20261117T043053Z/`, including Claude's
unchanged probe, pre-fix failures and source, supplemental overlay and final
validation. Only walk admission, its tests and implementation records change;
`build.go`, `state.go`, global reload, the typed transaction and its failure
policy remain unchanged. No independent approval is claimed before Claude's
re-review; broader CR-11, navigation and construction persistence remain open.


Validation-set hardening: reading an unlinked tail may cache additional complete
stored records. Admission now snapshots the validation participants before that
traversal; otherwise Go map insertion during range could change which records
were subsequently treated as primary actor witnesses. A deterministic extra-
stored-tail probe exercises four manual no-write deferrals. A test-only overlay
restoring the old mutable iteration reproduces the false HALT at the intended
assertion. No production source is altered by that control. Only `acceptance/`
executions against the fixed participant set establish the final-source pass;
prior `final/` and root-level logs are preserved as historical evidence.


Supplemental supplied-probe verification: the unchanged Claude probe passed all
four subcases under race (kill/morale, fire-effects probe, real build, actor-only
new ship). Each first walk deferred in SERVING mode; each post-segment retry
succeeded. A separate observational overlay confirmed that the never-saved88
record is absent from both actor and store after that segment (root1). Thus a
successful retry does not prove retention of an unsaved ship. The permanent
actor-only preservation probe deliberately completes its fixture save before
the boundary; broader construction/reload durability remains excluded.


### Partial-save extension final validation

Final Go1.27.4 source remained unchanged for all acceptance/ executions below;
`GOTOOLCHAIN=local`, default600-second package timeout unchanged. Preliminary
root-level and final/ executions remain historical, not reused as final passes.

| Final run | Passing nodes | Existing opt-in skips | Game seconds |
|---|---:|---:|---:|
| normal | 5307 | 2 | 41.666 |
| race | 5307 | 2 | 438.982 |
| long-race | 5309 | 0 | 459.901 |
| focused-final | 220 | 0 | 30.346 |

All nine walk real SIGKILL cases passed in each final run. All15 golden mutations,
normal-run inventory reconciliation, build/gofmt/vet, C fixtures, lint(1000
matched/0new/0stale), growth, gate self-tests, workflow policy/helper checks,
pinned actionlint, offline manifests, strict govulncheck, module verification,
tidy-diff and diff checks passed. ShellCheck is unavailable, including actionlint
integration; hosted container/CI checks and fresh independent review are unrun.
No test, timeout, assertion, allowance, scanner policy or gate was weakened.

The original three partial-save regressions and two unlinked-tail safety
regressions compiled and failed the intended assertions before their respective
corrections. All logs are retained. A test-only control restoring mutation during map iteration compiled and
reproduced false HALT on the extra valid stored-tail probe. The final-source
supplemental late-deletion race test passes through a clearly identified test-only overlay; production CAS
is unchanged and remains strict about records disappearing after admission.

[The partial-save ledger](evidence/cr11-walk-partial-save-validation.json)
contains exact commands, outcomes, timings, and source/log hashes. Private
`complete.patch`, per-file hashes, assessment and `SHA256SUMS` cover every intended
tracked/untracked file for Claude re-review. Earlier evidence remains unchanged.
No fresh independent approval, full CR-11 acceptance, staging, commit, push, PR,
deployment or live operation is claimed.
