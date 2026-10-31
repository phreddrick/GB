#!/usr/bin/env python3
"""Unit tests for the Phase 2C workflow helpers, with synthetic data only (no
registry, no runner, no cluster):

    python3 -m unittest discover -s .github/scripts -p 'test_*.py' -v

Covers oci_digest (a synthetic OCI layout archive, corrupted blobs, an
attestation index), set_image_digest (the real dev entry point text, invalid
and sentinel digests, tags, duplicate entries, no-op), trivy_policy (fixable
and unfixable CRITICAL findings, secrets, identity assertion) and
workflow_policy_check (the repository's own workflows must pass; synthetic
violations of each rule must fail). The release-safety regressions for the
independent review findings GB2C-01 to GB2C-05 (publish record, CI
provenance, malformed scan reports, kind cleanup, token-created pull
requests) are in test_release_safety.py.
"""
from __future__ import annotations

import hashlib
import io
import json
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import textwrap
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", ".."))
sys.path.insert(0, HERE)

import oci_digest  # noqa: E402
import set_image_digest  # noqa: E402
import trivy_policy  # noqa: E402
import workflow_policy_check  # noqa: E402

DEV_ENTRY = os.path.join(REPO, "deploy", "kustomize", "apps", "dev", "server", "kustomization.yaml")
IMAGE = "ghcr.io/phreddrick/gb-server"
GOOD = "sha256:" + "ab" * 32
GOOD2 = "sha256:" + "cd" * 32


# ----------------------------------------------------------------- helpers --
def _digest(b: bytes) -> str:
    return "sha256:" + hashlib.sha256(b).hexdigest()


def make_oci_archive(path: str, *, manifests: int = 1, index_entry: bool = False, corrupt_layer: bool = False, labels=None) -> dict:
    """Write a minimal but well-formed OCI layout tarball."""
    layer = b"layer-bytes-" + os.urandom(8)
    config = json.dumps({
        "architecture": "amd64", "os": "linux",
        "config": {"User": "65532:65532", "Entrypoint": ["/usr/local/bin/gb-server"], "Labels": labels or {"a": "b"}},
        "rootfs": {"type": "layers", "diff_ids": [_digest(layer)]},
    }).encode()
    manifest = json.dumps({
        "schemaVersion": 2, "mediaType": oci_digest.IMAGE_MANIFEST,
        "config": {"mediaType": "application/vnd.oci.image.config.v1+json", "digest": _digest(config), "size": len(config)},
        "layers": [{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": _digest(layer), "size": len(layer)}],
    }).encode()
    entry = {"mediaType": oci_digest.IMAGE_MANIFEST, "digest": _digest(manifest), "size": len(manifest),
             "annotations": {"io.containerd.image.name": "docker.io/library/gb-server:ci"}}
    if index_entry:
        entry["mediaType"] = "application/vnd.oci.image.index.v1+json"
    index = json.dumps({"schemaVersion": 2, "manifests": [entry] * manifests}).encode()
    blobs = {_digest(config): config, _digest(manifest): manifest, _digest(layer): (b"X" + layer[1:] if corrupt_layer else layer)}
    with tarfile.open(path, "w") as tar:
        def add(name: str, data: bytes) -> None:
            ti = tarfile.TarInfo(name)
            ti.size = len(data)
            tar.addfile(ti, io.BytesIO(data))
        add("oci-layout", b'{"imageLayoutVersion":"1.0.0"}')
        add("index.json", index)
        for d, data in blobs.items():
            add(f"blobs/sha256/{d[7:]}", data)
    return {"manifest_digest": _digest(manifest), "config_digest": _digest(config)}


# --------------------------------------------------------------- oci_digest --
class OciDigestTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.tmp)

    def test_reads_single_manifest(self):
        p = os.path.join(self.tmp, "a.tar")
        want = make_oci_archive(p)
        rec = oci_digest.inspect(p)
        self.assertEqual(rec["manifest_digest"], want["manifest_digest"])
        self.assertEqual(rec["config_digest"], want["config_digest"])
        self.assertEqual(rec["platform"], {"os": "linux", "architecture": "amd64"})
        self.assertEqual(rec["user"], "65532:65532")
        self.assertEqual(rec["layer_count"], 1)
        self.assertEqual(rec["annotations"]["io.containerd.image.name"], "docker.io/library/gb-server:ci")

    def test_rejects_two_manifests(self):
        p = os.path.join(self.tmp, "a.tar")
        make_oci_archive(p, manifests=2)
        with self.assertRaises(oci_digest.ArchiveError):
            oci_digest.inspect(p)

    def test_rejects_attestation_index(self):
        p = os.path.join(self.tmp, "a.tar")
        make_oci_archive(p, index_entry=True)
        with self.assertRaises(oci_digest.ArchiveError):
            oci_digest.inspect(p)

    def test_rejects_corrupt_blob(self):
        p = os.path.join(self.tmp, "a.tar")
        make_oci_archive(p, corrupt_layer=True)
        with self.assertRaises(oci_digest.ArchiveError):
            oci_digest.inspect(p)

    def test_cli_field_and_missing_file(self):
        p = os.path.join(self.tmp, "a.tar")
        want = make_oci_archive(p)
        out = subprocess.run([sys.executable, os.path.join(HERE, "oci_digest.py"), p, "--field", "manifest_digest"], capture_output=True, text=True)
        self.assertEqual(out.returncode, 0, out.stderr)
        self.assertEqual(out.stdout.strip(), want["manifest_digest"])
        out = subprocess.run([sys.executable, os.path.join(HERE, "oci_digest.py"), os.path.join(self.tmp, "none.tar")], capture_output=True, text=True)
        self.assertEqual(out.returncode, 1)


# --------------------------------------------------------- set_image_digest --
class SetImageDigestTests(unittest.TestCase):
    def setUp(self):
        with open(DEV_ENTRY, encoding="utf-8") as f:
            self.text = f.read()

    def test_replaces_only_the_digest_line(self):
        new, old, dig = set_image_digest.set_digest(self.text, IMAGE, GOOD)
        self.assertEqual(old, set_image_digest.SENTINEL)
        self.assertEqual(dig, GOOD)
        a, b = self.text.splitlines(), new.splitlines()
        self.assertEqual(len(a), len(b))
        changed = [i for i in range(len(a)) if a[i] != b[i]]
        self.assertEqual(len(changed), 1)
        self.assertIn("digest:", a[changed[0]])
        self.assertEqual(b[changed[0]], a[changed[0]].replace(set_image_digest.SENTINEL, GOOD))
        # Idempotent and detectable as a no-op.
        new2, old2, _ = set_image_digest.set_digest(new, IMAGE, GOOD)
        self.assertEqual(new2, new)
        self.assertEqual(old2, GOOD)
        # And replaceable again by a later digest.
        new3, old3, _ = set_image_digest.set_digest(new, IMAGE, GOOD2)
        self.assertEqual(old3, GOOD)
        self.assertIn(GOOD2, new3)

    def test_invalid_digests(self):
        for bad in ["sha256:" + "0" * 64, "sha256:" + "ab" * 31, "sha256:" + "AB" * 32, "ab" * 32, "sha512:" + "ab" * 32,
                    "sha256:" + "ab" * 32 + "\n", f"{IMAGE}@sha256:" + "ab" * 32, "", "latest"]:
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                set_image_digest.set_digest(self.text, IMAGE, bad)

    def test_wrong_image_name(self):
        with self.assertRaises(set_image_digest.ShapeError):
            set_image_digest.set_digest(self.text, "ghcr.io/phreddrick/gbctl", GOOD)

    def test_tag_entry_refused(self):
        tagged = self.text.replace("    digest: " + set_image_digest.SENTINEL, "    newTag: v1\n    digest: " + set_image_digest.SENTINEL)
        with self.assertRaises(set_image_digest.ShapeError):
            set_image_digest.set_digest(tagged, IMAGE, GOOD)

    def test_duplicate_entry_refused(self):
        dup = self.text.replace("images:\n", "images:\n  - name: ghcr.io/phreddrick/gb-server\n    digest: " + GOOD2 + "\n", 1)
        with self.assertRaises(set_image_digest.ShapeError):
            set_image_digest.set_digest(dup, IMAGE, GOOD)

    def test_cli_exit_codes(self):
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp)
        p = os.path.join(tmp, "kustomization.yaml")
        shutil.copy(DEV_ENTRY, p)
        cli = [sys.executable, os.path.join(HERE, "set_image_digest.py"), "--file", p, "--image", IMAGE]
        self.assertEqual(subprocess.run(cli + ["--check"], capture_output=True, text=True).stdout.strip(), set_image_digest.SENTINEL)
        self.assertEqual(subprocess.run(cli + ["--digest", "sha256:dead"], capture_output=True).returncode, 1)
        self.assertEqual(subprocess.run(cli + ["--digest", GOOD], capture_output=True).returncode, 0)
        self.assertEqual(subprocess.run(cli + ["--digest", GOOD], capture_output=True).returncode, 3)
        with open(p, encoding="utf-8") as f:
            self.assertIn(GOOD, f.read())
        self.assertEqual(subprocess.run(cli[:-2] + ["--image", "ghcr.io/other/x", "--digest", GOOD2], capture_output=True).returncode, 2)


# ------------------------------------------------------------- trivy_policy --
IMAGE_ID = "sha256:" + "ef" * 32


def trivy_report(vulns=(), secrets=()):
    """A report shaped like Trivy v0.76.1 output for an OCI archive (see test_release_safety.py for the negative shapes)."""
    result = {"Target": "gb-server.oci.tar (debian 12.12)", "Class": "os-pkgs", "Type": "debian"}
    if vulns:
        result["Vulnerabilities"] = list(vulns)
    if secrets:
        result["Secrets"] = list(secrets)
    return {"SchemaVersion": 2, "CreatedAt": "2026-10-30T00:00:00Z", "ArtifactName": "gb-server.oci.tar",
            "ArtifactType": "container_image", "Metadata": {"ImageID": IMAGE_ID, "OS": {"Family": "debian", "Name": "12.12"}},
            "Results": [result]}


SECRET = {"RuleID": "aws-access-key-id", "Category": "AWS", "Title": "AWS Access Key ID", "Severity": "CRITICAL"}


class TrivyPolicyTests(unittest.TestCase):
    def test_critical_fixable_fails(self):
        counts, viol, sec, total = trivy_policy.evaluate(trivy_report([{"VulnerabilityID": "CVE-1", "Severity": "CRITICAL", "FixedVersion": "1.2", "PkgName": "p", "InstalledVersion": "1.0"}]))
        self.assertEqual(counts["CRITICAL"]["fixable"], 1)
        self.assertEqual(total, 1)
        self.assertEqual(len(viol), 1)
        self.assertEqual(sec, [])

    def test_critical_unfixable_and_high_fixable_pass(self):
        counts, viol, _, _ = trivy_policy.evaluate(trivy_report([
            {"VulnerabilityID": "CVE-2", "Severity": "CRITICAL", "FixedVersion": "", "PkgName": "p", "InstalledVersion": "1.0"},
            {"VulnerabilityID": "CVE-3", "Severity": "HIGH", "FixedVersion": "2", "PkgName": "q", "InstalledVersion": "1"},
        ]))
        self.assertEqual(counts["CRITICAL"]["unfixable"], 1)
        self.assertEqual(counts["HIGH"]["fixable"], 1)
        self.assertEqual(viol, [])

    def test_secret_fails(self):
        _, viol, sec, _ = trivy_policy.evaluate(trivy_report(secrets=[SECRET]))
        self.assertEqual(viol, [])
        self.assertEqual(len(sec), 1)

    def test_cli(self):
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp)
        cli = [sys.executable, os.path.join(HERE, "trivy_policy.py")]
        good, bad, junk, empty = (os.path.join(tmp, n) for n in ("good.json", "bad.json", "junk.json", "empty.json"))
        with open(good, "w") as f:
            json.dump(trivy_report(), f)
        with open(bad, "w") as f:
            json.dump(trivy_report([{"VulnerabilityID": "CVE-1", "Severity": "CRITICAL", "FixedVersion": "1.2", "PkgName": "p", "InstalledVersion": "1.0"}]), f)
        with open(junk, "w") as f:
            f.write("{not json")
        open(empty, "w").close()
        summary = os.path.join(tmp, "summary.md")
        self.assertEqual(subprocess.run(cli + [good, "--summary", summary], capture_output=True).returncode, 0)
        self.assertTrue(os.path.getsize(summary) > 0)
        # Identity asserted: the expected archive name and config digest must match the report.
        self.assertEqual(subprocess.run(cli + [good, "--require-identity", "--expect", good, "gb-server.oci.tar", IMAGE_ID], capture_output=True).returncode, 0)
        self.assertEqual(subprocess.run(cli + [good, "--require-identity"], capture_output=True).returncode, 2)
        self.assertEqual(subprocess.run(cli + [good, "--expect", good, "gb-server.oci.tar", "sha256:" + "00" * 32], capture_output=True).returncode, 2)
        self.assertEqual(subprocess.run(cli + [good, bad], capture_output=True).returncode, 1)
        self.assertEqual(subprocess.run(cli + [junk], capture_output=True).returncode, 2)
        self.assertEqual(subprocess.run(cli + [empty], capture_output=True).returncode, 2)
        self.assertEqual(subprocess.run(cli + [os.path.join(tmp, "missing.json")], capture_output=True).returncode, 2)


# ---------------------------------------------------- workflow_policy_check --
CHECKOUT = "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1"
MINIMAL = textwrap.dedent(f"""\
    name: t
    on:
      pull_request:
    permissions:
      contents: read
    jobs:
      a:
        name: build, test, lint (fresh checkout)
        runs-on: ubuntu-26.04
        timeout-minutes: 5
        permissions:
          contents: read
        steps:
          - uses: {CHECKOUT}
            with:
              persist-credentials: false
          - run: echo "$X"
            env:
              X: ${{{{ github.event.pull_request.title }}}}
      b:
        name: docker build from checkout (no push)
        runs-on: ubuntu-26.04
        timeout-minutes: 5
        permissions:
          contents: read
        steps:
          - run: true
    """)
PUBLISH_OK = textwrap.dedent(f"""\
    name: p
    on:
      workflow_dispatch:
    permissions:
      contents: read
    concurrency:
      group: ghcr-publish
      cancel-in-progress: false
    jobs:
      preflight:
        runs-on: ubuntu-26.04
        timeout-minutes: 5
        permissions:
          contents: read
          actions: read
        steps:
          - run: true
      publish:
        if: github.ref == 'refs/heads/master'
        environment: ghcr-publish
        runs-on: ubuntu-26.04
        timeout-minutes: 5
        permissions:
          contents: read
          packages: write
        steps:
          - uses: {CHECKOUT}
            with:
              persist-credentials: false
    """)


class WorkflowPolicyTests(unittest.TestCase):
    def _run(self, files: dict[str, str]) -> int:
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp)
        d = os.path.join(tmp, ".github", "workflows")
        os.makedirs(d)
        for n, t in files.items():
            with open(os.path.join(d, n), "w") as f:
                f.write(t)
        with open(os.devnull, "w") as devnull:
            old = sys.stdout
            sys.stdout = devnull
            try:
                return workflow_policy_check.run(d)
            finally:
                sys.stdout = old

    def test_repository_workflows_pass(self):
        self.assertEqual(workflow_policy_check.run(os.path.join(REPO, ".github", "workflows")), 0)

    def test_minimal_pass(self):
        self.assertEqual(self._run({"ci.yml": MINIMAL, "publish-images.yml": PUBLISH_OK}), 0)

    def test_violations(self):
        cases = {
            "W1 pull_request_target": MINIMAL.replace("pull_request:", "pull_request_target:"),
            "W2 missing job permissions": MINIMAL.replace("    permissions:\n      contents: read\n    steps:\n      - run: true", "    steps:\n      - run: true"),
            "W2 write in ci": MINIMAL.replace("      contents: read\n    steps:\n      - run: true", "      contents: write\n    steps:\n      - run: true"),
            "W3 unpinned": MINIMAL.replace(CHECKOUT, "actions/checkout@v7"),
            "W3 no version comment": MINIMAL.replace(" # v7.0.1", ""),
            "W4 no timeout": MINIMAL.replace("    timeout-minutes: 5\n    permissions:\n      contents: read\n    steps:\n      - run: true", "    permissions:\n      contents: read\n    steps:\n      - run: true"),
            "W4 wrong runner": MINIMAL.replace("ubuntu-26.04", "ubuntu-latest", 1),
            "W5 injection": MINIMAL.replace('- run: echo "$X"', '- run: echo "${{ github.event.pull_request.title }}"'),
            "W5 inputs in run": MINIMAL.replace('- run: echo "$X"', '- run: echo "${{ inputs.digest }}"'),
            "W6 PAT": MINIMAL.replace("X: ${{ github.event.pull_request.title }}", "X: ${{ secrets.GHCR_PAT }}"),
            "W7 persist credentials": MINIMAL.replace("persist-credentials: false", "persist-credentials: true"),
            "W8 required name": MINIMAL.replace("name: docker build from checkout (no push)", "name: docker build"),
        }
        for label, text in cases.items():
            with self.subTest(label):
                self.assertNotEqual(text, MINIMAL, "replacement did not apply")
                self.assertEqual(self._run({"ci.yml": text, "publish-images.yml": PUBLISH_OK}), 1)
        pub_cases = {
            "publish on push": PUBLISH_OK.replace("  workflow_dispatch:", "  push:\n    branches: [master]"),
            "publish without environment": PUBLISH_OK.replace("    environment: ghcr-publish\n", ""),
            "publish without master guard": PUBLISH_OK.replace("    if: github.ref == 'refs/heads/master'\n", ""),
            "publish without concurrency": PUBLISH_OK.replace("concurrency:\n  group: ghcr-publish\n  cancel-in-progress: false\n", ""),
            "second packages write": PUBLISH_OK.replace("      actions: read", "      packages: write"),
            "preflight with checks instead of actions": PUBLISH_OK.replace("      actions: read", "      checks: read"),
        }
        for label, text in pub_cases.items():
            with self.subTest(label):
                self.assertNotEqual(text, PUBLISH_OK, "replacement did not apply")
                self.assertEqual(self._run({"ci.yml": MINIMAL, "publish-images.yml": text}), 1)
        # No publishing job at all is also a failure: the policy expects exactly one.
        self.assertEqual(self._run({"ci.yml": MINIMAL}), 1)


if __name__ == "__main__":
    unittest.main()
