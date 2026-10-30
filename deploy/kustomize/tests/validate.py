#!/usr/bin/env python3
"""Offline validation of the GB Kustomize entry points (Phase 2B).

Renders every entry point under deploy/kustomize/apps/<env>/<app> with
`kubectl kustomize` (or `kustomize build`) and checks the properties the
migration plan and the Phase 2B brief require: ownership uniqueness across
the Applications of one environment, the frozen StatefulSet identity, one
replica, node placement, no Secret or Namespace objects, non-root security
context, no admin-port exposure, the NetworkPolicy and PodMonitor wiring,
ingress class and issuer, port names, PVC and data-path settings.

It proves rendering and structure only. It does not prove that RWOP,
NetworkPolicy enforcement, port-forward, non-root writes to the volume or
the ingress path work on any cluster; those are the live validation
runbooks (docs/operations/storage-and-network-validation.md).

Exit status: 0 when every check passes (warnings allowed), 1 otherwise.
With --require-published-image the UNPUBLISHED image sentinel is a failure.
Schema validation is a separate step (run.sh, kubeconform).
"""
from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

try:
    import yaml
except ImportError:  # pragma: no cover
    sys.exit("validate.py: PyYAML is required (python3-yaml)")

ROOT = Path(__file__).resolve().parents[1]  # deploy/kustomize
APPS = ROOT / "apps"
GOLDEN = Path(__file__).resolve().parent / "statefulset-identity.golden.json"

SENTINEL_DIGEST = "sha256:" + "0" * 64
NODE_SELECTOR = {"workload.home-apps": "true"}
ADMIN_PORT = 8081
PLAYER_PORT = 8080
IMAGE_NAME = "ghcr.io/phreddrick/gb-server"

# Patterns that must never appear in rendered manifests (secret material or
# the plain-variable form of the two secrets).
SECRET_PATTERNS = [
    re.compile(r"^\s*GB_JWT_SECRET\s*:", re.M),
    re.compile(r"^\s*GB_ADMIN_TOKEN\s*:", re.M),
    re.compile(r"name:\s*GB_(JWT_SECRET|ADMIN_TOKEN)\s*$", re.M),
    re.compile(r"(?i)(password|passwd|token|secret)\s*[:=]\s*['\"]?[A-Za-z0-9+/=_-]{16,}"),
    re.compile(r"BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY"),
    re.compile(r"\bgh[pousr]_[A-Za-z0-9]{20,}\b"),
]


class Report:
    def __init__(self) -> None:
        self.failures: list[str] = []
        self.warnings: list[str] = []
        self.passes: list[str] = []

    def ok(self, msg: str) -> None:
        self.passes.append(msg)
        print(f"PASS  {msg}")

    def warn(self, msg: str) -> None:
        self.warnings.append(msg)
        print(f"WARN  {msg}")

    def fail(self, msg: str) -> None:
        self.failures.append(msg)
        print(f"FAIL  {msg}")

    def check(self, cond: bool, msg: str) -> bool:
        (self.ok if cond else self.fail)(msg)
        return cond


def render_cmd() -> list[str]:
    if shutil.which("kustomize"):
        return ["kustomize", "build"]
    if shutil.which("kubectl"):
        return ["kubectl", "kustomize"]
    sys.exit("validate.py: neither kustomize nor kubectl is available")


def render(entry: Path, cmd: list[str]) -> tuple[str, list[dict]]:
    proc = subprocess.run(cmd + [str(entry)], capture_output=True, text=True)
    if proc.returncode != 0:
        raise RuntimeError(f"render failed for {entry}:\n{proc.stderr}")
    docs = [d for d in yaml.safe_load_all(proc.stdout) if d]
    return proc.stdout, docs


def key(obj: dict) -> tuple:
    m = obj.get("metadata", {})
    return (obj.get("apiVersion"), obj.get("kind"), m.get("namespace"), m.get("name"))


def entry_points() -> dict[str, dict[str, Path]]:
    envs: dict[str, dict[str, Path]] = {}
    for kfile in sorted(APPS.glob("*/*/kustomization.yaml")):
        env, app = kfile.parent.parent.name, kfile.parent.name
        envs.setdefault(env, {})[app] = kfile.parent
    return envs


def identity_of(sts: dict) -> dict:
    spec = sts["spec"]
    vcts = []
    for t in spec.get("volumeClaimTemplates", []):
        vcts.append(
            {
                "name": t["metadata"]["name"],
                "accessModes": t["spec"].get("accessModes"),
                "storageClassName": t["spec"].get("storageClassName"),
                "storage": t["spec"].get("resources", {}).get("requests", {}).get("storage"),
            }
        )
    return {
        "kind": sts["kind"],
        "name": sts["metadata"]["name"],
        "replicas": spec.get("replicas"),
        "serviceName": spec.get("serviceName"),
        "podManagementPolicy": spec.get("podManagementPolicy"),
        "selector": spec.get("selector"),
        "persistentVolumeClaimRetentionPolicy": spec.get("persistentVolumeClaimRetentionPolicy"),
        "volumeClaimTemplates": vcts,
    }


def find(docs: list[dict], kind: str, name: str | None = None) -> list[dict]:
    return [d for d in docs if d.get("kind") == kind and (name is None or d["metadata"]["name"] == name)]


def one(docs: list[dict], kind: str, name: str) -> dict | None:
    """The single object kind/name, or None (never raises on a missing resource)."""
    hits = find(docs, kind, name)
    return hits[0] if len(hits) == 1 else None


# ---------------------------------------------------------------------------
# NetworkPolicy model (GB2B-01).
#
# The approved Phase 2 design is a closed set of four policies. The validator
# (a) applies explicit semantic checks that name the security reason for a
# rejection, and (b) compares every rendered NetworkPolicy, normalised, with
# the approved specification and rejects any extra, missing or modified
# policy. NetworkPolicies are additive: one extra permissive policy defeats
# every restrictive one, so the whole rendered set of an environment is
# evaluated, across both Applications. Within one rule, `from`/`to` peers are
# OR alternatives, and a peer that carries both namespaceSelector and
# podSelector is a single AND condition; a peer with only one of them
# matches every pod of the namespace(s) or every namespace, which the
# approved design never does. A missing `ports` list opens every port; a
# missing `from`/`to` admits every source/destination; an empty rule `{}`
# allows everything. This is a static model of the manifests: it says nothing
# about Cilium enforcement or node-originated traffic, which only the live
# tests cover.
# ---------------------------------------------------------------------------

ENGINE_LABELS = {"app.kubernetes.io/name": "gb-engine", "app.kubernetes.io/component": "engine"}
TRAEFIK_PEER = {
    "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": "kube-system"}},
    "podSelector": {"matchLabels": {"app.kubernetes.io/name": "rke2-traefik", "app.kubernetes.io/instance": "rke2-traefik-kube-system"}},
}
PROMETHEUS_PEER = {
    "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": "cattle-monitoring-system"}},
    "podSelector": {"matchLabels": {"app.kubernetes.io/name": "prometheus", "prometheus": "rancher-monitoring-prometheus"}},
}
DNS_PEER = {
    "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": "kube-system"}},
    "podSelector": {"matchLabels": {"k8s-app": "kube-dns"}},
}
APPROVED_NETWORKPOLICIES: dict[str, dict] = {
    "default-deny-all": {"podSelector": {}, "policyTypes": ["Egress", "Ingress"]},
    "allow-dns-egress": {
        "podSelector": {},
        "policyTypes": ["Egress"],
        "egress": [{"to": [DNS_PEER], "ports": [{"port": 53, "protocol": "TCP"}, {"port": 53, "protocol": "UDP"}]}],
    },
    "gb-engine-allow-player-from-traefik": {
        "podSelector": {"matchLabels": ENGINE_LABELS},
        "policyTypes": ["Ingress"],
        "ingress": [{"from": [TRAEFIK_PEER], "ports": [{"port": "player", "protocol": "TCP"}]}],
    },
    "gb-engine-allow-metrics-from-monitoring": {
        "podSelector": {"matchLabels": ENGINE_LABELS},
        "policyTypes": ["Ingress"],
        "ingress": [{"from": [PROMETHEUS_PEER], "ports": [{"port": "admin", "protocol": "TCP"}]}],
    },
}
# What the approved set permits, as the static model sees it (used for the
# positive assertions and the "no other path to 8081" check).
APPROVED_ENGINE_INGRESS = {("traefik", "player"), ("prometheus", "admin")}
PORT_ALIASES = {8080: "player", 8081: "admin", "8080": "player", "8081": "admin"}


def _canon(x):
    return json.dumps(x, sort_keys=True, separators=(",", ":"))


def norm_np(spec: dict) -> dict:
    """Normalise a NetworkPolicySpec for equality comparison.

    Defaults are made explicit (protocol TCP), lists whose order carries no
    meaning (policyTypes, peers, ports) are sorted, and empty rule lists are
    dropped (an empty list allows nothing, the same as an absent key). An
    empty rule object `{}` is kept as is, because it allows everything.
    """
    out: dict = {}
    out["podSelector"] = spec.get("podSelector", {}) or {}
    if "policyTypes" in spec:
        out["policyTypes"] = sorted(spec["policyTypes"])
    for direction, peer_key in (("ingress", "from"), ("egress", "to")):
        rules = spec.get(direction)
        if not rules:
            continue
        nrules = []
        for rule in rules:
            if rule is None:
                rule = {}
            nrule: dict = {}
            if peer_key in rule:
                nrule[peer_key] = sorted((p or {} for p in rule[peer_key]), key=_canon)
            if "ports" in rule:
                nports = []
                for p in rule["ports"]:
                    p = dict(p or {})
                    p.setdefault("protocol", "TCP")
                    nports.append(p)
                nrule["ports"] = sorted(nports, key=_canon)
            nrules.append(nrule)
        out[direction] = sorted(nrules, key=_canon)
    return out


def peer_identity(peer: dict) -> str | None:
    """Name the approved peer a selector pair denotes, or None."""
    for name, approved in (("traefik", TRAEFIK_PEER), ("prometheus", PROMETHEUS_PEER), ("dns", DNS_PEER)):
        if _canon(peer) == _canon(approved):
            return name
    return None


def selects_engine(pod_selector: dict) -> bool:
    """True when a policy's podSelector matches the engine pod (empty selects all pods)."""
    if not pod_selector:
        return True
    if pod_selector.get("matchExpressions"):
        return True  # conservatively assume it may match; the allowlist rejects it anyway
    ml = pod_selector.get("matchLabels") or {}
    return all(ENGINE_LABELS.get(k) == v for k, v in ml.items())


def rule_problems(direction: str, rule: dict | None) -> list[str]:
    """Semantic problems of one ingress/egress rule that broaden access."""
    peer_key = "from" if direction == "ingress" else "to"
    if not rule:
        return [f"empty {direction} rule {{}} allows all {direction} traffic"]
    probs = []
    peers = rule.get(peer_key)
    if peers is None or peers == []:
        probs.append(f"{direction} rule without '{peer_key}' admits every {'source' if direction == 'ingress' else 'destination'}")
    else:
        if len(peers) > 1:
            probs.append(f"{direction} rule lists {len(peers)} peers; peers are OR alternatives and the approved design has exactly one combined peer per rule")
        for peer in peers:
            peer = peer or {}
            if "ipBlock" in peer:
                probs.append(f"{direction} rule uses ipBlock; only pod selectors are approved")
            ns, ps = peer.get("namespaceSelector"), peer.get("podSelector")
            if ns is None or ps is None:
                probs.append(f"{direction} peer must combine namespaceSelector and podSelector (a lone selector matches every pod of the namespace, or every namespace)")
            else:
                if not (ns.get("matchLabels")) or ns.get("matchExpressions"):
                    probs.append(f"{direction} peer namespaceSelector must be an exact matchLabels (got {ns})")
                if not (ps.get("matchLabels")) or ps.get("matchExpressions"):
                    probs.append(f"{direction} peer podSelector must be an exact matchLabels (got {ps})")
                if ns.get("matchLabels") and ps.get("matchLabels") and peer_identity(peer) is None:
                    probs.append(f"{direction} peer does not match any approved peer (traefik, prometheus, dns): {peer}")
    ports = rule.get("ports")
    if ports is None or ports == []:
        probs.append(f"{direction} rule without 'ports' opens every port")
    else:
        for p in ports:
            p = p or {}
            if "port" not in p:
                probs.append(f"{direction} port entry without 'port' opens every {p.get('protocol', 'TCP')} port")
            if "endPort" in p:
                probs.append(f"{direction} port range {p} is not approved")
    return probs


def check_networkpolicies(tag: str, nps: list[tuple[str, dict]], r: Report) -> None:
    """Evaluate the complete rendered NetworkPolicy set of one environment."""
    by_name: dict[str, list[tuple[str, dict]]] = {}
    for app, np in nps:
        by_name.setdefault(np["metadata"]["name"], []).append((app, np))

    names = set(by_name)
    missing = sorted(set(APPROVED_NETWORKPOLICIES) - names)
    r.check(not missing, f"{tag} required NetworkPolicies present" + (f"; missing {missing} (a required player, DNS or monitoring permission or the default deny is gone)" if missing else f" ({sorted(APPROVED_NETWORKPOLICIES)})"))
    extra = sorted(names - set(APPROVED_NETWORKPOLICIES))
    r.check(not extra, f"{tag} no NetworkPolicy outside the approved set" + (f"; unexpected NetworkPolicy {extra} (policies are additive: an extra permissive policy would defeat the deny)" if extra else ""))
    dup = sorted(n for n, lst in by_name.items() if len(lst) > 1)
    r.check(not dup, f"{tag} no NetworkPolicy name rendered twice" + (f": {dup}" if dup else ""))

    # Engine-reachability model built from every rendered policy, approved or not.
    engine_ingress: set[tuple[str, str]] = set()
    unexpected_paths: list[str] = []
    egress_targets: set[str] = set()

    for name, lst in sorted(by_name.items()):
        for app, np in lst:
            spec = np.get("spec") or {}
            where = f"{tag} NetworkPolicy {name} ({app})"
            pod_sel = spec.get("podSelector")
            ptypes = spec.get("policyTypes")
            if ptypes is None:
                r.fail(f"{where}: policyTypes must be explicit (an inferred value changes with the rules present)")
                ptypes = []
            problems: list[str] = []
            if name == "default-deny-all":
                if pod_sel not in ({}, None):
                    problems.append(f"default deny must select every pod (podSelector {{}}), got {pod_sel}")
                if sorted(ptypes) != ["Egress", "Ingress"]:
                    problems.append(f"default deny must list policyTypes Ingress and Egress, got {ptypes} (a missing direction is not denied)")
                for direction in ("ingress", "egress"):
                    if spec.get(direction):
                        problems.append(f"default deny contains an {direction} allow rule {spec.get(direction)}: it would allow {direction} traffic instead of denying it")
            else:
                for direction in ("ingress", "egress"):
                    rules = spec.get(direction) or []
                    enforced = direction.capitalize() in ptypes
                    if rules and not enforced:
                        problems.append(f"{direction} rules present but policyTypes {ptypes} does not include {direction.capitalize()}: the rules have no effect and the intended traffic would be denied")
                    for rule in rules:
                        problems.extend(rule_problems(direction, rule))
                        # Build the model regardless of problems.
                        peer_key = "from" if direction == "ingress" else "to"
                        peers = rule.get(peer_key) if rule else None
                        ports = rule.get("ports") if rule else None
                        peer_names = [peer_identity(p or {}) or "ANY" for p in (peers or [None])]
                        port_names = [PORT_ALIASES.get(p.get("port"), p.get("port")) if (p and "port" in p) else "ALL" for p in (ports or [None])]
                        if direction == "ingress" and selects_engine(pod_sel or {}):
                            for pn in peer_names:
                                for po in port_names:
                                    engine_ingress.add((pn, str(po)))
                        if direction == "egress":
                            egress_targets.update(peer_names)
                if name in ("gb-engine-allow-player-from-traefik", "gb-engine-allow-metrics-from-monitoring"):
                    if _canon(pod_sel or {}) != _canon({"matchLabels": ENGINE_LABELS}):
                        problems.append(f"podSelector must be exactly the engine labels {ENGINE_LABELS}, got {pod_sel} (an empty or wider selector applies the allow to every GB pod)")
                    if sorted(ptypes) != ["Ingress"]:
                        problems.append(f"policyTypes must be exactly [Ingress], got {ptypes}")
                if name == "allow-dns-egress":
                    if pod_sel not in ({}, None):
                        problems.append(f"DNS egress must apply to every pod (podSelector {{}}), got {pod_sel}")
                    if sorted(ptypes) != ["Egress"]:
                        problems.append(f"policyTypes must be exactly [Egress], got {ptypes}")
            for p in problems:
                r.fail(f"{where}: {p}")
            if not problems:
                r.ok(f"{where}: rule semantics as approved")
            # Strict allowlist comparison of the normalised specification.
            approved = APPROVED_NETWORKPOLICIES.get(name)
            if approved is not None:
                got, want = norm_np(spec), norm_np(approved)
                if got != want:
                    r.fail(f"{where}: normalised spec differs from the approved specification\n        rendered: {_canon(got)}\n        approved: {_canon(want)}")
                else:
                    r.ok(f"{where}: normalised spec equals the approved specification")

    # Positive assertions of the static model (only meaningful when the set is complete).
    if not missing:
        wrong = sorted(engine_ingress - APPROVED_ENGINE_INGRESS)
        r.check(not wrong, f"{tag} static model: engine ingress is exactly player<-Traefik and admin<-Prometheus" + (f"; additional paths {wrong} (ANY = unrestricted source, ALL = every port)" if wrong else ""))
        admin_sources = sorted(src for src, port in engine_ingress if port in ("admin", "ALL"))
        r.check(admin_sources == ["prometheus"], f"{tag} static model: port 8081 (admin) is reachable only from the approved Prometheus pods (sources {admin_sources}); bearer-token auth still required; node-originated paths and Cilium enforcement verified live only")
        r.check(("traefik", "player") in engine_ingress, f"{tag} static model: player and WebSocket traffic from Traefik to port 8080 remains permitted")
        r.check(egress_targets <= {"dns"} and "dns" in egress_targets, f"{tag} static model: egress is DNS to kube-dns only (targets {sorted(egress_targets)})")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--require-published-image", action="store_true", help="treat the all-zero digest sentinel as a failure")
    ap.add_argument("--out-dir", type=Path, help="write each rendered entry point as <env>-<app>.yaml here (for kubeconform)")
    args = ap.parse_args()

    r = Report()
    cmd = render_cmd()
    print(f"renderer: {' '.join(cmd)}")
    golden = json.loads(GOLDEN.read_text())
    golden_cmp = {k: v for k, v in golden.items() if not k.startswith("_") and k != "ci_overrides_allowed"}

    envs = entry_points()
    r.check(set(envs) >= {"dev", "ci"} and all({"platform", "server"} <= set(a) for a in envs.values()),
            f"entry points present: {', '.join(f'{e}/{a}' for e in sorted(envs) for a in sorted(envs[e]))}")

    rendered: dict[str, dict[str, tuple[str, list[dict]]]] = {}
    for env, apps in envs.items():
        for app, path in apps.items():
            try:
                text, docs = render(path, cmd)
            except RuntimeError as e:
                r.fail(str(e))
                continue
            rendered.setdefault(env, {})[app] = (text, docs)
            r.ok(f"[{env}/{app}] renders ({len(docs)} objects)")
            if args.out_dir:
                args.out_dir.mkdir(parents=True, exist_ok=True)
                (args.out_dir / f"{env}-{app}.yaml").write_text(text)

    for env, apps in rendered.items():
        tag = f"[{env}]"
        all_docs = [(app, d) for app, (_, docs) in apps.items() for d in docs]

        # 2 + 15: ownership uniqueness across the Applications of one environment
        seen: dict[tuple, str] = {}
        dups = []
        for app, d in all_docs:
            k = key(d)
            if k in seen and seen[k] != app:
                dups.append((k, seen[k], app))
            seen.setdefault(k, app)
        r.check(not dups, f"{tag} no resource rendered by two entry points" + (f": {dups}" if dups else ""))
        same_app_dups = [k for k in set(key(d) for _, d in all_docs) if sum(1 for _, d in all_docs if key(d) == k) > 1 and all(a == seen[k] for a, d in all_docs if key(d) == k)]
        r.check(not same_app_dups, f"{tag} no duplicate object within one entry point" + (f": {same_app_dups}" if same_app_dups else ""))

        # 14: no Namespace object; 6: no Secret object; every object namespaced into one namespace
        kinds = [d.get("kind") for _, d in all_docs]
        r.check("Namespace" not in kinds, f"{tag} no Namespace object rendered (operator-owned, Decision C)")
        r.check("Secret" not in kinds, f"{tag} no Secret object rendered")
        nss = {d["metadata"].get("namespace") for _, d in all_docs}
        r.check(nss == {"gb-dev"}, f"{tag} every object targets namespace gb-dev (got {sorted(map(str, nss))})")
        for app, (text, _) in apps.items():
            hits = [p.pattern for p in SECRET_PATTERNS if p.search(text)]
            r.check(not hits, f"{tag}/{app} no secret values or plain secret variables in the render" + (f": {hits}" if hits else ""))

        # kind inventories
        plat_docs = apps["platform"][1]
        srv_docs = apps["server"][1]
        r.check(not find(plat_docs, "StatefulSet") and not find(plat_docs, "Service") and not find(plat_docs, "Ingress"),
                f"{tag} platform renders no workload, Service or Ingress")
        r.check(bool(find(plat_docs, "ServiceAccount", "gb-engine")), f"{tag} platform renders ServiceAccount gb-engine")
        sa = find(plat_docs, "ServiceAccount", "gb-engine")
        r.check(bool(sa) and sa[0].get("automountServiceAccountToken") is False, f"{tag} ServiceAccount does not automount a token")
        r.check(not find(plat_docs, "Role") and not find(plat_docs, "RoleBinding") and not find(plat_docs, "ClusterRole") and not find(plat_docs, "ClusterRoleBinding"),
                f"{tag} no RBAC objects (the server never calls the API)")

        # 10: NetworkPolicies, evaluated as the complete rendered set of the
        # environment (both Applications), GB2B-01.
        check_networkpolicies(tag, [(app, d) for app, d in all_docs if d.get("kind") == "NetworkPolicy"], r)
        np_outside_platform = [key(d) for app, d in all_docs if d.get("kind") == "NetworkPolicy" and app != "platform"]
        r.check(not np_outside_platform, f"{tag} NetworkPolicies are owned by the platform entry point only" + (f": {np_outside_platform}" if np_outside_platform else ""))

        pms = find(plat_docs, "PodMonitor")
        if env == "ci":
            r.check(not pms, f"{tag} ci drops the PodMonitor (no prometheus-operator in kind)")
        else:
            r.check(len(pms) == 1 and pms[0]["spec"]["podMetricsEndpoints"][0].get("port") == "admin" and pms[0]["spec"]["podMetricsEndpoints"][0].get("path") == "/metrics",
                    f"{tag} PodMonitor scrapes named port 'admin' at /metrics")
            if pms:
                sel = pms[0]["spec"].get("selector", {}).get("matchLabels", {})
                sts_for_pm = one(srv_docs, "StatefulSet", "gb-engine")
                if sts_for_pm is None:
                    r.fail(f"{tag} PodMonitor selector cannot be checked: StatefulSet gb-engine is missing or renamed")
                else:
                    sts_sel = sts_for_pm.get("spec", {}).get("selector", {}).get("matchLabels", {})
                    r.check(bool(sel) and all(sts_sel.get(k) == v for k, v in sel.items()), f"{tag} PodMonitor selector matches the StatefulSet pod labels")

        # 3/4/5/7/8/12/13: the StatefulSet
        stss = find(srv_docs, "StatefulSet")
        sts_names = [s.get("metadata", {}).get("name") for s in stss]
        if not r.check(len(stss) == 1 and sts_names == ["gb-engine"], f"{tag} exactly one StatefulSet, named gb-engine (found {sts_names}); the frozen identity, probes, security context and volume checks need it and were skipped"):
            continue
        sts = stss[0]
        ident = identity_of(sts)
        if env == "ci":
            cmp_ident = json.loads(json.dumps(ident))
            cmp_golden = json.loads(json.dumps(golden_cmp))
            for t in cmp_ident["volumeClaimTemplates"] + cmp_golden["volumeClaimTemplates"]:
                t.pop("accessModes", None)
                t.pop("storageClassName", None)
            r.check(cmp_ident == cmp_golden, f"{tag} StatefulSet identity equals the golden file (ci may differ only in access mode and storage class)")
        else:
            same = ident == golden_cmp
            r.check(same, f"{tag} StatefulSet identity equals statefulset-identity.golden.json")
            if not same:
                print("      rendered:", json.dumps(ident, sort_keys=True))
                print("      golden:  ", json.dumps(golden_cmp, sort_keys=True))
        r.check(sts["spec"].get("replicas") == 1, f"{tag} replicas == 1")
        r.check(sts["spec"]["serviceName"] == "gb-engine", f"{tag} serviceName == gb-engine")
        headless = find(srv_docs, "Service", "gb-engine")
        r.check(bool(headless) and headless[0]["spec"].get("clusterIP") == "None" and headless[0]["spec"].get("selector") == sts["spec"]["selector"]["matchLabels"],
                f"{tag} headless Service gb-engine exists with the StatefulSet selector")
        pod = sts["spec"]["template"]["spec"]
        tmpl_labels = sts["spec"]["template"]["metadata"].get("labels", {})
        r.check(all(tmpl_labels.get(k) == v for k, v in sts["spec"]["selector"]["matchLabels"].items()), f"{tag} pod template labels satisfy the selector")
        if env == "ci":
            r.check("nodeSelector" not in pod, f"{tag} ci has no node selector")
        else:
            r.check(pod.get("nodeSelector") == NODE_SELECTOR, f"{tag} nodeSelector is exactly {NODE_SELECTOR}")
        r.check(not pod.get("affinity") and not pod.get("tolerations"), f"{tag} no affinity, no tolerations (no control-plane or GPU scheduling path)")
        r.check(pod.get("automountServiceAccountToken") is False and pod.get("serviceAccountName") == "gb-engine", f"{tag} pod uses ServiceAccount gb-engine without a mounted token")
        psc = pod.get("securityContext", {})
        r.check(psc.get("runAsNonRoot") is True and psc.get("runAsUser") == 65532 and psc.get("runAsGroup") == 65532 and psc.get("fsGroup") == 65532
                and psc.get("seccompProfile", {}).get("type") == "RuntimeDefault" and psc.get("fsGroupChangePolicy") == "OnRootMismatch",
                f"{tag} pod securityContext: non-root 65532, fsGroup 65532, seccomp RuntimeDefault")
        r.check(not pod.get("hostNetwork") and not pod.get("hostPID") and not pod.get("hostIPC") and not pod.get("initContainers"),
                f"{tag} no hostNetwork/hostPID/hostIPC and no init containers")
        hooked = [key(d) for _, d in all_docs if any(a.startswith("argocd.argoproj.io/hook") or a == "helm.sh/hook" for a in (d.get("metadata", {}).get("annotations") or {}))]
        r.check(not hooked, f"{tag} no sync hooks (universe bootstrap is an explicit gbctl operation, ADR-0008)" + (f": {hooked}" if hooked else ""))
        r.check(not find(srv_docs, "Job") and not find(srv_docs, "CronJob") and not find(plat_docs, "Job") and not find(plat_docs, "CronJob"), f"{tag} no Job or CronJob touches the volume")
        vols = {v["name"]: v for v in pod.get("volumes", [])}
        r.check(all("hostPath" not in v for v in vols.values()), f"{tag} no hostPath volumes")
        containers = pod.get("containers", [])
        r.check(len(containers) == 1 and containers[0]["name"] == "gb-server", f"{tag} exactly one container, gb-server")
        c = containers[0]
        csc = c.get("securityContext", {})
        r.check(csc.get("allowPrivilegeEscalation") is False and csc.get("readOnlyRootFilesystem") is True and csc.get("capabilities", {}).get("drop") == ["ALL"] and not csc.get("privileged"),
                f"{tag} container securityContext: no escalation, read-only root, drop ALL, not privileged")
        ports = {p["name"]: p["containerPort"] for p in c.get("ports", [])}
        r.check(ports == {"player": PLAYER_PORT, "admin": ADMIN_PORT}, f"{tag} container ports are player=8080 and admin=8081 (got {ports})")
        res = c.get("resources", {})
        r.check(all(res.get(k, {}).get(q) for k in ("requests", "limits") for q in ("cpu", "memory")), f"{tag} cpu and memory requests and limits are set")
        probes = {k: c.get(k, {}).get("httpGet", {}) for k in ("startupProbe", "livenessProbe", "readinessProbe")}
        r.check(probes["livenessProbe"].get("path") == "/livez" and probes["readinessProbe"].get("path") == "/readyz" and probes["startupProbe"].get("path") == "/readyz"
                and all(p.get("port") == "player" for p in probes.values()),
                f"{tag} probes: startup /readyz, liveness /livez, readiness /readyz on port player")
        r.check(pod.get("terminationGracePeriodSeconds", 30) >= 300, f"{tag} terminationGracePeriodSeconds >= 300")
        # image reference
        image = c.get("image", "")
        if env == "ci":
            r.check(image == "gb-server:ci", f"{tag} ci image is the locally loaded tag gb-server:ci")
        else:
            if image == f"{IMAGE_NAME}@{SENTINEL_DIGEST}":
                (r.fail if args.require_published_image else r.warn)(f"{tag} image is the UNPUBLISHED sentinel {image}; replace with the CI publish digest before sync")
            else:
                r.check(re.fullmatch(re.escape(IMAGE_NAME) + r"@sha256:[0-9a-f]{64}", image or "") is not None, f"{tag} image is {IMAGE_NAME} pinned by digest (got {image!r})")
        # env and secrets wiring
        env_names = {e["name"]: e for e in c.get("env", [])}
        r.check(env_names.get("GB_JWT_SECRET_FILE", {}).get("value") == "/var/run/secrets/gb/jwt-secret" and env_names.get("GB_ADMIN_TOKEN_FILE", {}).get("value") == "/var/run/secrets/gb/admin-token",
                f"{tag} secrets are consumed through GB_*_FILE from /var/run/secrets/gb")
        r.check("GB_JWT_SECRET" not in env_names and "GB_ADMIN_TOKEN" not in env_names, f"{tag} the plain secret variables are not set")
        sec_vol = vols.get("gb-secrets", {}).get("secret", {})
        r.check(sec_vol.get("secretName") == "gb-server-secrets" and {i["key"] for i in sec_vol.get("items", [])} == {"jwt-secret", "admin-token"} and sec_vol.get("defaultMode") == 0o440,
                f"{tag} Secret gb-server-secrets mounted read-only with keys jwt-secret and admin-token (mode 0440)")
        mounts = {m["name"]: m for m in c.get("volumeMounts", [])}
        r.check(mounts.get("gb-secrets", {}).get("readOnly") is True and mounts.get("gb-secrets", {}).get("mountPath") == "/var/run/secrets/gb", f"{tag} secret mount is read-only at /var/run/secrets/gb")
        cms = find(srv_docs, "ConfigMap")
        cm_data: dict = {}
        for cm in cms:
            cm_data.update(cm.get("data", {}))
        ref = [e for e in c.get("envFrom", []) if e.get("configMapRef")]
        r.check(bool(ref) and any(cm["metadata"]["name"] == ref[0]["configMapRef"]["name"] for cm in cms), f"{tag} envFrom references a rendered ConfigMap (hash-suffixed)")
        r.check(cm_data.get("GB_ADDR") == f"0.0.0.0:{PLAYER_PORT}" and cm_data.get("GB_ADMIN_ADDR") == f"0.0.0.0:{ADMIN_PORT}", f"{tag} GB_ADDR=0.0.0.0:8080 and GB_ADMIN_ADDR=0.0.0.0:8081 (Decision A)")
        db_path = cm_data.get("GB_DB_PATH", "")
        world_mount = mounts.get("world", {}).get("mountPath", "")
        r.check(bool(world_mount) and db_path.startswith(world_mount.rstrip("/") + "/") and os.path.dirname(db_path) == world_mount.rstrip("/"),
                f"{tag} GB_DB_PATH ({db_path}) is a file directly inside the world mount ({world_mount})")
        r.check("GB_DATA_PATH" not in cm_data, f"{tag} the deprecated GB_DATA_PATH alias is not used")
        try:
            r.check(int(cm_data.get("GB_SHUTDOWN_TIMEOUT", "30")) < pod.get("terminationGracePeriodSeconds", 30), f"{tag} GB_SHUTDOWN_TIMEOUT < terminationGracePeriodSeconds")
        except ValueError:
            r.fail(f"{tag} GB_SHUTDOWN_TIMEOUT is not an integer")
        r.check(cm_data.get("GB_ENROLL_PREVIEW", "false") == "false", f"{tag} enrollment preview stays disabled")
        r.check(bool(cm_data.get("GB_WS_ALLOWED_ORIGINS")), f"{tag} GB_WS_ALLOWED_ORIGINS is set for the public origin")
        # the volume claim template
        vct = sts["spec"].get("volumeClaimTemplates", [])
        r.check(len(vct) == 1 and vct[0]["metadata"]["name"] == "world" and mounts.get("world", {}).get("mountPath") == "/data/world",
                f"{tag} one volumeClaimTemplate 'world' mounted at /data/world")

        # 9: no admin exposure through Services or Ingress
        for svc in find(srv_docs, "Service"):
            sp = [(p.get("name"), p.get("port"), p.get("targetPort")) for p in svc["spec"].get("ports", [])]
            r.check(all(p[0] != "admin" and p[1] != ADMIN_PORT and p[2] not in ("admin", ADMIN_PORT) for p in sp),
                    f"{tag} Service {svc['metadata']['name']} does not expose the admin port ({sp})")
        ings = find(srv_docs, "Ingress")
        if env == "ci":
            r.check(not ings and not find(srv_docs, "Middleware"), f"{tag} ci renders no Ingress or Middleware")
        else:
            # 11: ingress class, issuer, host, TLS, middleware
            r.check(len(ings) == 1, f"{tag} exactly one Ingress")
            if ings:
                ing = ings[0]
                ann = ing["metadata"].get("annotations", {})
                r.check(ing["spec"].get("ingressClassName") == "traefik", f"{tag} ingressClassName is traefik")
                r.check(ann.get("cert-manager.io/cluster-issuer") == "letsencrypt-production-cloudflare", f"{tag} issuer is letsencrypt-production-cloudflare (Decision B)")
                hosts = [rule["host"] for rule in ing["spec"].get("rules", [])]
                tls_hosts = [h for t in ing["spec"].get("tls", []) for h in t.get("hosts", [])]
                r.check(hosts == ["gb-dev.home.spukhaus.com"] and tls_hosts == hosts, f"{tag} host gb-dev.home.spukhaus.com with matching TLS entry")
                r.check(all(t.get("secretName") == "gb-dev-tls" for t in ing["spec"].get("tls", [])), f"{tag} TLS secret gb-dev-tls (ingress-shim generated, never rendered)")
                r.check("example.invalid" not in yaml.safe_dump(ing), f"{tag} no placeholder host left in the Ingress")
                backends = [(p["backend"]["service"]["name"], p["backend"]["service"]["port"]) for rule in ing["spec"]["rules"] for p in rule["http"]["paths"]]
                r.check(backends == [("gb-player", {"name": "player"})], f"{tag} Ingress routes only to Service gb-player port player ({backends})")
                mw = ann.get("traefik.ingress.kubernetes.io/router.middlewares", "")
                r.check(mw == "gb-dev-https-redirect@kubernetescrd", f"{tag} middleware annotation references gb-dev-https-redirect@kubernetescrd")
                r.check(bool(find(srv_docs, "Middleware", "https-redirect")), f"{tag} Middleware https-redirect is rendered by the server entry point")
            origins = cm_data.get("GB_WS_ALLOWED_ORIGINS", "")
            r.check(origins == "https://gb-dev.home.spukhaus.com", f"{tag} GB_WS_ALLOWED_ORIGINS matches the Ingress host")

    print()
    print(f"validate.py: {len(r.passes)} passed, {len(r.warnings)} warnings, {len(r.failures)} failed")
    for w in r.warnings:
        print(f"  warning: {w}")
    for f in r.failures:
        print(f"  failure: {f}")
    return 1 if r.failures else 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except SystemExit:
        raise
    except Exception as exc:  # noqa: BLE001 - a validator bug must still be a controlled, nonzero failure
        print(f"FAIL  validate.py internal error: {type(exc).__name__}: {exc} (validator defect; the manifests were not fully checked)")
        sys.exit(1)
