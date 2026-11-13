# CR-17C hostile ship-to-planet assaults

Baseline 7a17a1bc7edf75091386f30944a529a0ccf48d8d. Implementation complete; local acceptance passed; final independent acceptance and hosted CI are recorded below/in the PR.

## Owner approval after Phase 0

The owner explicitly approved preserving Go survivor mass refunds: victory,
mutual destruction and unopposed occupation remove all departing mass; repulse
removes only casualty mass, with returning complement and mass credited together.
Absorption affects sector population only. Finite/nonnegative mass is required;
no silent clamp or invented carrier exemption. Original C duplicate survivor
subtraction is rejected. Zero AP is explicitly approved.

The Phase0 record, archive, validation ledger and both independent reviews remain
unchanged historical evidence, not implementation acceptance. Their recorded
file identities and all archive SHA256SUMS were reverified before editing.

## Exact dispatch and gameplay scope

The existing #41A parser admits one explicit uint32 ship identity (optional `#`),
exact c/m commodity and optional signed-int32 decimal amount. The new classifier
then recognizes foreign-owned landing sectors before `runPopulation`; classified
requests never fall back to `doLoad`, even when ship authority, quantity or
context fails. Nonhostile #41A transfers and all other commodities retain their
previous behavior. Malformed/mixed/wildcard selectors remain refused by the
existing parser. There is no new protocol or network surface.

Supported: `unload #N c|m [amount]` with a legally positive departing force from
one live, active, docked, authorized selected ship, consistently planet-landed.
C omitted/explicit-zero defaults expand to the full selected complement ONLY
when the original pre-default signed interval permits it and the resolved force
is positive. Negative unload, hostile load/reverse, empty defaults, malformed or
overflowing amounts are refused without draws, mutations or success output.
C bound-before-default and the pre-default VN sign check are preserved; no
invented blanket ship-type, habitability, slavery or recipient permission rule
is added. The selected governor's authority is checked, not movement-only star
assignment. Scope coordinates must match the actual landing context.

Deferred/refused: mechanical AFVs/counterattacks, active targeted transporters,
ship recipients as hostile assaults, multiple ships, wildcard/mixed selection,
invalid orbital repairs and hostile reverse loading. Carrier/peer noncombat
transfers remain in #41A. A parent-carried ship cannot masquerade as a landed
planet-root assault; there is no new carrier mass exception. Friendly/neutral
sector movement and PR78 ground assaults remain separate contracts.

## Private rules, outcome and accounting

`commandrules.ShipAssaultInput` is distinct from sector-origin `AssaultInput`.
Selected ship Armor and Damage provide C's attacker multipliers
`(Armor+1)*(2-Damage/100)`; defender terrain/affinity, Fighters, civilian/military
factor, original morale constant, fixed pre-casualty scale, integer division and
stochastic rounding/inclusive integer draw order are preserved. Empty defense
uses approved unopposed occupation with no fabricated casualties or zero divide.

Outcome is determined before absorption: living attackers and eliminated
defenders capture; surviving defenders repulse and return attacking survivors;
mutual loss neutralizes ownership without winner morale or resurrection.
Absorption samples the original full body pool and saturates the absorbed
civilian quantity at remaining uint16 capacity. It does not change ship mass.
C's exact ship-helper extinction-learning test uses destination residents plus
surviving party, not onboard passengers or movement-origin residents.
Morale/Points and translation updates use checked widths and preserve unrelated
race fields. C Points indexes loser.Playernum; ordinary player IDs stop at63.

Complete sector aggregates are validated before preparation; inconsistent old
population/troop/ownership/mobilization totals are rejected, never globally
repaired. Final global and per-owner totals derive from the final embedded
sectors, with actual ownership transitions. Sector.Race, terrain, resources,
crystals, stockpiles and unrelated sectors are unchanged. Civilian/troop fields
are promoted before arithmetic. Ship complement/mass reflects actual departing,
returning and killed units with finite, nonnegative checked results.

## Complete transaction and witness inventory

| Record | Participation | Validation/installation |
| --- | --- | --- |
| Selected ship | Writable Popn **or** Troops and Mass only | Exact Number/map key, owner/governor/alive/active/docked, complete immutable preimage, independent rules equality; deep private special pointers; stable actor ship address |
| Complete planet | Writable landing sector residents/Owner; global and both owners population/troop/count/mobilization totals | Exact identity/dimensions/landing coordinate, coherent preimage totals, complete persisted JSON comparison; existing sector slice/address retained |
| Attacking and defending races | Writable defined morale, Points and Translate only | Exact IDs, caller active governor, full persisted preimages and independently recomputed proposed records; stable actor addresses |
| Root star/AP | Read-only | Exact star/planet membership, complete preimage including AP and defender governor; zero AP, never rewritten |
| Complete planet orbital root chain | Read-only except selected ship | Every unique living ship identity/location/root membership and full record checked; missing/cyclic/duplicate/dead context rejected; no undock repair |
| Third-party root owners/alliance races | Read-only | Exact unique identities and complete preimages; mechanical exclusion cannot assume absent/inactive witnesses |
| Actor directory | Read-only trusted actor scope | Captured consistently; ownership checked again before transaction/installation; no invented persistent directory record |
| Combat-news record | Required writable existing stream entry | Derived exact bounded ship-origin/outcome/casualty/absorption text, validated existing stream keys, checked next sequence, same transaction |
| Defender telegram/inbox sequence | Required writable existing entry/allocation | Authoritative previous owner/star governor, checked sequence/exhaustion/occupied keys, same transaction |
| Commit/schedule/maintenance/universe metadata, allocators, unrelated world | Unchanged | Complete snapshot assertions; maintenance/fence checked without changing commit kinds or sequence |

The closed `db.ShipGroundAssault` recomputes the exact permitted proposal before
transaction admission and again after all full preimages match inside bbolt.
Writes are selected ship, planet, attacker race, defender race, followed by
mandatory existing news and telegram allocation. The internal checked
`preparedAssault`/`writeAssaultEffects` mechanism is shared with PR78; its existing
`assault.news`/`assault.telegram` test boundaries are intentionally shared. New
world/admission boundaries use `ship-assault.*`. No generic callback or second
writer is exposed, and no schema/bucket/commit-kind/outbox is added.

## Randomness, installation and effects

Production uses the accepted isolated crypto draw source, uniform bounded integer
rejection sampling and uniform fractions. At most seven typed draws are staged
privately. Storage replays exact kind/bound/value/order/exhaustion without a new
draw. Static authority/accounting/mechanical validation precedes preparation;
preimage failure and uncertainty never trigger retries or rerolls. The turn/global
RNG and persistent RNG state are untouched.

The established command mutex, write fence/refusal accounting, strict durable
maintenance validation and shared uncertain-handle guard are preserved. Aborted
transactions roll back every participant and message allocation; no proposal is
installed. Confirmed commits install the complete proposal only after a combined
actor ownership/preimage check. Plain assignments preserve ship/race/planet and
sector identities; no callbacks interleave the installation. Staged player output
and live inbox hints follow installation. Live hints never call warn/pushTelegram
again: the telegram is already durable. Unavailable live delivery is best-effort;
response errors/panics or postcommit installation errors remain committed and
HALT as appropriate. Uncertainty installs nothing, retains diagnostic readback
and the handle guard, HALTs and suppresses stale turn/shutdown flushing.

No migrated assault increments volatile groundAssaults, avoiding a duplicate
later aggregate. No durable command ID, deduplication, outbox, automatic retry or
exactly-once command/delivery guarantee is implemented. A lost response requires
operator diagnosis; it is not evidence of rollback or permission to retry.

## Permanent tests and causal evidence

New files: `commandrules/ship_assault_test.go`, `db/ship_assault_test.go`,
`game/ship_assault_test.go`, `game/ship_assault_crash_test.go`.
Tests execute the real actor dispatcher and direct closed storage API.

- Exact independent C-derived strength/casualty oracles cover200/200 partial
  combat and Armor4/Damage50 strength375, including exact survivors, mass,
  morale, Points, translation and population/ownership/mob totals. Independent
  reviewer supplied those original oracles; permanent tests retain them.
- Civilian/military victory, no-loss and partial repulse, attacker total loss,
  mutual destruction, empty occupation, both absorption factions, full body
  pool65536 capacity saturation, defaults and AP0. The historical C no-loss
  example permanently asserts crew100 and mass210 after return.
- Whole logical database records and bucket sequences are compared against
  expected complete proposals; all unrelated records and raw metadata are
  checked. Actor/store equality and stable actor/sector addresses are asserted.
- Authority, signs, selectors, broken identities/chains, mechanical peers,
  targeted transporters, invalid numeric/accounting state and stale preimages
  reject without combat text, cargo/mass loss or record mutation.
- Error/panic tests cross admission, checks, validation, all four world writes,
  news, telegram, beforecommit, uncertain reporting and confirmed postcommit;
  fence and maintenance refusal, failed readback, install identity changes and
  response/live delivery failures are covered. Halted shutdown cannot flush.
- Deterministic concurrent/queued cancellation, quiesce/drain/shutdown tests use
  actor barriers; there are no timing sleeps or shared mutable fixtures.
- 26 actual SIGKILL cases (13 boundaries × civilian/military) cover admission,
  preimage validation, validated transaction, each world write, news, telegram,
  precommit, confirmed commit, preinstallation and installed-before-output.
  Reopened records must equal the complete old or complete committed state,
  including messages/sequences/raw metadata. Both segment and full update then
  check actor/store agreement, ownership preservation and no report duplicates.
  All24 #41A and20 PR78 existing kills remain unchanged.

Six private-overlay negative controls compile and fail their intended assertions:
C duplicate survivor subtraction; independently committed first ship write;
incorrect capture Owner; omitted combat news; omitted defender telegram; and
actual legacy hostile fallback. Intentional defects never enter the repository.
A seventh independent reviewer control changes the ship strength formula and
fails the exact Armor/Damage oracle. These test logic, not just rejection of all
combat requests. Neither crash coverage nor inventory allowances were reduced.

Two historical tests formerly required all hostile unloads to refuse. Only their
newly authorized positive case is updated: it must execute and match the full
actor proposal, every durable record/message/sequence and zero volatile report
counter. All other prior rejection cases remain unchanged. This is an explicit
accepted behavior change, not a removed safety assertion.

## Historical failures and validation status

Preserve every initial execution in the implementation archive/ledger. Initial
DB test compilation lacked a test-only bbolt import; corrected. Focused v3 found
a message timestamp crossing a wall-clock second between independent proposal
and dispatch. New private ship fixtures now freeze only their own actor clock;
no global time/environment or shared fixture changes. Initial normal/race and
mutation runs exposed the historical PR78 blanket hostile-unload refusal;
its newly authorized case now has stronger full-state assertions. Initial lint
reported one test integer conversion and a copied conditional-switch finding;
both fixed in source with no baseline/suppression change. The first legacy
negative-control ledger used the wrong expected assertion text; the test did
fail for the correct actor-state disagreement, and the final control records
verify that actual failure without losing the first history.

Full final-source validation and independent revalidation results are appended
below when complete. Earlier Phase0/review or provisional passes are not final
acceptance. Hosted Docker/Syft/Trivy/kind remain hosted responsibilities only.

## Remaining boundaries

This is only nonmechanical hostile planet unloading. #41/#51/#38 remain open for
mechanical combat, hostile reverse variants and remaining command persistence.
CR-11/CR-13 turn behavior, CR-20, stockpile-transfer volatile AP and broader
navigation/allocator/outbox/deduplication work remain separate. Original C
retreat mass, unsafe mutual capture and incomplete aggregate accounting are
intentional owner-approved corrections, not unreported parity. No public
registration/authentication, live universe or deployment settings change.

## Review correction R3: complete body-mass consistency

A final source audit identified a necessary numeric contract gap: checking only
M>=departure debit could accept M300,crew200,m2,Q100, leaving mass100 for crew100
whose bodies alone weigh200. The independent reviewer reproduced that pre-fix
acceptance and a CR13-style narrow-sum case. This is an implementation defect
under the already approved reject-inconsistent-mass requirement, not a new
gameplay decision. C doship.c and Go doMass use owner race mass for all bodies.

The corrected validator promotes Popn+Troops before computing their complete
body-mass floor; the planner checks the resulting floor too. Every product and
result must be finite/nonnegative. A small four-times-sum-of-operand-ULPs bound
permits representational rounding/cancellation only; it neither clamps a mass
value nor admits a materially deficient preimage. The post-check includes the
original mass, original body mass and actual debit, so near-total fractional
unloading is not rejected merely because its tiny final value has smaller ULPs.
Permanent tests include deficient complete complements, a mixed population total
above65535, fractional .3/3/.1 no-loss retreat, near-total occupation and material
deficiency. CR13's old doMass narrow sum is not rewritten or silently repaired.

All provisional passes before R3 remain historical. Complete final-source
acceptance is rerun after this correction. The mutation rerun also preserved an
external-host failure: Go's temporary test module discovered the invalid
/tmp/.git placeholder and failed VCS status128. No VCS stamping, gate setting or
assertion is disabled: subsequent checks use an isolated ignored TMPDIR under
this worktree's test/ctool/obj/tmp, as the previous accepted PR78 runner did.
No other worktree or /tmp/.git is changed. The initial gate-selftest failure
was its clean-tree lint assertion seeing the two new test findings, not a
package timeout; corrected source passed the unchanged selftests afterward.

## Complete validation before final clock correction (historical)

Final executable source/test/gate identity (canonical JSON SHA-256 of440 file
identities): `d275d12879d5614492d3106f9c5b65c3bd6155a6f723a820a8164f31bf4bf0f0`.
The source-bound final runners verify the same identity before and after checks.
The long run used this same frozen corrected source; no production/test change
occurred during it. Full command/stdout/stderr identities are in the ledger.

| Fresh check | Actual result |
| --- | --- |
| Full normal suite + inventory (`inventory-check.sh -json`, internally `go test -count=1`) |4981 passed nodes,4770 passed leaves,2 existing opt-in skips,0 failures; game38.522s; inventory reconciled |
| Exact `go test -count=1 -race ./...` |PASS, game334.443s;265.557s/44.3% below unchanged600s limit |
| `GB_LONG_TESTS=1 go test -count=1 -race -json ./...` |4983 passed nodes,4772 leaves,0 skips/failures; game369.530s;230.470s/38.4% margin |
| Real SIGKILL cases in each complete normal/long run |26 new ship +24 population +20 ground =70; all passed |
| Six final-source causal controls |All six compiled and failed the intended assertions; source defects remain private overlays |
| Build/gofmt/vet; module verify/tidy diff |PASS; modules unchanged |
| Strict pinned govulncheck v1.9.0 |PASS with Go1.27.4; no suppression |
| Original C `make -C test/ctool check` and golden parity |PASS; original C unchanged; full suite includes all2292 defined race-cost fixture designs |
| Golden mutations |All15 expected behaviors passed, including final unmodified-tree reconciliation |
| Known-defect inventory |PASS; existing explicit UNTESTED finding retained, no allowances changed |
| Pinned lint gate |1000 baseline/report findings matched;0 new,0 stale; no baseline edits |
| Growth/acknowledgment gate |PASS with empty labels; allowances unchanged, no protected tooling changes |
| Gate self-tests |PASS, including deliberately broken fixture/linter/approval cases; no gate edits |
| actionlint, shellcheck, workflow policy/helper tests |PASS, unchanged workflows/security/pins |
| Offline manifest rendering/mutations/schema/safety |PASS; no cluster access |
| `git diff --check` / staged check |PASS |

Node totals include parent tests; leaf totals count actual deepest subtests.
The two normal skips are the existing production-timing opt-ins that passed in
the complete long run. Do not infer physical power-loss/fsync simulation or
hosted runtime reliability from local SIGKILL and timing results. Hosted Docker,
Trivy, Syft and kind are still required; none ran locally.

## Independent review and precise CR13 finding

Separate read-only review preserved source hashes, fresh focused race executions,
independently derived C expected values and all six intentional-defect controls.
Round1 required stronger independent oracles and corrected a copied comment;
both were fixed. Round2 confirmed the four-world-record transaction, mandatory
news/inbox allocation, authority, complete preimages, stable installation and
26 actual kill cases. R3 reproduced deficient whole-body mass acceptance;
that blocking numeric contract gap was corrected and independently retested,
including extreme/fractional/cancellation cases. Complete histories and all
failed exploratory runs are retained, not rewritten into passes.

Additional real-dispatch investigation tested a C-legal overcapacity no-loss
repulse with crew40000,troops30000,mass140002.4. All these values stayed correct
in actor AND store through command, segment, update and restart (SERVING).
The reviewer initially predicted a turn regression; that prediction did not
reproduce and is explicitly withdrawn. A private-copy invocation proves that
old doMass has a narrow-sum bug, but that helper currently has no production
caller except its own recursion. It is NOT an observed new turn/persistence
failure and does not justify banning the legal input. CR13/CR11 remain open
without inventing a new regression or claiming their closure.

Final independent overall acceptance and source/evidence binding are recorded
in the accompanying implementation review; hosted CI is reported in the PR.
No labels are required by the current diff: allowances/baseline and protected
tooling remain unchanged. No acknowledgment is applied by the agent.

## Final-review correction R4: converted population fixture clock

The reviewer's fresh independent revalidation caught a timestamp-sensitive
whole-record assertion in the newly enabled positive population boundary case.
Unlike the new ship fixtures and converted ground case, this fixture had not
frozen its own actor clock before preparing expected news and dispatching. A
second boundary could change only durable message timestamps. This was a
blocking test-stability finding despite provisional complete-suite passes.

Only that converted test now captures and freezes its private actor time in the
same callback that injects deterministic draws. No global environment, clock,
production rule, timestamp format, assertion or timeout is changed. The prior
complete-source identity/table and all prior passes remain historical; complete
normal/race/long and gates are rerun on the corrected source. The separate
review preserves the deterministic pre-fix reproduction and corrected rerun.

## Final stable-source acceptance after R4

Canonical executable source/test/gate identity:
`5ed07fe6dc61cc201634feb4a2127b9642338b94d79ae975e0ecf39fcfa25dc6`.
Every final source-bound command below verified that identity before AND after
execution. No production/test change occurred between these runs.

| Final execution | Result |
| --- | --- |
| `inventory-check.sh -json` / full normal tests |4981 pass nodes,4770 pass leaves,2 existing opt-in skips,0 failures; game40.917s |
| Exact `go test -count=1 -race ./...` |PASS; game373.123s,37.8% margin below unchanged600s |
| `GB_LONG_TESTS=1 go test -count=1 -race -json ./...` |4983 pass nodes,4772 leaves,0 skips/failures; game345.003s,42.5% margin |
| Full-suite real SIGKILL leaves |26 new ship +24 #41A +20 PR78 =70, all passed |
| Repeated affected race groups |Five repetitions passed with all full durable assertions retained |
| Six regenerated causal controls |All compile and fail the intended assertions on the final source |
| Build/gofmt/vet/modules/strict govulncheck/pinned lint |PASS;1000 lint findings matched,0 new/stale, no baseline changes |
| Original C verification/all15 mutations |PASS, including final full reconciliation |
| Gate self-tests/workflow policy/helper checks |PASS, unchanged tooling |
| Offline manifests |PASS; no cluster access |
| Growth/diff checks |No allowances or protected-tool changes; no acknowledgment labels required |

The independent R4 deterministic upstream-clock control failed before correction
and passed ten civilian/military leaf executions under five race repetitions
after correction. Record differences in the failing control were only news and
inbox timestamps; no world mutation discrepancy was hidden. Review findings R1
through R4 and the withdrawn CR13 diagnostic are preserved with raw output and
source hashes. This final table supersedes the earlier provisional table only;
that earlier execution history remains intact.

There are184 newly added passing nodes across15 new test roots. Two previously
existing positive hostile cases now assert complete successful atomic results;
all other existing rejection cases are retained. Exact race command output
records package success/times; detailed counted race nodes are from the complete
JSON long run. These are local measurements, not proof of hosted timing.

The ledger and archived raw outputs retain every failed setup/provisional test,
R3/R4 regression, negative-control mismatch and withdrawn diagnostic. Final
independent review is preserved separately; its source/evidence acceptance does
not authorize merge, deployment or full #41/#51/#38 acceptance.

## Independent final disposition

**APPROVE FOR PR**, bounded scope; no blocking finding remains. The independent
reviewer verified all440 executable source/test/gate hashes, all nine final
required command records and raw-output identities, full test/kill counts, all15
mutations, six causal controls, lint and security results. R1–R4 are resolved;
all initial findings/corrections and final revalidation remain preserved.
Final review SHA-256:
`231a309f4abbea4224beb6c1c9909daa547a884eaabacdb1e56e1de2c6459269`.
See [final review](evidence/cr17c-ship-assault-implementation-review.md) and
[validation ledger](evidence/cr17c-ship-assault-implementation-validation.json).
Approval is for normal PR review, not merge, deployment or full-issue acceptance.
All seven hosted CI jobs remain an independent PR prerequisite.

Evidence archive: [complete implementation history](evidence/cr17c-ship-assault-implementation.tar.gz),
SHA-256 `29509a88ec398aa81b1d30d81312295fcd87e7fd6af51c61caf49624d27f7531`; 343 manifest-verified members.
Phase0 archive and its original identities remain unchanged. No live operations,
container installation, image publication, merge or deployment occurred.
