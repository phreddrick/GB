# Phase 0 handoff for independent review

**Current result (2026-10-28, after the third Codex review): all Phase 0 work units and every Codex finding so far are implemented and verified locally. Codex reviewed three times: 9 findings (GB0-01 to GB0-09), then 7 (GB0-06A, GB0-06B, GB0-04R, GB0-08R, GB0-03R, GB0-05R, GB0-09R), then 4 (GB0-06A simplified acknowledgment, GB0-04R accepted limitation, GB0-08R build-flag selection, GB0-09R documentation), 20 in total; all are addressed, one (GB0-04R) as a documented, owner-accepted limitation. Phase 0 is NOT declared complete: completion requires the hosted GitHub Actions run and the actual Docker image build to succeed, which need authorised commits.** Nothing is committed, staged, pushed or deployed. This document does not approve the work for merging.

## 1. Repository state

| Item | Value |
|---|---|
| Branch | `master` |
| HEAD | `71eaee729f84976cc54a048e04731a5816c40953` (unchanged since the start of Phase 0; nothing committed) |
| Working tree | 37 modified, 9 deleted at their old path (moved), 72 untracked files/directories; `source/` untouched; both architecture documents intact (409 and 1,999 lines) |

## 2. Findings-first status (Codex second review)

| Finding | Correction | Files | Adversarial reproduction | Result |
|---|---|---|---|---|
| **GB0-06A** (third review: fragile push-event reconstruction) | Pull-request-only acknowledgment: the workflow passes the exact label names from the `pull_request` payload as a JSON array; the script matches exactly and case-sensitively, fails closed on a missing base, missing or malformed label list, or unknown base; `labeled`/`unlabeled` re-run it. **Push events run technical validation only**: the gate is skipped, performs no label lookups and no API calls, and neither authorizes nor modifies any baseline; the `pull-requests: read` permission and the commit-association lookup were removed. Trailers were already removed. Labels are sole-maintainer acknowledgments; CODEOWNERS is informational; branch protection is UNVERIFIED. | `test/scripts/growth-gate.sh`, `test/scripts/gate-approval-selftest.sh`, `.github/workflows/ci.yml`, `docs/testing/known-defects.md`, ADR-0003 | `gate-approval-selftest.sh`, 22 checks: exact label accepted; missing labels rejected; partial (`known-defect-added-not`), wrong-case, combined-in-one-string and unrelated labels rejected; exact label among others accepted; label removed again rejected; trailer rejected; changed value rejected then accepted with label; removal accepted; tooling change rejected without `gate-change-reviewed` (also with only `known-defect-added`), accepted with it; missing label list, malformed JSON, non-array JSON, missing base and unknown base all fail closed; push event exits 0 with the technical-validation notice and a shadowed `curl` proving no network call. | PASS (CI enforcement); repository protection **UNVERIFIED** |
| **GB0-06B** changed expected values bypassed the gate | `growthgate` parses both revisions canonically (inventory with the test parser, CSV by case and value, baseline by line) and classifies growth, **modification** (changed test, package, expected file, finding, reason, phase, tested/untested, or an expected case's Go value) and reduction; growth and modification need the label, reductions do not. | `go/tools/growthgate/main.go`, `growthgate_test.go` | Unit test `TestChangedExpectedValueNeedsApproval` (points=5: 1 to 2, reported as `go value 1 -> 2`), `TestChangedEntryFieldsNeedApproval` (seven fields), replaced case, first introduction, reductions, unchanged; script self-test scenario 4 and 5. Golden tests cannot launder the change: a drifted Go value fails `TestGolden` itself (mutation scenario 3). | PASS |
| **GB0-04R** identical source lines allowed debt substitution | Finding identity is now path + linter + message + **enclosing top-level declaration** (resolved with `go/parser`) + normalised statement; duplicates counted per declaration; unparsable files are fatal; stale entries still fail. Baseline regenerated in format v3: **1,132** findings (1,061 previously committed, 71 newly tracked `go/cmd`), zero from Phase 0 files. | `go/tools/lintdiff/{main,report}.go`, `lintdiff_test.go`, `docs/testing/lint-baseline.{md,txt}` | Unit tests with a real Go fixture tree: the `f.Close()` statement moved from `openA` to `openB` is new+stale; identical statements in different functions have distinct keys; a duplicate added to the same function is new; formatting-only movement (blank lines, indentation, line numbers) produces zero findings; a finding moved to an unrelated declaration is new+stale; absolute and relative paths are equal; the unchanged baseline reports `new 0, stale 0` (live gate). Residual: two identical statements inside one declaration remain indistinguishable (documented). | PASS |
| **GB0-08R** source-text inventory validation could not prove execution | Three layers: the per-package `TestMain` validator uses `go list -json -test` to resolve the test files Go selected for this platform and writes a completion record (`GB_INVENTORY_RECORDS`); `TestInventoryStatic` uses the same selection repository-wide and prints excluded (platform-specific) files; `tools/inventorycheck` reconciles every tested entry against the records after the run and fails on missing records, wrong platform, unfinished tests or unvalidated ids. CI runs `test/scripts/inventory-check.sh` instead of a bare `go test`. | `go/internal/testutil/{knowndefects.go,inventory_test.go}`, `go/tools/inventorycheck/*`, `test/scripts/inventory-check.sh`, `.github/workflows/ci.yml` | `golden-mutations.sh` 12 (Windows-only owning test and `TestMain` on Linux: fails), 13 (owning package with no selected test files: fails), 14 (validator removed: fails), 15 (unchanged tree: `all tested entries reconciled with 1 validation record(s) on linux/amd64`), plus 7, 9, 10, 11 from the first round; `go test ./tools/inventorycheck` (missing record, unvalidated entry, unfinished test, other platform, wrong package, missing directory). | PASS |
| **GB0-03R** trailing `]` accepted | Whole-document `json.Unmarshal` replaces the streaming decoder; any token after the object is a syntax error. | `go/tools/lintdiff/report.go`, `lintdiff_test.go` | `TestTrailingContentIsRejected`: valid passes; trailing whitespace passes; trailing `]` fails; second object fails (with and without newline); truncated fails; plus the earlier malformed/missing-field/report-error/missing-linter cases. | PASS |
| **GB0-05R** Docker builder not pinned to the exact toolchain | Builder `golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb` (index digest obtained read-only from Docker Hub's registry on 2026-10-28), `ENV GOTOOLCHAIN=local`, and a `RUN` assertion that `go version` prints `go1.27.3`. Same patch version now for local validation, CI (`setup-go` 1.27.3, `GOTOOLCHAIN=local`, asserted), `go.mod` `toolchain go1.27.3`, govulncheck and Docker. | `Dockerfile`, `docs/testing/dependencies-2026-10.md` | Static only: BuildKit parser accepts the file; the builder-stage commands were run by hand earlier; no container runtime on igor by owner instruction. | PASS (static); image build **requires hosted CI** |
| **GB0-09R** (third review: remaining inconsistencies) | `lint-baseline.md` describes building and running `lintdiff` and `golangci-lint` as binaries; the review-finding count is stated consistently (9 + 7 + 4 = 20); the GB0-04R limitation and the pull-request-only acknowledgment policy are documented; branch protection is UNVERIFIED; post-merge push checks validate code but do not authorize baseline changes; the four-commit sequence is kept; Docker and hosted CI stay unverified. The existing documents were updated in place. | `docs/testing/phase0-{handoff,validation,implementation-log}.md`, `docs/testing/lint-baseline.md`, `docs/testing/known-defects.md`, ADR-0003, `CLAUDE.md` | Documentation link and path check (0 problems); stale-wording scan clean | PASS |

## 3. Local validation summary

Every check below was executed on igor against the working tree; none is a hosted result.

| Area | Result |
|---|---|
| `go build`, `go vet`, `gofmt -l` (empty), `go mod verify`, `go mod tidy` no-op | PASS |
| `go test ./...` (5 test packages) and `go test -race ./...` | PASS |
| Golden and call-site tests, individual cases | **1,395 comparisons: 1,249 PASS, 146 XFAIL, 0 XPASS, 0 FAIL** (XFAIL: CR-26 16, CR-27 3, GD-01 47, GD-02 80) |
| Fixture reproducibility (`make -C test/ctool check`) | PASS, 10 fixtures, 712 rows reproduce exactly |
| Golden and inventory mutation scenarios (`test/scripts/golden-mutations.sh`, temporary copies) | 15 of 15 behave as required |
| Inventory reconciliation (`test/scripts/inventory-check.sh`) | PASS, 1 record, 5 entries, `linux/amd64` |
| Lint gate (`test/scripts/lint-gate.sh`, exact CI script) | `baseline 1132, report 1132, matched 1132, new 0, stale 0` |
| Lint gate self-test | 5 of 5 scenarios (clean 0; injected finding 1; piped through `tee` still 1; invalid config 2; missing baseline 2) |
| Gate acknowledgment self-test (`test/scripts/gate-approval-selftest.sh`) | 22 of 22 checks (pull-request context, exact labels, fail-closed contexts, push mode without lookups) |
| Growth gate against `HEAD` (local simulation) | reports the whole Phase 0 inventory and tooling as new and refuses without the labels; in push mode exits 0 with the technical-validation notice |
| Build-selection tests (`go test ./internal/testutil -run TestSelection`) | 5 of 5 (default, `GOFLAGS -tags`, excluded by active tags, no tests, Windows-only) |
| `govulncheck` v1.9.0 with `GOTOOLCHAIN=local` (go1.27.3) | 0 reachable vulnerabilities |
| `actionlint` | clean |
| Candidate tracked-source tree (tracked + untracked-not-ignored files copied to a scratch directory) | build, vet, format, tests, fixture check pass |
| Documentation links and paths | 0 problems |
| Scope: `source/` unchanged, `git diff --check` clean, architecture documents intact | PASS |

## 4. Totals

- Golden tests: 1,395 individual comparisons, 1,249 PASS, 146 XFAIL, 0 XPASS, 0 FAIL, across 25 categories; 5 inventory entries, 0 untested. CR-27 has deterministic Go call-site coverage (radiation mode, exact CEW range, no armour, body 40); the C call site (`shootblast.c`, `hits = strength*CEW_hit`) was read, not executed.
- Lint baseline: 1,132 findings (659 errcheck, 453 gosec, 20 staticcheck); 1,061 in previously committed files, 71 newly tracked legacy debt in `go/cmd/*/main.go`; 0 in Phase 0 files.

## 5. Acknowledgment behaviour (single maintainer, pull requests only)

On pull requests, growth or modification of allowances fails CI unless the pull request carries the exact label `known-defect-added`; changes under the gate-tooling paths fail unless it carries `gate-change-reviewed`. Both are acknowledgments applied by the sole maintainer after reading the gate's itemised list; they are not independent approvals. Reductions pass without labels. Adding or removing a label re-runs CI. A missing or malformed pull-request context fails closed. Commit messages and trailers are never consulted. Pushes to `master` run the technical validation only (build, vet, tests, race, golden and mutation checks, inventory reconciliation, lint gate, govulncheck, Docker build); they neither look up labels nor authorize or modify baselines. Whether a change can reach `master` without a pull request is governed by branch protection, which is UNVERIFIED.

## 6. GitHub settings for the owner (UNVERIFIED, not modified)

Owner actions before the first merge, in order:

1. Create the repository labels `known-defect-added` and `gate-change-reviewed`.
2. Protect `master` (branch protection or ruleset): require a pull request before merging; require the status checks `ci / build, test, lint (fresh checkout)` and `ci / docker build from checkout (no push)`; required approving reviews **0**; do not require Code Owner review; disable force pushes, deletions and administrator bypass.
3. Open the Phase 0 pull request, read the gate output, apply both labels.

None of this could be inspected from igor (no `gh` CLI, no API token), so direct pushes are not claimed to be prevented; the pull-request gate is the enforcement point inside CI.

## 7. Dependency-safe commit sequence (unchanged)

| # | Commit | Contents | Why it stands alone |
|---|---|---|---|
| 1 | Foundation | both architecture documents; `.gitignore`, `go/.gitignore`; the four `go/cmd/*/main.go`; 19 `gofmt`-only edits; module path `github.com/phreddrick/GB/go` and `toolchain go1.27.3` in `go/go.mod`; 26 import edits; JWT v5.3.1 (`go.mod`, `go.sum`); `docs/testing/dependencies-2026-10.md`; `Dockerfile` (pinned builder) | builds, vets, formats; a clean clone builds for the first time |
| 2 | Golden foundation | `test/ctool/**`, `test/golden/testdata/*.csv`, `test/golden/expected/*.csv`, `test/knowndefects.yaml`, `go/internal/testutil/**`, `go/internal/game/{golden_test,cew_callsite_test,inventory_test}.go`, `go/tools/inventorycheck/**`, `test/scripts/inventory-check.sh`, `docs/testing/known-defects.md`, `docs/testing/phase0-baseline.md`, `docs/architecture/adr/{README,0001,0002}.md` | `go test ./...` passes with the recorded XFAIL cases; fixtures reproduce; reconciliation passes |
| 3 | CI, lint and gates | `.github/workflows/ci.yml`, `.github/CODEOWNERS`, `go/.golangci.yml`, `go/tools/lintdiff/**`, `go/tools/growthgate/**`, the other `test/scripts/*.sh`, `docs/testing/lint-baseline.{md,txt}`, `docs/architecture/adr/0003-*.md` | lint gate `new 0, stale 0` against exactly this code; the growth gate sees the inventory and baseline as new against the pre-Phase-0 base, so **this pull request carries both labels** (documented behaviour) |
| 4 | Documentation closeout | move of `go/reference/*.md` to `docs/archive/earlier-model-notes/`; `go/README.md`, `CLAUDE.md`, `KUBERNETES.md`; `docs/testing/phase0-{implementation-log,validation,handoff}.md` | documentation only; every link target exists after 1 to 3 |

Recommended shape: one branch `phase0` with these four commits and one pull request labelled `known-defect-added` and `gate-change-reviewed`, after the Codex re-review.

## 8. Validations that still require hosted CI

- The hosted run of `.github/workflows/ci.yml` itself (every step was executed locally through the same scripts, but no GitHub-hosted run has happened).
- The Docker image build (`docker` job): no container runtime on igor by owner instruction; only static validation was done.

## 9. Remaining blockers to committing Phase 0

- Codex re-review of this round (targeted).
- The owner actions in section 6 (labels and branch protection). Without the labels the first pull request cannot pass the acknowledgment gate; without protection, direct pushes are not prevented.
- No other blockers are known; no check fails locally. The feature branch can be created and the four commits made as soon as the owner authorises commits.

## 10. Changed-file inventory

Modified (37):

- `.gitignore`
- `CLAUDE.md`
- `Dockerfile`
- `KUBERNETES.md`
- `go/.gitignore`
- `go/README.md`
- `go/go.mod`
- `go/go.sum`
- `go/internal/db/keys.go`
- `go/internal/db/store.go`
- `go/internal/enroll/enroll.go`
- `go/internal/game/build.go`
- `go/internal/game/combat.go`
- `go/internal/game/commands.go`
- `go/internal/game/commands_cs.go`
- `go/internal/game/commands_fire.go`
- `go/internal/game/commands_load.go`
- `go/internal/game/commands_misc.go`
- `go/internal/game/commands_move.go`
- `go/internal/game/commands_nav.go`
- `go/internal/game/commands_order.go`
- `go/internal/game/commands_power.go`
- `go/internal/game/commands_profile.go`
- `go/internal/game/commands_tech.go`
- `go/internal/game/commands_teleg.go`
- `go/internal/game/helpers.go`
- `go/internal/game/planet.go`
- `go/internal/game/ship.go`
- `go/internal/game/state.go`
- `go/internal/game/turn.go`
- `go/internal/game/vn.go`
- `go/internal/model/planet.go`
- `go/internal/model/power.go`
- `go/internal/model/race.go`
- `go/internal/model/ship.go`
- `go/internal/model/star.go`
- `go/internal/net/server.go`

Deleted at the old path (moved to `docs/archive/earlier-model-notes/`, contents unchanged) (9):

- `go/reference/AUDIT_COMMAND.md`
- `go/reference/AUDIT_CONSTANTS.md`
- `go/reference/AUDIT_CONSTANTS_SUMMARY.md`
- `go/reference/FIXES_APPLIED.md`
- `go/reference/SESSION_SUMMARY.md`
- `go/reference/TESTS_MARKET_BIDDING.md`
- `go/reference/TESTS_PLANET_GENERATION.md`
- `go/reference/TEST_REPORT.md`
- `go/reference/USAGE_VERIFICATION.md`

Untracked (new) (73):

- `.github/CODEOWNERS`
- `.github/workflows/ci.yml`
- `docs/architecture/adr/0001-modular-monolith-first.md`
- `docs/architecture/adr/0002-golden-test-method.md`
- `docs/architecture/adr/0003-known-defect-inventory.md`
- `docs/architecture/adr/README.md`
- `docs/architecture/go-code-review.md`
- `docs/architecture/microservices-migration-plan.md`
- `docs/archive/earlier-model-notes/AUDIT_COMMAND.md`
- `docs/archive/earlier-model-notes/AUDIT_CONSTANTS.md`
- `docs/archive/earlier-model-notes/AUDIT_CONSTANTS_SUMMARY.md`
- `docs/archive/earlier-model-notes/FIXES_APPLIED.md`
- `docs/archive/earlier-model-notes/README.md`
- `docs/archive/earlier-model-notes/SESSION_SUMMARY.md`
- `docs/archive/earlier-model-notes/TESTS_MARKET_BIDDING.md`
- `docs/archive/earlier-model-notes/TESTS_PLANET_GENERATION.md`
- `docs/archive/earlier-model-notes/TEST_REPORT.md`
- `docs/archive/earlier-model-notes/USAGE_VERIFICATION.md`
- `docs/testing/dependencies-2026-10.md`
- `docs/testing/known-defects.md`
- `docs/testing/lint-baseline.md`
- `docs/testing/lint-baseline.txt`
- `docs/testing/phase0-baseline.md`
- `docs/testing/phase0-handoff.md`
- `docs/testing/phase0-implementation-log.md`
- `docs/testing/phase0-validation.md`
- `go/.golangci.yml`
- `go/cmd/gb-client/main.go`
- `go/cmd/gb-server/main.go`
- `go/cmd/makeuniv/main.go`
- `go/cmd/racegen/main.go`
- `go/internal/game/cew_callsite_test.go`
- `go/internal/game/golden_test.go`
- `go/internal/game/inventory_test.go`
- `go/internal/testutil/golden.go`
- `go/internal/testutil/inventory_test.go`
- `go/internal/testutil/knowndefects.go`
- `go/internal/testutil/selection_test.go`
- `go/tools/growthgate/growthgate_test.go`
- `go/tools/growthgate/main.go`
- `go/tools/inventorycheck/inventorycheck_test.go`
- `go/tools/inventorycheck/main.go`
- `go/tools/lintdiff/lintdiff_test.go`
- `go/tools/lintdiff/main.go`
- `go/tools/lintdiff/report.go`
- `test/ctool/.gitignore`
- `test/ctool/Makefile`
- `test/ctool/README.md`
- `test/ctool/include/curses.h`
- `test/ctool/main.c`
- `test/ctool/stubs.c`
- `test/golden/expected/cew_strength_multiplier.csv`
- `test/golden/expected/compatibility.csv`
- `test/golden/expected/ground_attack.astrength.csv`
- `test/golden/expected/ground_attack.dstrength.csv`
- `test/golden/expected/planet_guns.csv`
- `test/golden/testdata/ap_add.csv`
- `test/golden/testdata/cew_hit.csv`
- `test/golden/testdata/cew_hit_odds.csv`
- `test/golden/testdata/compatibility.csv`
- `test/golden/testdata/ground_attack.csv`
- `test/golden/testdata/hit_odds.csv`
- `test/golden/testdata/max_support.csv`
- `test/golden/testdata/planet_guns.csv`
- `test/golden/testdata/produce.csv`
- `test/golden/testdata/ship_design.csv`
- `test/knowndefects.yaml`
- `test/scripts/gate-approval-selftest.sh`
- `test/scripts/golden-mutations.sh`
- `test/scripts/growth-gate.sh`
- `test/scripts/inventory-check.sh`
- `test/scripts/lint-gate-selftest.sh`
- `test/scripts/lint-gate.sh`

## 11. Reviewer procedure

```sh
cd /home/steve/workspace/GB
git status --porcelain; git ls-files --others --exclude-standard
cd go && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go mod verify && cd ..
test/scripts/inventory-check.sh                       # go test ./... with records + reconciliation
cd go && go test -count=1 -race ./... && cd ..
make -C test/ctool check                              # fixtures reproduce from the C reference
test/scripts/golden-mutations.sh                      # 15 adversarial scenarios in temp copies
test/scripts/lint-gate.sh /tmp/lint.json              # expect "new 0, stale 0"
test/scripts/lint-gate-selftest.sh                    # 5 scenarios (several minutes)
test/scripts/gate-approval-selftest.sh                # 22 acknowledgment checks (PR context, exact labels, push mode)
test/scripts/growth-gate.sh HEAD                      # local simulation: expect refusal without labels
GITHUB_EVENT_NAME=push test/scripts/growth-gate.sh HEAD   # push mode: technical validation only, exit 0
cd go && GOTOOLCHAIN=local go version && GOTOOLCHAIN=local go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./... && cd ..
cd go && go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 ../.github/workflows/ci.yml && cd ..
grep -vc '^#' docs/testing/lint-baseline.txt; grep -c '^go/cmd/' docs/testing/lint-baseline.txt   # 1132, 71
git status --porcelain source/                        # empty
```

## 12. History

The finding-by-finding records of all three Codex reviews and of the Phase 0 work units are in `phase0-implementation-log.md`; the per-check results are in `phase0-validation.md`. Earlier versions of this handoff described a commit-trailer approval path, a CODEOWNERS review requirement and a push-event lookup of merged pull-request labels; all three were withdrawn (GB0-06A, twice, and the owner's single-maintainer policy) and are no longer part of the design.
