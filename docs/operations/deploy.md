# Deploying `gb-dev`: synchronisation order, universe initialisation, restarts, rollback, acceptance

> **DRAFT. NOT YET APPROVED FOR DEPLOYMENT.** Written in Phase 2B (2026-10-30). Every `argocd app sync`, `kubectl` mutation and `helm` operation below is a separately approved operator action under the `k8s-helpers` rules. Nothing has been executed. Prerequisites are in `bootstrap.md`; the storage and network tests in `storage-and-network-validation.md` must have passed first (that document is an operator planning checklist whose live execution belongs to a separately authorised operational-validation stage); recovery is the planning checklist `dev-volume-recovery.md`, which is **not approved for execution**.

## 1. Preconditions (all verifiable read-only)

- `bootstrap.md` sections 2 to 7 complete: image published by the manual `publish-images.yml` run and its digest merged into `deploy/kustomize/apps/dev/server/kustomization.yaml` through the digest-update pull request (no sentinel: `deploy/kustomize/tests/run.sh --require-published-image` passes; the publish record artifact of that run names the digest and the source commit), AppProject `gb` and both Applications exist, controller RBAC in `gb-dev` verified with `kubectl auth can-i`, namespace `gb-dev` labelled, Secret `gb-server-secrets` present with both keys, DNS record resolving.
- `storage-and-network-validation.md` section 1 (RWOP) recorded as supported or the RWO fallback committed.
- `argocd app diff gb-dev-platform` and `argocd app diff gb-dev-server` show only additions (nothing exists yet).

## 2. Synchronisation order (manual, one Application per approval)

1. **`gb-dev-platform` first.** `argocd app sync gb-dev-platform` (or the UI). Expect Synced/Healthy with exactly: ServiceAccount `gb-engine`, NetworkPolicies `default-deny-all`, `allow-dns-egress`, `gb-engine-allow-player-from-traefik`, `gb-engine-allow-metrics-from-monitoring`, PodMonitor `gb-engine`. Verify `kubectl -n gb-dev get networkpolicy,podmonitor,sa`.
2. **Network fence verified** (`storage-and-network-validation.md` section 2) with the throwaway target pod. Stop here on any unexpected reachability.
3. **`gb-dev-server` second.** `argocd app sync gb-dev-server`. Expect: StatefulSet `gb-engine` 1/1 on a `workload.home-apps` node (`kubectl -n gb-dev get pod -o wide`), PVC `world-gb-engine-0` Bound (`RWOP`, `longhorn`, 5Gi), Services `gb-engine` (headless) and `gb-player` with a single 8080 endpoint, Ingress `gb-player` with address 10.0.0.20, Middleware `https-redirect`, Certificate `gb-dev-tls` Ready within a few minutes (DNS-01), the pod Ready with `/readyz` returning `game_ready:false`.
4. `argocd app diff` empty for both; `argocd app get` shows no orphaned-resource warnings.

Argo CD provides no cross-Application ordering (plan 12.5); this runbook is the ordering.

## 3. Operator access to the admin listener

The admin API (port 8081) is on no Service and no Ingress; the NetworkPolicy admits only Prometheus. Operators use port-forward, which dials localhost inside the pod's network namespace:

```sh
kubectl -n gb-dev port-forward pod/gb-engine-0 18081:8081 &
export GBCTL_SERVER=http://127.0.0.1:18081 GBCTL_TOKEN_FILE=~/.config/gb/gb-dev-admin-token
gbctl status
```

`gbctl` exit codes: 0 ok, 1 usage or missing token, 2 cannot reach the server (port-forward down), 3 token rejected, 4 refused by engine state, 5 server error or invalid reply. The token is never passed as an argument and never printed.

## 4. Initial universe creation

```sh
gbctl universe init -seed 4242 -stars 100 -min-planets 2 -max-planets 10
```

Record in the operations log immediately: seed, stars, planets, min/max planets, generator version, `initialized_at`, the initialising tool, the image digest. This record is the only recovery mechanism until Phase 3a. Then: `gbctl status` shows `SERVING` and the next update time; `gbctl universe init -seed 4242` again must exit 4 with `already initialised`; `/readyz` through the Ingress now reports `game_ready:true`.

## 5. Orderly restarts (data preserved)

Precondition: no turn in progress and none due within the next few minutes, or `gbctl quiesce` first (the schedule is persisted and resumes after restart; `gbctl resume` if it was quiesced).

- Image or config change: merge the digest or ConfigMap change (an image digest arrives as the `bot/gb-server-digest-*` pull request opened by `digest-update-pr.yml` after a publish run whose record says `published`; because the token opened it, a maintainer first selects **Approve workflows to run** on it, then its required checks must pass (`bootstrap.md` section 2); merging deploys nothing), then `argocd app sync gb-dev-server`; the StatefulSet replaces `gb-engine-0` (RollingUpdate with one replica is a stop-then-start).
- Plain restart: `kubectl -n gb-dev rollout restart statefulset gb-engine` (approved).
- Secret rotation: recreate `gb-server-secrets`, update the workstation copy, then a plain restart.

Verify after each: the previous container's exit code was 0 (`kubectl -n gb-dev get pod gb-engine-0 -o jsonpath='{.status.containerStatuses[0].lastState.terminated.exitCode}'` shows 0 while the new container runs, or the logs of the previous pod show the orderly sequence), `gbctl status` shows the same universe record and schedule, a login works. Exit codes 2, 3, 4 or 6 mean the store may hold a partial turn (CR-18): treat as a recovery case.

`terminationGracePeriodSeconds` is 300 and `GB_SHUTDOWN_TIMEOUT` 240, so the server, not the kubelet, ends the shutdown.

## 6. Node drain and node loss

- **Drain (planned):** quiesce, then `kubectl drain <node> --ignore-daemonsets --delete-emptydir-data` (approved; it moves every other pod on that node too, so prefer the node that runs only `gb-engine-0`). The StatefulSet recreates the pod on the other `home-apps` node; Longhorn re-attaches the volume; verify as in section 5; `kubectl uncordon` afterwards.
- **Node loss (unplanned):** with `node-down-pod-deletion-policy: do-nothing` the pod stays `Terminating` and the volume stays attached; Kubernetes will not reschedule it, and the operator must not force anything. Loss of contact with the node is **not** evidence that the engine process has stopped (on RKE2 a `containerd-shim` and its container survive the loss of the kubelet and of `containerd`), and a forced deletion of the Pod object while the StatefulSet still asks for one replica would create a second writer on the same claim. This is Case C of `dev-volume-recovery.md`, a planning checklist that is **not approved for execution**: record the evidence it lists, change nothing, and escalate to the owner. A turn may have been interrupted (CR-18).

## 7. Rollback and deletion safeguards

- Bad image or config: `git revert` the digest or ConfigMap commit, sync. The PVC is untouched.
- Bad platform object: revert and sync `gb-dev-platform`; if the NetworkPolicy fence is in doubt, scale the server to 0 first.
- Removing the deployment: **non-cascading** Application delete per `k8s-helpers` §6.2 (`argocd app delete <app> --cascade=false`; the Applications have no finalizer, so a plain delete also leaves the objects), then explicit `kubectl delete` of the workload objects if wanted. `persistentVolumeClaimRetentionPolicy: Retain` keeps `world-gb-engine-0` through StatefulSet deletion; only `kubectl delete pvc` destroys it, and only the owner authorises that (`dev-volume-recovery.md`).
- Never `kubectl delete namespace gb-dev` while the PVC exists.

## 8. Acceptance tests (Phase 2 exit, plan 16.4)

| # | Test | Pass criterion |
|---|---|---|
| A1 | both Applications Synced and Healthy, `argocd app diff` empty | yes |
| A2 | `https://gb-dev.home.spukhaus.com/status` over TLS with a valid production certificate; HTTP redirects to HTTPS | 200, redirect 301/308 |
| A3 | WebSocket login through Traefik with the legacy client (`gb-client`) using the public origin | session established |
| A4 | three orderly restarts (section 5) | universe record and schedule unchanged after each, exit code 0 each time |
| A5 | one node drain (section 6) | pod rescheduled on the other `home-apps` node, data intact |
| A6 | `gbctl universe init` on the initialised store | exit 4 |
| A7 | second pod refused the RWOP volume (already shown in the throwaway test; repeat with `kubectl -n gb-dev run` of a pod referencing `world-gb-engine-0` if the owner wants it on the real volume: it must stay Pending, then delete it) | Pending with RWOP conflict |
| A8 | Prometheus: `gb_engine_mode` present in the Rancher Prometheus UI, target `gb-dev/gb-engine` up | yes |
| A9 | Loki: JSON log lines from `gb-dev/gb-engine-0` | yes |
| A10 | `kubectl -n gb-dev get endpointslices` shows no 8081 anywhere; N-tests from `storage-and-network-validation.md` still hold against the real pod (N1, N2, N7, N9) | yes |
| A11 | no resource rendered twice; `deploy/kustomize/tests/run.sh --require-published-image` passes on the deployed commit | yes |
| A12 | recovery rehearsal: **deferred** to the separately authorised operational-validation stage (`dev-volume-recovery.md` section 10), against a throwaway StatefulSet, never first on `gb-dev`; not an acceptance item of the first synchronisation | n/a (deferred) |

Not claimed: survival of a crash or kill mid-turn (CR-18, Phase 3a).
