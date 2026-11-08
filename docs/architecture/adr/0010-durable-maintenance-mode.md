# ADR-0010: Durable maintenance mode and process readiness independent of game readiness (CR-43)

Status: Proposed 2026-11-08 (implemented on `feature/durable-maintenance-mode`; independent review and hosted CI pending; not deployed)

## Context

The first image update of the development server (2026-11-08, `docs/testing/phase2-validation.md` section 9.3) exposed two defects in the engine's maintenance behaviour, recorded as **CR-43**:

1. `/readyz` answered 503 while the engine was `QUIESCED`. The quiesce is the documented precondition for a safe restart (ADR-0009, `deploy.md` section 5), yet a quiesced Pod is `NotReady`, and the StatefulSet controller (`OrderedReady`, Kubernetes 1.36 `processReplica`: "waiting for Pod to be Running and Ready") never deletes a not-Ready Pod for a rolling update. The Argo CD sync therefore succeeded and the rollout stalled; the owner had to authorise a guarded manual Pod deletion (`/home/steve/gb-evidence/cr18-image-upgrade-2026-11-08/assessment/POD-REPLACEMENT-ASSESSMENT.md`).
2. `QUIESCED` lived only in memory. Every restart, including the one the rolling update performs, entered `SERVING` and armed the turn timer; the operator had a ten-minute no-catch-up window to quiesce again by hand. The persisted schedule record carried no mode, and nothing else did.

Both defects together made "quiesce, upgrade, verify, resume" impossible to perform through the GitOps path alone.

## Decision

1. **Durable maintenance record.** `gbctl quiesce` writes `meta/maintenance` `{schema_version: 1, quiesced: true, set_at, reason, engine_version?}` in its own bbolt transaction **before** the mode changes. Startup reads the record after the CR-18 commit and schedule validation: present and valid → the engine starts `QUIESCED`, adopts the schedule exactly as persisted (no overdue policy, no write, no missed-turn accounting) and arms no turn timer; absent → the pre-existing behaviour (overdue policy, `SERVING`); malformed (not strictly decodable, unknown field, `quiesced: false`, JSON null, zero time, wrong schema) → `HALTED` with reason `CORRUPT_STORE: corrupt maintenance record …`, alive, not Ready, nothing written, nothing repaired. `gbctl resume` deletes the record **in the same transaction** that persists the resumed (no-catch-up) schedule, then switches to `SERVING`.
2. **Failure semantics, never silent.** If the quiesce's record write fails the engine still stops turns and mutating commands (quiescing is a safety action) but the request returns `ErrMaintenanceNotDurable`, status reports `maintenance.durable=false` with the store error, `/readyz` omits `maintenance_durable`, `gb_maintenance_durable` is 0 and the log carries an ERROR; a repeated `gbctl quiesce` retries the write. If the resume's transaction fails the engine halts (as GB1-03 already required) and the record stays, so the next start is `QUIESCED`. A crash between the record write and the mode change restarts `QUIESCED` (the caller got no success, so it checks status). A crash between the resume transaction and the mode change restarts `SERVING` with the rescheduled times (the resume was durable). No path lets a restart enter `SERVING` because a write was lost.
3. **Readiness split.** `/readyz` (process readiness for the orchestrator) answers 200 in `SERVING`, `TURN_IN_PROGRESS`, `UNINITIALIZED` and now `QUIESCED`; 503 in `STARTING`, `HALTED` (including `CORRUPT_STORE`), `STOPPED` and while draining. Game readiness is the body's `game_ready` (true only in `SERVING`/`TURN_IN_PROGRESS`, not draining), the new `game_ready` field of the admin status, and the `maintenance` object of the status. `/livez` and the startup probe are unchanged: the startup probe is satisfied by the first `/readyz` 200, which a quiesced start now gives at once. Players reaching a quiesced Pod through the Service get the actor's "paused" refusal for mutating commands exactly as before; nothing is served that was not served before.
4. **Independence from CR-18.** The record is written only by `Quiesce`, outside turns, through the write fence (`PutMaintenance`, `ClearMaintenanceWithSchedule` are fenced like every ordinary write); `CommitWorld` neither reads, writes nor deletes it, so the shutdown commit performed while quiesced keeps it; it is outside `WorldDigest` (operational state, not world state). Commit records, counters, the universe record and turn processing are untouched.
5. **Not changed.** The no-catch-up policy (applied at resume instead of at a quiesced start), the admin API surface (two new status members only), the StatefulSet manifest (no `partition`, no `OnDelete`), the Services, the Ingress, the PodMonitor.

## Alternatives considered

- **A Kubernetes-side workaround** (`updateStrategy: OnDelete`, a `partition`, scaling to zero, or deleting the Pod by hand). Rejected as the normal path: each needs a manifest change or an imperative action outside GitOps, and none fixes the in-memory quiesce. Manual Pod deletion remains an exceptional, owner-authorised recovery action (`deploy.md` section 5).
- **A start-paused configuration flag** (`GB_START_QUIESCED`). Rejected: the operator's intent belongs with the store the intent protects, not with the Pod template; a flag would have to be flipped in Git twice per upgrade and could not express "this store was quiesced when the process stopped".
- **Storing the mode in the schedule record.** Rejected: the schedule record is written by every commit (`CommitWorld`) and by `restoreSchedule`; tying the quiesce to it would make commits responsible for maintenance state and would change a strictly validated record that older binaries read.
- **Reporting Ready only when the quiesce is durable.** Rejected: readiness is about the process; a server quiesced in memory only is healthy and must stay routable and replaceable; durability is reported where operators look (status, probe body, metric).

## Consequences

- A quiesced server survives Pod replacement quiesced: the documented upgrade sequence becomes quiesce → recovery point → sync → verify → resume, with no manual Pod deletion and no race against the schedule.
- Operators must read `game_ready` (or the admin status), not the HTTP code of `/readyz`, to know whether players are served. External monitors keyed on `/readyz` for gameplay availability must switch to `game_ready` or `gb_engine_mode`.
- A quiesced Pod is `Ready`, so Traefik routes players to it; they can log in (CR-20 unchanged) and receive the paused refusal for mutating commands. This is unchanged behaviour behind a previously closed door; it is acceptable for development and must be revisited with the player-access decision (`GB_ENROLL_PREVIEW`).
- A `gbctl` built from an older revision cannot decode the new status (`DisallowUnknownFields`); the operating rule "build `gbctl` from the deployed revision" (`deploy.md` section 3) already covers it.
- One more strictly validated record at startup: a malformed `meta/maintenance` is a `CORRUPT_STORE` start like a malformed commit record. The operator inspects and decides; nothing is auto-repaired.

## Validation (how we know the decision is being followed)

- `internal/db`: round trip, strict decoding (14 malformed forms), fence refusal and commit independence, fault hook (`maintenance_test.go`).
- `internal/game`: quiesce → orderly restart → `QUIESCED` with nothing written; quiesced start with 13-hour-overdue schedule runs no turn and writes nothing, resume applies the policy and removes the record in one step; resume → restart → `SERVING` and a turn commits; failed record write reported, retried, documented restart behaviour; stores without the record (with and without a commit record) unchanged; four malformed records halt without writing or repairing; failed resume halts and keeps the record; quiesce during a running turn applies after the commit without touching the fence (`maintenance_test.go`).
- `internal/net`: `/readyz` per mode (QUIESCED 200, HALTED and STARTING 503), draining override, `maintenance`/`maintenance_durable` body members (`server_test.go`).
- `go/test/e2e`: real binaries, SIGKILL and SIGTERM after a quiesce both restart `QUIESCED` and Ready, resume then SIGKILL restarts `SERVING`, turns refused while quiesced and committing after (`maintenance_test.go`).
- `.github/scripts/kind-e2e.sh` (hosted runner only): quiesce → Pod stays Ready → image tag change applied through kustomize → `rollout status` completes → `SuccessfulDelete` event from the controller (no manual deletion) → replacement starts `QUIESCED`, Ready, no `turn starting`, counters and `next_update` unchanged, same PVC UID → resume.
- Acceptance on the development server (separately authorised, `docs/testing/cr43-handoff.md`): the next image update must complete through Argo CD alone while quiesced.

## References

- CR-43 (`docs/architecture/go-code-review.md` section 7.5, addendum 2026-11-08); ADR-0009 (CR-18); GB1-03 (resume write ordering).
- Evidence: `/home/steve/gb-evidence/cr18-image-upgrade-2026-11-08/` (sealed; `UPGRADE-RECORD.md`, `assessment/POD-REPLACEMENT-ASSESSMENT.md`), `/home/steve/gb-evidence/pre-upgrade-recovery-point-2026-11-08/`.
- Kubernetes `release-1.36` `pkg/controller/statefulset/stateful_set_control.go` (`processReplica`, `updateStatefulSet`), `stateful_set_utils.go` (`newVersionedStatefulSetPod`, `computeReplicaStatus`), checksums in the evidence directory.
