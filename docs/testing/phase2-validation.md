# Phase 2 validation record: `gb-dev` storage, network, RBAC and recovery evidence

> **Evidence record.** Sections 2, 3, 4 and 7 carry results recorded on 2026-11-03 from the owner-authorised disposable storage validation (P1 to P11, optional P7 skipped) and the direct-API RBAC verification; sections 5, 6 and 8 remain **NOT YET EXECUTED**. This file is the record that `docs/operations/storage-and-network-validation.md` (section 1.8), `deploy.md` (section 1) and ADR-0007 (decision 3) refer to. It was created on 2026-11-01 (Phase 2D Checkpoint 2) as a template with one filled section: the read-only Checkpoint 1 preflight. Nothing in it is a test result until an operator, under a separate authorisation, performs the operation and writes the observation here with its date, the command, the saved object files and the classification from the planning checklist. Do not fill a row from a version number, a manifest or a plausible expectation. Section 0.1 defines the three gates (before the Helm upgrade, before the first `gb-dev-platform` sync, before the first `gb-dev-server` sync); each needs recorded evidence and an explicit PASS decision, not a populated table. Section 6 (the recovery rehearsal) is deferred by design and is **not** a gate of the first server synchronisation; that deferral is not evidence of recovery readiness, and **no recovery of `gb-dev` is approved** (corrected 2026-11-01, review finding GB2D-02).

Outcome vocabulary (from the checklists): **PASS**, **FAIL**, **INCONCLUSIVE**, **BLOCKED**, **NOT YET EXECUTED**. A row that says anything but NOT YET EXECUTED must name its evidence file.

Evidence directory convention: a dated directory on the operator workstation (outside both repositories) holding saved objects (`-o yaml`), events, logs, the marker file and its checksum; values are read by a person, never sourced or evaluated by a shell. This file records the classification and the directory name, not credentials and not kubeconfig contents.

## 0. Status summary

| Section | Subject | Status (2026-11-03) | Blocks |
|---|---|---|---|
| 1 | Read-only cluster preflight (Checkpoint 1) | **DONE**, read-only facts only | nothing; informs everything below |
| 2 | RWOP same-node exclusion (Experiment 1) | **PASS** (RWOP EXCLUSION, SCHEDULER LEVEL; 2026-11-03, `P5-record.md`) | gate C (first `gb-dev-server` sync) |
| 3 | fsGroup non-root write on Longhorn (Checkpoint 0 of the RWOP procedure) | **PASS** (2026-11-03, `P4-record.md`) | gate C |
| 4 | Attachment handoff and persistence (Experiment 3; Experiment 2 optional) | **PASS** (PERSISTENCE OK; 2026-11-03, `P8-record.md`, `P9-record.md`); Experiment 2 **SKIPPED** by owner decision; cleanup and retirement complete (`P10a`, `P10b`, `P11`) | gate C |
| 5 | NetworkPolicy probes N1 to N13 | **NOT YET EXECUTED** | gate C (run after `gb-dev-platform` is synced) |
| 6 | Operational recovery rehearsal (`dev-volume-recovery.md`) | **NOT YET EXECUTED**; the checklist itself is NOT APPROVED FOR EXECUTION | any recovery of `gb-dev`; **not a gate** of the first server sync, and its deferral proves nothing about recovery |
| 7 | Controller RBAC, identity-verified against the direct API | **PASS** (2026-11-03, 504 SubjectAccessReviews through `https://10.0.0.13:6443`, `rbac-verification-matrix.md`) | gate B (`deploy.md` precondition) |
| 8 | Phase 2 acceptance A1 to A12 | **NOT YET EXECUTED** | Phase 2 exit |

### 0.1 Gates (GB2D-02)

A gate **passes** only when every item below it has a row in this file whose classification is PASS (or the named, explicitly approved alternative), each row names its evidence directory, and a named person writes the gate decision here with the date. A filled table, a version number, a rendered manifest or an offline validator result is not a PASS. Every gate is **OPEN (not passed)** as of 2026-11-01. Update 2026-11-03: the evidence for gate A is complete (the operations ran under per-step owner approval on 2026-11-02; the owner countersigns the decision cell at review); gates B and C stay OPEN with the remaining items named in their rows. Recording test results here is not deployment authorisation.

| Gate | Before | Required evidence | Decision |
|---|---|---|---|
| **A** | the shared Argo CD `helm upgrade` | namespace `gb-dev` exists, is `Active`, carries the restricted Pod Security labels, UID recorded (`bootstrap.md` section 5); the `k8s-helpers` change reviewed, merged and its Helm upgrade separately approved; the pinned chart, both values files and the rollback target identified; the needed CRDs (`middlewares.traefik.io`, `podmonitors.monitoring.coreos.com`) present at that moment | **Evidence complete 2026-11-02** (`gate-a-2026-11-02/gate-a-readiness-report.md`): namespace `gb-dev` created 14:51:17Z, UID `8c1be846-dba4-45a1-abf0-b49c0eece1fb`, restricted labels; `k8s-helpers` merged `af45523` (later renamed `gb-dev`, `68d45af`); Helm upgrade owner-approved: revision 13 (22:59Z) applied every object but the client was interrupted (`context canceled`), the identical retry recorded revision 14 (23:16Z) with no pod change; chart sha256 `dbb92a8c…`, rollback target 12, CRDs present. The shared release moved to revision 15 on 2026-11-03 (n8n onboarding) with the GB configuration preserved. Owner signature: pending |
| **B** | the first `gb-dev-platform` sync | AppProject `gb-dev` (`argocd/projects/gb-dev.yaml`; the name `gb` is reserved for future production and the AppProject `gb` created live on 2026-11-02 must be gone, see `bootstrap.md` section 4) and both Applications exist in `argocd` with project `gb-dev`, manual sync only, the four sync options and `Prune=false`; the Helm upgrade verified (components Ready, five existing Applications Synced / Healthy, no `forbidden` list error); section 7 PASS (controller permissions proven through the direct RKE2 API with impersonation shown to be honoured); `argocd app diff gb-dev-platform` shows additions only and `gb-dev` holds no unexpected object (no ownership conflict, nothing with another manager) | **OPEN.** Done: AppProject `gb-dev` created 2026-11-03 03:33Z (UID `c7bbb6a5-7179-403b-9e78-3ddea9f220db`), the old `gb` deleted 03:40Z by UID precondition, both Applications registered 03:45Z (`gb-dev-platform` UID `d8c10941-be51-47d9-a0f8-80050ee24a74`, `gb-dev-server` UID `f7febdc9-6497-4a3c-a2fb-fa2553323c2c`, OutOfSync / Missing, never synced); Helm upgrade verified; section 7 PASS. Remaining: `argocd app diff gb-dev-platform` additions-only review and the `gb-dev` no-unexpected-object check, then the owner's decision |
| **C** | the first `gb-dev-server` sync | section 2 PASS (RWOP same-node exclusion demonstrated) **or** the RWO fallback explicitly approved by the owner and committed before any GB claim exists; section 3 PASS (fsGroup and UID 65532 write); section 4 PERSISTENCE OK (handoff and persistence); section 5 all thirteen rows as expected after the platform sync; namespace, Secret `gb-server-secrets` (both keys), DNS record and IngressClass / issuer prerequisites verified read-only; `argocd app diff gb-dev-server` inspected and approved | **OPEN.** Sections 2, 3 and 4 are PASS (2026-11-03). Remaining: section 5 (N1 to N13), Secret `gb-server-secrets`, DNS record, prerequisite re-verification, `argocd app diff gb-dev-server`, owner decision |

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

**PASS: RWOP EXCLUSION, SCHEDULER LEVEL** (executed 2026-11-03; P1 to P6 each under a separate written owner approval; all times UTC).

| Item | Value |
|---|---|
| Date, operator, authorisation | 2026-11-03 04:36Z to 05:16Z; operator: the Claude Code session on the workstation `igor`, acting on per-step owner approvals (P1 namespace, P2 claim, P4 writer A, P5 contender, P6 contender deletion); kubectl against the direct RKE2 API `https://10.0.0.13:6443` (`system:admin`), never the Rancher proxy |
| Evidence directory | operator-held archive on `igor`: `/home/steve/gb-evidence/rwop-evidence-2026-11-03` (211 files, index in section 9); records `P1-record.md`, `P2-record.md`, `P3-record.md`, `P4-record.md`, `P5-record.md`, `P6-record.md` |
| Disposable namespace (name, UID) | `rwop-test`, UID `99c965b5-8009-4833-9a3f-60c7d82641e6`, created 04:36:02Z with `pod-security.kubernetes.io/enforce=restricted`, `enforce-version=latest`, `warn` and `audit` `restricted`; never `gb-dev` |
| Claim `rwop-probe` (UID), PV name, PV access modes, Longhorn volume access mode | PVC UID `3a448bed-c82e-46f4-bca3-861ce8b0476f`, created 04:45:31Z, `Bound` at 04:45:33Z (2 s; events ExternalProvisioning, Provisioning, ProvisioningSucceeded); PV `pvc-3a448bed-c82e-46f4-bca3-861ce8b0476f` (UID `1a0070f5-e047-495a-a6e0-2b39d3ffcf2c`), `accessModes: [ReadWriteOncePod]`, 1Gi, reclaim `Delete`, driver `driver.longhorn.io`, ext4; Longhorn volume `accessMode: rwop`, 1073741824 bytes, 2 replicas (`mgmt3`, `wkr4`), Longhorn v1.12.1, StorageClass `longhorn` (`pvc-rwop-probe.json`, `pv-pvc-3a448bed-c82e-46f4-bca3-861ce8b0476f.json`, `lhvolume-pvc-3a448bed-c82e-46f4-bca3-861ce8b0476f.json`) |
| Writer A: node, Pod UID, Ready time | node `backup`; Pod UID `68c16e5b-0c2d-4c89-9dc5-20889410e685`; created 04:57:00Z, Ready 04:57:16Z (16 s; `SuccessfulAttachVolume` 04:57:11Z); image `busybox:1.37.0@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec` (`pod-writer-a.json`) |
| Contender: pinned node, Pod UID, phase, verbatim scheduler or `FailedMount` event | `contender-same`, `nodeSelector: kubernetes.io/hostname: backup`, Pod UID `03ae2b9e-0a34-46a5-85cf-34551e87d6bf`, created 05:05:07Z; `Pending`, no node assigned, no container created, through the whole 120 s window (05:05:07Z to 05:07:17Z). `default-scheduler` `FailedScheduling` at 05:05:07Z, also the `PodScheduled=False (Unschedulable)` message, verbatim: `0/11 nodes are available: 1 node(s) unavailable due to PersistentVolumeClaim with ReadWriteOncePod access mode already in-use by another pod, 10 node(s) didn't match Pod's node affinity/selector. no new claims to deallocate, preemption: 0/11 nodes are available: 1 No preemption victims found for incoming pod, 10 Preemption is not helpful for scheduling.` The only eligible node was refused for the RWOP reason; the other ten only by the deliberate pin (`events-contender-same.json`, `pod-contender-same.json`) |
| `CONTENDER_STARTED` seen? | no: the log is empty because the container was never created (`contender-same.log`) |
| Classification (RWOP exclusion scheduler level / kubelet level / RWO-like / INCONCLUSIVE) | **RWOP EXCLUSION, SCHEDULER LEVEL** (no resource, affinity, image or admission error occurred; writer A stayed Running / Ready, 0 restarts; exactly one VolumeAttachment for the PV throughout, on `backup`; Longhorn attached, healthy, both replicas RW) |
| Contender deletion observed complete (UID compared) | P6: UID `03ae2b9e-…` re-read immediately before; API DELETE with `preconditions.uid` and `preconditions.resourceVersion: 250708329` at 05:16:11.361Z; explicit NotFound 0.2 s later; no Pod with that UID anywhere; namespace then held writer A only; attachment unchanged (`P6-record.md`, `p6-delete-response.json`) |

## 3. fsGroup non-root write (Checkpoint 0, 1.4)

**PASS** (executed 2026-11-03 04:57Z as P4; owner-approved; evidence `P4-record.md`, `writer-a.log`, `writer-a-stat-numeric.txt`).

| Item | Value |
|---|---|
| Writer A log: `uid=`, `/data` owner and mode, `WRITE_OK`, `sha256sum` line | `uid=65532 gid=65532 groups=65532`; `root:UNKNOWN 2775` (busybox cannot name gid 65532; a read-only `stat -c '%u:%g %a'` shows `/data` as `0:65532 2775`, the setgid, group-writable root Longhorn and the kubelet's `fsGroup` pass produced, and `/data/marker` as `65532:65532 644`, 48 bytes); `WRITE_OK`; `1f9cdf60a2089c9a15b7fd40469f9a8c39e920160f74281fdd34507a118e9921  /data/marker`; mount `/dev/longhorn/pvc-3a448bed-… on /data type ext4 (rw,relatime)`. Pod security context exactly the engine's (`runAsNonRoot`, 65532/65532/`fsGroup` 65532, `OnRootMismatch`, `RuntimeDefault`, no capabilities, read-only root filesystem); no init container, no `chown` |
| Checksum equals the recorded marker checksum? | yes: `marker.sha256` = `1f9cdf60a2089c9a15b7fd40469f9a8c39e920160f74281fdd34507a118e9921` for the 48-byte marker `rwop-probe-6c5e4151-84ae-4988-a132-179957b4bcc4` + LF (P3, generated with a CSPRNG UUID on the workstation, verified with `sha256sum -c` and `openssl dgst`) |
| Classification (PASS / FAIL (blocker for the non-root design) / INCONCLUSIVE) | **PASS**: the non-root design works without privilege on this Longhorn configuration |

## 4. Attachment handoff and persistence (Experiment 3, 1.7; Experiment 2, 1.6, optional)

**PASS: PERSISTENCE OK** (Experiment 3 executed 2026-11-03 05:24Z to 05:31Z as P8 and P9, each owner-approved; cleanup P10a, P10b and retirement P11 executed 06:16Z to 06:30Z, each owner-approved; evidence `P8-record.md`, `P9-record.md`, `P10a-record.md`, `P10b-record.md`, `P11-record.md`).

| Item | Value |
|---|---|
| E3-0 namespace holds only writer A | yes (after P6): exactly `writer-a` UID `68c16e5b-…`, Running / Ready on `backup`, 0 restarts; `contender-same` NotFound; no other Pod referenced the claim (`p8-preflight.txt`) |
| E3-1 writer A deletion observed complete; detachment confirmed (Kubernetes and Longhorn), time to detach | API DELETE with `preconditions.uid 68c16e5b-…` and `resourceVersion 250703530` at 05:24:05.316Z (normal 5 s grace, `Killing` event, no force); Pod gone (explicit NotFound, no Pod with the UID, 0 Pods) at 05:24:11.695Z (+6.4 s). Detachment by both sources: VolumeAttachment `csi-c074b60a9d17403f8ae413547fe20ef9e4d68f09cab06bb90ca5f731b8faccd5` (UID `9ae54519-803b-48ad-a9dc-90bcdbc21ec4`) gone at 05:24:21.741Z (+16.4 s; fresh list well-formed, 11 items, none for the PV); Longhorn `status.state: detached` at 05:24:22.079Z (+16.8 s; `currentNodeID` empty, robustness `unknown` with the engine stopped, both replicas stopped without failure, no fault, `Detached` event 05:24:22Z). Budget 180 s |
| Writer B: node, UID; `VERIFY_FIRST`, `sha256sum`, marker text | created 05:31:07.956Z only after E3-2; node `backup` (scheduler's choice under `workload.home-apps`), Pod UID `0607b95c-0833-4fdd-8eb9-60859822c71c`, Ready 05:31:21Z (13 s; `SuccessfulAttachVolume` 05:31:18Z; no RWOP, `FailedMount` or `FailedAttachVolume` message, so no stale holder remained); new VolumeAttachment of the same name (UID `8dd60957-790e-41af-96e7-7cf71ad07f96`) on `backup`; log, exactly three lines: `VERIFY_FIRST`; `1f9cdf60a2089c9a15b7fd40469f9a8c39e920160f74281fdd34507a118e9921  /data/marker`; `rwop-probe-6c5e4151-84ae-4988-a132-179957b4bcc4`; no `MARKER_MISSING`; `/data/marker` mtime still 04:57:16Z (the P4 write), no new file (`writer-b.log`, `writer-b-stat-readonly.txt`) |
| Persistence verdict (PERSISTENCE OK / FAIL / INCONCLUSIVE) | **PERSISTENCE OK**: hash and text equal the P3 marker files, compared on the workstation; the replacement writer issued no write |
| Experiment 2 (cross-node contender) performed? classification | **SKIPPED** by owner decision (optional P7); not PASS, not executed. The scheduler message of Experiment 1 already states a cluster-wide check |
| Cleanup checkpoints 1 to 8 (1.8), each observed; namespace retirement (1.10) | 1 claim identity re-verified; 2 writer B deleted by `preconditions.uid 0607b95c-…` / `resourceVersion 250726322` at 06:16:26.645Z, gone +6.3 s; 3 zero Pods; 4 VolumeAttachment gone +9.9 s; 5 Longhorn `detached` +10.2 s (P10a). 6 claim identity unchanged, zero consumers, zero attachments, snapshots 0, backups 0 (the volume existed 04:45Z to 06:22Z, between runs of the 02:30Z `daily-backup` job, so no automatic backup was ever taken); 7 PVC deleted by `preconditions.uid 3a448bed-…` / `resourceVersion 250695260` at 06:22:51.924Z, gone +0.16 s (`pvc-protection` released); 8 PV gone +2.23 s and Longhorn volume gone +2.40 s through the external-provisioner and Longhorn controllers, `Delete` event 06:22:52Z; residual engines, replicas, snapshots, backups, BackupVolumes, attachments and PVs for the volume all 0 (P10b). Namespace retirement (1.10): fresh inventory over all 198 listable namespaced kinds empty apart from Kubernetes defaults, identity equal to P1, owner approval recorded, inventory repeated at the final stop point, DELETE by `preconditions.uid 99c965b5-…` / `resourceVersion 250688916` at 06:30:14.795Z, NotFound at 06:30:23.135Z (+8.3 s), namespace count 41 to 40, nothing else changed (P11). Nothing of the test remains in the cluster |

**Decision (1.9): RWOP CONFIRMED.** Approved by the owner on 2026-11-03 after P11 completed: Experiment 1 showed scheduler-level exclusion of a same-node second Pod, Checkpoint 0 showed the non-root `fsGroup` write, Experiment 3 showed clean detachment and byte-exact persistence across a handoff. `ReadWriteOncePod` stays in the frozen `volumeClaimTemplates` of `gb-engine`; the RWO fallback is **not** activated. ADR-0007's status line records the decision. The result says nothing about crash-consistency (CR-18), node partitions or application-level corruption, and it is not deployment authorisation: gates B and C stay OPEN.

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

**PASS** (2026-11-03, about 03:50Z, after Helm revision 14 and before the Applications were registered). Method: `KUBECONFIG=~/.kube/rke2-admin.yaml` against the direct RKE2 API `https://10.0.0.13:6443` (TLS verified, identity `system:admin`), `kubectl auth can-i` with `--as` for `system:serviceaccount:argocd:argocd-application-controller` and `argocd-server`; impersonation shown to be honoured first (`auth whoami --as` named the controller; a non-existent ServiceAccount was refused `create namespaces`). 504 SubjectAccessReviews (482 in the first run plus 22 re-runs of named-resource and subresource checks with the `TYPE/NAME` and `--subresource` syntax after a tooling mistake on the first attempt), 0 unexpected results. Evidence: operator-held `/home/steve/gb-evidence/rbac-verification-2026-11-03/rbac-verification-matrix.md` (sha256 `badd0e96f9988d7c…`). Static cross-check the same day: the six `gb-dev` Roles and RoleBindings and the five widened observe Roles equal the deployed Helm render; every binding of either ServiceAccount is Helm-managed; no wildcard rule outside `argocd`. Nothing in this table was obtained through the Rancher proxy. Findings beyond the table: the controller's only cluster-wide grant is `namespaces get/list/watch` (pre-existing); the controller reads Secrets and ConfigMaps in `argocd` through the chart's own Role (pre-existing, unchanged); no write in `homebox`, `copilot`, `plex`, `jellyfin` or `ollama` was widened (their named Deployment, ConfigMap and Service grants are unchanged and other names are refused); the server has no write anywhere in `gb-dev`; neither account can reach namespace `gb`, `default`, `kube-system` or cluster-scoped workloads. The UI effect of the server's missing `namespaces/gb-dev` get stays an unverified usability question. If the direct kubeconfig cannot be used, write UNVERIFIED and the reason; do not substitute an inference from the Role objects.

| Check | Expected | Observed (direct API) | Date |
|---|---|---|---|
| `auth whoami --as=<controller>` names the controller | yes | yes (`system:serviceaccount:argocd:argocd-application-controller`, groups `system:serviceaccounts`, `system:serviceaccounts:argocd`) | 2026-11-03 |
| non-existent ServiceAccount: `create namespaces` | no | no | 2026-11-03 |
| `create statefulsets -n gb-dev` | yes | yes (also `patch`, `update`, `get`, `list`, `watch` on all eight kinds: 48/48 yes) | 2026-11-03 |
| `patch statefulsets -n gb-dev` | yes | yes | 2026-11-03 |
| `delete statefulsets -n gb-dev` | no | no (`delete` and `deletecollection` refused for all eight kinds: 16/16 no) | 2026-11-03 |
| `create secrets -n gb-dev` | no | no (`create`, `update`, `patch`, `delete` on Secrets, PVCs and Pods all refused; Roles and RoleBindings refused) | 2026-11-03 |
| `create persistentvolumeclaims -n gb-dev` | no | no | 2026-11-03 |
| `create statefulsets -n homebox` | no | no (all eight kinds, `create/patch/update/delete`, refused in the five existing namespaces: 160/160 no; the new read grants there allowed: 75/75 yes) | 2026-11-03 |
| `patch namespaces` | no | no (`create`, `update`, `patch`, `delete` on namespaces refused) | 2026-11-03 |
| server: `get namespaces/gb-dev` (`--as=system:serviceaccount:argocd:argocd-server`) | no (homebox is the only resourceName) | no (`namespaces/homebox` yes; `namespaces/gb-dev`, `namespaces/gb`, `namespaces/argocd` and `list` no; server reads in `gb-dev` 13/13 yes incl. `pods/log`; server writes in `gb-dev` 44/44 no) | 2026-11-03 |

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

## 9. Evidence index (operator-held archive, 2026-11-03)

The raw evidence is **not in Git**. It is an operator-held archive on the workstation `igor`, outside both repositories, with a checksum-identical scratchpad copy at the time of writing: `/home/steve/gb-evidence/rwop-evidence-2026-11-03` (211 files; the SHA-256 of the UTF-8 manifest sorted by filename, with each line formatted as `<file SHA-256><two spaces><filename><LF>` and no directory prefix, is `c336ebe96446d0be398a520a3883e4d95f1b08c86df859d692ffe2f7e455ba05`). It holds the manifests used (with sha256 files), client and server dry-run results, create and delete results with timestamps, DeleteOptions records, saved Pod, PVC, PV, VolumeAttachment and Longhorn volume/engine/replica objects (`-o json`), event listings, container logs, the marker and its checksum, and one record per step. A scan found no credentials, kubeconfig data, private keys, tokens or Secret objects in it. Key files and their sha256 prefixes:

| File | sha256 (first 16 hex) | What it establishes |
|---|---|---|
| `P1-record.md` | `81559148bc71359a` | namespace `rwop-test` created, UID `99c965b5-…`, restricted labels |
| `P2-record.md` | `b51a1d9137ebef44` | RWOP claim provisioned: PV `[ReadWriteOncePod]`, Longhorn `rwop`, 2 replicas, Bound in 2 s |
| `P3-record.md`, `marker.sha256` | `09c6e0319992009e`, `3efcfe9637646092` | the marker and its checksum `1f9cdf60a2089c9a15b7fd40469f9a8c39e920160f74281fdd34507a118e9921` |
| `P4-record.md`, `writer-a.log` | `7b09aa94cea81a40`, `67f399ce494af8f3` | non-root write as 65532, `/data` `0:65532 2775`, `WRITE_OK`, matching checksum |
| `P5-record.md`, `events-contender-same.json` | `604d41e4a3c93a5a`, `30c3d4b95d63e2eb` | same-node contender refused by the scheduler with the verbatim RWOP message; never started |
| `P6-record.md` | `d73d8210362af1f5` | contender removed by UID/resourceVersion preconditions |
| `P8-record.md` | `4348145821eae5f2` | writer A removed; VolumeAttachment gone +16.4 s; Longhorn `detached` +16.8 s |
| `P9-record.md`, `writer-b.log` | `0b3d067eb6a91cde`, `52a25bcbf2b9ceeb` | writer B reattached; `VERIFY_FIRST`, exact checksum and marker text; PERSISTENCE OK |
| `P10a-record.md` | `e47e4483c6787d31` | writer B removed; detachment re-proven |
| `P10b-record.md` | `fd3d0bc46ce1d0e2` | PVC deleted by preconditions; PV and Longhorn volume reclaimed by their controllers; no residue, no backup |
| `P11-record.md` | `55342043a79f801e` | namespace retired by preconditions after a repeated empty inventory |

Related operator-held records: `/home/steve/gb-evidence/rbac-verification-2026-11-03/` (section 7; `rbac-verification-matrix.md` `badd0e96f9988d7c…`, `matrix.psv` `821d124c86a1c5d5…`, `matrix-named.psv` `18a5419467f6162a…`) and `/home/steve/gb-evidence/gate-a-2026-11-02/` (`gate-a-readiness-report.md` `b1eff4a9b39451d4…`, `longhorn-preflight-2026-11-03.md` `9b1c9e918672d3fb…`). Reviewers who need a file ask the operator; the archive is not published.
