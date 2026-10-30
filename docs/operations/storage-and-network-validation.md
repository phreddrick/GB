# Storage and network validation in a disposable namespace: operator planning checklist

> **NOT APPROVED FOR EXECUTION. PLANNING CHECKLIST ONLY.** This document retains the independently reviewed **design** of the ReadWriteOncePod (RWOP), NetworkPolicy and port-forward experiments (GB2B-02, GB2B-02R) as a checklist of operations, their order, their outcome classification and their approvals. It contains **no runnable sequence**: no shell block, no deletion command, no cleanup script. The executable helper library that earlier revisions sourced was retired in Phase 2B (2026-10-30, `docs/testing/phase2b-handoff.md` section 12.9). The live experiments and their cleanup belong to a **separately authorised operational-validation stage**, which decides how each operation is issued, records every observation in `docs/testing/phase2-validation.md` and updates the status line of ADR-0007. Every operation that creates or deletes a Kubernetes object is a separately approved operator action under the `k8s-helpers` rules; nothing has been run.

Why before the first sync: the tests use a disposable namespace, a disposable claim and uniquely named Pods only, never `world-gb-engine-0`, and they must complete with their results recorded **before** `gb-dev-server` is synced for the first time, because the access mode and the network fence cannot be changed afterwards without recreating the StatefulSet.

## 1. ReadWriteOncePod on Longhorn 1.12.1

### 1.1 What is being distinguished

| Property | Meaning | How the design shows it |
|---|---|---|
| **RWOP** | only one Pod may use the claim for read/write access, **including a second Pod on the same node** | Experiment 1: a same-node contender is refused |
| **RWO** (the fallback, weaker) | a second Pod **on the same node** may be able to use the claim; a second node is refused by the attach/detach controller | Experiment 1 would admit the contender; Experiment 2 shows the node-level refusal |
| **Persistence** | data written by the first writer is present, unchanged, after a legitimate handoff | Experiment 3: the replacement writer verifies the marker and checksum before any write |

Expectation going in: Longhorn supports RWOP since v1.11.0 (installed v1.12.1, CSI provisioner v5.3.0, attacher v4.12.0, Kubernetes 1.36). Status on this cluster: **unvalidated**; no RWOP volume has ever existed here.

### 1.2 Outcome classification (decided before anything runs)

| Observation at a checkpoint | Classification |
|---|---|
| contender `Pending`; its events name `ReadWriteOncePod` (scheduler `VolumeRestrictions`: "node has pod using PersistentVolumeClaim with the same name and ReadWriteOncePod access mode", or equivalent wording) | **RWOP exclusion, scheduler level** |
| contender scheduled, stuck `ContainerCreating`, `FailedMount` event saying the volume is already used by another pod (kubelet RWOP check) | **RWOP exclusion, kubelet level** (still RWOP; record which layer) |
| contender scheduled on another node, `FailedAttachVolume` / `Multi-Attach error` | **node-level attach exclusion** (what RWO gives; by itself not proof of RWOP) |
| contender `Running` with the volume mounted while the writer still runs | **no exclusion** (RWO-like behaviour; RWOP not effective) |
| contender `Pending` for `Insufficient cpu/memory`, `didn't match Pod's node affinity/selector`, taints, image pull errors, claim still `Pending` | **INCONCLUSIVE**: scheduling or resource problem unrelated to the access mode; fix and repeat |
| claim never binds; provisioner events mention the access mode | **RWOP unsupported by the provisioner** |
| a read of the API fails, times out, or returns something other than a well-formed object or list of the expected kind (for example a VolumeAttachment list whose items are not `VolumeAttachment` objects with a boolean `status.attached`) | **INCONCLUSIVE** for that checkpoint; never read as "detached", "gone" or "zero" |
| a detachment or deletion has not been observed when its bounded budget ends | **INCONCLUSIVE**; never a pass, never a reason to proceed |

Every wait is bounded. A timeout without one of the named observations is INCONCLUSIVE.

### 1.3 Operations and their order

| Id | Operation | Approval | Checkpoint before the next |
|---|---|---|---|
| P1 | create the disposable namespace (name of the form `rwop-test`), labelled `pod-security.kubernetes.io/enforce=restricted` | **[approval]** | namespace Active; it is not `gb-dev` |
| P2 | create the disposable claim `rwop-probe` (manifest below) | **[approval]** | `Bound` within 180 s, else classify from provisioner events; PV access modes `[ReadWriteOncePod]`; Longhorn volume access mode `rwop`; claim UID, PV name recorded |
| P3 | generate the marker on the workstation and record it with its SHA-256 in the evidence directory (both outside the claim) | none | files present |
| P4 | create writer A | **[approval]** | 1.4 |
| P5 | create exactly one same-node contender | **[approval]** | 1.5 |
| P6 | delete the same-node contender object (recorded UID) | **[approval]** | observed gone; namespace holds writer A only |
| P7 | optional: create exactly one cross-node contender; delete it (recorded UID) | **[approval]** each | 1.6; observed gone; writer A only |
| P8 | delete writer A (recorded UID) | **[approval]** | observed gone; detachment confirmed by both sources within the budget (1.7) |
| P9 | create writer B (read-only command) | **[approval]** | 1.7 checksum comparison |
| P10 | cleanup: delete writer B (recorded UID), then the claim (recorded UID), each separately, each observed | **[approval]** each, after the owner has seen the results | 1.8 |
| P11 | namespace retirement | **[owner]**, separate | 1.10 |

Evidence directory: a dated directory on the workstation holding the marker file, its checksum file, every saved object (`-o yaml`/`-o json`), every events listing, every log, and a table of recorded identities (node of writer A, UID of every Pod created, claim UID, PV name). Values are read from the saved output by a person; **no value is assigned to a shell variable, exported, or written to a file that a shell sources or evaluates**.

Claim manifest (data):

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: rwop-probe
spec:
  accessModes: [ReadWriteOncePod]
  storageClassName: longhorn
  resources:
    requests:
      storage: 1Gi
```

Pod template used by every test Pod (only `NAME`, the node pin, `MARKER_VALUE` and `COMMAND` differ). Same security context as the real engine Pod, so the fsGroup behaviour is the one the deployment relies on. Image pinned by digest when run. All Pods use the default priority class, so none can preempt another (RWOP preemption only applies to higher-priority Pods).

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: NAME
spec:
  restartPolicy: Never
  terminationGracePeriodSeconds: 5
  nodeSelector: {workload.home-apps: "true"}      # Experiments 1 and 2 replace this with kubernetes.io/hostname: <recorded node>
  securityContext: {runAsNonRoot: true, runAsUser: 65532, runAsGroup: 65532, fsGroup: 65532, fsGroupChangePolicy: OnRootMismatch, seccompProfile: {type: RuntimeDefault}}
  containers:
    - name: probe
      image: busybox:1.37.0@sha256:<digest read at run time>
      env: [{name: MARKER, value: "MARKER_VALUE"}]
      command: ["sh", "-c", "COMMAND"]
      securityContext: {allowPrivilegeEscalation: false, readOnlyRootFilesystem: true, capabilities: {drop: [ALL]}}
      volumeMounts: [{name: data, mountPath: /data}]
      resources: {requests: {cpu: 10m, memory: 16Mi}, limits: {cpu: 100m, memory: 64Mi}}
  volumes: [{name: data, persistentVolumeClaim: {claimName: rwop-probe}}]
```

Container commands per role (these run inside the test Pod, not on the workstation):

| Pod | `COMMAND` |
|---|---|
| `writer-a` | `id; stat -c '%U:%G %a' /data; printf '%s\n' "$MARKER" > /data/marker && echo WRITE_OK; sha256sum /data/marker; sleep 100000` |
| `contender-same`, `contender-other` | `echo CONTENDER_STARTED; ls -l /data; sleep 100000` (it must never reach this line under RWOP) |
| `writer-b` (replacement) | `echo VERIFY_FIRST; if [ ! -f /data/marker ]; then echo MARKER_MISSING; sleep 100000; fi; sha256sum /data/marker; cat /data/marker; sleep 100000` (**reads only**; no write before the operator has compared the checksum) |

### 1.4 Checkpoint 0: writer A (P4)

- [ ] writer A Ready within 180 s; its node and UID recorded; the claim UID recorded.
- [ ] log shows `uid=65532`, `/data` owned by `root:65532` (or `65532:65532`) with group write, `WRITE_OK`, and a `sha256sum` line whose hash equals the recorded checksum (hash only; the path differs). Anything else: STOP; fsGroup or the mount is not behaving as the deployment assumes (record, classify INCONCLUSIVE for RWOP and as a **blocker** for the non-root design).
- [ ] the VolumeAttachment list, saved whole, is well-formed and contains exactly one attachment for the PV, on writer A's node.

### 1.5 Experiment 1: one same-node contender (P5, P6)

Exactly one contender, pinned with `kubernetes.io/hostname` to **writer A's recorded node**, so the scheduler cannot sidestep the question by picking another node.

- [ ] bounded wait for Ready (120 s) is **expected to expire**; Pod state, events and log saved.
- [ ] classify with 1.2. **RWOP pass:** `Pending` with the RWOP scheduler message, or `ContainerCreating` with the kubelet "already used by pod" `FailedMount`, and no `CONTENDER_STARTED`. **RWO-like:** `CONTENDER_STARTED` appears (the volume was mounted twice on that node). **INCONCLUSIVE:** any unrelated scheduling reason (the pinned node must have room for a 16Mi Pod; if not, that is the reason and the experiment is void).
- [ ] P6: the contender's UID is compared with the record immediately before its deletion; the deletion is observed complete within a bounded wait (60 s); the namespace then holds writer A (recorded UID) and nothing else.

A contender still `Terminating` at the end of the wait, a failed or malformed read, a different UID under the same name, or any other Pod in the namespace is a **STOP**: Experiments 2 and 3 do not start while any contender object exists, because a pending contender would race the replacement writer for the volume. A `Pending` Pod normally deletes immediately; resolve, re-check, then continue.

### 1.6 Experiment 2 (optional, separate): one cross-node contender (P7)

Only after 1.5 is fully cleaned up. The contender is pinned to the other `workload.home-apps` node (recorded).

- [ ] bounded wait for Ready expected to expire; events saved; the VolumeAttachment list still shows exactly one attachment for the PV.
- [ ] **RWOP:** `Pending` with the RWOP scheduler message (the scheduler check is cluster-wide). **RWO fallback behaviour:** scheduled, then `FailedAttachVolume`/`Multi-Attach error` (node-level refusal). Either way `CONTENDER_STARTED` must not appear.
- [ ] cleanup exactly as in 1.5 with this contender's recorded UID; the same STOP conditions apply.

### 1.7 Experiment 3: legitimate handoff and persistence (P8, P9)

Checkpoints in order, each proven before the next:

- [ ] **E3-0** exactly `writer-a` (recorded UID) exists in the namespace and nothing else (a pending or terminating contender would race the replacement).
- [ ] **E3-1** writer A's UID compared with the record immediately before its deletion; the deletion **observed complete** (a Pod still `Terminating` is a STOP, not "requested").
- [ ] **E3-2** detachment confirmed by **both** the VolumeAttachment list (well-formed, no attachment referencing the PV) and the Longhorn volume (`status.state` `detached`), within a 180 s budget. Expiry, a remaining attachment, `attaching`, `unknown`, `faulted`, a failed read or a malformed response: **INCONCLUSIVE for Experiment 3, saying nothing about RWOP either way**; keep the evidence, do not create writer B, do not launch any other contender, investigate the attachments and the Longhorn volume. Intermittent read failures are counted as failures, never as detached. Experiment 3 may be repeated from E3-0 only once the cause is understood; Experiments 1 and 2 stand on their own.
- [ ] **E3-3** writer B (default `home-apps` node selector, the read-only command) created only after E3-2; Ready within 180 s (if not: `Pending` with an RWOP message here means a stale attachment or an unexpected second holder, STOP and investigate; resource or affinity reasons are INCONCLUSIVE); its node and log saved.
- [ ] **Persistence verdict**, by comparing the saved log against the recorded checksum file by eye or with a checksum tool on the workstation: `VERIFY_FIRST`, then a `sha256sum` line whose hash equals the recorded checksum, then the marker text equal to the recorded marker: **PERSISTENCE OK**. `MARKER_MISSING` or a different hash: **PERSISTENCE FAIL**, recorded as such and a blocker for the design regardless of the RWOP result. No checksum line: **INCONCLUSIVE**; the Pod did not reach the verification and the cause must be found.

The replacement writer has issued **no write** at this point; only after PERSISTENCE OK may the operator, if wanted, have writer B append to a second file to show the volume is writable by the new holder (a separately approved exec).

### 1.8 Record and clean up the disposable resources (P10)

Record in `docs/testing/phase2-validation.md`: PV name, Longhorn access mode, both nodes, the classification of Experiments 1, 2 and 3 with the verbatim event lines, the checksum comparison, timings, the saved object files, and the decision. Cleanup starts only after the evidence directory is complete and the owner has seen the results and approved deleting the disposable Pod and claim. Each checkpoint is proven before the next operation; the first failed checkpoint stops the cleanup with nothing performed after it:

1. the claim's UID, `Bound` state, PV name and the PV's `claimRef` match the record;
2. writer B's UID compared with the record; its deletion issued by hand and observed complete;
3. no Pod remains in the namespace;
4. the VolumeAttachment list (well-formed) shows no attachment for the PV, and
5. the Longhorn volume reports `detached`, both within the budget;
6. the claim identity is unchanged;
7. the claim's deletion issued by hand **[approval]** and observed complete (a claim held by `kubernetes.io/pvc-protection` is still in use by something: STOP and find it);
8. the Longhorn volume disappears (reclaim `Delete`) within the budget.

The cleanup **never deletes the namespace, a PersistentVolume or a Longhorn volume directly**, never forces anything and never edits finalizers. An incomplete cleanup is reported with its checkpoint and observation; the remaining disposable objects are listed for a later, separately approved attempt, and leaving the disposable namespace in place is always preferred over deleting anything whose state is uncertain. An incomplete cleanup does not change the experiments' classification.

### 1.9 Decision

- **RWOP CONFIRMED** (Experiment 1 exclusion at scheduler or kubelet level, Experiment 3 persisted, no INCONCLUSIVE): keep `ReadWriteOncePod` in the frozen template; update the ADR-0007 status line; proceed.
- **RWOP NOT EFFECTIVE or UNSUPPORTED:** do **not** change the manifests as a reflex. The RWO fallback is a proposal that needs **explicit owner approval**, because RWO is per node: a second Pod on the same node could open the database. Before approval the fallback must add the safeguards ADR-0007 lists (StatefulSet one-pod-per-ordinal, `replicas: 1` validator-enforced, bbolt's exclusive lock, nothing else ever referencing the claim) and should wait for the advisory owner lease of Phase 3a if the owner wants detection. If approved, change `accessModes` in `deploy/kustomize/base/server/statefulset.yaml` **and** `deploy/kustomize/tests/statefulset-identity.golden.json` **before any `gb-dev` claim exists**, record the gap in ADR-0007 and the plan, re-run `deploy/kustomize/tests/run.sh`, commit, then sync.
- **INCONCLUSIVE:** fix the unrelated cause and repeat from the affected checkpoint; never treat a scheduling failure as proof of RWOP.

### 1.10 Namespace retirement (P11, separate manual operator action)

Deleting the namespace is **not** part of the cleanup and is never used as a shortcut to remove Pods or claims (a namespace deletion cascades into whatever is left, including a claim whose volume state is uncertain). It is a separate action after 1.8 completed all eight checkpoints, with its own approval and a final stop point:

1. fresh read-only inventory: no Pod, no claim, and no PersistentVolume with a `claimRef` into the namespace; anything remaining sends the operator back to 1.8 for that object;
2. identity: the namespace's name, UID and creation timestamp match the disposable namespace created at P1 (never `gb-dev`); the UID is recorded;
3. the owner's approval for this deletion, recorded;
4. **final stop point**: the inventory of step 1 is repeated; only if it is still empty is the deletion of the namespace object issued by hand, bounded, and its completion confirmed separately. A namespace that stays `Terminating` is inspected for finalizers and never forced.

If any step is uncertain, the empty namespace stays; it costs nothing.

## 2. NetworkPolicy enforcement and the admin port

The platform Application must be synced (and verified) before the server Application so the fence exists before the listener. Enforcement is Cilium's job; rendering and the offline validator prove nothing about it. These tests run in the real `gb-dev` namespace **after `gb-dev-platform` is synced and before `gb-dev-server` is synced**, with a throwaway target Pod that carries the engine labels and listens on both ports. Its manifest (data; the image digest is read at run time):

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: np-target
  namespace: gb-dev
  labels: {app.kubernetes.io/name: gb-engine, app.kubernetes.io/component: engine}
spec:
  restartPolicy: Never
  securityContext: {runAsNonRoot: true, runAsUser: 65532, runAsGroup: 65532, seccompProfile: {type: RuntimeDefault}}
  containers:
    - name: t
      image: busybox:1.37.0@sha256:<digest read at run time>
      command: ["sh", "-c", "(while true; do printf 'HTTP/1.1 200 OK\r\n\r\nplayer\n' | nc -l -p 8080; done) & while true; do printf 'HTTP/1.1 200 OK\r\n\r\nadmin\n' | nc -l -p 8081; done"]
      ports: [{name: player, containerPort: 8080}, {name: admin, containerPort: 8081}]
      securityContext: {allowPrivilegeEscalation: false, readOnlyRootFilesystem: true, capabilities: {drop: [ALL]}}
```

Record the target's Pod IP from the Pod object as `<target-ip>` in the evidence table. Each row is one approved, read-only probe from the named source (the throwaway source Pods are created for the test and removed afterwards):

| # | Source | Target | Expected | Why |
|---|---|---|---|---|
| N1 | a Pod in another namespace (`kubectl -n default run np-src --rm -it --image=busybox:1.37.0 -- wget -qO- -T 3 http://<target-ip>:8080`) | 8080 | **timeout** | default deny; only Traefik may reach the player port |
| N2 | same Pod | 8081 | **timeout** | only Prometheus may reach the admin port |
| N3 | a Pod in `gb-dev` without the engine labels | 8081 | **timeout** | unauthorised Pod in the same namespace |
| N4 | a **hostNetwork** Pod on the node running `np-target` | 8081 | record the result | Cilium treats the local host specially (`allow-localhost`); if this succeeds, the admin API is reachable from node processes and the result goes into the risk register |
| N5 | a hostNetwork Pod on a **different** node | 8081 | **timeout** | remote-node entity under default deny |
| N6 | the Traefik Pod on any node (`kubectl -n kube-system exec ds/rke2-traefik -- wget -qO- -T 3 http://<target-ip>:8080`; if Traefik's image lacks `wget`, an ephemeral debug container) | 8080 | **200 player** | the allow rule |
| N7 | same Traefik Pod | 8081 | **timeout** | the allow rule opens only `player` |
| N8 | the Prometheus Pod (`kubectl -n cattle-monitoring-system exec prometheus-rancher-monitoring-prometheus-0 -c prometheus -- wget -qO- -T 3 http://<target-ip>:8081/metrics`) | 8081 | **200 admin** | the allow rule |
| N9 | same Prometheus Pod | 8080 | **timeout** | the allow rule opens only `admin` |
| N10 | `np-target` itself, egress to `https://github.com` | any | **timeout** | default deny egress |
| N11 | `np-target`, `nslookup kubernetes.default` | 53 | **resolves** | DNS egress allow |
| N12 | kubelet probes: give `np-target` an httpGet readiness probe on 8080 and watch it become Ready | 8080 | **Ready** | probes come from the node; if this fails, Cilium's host policy blocks probes and the design needs a host-entity allow (record and stop) |
| N13 | `kubectl -n gb-dev port-forward pod/np-target 18081:8081`, then `curl http://127.0.0.1:18081/` from the workstation | 8081 via loopback | **200 admin** | port-forward dials localhost inside the Pod's network namespace and bypasses the CNI policy |

The target and the source Pods are removed afterwards (each an approved deletion of a named throwaway object). Any unexpected success in N1 to N3, N5, N7, N9 or N10 means the fence is not enforced as designed: **do not sync `gb-dev-server`** until it is understood. N4 is a documented limitation to record, not a pass/fail gate, because node-originated traffic is outside what a NetworkPolicy can promise.

## 3. Port-forward against the real server (after `gb-dev-server` is synced)

| Check | How (read-only) | Expected |
|---|---|---|
| admin listener reachable only through the API server | `kubectl -n gb-dev port-forward pod/gb-engine-0 18081:8081` in one terminal | port-forward established |
| `gbctl` through the forward | `GBCTL_SERVER=http://127.0.0.1:18081 GBCTL_TOKEN_FILE=~/.config/gb/gb-dev-admin-token gbctl status` | `mode: UNINITIALIZED` on a fresh volume, exit 0; a wrong token exits 3 |
| metrics on the same listener, unauthenticated | `curl -s http://127.0.0.1:18081/metrics` | Prometheus text |
| admin API refuses without a bearer token | `curl -s -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:18081/admin/v1/status` | `401` |
| no 8081 endpoint on the player Service | `kubectl -n gb-dev get endpointslices -l kubernetes.io/service-name=gb-player -o jsonpath='{.items[*].ports[*].port}'` | `8080` only |

## 4. What these tests do not cover

Crash consistency (CR-18), Longhorn replica loss, node-loss recovery timing, and certificate issuance; the last is covered by the acceptance list in `deploy.md`. Recovery of the real volume is a separate planning checklist (`dev-volume-recovery.md`), also not approved for execution.
