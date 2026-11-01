#!/usr/bin/env python3
"""Mutation suite for validate.py (Phase 2B, GB2B-01 regression coverage).

Each case copies deploy/kustomize to a temporary directory, applies one
mutation, runs the copied validate.py and requires: exit status 1, no Python
traceback, and the specific security or structure reason in the output. The
unmodified tree must pass (exit 0) in whichever of the two legitimate image
states apps/dev/server is in, and both states are always exercised, one on
the real tree and the other on a disposable copy whose only change is the
server image digest (GB2C digest-update run 41269189437, 2026-11-01):

  * unpublished: the digest is the documented all-zero UNPUBLISHED sentinel;
    normal validation passes with that one warning and
    --require-published-image rejects the tree for that reason alone;
  * published: the digest is sha256: plus 64 lower-case hex digits other than
    the sentinel; normal validation passes with no warning and
    --require-published-image passes.

Anything else in the digest field (a tag, a rename, a malformed or
upper-case digest, a missing or duplicate entry) is a baseline failure, not a
third state, and a copy carrying a malformed digest must be rejected by the
validator in both modes. The real tree's digest is never rewritten in place.

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

IMAGE = "ghcr.io/phreddrick/gb-server"
SENTINEL = "sha256:" + "0" * 64
DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
# A well-formed digest that is not the sentinel, used only on a disposable copy
# while the repository still carries the sentinel, so that the strict mode's
# acceptance path is tested before the first publication as well as after it.
FIXTURE_DIGEST = "sha256:" + "0123456789abcdef" * 4
# Well-formed to the eye but not a digest the validator may accept (upper-case
# hex): used on a disposable copy to prove the format check is load-bearing.
MALFORMED_DIGEST = "sha256:" + "0123456789ABCDEF" * 4

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


def dev_image_digest(tree: Path) -> str:
    """The digest apps/dev/server pins for IMAGE, or a RuntimeError naming why
    the entry point is not in a recognised shape (exactly one `images` entry
    for IMAGE, carrying `name` and `digest` and nothing else)."""
    try:
        import yaml
    except ImportError as exc:  # pragma: no cover
        raise RuntimeError("PyYAML is required to read the dev entry point") from exc
    doc = yaml.safe_load((tree / DEVSRV).read_text())
    images = doc.get("images") if isinstance(doc, dict) else None
    entries = [i for i in (images or []) if isinstance(i, dict) and i.get("name") == IMAGE]
    if len(entries) != 1:
        raise RuntimeError(f"{DEVSRV} has {len(entries)} images entries for {IMAGE}, expected exactly one")
    if set(entries[0]) != {"name", "digest"} or not isinstance(entries[0]["digest"], str):
        raise RuntimeError(f"{DEVSRV} images entry for {IMAGE} must carry name and a string digest only (got {sorted(entries[0])})")
    return entries[0]["digest"]


def set_dev_digest(old: str, new: str):
    """Rewrite the one `digest: <old>` line of apps/dev/server to `new` in a copy."""
    def apply(tree: Path) -> None:
        p = tree / DEVSRV
        s = p.read_text()
        if s.count(f"digest: {old}\n") != 1:
            raise RuntimeError(f"{DEVSRV} does not contain exactly one line 'digest: {old}'")
        p.write_text(s.replace(f"digest: {old}\n", f"digest: {new}\n", 1))
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


def image_baseline(tree: Path, digest: str) -> int:
    """Run normal and strict validation on a tree whose dev image digest is
    `digest`; print one line per mode and return the number of failed
    expectations. The sentinel must warn once and be refused by strict mode
    for that reason only; a well-formed digest must pass both modes with no
    warning and with the validator's own digest check reported; anything
    else must be refused by both modes by that digest check alone."""
    failures = 0
    kind = "sentinel" if digest == SENTINEL else "published" if DIGEST_RE.match(digest) else "malformed"
    pinned = f"[dev] image is {IMAGE} pinned by digest (got '{IMAGE}@{digest}')"

    for mode, args in (("normal validation", []), ("--require-published-image", ["--require-published-image"])):
        code, out = run_validator(tree, args)
        clean = "Traceback" not in out and "internal error" not in out
        fails = re.findall(r"^FAIL .*$", out, re.M)
        if kind == "sentinel" and not args:
            ok = code == 0 and clean and out.count("WARN ") == 1 and "UNPUBLISHED sentinel" in out
            what = "passes with the UNPUBLISHED warning as its only warning"
        elif kind == "sentinel":
            ok = code == 1 and clean and len(fails) == 1 and "UNPUBLISHED sentinel" in fails[0]
            what = "rejects the sentinel, for that reason only"
        elif kind == "published":
            ok = code == 0 and clean and not fails and out.count("WARN ") == 0 and "UNPUBLISHED" not in out and f"PASS  {pinned}" in out
            what = f"accepts the pinned digest {digest[:19]}... with no warning"
        else:
            ok = code == 1 and clean and len(fails) == 1 and fails[0] == f"FAIL  {pinned}" and "UNPUBLISHED" not in out
            what = f"rejects the malformed digest {digest[:19]}... by the digest check alone"
        print(f"{'ok  ' if ok else 'FAIL'}  baseline[{kind}]: {mode} {what} (exit {code})")
        failures += 0 if ok else 1
    return failures


def malformed_baseline(tmp: Path, current: str) -> int:
    """Both modes must refuse a copy whose only change is a malformed digest."""
    tree = tmp / "baseline-malformed"
    shutil.copytree(ROOT, tree)
    set_dev_digest(current, MALFORMED_DIGEST)(tree)
    return image_baseline(tree, MALFORMED_DIGEST)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("-k", default="", help="run only cases whose name contains this substring")
    ap.add_argument("-v", action="store_true", help="print the validator output of failed expectations")
    args = ap.parse_args()

    failures = 0
    with tempfile.TemporaryDirectory(prefix="gb-kustomize-mutations-") as tmp:
        base = Path(tmp) / "pristine"
        shutil.copytree(ROOT, base)

        # Baseline: which legitimate image state is the real tree in? The
        # other state, and a malformed digest, are exercised on copies that
        # differ from the tree in the digest only.
        baseline_checks = 6
        try:
            digest = dev_image_digest(base)
        except Exception as exc:  # noqa: BLE001
            digest = None
            print(f"FAIL  baseline: cannot read the dev image digest: {exc}")
        if digest == SENTINEL:
            state = "unpublished (UNPUBLISHED sentinel)"
            print(f"      {DEVSRV} pins the UNPUBLISHED sentinel; a published digest is tested on a copy")
            failures += image_baseline(base, SENTINEL)
            other = Path(tmp) / "baseline-published-fixture"
            shutil.copytree(ROOT, other)
            set_dev_digest(SENTINEL, FIXTURE_DIGEST)(other)
            failures += image_baseline(other, FIXTURE_DIGEST)
            failures += malformed_baseline(Path(tmp), SENTINEL)
        elif digest is not None and DIGEST_RE.match(digest):
            state = "published digest"
            print(f"      {DEVSRV} pins {digest}; the UNPUBLISHED sentinel is tested on a copy")
            failures += image_baseline(base, digest)
            other = Path(tmp) / "baseline-sentinel"
            shutil.copytree(ROOT, other)
            set_dev_digest(digest, SENTINEL)(other)
            failures += image_baseline(other, SENTINEL)
            failures += malformed_baseline(Path(tmp), digest)
        else:
            state = "INVALID"
            baseline_checks = 1
            if digest is not None:
                print(f"FAIL  baseline: {DEVSRV} pins {digest!r}, which is neither the UNPUBLISHED sentinel nor sha256: plus 64 lower-case hex digits")
            failures += 1

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
    print(f"mutations.py: {len(CASES)} mutation cases + {baseline_checks} baseline checks (dev image state: {state}), {failures} unexpected outcome(s)")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
