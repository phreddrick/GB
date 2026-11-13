# CR-17C #41B / CR-19: one-step atomic ground assaults

Baseline: `b3ac84d404cc180400336f91dadb828b52c5491a`.
The approved bounded implementation has passed local acceptance. Full issue
#41/#51 acceptance is not claimed; final independent review and hosted CI status
are identified in the evidence and PR.

## Owner approval and Phase 0 preservation

The owner's subsequent “Resume CR-17C #41B / CR-19 After Owner Gameplay Approval”
request authorizes the following bounded contract. This supersedes the Phase 0
blocking status; the original observations, failed regressions, reviews and
archive are historical evidence and remain unchanged.

- One-step nonmechanical hostile sector movement through `move`/`deploy`, civilian
  or military, valid positive quantities. Hostile ship unloading, AFVs and
  multistep assaults remain excluded; no unsafe legacy prefix may execute.
- Defender elimination with attacking survivors captures; surviving defenders
  repulse. Mutual destruction is neutral/no winner, never capture. An empty
  foreign-owned destination permits unopposed occupation without casualty draws.
- Determine the outcome before absorption. Absorption cannot resurrect an
  eliminated force or change mutual destruction into victory. Preserve the
  original casualty/body-pool random distribution and bound the resulting
  absorbed population to representable capacity; no wrapping.
- Reconcile sector and global/per-owner population, troops, actual ownership
  transitions, NumSectsOwned and MobPoints atomically; reject inconsistent
  preimages rather than repairing unrelated corruption. Preserve all unrelated
  fields and checked model widths.
- Preserve defined C morale/translation/Points and movement AP formulas except
  the explicitly approved no-winner mutual-destruction correction.
- Replace volatile aggregate counters for migrated operations with one durable
  per-assault combat-news entry and required defender inbox telegrams, allocated
  inside the same transaction. No duplicate later aggregate. Live delivery is
  best-effort after persistence; no exactly-once or command-idempotence claim.
- Use private random preparation and independently checked proposals; no fresh
  validation draws, retry/reroll, persisted RNG state, new schema/bucket/outbox,
  commit kind or protocol. Existing fencing, maintenance, uncertainty guard and
  HALTED/no-stale-flush behavior remain mandatory.

Phase 0 integrity was reverified before editing: archive SHA-256
`020cc7b69041df478da573c223686fc60c0dbb435f538d443aa0f067f58daf5f`,
review `dae11d8073eaa9566578c3298fb7bc5d1f15ed4b7246deb116fa8cef0c76d4c5`,
ledger `7216b18cbf4f08d8360431f1b017727c2fccbac41572f1bfa9217c0cf93c5e0f`;
every internal archive member matched SHA256SUMS. Phase 0 review approved only
its evidence, never a production implementation.

## Dispatch and exact scope

The `movePopn` entry classifies the entire direction string before the legacy
loop. Coordinates, direction aliases and map bounds must parse exactly. A route
containing any foreign-owned destination is intercepted in full. More than one
step is refused before prefix output, movement, mechanical defense or draws.
A one-step hostile invocation accepts an omitted amount as the positive entire
origin civilian/troop count (defined C default), or one explicit positive uint16
quantity. Zero, negative, overflowing and malformed amounts are refused; this
is the approved positive-only slice, not the C negative-amount bug fix.
All valid wholly nonhostile routes retain their existing legacy behavior and
CR-17 limitations. Hostile unloading stays refused by the population handler.

An eligible defensive AFV is recognized with the existing mechDefend predicates:
foreign, alive, landed at destination, staffed, selected guns/ammo/retaliation
available and not mutually allied. Third-party owners participate in alliance
witnesses too. Such invocations are refused before any volley or draw. Complete
unique living planet root chains, identities and orbital locations are checked;
missing/cyclic/inconsistent context is never treated as an absent defender.
Only root ships influence C's mechanical screening; contained ships are not
silently simulated or promoted into planetary defenders.

Review correction R2: current Go landed() means Docked && WhatOrbits==PLAN;
C fire.c landed() uses Docked && WhatDest==PLAN. The first new predicate
incorrectly required WhatDest==PLAN and could permit a human assault despite
an armed root AFV docked to a peer that current Go would use as a defender.
A permanent real-dispatch reciprocal-peer regression failed before correction.
The exclusion now covers the current Go predicate (including such peers), so
neither existing Go nor C mechanical effects can escape into the supported slice.
This is conservative refusal of deferred mechanics, not a repair or new balance
rule for mechanical combat. The C/Go helper difference remains explicitly deferred.


## Private rules and checked storage contract

`commandrules.AssaultInput` captures caller/governor, actor directory, requested
single direction/quantity/movement rate, complete planet/star, both races and
read-only planet-orbit ships/third-party race witnesses. Authority, active caller
governor, star assignment, slavery, ownership, exact coordinates, valid race
indices and coherent global/per-owner sector aggregates are required. Race64
cannot index C's Points[loser.Playernum] in a64-element array and is refused;
ordinary append-only player allocation already stops at63. Unrelated corrupt
accounting is rejected, never repaired as a side effect of assault.

`PlanAssault` owns all proposed copies, determines casualties and outcome, then
absorption, final ownership and complete accounting. Sector.Race, terrain,
stockpiles, resources, fertility, orbital fields, ship records and every unrelated
race/star field are preserved. Empty-origin cleanup uses actual remaining
residents. Per-owner NumSectsOwned/MobPoints derive only from final ownership;
planet and per-owner population/troop totals derive from final embedded sectors.
All actual field widths are checked; promoted arithmetic avoids narrow sums.

The closed `db.GroundAssault` contract independently recomputes the result from
expected models and a complete typed draw transcript. It rejects extra changes
and unsupported participants. Complete persisted JSON preimages are compared
inside the write transaction, then pure proposal validation is repeated there.
The four writes are planet, attacker race, defender race, and star/AP, followed
by required existing-format news and telegram records. Read-only witnesses are
checked but never rewritten. No allocator, commit/schedule/maintenance schema,
world-digest definition, bucket or CR-18 commit kind changes.

The operation uses the established command mutex, write fence/refusal accounting,
strict maintenance validation and shared uncertain-handle guard. World, news,
telegram records and the inbox sequence roll back together. Existing news and
telegram allocation helpers run inside this same transaction. Exhaustion, stale
inbox sequences, malformed relevant news keys or occupied next keys refuse the
operation instead of wrapping or overwriting prior messages.

## C parity, approved differences and randomness

Defined C source: move.c ground_attack and move_popn; rand.c stochastic rounding
and inclusive integer draws; GB_server.c adjust_morale. The documented Phase 0
civilian/military fixtures establish strengths, casualties, AP92/95, capture and
morale+1/-1/Points[2]+1 for the1000-versus1 case. The new rules restore that
normal outcome while correcting persistence/accounting as approved.

- Casualty scales, military integer division ordering, terrain/likes multipliers
  and the original morale-factor constant3.14159565 are preserved. Invalid,
  nonfinite, zero-strength or C-int-overflow arithmetic is rejected rather than
  used as an undefined oracle. Empty defenders bypass casualty formulas/draws.
- Survivor outcome is determined BEFORE absorption: defender survivors repulse;
  only attacking survivors capture; neither survivor means neutral mutual loss,
  no winner morale/Points and no absorption. Mutual/empty behavior differs from
  contradictory or undefined C behavior under explicit owner approval.
- For a surviving winner, sample the original inclusive body pool first, then
  saturate only the absorbed quantity at remaining uint16 civilian capacity.
  Attacker victory uses all eliminated defender civilians+troops; defender
  absorption uses killed attacking units. Do not reduce the random bound to
  capacity, wrap, redistribute or resurrect. Military survivors retain their
  independent troop capacity.
- Defined winner morale and C Points[loser.Playernum] updates are checked.
  Unopposed occupation follows the defined attacker-win adjustment branch,
  without invented casualties. Both races learn+5; extinction learning retains
  C's actual `origin population + origin troops + surviving party` test rather
  than Go's missing-party expression. Defending race learns another+5 when no
  attackers survive. Translation saturates at100.
- AP follows C log/log10 movement cost and the existing Go cast of configured
  MoveFactor before multiplication, checked against the current star/AP byte.
  Load/unload's approved zero-AP policy is unrelated and unchanged.

Production random draws use an isolated crypto source (uniform integer rejection
sampling and53-bit uniform fractions), never the engine/global math/rand stream.
Tests inject exact draws. Preparation records at most seven typed round/integer
entries; replay requests the next missing entry without regenerating previous
values. Storage consumes only that transcript and checks kind, bound, value,
order and exhaustion. C's round boundary/no-draw degenerate integer behavior is
retained. This preserves intended probabilities, not identical C/global PRNG
streams. No persistent RNG state is added. Failed preimages/uncertainty never
reroll or retry automatically. Text commands have no durable deduplication ID;
clients must not blindly retry after an ambiguous/lost response.

## Reporting, installation and failure classification

Exactly one per-assault combat-news entry and one consolidated defender inbox
message include both identities, origin/target, outcome, casualties and bounded
absorption. Timestamp and game context are server-owned. Both required effects
are derived by the closed storage operation, not optional caller-provided writes.
The migrated command never increments or drains legacy volatile counters;
preexisting non-migrated counters remain available for their original turn report.

After confirmed persistence, all actor identities/preimages are rechecked together.
Planet, sector-slice, race and star addresses are preserved during callback-free
installation. Only then are live hints and bounded attacker output released.
Live delivery uses Notify directly, with governor0 fallback; it must not call
warn/pushTelegram and create a second inbox record. An unavailable live recipient
still has the committed inbox. A response error/panic or installation failure
preserves COMMITTED classification and HALTs conservatively without shutdown
flush; an unavailable live hint alone is best-effort and does not undo success.

Confirmed aborts install/emit nothing. Uncertain writes install nothing, preserve
diagnostic world/message readback and the shared Store guard, HALT and block
turn/shutdown persistence. Visible readback does not prove fsync durability.
No automatic reopen, repair, reconciliation, retry or exactly-once promise exists.

## Permanent tests and actual local acceptance

New tests exercise the actual actor command dispatcher and the closed storage
contract, not a replacement combat service. The final set adds194 Go test nodes
(including parent groups): rules31, database43, actor120. Pure-case loops also
cover AP log boundaries for12 quantity/unit combinations and partial/mutual
outcomes for both unit types. Nodes are not independent scenario counts.

| Evidence | Actual result |
| --- | --- |
| Corrected-source complete normal suite + inventory reconciliation | PASS:4797 nodes; two existing GB_LONG_TESTS opt-in skips; game41.787s, runner46.072s |
| Exact `go test -count=1 -race ./...` | PASS: game363.384s, runner372.516s;39.4% margin below600s |
| `GB_LONG_TESTS=1 go test -count=1 -race -json ./...` | PASS:4799 nodes, no skips; game329.421s;45.1% margin below600s |
| Original C fixtures and existing golden parity | PASS; fixtures reproduce exactly; accepted golden allowances unchanged |
| All15 original golden mutations | PASS on corrected stable source;525.737s; expected failures remain effective |
| Inventory, lint and growth | PASS; lint1000 matched,0 new,0 stale; no allowance or acknowledgment-label delta |
| Gate helper self-tests; workflow policy/actionlint/shellcheck/helper tests | PASS; protected tooling unchanged |
| Offline manifest rendering, mutations, safety checks and schema validation | PASS; no cluster access |
| gofmt/build/vet/module verification/tidy diff/strict govulncheck | PASS with local Go1.27.4;0 code-affected vulnerabilities; full scanner output preserved |

The full current-source accepted executions record identical before/after source
manifests. Final source/test manifest SHA-256:
`3da030e68163e12f3dde7f85b8be724e41437a20ab0e8c38b2e10f111e917a85`.
Go1.27.4/GOTOOLCHAIN=local are used for application/tests/scanner. The unchanged
offline-manifest script internally permits auto toolchain selection only for its
pinned helper; no application toolchain pin or policy is changed. Docker builds,
Trivy/Syft and kind remain hosted CI responsibilities. Local timing is measured
evidence, not a guarantee of runner performance; the default600s limit is intact.

### Faults, crashes and causal controls

Database and actor tests inject errors/panics before admission, at preimage
checks, after each of four world writes, after news/inbox allocation, before
commit, at ambiguous commit reporting and after confirmed commit. They assert
complete rollback including inbox sequences, no proposal install/output, typed
outcomes, diagnostic-readback failure, shared uncertainty guard and no stale
HALTED shutdown flush. Installation identity changes/panics, postinstall panic,
response errors/panics, unavailable/panicking live hints, active fencing,
maintenance, queued cancellation, started cancellation, concurrent assaults,
draining, quiesce/restart and admitted shutdown have dedicated actor tests.

Twenty **actual SIGKILL** cases cover both civilian/military assaults at ten
boundaries each: before transaction; after planet, attacker, defender and star
writes; after news; after telegram; before commit; confirmed commit before
installation; installation before effects. Each child reaches the actual hook,
is signaled SIGKILL and must report that signal through WaitStatus. Reopened
stores contain the complete old or complete new logical world, required messages
and bucket sequences, never a mixture. Raw commit/schedule/maintenance/universe
metadata bytes are separately compared because the general fixture helper
normalizes three schedule timestamps. Allocator/free-list and unrelated records
remain intact. Known-aborted/confirmed-committed test oracles restart, segment and
update without stale overwrite. This is not physical disk-failure/fsync testing,
nor permission to reopen a genuinely uncertain handle as recovery.

All24 accepted #41A population SIGKILL boundaries remain unchanged and pass in
the complete suites alongside the20 new assault boundaries. No case, assertion,
expected failure allowance, protected gate or timeout was removed or weakened.
The PR77 immutable fixture seed optimization is preserved. The snapshot reader
now creates its private mode0600 file exclusively under os.Root and writes the
inspected bytes through File.Write; all original row/sequence comparisons remain.
This resolves a new traversal diagnostic without a scanner suppression/baseline
change and keeps arbitrary snapshot bytes out of pathname-taking APIs.

Four external compiler-overlay negative controls were executed and independently
repeated: removing capture; committing the planet separately before the checked
transaction; omitting news; omitting the telegram. Every run exited1 for the
specific permanent assertion, not a compilation failure. Phase0's original
four real-dispatch capture failures and split-write evidence remain archived.
R2's real reciprocal-peer refusal regression also failed before correction by
actually committing a human assault, then passed after correction.

## Independent review and remaining boundaries

The approved design, initial review, interim review, provisional review and R2
correction history are preserved separately. R1 (independent reviewer) found a
malformed current-stream news key could cause the legacy allocation helper to
overwrite an older key; the operation's strict key/next-allocation checks and
permanent regression resolved it. R2 (author, independently confirmed) found the
AFV-peer exclusion discrepancy described above; its before/after actor evidence
is preserved. Earlier reports and unsuccessful validation/fixture attempts are
not rewritten as clean first passes. Final review disposition and evidence are
recorded in the accompanying independent review artifact.

This is the approved one-step nonmechanical vertical slice only. #41/#51/#38,
CR-17, CR-11, CR-13 and CR-20 remain OPEN. Hostile ship unloading, AFVs, multi-step
assaults, transporters, general command IDs/deduplication/outbox and unrelated
movement remain deferred. Legacy commands can leave AP or other records divergent;
this operation refuses full-preimage disagreement and HALTs rather than blindly
merging or flushing it. A legitimate pending-AP condition should be reconciled
through a normal turn while SERVING before submitting another staged command;
there is no automatic resume/repair after HALTED. CR17A's stockpile AP limitation
and the CR07 ENROLLMENT_PENDING_TURN guard are not removed or claimed fixed.

Data-format impact: **NONE**. Existing model JSON, buckets, news/inbox encoding,
commit kinds/schema, world digest, authentication, enrollment and maintenance
contracts remain unchanged. Older binaries may still contain unsafe commands;
readable data is not a statement of behavioral/security rollback safety.

No merge, deployment, image publication, live database access, Kubernetes,
Argo CD, Longhorn, DNS/ingress/secret or recovery-point operation occurred.

## Final review and preserved evidence

Independent verdict: **APPROVE FOR PR REVIEW** for this exact bounded slice.
Both R1 and R2 are resolved. The reviewer independently executed focused race
checks/20 real kills, repeated all four causal controls, verified the corrected
mechanical boundary, and checked final current-source full-suite logs and hashes.
The provisional report and initial/interim findings remain in the archive;
Phase0 approval is never presented as implementation approval.

- [Final independent review](evidence/cr17c-ground-assault-implementation-review.md),
  SHA-256 `2eeaed9a662f4ccc5937aa669d29cfbae203ed9c3b395b01c0e8a14f8e96e873`.
- [Implementation validation ledger](evidence/cr17c-ground-assault-implementation-validation.json),
  SHA-256 `944616c5f05a673b0e74799a458edd22e78c78a5595246ec39fdd5746d4afaf5`.
  Exact commands, statuses, timings, test counts, source bindings and output hashes
  distinguish historical failures/drift from accepted final runs.
- [Lossless raw evidence/review archive](evidence/cr17c-ground-assault-implementation-logs.tar.gz),
  SHA-256 `a9badd3be9f185467f6d7b39a53c2aaccf834115dffe7ec0f4720f41fbaaf2ae`.
  All197 members and their SHA256SUMS verified; no databases, binaries, caches or
  credential artifacts included. Original Phase0 artifacts remain separate.

No maintainer acknowledgment label is required: no protected gate/workflow,
known-defect inventory, golden allowance or lint baseline changes. Hosted seven-job
CI and maintainer review are required before any separately authorized merge;
its run/HEAD are reported in the PR, not invented in this pre-commit record.
Local acceptance is PASS for the approved slice, not complete #41B/#51 acceptance.
