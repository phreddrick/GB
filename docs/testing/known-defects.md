# Known-defect inventory and parity reporting

Policy for `test/knowndefects.yaml` (ADR-0003; migration plan section 17.2).

## Why an inventory instead of `t.Skip`

A skipped test prints `ok` and hides the defect. The inventory keeps every test
running and classifies **each individual case** (one fixture row and output
column, never a whole category):

| Result | Meaning | Effect on the build |
|---|---|---|
| **PASS** | the case matches the reference and is not listed as a divergent case | none |
| **FAIL** | a new mismatch (case not listed), **or** a listed case whose Go value is no longer the recorded one (the divergence itself changed) | fails |
| **XFAIL** | the case differs from the reference and the Go value equals the one recorded in the entry's expected file | passes, counted and logged with the finding id |
| **XPASS** | the case matches the reference although the expected file lists it as divergent | **fails**: remove the row (and the entry when empty) so the inventory stays true |
| **STALE** | an inventory entry names a test that never checked it, or an expected row whose case key no longer exists in the fixture | fails |
| **UNTESTED** | an inventory entry explicitly marked `untested: true` (no test can exercise it yet) | logged in the summary; must name the phase that adds the test |

Expected divergences are recorded **per case** in `test/golden/expected/<name>.csv`
(`case,go`: the case key built from the row's input columns, and the Go value
the case currently produces). This is what makes the classification honest: a
category with sixteen documented divergent rows cannot absorb a seventeenth,
and a documented row whose Go value drifts is a failure, not an expected one.

The mechanism is `go/internal/testutil` (`CheckCase`, `CaseKey`, `Finish`, `ValidatePackageInventory`).

### Ownership and validation of every entry

Every tested entry names the Go package that owns it (`package:`). Two validators make sure no entry evades checking:

- **Static, repository-wide** (`internal/testutil`, `TestInventoryStatic`): for every entry, regardless of which tests ran, the package directory must exist, `go list -json -test` must select at least one test file for it on this platform, the named top-level test function must be defined in one of the selected files, a selected file must contain a `TestMain` that calls `testutil.ValidatePackageInventory("<package>", ...)`, and the expected file must exist and parse. Ignored (platform-excluded) test files are printed so exclusions stay visible. An entry naming `TestDoesNotExist`, or moved to a package without a validator, fails here.
- **Dynamic, per package** (`TestMain` after `m.Run()`): each owned entry's test must exist in the test files Go selected for this platform (`go list -json -test`, so a Windows-only file does not count on Linux), must have run to completion in this process, must have checked the entry, and must have seen every expected row; ids checked in the package must be owned by it. On success, when `GB_INVENTORY_RECORDS` names a directory, the validator writes a completion record (package, GOOS/GOARCH, validated ids, finished tests). With a `-run` filter the dynamic validation is skipped with a visible notice (CI runs full packages).
- **Reconciliation after the run** (`test/scripts/inventory-check.sh`, `go/tools/inventorycheck`, used by CI): every tested entry must have a completion record from its owning package produced on this platform in this run, listing the entry as validated and its test as finished; packages with no selected test files, Windows-only test files on Linux, removed test files and missing validators all fail here, because evidence of execution comes from the records, not from reading source text. The static check above uses the same `go list` selection, so a package reporting `[no test files]` fails both.

Explicit `untested: true` entries must not name a test or an expected file; they are printed as `UNTESTED (explicit)` by both validators so they stay visible, and a missing test is never classified as untested implicitly. The
summary printed by the tests, and written to `$GB_TEST_SUMMARY` for the CI job
summary, has two parts: the **individual-case** tally
(`PASS=n XFAIL=n XPASS=n FAIL=n`) and a **per-category** breakdown; the two are
labelled so they are not confused.

### Recording or updating expected divergences

`cd go && go test ./internal/game -run 'TestGolden|TestCEWCallSite' -args -golden.update-expected`
rewrites the expected files from the currently observed divergent values for
the entries in the inventory, then fails deliberately so an update run can
never be mistaken for a green run. Review the CSV diff: every added row is a
new allowance and needs the `known-defect-added` approval (below); removed rows
are fixes.

## Entry format

```yaml
- id: golden/planet_guns                           # unique; the CheckCase id used by the test
  test: TestGolden/planet_guns                     # Go test (top-level name must exist), or `untested: true`
  package: internal/game                           # the Go package whose tests own and validate the entry
  expected: test/golden/expected/planet_guns.csv   # the divergent cases and their current Go values
  finding: CR-26                                   # code-review id, or GD-nn (see below)
  reason: one line
  phase_to_fix: 3c
  since: 2026-10-28
```

The file is a restricted YAML subset (a list of flat mappings, plain scalars,
full-line comments) parsed strictly by `testutil`: unknown keys, duplicates and
missing fields are errors. It stays valid YAML for other tools.

## Adding or changing an allowance: trusted approval

Changes to the allowance files (`test/knowndefects.yaml`, `test/golden/expected/*.csv`, `docs/testing/lint-baseline.txt`) are gated by `test/scripts/growth-gate.sh` (`go/tools/growthgate`), which parses both revisions canonically and classifies every semantic difference:

| Class | Examples | Approval |
|---|---|---|
| growth | new inventory entry; new expected case; new baseline line | required |
| modification | an existing entry's test, package, expected file, finding, reason, phase or tested/untested status changed; an existing expected case's Go value changed (for example `points=5` from 1 to 2) | required |
| reduction | entry, case or baseline line removed | not required |

**The only acknowledgment signal is a pull-request label applied by the repository's sole maintainer**: `known-defect-added` for allowance changes and `gate-change-reviewed` for changes to the gate tooling itself (`.github/`, `go/tools/`, `go/internal/testutil/`, `test/scripts/`, `test/ctool/`, `go/.golangci.yml`). These labels are **sole-maintainer acknowledgments**, not independently verified approvals: `phreddrick/GB` has one maintainer and committer, GitHub does not let an author approve their own pull request, and no second account exists or is required. Independence in this project comes from the workflow around the label, not from the label: Claude implements, Codex reviews independently, the owner reads the gate's itemised list of additions and modifications and applies the label as an explicit acknowledgment. Commit messages, trailers, branch names and pull-request text are never consulted.

How CI evaluates it: **on pull requests only.** `pull_request` events (`opened`, `synchronize`, `reopened`, `labeled`, `unlabeled`, so adding or removing a label re-runs the gate) pass the exact label names from the event payload to `test/scripts/growth-gate.sh` as a JSON array; matching is exact and case-sensitive, a partially matching or combined label name does not count, and a missing or malformed pull-request context fails closed. No `pull_request_target` is used, so untrusted code never runs with write permissions. **On pushes to `master`** the workflow runs the technical validation (build, vet, tests, race tests, golden and mutation checks, inventory reconciliation, lint gate, govulncheck, Docker build) but the acknowledgment gate is skipped: it performs no label lookups and no API calls, and it neither authorizes nor modifies any baseline. Post-merge push checks therefore validate the code; they do not independently authorize baseline changes. Whether anything can reach `master` without a pull request depends on branch protection, which is UNVERIFIED (below). Reductions never need a label.

### Repository settings the policy relies on (status: UNVERIFIED)

The owner's intended protection for `master` in `phreddrick/GB`:

| Setting | Intended value | Status |
|---|---|---|
| Require a pull request before merging | on | **UNVERIFIED** (no `gh` CLI or API access from igor; not modified by this work) |
| Require status checks to pass: `ci / build, test, lint (fresh checkout)` and `ci / docker build from checkout (no push)` | on | **UNVERIFIED** |
| Required approving reviews | **0** (single maintainer; self-approval is impossible on GitHub) | n/a by policy |
| Require review from Code Owners | off (`.github/CODEOWNERS` is informational ownership documentation) | n/a by policy |
| Allow force pushes; allow deletions; bypass for administrators | off | **UNVERIFIED** |
| Labels `known-defect-added` and `gate-change-reviewed` exist and are applied only by the maintainer | yes | **UNVERIFIED** |

Owner actions required before the first merge: create the two labels; enable the branch protection or ruleset above; then open the Phase 0 pull request and apply both labels after reading the gate's itemised output. Until the protection is confirmed, this documentation does not claim that direct pushes are prevented; the pull-request gate is the enforcement point.

Findings not in `docs/architecture/go-code-review.md` get an id `GD-nn` ("golden defect") and a paragraph below.

## Removing an entry

When the fix lands, the test turns XPASS and fails until the entry is removed in
the same pull request. That is the intended workflow.

## Golden defects not in the code review (GD-nn)

Found by the Phase 0 golden tests on 2026-10-28; neither is in `go-code-review.md`.

- **GD-01, compatibility temperature truncation.** `source/max.c` declares `reg int i,add;` and computes `add = 0.1*((double)planet->conditions[TEMP]-race->conditions[TEMP]);`, so the temperature term is truncated to an integer before `abs(add)/100.0`. `go/internal/game/planet.go` (`compatibility`) keeps `0.1 * diff` as a float. 47 of 60 fixture rows differ (relative differences up to about 1%); the rows that agree are those where the temperature difference is a multiple of 10. Fix in Phase 3c under the golden test; whether to keep C's truncation is a rules question, so it is also listed for ADR-0012.
- **GD-02, morale_factor constant.** `source/shlmisc.c` line 647: `atan(x/10000.)/3.14159565+.5`; the divisor is not pi. `go/internal/game/combat.go` (`moraleFactor`) uses `math.Pi`. Every ground-attack strength differs by roughly 2e-6 relative. This is almost certainly a typo in the original. The owner's provisional decision (Phase 0 review, 2026-10-28) is to **keep Go's `math.Pi`** and record the intentional divergence for confirmation in Phase 3 (ADR-0012, Decision 10); until that confirmation the golden tests keep reporting the 80 affected cases as XFAIL with their recorded values, so the divergence stays visible and cannot drift.

## Current inventory (2026-10-28)

| id | finding | test | expected cases | fix phase |
|---|---|---|---|---|
| `golden/planet_guns` | CR-26 | `TestGolden/planet_guns` | 16 of 19 | 3c |
| `combat/cew-strength-multiplier` | CR-27 | `TestCEWCallSite` (deterministic call-site test in radiation mode; reference is the C formula `strength*CEW_hit`) | 3 of 4 | 3c |
| `golden/compatibility` | GD-01 | `TestGolden/compatibility` | 47 of 60 | 3c |
| `golden/ground_attack/astrength`, `/dstrength` | GD-02 | `TestGolden/ground_attack` | 40 of 40 each | 3d (provisional owner decision: keep `math.Pi`, record the intentional divergence in ADR-0012) |

## Parity-gap report

The command-inventory report (`present | classified | missing` for each of the
109 C commands) is generated from the declarative command table that Phase 4b
introduces; until then the list in `go-code-review.md` section 4.1 (45 missing
commands) is the reference.
