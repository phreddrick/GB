# Independent Phase 0 evidence and document review

Verdict: **PASS for Phase 0 evidence accuracy and the documented owner-decision stop; no implementation approval.** The proposed combat implementation remains BLOCKED pending owner disposition of the explicitly unapproved gameplay outcomes. This is not a finding that bbolt transactions, private random staging, or a complete bounded assault are intrinsically infeasible.

Reviewer: separate read-only agent `/root/assault_phase0_review`, reviewing parent-produced evidence. Source baseline: `b3ac84d404cc180400336f91dadb828b52c5491a`, worktree `/tmp/GB-cr17c-ground-assault`. The earlier independent source/design review is preserved unchanged as `independent-phase0-review.md`; this report supplements it.

## Scope and independence

I independently read the original C and current Go combat, warning, reporting, workspace and storage paths; the Phase 0 document; the diagnostic C harness/helper copies, Go tests and compiler overlays; original and replay raw outputs; and serialized actor/store observations. I independently executed read-only source/diff inspections and Python JSON parsers to verify test names, statuses, counts, package durations, and selected before/after record fields.

I did **not** personally re-execute the C/Go test binaries, run a full normal/race suite, perform new assault SIGKILL tests, implement combat, or approve a production transaction. Test execution evidence below belongs to the recorded parent runs and their separately inspected replay, not a reviewer-authored execution. No repository file was edited by this reviewer. Only this report and the earlier report were written under the temporary evidence directory.

## Reproduction and causal evidence

1. **CR-19 is reproduced on current dispatch.** The archived fixture calls the real actor command interface, not a substitute combat wrapper. `cr19-actor-prefx.out` contains exactly four failed capture leaves (civilian/military × unfenced/fenced), with the explicit wrong target ownership/population and repulse-message failures. The separate hostile-unload guard test passes. Thus the expected exit status 1 is backed by the intended assertion failures, not merely a build error or unrelated failure.
2. **The missing ownership assignment is isolated.** The private `audit-capture-probe.go` differs from current ground movement by only a defender-elimination assignment to attacker Owner. Its two unfenced capture leaves pass. This is a causal diagnostic only: it proves neither atomicity nor complete accounting, and the document at lines 73–76 correctly says so.
3. **The split-write interruption is real.** The fixture's registry callback raises the actual store write fence at the defender casualty warning, after both legacy race writes and before planet persistence. Fenced snapshots show changed persistent morale/translation alongside the old stored defender sector and a different actor sector; actor AP is debited while stored AP is unchanged. Later segment/update flushes actor state. This demonstrates an actual legacy failure boundary, not an invented storage callback.
4. **The two assault entry paths are distinguished.** Current hostile `unload c/m` is blocked by the accepted population handler with no writes. A separate, clearly named external overlay bypasses only that guard to inspect the dormant helper: stored target becomes Owner1/Popn1000 while the actor still sees Owner2/Popn1. The public/current guard is not removed in repository source. The document does not falsely claim both paths have the same missing-Owner bug.
5. **C comparison is bounded and valid.** The harness links unmodified `source/move.c` and `source/rand.c`; helper bodies are copied verbatim from original sources. Fixed `random()` returns a valid in-range value and corresponding Go double/integer adapters exercise matching valid draws. Positive capture-fixture strengths avoid undefined zero division. This is controlled fixture parity, not a claim that production C and Go global generators produce identical streams. No AFV is present in this fixture; mechanical behavior is source-audited separately.
6. **C local versus persisted state is now explicit.** The C sector/AP adapters record observed writes, but `putrace` is a no-op. I requested clarification and the author added document lines 48–49: C morale/Points are local C race observations, not independent durable race evidence. C's mutual-destruction `done` exit omits the final `putplanet`; saved planet Info remains its old values even after local mobilization changes. Lines 92–96 correctly distinguish this from the independently saved sectors and AP. No C atomicity guarantee is claimed.

## Gameplay boundary versus solvable design work

The proposed outcome table is expressly **unapproved** (document lines 122–138). Normal defined C victory/repulse formulas are retained as the reference; the table does not silently authorize neutral/no-winner mutual destruction, uncontested occupation of an empty foreign sector, overflow rejection, zero-strength refusal, or ownership-transition accounting for combat. Mutual destruction visibly grants victory/morale/Points in historical C and then leaves neutral sectors; empty defense reaches undefined arithmetic. Resolving these into a coherent new atomic contract requires the owner's gameplay disposition under the task's explicit stop condition.

The already approved noncombat transition-accounting rule is precedent, not blanket combat authorization. Overflow prevention is already required, but its complete rejection/capping/distribution semantics must remain explicit. The document appropriately proposes whole-operation rejection without reroll rather than quietly introducing a population cap.

The following are **conditional policy decisions, not automatic schema blockers**:

- Pending assault counts are historical volatile turn-reporting data, not reconstructible from final world state. They are not a combat-strength input. If historical crash-loss semantics are retained and disclosed, a new schema is not automatically necessary. Immediate durable NewsCombat changes reporting timing; lossless next-turn aggregation needs separately approved persisted pending counts and atomic turn drain.
- Existing telegram inbox records can participate in a bounded bbolt transaction. Best-effort online warning versus always-durable inbox plus optional live hint needs an explicit delivery/duplicate guarantee. No new outbox is inherently necessary unless stronger delivery requirements are selected.
- A typed random draw transcript/private proposal and pure storage validation can be designed without uncontrolled callbacks, extra validation draws, a new global RNG, or a persistent RNG schema. This is feasible design work, not already implemented or proved. Failed/uncertain attempts must not automatically reroll/retry.
- An explicitly bounded one-step, nonmechanical sector-origin slice is permitted by the task if it is complete and rejects unsupported entire invocations before prefix effects. AFV mechanics, multistep commands and ship-retreat mass corrections can remain named follow-ups; complexity alone must not be used to declare all assault work impossible.

No new schema, commit kind, protocol, combat implementation, or gameplay decision is present in the inspected repository diff.

## Verified recorded execution evidence

I parsed both original and replay JSON outputs. Both contain the same intended test sets and no skip nodes:

| Recorded run | Independently verified content |
| --- | --- |
| Original C civilian/military/mutual | All exit 0. Defined victories target Owner1 with 1000 incoming residents; AP92/95. Mutual destruction emits victory but finishes neutral/empty. |
| Original and replay CR-19 regression | Four intended failing capture leaves, one passing hostile-unload guard; parent aggregate failure as expected. |
| Original and replay causal overlay | Two unfenced capture leaves pass (three nodes including parent); no other scope claimed. |
| Original and replay dormant ship overlay | One passing observation test; no current-path activation claim. |
| Accepted population/fixture race regressions | 355 pass nodes / 333 leaf nodes: rules21, db111, game223 nodes; no failures or skips. |
| Accepted process crashes | 24 named boundary leaves across colonize/evacuate/ship-crew/ship-troops, six boundaries each. The test source signals `syscall.SIGKILL`, checks the child's actual WaitStatus signal, then reopens the disposable database. The normal-returning crash-child entry is not counted as a kill. |
| Original/replay game package duration | 20.109s / 20.319s respectively, for this focused accepted regression selection only. Parent runners 25.737s / 20.922s. No full-suite or 600-second margin inference follows. |
| C fixture check | Recorded `make -C test/ctool check` exits 0; original 1.168s, replay 0.037s. |

`replay-audit.py` validates baseline and Go1.27.4, uses fresh temporary outputs/compiler overlays, and records per-command status/duration. It intentionally warns that an expected exit 1 alone is insufficient; I verified the four specific failures in its fresh outputs at `/tmp/gb-assault-audit-replay-hr4bur4v`. The initial C link failure and the original output-frame extraction correction remain disclosed rather than rewritten as clean first passes.

## Findings and dispositions

- **F1, evidence clarification (resolved):** explicitly distinguish C local race values from durable race records because the adapter `putrace` is a no-op. The final document now does so.
- **F2, blocking owner gameplay decisions (open):** mutual destruction, empty-owned targets and complete combat ownership/overflow accounting need disposition before a production contract can truthfully claim both C parity and coherent invariants. No implementation is approved by this review.
- **F3, reporting/notification policy (open, conditional):** choose the required durability/delivery semantics; source evidence alone does not mandate a schema or outbox. Current document correctly makes the alternatives conditional.
- **F4, implementation evidence (not yet applicable):** no new assault transaction, fault matrix or SIGKILL coverage exists. The document accurately preserves this limitation and does not substitute accepted #41A crash coverage for new assault acceptance.

No additional correction is required to the inspected Phase 0 claims. Final disposition: preserve this evidence, obtain the specified gameplay decisions, then implement/review a complete bounded slice and run its actual acceptance gates. CR-17, #41 and #51 remain open. No production merge/deployment or live-system operation was performed by this review.
