#!/usr/bin/env python3
"""Keep active build/scanner pins aligned with the reviewed security toolchain."""
import json
from pathlib import Path
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[2]
VERSION = "1.27.4"


class ToolchainPins(unittest.TestCase):
    def test_module_language_minimum_and_toolchain(self):
        text = (ROOT / "go/go.mod").read_text()
        self.assertRegex(text, r"(?m)^go 1\.25\.0$")
        self.assertIn(f"\ntoolchain go{VERSION}\n", text)

    def test_all_workflow_pins_and_local_build_policy(self):
        for name in ("ci.yml", "publish-images.yml", "digest-update-pr.yml"):
            with self.subTest(workflow=name):
                doc = yaml.safe_load((ROOT / ".github/workflows" / name).read_text())
                self.assertEqual(doc["env"]["GO_VERSION"], VERSION)
                self.assertEqual(doc["env"]["GOTOOLCHAIN"], "local")
                setup = [s for j in doc["jobs"].values() for s in j.get("steps", [])
                         if s.get("uses", "").startswith("actions/setup-go@")]
                self.assertTrue(setup)
                for step in setup:
                    self.assertEqual(step["with"]["go-version"], "${{ env.GO_VERSION }}")
                    self.assertIs(step["with"]["check-latest"], False)

    def test_docker_tags_digests_and_version_assertions(self):
        evidence = json.loads((ROOT / "docs/testing/evidence/go-1.27.4-pins.json").read_text())
        self.assertEqual([r["image"] for r in evidence["images"]],
                         [f"golang:{VERSION}-bookworm", f"golang:{VERSION}-alpine"])
        for name, record in zip(("deploy/docker/Dockerfile", "Dockerfile"), evidence["images"]):
            with self.subTest(dockerfile=name):
                self.assertEqual(record["golang_version"], VERSION)
                text = (ROOT / name).read_text()
                self.assertRegex(record["index_digest"], r"^sha256:[0-9a-f]{64}$")
                self.assertIn(f'FROM {record["image"]}@{record["index_digest"]} AS builder', text)
                self.assertIn(f"grep -q 'go{VERSION} '", text)
                self.assertIn("GOTOOLCHAIN=local", text)
                self.assertNotIn("1.27.3", text)

    def test_ci_keeps_seven_jobs_and_strict_scanner(self):
        doc = yaml.safe_load((ROOT / ".github/workflows/ci.yml").read_text())
        self.assertEqual(set(doc["jobs"]), {"go", "docker", "manifests", "workflow-policy",
                                          "image-scan", "image-sbom", "kind-e2e"})
        steps = doc["jobs"]["go"]["steps"]
        scan = next(s for s in steps if s.get("name", "").startswith("govulncheck"))
        self.assertEqual(scan["run"].strip(),
                         "go version\ngo run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...")
        self.assertNotIn("continue-on-error", scan)
        assertion = next(s for s in steps if s.get("name") == "Toolchain is the pinned one")
        self.assertIn('grep -q "go${GO_VERSION} "', assertion["run"])
        docker = doc["jobs"]["docker"]
        self.assertIn("EXPECTED_GO_VERSION", str(docker))
        self.assertIn("image-checks.sh", str(docker))


if __name__ == "__main__":
    unittest.main()
