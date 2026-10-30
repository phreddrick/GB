# Offline validation of the deployment artifacts

```sh
deploy/kustomize/tests/run.sh                          # render + structure checks + mutations + safety check + kubeconform
deploy/kustomize/tests/run.sh --require-published-image # fails while the dev image is the UNPUBLISHED sentinel
python3 deploy/kustomize/tests/validate.py             # structure checks only, no network
python3 deploy/kustomize/tests/safety-check.py         # no recovery/deletion automation in the artifacts; --selftest plants violations
```

Tooling (nothing is installed on the host): `kubectl kustomize` (kustomize v5.8.1 embedded in kubectl 1.36.4) or the `kustomize` CLI; Python 3 with PyYAML; `go run github.com/yannh/kubeconform/cmd/kubeconform@v0.8.0` (module cache only; run outside the module so `go/go.sum` is untouched). kubeconform fetches the Kubernetes 1.36.4 strict schemas from `yannh/kubernetes-json-schema` over the network and reads the CRD schemas from `../schemas` (provenance in `../schemas/README.md`).

What `validate.py` checks, per environment (`dev`, `ci`) across its two entry points:

| # | Check |
|---|---|
| 1 | every `apps/<env>/<app>` renders |
| 2 | no `(apiVersion, kind, namespace, name)` rendered by two entry points, none duplicated within one |
| 3 | the `gb-engine` StatefulSet identity (name, `serviceName`, selector, `podManagementPolicy`, replicas, retention policy, `volumeClaimTemplates[world]` with access mode, class and size) equals `statefulset-identity.golden.json`; `ci` may differ only in access mode and storage class |
| 4 | `replicas: 1` |
| 5 | `nodeSelector` is exactly `workload.home-apps: "true"` (absent in `ci`); no affinity or tolerations |
| 6 | no `Secret` object; no secret values, private keys, tokens or the plain `GB_JWT_SECRET`/`GB_ADMIN_TOKEN` variables anywhere in the render; secrets consumed only through `GB_*_FILE` from the `gb-server-secrets` mount (read-only, mode 0440) |
| 7 | pod `runAsNonRoot`, UID/GID/fsGroup 65532, `fsGroupChangePolicy: OnRootMismatch`, seccomp `RuntimeDefault`; container no privilege escalation, read-only root, `drop: [ALL]`; no token automount |
| 8 | no privileged container, hostNetwork, hostPID, hostIPC, hostPath or init container |
| 9 | no Service lists port 8081 or `admin`; the Ingress routes only to `gb-player:player` |
| 10 | **NetworkPolicies, evaluated as the complete rendered set of the environment across both entry points (GB2B-01):** (a) explicit semantic checks that name the reason: the default deny selects every pod, lists both policyTypes and carries no allow rule; every allow rule has `from`/`to` and `ports`; no empty rule `{}`; exactly one peer per rule (peers are OR alternatives) that combines namespaceSelector and podSelector with exact matchLabels and equals an approved peer (Traefik, Prometheus, kube-dns); no ipBlock, no port ranges, no port entry without a port; the two engine allow policies select exactly the engine labels and enforce Ingress only; the DNS policy applies to every pod and enforces Egress only; no NetworkPolicy outside the approved set of four, none missing, none rendered twice, none owned by the server entry point; (b) a strict allowlist: each rendered policy's normalised spec (sorted peers, ports and policyTypes, explicit TCP default, empty rule lists dropped) must equal the approved specification; (c) a static reachability model built from every rendered policy: engine ingress is exactly player←Traefik and admin←Prometheus, port 8081 has no other source, Traefik→8080 remains permitted, egress is DNS only. The model is static: it proves nothing about Cilium enforcement or node-originated traffic. The PodMonitor scrapes `admin` at `/metrics` with a selector matching the pod labels (dropped in `ci`) |
| 11 | `ingressClassName: traefik`, issuer `letsencrypt-production-cloudflare`, host `gb-dev.home.spukhaus.com`, TLS secret `gb-dev-tls`, middleware `gb-dev-https-redirect@kubernetescrd`, no placeholder host left |
| 12 | container ports named `player` (8080) and `admin` (8081); probes on `player` (`/readyz`, `/livez`, `/readyz`) |
| 13 | one `volumeClaimTemplate` `world` mounted at `/data/world`; `GB_DB_PATH` is a file directly inside it; `GB_DATA_PATH` unused; `GB_SHUTDOWN_TIMEOUT` below `terminationGracePeriodSeconds` (≥ 300) |
| 14 | no `Namespace` object; every object targets `gb-dev` |
| 15 | PodMonitor selector, headless Service selector and pod template labels all agree with the StatefulSet selector |
| image | the dev image is `ghcr.io/phreddrick/gb-server@sha256:<64 hex>`; the all-zero digest is reported as a warning (UNPUBLISHED) or, with `--require-published-image`, a failure |

What it does **not** prove: RWOP support, NetworkPolicy enforcement, `kubectl port-forward` reachability, fsGroup write access on Longhorn, certificate issuance, WebSocket upgrades through Traefik, or that the image builds. Those are the live runbooks under `docs/operations/` and the later CI stages.

Error handling: a missing or renamed required resource (for example the StatefulSet) produces `FAIL` lines naming the resource and skips the dependent checks; an unexpected exception inside the validator is reported as a single `FAIL validate.py internal error` line with exit 1, never a traceback.

## Mutation suite

`python3 deploy/kustomize/tests/mutations.py` (also run by `run.sh`) copies the tree, applies one mutation per case, runs the copied validator and requires exit 1, no traceback and the intended reason in the output. 33 cases: the 13 structural ones (replicas, admin port on the Service, root user, RWO in dev, renamed StatefulSet, plain token in the ConfigMap, staging issuer, GPU node selector, default deny removed, Namespace object, writable root, short grace period, sync hook) and 20 NetworkPolicy bypasses (empty ingress/egress rule in the default deny, extra allow-all policy, extra all-TCP policy, extra unrestricted 8081 policy, admin rule without `from`, unrestricted source selector, wrong Prometheus pod selector, wrong monitoring namespace, split peers, admin policy selecting every pod, default deny ingress-only, Traefik policy with the wrong direction, each required allow removed, admin rule without `ports`, Traefik rule opening admin too, DNS peer without a namespace selector, a policy rendered by the server entry point). Two baseline checks: the unmodified tree passes with the UNPUBLISHED warning only, and `--require-published-image` rejects the sentinel. `-k <substring>` runs a subset.

## Regenerating the vendored CRD schemas

Read-only against the cluster whose CRDs the manifests target:

```sh
kubectl get crd podmonitors.monitoring.coreos.com -o json > podmonitor.crd.json
kubectl get crd middlewares.traefik.io -o json > middleware.crd.json
python3 - <<'EOF'
import json
def fix(n):
    if isinstance(n, dict):
        if n.get("x-kubernetes-int-or-string"):
            n.pop("type", None); n["oneOf"] = [{"type": "string"}, {"type": "integer"}]
        for k in [k for k in n if k.startswith("x-kubernetes-")]:
            n.pop(k)
        for v in list(n.values()): fix(v)
    elif isinstance(n, list):
        for v in n: fix(v)
    return n
for name in ("podmonitor", "middleware"):
    crd = json.load(open(f"{name}.crd.json"))
    group, kind = crd["spec"]["group"], crd["spec"]["names"]["kind"]
    for v in crd["spec"]["versions"]:
        s = fix(json.loads(json.dumps(v["schema"]["openAPIV3Schema"])))
        s["$schema"] = "http://json-schema.org/draft-07/schema#"
        s["title"] = f"{kind} {group}/{v['name']}"
        s["description"] = f"Converted from CustomResourceDefinition {crd['metadata']['name']} (apiVersion {group}/{v['name']}) as installed on the home-lab cluster; see README.md for provenance."
        json.dump(s, open(f"{kind.lower()}_{v['name']}.json", "w"), indent=2, sort_keys=True)
EOF
```

Then update the provenance table in `../schemas/README.md` (CRD `resourceVersion`, chart or operator version, date).

## Recovery-automation safety check

`python3 deploy/kustomize/tests/safety-check.py` (also run by `run.sh`, followed by `--selftest`) is the offline guard added by the Phase 2B scope reduction of 2026-10-30 (handoff section 12.9) and corrected for GB2B-07 the same day (section 12.10), after four independent reviews found defects in an executable recovery helper library that earlier remediation rounds had introduced. The library and its test suite were retired; the two operational documents that used them are now operator planning checklists. The check asserts three invariants, without a cluster and with the standard library only: **(I)** no executable recovery automation exists in the Phase 2B artifacts, **(II)** the two planning checklists contain no runnable recovery instructions and no other operational document carries a destructive or shell-evaluated command in a code block, **(III)** `run.sh` executes only approved validation targets.

| Rule | Assertion |
|---|---|
| S1 | the retired runbook directory under `deploy/` does not exist; every script under `deploy/` is one of `run.sh`, `validate.py`, `mutations.py`, `safety-check.py`. A script is any file with a script extension, an executable bit **or a `#!` first line**, whatever its name |
| S2 | nothing under `deploy/`, `docs/` (archive excluded) or the root pointer files references the retired library: its former file name, its former test file, the former runbook directory under `deploy/`, its `rb_*` function names or its `RB_*` variable names (the exact patterns are in the script); the handoff may name them as history, never in a code block or as a command |
| S3 | no **code block** in any Phase 2B operational document (the four `docs/operations/*.md`, ADR-0005 to ADR-0008, the handoff, the `deploy/**/README.md` files) contains a destructive command (`kubectl delete`/`scale`, `--force`, `--grace-period`, `--replicas`, `--cascade`, `rke2-killall`, `systemctl stop/mask/disable/kill`, `kill`, `rm -rf`, `argocd app delete`, finalizer edits) or shell evaluation of Kubernetes API output (`$(kubectl ...)`, `VAR=$(kubectl ...)`, `source`/dot-sourcing, `eval`, `set -a`; a registry lookup with `curl` in the Docker README is not API data). A code block is a fence of **three or more** backticks or tildes of any length, closed per CommonMark by a run of the same character at least as long (an unterminated fence runs to the end of the file), or an **indented block** (lines starting with four spaces or a tab outside a fence). Read-only commands in code blocks are permitted |
| S4 | `docs/operations/dev-volume-recovery.md` and `docs/operations/storage-and-network-validation.md` carry the banner `NOT APPROVED FOR EXECUTION`; their only code blocks are terminated fences whose info string is exactly `yaml` (manifest data); any other fence (any length, any or no info string), any unterminated fence, any indented block and any HTML `<pre>`, `<code>`, `<script>` or comment is **rejected rather than classified**; and none of the S3 patterns appears anywhere in them, prose included |
| S5 | no `Job` or `CronJob` under `deploy/kustomize`; `run.sh` is **byte-for-byte the reviewed runner**: a regular file (a symlink is rejected even when its target is identical), present, whose SHA-256 equals `APPROVED_RUN_SH_SHA256` in `safety-check.py`. Any difference, including a comment, a trailing space or one byte removed, is rejected; the checker never derives the digest from the file it is checking. This replaced two shell-parsing attempts that were bypassed (`bash "$HERE/recover.txt"`, then `if bash "$HERE/recover.txt"; then :; fi`), GB2B-07 |

`--selftest` copies the in-scope files to a temporary directory and plants 43 violations one at a time, each required to produce a `FAIL` for its intended rule: S1 (the retired directory reappearing, a stray `.sh`, a shebang file named `.txt` without an executable bit, an executable bit on a `.json`), S2 (a retired function name, the retired library path, an invocation in the handoff), S3 (`kubectl delete` in three-backtick, four-backtick, four-tilde, six-backtick-without-info and unterminated fences, indented and tab-indented blocks including a forced Pod deletion, `--force` in a fence, `PV=$(kubectl ...)`, a sourced evidence file), S4 (missing banner; three-backtick, four-backtick, five-tilde and info-less fences; an unterminated yaml fence; an indented read-only command; HTML `<pre>` and comment; a scale command and a `kill` in prose), S5 (a `Job` manifest; `run.sh` with Codex's conditional `if bash "$HERE/recover.txt"; then :; fi`, the same with an unresolved `${RECOVER_SCRIPT:-...}` variable, the original `bash "$HERE/recover.txt"`, a script hidden in a quoted `$(...)`, a call outside `deploy/kustomize`, `python3 -c`, another `go run` module, a bare `kubectl`, one added comment line, one trailing space, one byte removed, the runner replaced by a symlink to identical content, the runner missing). Four must-pass cases are required to stay accepted (a read-only `sh` fence in `deploy.md`, a four-backtick `yaml` data fence in a checklist, a read-only command in a four-backtick fence, the unchanged approved runner), and the clean copy must pass with the expected PASS lines (approved scripts present, retired automation absent, both checklists passing). A case whose fixture document is absent from the tree (the checklists arrive in commit group 4, the check in group 3) is reported as skipped, never as detected.

**Changing `run.sh`.** The runner is pinned, so any edit fails S5 by design. The procedure is: review the changed script and every target it invokes (today `validate.py`, `mutations.py`, `safety-check.py` with `--selftest`, and `go run github.com/yannh/kubeconform/cmd/kubeconform@v0.8.0`), then update `APPROVED_RUN_SH_SHA256` by hand in the same change, with the review recorded in the commit or handoff. The digest is never regenerated automatically and never accepted from the candidate being checked; a pull request that changes `run.sh` without a reviewed digest update must fail the offline suite.

**Limitations (stated, not hedged).** This is a structural guard over named files in this repository, not a shell or Markdown security scanner. It recognises the listed patterns and the listed code constructs; it does not interpret shell at all (the runner is compared by bytes, not understood), nor encoded or obfuscated text, images, links, HTML outside the checklists, files outside its scope, or prose instructions written without the listed tokens. Pinning `run.sh` verifies the runner's bytes only: it does not verify the safety of the programs the runner invokes transitively. Those are covered by their own means: `validate.py` and `mutations.py` are reviewed artifacts exercised by the mutation suite and the cumulative commit verification, `safety-check.py` by its self-test, kubeconform by its pinned version, and S1 keeps any other script out of `deploy/`. It proves that the repository carries no runnable recovery or deletion automation of the kinds listed and that the runner has not changed since review; it proves nothing about what an operator types at a terminal, and nothing about cluster behaviour.
