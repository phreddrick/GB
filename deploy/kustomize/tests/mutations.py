#!/usr/bin/env python3
"""Mutation suite for validate.py (Phase 2B, GB2B-01 regression coverage).

Each case copies deploy/kustomize to a temporary directory, applies one
mutation, runs the copied validate.py and requires: exit status 1, no Python
traceback, and the specific security or structure reason in the output. The
unmodified tree must pass (exit 0, the UNPUBLISHED sentinel as the only
warning) and --require-published-image must reject the sentinel.

Usage: python3 deploy/kustomize/tests/mutations.py [-k substring] [-v]
"""
from __future__ import annotations

import argparse
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]  # deploy/kustomize

PLAT = "base/platform/kustomization.yaml"
DENY = "base/platform/networkpolicy-default-deny.yaml"
MON = "base/platform/networkpolicy-allow-metrics-from-monitoring.yaml"
TRAEFIK = "base/platform/networkpolicy-allow-player-from-traefik.yaml"
DNS = "base/platform/networkpolicy-allow-dns.yaml"
STS = "base/server/statefulset.yaml"
SVC = "base/server/service-player.yaml"
SRVK = "base/server/kustomization.yaml"
DEVSRV = "apps/dev/server/kustomization.yaml"
DEVPLAT = "apps/dev/platform/kustomization.yaml"
SA = "base/platform/serviceaccount.yaml"

PROM_PEER_YAML = """        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: cattle-monitoring-system
          podSelector:
            matchLabels:
              app.kubernetes.io/name: prometheus
              prometheus: rancher-monitoring-prometheus
"""


def replace(path: str, old: str, new: str, count: int = 1):
    def apply(tree: Path) -> None:
        p = tree / path
        s = p.read_text()
        if old not in s:
            raise RuntimeError(f"mutation precondition failed: {old!r} not in {path}")
        p.write_text(s.replace(old, new, count))
    return apply


def add_platform_resource(filename: str, body: str):
    def apply(tree: Path) -> None:
        (tree / "base/platform" / filename).write_text(body)
        replace(PLAT, "  - podmonitor.yaml\n", f"  - podmonitor.yaml\n  - {filename}\n")(tree)
    return apply


def many(*fns):
    def apply(tree: Path) -> None:
        for fn in fns:
            fn(tree)
    return apply


def extra_policy(name: str, spec_yaml: str) -> str:
    return f"apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n  name: {name}\nspec:\n{spec_yaml}"


# (name, mutation, expected reason substring). The reason must be the
# security or structure finding, not an unrelated error.
CASES: list[tuple[str, object, str]] = [
    # ---- original structural cases (Phase 2B handoff section 7) ----
    ("replicas-2", replace(STS, "  replicas: 1\n", "  replicas: 2\n"), "replicas == 1"),
    ("admin-port-on-player-service", replace(SVC, "      targetPort: player\n", "      targetPort: admin\n"), "does not expose the admin port"),
    ("run-as-root", replace(STS, "runAsUser: 65532", "runAsUser: 0"), "pod securityContext"),
    ("rwo-in-dev", replace(STS, "- ReadWriteOncePod", "- ReadWriteOnce"), "StatefulSet identity equals statefulset-identity.golden.json"),
    ("renamed-statefulset", replace(STS, "metadata:\n  name: gb-engine\n", "metadata:\n  name: gb-engine2\n"), "exactly one StatefulSet, named gb-engine"),
    ("plain-admin-token-in-configmap", replace(SRVK, "      - GB_LOG_LEVEL=info\n", "      - GB_LOG_LEVEL=info\n      - GB_ADMIN_TOKEN=ZZZZZZZZZZZZZZZZZZZZ\n"), "no secret values or plain secret variables"),
    ("staging-issuer", replace(DEVSRV, "letsencrypt-production-cloudflare", "letsencrypt-staging-cloudflare"), "issuer is letsencrypt-production-cloudflare"),
    ("gpu-node-selector", replace(STS, 'workload.home-apps: "true"', 'accelerator: nvidia'), "nodeSelector is exactly"),
    ("default-deny-removed", replace(PLAT, "  - networkpolicy-default-deny.yaml\n", ""), "missing ['default-deny-all']"),
    ("namespace-object", many(lambda t: (t / "apps/dev/platform/ns.yaml").write_text("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: gb-dev\n"),
                              replace(DEVPLAT, "resources:\n", "resources:\n  - ns.yaml\n")), "no Namespace object rendered"),
    ("writable-root-filesystem", replace(STS, "readOnlyRootFilesystem: true", "readOnlyRootFilesystem: false"), "container securityContext"),
    ("short-grace-period", replace(STS, "terminationGracePeriodSeconds: 300", "terminationGracePeriodSeconds: 60"), "terminationGracePeriodSeconds >= 300"),
    ("presync-hook", replace(SA, "metadata:\n  name: gb-engine\n", "metadata:\n  name: gb-engine\n  annotations:\n    argocd.argoproj.io/hook: PreSync\n"), "no sync hooks"),
    # ---- NetworkPolicy bypasses (GB2B-01) ----
    ("np01-default-deny-empty-ingress-rule", replace(DENY, "    - Egress\n", "    - Egress\n  ingress:\n    - {}\n"), "default deny contains an ingress allow rule"),
    ("np02-default-deny-empty-egress-rule", replace(DENY, "    - Egress\n", "    - Egress\n  egress:\n    - {}\n"), "default deny contains an egress allow rule"),
    ("np03-extra-policy-allow-all-ingress", add_platform_resource("extra.yaml", extra_policy("allow-all", "  podSelector: {}\n  policyTypes: [Ingress, Egress]\n  ingress: [{}]\n  egress: [{}]\n")), "unexpected NetworkPolicy ['allow-all']"),
    ("np04-extra-policy-all-tcp-ports", add_platform_resource("extra.yaml", extra_policy("engine-all-tcp", "  podSelector:\n    matchLabels:\n      app.kubernetes.io/name: gb-engine\n  policyTypes: [Ingress]\n  ingress:\n    - from:\n        - namespaceSelector: {}\n      ports:\n        - protocol: TCP\n")), "opens every TCP port"),
    ("np05-extra-policy-unrestricted-8081", add_platform_resource("extra.yaml", extra_policy("engine-admin-open", "  podSelector:\n    matchLabels:\n      app.kubernetes.io/name: gb-engine\n      app.kubernetes.io/component: engine\n  policyTypes: [Ingress]\n  ingress:\n    - ports:\n        - protocol: TCP\n          port: 8081\n")), "ingress rule without 'from' admits every source"),
    ("np06-admin-rule-omits-from", replace(MON, "    - from:\n" + PROM_PEER_YAML + "      ports:", "    - ports:"), "ingress rule without 'from' admits every source"),
    ("np07-admin-rule-unrestricted-source", replace(MON, PROM_PEER_YAML, "        - namespaceSelector: {}\n"), "peer must combine namespaceSelector and podSelector"),
    ("np08-wrong-prometheus-pod-selector", replace(MON, "              app.kubernetes.io/name: prometheus\n              prometheus: rancher-monitoring-prometheus\n", "              app: untrusted\n"), "does not match any approved peer"),
    ("np09-wrong-monitoring-namespace", replace(MON, "kubernetes.io/metadata.name: cattle-monitoring-system", "kubernetes.io/metadata.name: default"), "does not match any approved peer"),
    ("np10-split-peers-or-alternatives", replace(MON, PROM_PEER_YAML, "        - namespaceSelector:\n            matchLabels:\n              kubernetes.io/metadata.name: cattle-monitoring-system\n        - podSelector:\n            matchLabels:\n              app.kubernetes.io/name: prometheus\n              prometheus: rancher-monitoring-prometheus\n"), "peers are OR alternatives"),
    ("np11-admin-policy-selects-every-pod", replace(MON, "  podSelector:\n    matchLabels:\n      app.kubernetes.io/name: gb-engine\n      app.kubernetes.io/component: engine\n", "  podSelector: {}\n"), "podSelector must be exactly the engine labels"),
    ("np12-default-deny-ingress-only", replace(DENY, "    - Ingress\n    - Egress\n", "    - Ingress\n"), "must list policyTypes Ingress and Egress"),
    ("np12b-traefik-policy-wrong-direction", replace(TRAEFIK, "  policyTypes:\n    - Ingress\n", "  policyTypes:\n    - Egress\n"), "does not include Ingress"),
    ("np13a-player-allow-removed", replace(PLAT, "  - networkpolicy-allow-player-from-traefik.yaml\n", ""), "missing ['gb-engine-allow-player-from-traefik']"),
    ("np13b-dns-allow-removed", replace(PLAT, "  - networkpolicy-allow-dns.yaml\n", ""), "missing ['allow-dns-egress']"),
    ("np13c-monitoring-allow-removed", replace(PLAT, "  - networkpolicy-allow-metrics-from-monitoring.yaml\n", ""), "missing ['gb-engine-allow-metrics-from-monitoring']"),
    ("np14-admin-rule-omits-ports", replace(MON, "      ports:\n        - protocol: TCP\n          port: admin\n", ""), "ingress rule without 'ports' opens every port"),
    ("np15-traefik-rule-opens-admin-too", replace(TRAEFIK, "          port: player\n", "          port: player\n        - protocol: TCP\n          port: admin\n"), "additional paths [('traefik', 'admin')]"),
    ("np16-dns-egress-to-any-namespace", replace(DNS, "        - namespaceSelector:\n            matchLabels:\n              kubernetes.io/metadata.name: kube-system\n          podSelector:\n            matchLabels:\n              k8s-app: kube-dns\n", "        - podSelector:\n            matchLabels:\n              k8s-app: kube-dns\n"), "peer must combine namespaceSelector and podSelector"),
    ("np17-policy-rendered-by-server-app", many(lambda t: (t / "base/server/np.yaml").write_text(extra_policy("server-side-allow", "  podSelector: {}\n  policyTypes: [Ingress]\n  ingress: [{}]\n")),
                                               replace(SRVK, "  - ingress.yaml\n", "  - ingress.yaml\n  - np.yaml\n")), "unexpected NetworkPolicy ['server-side-allow']"),
]


def run_validator(tree: Path, extra_args: list[str]) -> tuple[int, str]:
    proc = subprocess.run([sys.executable, str(tree / "tests/validate.py"), *extra_args], capture_output=True, text=True)
    return proc.returncode, proc.stdout + proc.stderr


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("-k", default="", help="run only cases whose name contains this substring")
    ap.add_argument("-v", action="store_true", help="print the validator output of failed expectations")
    args = ap.parse_args()

    failures = 0
    with tempfile.TemporaryDirectory(prefix="gb-kustomize-mutations-") as tmp:
        base = Path(tmp) / "pristine"
        shutil.copytree(ROOT, base)

        code, out = run_validator(base, [])
        ok = code == 0 and "Traceback" not in out and out.count("WARN ") == 1 and "UNPUBLISHED" in out
        print(f"{'ok  ' if ok else 'FAIL'}  baseline: unmodified tree passes with only the UNPUBLISHED warning (exit {code})")
        failures += 0 if ok else 1

        code, out = run_validator(base, ["--require-published-image"])
        ok = code == 1 and "Traceback" not in out and "UNPUBLISHED sentinel" in out
        print(f"{'ok  ' if ok else 'FAIL'}  strict: --require-published-image rejects the sentinel digest (exit {code})")
        failures += 0 if ok else 1

        for name, mutate, reason in CASES:
            if args.k and args.k not in name:
                continue
            tree = Path(tmp) / name
            shutil.copytree(ROOT, tree)
            try:
                mutate(tree)
            except Exception as exc:  # noqa: BLE001
                print(f"FAIL  {name}: could not apply the mutation: {exc}")
                failures += 1
                continue
            code, out = run_validator(tree, [])
            traceback = "Traceback" in out or "internal error" in out
            caught = code == 1 and reason in out and not traceback
            nfail = len(re.findall(r"^FAIL", out, re.M))
            if caught:
                print(f"ok    {name}: rejected for the intended reason ({nfail} failure line(s)): {reason!r}")
            else:
                failures += 1
                why = "exit 0" if code == 0 else ("traceback/internal error" if traceback else f"exit {code}, reason {reason!r} not reported")
                print(f"FAIL  {name}: {why}")
                if args.v or True:
                    for line in out.splitlines():
                        if line.startswith(("FAIL", "Traceback", "  failure")) or "Error" in line:
                            print(f"        {line[:200]}")
    print()
    print(f"mutations.py: {len(CASES)} mutation cases + 2 baseline checks, {failures} unexpected outcome(s)")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
