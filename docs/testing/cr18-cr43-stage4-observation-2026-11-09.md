# CR-18 / CR-43: Stage 4 post-resume observation (2026-11-09 UTC)

**Status:** Operator-reported evidence summary; **CR-18 post-resume PASS**, **CR-43 resume PASS**, **overall Stage 4 PASS WITH CONDITIONS**. This document records observations and a follow-up list. It does **not** change any known-defect inventory entry, close a finding, change a Gate decision, or authorize further live operations.

**Source of evidence:** Operator's sealed directory on Igor: `/home/steve/gb-evidence/cr43-stage4-resume-2026-11-09/` (reported 81 files, `STAGE4-RECORD.md`). This Git-only documentation change did not retrieve or independently revalidate those host-local evidence bytes. Reference the sealed record for raw outputs and checksums; do not commit credentials or private runtime dumps.

## Context and before state

- Dev Application: `gb-dev-server`, synced at Git revision `4acc145e3abb372ff643773a2c2ee3f3e7a713ef`.
- Container digest: `sha256:d62c1fe6af937b1da9aa661dbf3783a265ae16274135ccc21edd4fbc7211f251` (CR-18 + CR-43).
- Pod: `gb-engine-0`, UID `02152c5f-1a17-42d1-b40b-46f2d144479f`.
- Game QUIESCED with `maintenance.durable=true`; 17 updates / 1 segment; CR-18 commit sequence 2; queue empty, no turn running.
- Universe: seed 4242, 100 stars, 627 planets.
- Recovery baseline: `gb-dev-gb-engine-cr43-baseline-17-1-20261109-0032utc` snapshot and `backup-gb-dev-gb-engine-cr43-baseline-17-1-20261109-0032utc` full backup; earlier 17/0 recovery point also preserved.
- Operator reported 14/14 preflight checks passing.

## Authorized resume and first natural turn

| UTC event | Observed result |
|---|---|
| 2026-11-09 00:39:46 | Exactly one `gbctl resume`, exit 0. Mode SERVING, maintenance record absent, `game_ready=true`; counters still 17/1, commit sequence 2. |
| Shortly after resume | `/readyz` HTTP 200, `gb_maintenance_durable=0`, `gb_engine_mode{SERVING}=1`; Kubernetes Pod Ready, Argo CD Synced/Healthy. |
| 2026-11-09 00:42:33.262 | **Full update**, not a movement segment, began about 2 ms after its scheduled time. |
| About 643 ms later | Full update succeeded; counters advanced **17/1 → 18/0**, commit sequence **2 → 3**, metric `gb_commits_total{update,ok}` incremented. No fenced writes, panics, HALTED state, uncertain commits or error lines were reported. |
| 150-second post-commit observation | No new error logs; readiness healthy. Engine left SERVING. |

**Why this was an update:** at resume, the pending update time (00:42:33Z) was still in the future but the prior segment was overdue. The documented no-catch-up policy moved the segment to resume +10 minutes, then clamped it to the pending update boundary. The first scheduled turn was consequently a *full update*. This is the stated reason for the Stage 4 **PASS WITH CONDITIONS** classification rather than a nominal segment-counter progression. It was anticipated by the acceptance brief and should not be relabeled a segment or treated as an unexplained defect.

The normal successful commit verifies an operational commit, **not** crash-atomicity under power loss. CR-18 crash semantics rely on prior fault-injection and subprocess tests. Preserve those qualifications.

## Reported final state

- SERVING; `game_ready=true`, maintenance record absent, no turn running, command queue empty.
- 18 updates / 0 segments; commit sequence 3; universe identity unchanged.
- Next reported segment 00:52:33Z; next reported update 01:12:33Z (these are historical observations, **not** current schedule guarantees).
- New image running; Pod Ready with zero restarts, StatefulSet revisions matched; Argo CD Synced/Healthy.
- Longhorn attached and healthy with two running replicas; PVC and PV identities unchanged; all four snapshots and four backups preserved with original UIDs.
- No mutating operation beyond the single authorized resume and naturally scheduled turn.

## Acceptance and follow-ups

| Item | Current recorded assessment | Next action |
|---|---|---|
| CR-18 post-resume successful atomic commit | **PASS**, operator-reported, commit seq 3 | Owner may separately review the sealed evidence and authorize updating the appropriate inventory/handoff classification. **Do not close or rewrite an inventory entry in this documentation-only PR.** |
| CR-43 durable resume behavior (handoff item 4) | **PASS**, maintenance removed atomically with resumed schedule as observed | Preserve status, metrics and engine evidence; no additional restart just to retest. |
| CR-43 quiesced readiness (item 2) | Previously observed PASS on dev, supported by this stage's healthy Ready state after resume | Keep the process/game readiness distinction in client/API designs. |
| CR-43 automatic StatefulSet rollout while **durably** quiesced on real Longhorn (item 3) | **DEFERRED / NOT YET VERIFIED LIVE** | Test at the **next legitimate, separately approved image upgrade**. Hosted kind test passed but uses local-path RWO, not Longhorn RWOP. |
| CR-43 first deployment zero-turn criterion | **FAILED historically** (unexpected segment advanced 17/0 to 17/1 before quiesce) | Keep failure and its root-cause record intact; do not retroactively mark PASS. The valid 17/1 baseline was accepted and backed up. |
| Broader Phase 2 gameplay acceptance | Separate unexecuted or pending checks remain | Do not infer that all Phase 2 exit criteria have passed merely because Stage 4 passed. |

**Safety:** No new code, manifest, image, GitOps, Kubernetes or Longhorn operation is requested or authorized by this record.
