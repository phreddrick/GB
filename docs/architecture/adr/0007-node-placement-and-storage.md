# ADR-0007: Node placement, Longhorn persistence and the single-writer volume (ReadWriteOncePod with a declared fallback)

Status: Proposed 2026-10-30 (Phase 2B draft for owner review; RWOP **unvalidated** on the cluster)

## Context

Verified read-only on 2026-10-30: Longhorn v1.12.1 with CSI provisioner v5.3.0 and attacher v4.12.0, `CSIDriver` `fsGroupPolicy: ReadWriteOnceWithFSType`, StorageClass `longhorn` (reclaim `Delete`, `Immediate` binding, 2 replicas, ext4, daily backup recurring job), `node-down-pod-deletion-policy: do-nothing`, `auto-salvage: true`; every existing PVC and PV is `ReadWriteOnce`, no RWOP volume has ever been created on this cluster. Longhorn added RWOP support in v1.11.0. Kubernetes 1.36 (RWOP GA since 1.29). All 11 nodes are Ready and **none is tainted**, control-plane nodes included. `workload.home-apps: "true"` is on `backup` (4 CPU, 15 GiB, nearly idle) and `wkr1` (8 CPU, 7.7 GiB, about half its memory in use); `wkr4` is the GPU node; `wkr3` has 92 % of its memory committed in limits. bbolt tolerates exactly one writer; CR-18 (non-atomic turn flush) is open until Phase 3a.

## Decision

1. **Placement by the convention label, chosen by the scheduler.** `nodeSelector: workload.home-apps: "true"`, requests 100m/256Mi, limits 1 CPU/1Gi, no affinity, no hostname pin, no tolerations. The selector is mandatory on this cluster: without it a pod may land on a control-plane node. The scheduler's least-allocated scoring makes `backup` the expected home; `wkr1` remains eligible, which the drain acceptance test needs. Extending the label to other workers is a `k8s-helpers` change.
2. **Storage identity frozen with the StatefulSet** (ADR-0005, plan 12.3 item 5): `volumeClaimTemplates[0]` is `name: world`, `storageClassName: longhorn`, `accessModes: [ReadWriteOncePod]`, `5Gi`; `persistentVolumeClaimRetentionPolicy: {whenDeleted: Retain, whenScaled: Retain}`; `podManagementPolicy: OrderedReady`; `replicas: 1`. The PVC is therefore `world-gb-engine-0`, mounted at `/data/world`, with `GB_DB_PATH=/data/world/gb.db`.
3. **RWOP is the design but not yet a claim.** Before the first `gb-dev` PVC exists, the authorised throwaway test in `docs/operations/storage-and-network-validation.md` must show: a second pod is refused the volume cluster-wide, the attachment moves when the first pod is deleted, and the mount is group-writable by 65532 through `fsGroup` without an init container. The result is recorded in this ADR (status line) and in the plan.
4. **Declared fallback.** If RWOP fails on this cluster, the frozen access mode becomes `ReadWriteOnce` **before any PVC is created** (it is immutable afterwards). RWO is weaker: it is enforced per node, so a second pod on the same node could mount the volume. Single-writer semantics then rest on the StatefulSet (one pod per ordinal, `OrderedReady`, never a Deployment), `replicas: 1` enforced by the validator, bbolt's exclusive file lock (5 s timeout, exit 1), and the rule that nothing else ever references `world-gb-engine-0`. The gap is recorded; the advisory owner lease of plan 11.3 arrives in Phase 3a.
5. **Non-root writes without privilege.** `fsGroup: 65532` with `fsGroupChangePolicy: OnRootMismatch` relies on Longhorn's `ReadWriteOnceWithFSType` policy to make the ext4 mount group-writable; the process (UID 65532) creates the database file itself. No `chown`, no init container, no `hostPath`.
6. **Reclaim and protection.** The `longhorn` class deletes the volume with the PVC; the retention policy protects the PVC from StatefulSet deletion and scaling; `k8s-helpers` rules (no finalizer, non-cascading Application delete) protect it from Argo CD. Nothing protects against `kubectl delete pvc`, which is therefore the explicit, owner-authorised step of the regeneration procedure (ADR-0008). A `Retain` class or PV patch is a production concern.
7. **Node loss stays manual.** `do-nothing` is kept (plan Decision 30): a failed node leaves the pod `Terminating` and the volume attached until an operator confirms the node is down and force-deletes the pod (`docs/operations/deploy.md`, node-loss section). Acceptable for disposable development data.
8. **Persistence claim, stated exactly.** Longhorn replicates blocks synchronously; an orderly shutdown (exit 0) leaves a consistent file that survives restarts and node moves. A crash or kill mid-flush leaves a mixed-turn database on every replica alike (CR-18). Replication does not make the multi-transaction flush atomic and the daily backup job copies whatever state exists. `gb-dev` data is disposable until Phase 3a.

## Alternatives considered

- `nfs` (the cluster default class): rejected for a bbolt file (locking and fsync semantics).
- `longhorn-ollama` or a new `longhorn-retain` class: `Retain` is desirable for prod, not needed for a disposable dev volume; a new class is a `k8s-helpers` change deferred to Phase 8.
- Pinning to `backup` by hostname: rejected; it defeats the drain test and the convention.
- WaitForFirstConsumer binding: the `longhorn` class is `Immediate` and data locality is disabled, so binding before scheduling costs nothing here.
- Running as root with a `chown` init container: rejected (plan 11.6, PSS `restricted`).

## Consequences

- The access mode decision is a one-way door per PVC; the runbook sequence puts the RWOP test before the first server sync for that reason.
- A second universe is a second StatefulSet (plan 4.1 item 10), not a second replica; multi-universe naming is deferred (Phase 2B Decision D).
- Memory limits of 1Gi are a starting value; the plan's 200m/2 CPU engine numbers apply after measurement in Phase 3a.

## Validation

`deploy/kustomize/tests/validate.py` (identity golden file, `replicas: 1`, node selector, no affinity/tolerations, fsGroup and security context, data path inside the mount, grace period above the shutdown budget). Live: the RWOP and fsGroup throwaway test; three orderly restarts and one drain in the Phase 2 acceptance.

## References

Migration plan 11.1, 11.3, 11.4, 11.6, 11.8, Decisions 5 and 30 (as amended 2026-10-30); Phase 2A assessment sections 4 and 7; `docs/operations/local-server.md` section 10 (CR-18); inventory entry `persistence/non-atomic-turn-flush`.
