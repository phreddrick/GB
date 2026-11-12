This record preserves the independent agent review rounds and their original
execution history. Later dispositions supersede earlier conditional verdicts.


---

# Independent cargo implementation review — round 1

Reviewer: /root/cargo_independent_review; noninteractive, read-only source/test
review at /tmp/GB-cr17c-cargo. Baseline5c3c891. No worktree source/index/branch,
other session or live system was changed. Private overlays/logs only.

Owner-approved dispositions, as relayed by the parent: original-C zero AP;
retain Go's intended containing-carrier aggregate-mass exemption and document
C's Boolean-argument typo. C127 crystal capacity and functioning & alias are
explicitly documented behavior corrections. No-wrap rejection is required.

## Findings

### R1 — MUST FIX: irrelevant planet number rejects legitimate star peers

`db/cargo.go:cargoLocation` compares PNumOrbits for every non-Univ external peer,
including LevelStar. At star scope that field is stale/irrelevant. Shared
`telescopeContext.location` correctly checks only star number for star chains.
Existing `doDockOne` does not normalize planet numbers when two valid same-star
ships dock. C load's peer condition also does not compare those irrelevant fields.

A private Go overlay using real actor dispatch, a valid reciprocal same-star
chain and selected.PNum0/partner.PNum1 failed: ModeHALTED, NOT_COMMITTED, exit1,
0.130s. There was no partial write, but a supported legitimate command halted the
engine because of an artificial location constraint. Compare star number only
for LevelStar, star+planet for LevelPlan, neither for LevelUniv; add a permanent
positive actor and direct-contract test. This gameplay/availability defect must
be corrected before approval of the full requested star-peer matrix.

Evidence: reviewer-star-peer-before.log/exit and reviewer-star-peer-overlay.json,
reviewer-star-peer_test.go plus immutable captured reviewer-cargo-strict-star-pnum.go.
No existing source file was modified.

### R2 — test causality gap: coherent source authority

Initially direct DB foreign-source tests mutated only expected Owner, so removing
authority still failed diff whitelist. First new coherent tests also left cargo
unchanged / requested forbidden foreign loading, masking missing authority.
The parent corrected them to otherwise-valid foreign-selected resource gifting,
and active callerGov1/selectedGov2 with permitted cargo/mass deltas.

I independently removed only the owner/governor DB predicates in a private source
overlay and ran the two targeted tests. Both failed specifically because an
unauthorized command COMMITTED (exit1,0.012s). This demonstrates the corrected
regressions are causal. R2 RESOLVED in visible source. No production authority
bypass was found; this finding concerned regression effectiveness.

Evidence: reviewer-permission-counterexample.log/exit, reviewer-permission-overlay.json.

### R3 — copy/paste diagnostic

The initial pre-persistence ownership failure logged habitat factory identity
rather than cargo. Parent reports correction; this is operator diagnostic
precision, not a transactional defect. RESOLVED subject to final source audit.

## Assessment of the fifteen requested blocking areas

1. Mutations are exactly selected ship and one recipient ship/planet. Pure diff
   recomputation closes cargo/mass writes, preserves every unrelated field, and
   all full expected bytes are checked inside one bbolt Update before two Puts.
2. AP is zero per owner-approved C behavior, not an omitted Go debit. Root star/
   StarData context remains immutable. No AP writer or third Put is invented.
3. Effective signed amount, negative reversal, range-before-default ordering,
   load/unload omitted0 and VN early-negative/default behavior follow C. Strict
   bounded malformed number/extra arg rejection avoids atoi/INT_MIN UB.
4. uint16 result checks prevent stockpile/shuttle wrap; signed wide differences
   preserve capacity behavior; source/recipient deltas conserve quantities.
5. Resource gifts to foreign partner remain allowed, positive taking restricted;
   crystal nonzero transfers both directions restricted; no partner-governor
   permission is added. Factory effective capacity, C127 and shuttle exceptions
   are handled by one pure helper recomputed by DB.
6. Private full record copies, owns before and after commit, pointer-preserving
   assignments with no callback between selected/recipient installation. Full
   actor/store snapshots and pointer assertions cover coherence.
7. Both peer/contained/root chains and caller/scope context are captured; cycles,
   missing/dead/duplicate records reject. See R1 on over-validation of star PNum.
8. Parsed positive uint32 requested ID feeds staging; map-key/Number mismatch
   fails workspace identity. Wildcards/type/mixed selectors refuse before loops.
9. doLoad itself intercepts every r/x/&-prefixed token before legacy mutation;
   malformed aliases cannot fall through. Unsupported commodities remain legacy.
10. Active targeted transporter selected ships are refused before mutation;
    invalid/dead/nonreciprocal partners never run legacy undock. No news,
    telegram, third ship or allocator effect enters this operation.
11. Bounded caller text is buffered until confirmed persistence and installation.
    Errors before commit release no success; confirmed reporting/delivery errors
    preserve committed installed state and HALT without rollback claims.
12. Phase classifier, fence accounting, shared uncertainty guard and diagnostic
    readback preserve abort/commit/uncertain semantics. Maintenance record is
    checked/strictly decoded inside transaction. No automatic repair/retry/reopen.
    Twelve actual SIGKILL leaves test planet and contained partner modes at six
    boundaries each, checking complete old/new world and subsequent turns.
13. Actor planet replaces no independent GetPlanet copy. CR-11 reload reads new
    ship records; original regression now asserts later selected cargo/coherence
    and crash tests assert recipient as well. Existing CR-13/17A AP effects remain
    open; no entire turn/game guarantee is inferred.
14. Parent is updating stale-world regression setup to unsupported legacy fuel
    rather than deleting HALT/no-flush assertions after resource path becomes
    safe. Full-suite compatibility and final evidence remain pending.
15. Preserved pre-fix artifacts show real dispatcher split writes with one
    package-private post-Put hook; source mechanism is genuine. This reviewer
    decoded/sanitized them but has not claimed a fresh original-baseline rerun.
    Independent permission mutation tests above prove causal authority coverage.

## Fresh independent execution

Ran `timeout 240s env GOTOOLCHAIN=local GOCACHE=/tmp/cr17c-habitat-cache
TMPDIR=/tmp/GB-cr17c-cargo/test/ctool/obj/tmp
/tmp/gb-go1274-evidence/go/bin/go test -count=1 -race -json
./internal/commandrules ./internal/db ./internal/game -run Cargo`.

Exit0; 283 passing nodes /15 top-level entries, no failures/skips; all12 actual
SIGKILL leaves passed. SHA-256 of reviewer-initial-race.json:
c88c5e0a04741588f9c1650da47cc875fec58876be82cab2230ac155b4db6038.
Source hash capture initially used a wrong relative path and was immediately
repeated from repository root; it is not represented as an immutable pre-launch
snapshot. The run compiled before later authority/queue test additions and
minor review corrections, so it is NOT final-source acceptance.

## Round-1 verdict

CHANGES REQUIRED for R1 plus final-source/final-evidence validation. No partial
cargo write, preimage bypass, unauthorized partner policy, premature success or
uncertain-state stale-flush defect was found in reviewed transaction source.
R2 is independently demonstrated resolved; R3 is a minor correction. Approval
must follow correction of the supported star-peer behavior and fresh review.


# Follow-up independent code/test review — R1 corrected

The parent corrected `cargoLocation`: external star peers compare only star
number, planet peers compare star+planet, universe peers compare neither. For
contained ships, the actual root is walked through the already-validated parent
chain; meaningful coordinates are checked at that root level. This does not
introduce a nil/cycle hazard: preceding `ctx.location` has already proved those
parents exist, are alive and terminate in a valid root, with no intervening
callback or mutation. The permanent star-peer test explicitly uses differing
irrelevant planet numbers. Parent's fresh before/after execution records exit1
then exit0; this reviewer also independently reproduced the original check's
HALT in a private source overlay. **R1 RESOLVED.**

The coherent source-owner/governor negatives now propose otherwise legal cargo
and mass effects. Foreign selected uses a permitted resource gift (instead of
an independently forbidden foreign taking); selected-governor mismatch uses
legal same-owner cargo. Independently deleting only those predicates in a Go
source overlay makes both targeted tests fail with unauthorized COMMITTED
outcomes. Parent additionally recorded one-predicate-at-a-time counterexamples.
**R2 RESOLVED causally, not merely by a generic invalid request.** The diagnostic
now names cargo identity correctly; **R3 RESOLVED.**

Four existing stale-world tests now deliberately use still-legacy fuel with
valid stock/capacity instead of newly corrected resources. I reviewed the exact
diffs: original HALT, no-overwrite, no actor install and no stale shutdown flush
assertions remain. This preserves the safety regression while avoiding a false
expectation that fixed resource loading still corrupts state. New positive
cargo -> landed factory -> enrollment proves coherent immediate world state and
no newly introduced AP drift. CR-17A's old volatile AP safeguard remains intact.
Queued cancellation, operation-after-turn, HALTED admission and admitted
shutdown/quiesce/started-cancel tests use the actor and deterministic barriers.
No protocol, public route, actor loop or broad command rewrite was introduced.

## Fresh final-source independent execution

Before launching, captured eleven relevant source/test/hook SHA-256 values in
`reviewer-final-source.sha256`, from the correct repository-root directory.
All eleven still matched after the run. Executed:

`timeout 240s env GOTOOLCHAIN=local GOCACHE=/tmp/cr17c-habitat-cache
TMPDIR=/tmp/GB-cr17c-cargo/test/ctool/obj/tmp
/tmp/gb-go1274-evidence/go/bin/go test -count=1 -race -json
./internal/commandrules ./internal/db ./internal/game -run Cargo`

**Exit0,297 passing nodes,19 top-level entries,zero failures/skips; all12 real
SIGKILL leaves pass.** Package times: commandrules1.108s, db7.733s,
game145.146s. `reviewer-final-race.err` is empty and no race-warning pattern
occurs in JSON output; crash harness separately fails on child race warnings.

Final log `reviewer-final-race.json` SHA-256:
0ce5482c87090918e89dee2e7797d030396c5fc545c7ce5197496744f62842d2.

Independent private-overlay causal logs:
- `reviewer-permission-counterexample.log` (exit1, both unauthorized COMMITTED):
  2bf98092027e5d4f3fe957418f2b9583956943b64c8b67dc26925d4e69cf989a
- `reviewer-star-peer-before.log` (exit1, valid star peers HALTED):
  441af83932aaf598572794a8470dd84d5a4eb029014fd35fe1134e08cd44ee98

These overlays and review files did not alter source, index or any worktree.
All fifteen code/test blocking questions from the round-1 assessment are now
satisfied for the bounded operation, including R1's corrected meaningful-field
validation. No remaining partial-write, AP mutation, foreign-policy, authority,
transporter escape, premature success, installation or uncertainty-flush defect
was found.

## Final code review verdict

**APPROVE CODE for normal PR review of the exact resource/crystal slice.**
Full repository validation, accurate final evidence/docs and hosted CI remain
conditions. The parent reported one initial full-race admin HTTP500ms timeout
under concurrent workload; that failure must remain visible, and no assertion
or deadline should be weakened to hide it. This reviewer has not substituted
its focused297-node pass for a full-suite/compatibility pass or a final evidence
audit that is still pending. This verdict does not approve merge/deployment,
close CR-11/13/17/20, claim generic command durability/exactly-once, or authorize
crew/fuel/destruct/transporter/navigation work.

---

# Independent cargo descendant-context follow-up

Read-only follow-up, 2026-11-11. Source inspected: internal/commandrules/cargo.go, internal/db/cargo.go and telescope.go context traversal, internal/game/cargo_command.go and telescope_command.go, and ship.go doMass. No source/index/worktree changes made.

No additional blocking dependency found. Cargo arithmetic reads selected/partner stored fields and the validated containing relationship. It does not read, rewrite, sum, or recalculate unrelated descendants. Resource mass changes use exact commodity deltas; crystals do not change mass; containing-carrier aggregate exemption is explicitly owner-approved. Full participating ship preimages include Ships head and Mass. All containing/root lists used to prove selected/partner residency and the recursive parent chains are witnessed.

Unrelated descendant subtrees are not required transaction inputs merely because a participating ship has children. A concurrent child change leaving parent stored Mass/head unchanged would indicate an existing global mass/graph inconsistency; this operation does not overwrite that child or claim to repair that inconsistency. CR-11/CR-13 remain open. Documentation must distinguish complete cargo-relevant membership witnesses from a whole-world descendant audit. A representative unaffected-child preservation test would strengthen evidence without changing this dependency conclusion.

No tests were executed for this follow-up. Earlier final-source focused race results remain separately documented in reviewer-code-final.md; complete acceptance evidence audit remains pending.

---

# Independent cargo evidence audit (bounded interim)

2026-11-11. Read-only source, test and evidence review. No source, index or working-tree changes. This audits actual recorded outputs, not just author assertions.

- Normal suite independently parsed: 4248 passing test nodes, zero failures; only existing opt-in long-test nodes in net/e2e skipped (2).
- Focused final normal:337 pass. Cargo test family total297 passing nodes, including72 gameplay matrix leaves,12 permanent original-defect leaves,12 real SIGKILL leaves. Independent final-source focused race also passed297 nodes, separately recorded in reviewer-code-final.md.
- Initial full long race:4248 pass, failure limited to existing admin TestPrivateEnrollmentTimeoutAndUncertainty/after-commit and its parent (500ms response deadline under concurrent check workload). Preserve failure evidence; do not relabel this run PASS. Exact admin race rerun result exit0. Complete serial-package full long race remains running at audit time; acceptance must await its result.
- All15 golden mutations show the expected success/failure behavior, including unchanged-tree reconciliation. Lint baseline1000/current1000/matched1000/new0/stale0. Growth confirms no protected gate/allowance delta. Govulncheck:0 reachable code vulnerabilities;17 noncalled required-module advisories are reported, not suppressed. C fixture reproduction exact. Workflow policy17/17; offline manifests21 valid/0 invalid/errors/skipped. Remaining supplied gate results below all exit0 except preserved original full-race run.
- All15 source hashes in reviewed-source-sha256.json match current bytes. No source changes after independent focused-race approval.
- Evidence archive checks: hook.patch.gz647bytes SHA2560caa2b077b54f76e41744afc3b69038271fd0e86905ca64a0479b98a8e37fc79; decompressed1591bytes SHA256ce1929d5983819c5244c5a436be09e17b6c083325f51246be549b25431ef8c27. Records JSON gzip115646bytes SHA2569015e13f5d025d9d7bf6fe20b191f4754d69b30ca8c632ee442bc6a5291b1455; raw6389297bytes SHA256fc2616fbef1da79819b026d248082700d2018e4a8192d6802174c3a6585e9ce4. Archive contains12 cases and no password/credential/token/hash property names or bcrypt encodings. Compression preserves historical bytes, not rewritten results.
- Implementation document correctly distinguishes owner-approved zero AP/two writes, C ownership and arithmetic rules/corrections, actor staging, full relevant location/preimage checking,12 process kills, safe uncertainty, unrelated-descendant/global-mass limitation, unsafe deferred commodities, CR17A stockpile AP, no dedup/outbox and no format/commit-kind change. It does not claim disk/fsync failure coverage or complete CR17 closure.

No new blocking source/evidence finding. Final approval is conditional on the clean complete long-race result and a final committed validation/review ledger preserving the historical race timeout. Hosted Docker/scanner/kind checks remain hosted CI obligations. No maintainer-label delta found: protected tooling and lint baseline unchanged.

| Result ledger | Exit | Seconds | Result JSON SHA256 |
| --- | --- | --- | --- |
| normal | 0 | 87.523 | 921352cdaf1ceed4e8b8251834ce45dbb2c2b9ad63caccdf44d0f457439435b1 |
| long-race | 1 | 574.098 | 5b8c1d01b409a21a06d44deeaf4385069f7ef308a8aa12e8308486e8d802d798 |
| admin-timeout-race | 0 | 23.114 | 4f76152da151e4476be02389080f3b6da6ca3a2be840a77468f17ba8002b14c5 |
| mutations | 0 | 713.901 | ad86a31ed9d98165587addf86f3883b35131573f06f1d7f771bbb4ee8c8b08da |
| lint | 0 | 13.052 | c2fefa6de6f880fffe2d2d038be7a4e99c4f445f4a00fef408b3d845c3eaf9d6 |
| growth | 0 | 0.516 | e868399e28bb9c43792e3d775dd705deb43d41a3be492daab73ac65d4755140b |
| govuln | 0 | 5.011 | 69c61e078d76bb242ca352e8a647a25a8efe4e3cb02bde0c276da827b5d61fb2 |
| inventory | 0 | 88.618 | 703a4bf83dbf22ad3b0e46801fd2aa2cff42c1f4d45d6f81d787711d6555208d |
| c-fixtures | 0 | 1.931 | 18124b4483eed3e3535c7c7deeca35a884c43effb8b31288d79e2c0f95dbb646 |
| workflow | 0 | 33.598 | 54759e36f3eded8a34122b7fe45080d78d4ec43674001995d310953bc2653315 |
| workflow-policy | 0 | 0.167 | d59f44db83614b8b4f3d6ce1d60790648ed4c968485220c5c82b4e51c9dc627d |
| manifests | 0 | 37.703 | 66c43efaaad9c62e9e0d7022727c3345ae087b4bc3e7bad82d87b3c287647ca7 |
| build | 0 | 1.337 | f9d9ca858cdb3dbb7497e4d1c29128395671f922f12fecd230cfec7e920da897 |
| vet | 0 | 1.527 | de232db4ae14e2de6a8eb6c13fb5db46683c4228dbe8e82dfa31b42b1418d3cf |
| format | 0 | 0.219 | c4ad4e92cee826eb2dfb155a3ea3c8661669fd867a89244ea7e2d69156d9fd2c |
| actionlint | 0 | 0.218 | 8bf4bb0e7156c6936c9e9128efff14d85c406ee7aefd5a24f9cf675fa53126ae |
| shellcheck | 0 | 3.118 | 3d619d035e9129382a3b821276fe15c413d82c46366ff8ca9fac0bd3d981040b |
| module-verify | 0 | 0.416 | 3e8745d430059713d06ce1c727772677848a0c1419d464fa5c492df78cd2be15 |
| module-tidy | 0 | 0.065 | 546acb9b6310e2786b30a2ce695f53276a21a5f631e88b5e71a196f2202225b3 |
| gate-approval-selftest | 0 | 5.025 | 14bd3fa4819aabb463f41cb144a481851be131041b273887b319ec886fa5055e |
| lint-gate-selftest | 0 | 28.005 | e007554db8983721c8eb99bb76c773e49265a16db425134d462ae19d220781b3 |
| golden-mutations-selftest | 0 | 133.571 | 2d27c2a98d41798ca90ab00ae16204877af6a391adec3e261c1faa0d80192860 |
| focused-final-normal | 0 | 12.01 | 63c063bb8b90ab87f6965349adec86cfd0df6ca357d8bfcdb5ca5c02169be6ac |
| crash | 0 | 3.775 | 585687563ee8fb7cd0682d9b1bda8043b5831f7828a57f7259c95594787b130d |

---

# Final independent implementation and evidence review: atomic noncombat cargo

Reviewer: /root/cargo_independent_review. Bounded, noninteractive read-only review of /tmp/GB-cr17c-cargo; no repository source/index/working-tree edits or live operations. Source/test review, private mutation overlays, independent focused race execution and actual final evidence inspection were completed in separate recorded rounds. This final disposition supersedes the pending-evidence conditions in earlier preserved review rounds.

## Verdict

**APPROVE for normal PR review: bounded implementation and local acceptance PASS.** No remaining blocking partial-write, conservation, authority, output-timing, uncertainty or stale-flush defect found in the migrated resource/crystal slice. Hosted CI and maintainer PR review remain required; this is not merge/deployment authorization. Broader CR-17, CR-11, CR-13 and CR-20 remain open. The overall work unit can accurately be reported PASS WITH CONDITIONS because unsupported commands, general retry/deduplication guarantees and hosted integration are expressly outside local acceptance.

## Final evidence verified

- Recomputed all15 Go source/test/hook SHA-256 entries in committed validation ledger: every current file matches. Source is unchanged from the reviewed final implementation.
- Recomputed all34 listed stdout/stderr check hashes against private raw logs; every supplied hash matches. Compared recorded commands, exit statuses and elapsed times to the raw result JSONs.
- Independently parsed complete normal output:4248 passing nodes /438 passing top-level tests;2 explicitly documented opt-in long tests skipped.
- Independently parsed complete serial-package GB_LONG_TESTS=1 race output:4250 passing nodes /440 passing top-level tests,0 test skips,21 tested packages,exit0 in760.843s. Six no-test packages are separately distinguished. No DATA RACE warning, empty stderr.
- Initial full parallel long-race failure remains honestly recorded: one existing500ms admin after-commit timeout leaf plus failed parent,4248 passing nodes. Three unchanged targeted race repetitions and the complete long-race rerun passed. No assertion/deadline was loosened. This timing sensitivity is a CI observation risk, not falsely reported as an all-green initial history.
- Independently executed final focused race previously passed297 cargo nodes /19 top-level tests, including12 real SIGKILL leaves. Full normal/focused evidence confirms72 gameplay leaves and12 original-defect permanent regressions.
- All15 golden mutations demonstrate their intended behavior; C fixtures reproduce exactly. Lint baseline/current1000 matched,new0/stale0. Growth finds no protected-tooling/allowance changes. Inventory reconciles. Build/gofmt/vet, gate self-tests, workflow188 tests/17 policy checks, actionlint/shellcheck, offline21 valid manifests and module verification/tidy all pass. Govulncheck reports0 reachable/imported-package vulnerabilities;17 noncalled required-module advisories remain visible rather than suppressed.
- Preserved hook gzip/raw bytes and full six-boundary record archive hashes verified in prior audit, with no credential/token/hash field names or bcrypt encodings in records. Compression is lossless and does not rewrite historical reproduction. Exact baseline-handler and authority-guard deletion controls intentionally fail, proving real regressions.

## Fifteen blocking questions — final disposition

1. **Complete mutation set:** selected ship plus exactly one partner ship or caller planet stockpile; only permitted commodity/mass fields differ. Full preimages and every Put participate in one transaction.
2. **AP:** original-C zero AP is explicit owner approval, not a missing debit. Star/universe AP stays read-only and fully preimage-checked. There is deliberately no fabricated AP write or after-AP crash boundary.
3. **Directions/defaults:** signed int32 bounded parsing, unload negation, negative reversal and range-before-default ordering match defined C behavior, including VN ordering.
4. **Conservation:** wide signed arithmetic and checked uint16 results prevent wrapping/truncation; commodity deltas conserve exact quantities and checked mass follows effective resource deltas.
5. **Foreign ownership:** resource gifting allowed; taking from foreign partner restricted; nonzero foreign crystals restricted both ways. Selected owner/governor is independently enforced. No new partner-owner permission is introduced.
6. **Actor/store coherence:** private proposals and complete preimages, checks before/after persistence, stable-object installation without a callback between assignments. Abort never installs; confirmed commit cannot be flushed away by stale state.
7. **Location witnesses:** root/orbit membership, recursive containing parents and all lists required to establish both participant locations are checked. Unrelated descendants are not arithmetic inputs or writes; no global mass/tree audit is claimed. R1 meaningful-coordinate correction is verified.
8. **Selection:** exact safely parsed positive ship identity and actual map key/Number are checked. Mixed/wildcard/type/overflow cases reject before any legacy prefix operation.
9. **Legacy bypass:** doLoad interception catches supported commodity prefixes even through direct/internal callers; invalid eligible variants cannot fall back to unsafe writes. Other commodities remain explicitly legacy.
10. **Transporter/navigation:** active targeted transporter and broken partner links fail closed; no automatic transporter/undock mutation escapes the transaction.
11. **Output:** bounded success effects release only after confirmed commit and coherent installation. Delivery failures preserve committed state and HALT conservatively. No aborted operation acknowledges success.
12. **Crash/outcomes:** real12-leaf SIGKILL harness proves complete old/new states for planet and contained-ship modes; in-process error/panic/readback tests preserve confirmed-abort/commit/uncertain distinctions. Shared uncertainty guard, CR-18 fence and durable maintenance checks fail closed. No automatic retry/reopen/repair; readback is not fsync proof.
13. **Turns:** successful command and crash recovery tests assert participants across restart, segment and update; no stale actor planet copy is persisted. Existing independent CR-11/13 turn defects are not claimed fixed.
14. **Compatibility:** full final normal/long-race runs include CR-07/17A/17B/factory/habitat/telescope/18/43 and WebSocket coverage. Four older stale-world setups moved to unsupported fuel without weakening their original HALT/no-overwrite/no-stale-flush assertions.
15. **Causal regression:** actual-dispatch original-handler overlay causes all12 permanent atomicity leaves to fail; independently removed owner/governor predicates expose unauthorized COMMITTED outcomes. Original split-write reproduction and fresh fixed-code execution are clearly distinguished.

## Review corrections and limitations

R1 supported same-star peer rejection fixed and permanently reproduced; R2 authority-test causality corrected and independently mutation-verified; R3 copied diagnostic corrected. No unresolved review blocker remains. Source changes do not touch protected gates, defect allowances or lint baseline; CLAUDE.md policy therefore requires neither known-defect-added nor gate-change-reviewed for this diff. No acknowledgment label was applied.

The implementation record and validation/review ledger accurately preserve the initial race timeout and all deliberate negative controls, distinguish process crash from hardware/fsync coverage, preserve owner decisions, and defer unsupported commodities, transporter#42, navigation, allocators, stockpile-transfer AP, global staging, command IDs/dedup/outbox and general client retry contracts. Public signup/authentication and all live systems remain unaffected.
