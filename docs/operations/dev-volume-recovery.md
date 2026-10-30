# Development-volume recovery: operator planning checklist

> **NOT APPROVED FOR EXECUTION. PLANNING CHECKLIST ONLY.** This document identifies the operations a recovery of the `gb-dev` universe volume would require, their order, the evidence each one needs and who approves it. It contains **no runnable sequence**: no shell block, no deletion command, no scale command, no forced termination, no helper script. Phase 2B (2026-10-30) retired the executable recovery automation that earlier revisions of this document carried, after four independent reviews found defects in it (`docs/testing/phase2b-handoff.md` section 12.9). Until (1) this checklist has passed an independent review, (2) a separately authorised operational-validation stage has rehearsed it against a throwaway StatefulSet in a disposable namespace, and (3) the owner has approved a written per-incident plan, **every recovery of `gb-dev` is BLOCKED**. The honest response to a `gb-dev` failure in the meantime: change nothing, delete nothing, scale nothing, record the evidence of section 4, and ask the owner.

Revision history: GB2B-03 (recovery must not quiesce a HALTED engine), GB2B-03R (an unavailable engine is never assumed stopped; no replacement before the desired replica count is zero and fencing is proven), GB2B-02R and GB2B-04 (no step continues past a failed check; API failures and malformed responses are never success), GB2B-05 and GB2B-06 (no shell evaluation of API-derived evidence; no automation shipped ahead of its dependencies). The last three were answered first with a shell library and then, on the fourth review, by removing executable procedures from Phase 2B altogether.

**Single rule behind every case: a replacement writer (a desired replica count above zero, or any Pod referencing the claim) is started only after the original writer is proven unable to run. Unknown, contradictory or missing evidence means STOP. Nothing is promoted from uncertain to GO by time passing, by retrying, or by a partial match.**

## 0. What recovery means here

Until Phase 3a `gb-dev` is disposable: the only recovery mechanism is a **fresh, empty volume initialised from the recorded seed** (`gbctl universe init`; seeded generation reproduces the identical universe for identical parameters and generator version). Player state created after initialisation is lost. There is no crash-consistent snapshot, no atomic turn recovery and no guaranteed rollback (CR-18, inventory entry `persistence/non-atomic-turn-flush`). Longhorn replication and the daily recurring backup copy whatever bytes exist; they do not make a mixed-turn store consistent. No overwrite, reset or in-place re-initialisation exists in `gbctl`, and none will be added (ADR-0008).

Not covered: an orderly restart (`deploy.md` section 5), a configuration change (ConfigMap edit and sync), a secret rotation (recreate the Secret, orderly restart). Node loss during normal operation is routed here (`deploy.md` section 6) and is Case C.

## 1. Outcome vocabulary

| Word | Meaning | Promotion |
|---|---|---|
| **GO** | every item of the gate is satisfied by recorded evidence | never automatic; a named person writes GO with the evidence reference |
| **STOP** | an item is unsatisfied, unknown, contradictory or its source failed (API error, malformed response, timeout) | the default; a STOP never becomes GO without new evidence |
| **INCONCLUSIVE** | the observation itself is unusable (timeout without a named observation, malformed response, failed read) | treated as STOP |
| **BLOCKED** | a prerequisite outside the operator's control is missing (no console access, no fencing possible, no approval, no reviewed checklist) | recovery waits; nothing is attempted "meanwhile" |

## 2. Safety requirements and where the checklist satisfies them

| # | Requirement | Satisfied by | Never satisfied by |
|---|---|---|---|
| 1 | identify the original StatefulSet, Pod, node, PVC and volume | section 4 evidence record (UIDs, not names) | names alone; memory; a previous incident's record |
| 2 | establish the identity of every potentially surviving writer | C0: controller ownerReference, ControllerRevision, every engine-labelled Pod, every Pod UID seen in Events **plus** the statement that Events are incomplete | the current Pod alone; Events alone |
| 3 | stop or control competing reconcilers | C1: StatefulSet controller held at zero, no autoscaler, Argo CD Application with no automated sync and no operation in flight, known field managers | an accepted API update |
| 4 | establish a verified maintenance window | C1: written window naming who holds the Argo CD sync, until when, re-verified before every later gate | an informal agreement |
| 5 | scale to zero before considering stale Pod cleanup | C1 precedes C3 in a fixed order | clearing the Pod object first "to unstick it" |
| 6 | prove the original writer cannot restart | C2: restart prevention verified by effective state | a successful service-manager return code; a cordon |
| 7 | require independently verified fencing | C2 (a), (b) or (c), written, by a named person | NotReady, missing Pod object, stopped services, lost SSH, a detached-looking volume, a silent endpoint, a timeout |
| 8 | verify attachment and storage state | C3 and section 6: VolumeAttachment list **and** Longhorn volume, each well-formed, each saying detached | either source alone; a malformed response; a missing Longhorn volume |
| 9 | protect the original PVC | R1 annotation and optional snapshot; UID re-checked at every gate; nothing in this checklist removes it automatically | retention policy alone (it does not survive a claim deletion) |
| 10 | separate approval for any destructive change | each operation marked **[approval]** or **[owner]** below is its own approval, naming the object UID | a blanket approval for "the recovery" |
| 11 | fresh empty storage for seeded universe recreation | R4: a **new** claim object (new UID, new PV) provisioned by the controller | reuse, reformatting or in-place deletion of files on the old volume |

If the original writer cannot be identified (C0) or cannot be fenced (C2), recovery stays **BLOCKED**. This cluster has no demonstrated remote power control (no IPMI, BMC, smart PDU or UPS-driven fencing has been shown); nothing here assumes one.

## 3. Operations this checklist would involve

Each operation is listed once, with its approval level. None is performed as part of reading this checklist; the live operational-validation stage will decide how each is issued and verified.

| Id | Operation | Approval | Reversible | Notes |
|---|---|---|---|---|
| O1 | read-only queries (Pods, StatefulSet, ControllerRevisions, Events, PVC, PV, VolumeAttachments, Longhorn volume, nodes, Argo CD Application, logs) | none | n/a | the only operations that need no approval |
| O2 | `gbctl quiesce` through a port-forward | admin op | yes (`gbctl resume`) | Case A only; refused in HALTED and UNINITIALIZED |
| O3 | set the StatefulSet's desired replica count to zero | **[approval]** | yes | the first mutating step of every case; kubelet-driven termination on a Ready node |
| O4 | physical power-off of a node, held | **[approval]**, physical presence | yes | fencing method (a) |
| O5 | on-node termination of a surviving container shim and process, and restart prevention of the node agent for the window | **[approval]**, console or SSH on the node | yes (unmask, restart) | fencing method (b); affects every workload on that node; **not performed in Phase 2B** |
| O6 | forced deletion of a stale Pod **object** whose UID matches the record | **[approval]** | n/a | bookkeeping only: stops no container, detaches no volume; only after C1 GO and C2 GO |
| O7 | retirement annotation on the original claim | **[approval]** | yes | R1 |
| O8 | Longhorn snapshot or backup of the original volume | **[approval]** | yes | R1, optional |
| O9 | deletion of the original claim (`world-gb-engine-0`, recorded UID) | **[owner]**, every time, for that UID | **no** (reclaim policy `Delete` destroys the data) | R3 |
| O10 | set the desired replica count back to one | **[approval]** | yes | R4; creates the fresh claim |
| O11 | `gbctl universe init` with the recorded parameters | admin op | no (refused on an initialised store) | R5 |

## 4. Evidence record (data, never shell)

Collect into a dated incident directory on the workstation (never on the PVC) as files and a filled-in table. Values are copied from read-only output into the record by hand or saved as the raw output file. **No value is assigned to a shell variable, exported, or written to a file that a shell will later source or evaluate**; the record is data that a person reads and compares. A query that fails, times out or returns something other than a well-formed object of the expected kind is recorded as `INVALID` with the error text, not as empty, absent or zero.

| Field | Source (read-only) | Record |
|---|---|---|
| incident time (UTC), operator | clock | |
| StatefulSet `gb-engine`: UID, `spec.replicas`, `status.replicas`, `currentRevision`, `updateRevision` | the StatefulSet object (`-o yaml`) | |
| Pod `gb-engine-0`: UID, node, phase, `deletionTimestamp`, container state and last exit code, `ownerReferences` (kind, name, UID, `controller`), label `controller-revision-hash` | the Pod object (`-o yaml`), `describe`, current and previous logs | |
| ControllerRevision named by the hash: owner UID | the ControllerRevision object | |
| every Pod carrying the engine labels: name, UID, node, phase, controller UID | Pods by label selector | |
| Pod UIDs recorded in Events under the name `gb-engine-0` (`involvedObject.uid`) | Events in `gb-dev` | **incomplete by design**: Events expire (one hour by default on this cluster's API server) and are not a history of every writer; an absent Event proves nothing |
| PVC `world-gb-engine-0`: UID, phase, `volumeName`, finalizers, annotations | the claim object | |
| PV: name, `claimRef` (namespace, name, UID), reclaim policy, access modes | the PV object | |
| Longhorn volume (same name as the PV): `status.state`, `status.currentNodeID`, `status.robustness`, attachment tickets | the `volumes.longhorn.io` object in `longhorn-system` | a missing volume is a contradiction with a Bound claim: STOP |
| VolumeAttachments whose `spec.source.persistentVolumeName` is the PV: name, node, `status.attached` | the VolumeAttachment list (`-o json`, saved whole) | a response that is not a `List` of `VolumeAttachment` objects with a boolean `status.attached` is `INVALID` |
| node of the Pod: Ready condition, its transition time, taints, `unschedulable` | the Node object | |
| Argo CD Application `gb-dev-server`: `syncPolicy.automated` present or absent, `operation`/`status.operationState` in flight or not, sync status | the Application object in `argocd` | a failed lookup is STOP for C1, not "absent" |
| HorizontalPodAutoscalers in `gb-dev` targeting the StatefulSet | HPA list | |
| field managers of `spec.replicas` | the StatefulSet with managed fields shown | |
| engine state | `gbctl -json status` through a port-forward, if it answers (mode, `halt_reason`, universe record, schedule) | |
| universe record | the operations log written at initialisation (`deploy.md` section 4): seed, stars, planet bounds, generator version, `initialized_at`, image digest | if missing, the universe cannot be reproduced identically; the owner decides on a new seed |

Identify the condition from the record:

| Observation | Case |
|---|---|
| Pod Running and Ready; `gbctl status` answers `SERVING`, `TURN_IN_PROGRESS`, `QUIESCED` or `UNINITIALIZED` | **A** healthy and responsive |
| `gbctl status` answers `HALTED` (`halt_reason` set); `/readyz` 503; `/livez` 200 | **B** halted |
| CrashLoopBackOff, `Terminating` for a long time, `Unknown`; node `NotReady`; port-forward or `gbctl status` cannot connect (exit 2) or times out (exit 5) | **C** unavailable or unresponsive |
| engine was HALTED (B) and its node then became unreachable before it was stopped | **C**, with the Case B store caveat |

## 5. Cases

### Case A: healthy and responsive engine

1. **Quiesce** (O2). The request is applied by the actor at an operation boundary: if a turn is running it waits for the turn to finish, and `gbctl` may time out first (exit 5) while the request stays queued or is cancelled; a quiesce never interrupts a running turn. Repeat until `gbctl status` reports `QUIESCED`. In `UNINITIALIZED` quiesce is refused (exit 4) and is not needed. A reply of exit 4 with `engine halted` means Case B.
2. **Verify** `QUIESCED` in `gbctl status`; `/readyz` through the Ingress is 503 (expected while quiesced); no turn activity in the logs.
3. **Orderly stop** (O3) **[approval]**. SIGTERM takes the orderly path (sessions closed, schedule persisted, store closed). The grace period is 300 s and the server's budget 240 s. Wait, bounded, for the Pod object to disappear through the kubelet's graceful deletion on a `Ready` node; if the node stops being `Ready` during this step, switch to Case C.
4. **Check the exit**: the last log lines show the shutdown sequence and exit 0. Any other exit code (2, 3, 4, 6) means the store may hold a partial turn; record it; the replacement checklist is the same.
5. Continue with section 6 (fence check) and section 7 (replacement).

### Case B: HALTED engine

HALTED means a panic or a failed store write left in-memory state untrusted (`local-server.md` section 9). The actor still runs, holds the database open, refuses every mutating operation and scheduled turn, and refuses `Resume`. Shutdown from HALTED exits 2 **without flushing memory**, so the database holds whatever the last transaction wrote and nothing else: **potentially partially updated** (CR-18).

1. **Do not quiesce, do not resume** (both refused by design, exit 4). Do not restart the Pod "to see whether it clears": a restart reloads the store and may serve a mixed-turn world as if it were consistent.
2. **Preserve evidence**: section 4 plus `gbctl -json status` (contains `halt_reason`) and the Loki query for the Pod around the halt time.
3. **Establish that the engine is no longer writing**: in HALTED the actor performs no writes after the halt; confirm from the logs that no `turn`, `flush` or `universe.init` entry follows the `engine halted` line.
4. **Stop the process** (O3) **[approval]**; wait as in Case A step 3. Expect exit code 2 (`ErrHaltedNotFlushed`) in the final log line: the documented, honest outcome. On a `Ready` node this deletion is trustworthy: the Pod object disappears only after the kubelet has reported the container stopped (a graceful deletion, not a forced one). If the node stops being `Ready` during this step, switch to Case C.
5. Continue with sections 6 and 7. If the owner wants the halted store examined later, say so at R1 (snapshot before retirement).

### Case C: unavailable or unresponsive engine

Loss of connectivity to the Pod is **not** evidence that the process has stopped. A Pod on a `NotReady` or partitioned node may still be running and writing to the attached volume; the API simply cannot see it. On RKE2 the container is supervised by a `containerd-shim` process that **survives the termination of `containerd`, the kubelet and the `rke2-agent` service**; stopping those services, or losing SSH to the node, says nothing about the game server. **The disappearance of an API object never proves that a process has terminated.** Starting a replacement writer while the old one may still run is the one failure the single-writer design exists to prevent (plan 11.3), and the StatefulSet controller will create exactly such a replacement, with the same name and the same claim, the moment the old Pod object disappears while the desired replica count is still one.

Case C has four gates in a fixed order, each ending in GO, STOP or BLOCKED, none passed by a timeout: **C0 identify**, **C1 prevent any replacement and control reconcilers**, **C2 prove the original writer is fenced**, **C3 clear bookkeeping and verify storage state**.

#### C0. Identify and record (read-only)

Fill in section 4 completely, then check:

- [ ] the Pod's `ownerReferences` contain **exactly one** controller reference, kind `StatefulSet`, name `gb-engine`, UID equal to the StatefulSet's UID (names alone are not trusted);
- [ ] the Pod's `controller-revision-hash` names a ControllerRevision owned by that same UID;
- [ ] every Pod carrying the engine labels is controlled by that UID, and its UID is in the record;
- [ ] every Pod UID that Events still show under the name `gb-engine-0` is in the record. A UID other than the current Pod's is a predecessor or a replacement writer and needs its own fencing evidence (C2) before recovery continues. Events are **not** a complete history: a writer that left no Event, or whose Events expired, is not excluded by their absence. If the Pod object is already gone, the writer under recovery is the UID the record attributes to it from logs, `describe` output, Events and the previous incident log together; a record that cannot name it is a STOP;
- [ ] the claim is `Bound`, its UID recorded, its `volumeName` recorded; the PV's `claimRef` names that claim UID; the Longhorn volume exists;
- [ ] the VolumeAttachment list is well-formed and recorded (its content is not a gate here).

Unexpected ownership (`OWNERSHIP MISMATCH`), missing metadata, a claim that is not Bound, a PV bound to another claim UID, a missing Longhorn volume, a failed or malformed read: **STOP**. Identities are never worked around, and the rest of the case refers only to the recorded UIDs.

#### C1. Prevent any replacement and establish reconciliation control

- [ ] **Maintenance window** written in the record: who holds the `gb-dev-server` Argo CD sync (manual-sync Application, no self-heal; nobody syncs it until recovery is finished), that no autoscaler targets the StatefulSet, who may change `spec.replicas`, until when. Without it: BLOCKED.
- [ ] **Desired replica count set to zero** (O3) **[approval]**, before any Pod object is touched.
- [ ] `spec.replicas` reads **0 on two reads a minute apart** (an accepted update is not a persisted desired state under a competing writer).
- [ ] no HorizontalPodAutoscaler targets the StatefulSet.
- [ ] the Argo CD Application `gb-dev-server` exists, has no `automated` sync policy and no operation pending or running. A failed lookup is a STOP: reconciliation control cannot be established on a guess.
- [ ] every field manager of `spec.replicas` is known (`kubectl-*` operators, `argocd-controller`); anything else is identified before continuing.
- [ ] no Pod with the engine labels exists other than the recorded writer UID. A `gb-engine-0` with a **different UID** is a replacement the controller created earlier (for example after a forced deletion): a potential second writer whose later disappearance from the API would prove nothing about its process; recovery does not continue until that UID has its own fencing evidence in the record.

These observations describe the present; the window keeps them true; the checklist re-runs them before C2, C3, section 6 and section 7. If `spec.replicas` ever reads 1 again: STOP, find out what restored it, and restart C1 only after that actor is controlled. Changing the desired count proves nothing about the old process; it only guarantees that Kubernetes will add no second one.

#### C2. Prove the original writer is fenced

One of the following, written in the record with who, when and how, by a named person, for **every** writer UID in the record:

- **(a) Power fencing, verified and held** (O4). The node is powered off (physical switch, or power lead removed), confirmed off by someone looking at it or at its console, and kept off until the volume is detached and the replacement is serving. There is no remote power control in this lab; this is a manual, physical action. A machine set to power on after AC loss, or reachable by Wake-on-LAN, stays unplugged, not merely switched off.
- **(b) Direct verification of termination plus restart prevention** (O5). On the node's console or over SSH: the process table shows no `containerd-shim` for the engine container and no `gb-server` process; the listening sockets show nothing on 8080 or 8081; the mount table shows the PV is not mounted. If a shim or the process is present, terminating it is a forced stop of the engine (partially applied changes possible, CR-18) and is its own **[approval]**; the live validation stage decides the tool (the RKE2-provided kill-all script affects every workload on the node). Then the node agent is prevented from restarting for the window and the **effective** state is verified (the service reports inactive and masked); a successful command return proves nothing by itself. After that the process, socket and mount checks are repeated and all three must be empty. Any check that fails is a STOP, not a retry loop. Restoration after recovery is verified the same way. **Not performed in Phase 2B.**
- **(c) Kubelet-confirmed termination.** If the node returns `Ready` by itself while the desired count is zero, the kubelet terminates the Pod gracefully and the Pod object disappears **only after the container has stopped**; that deletion, forced by nobody, is trustworthy. Preferred when the node is expected back and there is no urgency.

What never counts, alone or together: node `NotReady`; the Pod missing from the API; a successful forced-deletion response; stopped kubelet; stopped containerd; stopped `rke2-server` or `rke2-agent`; lost SSH; a cordoned node; a volume that looks detached; an admin endpoint that does not answer; a timeout of any length; the absence of Events.

If none of (a), (b), (c) can be established: **BLOCKED.** The prerequisite is physical: power the machine off and keep it off (a), get a console on it (b), or wait for it to return (c). Nothing below is attempted on a guess, and no forced deletion is issued "to see what happens".

#### C3. Clear bookkeeping and verify storage state

Only with C1 GO (re-checked now) and C2 GO for every writer UID:

- [ ] **Stale Pod object** (O6) **[approval]**: if the Pod object still exists (typically `Terminating` with a `deletionTimestamp` set by the node-lifecycle controller), its UID is compared with the record immediately before the deletion; a different UID under the same name is an identity mismatch and a STOP. The deletion removes an API object. **It stops no container** (C2 established that), **it detaches no volume**, and with the desired count at zero it creates no replacement. The absence of the object afterwards is bookkeeping, not evidence.
- [ ] **Detachment** is a separate fact, proven only when **both** the VolumeAttachment list (well-formed, no attachment referencing the PV) and the Longhorn volume (`status.state` `detached`) say so, within a bounded budget (about ten minutes: the attach/detach controller waits about six minutes after the Pod object is gone before it force-detaches from an unresponsive node, and Longhorn then processes it). Still attached, `attaching`, `unknown`, `faulted`, a failed read or a malformed response at the end of the budget: **INCONCLUSIVE, STOP**; investigate the Longhorn volume and the Longhorn UI. A manual detach from the Longhorn UI is never used unless C2 (a) holds, because detaching from a node whose shim is alive does not stop that process writing to its local replica.
- [ ] **Retention held**: the claim is the same object (recorded UID), still `Bound` to the same PV. Nothing in this case removes it. The store on it may contain partially applied turn changes (CR-18); its disposal is the owner's decision (R2, R3).

Continue with section 6 (which re-checks everything above) and section 7.

## 6. Fence check before any replacement storage or writer

Read fresh (never reusing earlier output), whichever case led here:

- [ ] `spec.replicas` is 0;
- [ ] no Pod with the engine labels exists (original or replacement);
- [ ] the VolumeAttachment list is well-formed and shows no attachment for the PV, **and** the Longhorn volume reports `detached`;
- [ ] the claim is unchanged (recorded UID, `Bound`, same PV);
- [ ] for Case C: written fencing evidence (a), (b) or (c) for every writer UID, and C1 re-checked immediately before section 7.

For Cases A and B the Pod deletion was graceful on a `Ready` node, which is the kubelet's confirmation of termination; if that node became `NotReady` during the stop, treat it as Case C. Any item not satisfied: **STOP**; no replacement storage or writer, however long it has been.

## 7. Replacement (planned operations)

- **R1 Protect** (O7, optionally O8) **[approval]**: annotate the original claim with a retirement marker (key `gb.phreddrick.github.io/retired`: time, case, reason). If the owner wants the old store kept for analysis, take a Longhorn snapshot or backup of the PV first and record its name; it copies the bytes as they are, not a consistent turn.
- **R2 Decide**: present the owner with the record, the claim and PV names and UIDs, the annotation, the snapshot status and the seed record. Kubernetes cannot rename a claim and the template name `world` is frozen, so the options are (a) delete the claim and regenerate, or (b) keep the claim under the same name and abandon recovery. Nothing deletes automatically.
- **R3 Retire** (O9) **[owner, every time, for the recorded UID]**: the deletion of the original claim is issued by hand, once, after the owner's written authorisation naming that UID. The `longhorn` reclaim policy is `Delete`: the volume and its replicas are destroyed. Afterwards the Longhorn volume must be `NotFound`. A snapshot from R1 survives only if it was converted to a backup; a Longhorn snapshot lives with the volume.
- **R4 Recreate** (O10) **[approval]**: desired count back to one. The controller provisions a new `world-gb-engine-0` (same frozen template: RWOP, `longhorn`, 5Gi); `fsGroup` makes the mount writable; the engine opens an empty store and reports `UNINITIALIZED` (`/readyz` 200, `game_ready:false`). Verify the claim is a **new object** (UID and PV name differ from the record). The original volume is never reused, reformatted or cleaned in place.
- **R5 Initialise** (O11): `gbctl universe init` with the recorded seed, star count and planet bounds. Compare the printed record with the log; record the new `initialized_at`. A second `gbctl universe init` must exit 4. If the recorded generator version differs from the running image's, the universe is not guaranteed identical: record that.
- **R6 Verify**: `gbctl status` shows `SERVING` with the schedule; a login through `https://gb-dev.home.spukhaus.com` works; Prometheus shows `gb_engine_mode`.

## 8. Rollback of the procedure

- Before R3: desired count back to one **[approval]**; the old volume is intact and the engine resumes on it (in Case B it reloads the possibly mixed-turn store and starts SERVING; the owner accepts that explicitly).
- After R3: no rollback. The universe is regenerated from the seed.

## 9. Never

- Never run `makeuniv` against the mounted volume, from an init container, a Job or a debug container.
- Never mount `world-gb-engine-0` from a second Pod: with RWOP it is refused; with the RWO fallback it would be a second bbolt opener.
- Never start a replacement writer (desired count above zero, or any Pod referencing the claim) before section 6 holds.
- Never issue a forced Pod deletion while the desired replica count is above zero, or before C2 holds.
- Never treat a missing API object, a stopped service, a cordon, a lost connection or a timeout as proof that a process has stopped.
- Never delete the original claim while `gb-engine-0` exists or without the owner's written authorisation naming its UID.
- Never resume a HALTED engine to "finish" recovery; Phase 1 refuses it, and a restart is the only path back to serving, which is a decision, not a step.
- Never automate this checklist, in the repository or on a workstation, without the owner's separate authorisation, an independent review of the automation, and the live validation stage's evidence.

## 10. Before this checklist may be executed

1. Independent review of this document against the safety requirements of section 2.
2. A separately authorised **operational-validation stage**: rehearsal against a throwaway StatefulSet with a disposable RWOP claim in a disposable namespace, exercising Cases A, B and C (including a simulated unavailable node with a surviving container, which is where the earlier automation was wrong), with every observation recorded and the outcome vocabulary of section 1 applied; its results in `docs/testing/phase2-validation.md`.
3. Owner approval of the resulting procedure, recorded in ADR-0008's status line.
4. For each real incident: a written per-incident plan naming the recorded UIDs, the approvals and the window, approved before O3.
