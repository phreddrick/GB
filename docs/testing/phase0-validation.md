# Phase 0 validation report

Current result, run on 2026-10-28 (after the third Codex review) against the working tree at HEAD `71eaee729f84` plus the uncommitted Phase 0 changes, after all Codex findings (first review GB0-01 to GB0-09; second review GB0-06A, GB0-06B, GB0-04R, GB0-08R, GB0-03R, GB0-05R, GB0-09R) and the owner's single-maintainer policy. Commands run from `go/` unless stated. Nothing is committed.

## Status legend

| Class | Meaning | Items |
|---|---|---|
| **Verified locally** | executed on igor against the working tree; output recorded here | every PASS row below |
| **Requires hosted GitHub Actions** | observable only after authorised commits | the hosted run of `.github/workflows/ci.yml` |
| **Docker image build: unverified** | no container runtime on igor by owner instruction; static validation only | the `docker` job |
| **Known defects, intentionally deferred** | recorded per case as XFAIL with expected values; not Phase 0 failures | CR-26 (16 cases), CR-27 (3 cases), GD-01 (47 cases), GD-02 (80 cases, provisionally keeping `math.Pi`) |
| **Not executed** | no attempt made | race detector under load, Longhorn timing, any C server execution, RNG-value-dependent golden comparisons (Phase 3b), hosted CI, Docker build, GitHub repository-settings inspection (no `gh`/API access) |

Phase 0 is not declared complete until the hosted CI run and the Docker image build succeed.

## Checks

| Check | Result | Actual output | Known-defect ref | Pre-existing | To resolve a blocker |
|---|---|---|---|---|---|
| `go build ./...` | PASS | clean | | n/a | |
| `go vet ./...` | PASS | clean | | n/a | |
| `gofmt -l .` | PASS | no files (19 before Phase 0) | | was failing | |
| `go mod verify`; `go mod tidy` no-op | PASS | all modules verified; no change | | n/a | |
| Toolchain | PASS | `GOTOOLCHAIN=local go version` is `go1.27.3`; `go.mod`: `go 1.25.0`, `toolchain go1.27.3`; CI pins `go-version: 1.27.3` and asserts it; Dockerfile builder `golang:1.27.3-alpine@sha256:d200d2dd…3deb` with `GOTOOLCHAIN=local` and a `go version` assertion | | | |
| `go test ./...` | PASS | `ok` for `internal/game`, `internal/testutil`, `tools/growthgate`, `tools/inventorycheck`, `tools/lintdiff` | XFAIL cases listed below | n/a | |
| `go test -race ./...` | PASS | `ok` for the same five packages | | BLOCKED before the owner installed gcc | |
| Golden and call-site tests, individual cases | PASS with recorded XFAIL | **1,395 comparisons: PASS=1249 XFAIL=146 XPASS=0 FAIL=0**, 25 categories | CR-26, CR-27, GD-01, GD-02 | pre-existing code behaviour | |
| `test/scripts/inventory-check.sh` (records + reconciliation) | PASS | `inventorycheck: package internal/game on linux/amd64: selected test files [cew_callsite_test.go golden_test.go inventory_test.go], excluded []`; `all tested entries reconciled with 1 validation record(s)` | | | |
| `make -C test/ctool check` | PASS | 10 fixtures, 712 data rows, "fixtures reproduce exactly" | | | |
| `test/scripts/golden-mutations.sh` (15 scenarios, temporary copies) | PASS | 1 unchanged tally; 2 new mismatch fails; 3 drifted divergence fails; 4 fixed divergence XPASS fails; 5 removed entry fails; 6 missing fixture fails; 7 `TestDoesNotExist` fails; 8 unused expected case STALE; 9 deleted test file fails; 10 moved package fails; 11 explicit UNTESTED visible and passing; 12 Windows-only owning test fails on Linux; 13 package with no selected test files fails; 14 removed `TestMain` validator fails; 15 unchanged tree reconciles | | | |
| `go test ./tools/inventorycheck` | PASS | reconciled run passes; missing record, unvalidated entry, unfinished test, other-platform record, wrong package, missing directory all fail | | | |
| `go test ./internal/testutil` | PASS | `TestInventoryStatic` (go-list-selected files, validator present, expected files parse), `TestInventoryParserRejectsEvasions` | | | |
| Lint gate `test/scripts/lint-gate.sh` (exact CI script) | PASS | golangci-lint v2.15.0 exit 1 (findings), report validated, `baseline 1132, report 1132, matched 1132, new 0, stale 0`, exit 0 | | 1,132 baselined (1,061 previously committed, 71 newly tracked `go/cmd`) | |
| `test/scripts/lint-gate-selftest.sh` | PASS | clean 0; injected unchecked error 1; piped through `tee` still 1 and the log names the finding; invalid golangci configuration 2; missing baseline 2 | | | |
| `go test ./tools/lintdiff` (identity and validation) | PASS | `f.Close()` moved between functions is new+stale; identical statements in different functions distinct; duplicate in the same function new; formatting-only movement zero findings; move to another declaration new+stale; absolute/relative paths equal; unparsable file fatal; trailing `]`, second document, truncated, malformed, missing fields, report error, missing linter all rejected; trailing whitespace accepted; genuine zero report valid but all-stale against a baseline | | | |
| `go test ./tools/growthgate` | PASS | unchanged; reduction only; growth of entry/case/baseline refused; changed expected value `points=5` 1 to 2 refused as a modification and accepted with the label; seven entry-field changes refused; replaced case refused; first introduction refused then accepted; missing head fatal; malformed inventory fatal; `-trailer-from` is an unknown flag | | | |
| `test/scripts/gate-approval-selftest.sh` (pull-request context, 22 checks) | PASS | unchanged accepted; growth refused; self-authored trailer refused; exact label accepted; partial, wrong-case, combined-in-one-string and unrelated labels refused; exact label among others accepted; label removal re-evaluated and refused; changed expected value refused then accepted with label; removal accepted; gate-tooling change refused (also with only `known-defect-added`) then accepted with `gate-change-reviewed`; missing label list, malformed JSON, non-array JSON, missing base and unknown base fail closed; push event: technical-validation notice, exit 0, no network call (shadowed `curl` never invoked) | | | |
| `test/scripts/growth-gate.sh HEAD` (local simulation) | PASS (behaves as designed) | all Phase 0 allowances and tooling reported as new relative to `HEAD`; refused without labels; `GITHUB_EVENT_NAME=push` exits 0 with the technical-validation-only notice | | | the first pull request carries both labels |
| `go test ./internal/testutil -run TestSelection` (build selection honours the real flags) | PASS | default: tagged file ignored and reported; `GOFLAGS=-tags=gbtag`: tagged file selected; `GOFLAGS=-tags=othertag`: file excluded, no test; package without tests: empty selection; Windows-only file ignored on linux | | | |
| Lint identity, accepted limitation (GB0-04R) | documented | `TestSameDeclarationSwapIsNotDistinguished` pins that an identical statement moved within one declaration is not detected; cross-function moves, duplicates, stale and new findings are | | | review the code diff of files with baseline findings |
| `govulncheck@v1.9.0` with `GOTOOLCHAIN=local` | PASS | 0 reachable vulnerabilities (17 unreachable in required modules) | | 1 reachable before (GO-2025-3553) | |
| `actionlint` | PASS | no findings for both event types; `shell: bash` (pipefail); `permissions: contents: read` only; `pull_request` types include `labeled`/`unlabeled`; labels passed as a JSON array; no `pull_request_target`; push runs technical validation only | | | |
| Candidate tracked-source tree | PASS | build, vet, gofmt, tests, fixture check pass on the copied tracked + untracked-not-ignored files | | | a real fresh clone needs the first commit |
| Hosted GitHub Actions run | **NOT RUN** | requires authorised commits | | | first pull request |
| Docker image build | **BLOCKED: Docker not installed** (igor is an RKE2 control-plane node) | static: BuildKit parser accepts the Dockerfile; builder-stage commands run by hand; `.dockerignore` keeps `go/cmd`, `go.mod`, `go.sum` | | | CI `docker` job |
| GitHub branch protection, rulesets, labels | **UNVERIFIED** | no `gh` CLI or API token on igor; intended values and the owner's pre-merge actions are listed in `known-defects.md` and the handoff; direct pushes are not claimed to be prevented | | | owner creates the labels and protects `master` |
| `git diff --check`; `source/` unchanged; architecture documents intact | PASS | no whitespace errors; `git status --porcelain source/` empty; 409 and 1,999 lines | | | |
| Documentation links and repository paths in changed Markdown | PASS | 0 problems | | | |

## Golden test detail

| Fixture | Rows | Go function(s) | Result |
|---|---|---|---|
| `planet_guns` | 19 | `planetGuns` | 3 PASS, 16 XFAIL CR-26 (Go `mob/5`, C `MIN(20, points/1000)`; values recorded in `test/golden/expected/planet_guns.csv`) |
| `hit_odds` | 246 | `hitOdds` (odds, factor) | 492 PASS |
| `cew_hit_odds` | 48 | `cewHitOdds` | PASS |
| `cew_hit` | 12 | `cewHit` at odds 100 and 0 | PASS |
| `compatibility` | 60 | `compatibility` | 13 PASS, 47 XFAIL GD-01 (C truncates the temperature term to an int) |
| `max_support` | 50 | `maxSupport` | PASS |
| `ship_design` | 95 | `shipSize`, `getMass`, `shipCostFromDesign`, `complexityOf` on 47 C-generated default designs plus 48 modified ones | 380 PASS. Covers exactly those four functions; inputs come from C's `Getship()`, so Go's `getshipDefaults` is not tested, and only the `Shipdata` columns these functions read (`ABIL_COST`, `ABIL_TECH` and the eight design bases) are exercised |
| `ground_attack` | 40 | `groundAttack` strengths | 80 XFAIL GD-02 (`moraleFactor` uses `math.Pi`; C uses 3.14159565; owner's provisional decision: keep `math.Pi`, confirm in Phase 3) |
| `ap_add` | 130 | `apAdd` + `governed` | PASS (every Go result inside the C bracket) |
| `produce` | 12 | `produce` + `plate`, steady state | 120 PASS |
| call site | 4 | `shootShipToShip` CEW path in radiation mode | 1 PASS (strength 1), 3 XFAIL CR-27 (strengths 2, 5, 20). Go call site executed deterministically; the C call site (`shootblast.c`, `hits = strength*CEW_hit`) was read, not executed |

Not validated in Phase 0 and recorded as such: ground-attack casualties, resource production, population growth and other RNG-value-dependent paths (Phase 3b), and anything about turns, persistence, networking or enrollment (Phase 1 onward).
