# Independent habitat factory review — round 1

Reviewer: `/root/habitat_independent_review`, a separately spawned Codex agent;
not the Claude implementation author and not the parent making corrections.
Mechanism: explicit read-only review task, noninteractive tool calls, bounded
12-minute review and a subprocess test bounded with `timeout 240s`. No existing
worktree file, index, branch or session was modified by this reviewer.
Baseline: 97ee43763da31b6b5eec3890e818e394dfcb1bf2. Reviewed Claude staged patch,
production surroundings and parent follow-up diff as it became visible.

## Findings and disposition

1. **P1 — requested ship identity lost at dispatch** (`factory_command.go:48-50`).
   The original staged interceptor parsed an exact requested ID, then passed
   `sh.Number`. An inconsistent map key for 77 could name valid factory 78 in a
   valid carrier chain and activate/debit 78. The workspace check then examined
   78, missing the originally requested key mismatch. Parent reproduced this
   through real dispatch in an isolated overlay. **Blocking until corrected.**
   Parent's visible follow-up passes `uint32(n)` on both factory branches and
   adds `TestCR17CHabitatRequestedShipIdentity` for #77 and 77. Code inspection
   confirms this addresses the cause; final execution/signoff remains pending.

2. **P2 — crash child diagnostics discarded** (`habitat_crash_test.go:129`).
   `cmd.Stderr = io.Discard` hides any race-detector diagnostic emitted before
   deliberate SIGKILL; exit status cannot prove race freedom for a killed child.
   Capture bounded stderr and assert absence of race reports after Wait, as the
   existing telescope harness does. The six crash-state oracles themselves are
   effective and this is a test-evidence gap, not an observed production race.

3. **P3 — read-witness claim too strong** (implementation record, participant
   table). The reused actor/DB location checks require each linked member alive,
   correct WhatOrbits/parent ID and unique chain membership, but do not require
   Docked/WhatDest on every read-only sibling. Selected factory has both checks.
   Correct the table rather than imply unimplemented sibling validation. No
   partial-write bug follows: full sibling records are immutable preimages.

4. **P3 — maximum-width arithmetic prose inaccurate** (implementation record,
   C/Go table). 65537/13108 is MaxHanger with other default factory statistics,
   not all maximum-width fields. At default rates, Primary/Secondary=255 and all
   six capacity terms maximal produce ship_size 69519 and new size 13904.
   Current implementation handles it safely; fix the proof claim and preferably
   add the all-fields-maximum pure test.

5. **P3 — lint prose says nothing suppressed**. Two new justified local gosec
   annotations exist (guarded Resource narrowing and own-test-executable launch).
   No baseline allowance is added or threshold weakened. Clarify the prose;
   these annotations do not themselves identify an exploitable operation.

## Independent technical assessment

- C `source/order.c:519-551`, `build.c:1451-1462` and `tweakables.h:110-121`
  confirm cost 4*BuildCost in an unsigned-int-width result, signed promoted
  hanger subtraction, 1+trunc(.2*trunc(ship_size)) size, unchanged masses, no AP,
  damaged factories allowed, and factory On/Notified after branch completion.
  Cost 16383 payable / 16384 unpayable and overfull/undersized hanger behavior
  are explicitly tested. Rejecting invalid wrapped hanger and requiring a live
  same-owner habitat are documented tightenings; same-owner remains a deliberate
  compatibility choice, not byte-identical original-C behavior.
- `PlanHabitatActivation` is pure, uses wide intermediates, finite nonnegative
  factors, record-width bounds and no global RNG or borrowed modifications.
- `prepareHabitatActivation` closes the mutation set to factory On/Notified/Size
  and carrier Resource/Hanger via exact whole-record equality against computed
  expected results. Race/governor authority, factory identity, carrier identity,
  root/context records and recursively linked membership are validated. Context
  slices are copied before sorting and nested ship fields are immutable checks.
- `CommitHabitatActivation` makes exactly two checked Puts within one bbolt
  Update; all canonical byte preimages are compared within that same transaction.
  The actor remains the writer, with no separate handle, allocator reservation,
  metadata/schema or CR-18 commit-kind change. Early and in-transaction write
  fence checks retain refusal accounting. Shared uncertainty guard is respected.
- Fault phases distinguish known abort (body error/panic rolls back) from commit
  uncertainty (Update may have committed) and confirmed commit/reporting errors.
  Visible readback is diagnostic only, never durable confirmation. No automatic
  retry/reopen/recovery appears. Errors sanitize hook/panic payloads.
- Actor `habitatStage` deeply copies participating ships, captures race/scope,
  root star/planet/universe and complete chains, stages bounded output, and
  verifies actor ownership before persistence. It installs only after confirmed
  commit and a second ownership check, with two pointer-preserving assignments
  and no callback between them. Before-install identity failure halts, with no
  partial install. Uncertain writes install nothing; confirmed report failures
  preserve installed committed state. HALTED shutdown refuses stale flush.
- Legacy `activateFactory` now refuses all factory activations; `giveOrders`
  returns before On/Notified/save. Bulk selectors that would reach a staged
  factory refuse before a nonfactory prefix executes. Planet-level wildcard
  switching the habitat itself remains existing nonfactory behavior. Numeric
  overflow/malformed selectors are bounded. No alternate writer was enabled.
- Six real-process crash stages block in the test executable only, signal an
  actual reached boundary, and the parent asserts SIGKILL status before reopen.
  Four precommit cases require complete old records; two confirmed postcommit
  cases require complete new ship records and unchanged world/auxiliary data.
  Restart, segment, update, orbit and free-list checks follow each boundary.
  No production fault endpoint exists. See finding 2 on discarded diagnostics.
- Whole-record actor/store checks, stale race/star/planet/ship witnesses,
  deferred legacy load disagreement, mode refusal, fence, cancel/queue, admitted
  shutdown/quiesce, output timing, response faults and stable pointers are covered
  by real actor-dispatch tests. The CR-07 pending-AP guard remains. C's per-turn
  factory-size recomputation is preserved, including the existing resulting
  hanger disagreement; land-on-ship linkage and double hanger accounting remain
  accurately deferred. CR-11/17/20 are not declared closed.
- Five baseline removals are precisely three retired unchecked sends and two
  retired G115 conversions. No golden allowance, scanner threshold, gate tooling,
  original C or workflow definition was changed. Maintainer `known-defect-added`
  is still required by CLAUDE.md; no gate-change-reviewed requirement is created
  by this diff. No acknowledgment label was applied by this reviewer.

## Execution evidence from this reviewer

Command: `timeout 240s env GOTOOLCHAIN=local GOCACHE=/tmp/cr17c-habitat-cache
TMPDIR=/tmp/GB-cr17c-habitat/test/ctool/obj/tmp
/tmp/gb-go1274-evidence/go/bin/go test -count=1 -race -json
./internal/commandrules ./internal/db ./internal/game -run Habitat`

Exit: 0; 224 passing test nodes, zero failures/skips; six real SIGKILL subtests
passed. Log: `/tmp/cr17c-habitat-resume/independent-review-tests.json`;
SHA-256 `87a3bb814417a80c7ce3d7a21330b4fbe849a18899468f5859b97f30dbaf5fde`.
The build started before the parent corrections; this is fresh execution of the
original staged test set, not final-source validation. Existing full-suite and
gate ledger was inspected as historical evidence, not counted as this reviewer's
own fresh full-suite run.

The old CLI review log received its explicit prompt and performed substantial
source inspection before termination; its initial stdin message alone does not
prove that particular attempt hung awaiting input. It has no completed verdict.
This agent task is separate, bounded and noninteractive.

Round-1 verdict: CHANGES REQUIRED for finding 1; address the test-evidence and
documentation corrections before final approval. No other production partial-
commit, resource-loss, premature-success or unsafe-flush defect was identified.

## Follow-up code review — corrections verified

The parent corrected the shared selector to pass the parsed requested number
on both factory branches, added two actual-dispatch alias regressions (including
unchanged complete actor/store snapshots and HALTED shutdown no-flush), captured
crash-child stderr with a DATA RACE assertion and fixed `/proc/self/exe` launch
without a G204 exception, and added maximum-field arithmetic assertions.
These are focused additions; the original staged source and historical logs
remain preserved in the parent's snapshot/evidence. No original assertion or
crash boundary was removed or weakened.

The reviewer independently inspected that follow-up diff and reran:
`timeout 180s env GOTOOLCHAIN=local GOCACHE=/tmp/cr17c-habitat-cache
TMPDIR=/tmp/GB-cr17c-habitat/test/ctool/obj/tmp
/tmp/gb-go1274-evidence/go/bin/go test -count=1 -race -json
./internal/commandrules ./internal/db ./internal/game -run Habitat`.
Exit **0**, **227 passing nodes**, zero failures/skips; all six real SIGKILL cases
pass with the strengthened child diagnostics. Log:
`/tmp/cr17c-habitat-resume/independent-review-final-tests.json`, SHA-256
`282f79f51747d3ef5480ffc8bae6c48b2904b88d85790e0b57c198e285c3b605`.

Reviewed follow-up code hashes:
- commandrules/habitat.go: ef1d1fc68cb152cdc409c7f19a227247b4a55dd66870712fb974700ebea3986c
- commandrules/habitat_test.go: 60e80cc14b1a85ba357e31621dc07efc04e1d99e848793880d4deb11f20edcaa
- db/habitat.go: d3a46cc38a73f477456b7f7de59117b685ad874d0a8365bd18f61cf047059cd9
- db/habitat_test.go: 603d22649075c697408eb1f873cbdae65fcecf215177423e2d02e59f49f210ac
- game/habitat_command.go: 04d225c4fcc628afd00dfe53d767d60a329025c4b3064e043bf9bc6f701682a7
- game/habitat_staging_test.go: 1a2e7cffd8e44bd05950663a0e9c64c37000bc6390d54dc0384327b5ff1c4962
- game/habitat_crash_test.go: 8b7b0b36a7d661f8b7587b639d652e8d5bf7b7fbeca7189782ee2697d4088a50
- game/factory_command.go: 0e8a5d9473ba6335cf9884ed15dcf53164583567bb60fc87ba39285df8cd6a7c

Findings 1 and 2 are resolved. Findings 3–5 are documentation accuracy items,
awaiting the final document/evidence update. **Code review: APPROVE for the
bounded habitat operation**, subject to accurate final documentation, full local
gates on final source and normal maintainer/hosted-CI review. No remaining
blocking production correctness or safety issue identified. This is not approval
to merge/deploy, close CR-17/CR-11/CR-20 or assert general exactly-once commands.

## Final documentation re-review — pending final execution ledger

Read the current WORKING implementation record after all three documentation
findings were corrected. The sibling check table now accurately distinguishes
selected-factory Docked/WhatDest checks from immutable sibling orbit witnesses;
maximum/default factory size values and 45 invalid cases are accurate; the single
proven resource-narrowing annotation is acknowledged while scanner/baseline
thresholds remain unchanged. The SIGKILL section now states Linux/fixed self path
and checks child race diagnostics, and the subsequent-action table distinguishes
SERVING/QUIESCED/STOPPED. The historical Claude ledger is explicitly historical
and unchanged rather than represented as fresh final-source evidence. No new
factual issue was found in those corrected sections.

The original compressed pre-fix JSON was independently decoded (28,466 bytes,
valid object) and contains none of the strings password/bcrypt/bearer/jwt/
credential/token. Its original reproduction history was not altered.

The final review/verdict placeholders and final Codex execution ledger were still
pending at this point. Final acceptance signoff is deliberately held until the
parent provides completed final tests and the final ledger. This is a completed
document review of the corrections, not a claim that still-running suites pass.

## Final evidence audit and completed review verdict

A further explicit, noninteractive read-only follow-up was bounded to six
minutes. The reviewer inspected the completed WORKING implementation document,
`cr17c-habitat-codex-validation.json`, the actual raw logs and per-run result
files in `/tmp/cr17c-habitat-resume`, and current source hashes. This is evidence
verification, separately distinguished from the two test runs actually executed
by this reviewer above. No new test run is invented or claimed here.

The audit independently computed and matched:

- All **11 final source/baseline SHA-256 values** against current files.
- stdout/stderr hashes and actual result-file exit statuses for all **17 final
  successful checks** and all **7 preserved restricted attempts**.
- Original historical ledger and original private staged patch SHA-256 values,
  demonstrating the retained Claude history was not overwritten.
- Full normal raw JSON: **3918 passing test nodes, 2 opt-in skips, 416 top-level
  passes, 21 passing tested packages**, including **227 habitat nodes / 29
  top-level entries / 6 SIGKILL subtests**.
- Full `GB_LONG_TESTS=1` race raw JSON: **3920 passing test nodes, 0 skips or
  failures, 418 top-level passes, 21 passing tested packages**, including the
  same **227 / 29 / 6** habitat results and no DATA RACE warning.
- Explicit DB/game race raw JSON: **1375 passing nodes, 0 skips or failures,
  269 top-level passes / 2 packages**; its habitat subset is **214 nodes / 25
  top-level entries / 6 SIGKILL subtests** because the separate pure-rules
  package is not part of that command.
- The original before/after exact-ID regression logs match their ledger hashes,
  with expected failure before correction and success afterward.

Raw outputs corroborate all 15 golden mutation scenario outcomes, golden
summary 1248 PASS / 143 XFAIL / 0 XPASS / 0 FAIL, lint 1000 baseline/report/
matched with 0 new/stale, C fixture reproduction, inventory reconciliation,
gate self-tests (intentional negative tool failures are tested, not hidden),
module verify/tidy, offline validation of 21 resources with zero invalid/error/
skip, workflow 188 helper tests / 17 policy checks, and strict govulncheck zero
reachable findings with 17 non-reachable module findings still reported.
The growth run genuinely used labels=[] and reports no protected-tooling delta.
The retained restricted attempts contain actual socket-permission/network
failures; the successful authorized reruns are separate records, not rewritten
failures. The final comment-only rules correction changes no executable logic;
the exact current rules file hash is in the ledger and its final race check
passed. Staged and working diff whitespace checks also passed during this audit.

The completed document accurately records the five review findings and fixes,
source ownership and transaction boundaries, conservative uncertainty semantics,
Linux crash-harness assumptions, C compatibility deviations, deferred landing/
hanger/cargo/allocator-related command work, CR-07/18/43 compatibility and open
CR-11/17/20 scope. It does not claim general durable acknowledgment, exactly-once
commands, public enrollment, a disk/fsync fault proof or a deployment approval.
The prior CLI review diagnosis is now appropriately conservative: both retained
logs reached explicit prompts/source inspection but never produced a verdict;
no unproven infinite-stdin-wait explanation is substituted for evidence.

All five findings are **RESOLVED**. No remaining blocking partial activation,
resource loss introduced by the migrated operation, premature success, actor
installation, preimage, write-fence, uncertainty or shutdown-flush defect was
identified. The intentionally preserved original-C turn-time size/hanger behavior
and same-owner carrier tightening are explicitly disclosed for maintainer review.

**FINAL VERDICT: APPROVE for normal PR review of the bounded habitat factory
activation implementation.** Final delivery remains conditional on the sole
maintainer's `known-defect-added` acknowledgment and hosted CI; no
`gate-change-reviewed` requirement is introduced by this diff. This approval is
not permission to merge, deploy, operate the running universe or close CR-17.
No worktree source/index, remote branch, PR, label or live system was changed by
this reviewer. The parent may copy this complete report into the new evidence
file and record its hash in the final ledger; the report intentionally contains
no hash of that evolving ledger, avoiding a circular hash dependency.
