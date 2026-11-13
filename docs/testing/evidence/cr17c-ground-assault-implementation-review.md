# Final independent source/test review — validation condition still open

Reviewer: separate read-only agent `/root/assault_phase0_review`. Baseline `b3ac84d404cc180400336f91dadb828b52c5491a`; worktree `/tmp/GB-cr17c-ground-assault`. This review preserves the Phase0, approved-design, initial-implementation and interim reports unchanged. Reviewed file hashes are in `final-review-source-binding.json`.

## Verdict

**No unresolved blocking source or focused-test finding. PROVISIONAL: final acceptance remains pending stable, complete final-source exact race and long-race validation. This is not unconditional APPROVE.** The final complete normal suite is already a stable, current-source pass. Earlier successful race/long runs remain valid historical evidence but do not bind the last test additions; do not present them as exact final-source acceptance.

The only blocking finding during independent review was R1: malformed news-key selection could overwrite prior durable news. It was corrected without changing the legacy helper or weakening a gate. The permanent and independent regression both pass. No partial-write defect was found in the final migrated operation.

## Exact supported contract reviewed

This is one-step, nonmechanical sector-origin hostile `move`/`deploy`, with omitted positive entire-party count or explicit positive uint16 quantity. The owner-approved outcome corrections are applied: surviving attacker with eliminated defenders captures; surviving defenders repulse; mutual elimination is neutral/no winner; empty foreign sector is unopposed occupation without casualty draws. Winner is selected before absorption; no resurrection. Draw from the original full supported body pool and then saturate representable civilian capacity, with independent military-survivor capacity.

Defined C casualty integer-division ordering, terrain/likes, morale constant, stochastic rounding boundaries, inclusive draw bounds, AP log/log10 and configured movement-rate handling, winner morale/Points, initial translation and origin+surviving-party extinction learning were checked against original sources. C's direct Points[loser.Playernum] index is preserved for defined indices; player64 is explicitly refused. Model-width, finite-strength, morale and Points arithmetic is checked. The rule tests cover actual partial casualties and mutual destruction for civilian and military variants, plus absorption saturation and no-resurrection.

Full-route classification occurs before any legacy movement prefix or output, including aliases, wrap, invalid suffixes and friendly-prefix→hostile-later cases. Invalid supported hostile inputs never fall back. Complete unique living planetary root-ship chains and owner-race diplomacy witnesses are validated; third-party hostile armed AFVs are refused, mutually allied AFVs remain immutable checked witnesses. No mechanical shots or legacy hostile ship-unload writes escape this boundary. Nonhostile movement, multistep hostile movement, negative hostile quantities, mechanical combat and hostile ship unloading remain explicitly outside this transaction; the review does not close all #41/#51/CR-17.

## Complete transaction and actor consistency

Writable world records: entire embedded-sector planet, attacker race, defender race and home star/AP. The derived planet changes include both sectors, global/per-owner civilian/troop totals, actual ownership, NumSectsOwned and MobPoints; unrelated fields are preserved. Read-only ship and third-party race records participate with complete persisted preimages. Directory/scope and movement configuration are trusted actor context, not browser claims. Full actor preimages are captured privately, compared inside the same write transaction, and pure typed rules are recomputed after those matches. No allocator, bucket/schema, commit kind, turn sequence, schedule or maintenance format change is introduced.

Required one-per-assault existing-format news plus consolidated defender inbox message are derived and appended inside that transaction, including inbox sequence allocation. Current-stream news-key validation, sequence exhaustion and next-key absence prevent prior news overwrite; telegram format/sequence/next-key guards prevent stale reuse. Message rows join diagnostic readback but visible readback never establishes fsync durability. The migrated operation does not increment or drain legacy groundAssaults; a dedicated test preserves already pending legacy counts and verifies their separate next report.

Private proposals have no borrowed mutable sector slice. Installation follows confirmed commit and a combined actor-identity/preimage check. Planet, sector slice, A/D race and star addresses remain stable. No callback occurs between assignments. A failed installation or committed reporting error halts and suppresses stale shutdown flushing; uncertain commit installs nothing, preserves the shared store uncertainty guard and requires operator reconciliation. Confirmed aborts leave every participating record/message/sequence unchanged. Existing write fence, strict maintenance refusal, command mutex, actor serialization and started-versus-queued lifecycle remain intact.

Success messages are buffered until installation. Live defender hints are postcommit registry notifications only; no legacy warn/pushTelegram fallback creates a second inbox write. Unavailable live delivery is best effort because the inbox is already durable. Error/panic paths retain truthful committed/uncertain classification. No automatic reroll, retry, deduplication, durable command ID or exactly-once guarantee is introduced.

## Randomness and bounded validation

Production preparation uses isolated uniform crypto fractions and unbiased integer rejection sampling, preserving intended C distributions rather than identical historical/global PRNG streams. No global RNG helper or persistent turn RNG changes. A typed transcript records at most seven round/integer requests; storage validates kinds, bounds, unused fields, finite values, exact ordering and exhaustion without generating new draws. Validation replay cannot reroll combat, and a stale transaction abort does not re-enter preparation.

## Fresh independent execution

All runtime work below used disposable stores/test subprocesses, Go1.27.4, GOTOOLCHAIN=local, and a temporary writable cache. No source edits were made by this reviewer.

1. Focused complete new-assault race tests:

```
GOTOOLCHAIN=local GOCACHE=/tmp/cr17c-habitat-cache /tmp/gb-go1274-evidence/go/bin/go test -count=1 -race -json -run '^(TestAssault|TestGroundAssault)' ./internal/commandrules ./internal/db ./internal/game
```

Exit0. **193 passing nodes / 182 leaves**, rules31/db43/game119 nodes; no skips or failures. Package times rules1.064s, db2.505s, game19.839s. Raw output `final-review-focused.out`, stderr empty. This includes **all20 real SIGKILL boundary leaves**, not merely their normal-returning child entry.

2. Independently replayed all four causal negative controls into a separate `independent-negative-controls/` directory, leaving author outputs unchanged. Invocation: Go1.27.4/local environment, `python3 independent-negative-controls/negative-controls.py`; wrapper exit0 means all four required intended failures were detected. Each underlying go test exited1; compile/syntax failures are rejected by the controller. Capture removal fails both real actor capture leaves; independent split write fails the partial-world/effects/sequence invariant; omitted news and omitted telegram each fail their required durable-effect assertions. Each run records matching source_before/source_after hashes. These are diagnostic overlays, never installed production changes.

3. Earlier independent R1 regression and focused rule race run remain separately recorded with exact commands/status and infrastructure cache-failure history. They are not rewritten as fresh final execution.

## Real-crash and lifecycle proof checked

The test executable, not production configuration, owns all fault controls. Parent waits for an actual transaction boundary, sends SIGKILL and asserts the child's exact signal status, then reopens the same private bbolt file. Both civilian/military variants test before admission, each of four world writes, news, telegram, before commit, confirmed commit and installed boundaries. Complete old-or-new row maps and bucket sequences reject partial world/messages/allocator state. Additionally, the last test additions compare raw commit, schedule, maintenance and universe metadata bytes after every kill; the preexisting snapshot helper's schedule-time normalization cannot conceal a changed schedule. Restart, following segment/update and single-news assertions detect stale flushing and duplicate aggregate reporting.

The fixture-helper OpenRoot/O_EXCL/File.Write change preserves test-owned path confinement, 0600 permissions, full bytes and checked close/errors. All record/sequence comparisons and PR77 immutable seed caching remain; no scanner suppression, lint baseline reduction or gate relaxation was found. This avoids treating untrusted snapshot bytes as a path without removing substantive validation.

Actor tests exercise actual dispatch, committed installation identity checks, response/notification faults, unavailable readback, RNG failure, stale planet/race/AP/ship witnesses, queued and started cancellation, serialization, quiesce, shutdown admission, governor authority and aliases/defaults. They are not a fake storage wrapper.

## Full validation evidence inspected and remaining condition

Verified `acceptance-normal.result.json`: exit0, runner45.411s/game42.157s, source_before==source_after and all hashes match the reviewed tree. Inspected static build/vet/format/module checks, original C/golden fixtures and15 mutations, inventory, gate helper tests/workflow checks, offline manifests, strict govulncheck and final lint results. Successful lint is `final-rules-and-lint` (1000 matched,0 new,0 stale); earlier failed lint artifacts remain historical. Growth reports no changed protected tooling or expected-failure allowances. No maintainer acknowledgment label is indicated by the inspected unchanged baselines/gates, subject to final complete diff.

At report creation the stable final-source full exact race and GB_LONG_TESTS=1 race reruns were still pending. Earlier race-full-final passed game373.163s but preceded final test-only additions. Earlier long-race-final passed but its source-binding records explicitly show those three test files changed during execution; it is **not** exact-final-source proof. This condition must be resolved and final recorded source hashes verified before unconditional review approval or complete acceptance claims. A single local runtime also cannot prove hosted reliability; all seven hosted checks remain the PR requirement.

## Remaining limitations and stopping boundary

No general command durability, ground ship unloading, AFV/multistep assault, CR-11/CR-13 repair, CR-20, persistent outbox or command replay protocol is implemented. #38/#41/#51 remain open for their broader scopes. The approved bounded correction may be proposed for PR review after the final validation condition is met. Hosted container/scanner/kind checks are still hosted responsibilities. No merge, deployment, image publication, Kubernetes operation or live database mutation occurred in this review.


# Final approval addendum after R2 and stable corrected-source acceptance

This addendum supersedes the provisional verdict above **without rewriting its history**. The provisional report is also preserved as `final-independent-review-provisional.md`. R2 was subsequently discovered by the parent and independently confirmed; `R2-independent-confirmation.md` records that discovery, my earlier missed C/Go landed-helper distinction, the causal failing actor regression, correction and fresh independent execution. R1 and R2 are both resolved. The earlier “no source blocker” conclusions were provisional and must not be read as pre-R2 approval.

## Final verdict: APPROVE FOR PR REVIEW — bounded operation only

No unresolved blocking correctness, authorization, transaction-participation or test-effectiveness finding remains for the approved single-step, nonmechanical, sector-origin hostile `move`/`deploy` slice. This is **not** whole-issue completion for #41/#51/#38, authorization to merge/deploy, or approval of deferred mechanical, multistep or ship-origin assault behavior. Normal maintainer review and all seven hosted checks remain required.

The corrected mechanical exclusion conservatively respects current Go `landed` eligibility (Docked and planetary root orbit), including reciprocally docked root peers. It does not claim C and Go landed helpers are identical: original C uses WhatDest==PLAN. The peer regression fails before the correction by actually permitting combat, and passes afterward with no changes or successful output. Ordinary and third-party mechanical refusals and mutually allied AFV immutable-witness acceptance independently pass on corrected source. This is a bounded deferral, not a mechanical gameplay rewrite.

## Final source binding independently checked

Final accepted source identity: `3da030e68163e12f3dde7f85b8be724e41437a20ab0e8c38b2e10f111e917a85`.

`accepted-final-source-binding.json` contains385 file hashes. I independently recomputed every current file hash and verified they match. Each of the five final `accepted-source-*` execution records has matching source_before/source_after dictionaries, and every hash still matches the reviewed source. The accepted source manifest is additionally preserved as `final-approved-source-binding.json`; the earlier pre-R2 review binding remains historical.

## Corrected-source final acceptance evidence independently reconciled

| Final execution | Actual result | Runtime / coverage |
| --- | --- | --- |
| Complete normal suite via unchanged inventory runner | PASS, exit0 | 4797 passing test nodes; two existing GB_LONG_TESTS opt-in skips; game41.787s, runner46.072s |
| Exact unchanged CI race invocation `go test -count=1 -race ./...` | PASS, exit0 | game363.384s, runner372.516s; unchanged600-second package timeout |
| Complete `GB_LONG_TESTS=1 go test -count=1 -race -json ./...` | PASS, exit0 | 4799 passing test nodes, no skipped test nodes; game329.421s, runner330.888s |
| All15 golden mutations | PASS, exit0 | Each required positive/negative outcome retained; runner525.737s |
| Format/build/vet/module verification/tidy/strict govulncheck/lint | PASS, exit0 | runner12.754s; lint1000 matched,0 new,0 stale; no reachable vulnerability |

I parsed raw normal/long Go JSON rather than relying on parent totals: no failed nodes; the two normal skips are exactly the existing production-timing GB1-04 tests, and both execute in the long suite. Package entries with “skip” merely mean no test files and are not skipped test nodes. Both normal and long runs include all20 named assault real-SIGKILL leaves and all24 accepted population real-SIGKILL leaves. The child entry itself is not counted as a kill. I inspected the plain exact-race output and its exit0 record; it independently reports the game package363.384s. That gives39.436% local headroom below600 seconds, without increasing timeouts or altering CI gates. Local timing is not a guarantee of hosted reliability.

The existing static gate/self-test/workflow/C fixture outputs, manifest validation and growth check pass. Their protected inputs remain unchanged in the final diff. No protected workflow/gate tooling, lint baseline, defect inventory or expected allowance was modified. Accordingly no `gate-change-reviewed` or `known-defect-added` requirement is introduced by this reviewed diff. Future unrelated changes would require reevaluation.

I independently checked all86 output/stderr SHA-256 values referenced by the43 executions in `docs/testing/evidence/cr17c-ground-assault-implementation-validation.json`; all match preserved files. Its final source identity and counts agree with the current manifests and raw outputs. Early cache/build/fixture/lint failures, pre-R2 passes and source-drift runs remain historical and are not substituted for the five stable final executions. The earlier provisional source-bound acceptance condition is now satisfied.

## Independent work versus inspected author executions

I personally executed the focused new-assault race suite (193 nodes/182 leaves including20 real kills) before R2; four independent causal negative-control overlays; the R1 news-preservation regression; and the final corrected mechanical/peer/third-party/allied source-bound race regression. The latter has exit0, five nodes/no skips, game3.565s/runner4.223s and the exact final corrected-source identity. Full normal/exact-race/long suites and repository gates above were executed by the parent; I independently inspected and reconciled their actual logs, statuses, source bindings, counts and hashes rather than claiming them as my own fresh executions.

All required world/AP/race/news/telegram records participate in one checked transaction; actor installation and buffered effects follow confirmed persistence; uncertainties halt without stale shutdown flushing. Actual crash tests additionally compare raw commit/schedule/maintenance/universe bytes, so fixture timestamp normalization does not hide metadata changes. Required news and telegram omission, split persistence and capture regression controls fail for their intended assertions; these tests genuinely detect the original defect classes.

Remaining limitations are explicit and acceptable for this bounded PR: no general command IDs/deduplication/outbox, no exactly-once/lost-response retry guarantee, no physical disk-failure/fsync proof, and no mechanical/multistep/ship assault implementation. Existing nonhostile movement and broader CR-11/CR-13/CR-17/CR-20 issues remain open. No further gameplay or schema decision is silently introduced.

No merge, deployment, image publication, Kubernetes/Argo/Longhorn operation, live database access or other live-system modification was performed by this reviewer.
