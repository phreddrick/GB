# Phase 2 validation record: `gb-dev` storage, network, RBAC and recovery evidence

> **Evidence record. Every live experiment below is NOT YET EXECUTED.** This file is the record that `docs/operations/storage-and-network-validation.md` (section 1.8), `deploy.md` (section 1) and ADR-0007 (decision 3) refer to. It was created on 2026-11-01 (Phase 2D Checkpoint 2) as a template with one filled section: the read-only Checkpoint 1 preflight. Nothing in it is a test result until an operator, under a separate authorisation, performs the operation and writes the observation here with its date, the command, the saved object files and the classification from the planning checklist. Do not fill a row from a version number, a manifest or a plausible expectation. Section 0.1 defines the three gates (before the Helm upgrade, before the first `gb-dev-platform` sync, before the first `gb-dev-server` sync); each needs recorded evidence and an explicit PASS decision, not a populated table. Section 6 (the recovery rehearsal) is deferred by design and is **not** a gate of the first server synchronisation; that deferral is not evidence of recovery readiness, and **no recovery of `gb-dev` is approved** (corrected 2026-11-01, review finding GB2D-02).

Outcome vocabulary (from the checklists): **PASS**, **FAIL**, **INCONCLUSIVE**, **BLOCKED**, **NOT YET EXECUTED**. A row that says anything but NOT YET EXECUTED must name its evidence file.

Evidence directory convention: a dated directory on the operator workstation (outside both repositories) holding saved objects (`-o yaml`), events, logs, the marker file and its checksum; values are read by a person, never sourced or evaluated by a shell. This file records the classification and the directory name, not credentials and not kubeconfig contents.

## 0. Status summary

| Section | Subject | Status (2026-11-01) | Blocks |
|---|---|---|---|
| 1 | Read-only cluster preflight (Checkpoint 1) | **DONE**, read-only facts only | nothing; informs everything below |
| 2 | RWOP same-node exclusion (Experiment 1) | **NOT YET EXECUTED** | gate C (first `gb-dev-server` sync) |
| 3 | fsGroup non-root write on Longhorn (Checkpoint 0 of the RWOP procedure) | **NOT YET EXECUTED** | gate C |
| 4 | Attachment handoff and persistence (Experiment 3; Experiment 2 optional) | **NOT YET EXECUTED** | gate C |
| 5 | NetworkPolicy probes N1 to N13 | **NOT YET EXECUTED** | gate C (run after `gb-dev-platform` is synced) |
| 6 | Operational recovery rehearsal (`dev-volume-recovery.md`) | **NOT YET EXECUTED**; the checklist itself is NOT APPROVED FOR EXECUTION | any recovery of `gb-dev`; **not a gate** of the first server sync, and its deferral proves nothing about recovery |
| 7 | Controller RBAC, identity-verified against the direct API | **UNVERIFIED** (nothing to verify yet: no Role exists in `gb-dev`) | gate B (`deploy.md` precondition) |
| 8 | Phase 2 acceptance A1 to A12 | **NOT YET EXECUTED** | Phase 2 exit |

### 0.1 Gates (GB2D-02)

A gate **passes** only when every item below it has a row in this file whose classification is PASS (or the named, explicitly approved alternative), each row names its evidence directory, and a named person writes the gate decision here with the date. A filled table, a version number, a rendered manifest or an offline validator result is not a PASS. Every gate is **OPEN (not passed)** as of 2026-11-01.

| Gate | Before | Required evidence | Decision |
|---|---|---|---|
| **A** | the shared Argo CD `helm upgrade` | namespace `gb-dev` exists, is `Active`, carries the restricted Pod Security labels, UID recorded (`bootstrap.md` section 5); the `k8s-helpers` change reviewed, merged and its Helm upgrade separately approved; the pinned chart, both values files and the rollback target identified; the needed CRDs (`middlewares.traefik.io`, `podmonitors.monitoring.coreos.com`) present at that moment | OPEN |
| **B** | the first `gb-dev-platform` sync | AppProject `gb` and both Applications exist in `argocd` with project `gb`, manual sync only, the four sync options and `Prune=false`; the Helm upgrade verified (components Ready, five existing Applications Synced / Healthy, no `forbidden` list error); section 7 PASS (controller permissions proven through the direct RKE2 API with impersonation shown to be honoured); `argocd app diff gb-dev-platform` shows additions only and `gb-dev` holds no unexpected object (no ownership conflict, nothing with another manager) | OPEN |
| **C** | the first `gb-dev-server` sync | section 2 PASS (RWOP same-node exclusion demonstrated) **or** the RWO fallback explicitly approved by the owner and committed before any GB claim exists; section 3 PASS (fsGroup and UID 65532 write); section 4 PERSISTENCE OK (handoff and persistence); section 5 all thirteen rows as expected after the platform sync; namespace, Secret `gb-server-secrets` (both keys), DNS record and IngressClass / issuer prerequisites verified read-only; `argocd app diff gb-dev-server` inspected and approved | OPEN |

**Deferred, not a gate:** section 6 (the recovery rehearsal of `dev-volume-recovery.md`) remains separately authorised and NOT APPROVED FOR EXECUTION. Its deferral does not represent recovery readiness: CR-18 (non-atomic turn flush) stays open, `gb-dev` data is disposable until Phase 3a, and no unapproved recovery procedure may be run. No unexecuted experiment is marked passed anywhere in this file.

## 1. Read-only preflight, 2026-11-01 (Phase 2D Checkpoint 1)

Method: kubectl context `local` through the Rancher proxy (`https://rancher.home.spukhaus.com/k8s/clusters/local`), GET/LIST only; `dig`; `git`; the offline validation on a `git archive` export of `origin/master` `903c91c`. No object was created, changed or deleted; no Secret value was read.

| Area | Observed | Matches the design? |
|---|---|---|
| Repository | `origin/master` `903c91c83a612acf21557d915bd5522ba790b68b`; dev overlay pins `ghcr.io/phreddrick/gb-server@sha256:a2e433228760d9556e1824aad3fc4b09321a50d7dd8e225bf69e489a9432535a`; `run.sh --require-published-image` exit 0 (143 structural checks, 33 mutation cases + 6 baseline checks, safety-check 5/5, kubeconform 21/21); GHCR packages public, manifest digest confirmed by HEAD; no image-affecting change between the image's source commit `46b7bc0` and master | yes |
| Nodes | 11 Ready, none unschedulable or tainted, no pressure; `workload.home-apps=true` on exactly `backup` (4 CPU, 15.4 GiB, 9 % used) and `wkr1` (8 CPU, 7.7 GiB, 49 % used); `/healthz` ok; etcd and apiserver 3/3; Cilium v1.19.6 11/11, `enable-policy=default`, k8s NetworkPolicy on | yes |
| Longhorn | v1.12.1 (Helm rev 10), CSI attacher v4.12.0, provisioner v5.3.0, resizer v2.2.1, snapshotter v8.6.0 all ready; `CSIDriver` `fsGroupPolicy=ReadWriteOnceWithFSType`; SC `longhorn` Delete/Immediate/2 replicas/ext4, not default; all 11 Longhorn nodes schedulable; `backup` 393 GiB and `wkr1` 367 GiB free; 12 volumes, all `rwo`, 11 healthy, 1 detached (`ollama-data`, unrelated); **no RWOP volume was present in the inventory inspected on 2026-11-01** (an inventory does not show whether one ever existed); no GB PVC, PV, volume or VolumeAttachment | yes; behaviour unproven |
| Traefik, DNS, TLS | IngressClass `traefik` only and default; `rke2-traefik` DaemonSet 11/11 v3.7.11, providers kubernetescrd + kubernetesingress, no cross-namespace; LoadBalancer `10.0.0.20` from MetalLB pool `home-lab`; Middleware CRD v1alpha1; cert-manager v1.21.1, ClusterIssuer `letsencrypt-production-cloudflare` Ready; `gb-dev.home.spukhaus.com` **does not resolve** (Pi-hole `10.0.0.42` answers NOERROR, no address; `homebox` resolves to `10.0.0.20`); no GB Ingress or Certificate | DNS record missing |
| Argo CD | v3.5.6, chart 10.11.1, Helm rev 12; projects `default`, `homelab-apps`; five Applications, none automated; **no** `gb` project, GB Applications, `gb-dev` managed namespace, gb-dev Role or GB inclusions; repo Secret `repo-gitlab-k8s-helpers` only; controller ClusterRole `namespaces get/list/watch` only | integration missing (prepared in `k8s-helpers`) |
| Impersonation | through the Rancher proxy `kubectl auth whoami --as=<controller SA>` still reports the admin; a non-existent SA gets `yes` for `create namespaces`: **`can-i --as` through context `local` is not evidence** | method corrected in `bootstrap.md` section 4 |
| Namespace, Secret, workstation | `gb-dev` absent; `gb-server-secrets` absent; `gbctl` not built; `~/.config/gb` absent | prerequisites unmet |
| Monitoring, logging | Prometheus v3.8.1, `podMonitorNamespaceSelector` and `podMonitorSelector` `{}`; Prometheus pod labels `app.kubernetes.io/name=prometheus`, `prometheus=rancher-monitoring-prometheus`; CoreDNS pods `k8s-app=kube-dns`; Alloy DaemonSet 11/11 discovers every namespace; Loki healthy; port 8081 on no Service or Ingress | yes |
| Pre-existing GB state | none (namespace, StatefulSet, Pod, claim, volumes, attachments, Applications all absent) | fresh installation |

## 2. RWOP same-node exclusion (Experiment 1, `storage-and-network-validation.md` 1.5)

**NOT YET EXECUTED.** Requires separate authorisation (operations P1 to P6: disposable namespace, claim `rwop-probe`, writer A, one same-node contender, observed deletion).

| Item | Value |
|---|---|
| Date, operator, authorisation | |
| Evidence directory | |
| Disposable namespace (name, UID) | |
| Claim `rwop-probe` (UID), PV name, PV access modes, Longhorn volume access mode | |
| Writer A: node, Pod UID, Ready time | |
| Contender: pinned node, Pod UID, phase, verbatim scheduler or `FailedMount` event | |
| `CONTENDER_STARTED` seen? | |
| Classification (RWOP exclusion scheduler level / kubelet level / RWO-like / INCONCLUSIVE) | **NOT YET EXECUTED** |
| Contender deletion observed complete (UID compared) | |

## 3. fsGroup non-root write (Checkpoint 0, 1.4)

**NOT YET EXECUTED.**

| Item | Value |
|---|---|
| Writer A log: `uid=`, `/data` owner and mode, `WRITE_OK`, `sha256sum` line | |
| Checksum equals the recorded marker checksum? | |
| Classification (PASS / FAIL (blocker for the non-root design) / INCONCLUSIVE) | **NOT YET EXECUTED** |

## 4. Attachment handoff and persistence (Experiment 3, 1.7; Experiment 2, 1.6, optional)

**NOT YET EXECUTED.**

| Item | Value |
|---|---|
| E3-0 namespace holds only writer A | |
| E3-1 writer A deletion observed complete; detachment confirmed (Kubernetes and Longhorn), time to detach | |
| Writer B: node, UID; `VERIFY_FIRST`, `sha256sum`, marker text | |
| Persistence verdict (PERSISTENCE OK / FAIL / INCONCLUSIVE) | **NOT YET EXECUTED** |
| Experiment 2 (cross-node contender) performed? classification | |
| Cleanup checkpoints 1 to 8 (1.8), each observed; namespace retirement (1.10) | |

**Decision (1.9):** RWOP supported / RWO fallback proposed for owner approval: **not yet taken**. ADR-0007 status line to be updated from this section.

## 5. NetworkPolicy enforcement and the admin port (section 2, probes N1 to N13)

**NOT YET EXECUTED.** Runs in the real `gb-dev` namespace after `gb-dev-platform` is synced and before `gb-dev-server` is synced, with the throwaway target Pod.

| # | Source | Port | Expected | Observed | Result |
|---|---|---|---|---|---|
| N1 | Pod in another namespace | 8080 | timeout | | NOT YET EXECUTED |
| N2 | same Pod | 8081 | timeout | | NOT YET EXECUTED |
| N3 | unlabelled Pod in `gb-dev` | 8081 | timeout | | NOT YET EXECUTED |
| N4 | hostNetwork Pod on the target's node | 8081 | record | | NOT YET EXECUTED |
| N5 | hostNetwork Pod on another node | 8081 | timeout | | NOT YET EXECUTED |
| N6 | Traefik Pod | 8080 | 200 player | | NOT YET EXECUTED |
| N7 | Traefik Pod | 8081 | timeout | | NOT YET EXECUTED |
| N8 | Prometheus Pod | 8081 | 200 admin | | NOT YET EXECUTED |
| N9 | Prometheus Pod | 8080 | timeout | | NOT YET EXECUTED |
| N10 | target egress to github.com | any | timeout | | NOT YET EXECUTED |
| N11 | target `nslookup kubernetes.default` | 53 | resolves | | NOT YET EXECUTED |
| N12 | kubelet readiness probe | 8080 | Ready | | NOT YET EXECUTED |
| N13 | port-forward from the workstation | 8081 loopback | 200 admin | | NOT YET EXECUTED |

Target Pod IP, UID, node, and the removal of every throwaway Pod (observed): not recorded yet.

## 6. Operational recovery rehearsal (`dev-volume-recovery.md`)

**NOT YET EXECUTED, and the checklist is NOT APPROVED FOR EXECUTION.** Preconditions before any rehearsal: an independent review of the checklist, a throwaway StatefulSet in a disposable namespace, and an owner-approved written plan. CR-18 remains: a failed process or interrupted turn may leave partially persisted game state; no procedure here makes that consistent. Record here only when all three preconditions are met.

| Item | Value |
|---|---|
| Review of the checklist (date, reviewer, outcome) | |
| Rehearsal (date, disposable StatefulSet, case A/B/C exercised, evidence directory) | |
| Outcome | **NOT YET EXECUTED** |

## 7. Controller RBAC, identity-verified (bootstrap.md section 4)

**UNVERIFIED.** No Role exists in `gb-dev` yet. When the `k8s-helpers` change is deployed, record the output of the verification block of `bootstrap.md` section 4 run against the direct RKE2 API (`auth whoami --as` naming the controller; a non-existent ServiceAccount refused; the expected yes/no matrix). A result obtained through the Rancher proxy is not evidence and must not be written here. If the direct kubeconfig cannot be used, write UNVERIFIED and the reason; do not substitute an inference from the Role objects.

| Check | Expected | Observed (direct API) | Date |
|---|---|---|---|
| `auth whoami --as=<controller>` names the controller | yes | | |
| non-existent ServiceAccount: `create namespaces` | no | | |
| `create statefulsets -n gb-dev` | yes | | |
| `patch statefulsets -n gb-dev` | yes | | |
| `delete statefulsets -n gb-dev` | no | | |
| `create secrets -n gb-dev` | no | | |
| `create persistentvolumeclaims -n gb-dev` | no | | |
| `create statefulsets -n homebox` | no | | |
| `patch namespaces` | no | | |
| server: `get namespaces/gb-dev` (`--as=system:serviceaccount:argocd:argocd-server`) | no (homebox is the only resourceName) | | |

## 8. Phase 2 acceptance (deploy.md section 8, A1 to A12)

**NOT YET EXECUTED.** A12 (recovery rehearsal) is deferred by design and is not an acceptance item of the first synchronisation.

| # | Result | Date | Evidence |
|---|---|---|---|
| A1 | NOT YET EXECUTED | | |
| A2 | NOT YET EXECUTED | | |
| A3 | NOT YET EXECUTED | | |
| A4 | NOT YET EXECUTED | | |
| A5 | NOT YET EXECUTED | | |
| A6 | NOT YET EXECUTED | | |
| A7 | NOT YET EXECUTED | | |
| A8 | NOT YET EXECUTED | | |
| A9 | NOT YET EXECUTED | | |
| A10 | NOT YET EXECUTED | | |
| A11 | NOT YET EXECUTED (the offline part passed on 2026-11-01 for `903c91c`; the deployed-commit part waits for a deployment) | | |
| A12 | deferred | | |
