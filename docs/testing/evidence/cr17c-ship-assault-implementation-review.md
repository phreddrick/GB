# Independent final review: atomic hostile ship assaults

## Verdict: APPROVE FOR PR

The bounded implementation is safe and locally accepted for **one explicitly selected, authorized, consistently planet-landed ship unloading a valid positive civilian/military force onto a foreign-owned sector, with mechanical/transporter paths excluded**. No blocking findings remain. This does not declare all #41, #51, #38, CR17, CR11, CR13 or CR20 complete, authorize deployment, or replace normal maintainer review and the seven hosted CI jobs.

Reviewed final canonical source/test/gate identity:

`5ed07fe6dc61cc201634feb4a2127b9642338b94d79ae975e0ecf39fcfa25dc6`

I independently verified every one of the440 file SHA256 identities in the final ledger against the current worktree, the separate frozen manifest, and the canonical computation used by the recorded runner (`sha256(json.dumps(manifest, sort_keys=True).encode())`). All nine required final commands record this same identity before AND after execution, exit0, and stdout/stderr hashes matching the actual raw files. No source mismatch or unrecorded production edit was found. Packaging final review/ledger/archive metadata must preserve this executable source identity.

## Scope and correctness review

Reviewed original C load.c/move.c and relevant ship mass/authority helpers, current closed command rules/storage/actor implementation, shared transaction/effects and workspace infrastructure, dispatch changes, permanent gameplay/database/failure/crash tests, updated historical tests, private causal overlays, complete raw validation evidence and the final implementation record. Phase0 evidence and its two separate reviews remain historical; their accurate reproduction of C's negative retreat mass is not misrepresented as implementation approval.

- Owner-approved survivor refund is implemented: all departing mass leaves on capture/occupation/mutual loss, only casualty mass leaves on repulse, returning complement agrees exactly, absorption remains sector-only. Finite/nonnegative complete onboard-body floors use promoted sums and bounded operand-ULP tolerance without clamping or repairing other mass code.
- Ship armor/damage supplies `(Armor+1)*(2-Damage/100)` attack factors; it is not replaced by movement terrain/affinity. Fixed casualty scale, military factor/integer ordering, morale constant, stochastic rounding and inclusive body-pool draw order match defined C. Outcome precedes absorption; empty occupation avoids zero division; mutual loss is neutral and absorption cannot resurrect attackers.
- Complete planet/global/per-owner civilian/troop/ownership/mobilization totals, both race morale/translation/Points and selected complement/mass are independently checked. Actual transitions avoid phantom ownership and repeated count adjustments; unrelated fields are preserved.
- ZeroAP is established and immutable star/AP witnesses are checked without a write. Selected owner/governor and active caller authorization are preserved; movement-only star-governor/slavery restrictions are not invented. One strict explicit ship selector and exact c/m grammar intercept all eligible forms before legacy effects; positive C defaults require a genuinely positive resolved complement. Hostile loading/reverse, invalid/signed/overflow/mixed selectors, mechanical defenders and targeted transporters cannot fall through to the unsafe writer.
- Complete unique living root/orbit identities and required third-party alliance races participate. Independent stale-diplomacy/orbital probes show these are real checked witnesses, not missing-defender assumptions. Carrier/peer noncombat behavior and all earlier accepted operations remain separately scoped.
- One bbolt transaction checks full preimages, repeats pure proposal validation and writes selected ship, complete planet and both races, then mandatory existing news/inbox/sequence allocations. Read-only context is not rewritten. No schema, bucket, commit kind, persistent RNG, protocol, commandID or outbox is introduced.
- Prepared random outcomes are bounded typed transcripts; storage consumes no new draw and never rerolls on stale/failed/uncertain persistence. Command mutex, write fence/refusal accounting, strict maintenance and shared uncertainty guard remain intact.
- Actor installation follows confirmed persistence and collective preimage/identity validation; stable actor addresses and sector slice are preserved. Buffered success/casualty text and live hints follow complete installation. Live notification does not duplicate the already durable telegram. Reporting failures remain committed; uncertain/install failures HALT without stale shutdown flushing or automatic recovery.

## Independent findings and dispositions

R1: initial gameplay expectations relied too heavily on the same production planner as their oracle. Permanent independent exact-value strength/casualty/race/accounting tests now cover defined C partial/victory cases, nondefault Armor/Damage, body-pool saturation and numeric/default boundaries. The reviewer-authored wrong-strength control compiles and fails the intended nondefault geometry assertion. **Resolved.**

R2: copied extinction-learning comment incorrectly said origin. It now correctly identifies landing-sector residents plus surviving party, preserving the ship helper's distinct C expression. **Resolved.**

R3: the author's final audit raised a real complete-body mass contract gap. Independent pre-fix tests reproduced accepted deficient mass and a promoted-sum case; corrected before/after floors reject them. Independent fractional, near-total65535-body, zero-mass and maximum-finite probes pass. This enforces the already approved unsafe-preimage requirement without inventing a new balancing policy. **Resolved.**

R4: a fresh independent race run naturally failed the newly converted positive historical population case at the complete-record assertion despite successful world installation. Deterministic two-second upstream-clock controls proved only news/inbox timestamps differed. A private per-fixture actor clock freeze fixes the actual cause, preserves every complete assertion and changes no production clock or timeout. Same control passes all ten c/m leaf executions across five race repetitions after correction. Complete suites and gates were then rerun on the final5ed identity. **Resolved.**

The reviewer also investigated a possible CR13 later-turn mass concern. The actual authorized overcapacity repulse preserves crew40000/troops30000/mass140002.4 in actor/store through command, segment, update and restart. A private-copy call proves the old doMass helper's narrow-sum bug, but production turns currently do not call it. The predicted persisted turn regression did not occur and is explicitly withdrawn; failed diagnostic expectations are preserved rather than presented as production failures. This investigation is not a blocker or an invented qualification to this bounded operation. Broader dormant helper/integration concerns remain open.

The two old positive-hostile-unload refusal expectations were legitimately migrated to actual execution plus complete actor/durable world/effect/sequence assertions; unrelated old rejection cases and all44 accepted crash cases remain. No test, timeout, fixture, scanner, allowance, baseline or gate was weakened.

## Fresh independent executions and causal effectiveness

The reviewer independently ran focused race suites, including all26 new real SIGKILL boundaries after the added preinstallation cases; independently authored and ran exact C-rule oracles and additional root-alliance/stale-diplomacy, numeric and real-dispatch turn/restart probes; independently reran all six causal control families; and causally reproduced/revalidated R4 rather than assuming successful author runs disproved it. Complete command/raw output/history files are retained under the implementation evidence directory and archive.

The six final-source control artifacts were separately inspected against their modified-source hashes and actual outputs: duplicate survivor mass subtraction, split world persistence, incorrect capture, missing news, missing telegram and actual unsafe legacy fallback all compile and fail their intended assertions. Final controls are regenerated from the corrected source; earlier failed expected-needle attempts remain historical evidence. No intentional defect entered production files.

Real kills use actual subprocesses, boundary barriers and SIGKILL WaitStatus checks. Full old-or-new records include ship, planet/sectors, both races, immutable AP, messages, inbox sequences and raw metadata; reopen plus segment/update detects stale overwrites. CrashChild ordinary tests are not counted as additional kills. No physical power-loss/fsync fault simulation is claimed.

## Complete accepted-source evidence independently audited

The following complete-suite executions were performed by the author and independently checked from raw output, commands, source identities and hashes; do not misrepresent them as full-suite commands executed by the reviewer.

| Final check | Independently verified result |
| --- | --- |
| Full normal suite/inventory |4981 pass nodes,4770 pass leaves,2 existing long-only skips,0 failures; game40.917s |
| Exact `go test -count=1 -race ./...` |exit0; game373.123s;226.877s/37.8% below unchanged600s limit |
| Complete `GB_LONG_TESTS=1` race suite |4983 pass nodes,4772 pass leaves,0 skips/failures; game345.003s;42.5% margin |
| Actual crash leaves in each normal/long run |26 new ship +24 population +20 ground =70, all pass |
| Six final causal controls |All intended runtime failures detected; no compilation/environment substitute |
| Original C fixtures/golden/all15 mutations |PASS, including unchanged-tree reconciliation |
| Build/gofmt/vet/module integrity/tidy |PASS; Go1.27.4,GOTOOLCHAIN=local |
| Strict govulncheck |PASS,0 reachable affected vulnerabilities;17 uncalled module advisories remain reported rather than suppressed |
| Pinned lint |1000 matched,0 new,0 stale; no baseline edits |
| Gate self-tests/workflow/helper checks |PASS; security/tooling unchanged |
| Offline manifests |PASS; no cluster access |
| Growth/acknowledgment and diff |PASS; no protected-tool or allowance change |

All nine required result records are verified in review-final-verification.json; normal/long counts and all70 crash leaves were independently parsed by package/test identity. All complete race stdout/stderr were checked for reported data races; none appear. Final controls' raw assertions and hashes are in review-final-controls-verification.json. The final record distinguishes all initial failures, provisional successes and corrected final successes. A local runtime margin does not promise identical hosted timing.

No maintainer acknowledgment label is required by the inspected diff: protected tooling, workflows, inventory allowances and lint baseline remain unchanged. Hosted Docker/Trivy/Syft/kind and the normal seven checks are still GitHub's responsibility; no hosted result is invented before PR creation.

## Final disposition and boundaries

**APPROVE FOR PR**, tied to source identity5ed above and the reviewed bounded scope. No unresolved partial-write, mass, authority, reporting, uncertainty, stale-installation or test-stability finding remains. Preserve the complete review/correction/evidence history in the PR. Maintainer review/hostedCI and any later deployment remain separate. No full #41/#51/#38 closure, exactly-once command semantics, mechanical assault, hostile reverse flow, transporter, broad CR11/13/20 or generic command durability completion is claimed.

Reviewer changes were limited to private evidence/test overlays; no repository source edit, merge, deployment, image publication, live database, Kubernetes, ArgoCD, Longhorn, secret or unrelated-worktree operation occurred.
