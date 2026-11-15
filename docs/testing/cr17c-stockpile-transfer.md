# CR-17C / #45: atomic planet-stockpile transfers

Independently reviewed bounded implementation; hosted acceptance is recorded on
the pull request. Base: `471f92c6de9ceb84718f253a59e6d54f19c31eac` (fresh origin/master).
Branch `feature/cr17c-stockpile-transfer`, worktree `/tmp/GB-cr17c-stockpile-transfer`.
This completes one bounded #45 invocation, not #45, CR-17, CR-11 or CR-20 overall.

## Reproduction and preserved gameplay

Before production changes, `TestStockpileTransferDurableAP` invoked the real actor
with `transfer 2 x 7`. It printed success and persisted crystals 100/10 → 93/17,
while star AP was actor49 / stored50 (preimage50). The desired complete-record
assertion failed. Full planet/star preimages and actor/store results are preserved
in `evidence/cr17c-stockpile-pre-fix.log.gz`; original local execution logs remain
under `/tmp/gb-stockpile-transfer-validation/`.

`source/load.c:707–811`, its dispatcher (`transfer(...,1)`), the accepted CR-17A
handler and its tests were compared. The whole existing invocation is migrated:

| Property | Preserved behavior |
|---|---|
| Grammar | `transfer <player> <commodity> <amount>`; existing player/name/prefix resolution; exactly three args |
| Commodities | first character `r`, `x`/`&`, `f`, `d` (planet stockpiles only) |
| Amount | existing `strconv.Atoi` parsing; nonnegative, no more than sender balance; omitted, malformed and negative values refused |
| AP | exactly **one star AP**, including zero-quantity and self transfers; never universe AP |
| Balances | both players within one planet; original uint16 assignment arithmetic, including destination wrap and self-transfer, retained |
| Scope/authority | planet scope and authenticated active caller governor; no new planet-owner, carrier-owner, star-governor or recipient permission policy |
| Effects | same transfer text to sender and registry-only live recipient notice, after commit/install |
| Invalid requests | no write, AP debit or success; CR-17A's existing invalid-command behavior remains |

Additional bounds checks refuse invalid authority/directory indexes rather than
indexing model arrays. Empty commodity is refused. No accepted grammar or balance
cap is changed; broader C/Go authorization findings belong to CR-41. The prior
CR-17A difference from C (invalid commands do not spend AP) remains intentional.
`load/unload`, dump, factory, habitat, telescope and route writers are not widened.

## Transaction and ownership

`db.StockpileTransfer` / `CommitStockpileTransfer` is a closed typed operation on
the existing Store handle, sharing the existing command mutex, uncertainty guard,
error/outcome types and diagnostic readback. It does not broaden dump or other
command contracts. No generic callback, new bucket/schema, commit kind, allocator,
command ID or outbox is introduced.

| Participant | Role |
|---|---|
| Complete scoped planet | write1: only selected commodity balances for caller/recipient |
| Complete scoped star | write2: only caller AP decremented by one |
| Complete caller race | read-only identity/active-governor witness |
| Complete recipient race | read-only identity witness; deduplicated for self transfer |
| Directory | actor-local scope witness, verified against planet/star and checked unchanged before installation; directories are not persisted records |
| Maintenance/fence | checked at admission and within the one write transaction |

The storage preparation independently reconstructs the sole permitted changes and
rejects every unrelated proposed planet/star field change. Full encoded preimages
of both writes and race witnesses are compared **inside the same bbolt write
transaction**, including conflicts introduced after preparation. Every Put error
is checked. Existing orbital references, sectors, other players and auxiliary
records remain unchanged. This operation neither reads nor changes ship/orbit
membership, so it does not invent ship writes or allocator participants.

The actor supplies a private planet proposal and workspace-owned star/race copies.
Planet sectors are deep-copied; star and race models contain value arrays/strings.
Actor addresses and full expected contents, plus directory identity, are checked
before persistence and again before installation. Both stable-address assignments
are non-callback operations; no fallible hook runs between them. The single success message
is kept as a private immutable string and sent only afterward; no new response-size
restriction is imposed on existing world names. Recipient delivery stays best-effort live
registry output; there is no hidden telegram/news write or durable delivery claim.

## Outcomes, faults and crashes

- Confirmed abort: no planet/AP/witness/auxiliary change or proposal installation.
  Storage failures/conflicts conservatively HALT, prohibiting later turn/shutdown
  flush of the stale actor world.
- Confirmed commit: both writes durable before coherent installation/output. A
  confirmed reporting error still installs both, then HALTs without claiming rollback.
- Uncertainty: no unverified proposal installs; handle uncertainty guard and
  diagnostic-only readback retained; HALT/no stale flush/no automatic retry/reopen.
- Installation identity change or panic: confirmed store remains intact, actor
  HALTs; no partial two-record install. Post-install failure retains both records.
- Response failure: committed/installed records remain; HALT per existing policy.
  Best-effort live notices cannot provide exactly-once delivery or safe blind retries.

Errors and panics cover before admission, each check/write, validation, precommit,
confirmed postcommit and uncertain reporting (including unavailable readback).
Tests cover stale planet/star/caller/recipient preimages, late external writers,
write fence, durable maintenance, actor identity faults, response failures,
concurrency and a command already admitted when shutdown begins.

**Nine real SIGKILL cases**, using the test executable and disposable bbolt files:
`stockpile.before`, `check:1`, `validated`, `write:1`, `write:2`, `before-commit`,
`after-commit`, `preinstall`, `installed`. Each requires an actual signaled SIGKILL
exit, then compares **every logical record and bucket sequence** with the complete
old or independently calculated new state. Each known-boundary case reopens,
verifies actor/store equality and runs segment/update persistence checks. No
uncertain production outcome is treated as known aborted or automatically repaired.
Physical power-loss/fsync hardware failure was not tested.

The original-implementation regression fails for the actual lost AP. A separate
Go overlay negative control omitting only the star write fails the contract's
`partial or incorrect planet/AP record set` assertion. Intentional defects remain
outside the worktree. Successful tests cannot pass merely by rejecting transfers:
all five commodity spellings, zero/max/self variants, existing arithmetic tests,
concurrent success and scheduled timer turns exercise real commits.

## Compatibility and changes to historical tests

The complete normal/long-race suites retain enrollment, route, dump, factory,
habitat, telescope, cargo, population, assault, CR-18/43 and WebSocket coverage.
No existing SIGKILL cases, gates or golden expectations are removed.

CR-17A's obsolete single-PutPlanet transfer fault adapter is replaced by the new
complete transaction fault matrix (including both record writes). Route adapter
coverage remains unchanged. Existing effects/draining tests still exercise both
commands, using the real new transfer boundary. Transfer's previously volatile
AP no longer needs an enrollment deferral. Existing dump/factory/habitat guard
tests now create the unrelated volatile AP precondition explicitly and retain
`ENROLLMENT_PENDING_TURN` assertions, refusal-store equality and later admission.
A new test proves immediate enrollment after durable transfer, and refusal after
an unrelated volatile AP change. **The guard's production code is unchanged.**

A dedicated actor test crosses restart, an automatically scheduled segment and
full update under a disposable test clock (no admin turn trigger), then restart.
It checks complete actor/store equality, crystals and durable commit kinds.
Subsequent legitimate turn AP income is not mistaken for the transfer debit.

## Validation and limitations

Actual results, commands, source/log SHA-256 values and failed attempts are in
`evidence/cr17c-stockpile-validation.json` and the external validation directory.
Go1.27.4 with `GOTOOLCHAIN=local`; default600s package timeout unchanged. No lint
baseline, inventory allowance, protected tooling or workflow change. No maintainer
acknowledgment label is currently indicated; no labels were applied.

Preserved initial failures: a wrong relative setup path (one no-tests execution,
not counted as acceptance), one unused fixture variable, incorrect test assumptions
that HALT emits no diagnostic broadcast, a test enrollment caller outside the
fixture policy, obsolete volatile-transfer guard fixtures, sandbox localhost/DNS
restrictions and temporary-module VCS discovery interference. Corrected execution
uses the existing approved toolchain and an isolated worktree-owned TMPDIR. Tests,
timeouts and gates were not suppressed or weakened.

The original local-only handoff did **not** include independent review or hosted CI;
that historical state is retained in the original validation ledger. The subsequent
owner-authorized review and PR delivery are recorded below. Docker builds, Trivy/Syft
and kind are hosted checks, not executed on igor. No live systems/database,
Kubernetes/Argo/Longhorn, host services, image publication or deployment operation
is part of this change. Remaining direct command writers,
transporters, ship ownership, general staging/dedup/outbox and CR-17D acceptance
remain open.

## Recorded local results (2026-11-15 UTC)

| Check | Actual result |
|---|---|
| Final complete normal suite | 5087 passing test nodes, 2 existing opt-in skips; game 36.243s |
| Exact ordinary complete race suite | 5087 passing nodes, 2 existing opt-in skips; game 424.662s / 600s |
| Complete GB_LONG_TESTS=1 race suite | 5089 passing nodes, 0 skips; game 398.758s / 600s (33.5% headroom) |
| Installation/output assertion strengthening | 5 race-enabled nodes pass; also included in the later frozen ordinary race suite |
| Process crashes | 9 new real SIGKILL boundaries pass in normal, ordinary race and long race; all pre-existing crash suites retained |
| C fixtures / golden parity | make check passes; normal/long golden tests pass; unchanged PASS1248/XFAIL143/XPASS0/FAIL0 |
| All 15 golden mutations | complete fresh rerun passes on frozen final Go source/tests; hashes checked unchanged |
| Inventory | final normal completion records reconcile; existing explicit CR-18 UNTESTED entry remains |
| Lint/growth | 1000 matched / zero new / zero stale; allowances and protected tooling unchanged |
| Build/format/vet | pass |
| Gate self-tests | approval, lint and mutation-helper self-tests pass |
| Workflow/offline checks | 19 policy checks, 231 helper tests, pinned actionlint pass; manifests21 valid/0 invalid/errors/skips |
| Strict govulncheck | zero affected vulnerabilities; reports17 noncalled module vulnerabilities (not suppressed) |
| Module/diff checks | verify, tidy-diff and git diff --check pass |
| Unrun | At the local-only handoff: ShellCheck unavailable locally, including actionlint's ShellCheck integration; Docker/Trivy/Syft/kind hosted checks and independent review not performed |

The complete long run preceded a final **test-only assertion strengthening** that
explicitly checks no transfer-success text on installation/effect failures. Its
affected cases were rerun with race detection, and the subsequent complete normal
and ordinary race suites and frozen mutation rerun include that stronger assertion.
All runs use the final production implementation. The previous 15-scenario run
straddled the response-size compatibility correction; its log is preserved and
is superseded by the final frozen-source run, not rewritten. Earlier failures
and successful intermediate executions remain in the external directory.

No unresolved implementation failure was found by local validation. The historical
local-only handoff was not independent approval or whole-issue acceptance.


## Independent review and authorized PR delivery (2026-11-15 UTC)

The owner subsequently authorized a separate independent review, focused fixes if
needed, commit/push, one PR and hosted CI monitoring. The original local evidence
and its ledger remain unchanged under `/tmp/gb-stockpile-transfer-validation/`.
New delivery evidence is under `/tmp/gb-stockpile-transfer-delivery/`.

A separate read-only reviewer inspected the actual complete diff and all new
files, C semantics, preimages, balances/AP, actor ownership/install, output,
uncertainty, command compatibility, and crash/restart evidence. The verdict is
**APPROVE for this bounded PR**, with no blocking or actionable nonblocking
findings; no production correction was required. The reviewer independently ran
**195 race-enabled test nodes**, zero skips/failures (db2.904s, game126.428s),
including all nine real SIGKILL cases and the modified admission-guard regressions.
They independently reproduced the exact-base original handler's AP loss and the
omitted-star-write causal control; both failed the intended assertions. The initial
wrong-directory control execution is preserved separately and not counted.

The [full independent review](evidence/cr17c-stockpile-independent-review.md)
has SHA-256 `4187639e16fdbdf37329c32bffa49a12d9052d59e7f8c5c5b942122ecd0d2be1`.
Its separate external ledger is `/tmp/gb-stockpile-independent-review/SHA256SUMS`
(SHA-256 `cbe2341808ee8c1320b074ceb4c7ed21ee4f4ba33cfcfdabc5c91ae7e7a1f36e`).
The reviewer audited the underlying full-suite logs and all source/evidence hashes;
they did not claim to rerun every gate independently. No allowance or protected
workflow/gate path changes; neither acknowledgment label is indicated.

Hosted results must be checked on the exact PR head; the PR records those results
without presenting older local-only ledger status as current hosted acceptance.
This slice does not close #45 or CR-17 and provides no general durable command IDs,
deduplication, durable notification delivery or exactly-once retries.


A fresh complete final-source `GB_LONG_TESTS=1` race run after review passed
**5089 nodes, zero skips/failures**, game **392.852s / 600s** (34.5% headroom).
All Go hashes remained unchanged throughout. The final test-only output assertion
is included in this fresh run. All nine new SIGKILL boundaries and existing suites
remain present. Source, output and independent-review hashes are recorded in the
[supplemental delivery ledger](evidence/cr17c-stockpile-delivery-validation.json).
The earlier ledger remains the unaltered historical local-only record.
