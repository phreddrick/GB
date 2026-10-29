# ADR-0003: Known-defect inventory and parity-gap reporting; static-analysis baseline

Status: Accepted 2026-10-28 (implemented in Phase 0: `test/knowndefects.yaml`, `go/internal/testutil`, `docs/testing/known-defects.md`, `docs/testing/lint-baseline.md`)

## Context

Phase 0 must establish honest baselines without repairing runtime code: the golden tests fail for known reasons (CR-26 and others), and static analysis reports 1,132 pre-existing findings that live in code later phases rewrite. The usual shortcuts, `t.Skip` for failing tests and global linter exclusions, hide the state of the code and let regressions in unnoticed.

## Decision

1. **Known-defect inventory, per case.** `test/knowndefects.yaml` lists expected failures, each with a unique id, the Go test that exercises it, an **expected-divergence file** (`test/golden/expected/<name>.csv`: the individual cases that diverge, keyed by their input columns, with the Go value each currently produces), a finding id (`CR-nn` from the review or `GD-nn` from `docs/testing/known-defects.md`), a reason, the phase that fixes it and the date added. Tests report every case through `testutil.CheckCase`, which classifies it as PASS, FAIL (a new mismatch, or a documented case whose Go value drifted: fails the build), XFAIL (differs exactly as recorded) or XPASS (matches although recorded as divergent: **fails the build** until the row is removed). `testutil.Finish` fails on stale entries and stale expected rows and prints an individual-case tally and a per-category breakdown. Ownership is explicit: every tested entry names its `package:`; that package's `TestMain` calls `testutil.ValidatePackageInventory` after all tests ran, which fails when a named test does not exist, did not complete, never checked its entry, or left expected rows unseen, and when an id checked there belongs to another package; a repository-wide static test in `internal/testutil` additionally checks every entry's package, test function, validator and expected file regardless of which tests ran. Expected files are rewritten only with the explicit `-golden.update-expected` flag, which itself fails the run so that it is reviewed. Entries that no test can exercise yet are marked `untested: true` and are printed in every summary. Nothing is ever skipped, and no category is ever allowed to fail wholesale.
2. **Parity-gap report.** The command-coverage report (`present | classified | missing` for each of the 109 C commands) is generated from the declarative command table introduced in Phase 4b. Until then the review's section 4.1 is the reference and the inventory covers formula-level parity only.
3. **Static-analysis baseline.** `go build`, `gofmt`, `go vet`, `go test` (with and without `-race`), `go mod verify`/`tidy` and `govulncheck` are strict. `golangci-lint` (errcheck, staticcheck, gosec, govet) produces a JSON report that `tools/lintdiff` validates (whole-document JSON, required sections and linters, no execution error) and compares with the committed baseline `docs/testing/lint-baseline.txt`, keyed by repository-relative file, linter, message, enclosing top-level declaration and normalised source statement (line-number independent, so formatting changes cannot resurrect old findings as new, while the same statement in another function is new); findings absent from the baseline and baseline entries that no longer occur both fail the build. The baseline summary, with the phase that burns each rule down, is `docs/testing/lint-baseline.md`. No `exclude-rules` entries hide anything; the baseline only ever shrinks.
4. **Allowance changes are gated by the sole maintainer's acknowledgment labels.** `test/scripts/growth-gate.sh` (`go/tools/growthgate`) parses the inventory, the expected files and the lint baseline of the base and head revisions and classifies every semantic difference as growth (new entry, case or baseline line), modification (any field of an existing entry, or an expected case's Go value) or reduction. Growth and modification fail the build unless the pull request carries the label `known-defect-added`; changes to the gate tooling need `gate-change-reviewed`. The labels are applied by the repository's only maintainer as explicit acknowledgments of the gate's itemised list; they are not independent reviews (GitHub forbids self-approval, and the project's independence comes from the Claude-implements, Codex-reviews, owner-acknowledges workflow). Label add/remove events re-run CI; matching is exact. Pushes to `master` run technical validation only (the acknowledgment gate is a pull-request control; it performs no API lookups and never authorizes or changes baselines), and the policy that nothing reaches `master` without a pull request is a branch-protection setting the owner configures (UNVERIFIED). Commit messages, trailers, branch names and pull-request text are never an acknowledgment. Reductions need none. `.github/CODEOWNERS` is informational only. The branch-protection settings the policy prefers (pull requests required, status checks required, no required reviews) are documented in `docs/testing/known-defects.md` and marked UNVERIFIED until the owner confirms them.

## Alternatives considered

- `t.Skip` with the finding id: prints `ok`, hides the defect, and never tells anyone when the defect is fixed; rejected.
- Build tags or separate "allowed to fail" jobs for failing tests: splits the signal across jobs and still hides XPASS; rejected.
- Global linter exclusions for the baseline rules: would silence the rules for new code too; rejected.
- `golangci-lint --new-from-rev`: line-based, so the Phase 0 `gofmt` pass would make hundreds of old findings look new; not used.
- Fixing the lint findings in Phase 0: most are in `game/`, `net/` and `db/` code that Phases 1, 3a and 4b rewrite; fixing them now would be churn and would blur the "no runtime change" boundary of Phase 0.

## Consequences

- A fix for a listed defect must remove the inventory entry in the same change, or CI fails with XPASS; this is the intended workflow.
- The inventory file uses a restricted YAML subset parsed strictly by `testutil` (no YAML dependency added to the module); it remains valid YAML for other tools.
- The lint baseline is large (659 errcheck, 453 gosec, 20 staticcheck). It is a measurement, not a target for Phase 0; the burn-down phases are named per rule.

## Validation

- Removing an inventory entry for a still-failing case makes `go test` fail with FAIL; adding an entry for a passing case fails with XPASS (both checked manually in Phase 0, see `docs/testing/phase0-validation.md`).
- `lintdiff` reports zero new and zero stale findings on the Phase 0 tree; `test/scripts/lint-gate-selftest.sh` proves that an injected unchecked error fails the exact gate script (also when piped into a log), that an invalid linter configuration is a tool failure, and that a missing baseline is fatal.
- `test/scripts/golden-mutations.sh` proves, in temporary copies, that a new mismatch, a drifted divergence, a fixed divergence (XPASS), a removed inventory entry and a missing fixture each fail.
- `go test ./tools/growthgate` covers unchanged, reduction-only, growth without approval, growth with label or trailer approval, mixed add/remove, first introduction and missing inputs.

## References

- `docs/architecture/microservices-migration-plan.md` sections 13.1, 17.2, 17.3, 21.
- `docs/testing/known-defects.md`, `docs/testing/lint-baseline.md`.
