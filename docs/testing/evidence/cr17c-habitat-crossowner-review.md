# Independent review: original-C cross-owner habitat activation

Reviewer: `/root/habitat_independent_review`, separate from the correcting
implementation author. Explicit read-only noninteractive task, max eight minutes
per review turn. No existing worktree or index edits permitted. Baseline inspected:
5ebe816dac788ee150a0c5ace20260aa492f722a on feature/cr17c-habitat-factory.

## Initial source investigation (before correction)

Original C `source/order.c:42-71` authorizes the selected factory through the
ship selector and governor checks. `order.c:524-551` then obtains its containing
ship via destshipno, requires a habitat, and debits that habitat's resource and
hanger; it does not compare the habitat's owner or governor with the caller.
The maintainer explicitly requires restoring that behavior. The earlier
same-owner tightening is not a requirement and must not be retained.

Exactly two production carrier-owner restrictions were identified:
`game.habitatStage` and `db.prepareHabitatActivation`. The shared selector's
factory-owner check is distinct and must remain; it must not be removed as
part of this compatibility correction. `PlanHabitatActivation`, private workspace
capture, recursive location/membership checks, full preimage checks and closed
record writes do not otherwise depend on carrier ownership. The actor operation
reads the caller's race/governor authority, not the carrier owner's race; no
carrier-race mutation or attribute lookup requires a new record participant.

Required test concerns sent to the parent:
- Positive cross-owner actor/store persistence and real turn/restart fixtures
  should contain a real second race, rather than merely mark a habitat owner=2
  while no race2 exists.
- The containing habitat is the resource payer. Factory owner's race/planet
  stockpiles and other participants must remain unchanged except legitimate
  later turn effects.
- The inverse case (caller owns habitat but another player owns factory) must
  still be rejected at both actor dispatch and DB validation. Factory governor
  authority remains mandatory; foreign habitat governor authority is not added.
- Full immutable foreign-carrier, sibling and root-chain preimages must remain
  in the same write transaction; stale carrier ownership/resource/hanger changes
  must fail closed without overwriting the changed record.
- Extend known abort, confirmed commit, uncertainty and real SIGKILL scenarios
  to both carrier ownership cases; factory/carrier installation, delayed output,
  no stale shutdown flush, fence and allocator preservation must remain intact.

No test execution or approval is claimed in this initial source investigation.
Final correction review and independent bounded execution await the parent.

## Correcting the post-turn acceptance assumption

The first independent race run exited 1 and is retained as reviewer-race.json/
.err/.exit. It had {'pass': 258, 'fail': 2} test outcomes and 12 successful actual SIGKILL cases. Its failing test nodes were TestCR17CHabitatCompleteStateAndBoundaries/carrier-owner-2, TestCR17CHabitatCompleteStateAndBoundaries. The newly introduced claim that carrier ownership stays unchanged after a normal turn was false; the failed run is not represented as a pass.

Independent source inspection confirms Go ship.go:306-312 changes a containing carrier owner/governor to the child owner/governor. C doship.c:112-118 has that isolated synchronization, but C doturn.c:202-206 first calls doown (doship.c:223-232) to propagate parent ownership down. Go defines doOwn but its turn driver does not call it, a pre-existing CR-13 finding. Entire-turn C parity must not be claimed. The narrowly corrected test now proves original foreign owner2 survives activation and a restart BEFORE any turn, then explicitly asserts existing Go owner synchronization and unchanged Race fields after turns. Runtime turn code is unchanged. This does not restore the rejected same-owner activation restriction.

## Completed independent code review of cross-owner correction

Read the complete five-file working diff. Production changes remove only the
actor and DB carrier-owner equality checks and correct their comments. Factory
owner/governor/race authority, alive/active/type, Docked/WhatDest, requested-ID,
parent membership, location and full persisted carrier preimages remain required.
The pure cost/hanger calculation, all transaction writes/preimage checks,
write-fence and uncertainty guards, private actor workspace, coherent installation
and delayed success output are unchanged. No alternative writer, AP, allocator,
schema, commit kind, deduplication or recovery policy is introduced.

The tests now construct a real second enrolled empire for foreign habitats and
exercise both owners through complete record checks, stable actor identities,
restart before any turn, segments, update and later restart. The actual foreign
habitat pays the resource cost; full proposals preserve other record fields and
ownership until normal turn processing. Dedicated actor and DB inverse cases
prove owning the habitat grants no authority over the other player's factory.
Factory-authorized active governor coverage now uses a foreign habitat. Stale
foreign owner/resource changes after proposal preparation reject the transaction,
preserve the changed store and original actor and prevent shutdown flushing.
Known-abort/error/panic, confirmed-commit/reporting-error, uncertain write and
write-fence assertions now run for both carrier owners. All six real SIGKILL
boundaries run for each owner, retaining complete old/new record, auxiliary,
allocator, orbit, startup, segment and update assertions.

Only the obsolete owner-equality negative cases are replaced by positive and
inverse-authority coverage. Original transaction and safety assertions are not
weakened. The incorrect newly added post-turn ownership invariant was corrected
against independently verified unchanged turn code, with the failed attempt
preserved above. The existing CR-13 doOwn omission must remain explicitly
identified; no complete C turn parity or preservation of foreign owner2 after a
turn may be claimed.

Independent final command:
`timeout 240s env GOTOOLCHAIN=local GOCACHE=/tmp/cr17c-habitat-cache
TMPDIR=/tmp/GB-cr17c-habitat/test/ctool/obj/tmp
/tmp/gb-go1274-evidence/go/bin/go test -count=1 -race -json
./internal/commandrules ./internal/db ./internal/game -run Habitat`

Result: **exit 0, 260 passing test nodes / 32 top-level entries, zero failures or
skips, no DATA RACE warning; all 12 actual SIGKILL cases passed**. Log:
`/tmp/cr17c-habitat-crossowner/reviewer-final-race.json`, SHA-256
`2326a3f3ca13d7a1537b285bc609b52c1371a8bbbe99cab63145149faa1fc8b0`.
The final five source hashes in reviewer-source-final.sha256 were checked again
after the run and all match current files. Final normal/full-race/gate results
and updated implementation documentation are the parent's separate pending work;
they are not claimed as this reviewer's execution.

**CODE REVIEW VERDICT: APPROVE for the maintainer-requested restoration of
original-C cross-owner habitat activation.** No remaining blocking activation,
atomic-persistence, factory-authority, stale-preimage, output or halt defect was
identified. The earlier same-owner tightening is explicitly superseded; it must
not be presented as a requirement or restored as a workaround. Documentation
must accurately disclose the separate pre-existing CR-13 turn limitation.
Full final evidence/document audit remains pending the parent's completed files.
No worktree/index, labels, remote refs or live systems were changed by reviewer.

Final reviewed source SHA-256 values:
- d9973bda3fcdf3188957f46b12c40fecd596ee1c28eb88f5edba2dc7e645c566  go/internal/db/habitat.go
- 86ce3fdddbf264a6e22378831e6749bfd26f67cf2e93dba0ec4a8459335113a7  go/internal/db/habitat_test.go
- 5555ccab3cda9419ba8ab28654fa57b6403ad7c5096a0bb4b2d264f5b7cd719e  go/internal/game/habitat_command.go
- d5eacb088f7fd637de84a6143d3e7d3042da5d8e3a39568cdcb497662853b92a  go/internal/game/habitat_crash_test.go
- 483599ea2cc1604a5ad087531fd0861b11e3f8c9584425f5192e6f54e3f44b76  go/internal/game/habitat_staging_test.go

## Final documentation and execution-evidence audit

This follow-up was an explicit read-only noninteractive evidence audit bounded
to six minutes. The reviewer read the completed working implementation record,
updated architecture note, new cross-owner execution ledger and actual raw logs
and result files under `/tmp/cr17c-habitat-crossowner`. It did not relabel a
parent-executed full suite as the reviewer's own execution: the reviewer's own
two bounded race runs and their actual outcomes are recorded above.

Independently computed and checked:

- All **11 source/baseline SHA-256 values** match current files, including all
  five corrected Go files. The pure rules, shared selector and other surrounding
  production source remain unchanged.
- All **7 preserved evidence-file hashes** match both current files and their
  actual `HEAD:...` Git blobs. Earlier Claude/Codex ledgers, reviews and pre-fix
  artifacts were preserved, not rewritten to imply the old owner rule was C.
- stdout/stderr hashes and recorded result-file exit statuses match for all
  **13 successful checks**, the **3 retained expected/diagnostic failed runs**,
  and both independently executed reviewer race runs. Every run that was
  expected to fail remains visibly recorded as exit 1.
- Parsing actual full normal JSON yields **3951 passing nodes, 2 existing opt-in
  skips, 419 top-level passes, 21 passing tested packages**.
- Parsing actual full `GB_LONG_TESTS=1` race JSON yields **3953 passing nodes,
  zero test skips/failures, 421 top-level passes, 21 passing tested packages**.
  No DATA RACE warning is present in either successful full-suite log.
- Both full suites, corrected targeted run and final reviewer race run have
  **260 habitat passing nodes / 32 top-level entries**, with **12 actual SIGKILL
  leaf cases**, six for carrier owner1 and six for carrier owner2. The exact
  leaf names, statuses and all other ledger counters match raw JSON.
- Raw gate outputs corroborate all 15 golden mutation outcomes, fixture
  reproduction, 1248 PASS / 143 XFAIL / 0 XPASS / 0 FAIL parity cases, inventory,
  lint 1000 baseline/report/matched with 0 new/stale, growth with labels=[] and
  no protected-tooling delta, gate self-tests, build/vet/formatting, module
  integrity/tidy, 21 valid offline manifest resources, 188 workflow-helper tests
  and 17 workflow-policy checks. Strict govulncheck reports zero reachable
  vulnerabilities and retains the 17 non-reachable module findings.
- Fresh working and staged whitespace checks pass. The follow-up changes no
  protected tooling, baseline, original C, schema or CR-18 commit kinds.

The active documentation now accurately states ownership-independent carrier
participation and factory-scoped authorization, full foreign-carrier preimages,
resource/hangar payer, inverse authorization tests, complete abort/commit/
uncertainty and double-owner crash evidence. Old execution counts and reviews
remain historical and the fresh follow-up clearly supersedes the rejected
same-owner policy. The architecture and implementation records identify the
pre-existing CR-13 omission precisely: cross-owner activation and pre-turn
restart preserve carrier owner2; later current-Go turn processing may synchronize
its Owner/Governor from the child, unlike the full C sequence with doown first.
No whole-turn C parity, ownership-repair feature or closure of CR-13/11/17/20 is
claimed. No new blocking documentation or execution-evidence discrepancy found.

**FINAL INDEPENDENT VERDICT: APPROVE** for this maintainer-directed cross-owner
activation correction in PR #73. The review approves original-C activation
semantics, not the previously rejected same-owner restriction. All selected
factory authority, linked-record participation, atomic persistence, installation,
output, fence, failure and shutdown safeguards remain intact. No remaining
blocking activation defect was identified after final validation.

Normal maintainer review, the existing maintainer-applied `known-defect-added`
condition for the original five lint removals, and hosted CI remain delivery
conditions. No new `gate-change-reviewed` requirement arises from this follow-up.
This is not authorization to merge/deploy, operate the running game, fix the
broader turn architecture or close CR-13/11/17/20. No source, index, branch,
remote ref, PR, label, worktree or live system was changed by the reviewer.

The parent may copy this complete report to the new cross-owner evidence file
and record its hash in the ledger. This report intentionally does not record
that ledger's evolving hash, preventing a circular dependency.
