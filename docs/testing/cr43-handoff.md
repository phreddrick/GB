# CR-43 handoff: durable maintenance mode and process readiness (ADR-0010)

Branch `feature/durable-maintenance-mode` (from `master` `194592c6`), 2026-11-08. Not deployed; independent review and hosted CI pending; acceptance on the development server needs its own authorisation.

## 1. Root cause

The Stage 3 image upgrade of 2026-11-08 (`docs/testing/phase2-validation.md` section 9.3, evidence `cr18-image-upgrade-2026-11-08`) synced successfully in Argo CD and then stalled: the quiesced Pod answered `/readyz` 503, Kubernetes 1.36's `OrderedReady` StatefulSet controller returns from `processReplica` while a Pod is not Running and Ready and never reaches the rolling-update deletion, so the old Pod stayed. Quiescing was also only an in-memory mode: the replacement (created after an owner-authorised guarded deletion) started `SERVING` and had to be quiesced again inside the ten-minute no-catch-up window.

## 2. Changes

| Area | Change |
|---|---|
| `internal/db/meta.go` | `MaintenanceRecord` (`meta/maintenance`, schema 1: `schema_version`, `quiesced`, `set_at`, `reason`, optional `engine_version`), strict decoding (`strictDecode`, `ErrCorruptMaintenanceRecord`), `GetMaintenance`, `PutMaintenance` (fenced, validates first), `ClearMaintenanceWithSchedule` (one transaction: delete the record, put the schedule; fenced), `SetMetaFault` (test hook) |
| `internal/game/admin.go` | `Quiesce` writes the record before changing the mode; a failed write still quiesces in memory and returns `ErrMaintenanceNotDurable` (HTTP 500, `gbctl` exit 5 with the explicit message); a repeat quiesce retries; `Resume` uses `ClearMaintenanceWithSchedule` and halts on failure (GB1-03), the record then stays. `Status` gains `game_ready` and `maintenance {durable, set_at, reason, error}` |
| `internal/game/state.go`, `maintenance.go` | startup: after the CR-18 validation, a valid record → `QUIESCED` with the schedule adopted as persisted (no overdue policy, no write); a malformed record → `HALTED` `CORRUPT_STORE: corrupt maintenance record …`; absent → unchanged behaviour |
| `internal/engine/engine.go` | `Mode.Ready()` includes `QUIESCED`; `Health.Maintenance`, `Health.MaintenanceDurable`; `Status.GameReady`, `Status.Maintenance`; `ErrMaintenanceNotDurable` |
| `internal/net/health.go` | `/readyz` 200 in `QUIESCED`; body members `maintenance`, `maintenance_durable`; `game_ready` unchanged (false while quiesced) |
| `internal/game/metrics.go` | `gb_maintenance_durable` |
| `cmd/gbctl` | human output: `game ready:` and `maintenance:` lines |
| `.github/scripts/kind-e2e.sh`, `ci.yml` step name | new steps: quiesce → Pod stays Ready → image tag change through kustomize → controller replaces the Pod (`SuccessfulDelete` event, orderly `QUIESCED` shutdown) → replacement starts `QUIESCED`, Ready, no turn, same PVC, counters and `next_update` unchanged → resume (protected gate tooling: label `gate-change-reviewed`) |
| Docs | ADR-0010, `local-server.md` sections 4, 5, 7, 7a, `deploy.md` sections 5.1 to 5.3 and 7, `go-code-review.md` 7.5 addendum (CR-43), `phase2-validation.md` 9.3, `CLAUDE.md` |

Not changed: game rules, turn processing, the commit path (`CommitWorld` never touches the record), the universe record, counters, the StatefulSet manifest, Services, Ingress, PodMonitor, `source/`.

## 3. Persistence format and compatibility

- `meta/maintenance` = `{"schema_version":1,"quiesced":true,"set_at":"<RFC3339>","reason":"operator quiesce (admin API)","engine_version":"<module@rev>"}`; strictly decoded like the commit and schedule records (presence of every required key, no unknown key, no JSON null, no trailing data) and validated (`schema_version 1`, `quiesced true`, non-zero `set_at`).
- Absent record: pre-CR-43 behaviour exactly (`TestStoreWithoutMaintenanceRecordKeepsStartupBehaviour`, with and without a CR-18 commit record).
- Forward: an engine without ADR-0010 ignores the extra key (bbolt bucket entries are independent) and starts `SERVING` as it always did; the record remains and a later ADR-0010 engine honours it. Rolling back the image therefore loses the durable quiesce but never the data.
- The record is outside `WorldDigest`; a quiesce or resume does not change the logical world state.

## 4. Crash consistency and ordering

| Point of failure | Store afterwards | Next start |
|---|---|---|
| crash before the quiesce's record transaction commits | no record | `SERVING` (the caller got no success; status after the start says so) |
| crash after the record committed, before the mode changed or the reply was sent | record | `QUIESCED` |
| record write fails | no record; engine `QUIESCED` in memory, `maintenance.durable false`, exit 5 to `gbctl` | `SERVING` (documented and reported, never silent); re-issue `quiesce` to make it durable |
| orderly shutdown while quiesced (shutdown commit) | record kept, new commit record of kind `shutdown` | `QUIESCED` |
| crash before the resume transaction commits | record and old schedule | `QUIESCED` |
| crash after the resume transaction committed | no record, rescheduled schedule | `SERVING` (the resume was durable; `next_update` is the rescheduled one) |
| resume transaction fails | record and old schedule; engine `HALTED` | `QUIESCED` |
| malformed record | untouched | `HALTED` `CORRUPT_STORE`, not Ready, nothing written |

The maintenance writes are ordinary fenced writes: a turn body cannot perform them (`TestMaintenanceWritesAreFencedAndIndependentOfCommits`), and a quiesce requested during a turn runs after the turn's commit on the actor (`TestQuiesceDuringTurnIsAppliedAfterTheCommit`).

## 5. Readiness by mode

| Mode | `/readyz` | `game_ready` | `/livez` |
|---|---|---|---|
| `STARTING` | 503 | false | 200 |
| `UNINITIALIZED` | 200 | false | 200 |
| `SERVING`, `TURN_IN_PROGRESS` | 200 | true | 200 (progress-aware) |
| `QUIESCED` | **200** (`maintenance true`, `maintenance_durable` when the record is held) | false | 200 |
| `HALTED` (incl. `CORRUPT_STORE`) | 503 | false | 200 |
| `STOPPED` / draining | 503 | false | 503 / 200 |

Startup and liveness probes are unchanged in the manifests; the startup probe is satisfied by the first 200, which a quiesced start gives at once.

## 6. Tests

`internal/db/maintenance_test.go` (4 tests, 14 malformed forms), `internal/game/maintenance_test.go` (8 tests), `internal/net/server_test.go` (`TestProbesPerMode` updated, `TestQuiescedReadinessSemantics`), `internal/game/lifecycle_test.go` (`TestQuiesceResume` updated), `go/test/e2e/maintenance_test.go` (`TestQuiesceSurvivesCrashAndRestart`: SIGKILL and SIGTERM after a quiesce, resume then SIGKILL), `go/test/e2e/basic_test.go` (quiesced readiness expectation updated), `.github/scripts/kind-e2e.sh` (hosted). Local results on igor, 2026-11-08: `go test ./...` ok, `go test -race` ok on `db`, `game`, `net`, `admin`, `gbctl`; lint gate 1057/1057 baseline, 0 new; `test_release_safety.py` 64 ok; `workflow_policy_check.py` 17 ok.

## 7. Acceptance criteria and evidence (development server, separately authorised)

1. Deploy the ADR-0010 image by the normal path (publish, digest pull request, one sync). Because the running server is quiesced on a pre-ADR-0010 image, this first rollout still needs the exceptional path of `deploy.md` section 5.2 (or a resume first); record it.
2. On the new image: `gbctl quiesce` → `gbctl status` shows `maintenance.durable true`; `/readyz` through the Ingress 200 with `game_ready false`; the Pod has no readiness `Unhealthy` events for at least two probe periods; `gb_maintenance_durable 1`.
3. A rollout while quiesced (the next image update, or an approved `kubectl rollout restart`) completes with a `SuccessfulDelete` event from the controller, no manual deletion, the replacement logging `starting QUIESCED`, counters and `next_update` unchanged, same PVC UID, Argo CD `Synced`/`Healthy`.
4. `gbctl resume` → `SERVING`, record gone, first segment commits.
5. Evidence: sealed directory with the status replies, probe bodies, controller events, old and new Pod logs, PVC and volume identities, and this document's table 4 row that applied.

## 8. Outstanding risks

- The kind proof runs only on hosted runners; it exercises the controller mechanics with kind's local-path RWO volume, not Longhorn RWOP attachment transitions (those were observed in Stage 3: reused on the same node).
- A quiesced Pod is now routable: players can connect and log in (CR-20) and receive the paused refusal. Acceptable for development with `GB_ENROLL_PREVIEW=false`; revisit with the player-access decision.
- Monitors keyed on `/readyz` for gameplay must switch to `game_ready` or `gb_engine_mode`.
- `gbctl` from an older revision cannot decode the new status (strict envelope); build it from the deployed revision as the runbook already requires.
- A `CORRUPT_STORE` start caused by a malformed maintenance record requires operator inspection of `meta/maintenance`; no repair tool exists (consistent with the commit-record policy).
