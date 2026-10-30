# Phase 1 validation report

Current result, run on 2026-10-29 on igor against the working tree of branch `feature/gb-phase1-stabilization` (created from `origin/master` at `a6e65ee9a769279b276602ed60fca67acf40cb21`, the Phase 0 merge) plus the uncommitted Phase 1 changes. Commands run from `go/` unless stated. **Nothing is committed, staged, pushed or deployed.** The implementation awaits independent Codex review (`phase1-handoff.md`).

**Remediation update (2026-10-29, evening).** The independent Codex review returned FAIL (GB1-01 to GB1-09). All nine findings are corrected and every check below was re-run on the corrected tree; the remediation evidence is in the section "Independent review remediation" at the end. The figures in the tables reflect the corrected tree.

## Status legend

| Class | Meaning | Items |
|---|---|---|
| **Verified locally** | executed on igor against the working tree; output recorded here | every PASS row below |
| **Requires hosted GitHub Actions** | observable only after authorised commits | the hosted run of `.github/workflows/ci.yml`, the `docker` job |
| **Known defects, intentionally deferred** | recorded in the inventory; not Phase 1 failures | CR-26, CR-27, GD-01, GD-02 (146 XFAIL cases, unchanged); **CR-18** (new explicit `untested` entry, Phase 3a) |
| **Not executed** | no attempt made | crash injection (kill -9 during a flush) and any crash-consistency assertion; Longhorn timing; any C server execution; hosted CI; Docker build; GitHub settings inspection |

## Baseline (before any Phase 1 change, same tree as `origin/master`)

`go build`, `go vet`, `gofmt -l` (empty), `go test ./...` (5 test packages), `go test -race ./...`: PASS; `govulncheck` v1.9.0: 0 reachable; lint gate: `baseline 1132, report 1132, matched 1132, new 0, stale 0`.

## Checks on the Phase 1 tree

| Check | Result | Actual output | Notes |
|---|---|---|---|
| `go build ./...` | PASS | clean (five commands including the new `gbctl`) | |
| `go vet ./...` | PASS | clean | |
| `gofmt -l .` | PASS | no files | |
| `go mod verify`; `go mod tidy` | PASS | verified; tidy removed `golang.org/x/term` (its only user, the interactive prompt in the old `makeuniv`, is gone); no module added | `docs/testing/dependencies-2026-10.md` |
| `go test -count=1 ./...` | PASS | `ok` for 14 test packages (the earlier "15" was a miscount of this same list): `cmd/gbctl`, `cmd/makeuniv`, `internal/admin`, `internal/config`, `internal/game`, `internal/net`, `internal/platform/logging`, `internal/platform/metrics`, `internal/testutil`, `internal/universe`, `go/test/e2e`, `tools/growthgate`, `tools/inventorycheck`, `tools/lintdiff` | 5 packages before Phase 1 |
| `go test -count=1 -race ./...` | PASS | `ok` for the same 14 packages, including the GB1 and GB1-06R regression suites, including concurrent logins during updates (`TestAuthenticateThroughActor`), concurrent admin requests (`TestAdminRequestsAreBoundedAndSerialized`), concurrent universe-init requests, the WebSocket flood test and the subprocess suite | |
| Golden and call-site tests, individual cases | PASS with recorded XFAIL | **1,395 comparisons: PASS=1249 XFAIL=146 XPASS=0 FAIL=0**, 25 categories (CR-26 16, CR-27 3, GD-01 47, GD-02 80) | identical to the Phase 0 baseline; no expected file changed |
| `make -C test/ctool check` (repository root) | PASS | 10 fixtures reproduce exactly | C tree unchanged |
| `test/scripts/inventory-check.sh` | PASS | `package internal/game on linux/amd64: selected test files [cew_callsite_test.go golden_test.go inventory_test.go lifecycle_test.go]`; `all tested entries reconciled with 1 validation record(s)`; the new `persistence/non-atomic-turn-flush` entry is printed as `UNTESTED (explicit)` | |
| `test/scripts/golden-mutations.sh` | PASS | all fifteen scenarios behaved as required (corrected script, GB1-10; run three times in this round: 45/45) | protected gate tooling changed, `gate-change-reviewed` |
| `test/scripts/golden-mutations-selftest.sh 100` (new, GB1-10) | PASS | 12 cases x 100 iterations all as required; the former `echo | grep -q` pipeline: 100/100 false failures on the same input | |
| Lint gate `test/scripts/lint-gate.sh` (golangci-lint v2.15.0) | PASS | `baseline 1059, report 1059, matched 1059, new 0, stale 0` | first run on the Phase 1 tree reported **47 new** (all in Phase 1 files) and **73 stale**; the 47 were fixed in code (checked `Fprintf` returns, checked `runTurn` results, annotated test-only `exec`/fixture-credential sites with reasons), then the baseline was regenerated with `lintdiff -write-baseline` from the clean report (reduction only) |
| `test/scripts/lint-gate-selftest.sh` | PASS | all scenarios behaved as required | |
| `test/scripts/gate-approval-selftest.sh` | PASS | all scenarios behaved as required | |
| Growth gate against `origin/master` (local pull-request simulation) | behaves as designed | without labels: `1 additions, 0 modifications, 61 removals` (`+ inventory entry persistence/non-atomic-turn-flush`; removals are the retired baseline keys), **NOT acknowledged**, exit 1; `.github/workflows/ci.yml`, `test/scripts/golden-mutations.sh` and `test/scripts/golden-mutations-selftest.sh` flagged as protected tooling; with `known-defect-added` and `gate-change-reviewed`: acknowledged, exit 0 | the pull request therefore needs **both labels** |
| `govulncheck` v1.9.0 (`GOTOOLCHAIN=local`, go1.27.3) | PASS | 0 reachable vulnerabilities (17 in required modules, not called) | |
| `actionlint` v1.7.7 (`go run`) on `ci.yml` | PASS | clean | the entry-point guard now checks the five `main.go` files by name |
| `git diff --check` | PASS | clean | |
| Scope audit | PASS | `source/`, `k8s-deployment.yaml`, `KUBERNETES.md` body (one note prepended), `Dockerfile`, both architecture documents, `test/golden/**`, `test/ctool/**` unchanged; no commits (`git log origin/master..HEAD` empty); nothing pushed; no image built; no cluster or `k8s-helpers` change | |

## Phase 1 behavioural tests (what they prove)

| Test | File | Proves |
|---|---|---|
| `TestAdminUpdateDoesNotCrash` | `internal/game/lifecycle_test.go` | CR-04 reproduction: a typed update and segment run on a real seeded store, counters and next times advance, the actor still serves commands afterwards, metrics recorded |
| `TestHandlerPanicHaltsEngine` | same | injected handler panic: actor survives, mode HALTED with reason and stack, player told, update refused, resume refused, `Stop` returns `ErrHaltedNotFlushed` |
| `TestTurnPanicHaltsAndKeepsSchedule` | same | injected turn panic halts; persisted schedule not advanced past the failed turn |
| `TestQuiesceResume` | same | quiesce refuses turns and mutating commands, safe verbs still work, resume restores serving |
| `TestAdminRequestsAreBoundedAndSerialized` | same | a request that cannot be accepted before its deadline returns `ErrEngineBusy`; two concurrent turn requests never overlap; health during a turn |
| `TestLivenessWatchdog` | same | stale heartbeat without a turn is not alive; a turn with recent progress is alive; a turn stalled beyond the timeout is not |
| `TestScheduleRestoredAcrossRestart` | same | CR-21: counters, next times and universe identical after `Stop` and a new `State` on the same store |
| `TestOverdueScheduleIsNotCaughtUp` | same | overdue persisted schedule is rescheduled, counted, not executed |
| `TestUninitializedStoreAndAdminInit` | same | CR-05: empty store serves admin only, startup generates nothing, concurrent inits: exactly one succeeds, re-init refused, engine serves afterwards |
| `TestEnrollmentDisabledDoesNotTouchStore` | same | CR-03/Decision 9: disabled enrollment writes nothing and returns at once; preview path runs through the actor in well under the old five-second lock timeout, stores the race, marks it PREVIEW, duplicate and invalid requests rejected |
| `TestAuthenticateThroughActor` | same | credentials checked on the actor; concurrent logins during three updates race-clean |
| `TestStopFlushesAndClosesCleanly` | same | orderly stop flushes a memory-authoritative race change, second stop harmless, work refused afterwards |
| `TestStopTimeoutDuringTurn` | same | `Stop` returns `ErrShutdownTimeout` when the turn outlives the budget; the turn still completes and is persisted once |
| `TestStopReportsFlushFailure` | same | an unusable store at shutdown yields an error, never a silent success |
| `TestProbesPerMode`, `TestDrainingNotReady` | `internal/net/server_test.go` | `/livez` and `/readyz` codes and bodies for all seven modes and while draining; no secret or player fields |
| `TestOldAdminEndpointsAreGone` | same | `/admin/update`, `/admin/segment` answer 410 and never reach the engine |
| `TestEnrollmentDisabledByDefault` | same | 403 and no engine call by default; preview routes through the engine |
| `TestLoginAndWebSocketRoundTrip` | same | login codes (401, 503 busy, 200), welcome, echo, unauthenticated upgrade refused, session cleanup |
| `TestOriginAllowList` | same | no Origin and same-origin allowed, allow-listed origin allowed, foreign origin 403 |
| `TestReadLimitClosesOversizedFrame` | same | oversized frame closes the connection, never reaches the engine, counted |
| `TestSlowClientIsDroppedWithoutBlockingSender`, `TestSessionSendNeverBlocks` | same | a non-reading client is dropped on queue overflow while every `Send` returns promptly |
| `TestCommandWaitIsBoundedAndShutdownClosesSessions` | same | server shutdown sends close code 1012 even with a command parked on a blocked engine; upgrades refused while draining |
| `TestAuthRequired`, `TestOperationsAndStatusCodes`, `TestOpTimeoutAndMetrics` | `internal/admin/admin_test.go` | every admin route needs the exact bearer token (constant-time compare), wrong method 405, error-to-status mapping (409/503/500/400), server-side operation timeout, `/metrics` and `/livez` on the admin listener |
| `TestExitCodes` | `cmd/gbctl/main_test.go` | exit codes 0 to 5, token from env or file only, token never echoed, usage errors |
| `TestRefusesMissingAndInsecureJWTSecret`, `TestRefusesMissingAdminTokenWhenAdminEnabled`, precedence, file-key rejection, secret files, alias, bad values | `internal/config/config_test.go` | CR-06/34/35/36 configuration policy |
| `TestGenerateIsDeterministicPerSeed`, `TestInitRefusesInitializedStore`, `TestLegacyStoreWithoutRecordCountsAsInitialized` | `internal/universe/universe_test.go` | seeded determinism, single-transaction init, refusal on initialised and legacy stores |
| `TestMakeunivRefusesInitializedDatabase` | `cmd/makeuniv/main_test.go` | offline tool refuses (exit 3) or no-ops (`-skip-if-initialized`) |
| `TestAcceptanceScenario` | `go/test/e2e/basic_test.go` | the Phase 1 acceptance scenario with real binaries: uninitialised start, wrong token refused, `gbctl universe init`, re-init refused, clean stop, fixture race added offline, restart with identical universe and schedule, login, `who`/`help`/`survey` over WebSocket, `gbctl update` and `segment` with the process surviving, quiesce/resume with probe codes, SIGTERM with a connected client (close 1012, exit 0, flush logged), restart with identical counters, schedule and universe, JSON logs with no secret |
| `TestStartupRefusesInsecureConfig` | same | the binary exits 1 and creates no database for a missing secret, the old default secret, and a missing admin token; the secret is not echoed |
| `TestSigtermDuringTurnIsBounded` | same | SIGTERM while a 3 s turn runs: the turn completes within the budget, exit 0, counted once after restart |
| `TestShutdownTimeoutIsHonest` | same | SIGTERM while an 8 s turn runs with a 2 s budget: exit 3 within the budget, the timeout is logged, the store reopens with the interrupted turn not counted; **no consistency claim** (CR-18) |
| `TestIdleSigtermAndMetrics` | same | metrics exposed on the admin listener only, no secret in them, admin routes absent from the player listener, idle SIGTERM exits 0 quickly |

Timing: `go test ./...` about 12 s wall clock (`go/test/e2e` 7.5 s after building the two binaries once); `-race` about 30 s.

## Acceptance criteria (brief section 9)

| Criterion | Status | Evidence |
|---|---|---|
| Original admin update panic eliminated | met | `TestAdminUpdateDoesNotCrash`, `TestAcceptanceScenario`; the `@@update` text path no longer exists |
| Typed admin operations without nil responders | met | `internal/game/admin.go`; every request carries a buffered reply channel |
| Admin interface requires authentication | met | `TestAuthRequired`; constant-time compare of SHA-256 digests |
| Missing or insecure secrets fail startup explicitly | met | `TestRefusesMissingAndInsecureJWTSecret`, `TestStartupRefusesInsecureConfig` (exit 1, no file created) |
| Configured database path respected | met | `GB_DB_PATH` in every e2e run; `GB_DATA_PATH` alias test |
| Ordinary startup cannot regenerate an existing universe | met | startup only loads; `TestUninitializedStoreAndAdminInit`, `TestAcceptanceScenario` (seed 4242 identical across three restarts) |
| Universe init explicit, seedable, refuses initialised data | met | `universe.Init`, `db.PutUniverse` (refusal inside the transaction), `TestInitRefusesInitializedStore`, `TestLegacyStore...`, concurrent-init test |
| Public enrollment disabled while races are unplayable | met | default off; 403 and no engine call; preview marked |
| Health and readiness reflect modes | met | `TestProbesPerMode`; `TestLivenessWatchdog` |
| Scheduling metadata survives orderly restarts | met | `TestScheduleRestoredAcrossRestart`, acceptance scenario |
| SIGTERM idle: clean shutdown | met | `TestIdleSigtermAndMetrics`, exit 0 |
| SIGTERM during a turn: bounded, documented, no atomic-recovery claim | met | `TestSigtermDuringTurnIsBounded` (finishes within budget), `TestShutdownTimeoutIsHonest` (exit 3, logged); CR-18 entry |
| WebSocket size, origins, write deadlines, cleanup | met | `TestReadLimit...`, `TestOriginAllowList`, writer goroutine deadlines, `TestSlowClient...`, session cleanup in the round-trip test |
| Operational logs structured | met | slog JSON; legacy `log.Printf` bridged; verified in the acceptance scenario |
| Basic metrics available | met | `/metrics` on the admin listener; `TestIdleSigtermAndMetrics` |
| Phase 0 tests continue to pass | met | 1,249 PASS / 146 XFAIL / 0 / 0; fixtures reproduce; no expected file changed |
| New regression tests cover the corrected defects | met | table above |
| Race tests pass for the executed coverage | met | `go test -race ./...` |
| No new reachable vulnerabilities | met | govulncheck 0; one dependency removed, none added |
| No Phase 2/3 functionality implemented | met | no manifests, no transactional flush, no turn reordering, no new commands, no placement, no gRPC, no client |
| **Not met by design: crash consistency** | deferred (3a) | explicitly out of scope; the `untested` inventory entry keeps it visible |

## Exceptions to "gameplay files untouched"

`go/internal/game/turn.go` changed in five places, none a rule change: `doUpdate`/`doSegment` read and advance the schedule through `s.sched` and no longer persist timing themselves (runTurn does, after success); `doTurn` returns an error instead of silently returning when ships cannot be reloaded; a `Progress()` call per star and per flushed record; `flushAll` returns the joined errors instead of only logging them, and a failed update or segment flush fails closed (panic recovered into HALTED) instead of continuing; the 8-byte `persistTiming` writer is replaced by the JSON schedule record. `state.go` keeps the dispatch switch unchanged apart from the test-only panic hook and the `_ =` on the unknown-command `Send`. `commands*.go`, `combat.go`, `planet.go`, `ship.go`, `build.go`, `vn.go`, `helpers.go` and `model/*` are untouched. The authentication logic was moved verbatim into `authenticate` and now runs on the actor (interim CR-20 mitigation, documented in the handoff).

## Architectural discrepancies recorded for review (not changed in the plan)

1. Plan 11.5 says `/readyz` returns 200 in `UNINITIALIZED`; the brief asks that an uninitialised server not claim game readiness. Implemented as 200 with `game_ready: false` (so a future startup probe does not restart-loop an empty pod) and documented; the Phase 2 manifests should decide whether the Service uses `game_ready`.
2. Plan 11.5's graceful sequence aborts an unfinished turn "with nothing written"; turn code already writes ships, telegrams and news to the store during processing, so a mid-turn abort would not be "nothing written". Phase 1 therefore lets the turn finish within the budget and reports a timeout honestly; the abort-at-boundary semantics belong to the Phase 3a staging model.
3. Plan Phase 2 mentions `gbctl universe init --force --seed` for dev regeneration; Phase 1 deliberately ships no overwrite path (brief section 4, item 10). Phase 2 should implement regeneration as "delete the disposable volume, then init", or add a guarded operation then.
4. The plan lists `Authenticate` through the actor under Phase 3a (CR-20); Phase 1 moved it there early because the new concurrent login and update tests would otherwise race. The snapshot-based design of 3a still applies.

## Independent review remediation (GB1-01 to GB1-09)

Run on igor on 2026-10-29 after the corrections. Mapping of finding to code, root cause and test: `phase1-handoff.md` section 14.

### Regression suites added

| Finding | Tests | Result |
|---|---|---|
| GB1-01 | `internal/game/remediation_test.go` `TestGB101*` (8 tests) | PASS; with `-race` 15 consecutive runs and without `-race` 30 consecutive runs, no failure and no race report. The reply-window stress test is calibrated to the measured duration of one authentication and asserts that both "delivered" and "still running" outcomes occur (typical: 250/0/50 without `-race`, 160/0/140 with it). |
| GB1-02 | `internal/net/remediation_test.go` `TestGB102*` (5 tests) | PASS; 25 consecutive `-race` runs |
| GB1-03 | `TestGB103ResumePersistenceFailureHalts` | PASS; 15 consecutive `-race` runs |
| GB1-04 | `TestGB104*` (6 fast tests) | PASS; 25 consecutive `-race` runs. Production timing (`GB_LONG_TESTS=1`): `TestGB104LongCommandWithProductionTiming` PASS (65 s command, 67.0 s, `-race`); `test/e2e` `TestGB104RealEngineLongTurnKeepsHealthyClient` PASS (real binary, 70 s turn, 71.0 s) |
| GB1-05 | `test/e2e/exitcode_test.go` `TestGB105ListenerFailureExitCode`, `TestGB105PortBindingFailure`, `TestGB105OrdinarySigtermStillExitsZero` | PASS; 5 consecutive runs |
| GB1-06 | `cmd/gbctl/response_test.go` `TestGB106RejectsUnconfirmedReplies` (15 cases), `TestGB106AcceptsValidReplies` | PASS |
| GB1-07 | `internal/platform/metrics` `TestGaugeConcurrentAdd`, `TestGaugeConcurrentAddSetRead` | PASS; 50 consecutive `-race` runs |
| GB1-08 | commit-plan verification (below) | PASS |
| GB1-09 | `TestGB109SecondSignalForcesExit` (exit 6, logged, not a signal death) plus the existing `TestSigtermDuringTurnIsBounded` (first signal still exit 0) | PASS; 5 consecutive runs |

All regression tests run in the normal `go test ./...` and `go test -race ./...` (except the two 65/70-second `GB_LONG_TESTS` variants). They drive the production code: the real actor, bbolt store, `net.Server`, `Session`, writer goroutine, gorilla client, admin listener, `gbctl` `run` and the `gb-server` binary. The only substitute is the transport tests' fake engine, which has the actor's command semantics; the real-engine path is covered by the e2e long-turn test.

### Independent reproductions (Codex's own files)

Codex's reproduction tests (`/tmp/gb1-adversarial/go/**/review_*_test.go`) and process script (`/tmp/gb1-process-review.py`) were run unchanged against two scratch copies: the pre-remediation tree (a backup taken before any change) and the corrected tree. The only adaptation is in the corrected tree's registration test, which called the removed `setAuth`; it now builds the session with its identity and calls `register`, which is the corrected API. The process script was pointed at each copy's binary and at scratch output paths, so Codex's evidence files were not overwritten.

| Reproduction | Before | After |
|---|---|---|
| `TestReviewTimedOutAuthentication` (`-race`) | FAIL, DATA RACE at `state.go:623` | PASS |
| `TestReviewCanceledQueuedUpdate` | FAIL: "queued update executed AFTER caller timed out: updates 0 -> 1" | PASS |
| `TestReviewResumePersistenceFailure` | FAIL: "engine mode=SERVING, gameReady=true" | PASS |
| `TestReviewPanicDoesNotFlushMutatedMemory` | PASS | PASS |
| `TestReviewBroadcastDuringRegistration` (`-race`) | FAIL, DATA RACE | PASS |
| `TestReviewHealthyClientDuringLongCommand` (65 s) | FAIL: "close 1001 (going away)" at 65.0 s | PASS (67.0 s) |
| `TestReviewMalformedSuccessReply` | FAIL: `exit=0 stdout="update: ok"` | PASS |
| `TestReviewConcurrentGaugeAdd` | FAIL: "got 3437 want 20000" | PASS |
| `TestReviewRichMetrics` | PASS | PASS |
| process script: missing TLS files | exit **0** | exit **5** |
| process script: tokens, 410 routes, metrics, idle SIGTERM | as before (401, 410, text exposition, exit 0) | unchanged |

### GB1-08 commit plan verification

Scratch clone of the repository, base `a6e65ee`, the four groups of `phase1-handoff.md` section 11 applied cumulatively from the working tree and committed in the clone only. Every check passed at every commit:

| Commit | Files | Lint baseline lines | Tracked `go/cmd` files | Lint gate | CI entry-point guard (own `ci.yml`) | build, vet, gofmt, mod verify, tidy no-op | `go test` | `go test -race` | inventory check |
|---|---|---|---|---|---|---|---|---|---|
| 1 platform | 4 | 1132 (unchanged) | 4 | PASS | PASS | PASS | PASS | PASS | PASS |
| 2 universe | 7 + baseline | 1075 (57 retired) | 4 | PASS | PASS | PASS | PASS | PASS | PASS |
| 3 engine, transport, admin | 37 + baseline | 1059 (16 retired) | 8 | PASS | PASS | PASS | PASS | PASS | PASS |
| 4 e2e and documentation | 10 | 1059 | 8 | PASS | PASS | PASS | PASS | PASS | PASS |

The final scratch commit holds exactly the 303 tracked-or-untracked, non-ignored files of the working tree, byte-identical. The baseline shrinks only (73 retired lines in total, 1,132 to 1,059); they are legacy findings that disappeared because the code moved or was rewritten, not 73 independently fixed bugs.

### Gate observation, now corrected (GB1-10)

During the first remediation `test/scripts/golden-mutations.sh` failed intermittently, each time on a mutation whose expected message **was** present in the output; the cause (`echo "$out" | grep -qE` under `pipefail`, SIGPIPE on `echo` when `grep -q` exits early) was recorded here but the protected script was left unchanged. The second review reproduced the flake independently (GB1-10) and the owner authorised the tooling change; see "Second review remediation" below.

## Second review remediation (GB1-06R, GB1-10, GB1-11)

| Finding | Before (reproduced on the uncorrected tree) | Correction | After |
|---|---|---|---|
| GB1-06R `gbctl universe init` accepts invalid results | the reviewer's `TestReviewRejectWrongUniverseResult` against the uncorrected `go/internal/admin/client.go`: **FAIL**, all four results (`"not a universe record"`, `false`, `[]`, `{}`) accepted with `exit=0 stdout="universe init: ok\n..."` (`scratchpad/p1/gb106r-before.log`) | typed, operation-specific validation of the universe record against the server contract and the request (`universeRecordFromReply`, `validateUniverseRecord`); non-init operations reject a stray result; `gbctl` prints the validated record only | `TestReviewRejectWrongUniverseResult` PASS; `TestGB106RRejectsInvalidUniverseRecords` 29/29 exit 5 with empty stdout; `TestGB106RRejectsResultOnOtherOperations`, `TestGB106RAcceptsValidUniverseRecords`, `TestGB106RUniverseRecordValidation` PASS; `cmd/gbctl`, `internal/admin` and `test/e2e` (real `gb-server`) PASS with `-race` |
| GB1-10 mutation gate false failures | former pipeline on a 419 KB output with an early match: **100/100 false failures** (self-test, informational line) | here-string matcher `grep -E -- "$pat" <<< "$out" > /dev/null`, shared `check_outcome`, self-test hook; new `golden-mutations-selftest.sh` | self-test 1,200/1,200 as required (A early, B late, C no match, D expected failure with exact text, E unexpected success, F expected pass; `expect` and `expect_inv`); `golden-mutations.sh` 15/15 three times |
| GB1-11 stale shutdown summary | handoff unit 6 described `signal.NotifyContext` and exit codes 0 to 4 | summary rewritten (persistent registration, orderly first signal, exits 5 and 6, no clean-shutdown guarantee after a forced exit, CR-18 partial persistence), operator guide section 8 authoritative | no other stale description found in tracked docs or `gb-server` comments |

Full local validation after the three corrections: the "Checks on the Phase 1 tree" table above was re-run in this round (build, vet, gofmt, mod verify, tidy no-op, `go test`, `go test -race`, golden 1,395 = 1,249/146/0/0, fixtures, inventory, mutations, lint gate 1059/0/0, both self-tests, govulncheck 0 reachable, actionlint, growth gate with and without labels, `git diff --check`, scope audit). Repository state: branch `feature/gb-phase1-stabilization`, HEAD `a6e65ee9a769279b276602ed60fca67acf40cb21`, 0 commits ahead of `origin/master`, 0 staged files, 24 modified, 39 untracked.

### Commit plan re-verification (second round)

Scratch clone of the repository, base `a6e65ee`, the four groups of `phase1-handoff.md` section 11 (now including the GB1-06R test files in commit 3 and the GB1-10 script and self-test in commit 1) applied cumulatively and committed in the clone only. Every check passed at every commit:

| Commit | Files | Lint baseline lines | Tracked `go/cmd` files | Lint gate | CI entry-point guard (own `ci.yml`) | build, vet, gofmt, mod verify, tidy no-op | `go test` | `go test -race` | inventory check | mutation-gate self-test (20 iterations) | `golden-mutations.sh` |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 platform + mutation-gate fix | 6 | 1132 (unchanged) | 4 | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS 15/15 |
| 2 universe | 7 + baseline | 1075 (57 retired) | 4 | PASS | PASS | PASS | PASS | PASS | PASS | PASS | not run (commits 1 and 4 only) |
| 3 engine, transport, admin, GB1-06R | 39 + baseline | 1059 (16 retired) | 9 | PASS | PASS | PASS | PASS | PASS | PASS | PASS | not run |
| 4 e2e and documentation | 10 | 1059 | 9 | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS 15/15 |

The final scratch commit held exactly the 306 tracked-or-untracked, non-ignored files of the working tree, byte-identical, at the time of the run; the only file edited afterwards is this report (this results section), which belongs to commit 4. No commit, stash or ref was created in the real repository.
