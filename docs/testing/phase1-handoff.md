# Phase 1 handoff for independent review

**Current result (2026-10-29): all seven Phase 1 work units are implemented and verified locally; the nine findings of the first independent Codex review (GB1-01 to GB1-09) are corrected with regression tests (section 14), and the three findings of the second review (GB1-06R, GB1-10, GB1-11; the second review confirmed GB1-01 to GB1-05 and GB1-07 to GB1-09 as resolved) are corrected (section 15). `docs/testing/phase1-validation.md` has the exact outputs. Nothing is committed, staged, pushed or deployed. Phase 1 is not declared complete: completion needs the Codex re-review, the authorised commits, the hosted CI run and the Docker image build.** This document does not approve the work for merging. Phase 1 does **not** make turn processing crash-atomic (CR-18 remains open, Phase 3a).

## 1. Repository state

| Item | Value |
|---|---|
| Branch | `feature/gb-phase1-stabilization` (local only, not pushed), created from `origin/master` |
| Baseline HEAD | `a6e65ee9a769279b276602ed60fca67acf40cb21` (merge of pull request #1, Phase 0); `origin/master` had not advanced |
| Working tree | 24 modified files, 39 untracked files (32 from Phase 1, 4 GB1 regression test files, 2 GB1-06R test files, 1 gate self-test script); nothing staged; `source/`, `k8s-deployment.yaml`, `Dockerfile`, both architecture documents and `test/golden/**` untouched; `git log origin/master..HEAD` empty |
| Baseline validation (before changes) | build, vet, gofmt, tests, race, govulncheck 0, lint gate 1132/0/0: all PASS |

## 2. Work units

| Unit | What was done | CR findings | Files |
|---|---|---|---|
| 1 Typed administrative messages and panic safety | `adminRequest` channel on the actor with buffered replies and context-bounded acceptance; `TriggerUpdate`, `TriggerSegment`, `Quiesce`, `Resume`, `InitUniverse`, `Enroll`, `Authenticate`, `WelcomeMessage` all run as typed requests; `runTurn` wraps turns, `handlePlayerCommand` wraps dispatch, both under `guard` (recover + stack + metrics) which moves the engine to `HALTED`; HALTED refuses mutating work, cannot be resumed, is never flushed; test-only panic verb and turn hook | CR-04, CR-22 (interim) | `internal/game/{admin,lifecycle,state,metrics,mode}.go`, `internal/engine/engine.go` |
| 2 Configuration and admin authentication | `config.Load` (defaults < JSON file < env), secrets only from env/files, insecure values refused, validation before the store opens, `GB_DATA_PATH` alias; admin HTTP listener on loopback with constant-time bearer token, routes `status`, `update`, `segment`, `quiesce`, `resume`, `universe/init`; old `/admin/*` password endpoints answer 410; `gbctl` skeleton (env/file token, exit codes, no leakage) | CR-06, CR-34, CR-35, CR-36 | `internal/config/{config,env,validate}.go`, `internal/admin/{server,client}.go`, `cmd/gbctl/main.go`, `cmd/gb-server/main.go`, ADR-0004 |
| 3 Health, readiness, operational state | engine modes (`STARTING`, `UNINITIALIZED`, `SERVING`, `TURN_IN_PROGRESS`, `QUIESCED`, `HALTED`, `STOPPED`) in an atomic; heartbeat every 10 s, progress counter per star and per flushed record; `/livez` (alive = heartbeat ≤ 60 s or turn progress within `GB_TURN_STALL_TIMEOUT`), `/readyz` per plan 11.5; both served from atomics, never through the queue; `/status` without player list | CR-02 | `internal/net/health.go`, `internal/game/lifecycle.go` |
| 4 Safe universe initialisation | `internal/universe` library (the former `makeuniv` generator with a seeded `*rand.Rand`, algorithm unchanged), `db.PutUniverse` writes universe plus `meta/universe` record in one transaction and refuses an initialised store inside the transaction; `State.InitUniverse` runs it on the actor through the single store handle, reloads, starts the schedule; `makeuniv` becomes a thin offline wrapper that refuses an initialised database (exit 3, `-skip-if-initialized`); no overwrite path | CR-05 | `internal/universe/{gen,init}.go`, `internal/db/meta.go`, `cmd/makeuniv/main.go` |
| 5 Enrollment locking and exposure | `handleEnroll` calls `State.Enroll` (actor, shared store handle); second `db.Open` deleted; disabled by default (`GB_ENROLL_PREVIEW`), 403 and no store access; preview responses say PREVIEW ONLY; new race added to the in-memory list | CR-03 (CR-07 and CR-09 unchanged in substance; see limitations) | `internal/net/server.go`, `internal/game/admin.go` |
| 6 Scheduling, shutdown, resource lifecycle | `meta/schedule` JSON record restored at start, persisted after init, every turn, resume and shutdown; no-catch-up policy for overdue turns; `signal.Notify` stays registered for the whole shutdown (GB1-09): the first `SIGINT`/`SIGTERM`/`SIGHUP` starts the orderly path (listeners stop → sessions closed with 1012 → `State.Stop`: draining flag, running turn finishes, flush like a turn boundary, schedule persisted → store closed), a second signal forces exit 6 at once with no clean-shutdown guarantee; exit codes 0 orderly, 1 configuration/startup, 2 halted (not flushed), 3 shutdown budget expired mid-turn, 4 flush or close error, 5 listener failed while serving (GB1-05), 6 forced; for 2, 3 and 6 the store may hold partially applied turn changes (CR-18); errors never hidden; the table in `docs/operations/local-server.md` section 8 is authoritative | CR-21, CR-24 | `internal/game/{schedule,lifecycle}.go`, `cmd/gb-server/main.go`, `internal/net/session.go` |
| 7 WebSocket limits, logging, metrics | `SetReadLimit`, read deadline refreshed by pongs, origin policy (none/same-origin/allow-list), one writer goroutine per session with write deadlines and a bounded outbound queue (overflow closes the session, the actor never blocks), 1012 on restart; `log/slog` JSON with the legacy `log` package bridged; dependency-free Prometheus text registry with engine and transport families | review 3.5 items, CR-23 (mitigated) | `internal/net/{ws,session,server}.go`, `internal/platform/{logging,metrics}` |

## 3. Important design decisions

1. **Shared types package.** `internal/engine` holds `Mode`, `Health`, `Status` and the sentinel errors so `internal/net` and `internal/admin` describe the engine without importing `internal/game` (which imports `net`). `game` aliases them.
2. **HALTED is terminal until restart.** A panic anywhere on the actor, a failed turn flush or a failed schedule write halts the engine; `resume` is refused; `Stop` discards memory and exits 2. The alternative (continue, or flush "what we have") was rejected as worse than losing unflushed changes.
3. **No mid-turn abort.** Turn code writes to the store while it runs, so an abort would not be "nothing written"; SIGTERM waits for the turn within the budget and exits 3 with a logged warning otherwise.
4. **Shutdown flush mirrors a turn boundary** (ships reloaded from the store, then stars/planets/races written from memory): an orderly restart preserves exactly what the next turn would have preserved, including the CR-17 caveat.
5. **Overdue turns are not caught up.** Rescheduled to now plus one interval, counted, logged; `gbctl update` runs a missed one on purpose.
6. **Readiness in UNINITIALIZED is 200 with `game_ready:false`** (plan 11.5) so an empty pod can be initialised through a port-forward without a startup-probe restart loop.
7. **Authentication, welcome and enrollment run on the actor** (bounded 10 s): the only way to make concurrent login/update tests race-clean without the Phase 3a snapshot; documented as interim for CR-20.
8. **Metrics without `client_golang`.** A 350-line text-exposition registry with declared label sets keeps the dependency set unchanged and makes cardinality bounded by construction.
9. **Enrollment stays disabled by default** (Decision 9); the preview path exists only so the CR-03 fix is testable.
10. **The old generator algorithm is kept byte-for-byte in behaviour**, only its randomness source and global state were made per-run; the lint baseline shrank because the moved code uses explicit bounded conversions and a single annotated `rand.New`.

## 4. Files added and modified

Modified (24): `.github/workflows/ci.yml` (entry-point guard lists the five commands), `test/scripts/golden-mutations.sh` (GB1-10: here-string matcher, shared `check_outcome`, self-test hook; protected gate tooling), `CLAUDE.md`, `KUBERNETES.md` (note only), `docs/architecture/adr/README.md`, `docs/testing/dependencies-2026-10.md`, `docs/testing/known-defects.md`, `docs/testing/lint-baseline.md`, `docs/testing/lint-baseline.txt` (1,132 → 1,059, reduction only), `go/.gitignore` (`/gbctl`), `go/Makefile`, `go/README.md`, `go/go.mod`, `go/go.sum` (x/term removed), `go/cmd/gb-server/main.go`, `go/cmd/makeuniv/main.go`, `go/internal/config/config.go`, `go/internal/game/state.go`, `go/internal/game/turn.go`, `go/internal/net/{auth,server,session,ws}.go`, `test/knowndefects.yaml` (one `untested` entry, CR-18).

Added: `go/cmd/gbctl/{main.go,main_test.go}`, `go/cmd/makeuniv/main_test.go`, `go/internal/admin/{server,client,admin_test}.go`, `go/internal/config/{env,validate,config_test}.go`, `go/internal/db/meta.go`, `go/internal/engine/engine.go`, `go/internal/game/{admin,lifecycle,metrics,mode,schedule,lifecycle_test}.go`, `go/internal/net/{enroll_page,health,server_test}.go`, `go/internal/platform/logging/{logging,logging_test}.go`, `go/internal/platform/metrics/{metrics,metrics_test}.go`, `go/internal/universe/{gen,init,universe_test}.go`, `go/test/e2e/basic_test.go`, the GB1 regression tests `go/internal/game/remediation_test.go`, `go/internal/net/remediation_test.go`, `go/cmd/gbctl/response_test.go`, `go/test/e2e/exitcode_test.go`, the GB1-06R tests `go/cmd/gbctl/universe_result_test.go` and `go/internal/admin/universe_result_test.go`, the GB1-10 self-test `test/scripts/golden-mutations-selftest.sh`, `docs/operations/local-server.md`, `docs/architecture/adr/0004-configuration-and-secrets.md`, `docs/testing/phase1-validation.md`, `docs/testing/phase1-handoff.md`.

Not changed: `source/**`, `k8s-deployment.yaml`, `Dockerfile`, `docs/architecture/go-code-review.md`, `docs/architecture/microservices-migration-plan.md`, `test/golden/**`, `test/ctool/**`, `test/scripts/**` other than `golden-mutations.sh` and the new self-test, `go/tools/**`, `go/internal/testutil/**`, `go/internal/{model,enroll,db/store.go,db/keys.go}`, every `go/internal/game/commands*.go`, `combat.go`, `planet.go`, `ship.go`, `build.go`, `vn.go`, `helpers.go`, `rand.go`.

## 5. Exact test results

See `phase1-validation.md`. Summary (re-run after the second-review remediation): build, vet, gofmt, mod verify/tidy PASS; `go test ./...` 14 packages `ok`; `go test -race ./...` 14 packages `ok`; golden 1,395 comparisons = 1,249 PASS, 146 XFAIL, 0 XPASS, 0 FAIL (unchanged); fixtures reproduce; inventory reconciled (1 record, 5 tested entries, 1 explicit untested); mutations 15/15 with the corrected script; mutation-gate self-test 12 cases x 100 iterations all as required; lint gate `1059/1059, new 0, stale 0`; lint and acknowledgment self-tests pass; govulncheck 0 reachable; actionlint clean; growth gate simulation: refuses without labels (1 addition, 0 modifications, 61 removals; protected `ci.yml`, `test/scripts/golden-mutations.sh`, `test/scripts/golden-mutations-selftest.sh`), passes with both labels.

## 6. Reproduction of the original defects

**CR-04 (admin update crash).** Before: `POST /admin/update {"password":...}` with `external_trigger` enqueued `&command{text:"@@update"}` with nil `sess`/`done`; `dispatchCommand` hit the default branch and called `Send` on a nil interface; exit status 2. The path is deleted: `config` no longer has `ExternalTrigger`/`UpdatePassword`, `/admin/update` returns 410, and the only way to trigger a turn is `State.TriggerUpdate(ctx)` → actor → `runTurn`. Positive proof: `TestAdminUpdateDoesNotCrash` and the e2e `gbctl update` with the process observed alive afterwards.

**CR-03 (enrollment lock timeout).** Before: `handleEnroll` called `db.Open(cfg.DBPath)` while the server held the file; bbolt's `flock` timed out after 5 s (`{"success":false,"message":"Database error","errors":["open db ...: timeout"]}`). Now there is one `db.Open` in the process (`main`); enrollment is a typed actor request on that handle. `TestEnrollmentDisabledDoesNotTouchStore` asserts the preview path completes in well under 2 s and the disabled path in under 1 s with no store mutation.

## 7. Evidence that startup does not overwrite an initialised universe

Startup calls `loadAll` and `IsInitialized` only (no generation code is reachable from `game.New`); `TestUninitializedStoreAndAdminInit` asserts zero stars after starting on an empty store; `TestAcceptanceScenario` starts the same database three times and checks seed 4242, 6 stars and the schedule each time; `db.PutUniverse` refuses inside its transaction when any star, a non-empty `StarData` or the universe record exists (`TestLegacyStoreWithoutRecordCountsAsInitialized` covers databases from the pre-Phase-1 `makeuniv`).

## 8. Evidence of schedule restoration and graceful shutdown

`TestScheduleRestoredAcrossRestart` (in-process), `TestAcceptanceScenario` steps 5 and 6 (SIGTERM with a connected client: close code 1012, exit 0, `shutdown: flushed` logged; restart shows identical `num_updates`, `num_segments`, `next_update`, `next_segment`, seed and star count), `TestIdleSigtermAndMetrics` (idle SIGTERM exit 0 in under 5 s), `TestSigtermDuringTurnIsBounded`, `TestShutdownTimeoutIsHonest` (exit 3 within the budget, warning logged, interrupted turn not counted; nothing asserted about consistency).

## 9. Security checks performed

Secrets never in logs, status, probes, metrics, admin replies or error text (unit tests and the e2e log scan); default secret refused; admin token constant-time compared on SHA-256 digests; admin routes absent from the player listener; loopback default for the admin listener; origin allow-list; read limit; write deadlines and bounded queues; config file rejects secret keys and unknown keys; `gbctl` never takes a token argument and redacts it from connection errors; `govulncheck` 0; `gosec` findings in Phase 1 code are zero apart from annotated test-only `exec` and fixture-credential sites and the `G304` operator-named files, each with a stated reason. Not done: login rate limiting, token revocation, moving the token out of the WebSocket URL (review 3.5; later phases).

## 10. Outstanding known limitations

- **CR-18**: non-atomic turn flush; a crash or budget timeout during a flush can leave a mixed-turn store. Inventory entry `persistence/non-atomic-turn-flush`, Phase 3a.
- **CR-17**: the shutdown flush, like every turn flush, writes in-memory planets over the six handler sites that read and write planets through the store directly.
- **CR-20** interim: logins, welcome and enrollment wait for the actor (10 s) and answer 503 during long turns.
- **CR-22** interim: HALTED requires a process restart; in-memory changes since the last flush are lost.
- **CR-09**: races created offline with `racegen` still need a restart to log in (preview enrollment through the actor does not).
- A player command queued behind a long turn waits up to two minutes to start; then it is withdrawn and the player is told it was not run (GB1-04 removed the read-loop blocking; the actor itself still runs one thing at a time).
- A second SIGINT/SIGTERM during shutdown forces exit 6 without the clean-shutdown guarantee (GB1-09, documented behaviour).
- Token accepted in the WebSocket URL query string; no login rate limit.
- `/readyz` is 200 in `UNINITIALIZED` (plan 11.5); consumers must read `game_ready`.
- `k8s-deployment.yaml` and `KUBERNETES.md` remain unusable and unchanged except for the note; Phase 2 replaces them.
- All gameplay findings (CR-07, CR-10 to CR-16, CR-19, CR-26, CR-27, CR-40 to CR-42, GD-01, GD-02) are unchanged.

## 11. Commit sequence (not executed; verified, GB1-08)

The original six-commit proposal did not build: its first commit removed `Config.ExternalTrigger` while the old `internal/net/server.go` still used it (Codex GB1-08). It is replaced by four commits whose boundaries follow the real import graph (`game` imports `net`; `net` imports the new `config` and `engine`; `engine` imports `db/meta.go`), so configuration, engine, game, transport, admin and the entry points cannot be split further without a commit that does not compile.

| # | Commit | Files |
|---|---|---|
| 1 | `Phase 1 platform: slog JSON logging, Prometheus registry, mutation-gate SIGPIPE fix` | `go/internal/platform/logging/{logging,logging_test}.go`, `go/internal/platform/metrics/{metrics,metrics_test}.go`, `test/scripts/golden-mutations.sh`, `test/scripts/golden-mutations-selftest.sh` (GB1-10; protected gate tooling, first so that every later commit is checked by the corrected gate) |
| 2 | `Phase 1 universe library: seeded generation, single-transaction init, offline makeuniv` | `go/internal/universe/{gen,init,universe_test}.go`, `go/internal/db/meta.go`, `go/cmd/makeuniv/main.go`, `go/go.mod`, `go/go.sum`, and the lint-baseline reduction for the moved generator (`docs/testing/lint-baseline.txt`, regenerated with `lintdiff -write-baseline`, reduction only) |
| 3 | `Phase 1 engine, transport and administration, with GB1-01..07/09 and GB1-06R remediation` | `go/internal/config/{config,env,validate,config_test}.go`, `go/internal/engine/engine.go`, `go/internal/game/{admin,lifecycle,lifecycle_test,metrics,mode,schedule,state,turn,remediation_test}.go`, `go/internal/net/{auth,enroll_page,health,server,server_test,session,ws,remediation_test}.go`, `go/internal/admin/{admin_test,client,server,universe_result_test}.go`, `go/cmd/gbctl/{main,main_test,response_test,universe_result_test}.go`, `go/cmd/gb-server/main.go`, `go/cmd/makeuniv/main_test.go`, `go/.gitignore`, `go/Makefile`, `.github/workflows/ci.yml`, `test/knowndefects.yaml`, `docs/testing/known-defects.md`, `docs/architecture/adr/{0004-configuration-and-secrets,README}.md`, and the remaining lint-baseline reduction |
| 4 | `Phase 1 end-to-end tests, validation and documentation` | `go/test/e2e/{basic,exitcode}_test.go`, `docs/operations/local-server.md`, `docs/testing/{phase1-handoff,phase1-validation,dependencies-2026-10,lint-baseline}.md`, `go/README.md`, `CLAUDE.md`, `KUBERNETES.md` |

Two constraints decided the boundaries beyond the import graph:

- `cmd/makeuniv/main_test.go` belongs to commit 3, not 2. The old CI guard requires exactly four tracked files under `go/cmd`, and the new guard requires `go/cmd/gbctl/main.go`; in commit 2 the test would fail the old guard and the new guard could not pass yet.
- `.github/workflows/ci.yml` belongs to commit 3, the first commit that adds tracked files under `go/cmd`.

**Verification (not just documentation).** `verify-commit-plan.sh` (session scratch, not part of the repository) cloned the repository, checked out `a6e65ee`, applied the four groups cumulatively from the working tree, committed each in the scratch clone, and ran at every commit: the lint gate (stale-only results regenerate the baseline in that commit; any new finding fails), this commit's own CI entry-point guard step (extracted from its `ci.yml`), `go build`, `go vet`, `gofmt -l`, `go mod verify`, `go mod tidy` as a no-op, `go test ./...`, `go test -race ./...`, `test/scripts/inventory-check.sh`, the mutation-gate self-test (20 iterations), and at commits 1 and 4 the full `golden-mutations.sh`. The final scratch commit was then compared file by file with the working tree. Results are in `phase1-validation.md`, sections "GB1-08 commit plan verification" and "Second review remediation". No commit, stash or ref was created in the real repository.

The pull request still needs the labels `known-defect-added` (CR-18 entry) and `gate-change-reviewed` (`ci.yml`, and now `test/scripts/golden-mutations.sh` plus the new `test/scripts/golden-mutations-selftest.sh`; section 15.2 states exactly what changed in the gate tooling).

## 12. Commands for independent verification

```sh
cd /home/steve/workspace/GB
git status --porcelain && git log --oneline origin/master..HEAD      # tree as described; no commits
cd go
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go mod verify
go test -count=1 ./...
go test -count=1 -race ./...
cd ..
make -C test/ctool check
test/scripts/inventory-check.sh
test/scripts/golden-mutations.sh
test/scripts/golden-mutations-selftest.sh 100     # GB1-10 helpers on large outputs; about 2 minutes
test/scripts/lint-gate.sh /tmp/lint.json
test/scripts/lint-gate-selftest.sh
test/scripts/gate-approval-selftest.sh
PR_LABELS_JSON='[]' GITHUB_EVENT_NAME=pull_request test/scripts/growth-gate.sh origin/master   # expect exit 1
PR_LABELS_JSON='["known-defect-added","gate-change-reviewed"]' GITHUB_EVENT_NAME=pull_request test/scripts/growth-gate.sh origin/master   # expect exit 0
(cd go && go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...)
(cd go && go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 ../.github/workflows/ci.yml)
git diff --check; git status --porcelain -- source k8s-deployment.yaml Dockerfile test/golden test/ctool   # empty
# GB1 regressions (also part of the runs above), and the two long production-timing reproductions:
(cd go && go test -count=1 -race -run 'TestGB10|TestGauge|TestReviewRejectWrongUniverseResult' ./internal/game ./internal/net ./internal/platform/metrics ./internal/admin ./cmd/gbctl ./test/e2e)
(cd go && GB_LONG_TESTS=1 go test -count=1 -run 'TestGB104LongCommandWithProductionTiming|TestGB104RealEngineLongTurnKeepsHealthyClient' ./internal/net ./test/e2e)   # about 70 s each
```

Manual scenario (optional, uses temporary paths only):

```sh
cd go && make all
export GB_JWT_SECRET="$(openssl rand -base64 48)" GB_ADMIN_TOKEN="$(openssl rand -base64 24)" GB_DB_PATH=/tmp/gb-review.db
./gb-server &                                # mode UNINITIALIZED
export GBCTL_TOKEN="$GB_ADMIN_TOKEN"
./gbctl status; ./gbctl universe init -seed 4242 -stars 10; ./gbctl universe init -seed 1   # second refused, exit 4
./gbctl update; curl -s localhost:8080/readyz; curl -s 127.0.0.1:8081/metrics | head
kill -TERM %1; wait %1; echo "exit $?"      # 0
./gb-server & sleep 1; ./gbctl status        # same seed, counters, schedule
kill -TERM %1; rm -f /tmp/gb-review.db
```

## 13. Classification

- **Verified fixes:** CR-02, CR-03 (lock), CR-04, CR-05, CR-06, CR-21, CR-24 (orderly path), CR-34, CR-35, CR-36; WebSocket limits; structured logs; metrics.
- **Partially verified / interim:** CR-22 (halt, not rollback), CR-20 (actor-side login), CR-23 (bounded queue; head-of-line blocking of a player's own read loop during turns remains), CR-09 (preview enrollment only).
- **Suspected problems worth a reviewer's eye:** the shutdown flush inherits CR-17's planet reversion; `Health` treats a turn that has not yet ticked progress as alive for `turn_stall_timeout` from its start; `commandWait` (2 min) is a constant, not configuration; the growth gate counts the baseline shrink as 61 distinct keys versus 73 occurrences (expected, occurrence-counted keys).
- **Deferred:** CR-18 and every gameplay finding; Phase 2 manifests; dependency bumps; login rate limit; query-string token.

## 14. Remediation of the independent Codex review (GB1-01 to GB1-09)

The review of 2026-10-29 returned **FAIL** with five High and four Medium findings. All nine are corrected in the working tree (still uncommitted). Each has a regression test that failed, or demonstrably could not pass, before the correction; Codex's own reproduction files were also re-run against a copy of the pre-remediation tree and against a copy of the corrected tree (`phase1-validation.md`, "Independent reproductions").

| ID | Sev. | Root cause | Correction | Regression tests | Remaining limitation |
|---|---|---|---|---|---|
| GB1-01 | High | Actor requests returned results through variables captured by the closure; a caller that timed out read them while the actor still wrote them (data race). A request whose caller had timed out stayed in the queue and ran later (an update ran after `gbctl` reported a timeout). | `internal/game/lifecycle.go` `submit`/`call`: results travel only through the buffered reply channel (exactly one send, by the actor). Each request and player command has an atomic state; one compare-and-swap decides between "the actor starts it" and "the caller cancels it": a canceled request is skipped forever, a started one runs to completion. Admission checks the context first. Errors say which case happened (`engine.ErrNotExecuted`, `engine.ErrStillRunning`, both wrapping `ErrEngineBusy`; metrics `gb_requests_canceled_total`, admin results `not_executed`/`still_running`). Authenticate, WelcomeMessage, Enroll, InitUniverse and the player command path were converted. | `internal/game/remediation_test.go`: `TestGB101AuthenticateTimeoutWhileActorBusy`, `...AuthenticateTimeoutRacesReplyDelivery`, `...CancellationPolicyIsDeterministic`, `...CanceledQueuedUpdateNeverRuns`, `...AdmissionRespectsCanceledContext`, `...UpdateStartedBeforeTimeoutCompletes`, `...WelcomeEnrollInitTimeouts`, `...CanceledQueuedCommandNeverRuns` | A started operation cannot be interrupted (by design: a turn writes while it runs). The caller of a still-running operation learns its outcome from status, metrics and logs, not from the timed-out call. |
| GB1-02 | High | `register` published a session, then `setAuth` wrote its identity under the registry lock, while `Broadcast`/`Notify`/`ConnectedPlayers` read the identity of copied session pointers after releasing the lock. | `internal/net/session.go`: the identity is a constructor argument (`newSession(..., Identity, ...)`) and immutable afterwards; `register` publishes session and identity in one locked step; `setAuth` is gone. The registry refuses registrations after `CloseAll` (`ErrRegistryClosed`), so an upgrade that completes during shutdown is closed with 1012 instead of staying open. | `internal/net/remediation_test.go`: `TestGB102RegistrationVersusBroadcast`, `...IdentityVisibleAtPublication`, `...RegistryRefusesAfterShutdown`, `...AuthFailureNeverRegisters`, `...DisconnectAndShutdownDuringRegistration` | none known |
| GB1-03 | High | `Resume` switched to `SERVING` and updated the schedule in memory before writing it; a failed write left the engine serving with `game_ready=true`. `persistSchedule` also published the new schedule to status before the write. | `internal/game/admin.go` `Resume` computes the new schedule on a copy, writes it (`writeSchedule`), and only then adopts it, publishes it and switches to `SERVING`. A failed write halts the engine (fail closed) and returns an error that says so. `persistSchedule` publishes only after a successful write. `runTurn` reports a halted engine as `ErrHalted` (wrapping `ErrNotServing`). Other transitions audited: `InitUniverse` and `runTurn` already halt on a failed schedule write; `Quiesce` writes nothing; orderly shutdown reports flush errors (exit 4). | `TestGB103ResumePersistenceFailureHalts` (disposable store closed under the engine): failure reported, `HALTED`, readiness false, status keeps the stored schedule, update and mutating command refused, no success metric or log, `Stop` returns `ErrHaltedNotFlushed`, reopened store unchanged | none known |
| GB1-04 | High | The WebSocket read loop waited for the actor to finish each command, so it could not process pong frames; a command or turn longer than the 60 s read deadline disconnected a healthy client. | `internal/net/ws.go`: the read loop only reads frames and enqueues commands into a bounded per-session queue (32); one command worker per session runs them in order (and sends the welcome first); the session's writer goroutine stays the only writer. A full queue refuses the command with a message (`gb_ws_commands_rejected_total`). Closing a session cancels its queued commands (never run) and waits for a started one. Timing is a `wsTiming` field so tests can shorten it; production values are unchanged (60 s read deadline, 30 s ping). | `internal/net/remediation_test.go`: `TestGB104HealthyClientStaysConnectedDuringLongCommand` (2 s command, 300 ms deadline, order preserved), `...UnresponsiveClientIsDisconnected`, `...CommandQueueLimit`, `...QueuedCommandCanceledByDisconnect`, `...ShutdownDuringLongCommandIsBounded`, `...ManySessionsNoGoroutineLeak`; long variants with production timing under `GB_LONG_TESTS=1`: `TestGB104LongCommandWithProductionTiming` (65 s command) and `test/e2e` `TestGB104RealEngineLongTurnKeepsHealthyClient` (real binary, 70 s turn) | A command queued behind a turn longer than two minutes is withdrawn (the player is told it did not run). |
| GB1-05 | High | After a listener failure, `run` fell through to `shutdown()` and returned its result, 0 after a clean cleanup. | `cmd/gb-server/main.go`: listener failure is tracked separately and the process exits **5** after the shutdown sequence (whose own result is logged as `shutdown_exit_code`); cleanup cannot overwrite it. A bind failure at startup stays exit 1. | `test/e2e/exitcode_test.go`: `TestGB105ListenerFailureExitCode` (missing certificate, missing key: exit 5, cleanup completed, store reopenable), `TestGB105PortBindingFailure` (player and admin ports: exit 1), `TestGB105OrdinarySigtermStillExitsZero` | none known |
| GB1-06 | Medium | `admin.Client` ignored body-read and JSON errors and treated any 2xx as success. | `internal/admin/client.go`: a 2xx reply must be one complete JSON object with exactly the envelope fields, `ok: true`, the requested operation, the engine status and (for init) the universe record; anything else is `ErrBadResponse`. `gbctl` reports it with exit 5 and "the outcome is unknown". | `cmd/gbctl/response_test.go`: `TestGB106RejectsUnconfirmedReplies` (15 cases: not JSON, empty, truncated stream, truncated object, array, wrong type, missing `ok`, `ok:false` on 200, wrong operation, no status, unknown field, trailing data, init without record, error codes kept), `TestGB106AcceptsValidReplies` (against the real admin listener) | The second review found the record check incomplete (only "non-null"): corrected as GB1-06R, section 15. |
| GB1-07 | Medium | `Gauge.Add` read and wrote the value under two separate lock holds. | `internal/platform/metrics/metrics.go`: `Set` and `Add` share one `update` that holds the family lock for the whole read-modify-write. Counters and histograms were already correct. | `internal/platform/metrics/metrics_test.go`: `TestGaugeConcurrentAdd` (20 x 1,000 = exactly 20,000, plus labelled children), `TestGaugeConcurrentAddSetRead` | none known |
| GB1-08 | Medium | The proposed first commit removed `Config.ExternalTrigger` while the old transport still used it. | Section 11 replaced by a four-commit plan derived from the import graph and the CI guard, and **verified** in a scratch clone (every commit builds, vets, is gofmt-clean, tidy, passes `go test`, `go test -race`, the lint gate and the inventory check; the final commit is byte-identical to the working tree). | `verify-commit-plan.sh` run (results in `phase1-validation.md`) | Commit 3 is large because the import graph allows nothing smaller that compiles. |
| GB1-09 | Medium | `ErrHaltedNotFlushed`, `main.go` and the operator guide claimed a halted shutdown leaves "the last completed flush"; a panic can follow partial turn writes. A second SIGTERM during an update killed the process by signal 15 because the handler was unregistered after the first signal. | Wording corrected everywhere to "the store may already contain partially applied turn changes (CR-18)". Signals stay registered during shutdown; a second SIGINT/SIGTERM logs a forced exit and exits **6** without waiting for the engine or closing the store, documented as having no clean-shutdown guarantee. The first-signal path is unchanged. | `TestGB109SecondSignalForcesExit` (second SIGTERM during an 8 s turn: exit 6 within 2 s, logged, no "shutdown complete", process not killed by a signal); the orderly path stays covered by `TestSigtermDuringTurnIsBounded` and `TestShutdownTimeoutIsHonest` | CR-18 itself is unchanged and deferred to Phase 3a. |

**Preserved.** CR-02, CR-03, CR-04, CR-05, CR-06/CR-34, CR-21, CR-24, CR-35/CR-36, bounded outbound queues, HALTED refusal, no flush of questionable memory, Prometheus exposition, existing-universe and legacy-store recognition, public enrollment off by default, the old player command protocol, gameplay formulas and `source/` are unchanged; their tests still pass. No dependency was added.

**New observation recorded, not concealed.** While auditing GB1-01 the same "runs after the caller gave up" pattern was found in the **player command** path (`HandleCommand`), not only in admin requests; it is corrected with the same state machine (`TestGB101CanceledQueuedCommandNeverRuns`).

## 15. Remediation of the second independent review (GB1-06R, GB1-10, GB1-11)

The second Codex review (2026-10-29) returned **FAIL** with no Critical or High finding: GB1-01 to GB1-05 and GB1-07 to GB1-09 were confirmed resolved by independent reproduction; two Medium findings and one Low finding remained. All three are corrected in the working tree, still uncommitted; nothing else was changed (the diff against the previous review tree is the files named below).

### 15.1 GB1-06R: `gbctl universe init` accepted any non-null result (Medium)

**Root cause.** `Response.confirm` only tested `r.Result == nil`, so a 2xx `universe.init` envelope whose `result` was a string, `false`, `[]` or `{}` printed `universe init: ok` and exited 0 (reproduced before the correction with the reviewer's `TestReviewRejectWrongUniverseResult`: all four accepted).

**Contract used (not invented).** The server's `universeInit` handler returns the `*db.UniverseRecord` that `universe.Init` wrote: `seed`, `stars`, `min_planets`, `max_planets`, `planets`, `generator`, `initialized_at`, `tool`, every field set (`internal/admin/server.go`, `internal/universe/init.go`, `internal/db/meta.go`). No other operation sets `result`.

**Correction** (`go/internal/admin/client.go`): `decodeEnvelope` also returns the raw `result` member; `doOp` takes a per-operation `resultCheck`; `UniverseInit` installs `universeRecordFromReply`, which requires a JSON object, decodes it into `db.UniverseRecord` with `DisallowUnknownFields`, and runs `validateUniverseRecord`: seed non-zero and equal to the requested seed; `stars`, `min_planets`, `max_planets` present and valid under `universe.Params.Validate`; each explicitly requested parameter echoed unchanged; `planets` within `stars x min_planets .. stars x max_planets`; `generator`, `initialized_at`, `tool` present. The validated `*db.UniverseRecord` replaces `Response.Result`; operations without a result check reject a non-null `result`. `go/cmd/gbctl/main.go`: success for `universe` additionally requires the typed record (defence in depth) and the human output prints the validated record's fields instead of re-encoding whatever arrived. Universe generation and the initialisation transaction are untouched.

**Tests** (all through `run()`, that is the production client path): `go/cmd/gbctl/universe_result_test.go`: `TestReviewRejectWrongUniverseResult` (the reviewer's reproduction, kept under its name), `TestGB106RRejectsInvalidUniverseRecords` (29 cases: string, `false`, `true`, `[]`, array of records, `{}`, `null`, number, seed only, seed as string, seed zero, seed/stars/min/max differing from the request, min/max/planets/generator/initialized_at missing, planets too few/too many, stars out of range, max below min, generator or tool blank, bad time, unknown member, nested record; each exit 5, empty stdout, outcome warning, no token), `TestGB106RRejectsResultOnOtherOperations`, `TestGB106RAcceptsValidUniverseRecords` (explicit parameters, server defaults, `-json`, and the admin listener with a complete record); `go/internal/admin/universe_result_test.go`: `TestGB106RUniverseRecordValidation`. The test fakes now return complete records, as the real engine does; the end-to-end suite (`TestAcceptanceScenario` and the exit-code tests) runs `gbctl universe init` against the real `gb-server` and still passes, which proves genuine replies are accepted.

### 15.2 GB1-10: false failures of the golden-mutation gate (Medium; protected gate tooling)

**Root cause.** `expect` and `expect_inv` in `test/scripts/golden-mutations.sh` checked `echo "$out" | grep -qE "$pat"` under `set -o pipefail`. When the pattern occurs early, `grep -q` exits at the first match while `echo` is still writing; `echo` takes SIGPIPE (or `write error: Broken pipe`) and the pipeline fails although the required message is present. The reviewer reproduced 2 false failures in 600 attempts on a 25 KB output; the new self-test reproduces it on 100 of 100 attempts with a 419 KB output whose match is on the first lines.

**Exact change** (the only edits to the script): the two matchers become `output_matches() { grep -E -- "$2" <<< "$1" > /dev/null; }` (here-string: grep consumes the complete input, no producer to kill; `--` protects patterns starting with a dash; ERE semantics unchanged); the duplicated assertion body of `expect` and `expect_inv` is one function `check_outcome` (same messages, same exit statuses, same `tail -20` diagnostics, now also via here-string); `run_inventory`/`expect_inv` moved next to `expect` so that all helpers precede a guard `if [ "${BASH_SOURCE[0]}" != "$0" ] && [ "${GOLDEN_MUTATIONS_LIB_ONLY:-}" = 1 ]; then return 0; fi`, which lets the self-test source the real helpers without running any scenario (a direct run is unaffected: the guard is false when executed). The fifteen scenarios, their mutations, patterns and expected statuses are byte-for-byte the same. No `|| true`, no removal of `pipefail`, no weakened pattern.

**Self-test** `test/scripts/golden-mutations-selftest.sh [iterations]` (new): sources the helpers, stubs the two runners with synthetic output of about 400 KiB, and checks, for `expect` and `expect_inv`, 100 iterations each of: A early match (expected failure, exit 1, message first), B late match (message last), C no match (must report `output lacks`), D expected failure with the exact golden message, E unexpected success (exit 0 where 1 was required: must report `exit 0, wanted 1`), F expected pass. Result: 1,200 of 1,200 as required; the former pipeline on A's input: 100 of 100 false failures (printed for information). The full script was run three times during this round (standalone, and at commits 1 and 4 of the commit-plan verification): 45 of 45 scenarios as required.

**Acknowledgment required.** `test/scripts/` is protected gate tooling: the growth gate now lists `.github/workflows/ci.yml`, `test/scripts/golden-mutations.sh` and `test/scripts/golden-mutations-selftest.sh` and requires `gate-change-reviewed` on the pull request. This is the second protected change of Phase 1 (the first is the entry-point guard in `ci.yml`). Golden fixtures and expected divergences are unchanged.

### 15.3 GB1-11: stale shutdown summary (Low)

Section 2, unit 6, described `signal.NotifyContext` and exit codes 0 to 4. It now describes the persistent signal registration, the orderly first-signal path, exit 5 (listener failure) and exit 6 (forced second-signal exit without a clean-shutdown guarantee), and states that for 2, 3 and 6 the store may hold partially applied turn changes (CR-18), with `docs/operations/local-server.md` section 8 as the authoritative table. A search of the tracked documentation and the `gb-server` source comments found no other 0-to-4 description; no crash-consistency claim was added anywhere.

### 15.4 What was not touched

The GB1-01 to GB1-05, GB1-07, GB1-08 and GB1-09 corrections (request cancellation and reply channels, immutable session identity, fail-closed resume, bounded command workers and ping handling, listener exit codes, gauge atomicity, second-signal exit) are unchanged; their tests pass in this round's full and race runs. The CR-18 `untested` inventory entry, `source/`, gameplay formulas, `Dockerfile`, `k8s-deployment.yaml`, the golden fixtures and expected files, `go/tools/**` and `go/internal/testutil/**` are unchanged.

