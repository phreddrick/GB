#!/usr/bin/env python3
"""Release-safety regressions for the independent review findings GB2C-01 to
GB2C-05 (docs/testing/phase2c-handoff.md section 19). They drive the real
production scripts with controlled mocks on PATH (crane, gh, git, kind,
docker, mv) and never reach a registry, GitHub or a cluster:

    python3 -m unittest discover -s .github/scripts -p 'test_*.py' -v

Every negative case asserts that the unsafe condition was named, that the
script exited non-zero, that no false success line was printed, and that no
downstream action (second push, master tag, digest outputs, deletion of a
cluster not named gb-ci*) happened. The mocks log every invocation so the
absence of an action is asserted, not assumed.
"""
from __future__ import annotations

import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import textwrap
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", ".."))
sys.path.insert(0, HERE)

import ci_provenance  # noqa: E402
import oci_digest  # noqa: E402
import publish_record  # noqa: E402
from test_scripts import IMAGE_ID, SECRET, make_oci_archive, trivy_report  # noqa: E402

SHA = "8f23bb093fcbc25d2d67aba4a7d10fed6f585bb4"
OTHER_SHA = "1" * 40
NS = "ghcr.io/phreddrick"
SYSTEM_PATH = "/usr/bin:/bin"

CRANE_MOCK = r'''#!/usr/bin/env python3
"""crane stand-in: answers from the local OCI archives; modes via MOCK_MODE."""
import json, os, pathlib, sys, tarfile
cmd = sys.argv[1]; ref = sys.argv[-1]
with open(os.environ["MOCK_LOG"], "a") as log:
    log.write(" ".join(sys.argv[1:]) + "\n")
target = "gbctl" if "/gbctl" in ref else "gb-server"
mode = os.environ.get("MOCK_MODE", "success")
if cmd == "push":
    if mode == "second-push-fails" and target == "gbctl": print("mock: push refused", file=sys.stderr); sys.exit(1)
    if mode == "first-push-fails" and target == "gb-server": print("mock: push refused", file=sys.stderr); sys.exit(1)
    sys.exit(0)
if cmd == "tag":
    if mode == "tag-fails": sys.exit(1)
    # Hooks that change the record destination between the last registry
    # write and the record finalisation (GB2C-01R): a directory or a symlink
    # appears at the record path, or its directory becomes unwritable.
    d = os.environ.get("MOCK_ON_TAG_MKDIR")
    if d: os.makedirs(d, exist_ok=True)
    l = os.environ.get("MOCK_ON_TAG_SYMLINK")  # "<link>:<target>"
    if l and not os.path.lexists(l.split(":", 1)[0]): os.symlink(l.split(":", 1)[1], l.split(":", 1)[0])
    c = os.environ.get("MOCK_ON_TAG_CHMOD_RO")
    if c: os.chmod(c, 0o555)
    sys.exit(0)
p = pathlib.Path(os.environ["MOCK_IMAGES"]) / (target + ".oci.tar")
with tarfile.open(p) as t:
    index = json.load(t.extractfile("index.json")); digest = index["manifests"][0]["digest"]
    raw = t.extractfile("blobs/sha256/" + digest[7:]).read(); manifest = json.loads(raw)
    config = t.extractfile("blobs/sha256/" + manifest["config"]["digest"][7:]).read()
if cmd == "digest":
    if mode == "mismatch" and target == "gb-server": print("sha256:" + "f" * 64)
    else: print(digest)
elif cmd == "manifest": sys.stdout.write(raw.decode())
elif cmd == "config": sys.stdout.write(config.decode())
else: sys.exit(92)
'''

GIT_MOCK = '#!/bin/sh\ncase "$1" in fetch|merge-base) exit 0;; rev-parse) printf "%s\\n" "$GITHUB_SHA";; *) exit 91;; esac\n'

GH_MOCK = r'''#!/bin/sh
# gh stand-in: serves fixture files by URL; logs every call.
echo "$*" >> "$MOCK_LOG"
for a in "$@"; do last="$a"; done
case "$last" in
  *actions/workflows/*/runs*) cat "$MOCK_RUNS";;
  *actions/runs/*/attempts/*/jobs*) cat "$MOCK_JOBS";;
  *) echo "mock gh: unexpected URL $last" >&2; exit 90;;
esac
'''

KIND_MOCK = r'''#!/bin/sh
# kind stand-in. Static answers via MOCK_KIND_LIST/MOCK_KIND_*_RC; with
# MOCK_KIND_STATE a successful (or, with MOCK_KIND_CREATE_LISTS=1, a failed)
# `create cluster` makes the name appear in later listings until a successful
# `delete cluster` removes it. MOCK_OWNERSHIP_PROBE: the ownership marker path;
# its presence at create time is logged so a test can prove the marker is
# written only after creation.
echo "kind $*" >> "$MOCK_LOG"
case "$1 $2" in
  "get clusters")
    printf '%s' "${MOCK_KIND_LIST:-}"; [ -n "${MOCK_KIND_LIST:-}" ] && echo
    if [ -n "${MOCK_KIND_STATE:-}" ]; then for f in "$MOCK_KIND_STATE"/created-*; do [ -e "$f" ] && basename "$f" | sed 's/^created-//'; done; fi
    exit "${MOCK_KIND_GET_RC:-0}";;
  "delete cluster")
    if [ -n "${MOCK_KIND_STATE:-}" ] && [ "${MOCK_KIND_DELETE_RC:-0}" = 0 ]; then rm -f "$MOCK_KIND_STATE/created-$4"; fi
    exit "${MOCK_KIND_DELETE_RC:-0}";;
  "create cluster")
    if [ -e "${MOCK_OWNERSHIP_PROBE:-/nonexistent}" ]; then echo "marker-present-at-create" >> "$MOCK_LOG"; fi
    if [ -n "${MOCK_KIND_STATE:-}" ]; then
      if [ "${MOCK_KIND_CREATE_RC:-1}" = 0 ]; then touch "$MOCK_KIND_STATE/created-$4"; fi
      if [ "${MOCK_KIND_CREATE_LISTS:-0}" = 1 ]; then touch "$MOCK_KIND_STATE/created-$4"; fi
    fi
    if [ "${MOCK_KIND_CREATE_RC:-1}" != 0 ]; then echo "mock: create refused" >&2; fi
    exit "${MOCK_KIND_CREATE_RC:-1}";;
  *) exit 0;;
esac
'''

DOCKER_MOCK = r'''#!/bin/sh
echo "docker $*" >> "$MOCK_LOG"
case "$1" in
  ps) printf '%s' "${MOCK_DOCKER_PS:-}"; [ -n "${MOCK_DOCKER_PS:-}" ] && echo; exit "${MOCK_DOCKER_PS_RC:-0}";;
  *) exit 0;;
esac
'''


def write_exec(path: str, text: str) -> None:
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)
    os.chmod(path, os.stat(path).st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


class ScriptCase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="gb2c-")
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
        self.bin = os.path.join(self.tmp, "bin")
        os.makedirs(self.bin)
        self.log = os.path.join(self.tmp, "calls.log")
        open(self.log, "w").close()

    def calls(self) -> str:
        with open(self.log, encoding="utf-8") as f:
            return f.read()

    def run_script(self, script: str, args: list[str], env: dict) -> subprocess.CompletedProcess:
        full = {"PATH": self.bin + ":" + SYSTEM_PATH, "HOME": self.tmp, "MOCK_LOG": self.log, "PYTHONDONTWRITEBYTECODE": "1"}
        full.update(env)
        return subprocess.run(["bash", script, *args], env=full, capture_output=True, text=True, cwd=self.tmp)


# ------------------------------------------------------------------ GB2C-01 --
class PublishPushTests(ScriptCase):
    """publish-push.sh with a crane stand-in: the record is mandatory and honest."""

    def setUp(self):
        super().setUp()
        self.images = os.path.join(self.tmp, "images")
        os.makedirs(self.images)
        self.ids = {}
        for t in ("gb-server", "gbctl"):
            self.ids[t] = make_oci_archive(os.path.join(self.images, f"{t}.oci.tar"),
                                           labels={"org.opencontainers.image.revision": SHA, "org.opencontainers.image.title": t})
        write_exec(os.path.join(self.bin, "crane"), CRANE_MOCK)
        self.scan = os.path.join(self.tmp, "scan")
        os.makedirs(self.scan)
        with open(os.path.join(self.scan, "trivy-version.txt"), "w") as f:
            f.write("Version: 0.76.1\n")
        self.record = os.path.join(self.tmp, "out", "publish-record.json")
        os.makedirs(os.path.dirname(self.record))
        self.outputs = os.path.join(self.tmp, "github-output")
        open(self.outputs, "w").close()

    def publish(self, mode: str = "success", script: str | None = None, record: str | None = None, env: dict | None = None) -> subprocess.CompletedProcess:
        full = {"MOCK_MODE": mode, "MOCK_IMAGES": self.images, "IMAGE_NAMESPACE": NS, "GITHUB_SHA": SHA,
                "GITHUB_REPOSITORY": "phreddrick/GB", "GITHUB_SERVER_URL": "https://github.com", "GITHUB_RUN_ID": "1",
                "SCAN_DIR": self.scan, "GITHUB_OUTPUT": self.outputs, "SOURCE_DATE_EPOCH": "1761782400"}
        full.update(env or {})
        env = full
        return self.run_script(script or os.path.join(HERE, "publish-push.sh"), [self.images, record or self.record], env)

    def read_record(self) -> dict:
        with open(self.record, encoding="utf-8") as f:
            return json.load(f)

    def outputs_text(self) -> str:
        with open(self.outputs, encoding="utf-8") as f:
            return f.read()

    def assert_not_success(self, r: subprocess.CompletedProcess) -> None:
        self.assertNotEqual(r.returncode, 0)
        self.assertNotIn("ALL PASS", r.stdout + r.stderr)
        self.assertEqual(self.outputs_text(), "", "digests must not be exported to GITHUB_OUTPUT")

    def test_both_images_publish(self):
        r = self.publish()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("publish-push: ALL PASS", r.stdout)
        rec = self.read_record()
        self.assertEqual(rec["status"], "published")
        self.assertTrue(rec["complete"] and rec["master_tag_applied"])
        for t in ("gb-server", "gbctl"):
            self.assertEqual(rec["images"][t]["state"], "verified")
            self.assertEqual(rec["images"][t]["published_manifest_digest"], self.ids[t]["manifest_digest"])
            self.assertEqual(rec["images"][t]["pull"], f"{NS}/{t}@{self.ids[t]['manifest_digest']}")
            self.assertEqual(rec["images"][t]["tags"], [SHA, "master"])
        self.assertEqual(len(rec["published"]), 2)
        self.assertIn(f"server_digest={self.ids['gb-server']['manifest_digest']}", self.outputs_text())
        self.assertIn(f"gbctl_digest={self.ids['gbctl']['manifest_digest']}", self.outputs_text())
        self.assertEqual(publish_record.validate(rec, expect_status="published", expect_commit=SHA, expect_namespace=NS, images_dir=self.images), [])
        self.assertEqual(self.calls().count("tag "), 2)

    def test_second_push_fails_is_partial(self):
        r = self.publish("second-push-fails")
        self.assert_not_success(r)
        self.assertIn("PARTIAL_PUBLICATION", r.stderr)
        self.assertIn(f"{NS}/gb-server:{SHA} (verified, digest {self.ids['gb-server']['manifest_digest']})", r.stderr)
        rec = self.read_record()
        self.assertEqual(rec["status"], "partial")
        self.assertFalse(rec["complete"] or rec["master_tag_applied"])
        self.assertEqual(rec["images"]["gb-server"]["state"], "verified")
        self.assertEqual(rec["images"]["gb-server"]["published_manifest_digest"], self.ids["gb-server"]["manifest_digest"])
        self.assertEqual(rec["images"]["gbctl"]["state"], "push_failed")
        self.assertIsNone(rec["images"]["gbctl"]["published_manifest_digest"])
        self.assertEqual([e["name"] for e in rec["published"]], ["gb-server"])
        self.assertNotIn("tag ", self.calls(), "no master tag after a partial publication")

    def test_first_push_fails_is_failed_and_stops(self):
        r = self.publish("first-push-fails")
        self.assert_not_success(r)
        self.assertIn("publish-push: FAILED", r.stderr)
        self.assertIn("nothing was pushed", r.stderr)
        rec = self.read_record()
        self.assertEqual(rec["status"], "failed")
        self.assertEqual(rec["images"]["gb-server"]["state"], "push_failed")
        self.assertEqual(rec["images"]["gbctl"]["state"], "not_pushed")
        self.assertEqual(rec["published"], [])
        self.assertNotIn("/gbctl:", self.calls(), "the second image is not pushed after the first failed")

    def test_published_digest_differs_is_partial_unverified(self):
        r = self.publish("mismatch")
        self.assert_not_success(r)
        self.assertIn("differs from the scanned archive", r.stderr)
        self.assertIn("PARTIAL_PUBLICATION", r.stderr)
        rec = self.read_record()
        self.assertEqual(rec["status"], "partial")
        self.assertEqual(rec["images"]["gb-server"]["state"], "pushed_unverified")
        self.assertEqual(rec["images"]["gb-server"]["published_manifest_digest"], "sha256:" + "f" * 64)
        self.assertEqual(rec["images"]["gbctl"]["state"], "not_pushed")
        self.assertNotIn("tag ", self.calls())

    def test_master_tag_failure_is_partial(self):
        r = self.publish("tag-fails")
        self.assert_not_success(r)
        rec = self.read_record()
        self.assertEqual(rec["status"], "partial")
        self.assertFalse(rec["master_tag_applied"])
        self.assertEqual({rec["images"][t]["state"] for t in rec["images"]}, {"verified"})

    def test_jq_program_that_does_not_compile_stops_before_any_push(self):
        scripts = os.path.join(self.tmp, "scripts")
        shutil.copytree(HERE, scripts, ignore=shutil.ignore_patterns("__pycache__", "test_*"))
        with open(os.path.join(scripts, "publish-record.jq"), "a") as f:
            f.write("\n{ broken: [ }\n")
        r = self.publish(script=os.path.join(scripts, "publish-push.sh"))
        self.assertEqual(r.returncode, 2, r.stderr)
        self.assertIn("does not compile", r.stderr)
        self.assertEqual(self.calls(), "", "no crane call before the record program compiled")
        self.assertFalse(os.path.exists(self.record))

    def test_jq_program_compiles_standalone(self):
        args = ["jq", "-n", "-f", os.path.join(HERE, "publish-record.jq")]
        for name in ("status", "master", "sha", "run", "epoch", "ns", "trivy", "s_state", "s_local", "s_remote", "s_config", "c_state", "c_local", "c_remote", "c_config"):
            args += ["--arg", name, "x"]
        args += ["--arg", "s_size", "0", "--arg", "c_size", "0"]
        r = subprocess.run(args, capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(json.loads(r.stdout)["kind"], "gb-publish-record")

    def test_record_directory_missing_refuses_before_push(self):
        r = self.publish(record=os.path.join(self.tmp, "nowhere", "record.json"))
        self.assertEqual(r.returncode, 2)
        self.assertIn("not a writable directory", r.stderr)
        self.assertEqual(self.calls(), "")

    # ---------------------------------------------------------- GB2C-01R --
    # Record finalisation is a verified operation: the destination is refused
    # when it exists at start, `mv -T` is checked, the finalised file must be
    # a regular file at the exact pathname with the validated bytes.

    def assert_partial_after_complete_registry_writes(self, r: subprocess.CompletedProcess) -> None:
        self.assert_not_success(r)
        self.assertIn("PARTIAL_PUBLICATION", r.stderr)
        self.assertIn("registry writes complete", r.stderr)
        for t in ("gb-server", "gbctl"):
            self.assertIn(self.ids[t]["manifest_digest"], r.stderr, "identifiers of what was published are still named")
        self.assertEqual(self.calls().count("tag "), 2, "both images had been tagged before the record failed")

    def kept_tmp(self) -> str | None:
        d = os.path.dirname(self.record)
        tmps = [f for f in os.listdir(d) if f.startswith(".publish-record.json.tmp.")]
        return os.path.join(d, tmps[0]) if tmps else None

    def test_normal_finalisation_is_a_regular_file_with_the_validated_bytes(self):
        r = self.publish()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertTrue(os.path.isfile(self.record) and not os.path.islink(self.record))
        self.assertIsNone(self.kept_tmp(), "no temporary record remains after a verified move")
        with open(self.record, "rb") as f:
            digest = __import__("hashlib").sha256(f.read()).hexdigest()
        self.assertIn(f"publish record ({self.record}, sha256 {digest}):", r.stdout)
        self.assertLess(r.stdout.index("publish record ("), r.stdout.index("publish-push: ALL PASS"))

    def test_codex_directory_destination_reproduction_now_refuses_before_any_push(self):
        os.mkdir(self.record)
        r = self.publish()
        self.assertEqual(r.returncode, 2, r.stdout + r.stderr)
        self.assertIn("already is a directory; refusing to publish", r.stderr)
        self.assert_not_success(r)
        self.assertEqual(self.calls(), "", "no crane call when the record destination is unusable")
        self.assertTrue(os.path.isdir(self.record))
        self.assertEqual(os.listdir(self.record), [], "nothing was placed inside the directory")

    def test_symlink_destination_is_refused_before_any_push(self):
        target = os.path.join(self.tmp, "elsewhere.json")
        with open(target, "w") as f:
            f.write("{}")
        for link_target in (target, os.path.join(self.tmp, "dangling")):
            with self.subTest(target=link_target):
                if os.path.lexists(self.record):
                    os.unlink(self.record)
                os.symlink(link_target, self.record)
                r = self.publish()
                self.assertEqual(r.returncode, 2, r.stdout + r.stderr)
                self.assertIn("already is a symlink; refusing to publish", r.stderr)
                self.assertEqual(self.calls(), "")
                with open(target) as f:
                    self.assertEqual(f.read(), "{}", "the symlink target is untouched")

    def test_existing_record_is_never_overwritten(self):
        with open(self.record, "w") as f:
            f.write('{"kind":"gb-publish-record","status":"published","note":"from another run"}\n')
        r = self.publish()
        self.assertEqual(r.returncode, 2)
        self.assertIn("already exists; refusing to publish", r.stderr)
        self.assertEqual(self.calls(), "")
        with open(self.record) as f:
            self.assertIn("from another run", f.read())

    def test_unwritable_destination_directory_is_refused_before_any_push(self):
        if os.geteuid() == 0:
            self.skipTest("root ignores directory modes")
        os.chmod(os.path.dirname(self.record), 0o555)
        self.addCleanup(os.chmod, os.path.dirname(self.record), 0o755)
        r = self.publish()
        self.assertEqual(r.returncode, 2)
        self.assertIn("not a writable directory", r.stderr)
        self.assertEqual(self.calls(), "")

    def test_directory_appearing_during_the_run_is_not_a_container_for_the_record(self):
        r = self.publish(env={"MOCK_ON_TAG_MKDIR": self.record})
        self.assert_partial_after_complete_registry_writes(r)
        self.assertIn("appeared during the run (directory); refusing to overwrite it", r.stderr)
        self.assertTrue(os.path.isdir(self.record))
        self.assertEqual(os.listdir(self.record), [], "mv -T never places the record inside the directory")
        self.assertIsNone(self.kept_tmp(), "nothing was rendered once the destination was found unusable")

    def test_symlink_appearing_during_the_run_is_refused(self):
        target = os.path.join(self.tmp, "elsewhere.json")
        with open(target, "w") as f:
            f.write("{}")
        r = self.publish(env={"MOCK_ON_TAG_SYMLINK": f"{self.record}:{target}"})
        self.assert_partial_after_complete_registry_writes(r)
        self.assertIn("appeared during the run (symlink)", r.stderr)
        with open(target) as f:
            self.assertEqual(f.read(), "{}")

    def test_directory_becoming_unwritable_during_the_run_is_reported(self):
        if os.geteuid() == 0:
            self.skipTest("root ignores directory modes")
        self.addCleanup(os.chmod, os.path.dirname(self.record), 0o755)
        r = self.publish(env={"MOCK_ON_TAG_CHMOD_RO": os.path.dirname(self.record)})
        self.assert_partial_after_complete_registry_writes(r)
        self.assertIn("cannot create the temporary record", r.stderr)
        self.assertFalse(os.path.exists(self.record))

    def test_move_failure_after_both_images_published_is_partial(self):
        write_exec(os.path.join(self.bin, "mv"), "#!/bin/sh\necho 'mock mv: disk full' >&2\nexit 1\n")
        r = self.publish()
        self.assert_partial_after_complete_registry_writes(r)
        self.assertIn("could not be moved to", r.stderr)
        self.assertIn("mv -T failed", r.stderr)
        self.assertFalse(os.path.exists(self.record))
        tmp = self.kept_tmp()
        self.assertIsNotNone(tmp, "the validated temporary record is kept for the operator")
        with open(tmp) as f:
            self.assertEqual(json.load(f)["status"], "published")

    def test_finalised_record_missing_after_a_silent_move(self):
        write_exec(os.path.join(self.bin, "mv"), "#!/bin/sh\nexit 0\n")
        r = self.publish()
        self.assert_partial_after_complete_registry_writes(r)
        self.assertIn("missing at the exact pathname", r.stderr)
        self.assertFalse(os.path.exists(self.record))

    def test_finalised_record_differing_from_the_validated_record(self):
        write_exec(os.path.join(self.bin, "mv"), '#!/bin/sh\n/usr/bin/mv "$@" && printf "\\n" >> "$3"\n')
        r = self.publish()
        self.assert_partial_after_complete_registry_writes(r)
        self.assertIn("differs from the validated record", r.stderr)

    def test_finalised_record_malformed(self):
        write_exec(os.path.join(self.bin, "mv"), '#!/bin/sh\nrm -f "$2" && printf "{" > "$3"\n')
        r = self.publish()
        self.assert_partial_after_complete_registry_writes(r)
        self.assertRegex(r.stderr, "differs from the validated record|does not validate on a fresh read")
        with self.assertRaises(publish_record.RecordError):
            publish_record.load(self.record)

    def test_finalised_record_becoming_a_symlink_is_refused(self):
        target = os.path.join(self.tmp, "elsewhere.json")
        write_exec(os.path.join(self.bin, "mv"), f'#!/bin/sh\n/usr/bin/mv "$2" "{target}" && ln -s "{target}" "$3"\n')
        r = self.publish()
        self.assert_partial_after_complete_registry_writes(r)
        self.assertIn("is a symlink after the move", r.stderr)

    def test_first_image_published_second_fails_finalises_a_partial_record(self):
        r = self.publish("second-push-fails")
        self.assert_not_success(r)
        self.assertIn("PARTIAL_PUBLICATION", r.stderr)
        self.assertNotIn("registry writes complete", r.stderr)
        self.assertTrue(os.path.isfile(self.record) and not os.path.islink(self.record))
        self.assertEqual(publish_record.validate(self.read_record(), expect_status="partial", expect_commit=SHA, expect_namespace=NS, images_dir=self.images), [])
        self.assertIsNone(self.kept_tmp())


class PublishRecordValidatorTests(unittest.TestCase):
    """publish_record.py: the record schema and its contradictions."""

    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.mkdtemp(prefix="gb2c-rec-")
        cls.images = os.path.join(cls.tmp, "images")
        os.makedirs(cls.images)
        cls.ids = {t: make_oci_archive(os.path.join(cls.images, f"{t}.oci.tar")) for t in ("gb-server", "gbctl")}

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.tmp, ignore_errors=True)

    def render(self, status="published", master="true", **states) -> dict:
        s = states.get("s", "verified")
        c = states.get("c", "verified")
        args = ["jq", "-n", "-f", os.path.join(HERE, "publish-record.jq"), "--arg", "status", status, "--arg", "master", master,
                "--arg", "sha", SHA, "--arg", "run", "https://github.com/phreddrick/GB/actions/runs/1/attempts/1", "--arg", "epoch", "1",
                "--arg", "ns", NS, "--arg", "trivy", "Version: 0.76.1"]
        for prefix, t, state in (("s", "gb-server", s), ("c", "gbctl", c)):
            pushed = state in ("verified", "pushed_unverified")
            args += ["--arg", f"{prefix}_state", state, "--arg", f"{prefix}_local", self.ids[t]["manifest_digest"],
                     "--arg", f"{prefix}_remote", self.ids[t]["manifest_digest"] if pushed else "",
                     "--arg", f"{prefix}_config", self.ids[t]["config_digest"], "--arg", f"{prefix}_size", "123"]
        r = subprocess.run(args, capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stderr)
        return json.loads(r.stdout)

    def problems(self, rec: dict, status: str = "published") -> list[str]:
        return publish_record.validate(rec, expect_status=status, expect_commit=SHA, expect_namespace=NS, images_dir=self.images)

    def cli(self, text: bytes, status: str = "published") -> int:
        p = os.path.join(self.tmp, "r.json")
        with open(p, "wb") as f:
            f.write(text)
        return subprocess.run([sys.executable, os.path.join(HERE, "publish_record.py"), p, "--expect-status", status, "--expect-commit", SHA,
                               "--expect-namespace", NS, "--images-dir", self.images], capture_output=True).returncode

    def test_valid_published_and_partial(self):
        self.assertEqual(self.problems(self.render()), [])
        self.assertEqual(self.problems(self.render("partial", "false", s="verified", c="push_failed"), "partial"), [])
        self.assertEqual(self.problems(self.render("failed", "false", s="push_failed", c="not_pushed"), "failed"), [])
        self.assertEqual(self.cli(json.dumps(self.render()).encode()), 0)

    def test_empty_truncated_and_non_object(self):
        self.assertEqual(self.cli(b""), 2)
        self.assertEqual(self.cli(b"   \n"), 2)
        self.assertEqual(self.cli(json.dumps(self.render()).encode()[:-40]), 2)
        self.assertEqual(self.cli(b"[]"), 2)
        self.assertEqual(self.cli(b"null"), 2)

    def test_missing_image_and_contradictions(self):
        good = self.render()
        cases = {}
        rec = json.loads(json.dumps(good)); del rec["images"]["gbctl"]; cases["one image missing"] = rec
        rec = json.loads(json.dumps(good)); rec["images"]["gb-server"]["repository"] = f"{NS}/gbctl"; cases["contradictory image identity"] = rec
        rec = json.loads(json.dumps(good)); rec["images"]["gb-server"]["target"] = "gbctl"; cases["wrong target"] = rec
        rec = json.loads(json.dumps(good)); rec["images"]["gbctl"]["state"] = "pushed_unverified"; cases["published with an unverified image"] = rec
        rec = json.loads(json.dumps(good)); rec["images"]["gbctl"]["published_manifest_digest"] = "sha256:" + "0" * 64; rec["images"]["gbctl"]["pull"] = f"{NS}/gbctl@sha256:" + "0" * 64; cases["published digest differs from the archive"] = rec
        rec = json.loads(json.dumps(good)); rec["images"]["gbctl"]["pull"] = f"{NS}/gbctl@{self.ids['gb-server']['manifest_digest']}"; cases["pull reference names the other digest"] = rec
        rec = json.loads(json.dumps(good)); rec["source_commit"] = OTHER_SHA; cases["other commit"] = rec
        rec = json.loads(json.dumps(good)); rec["complete"] = False; cases["complete contradicts status"] = rec
        rec = json.loads(json.dumps(good)); rec["status"] = "partial"; rec["complete"] = False; cases["partial but everything verified and tagged"] = rec
        rec = json.loads(json.dumps(good)); rec["published"] = []; cases["published list omits a pushed image"] = rec
        rec = json.loads(json.dumps(good)); rec["images"]["gbctl"]["local_manifest_digest"] = self.ids["gb-server"]["manifest_digest"]; cases["local digest is not the archive's"] = rec
        rec = json.loads(json.dumps(good)); rec["status"] = "started"; rec["complete"] = False; rec["master_tag_applied"] = False; cases["placeholder status"] = rec
        rec = json.loads(json.dumps(good)); del rec["workflow_run"]; cases["missing field"] = rec
        for label, bad in cases.items():
            with self.subTest(label):
                status = bad.get("status") if bad.get("status") in publish_record.STATUSES else "published"
                self.assertNotEqual(self.problems(bad, status), [], label)
        partial = self.render("partial", "true", s="verified", c="push_failed")
        self.assertNotEqual(self.problems(partial, "partial"), [], "master tag on a partial publication")
        failed = self.render("failed", "false", s="verified", c="push_failed")
        self.assertNotEqual(self.problems(failed, "failed"), [], "status failed although an image was pushed")

    def test_publisher_and_record_must_agree(self):
        self.assertNotEqual(self.problems(self.render(), "partial"), [])


# ------------------------------------------------------------------ GB2C-02 --
def run_obj(**over) -> dict:
    base = {"id": 100, "run_attempt": 1, "path": ".github/workflows/ci.yml", "head_sha": SHA, "head_branch": "master", "event": "push",
            "status": "completed", "conclusion": "success", "html_url": "https://github.com/phreddrick/GB/actions/runs/100",
            "repository": {"full_name": "phreddrick/GB"}, "head_repository": {"full_name": "phreddrick/GB"}}
    base.update(over)
    return base


REQUIRED = ["build, test, lint (fresh checkout)", "docker build from checkout (no push)", "image scan (trivy)"]


def job_obj(name: str, **over) -> dict:
    base = {"id": hash(name) % 10000, "run_id": 100, "run_attempt": 1, "head_sha": SHA, "name": name, "status": "completed", "conclusion": "success"}
    base.update(over)
    return base


def runs_doc(*runs) -> dict:
    return {"total_count": len(runs), "workflow_runs": list(runs)}


def jobs_doc(*jobs) -> dict:
    return {"total_count": len(jobs), "jobs": list(jobs)}


class CiProvenanceTests(unittest.TestCase):
    """ci_provenance.py on synthetic workflow-run and job responses."""

    def select(self, doc, **over):
        kw = {"repository": "phreddrick/GB", "workflow_path": ".github/workflows/ci.yml", "sha": SHA, "branch": "master", "event": "push"}
        kw.update(over)
        return ci_provenance.select_run(doc, **kw)

    def verify(self, doc, run_id=100, attempt=1, required=None):
        return ci_provenance.verify_jobs(doc, run_id=run_id, run_attempt=attempt, sha=SHA, required=required or REQUIRED)

    def test_valid_run_and_jobs(self):
        run, lines = self.select(runs_doc(run_obj()))
        self.assertEqual(run["id"], 100)
        self.assertTrue(any(l.startswith("PASS selected run 100 attempt 1") for l in lines))
        ok, lines = self.verify(jobs_doc(*(job_obj(n) for n in REQUIRED), job_obj("extra job")))
        self.assertTrue(ok, lines)

    def test_run_identity_rejections(self):
        cases = {
            "unrelated workflow": run_obj(path=".github/workflows/other.yml"),
            "wrong repository": run_obj(repository={"full_name": "someone/GB"}),
            "fork head repository": run_obj(head_repository={"full_name": "someone/GB"}),
            "wrong sha": run_obj(head_sha=OTHER_SHA),
            "feature branch": run_obj(head_branch="feature/x"),
            "pull request event": run_obj(event="pull_request"),
            "in progress": run_obj(status="in_progress", conclusion=None),
            "cancelled": run_obj(conclusion="cancelled"),
            "failure": run_obj(conclusion="failure"),
        }
        for label, run in cases.items():
            with self.subTest(label):
                selected, lines = self.select(runs_doc(run))
                self.assertIsNone(selected, label)
                self.assertTrue(any(l.startswith("FAIL") for l in lines), lines)
        self.assertIsNone(self.select(runs_doc())[0])

    def test_newer_failed_run_does_not_outrank_and_two_eligible_is_ambiguous(self):
        run, _ = self.select(runs_doc(run_obj(id=101, conclusion="failure"), run_obj(id=100)))
        self.assertEqual(run["id"], 100)
        run, lines = self.select(runs_doc(run_obj(id=100), run_obj(id=101)))
        self.assertIsNone(run)
        self.assertTrue(any("ambiguous" in l for l in lines))

    def test_job_rejections(self):
        base = [job_obj(n) for n in REQUIRED]
        cases = {
            "mixed runs": [job_obj(REQUIRED[0], run_id=99)] + base[1:],
            "mixed attempts": [job_obj(REQUIRED[0], run_attempt=2)] + base[1:],
            "job for another commit": [job_obj(REQUIRED[0], head_sha=OTHER_SHA)] + base[1:],
            "missing required job": base[1:],
            "skipped job": [job_obj(REQUIRED[0], conclusion="skipped")] + base[1:],
            "failed job": [job_obj(REQUIRED[0], conclusion="failure")] + base[1:],
            "unfinished job": [job_obj(REQUIRED[0], status="in_progress", conclusion=None)] + base[1:],
            "duplicate required job": base + [job_obj(REQUIRED[0])],
        }
        for label, jobs in cases.items():
            with self.subTest(label):
                ok, lines = self.verify(jobs_doc(*jobs))
                self.assertFalse(ok, label)
                self.assertTrue(any(l.startswith("FAIL") for l in lines), lines)

    def test_malformed_responses(self):
        for label, doc in {
            "runs not a list": {"total_count": 1, "workflow_runs": {}},
            "runs truncated": {"total_count": 2, "workflow_runs": [run_obj()]},
            "run without id": runs_doc({k: v for k, v in run_obj().items() if k != "id"}),
            "no total_count": {"workflow_runs": [run_obj()]},
        }.items():
            with self.subTest(label), self.assertRaises(ci_provenance.Malformed):
                self.select(doc)
        for label, doc in {
            "jobs not a list": {"total_count": 0, "jobs": None},
            "jobs truncated": {"total_count": 5, "jobs": [job_obj(REQUIRED[0])]},
            "job without name": jobs_doc({**job_obj(REQUIRED[0]), "name": ""}),
        }.items():
            with self.subTest(label), self.assertRaises(ci_provenance.Malformed):
                self.verify(doc)

    def test_cli_exit_codes(self):
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp)
        runs, jobs, out, req = (os.path.join(tmp, n) for n in ("runs.json", "jobs.json", "out.json", "req.txt"))
        with open(req, "w") as f:
            f.write("\n".join(REQUIRED) + "\n")
        cli = [sys.executable, os.path.join(HERE, "ci_provenance.py")]
        sel = cli + ["select-run", "--runs", runs, "--repository", "phreddrick/GB", "--workflow-path", ".github/workflows/ci.yml",
                     "--sha", SHA, "--branch", "master", "--event", "push", "--out", out]
        ver = cli + ["verify-jobs", "--jobs", jobs, "--run-id", "100", "--run-attempt", "1", "--sha", SHA, "--required-checks", req]
        with open(runs, "w") as f:
            json.dump(runs_doc(run_obj()), f)
        with open(jobs, "w") as f:
            json.dump(jobs_doc(*(job_obj(n) for n in REQUIRED)), f)
        self.assertEqual(subprocess.run(sel, capture_output=True).returncode, 0)
        with open(out, encoding="utf-8") as f:
            self.assertEqual(json.load(f)["run_id"], 100)
        self.assertEqual(subprocess.run(ver, capture_output=True).returncode, 0)
        with open(runs, "w") as f:
            f.write("build, test, lint (fresh checkout)\tcompleted\tsuccess\tgithub-actions\n")  # the Codex check-run TSV
        self.assertEqual(subprocess.run(sel, capture_output=True).returncode, 2)
        with open(runs, "w") as f:
            json.dump({"message": "Not Found"}, f)
        self.assertEqual(subprocess.run(sel, capture_output=True).returncode, 2)
        with open(jobs, "w") as f:
            json.dump(jobs_doc(*(job_obj(n, run_attempt=2) for n in REQUIRED)), f)
        self.assertEqual(subprocess.run(ver, capture_output=True).returncode, 1)


class PreflightScriptTests(ScriptCase):
    """publish-preflight.sh end to end with gh and git stand-ins."""

    def setUp(self):
        super().setUp()
        write_exec(os.path.join(self.bin, "git"), GIT_MOCK)
        write_exec(os.path.join(self.bin, "gh"), GH_MOCK)
        self.runs = os.path.join(self.tmp, "runs.json")
        self.jobs = os.path.join(self.tmp, "jobs.json")

    def preflight(self, runs, jobs, **env_over) -> subprocess.CompletedProcess:
        with open(self.runs, "w") as f:
            f.write(runs if isinstance(runs, str) else json.dumps(runs))
        with open(self.jobs, "w") as f:
            f.write(jobs if isinstance(jobs, str) else json.dumps(jobs))
        env = {"MOCK_RUNS": self.runs, "MOCK_JOBS": self.jobs, "CONFIRM": "publish", "GITHUB_REF": "refs/heads/master", "GITHUB_SHA": SHA,
               "GITHUB_REPOSITORY": "phreddrick/GB", "GH_TOKEN": "synthetic", "REQUIRED_CHECKS": "\n".join(REQUIRED) + "\n"}
        env.update(env_over)
        return self.run_script(os.path.join(HERE, "publish-preflight.sh"), [], env)

    def test_valid_run_passes(self):
        r = self.preflight(runs_doc(run_obj()), jobs_doc(*(job_obj(n) for n in REQUIRED)))
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("preflight complete: ci run 100 attempt 1 vouches for", r.stdout)
        self.assertIn("actions/workflows/ci.yml/runs?head_sha=" + SHA, self.calls())
        self.assertIn("actions/runs/100/attempts/1/jobs", self.calls())

    def test_codex_unrelated_checks_reproduction_now_fails(self):
        tsv = "\n".join(f"{n}\tcompleted\tsuccess\tgithub-actions\thttps://github.com/phreddrick/GB/actions/runs/999/job/1" for n in REQUIRED)
        r = self.preflight(tsv, tsv)
        self.assertNotEqual(r.returncode, 0)
        self.assertNotIn("preflight complete", r.stdout)
        self.assertIn("publication refused", r.stderr)

    def test_unrelated_workflow_with_identical_job_names_fails(self):
        r = self.preflight(runs_doc(run_obj(path=".github/workflows/nightly.yml")), jobs_doc(*(job_obj(n) for n in REQUIRED)))
        self.assertNotEqual(r.returncode, 0)
        self.assertNotIn("preflight complete", r.stdout)
        self.assertNotIn("attempts/", self.calls(), "jobs are never fetched without a selected run")

    def test_feature_branch_pull_request_and_mixed_attempt_fail(self):
        for runs, jobs in (
            (runs_doc(run_obj(head_branch="feature/x")), jobs_doc(*(job_obj(n) for n in REQUIRED))),
            (runs_doc(run_obj(event="pull_request")), jobs_doc(*(job_obj(n) for n in REQUIRED))),
            (runs_doc(run_obj(run_attempt=2)), jobs_doc(job_obj(REQUIRED[0], run_attempt=1), *(job_obj(n, run_attempt=2) for n in REQUIRED[1:]))),
            (runs_doc(run_obj()), jobs_doc(*(job_obj(n) for n in REQUIRED[1:]))),
        ):
            with self.subTest(runs=runs, jobs=jobs):
                r = self.preflight(runs, jobs)
                self.assertNotEqual(r.returncode, 0)
                self.assertNotIn("preflight complete", r.stdout)

    def test_confirmation_and_ref_still_guard(self):
        self.assertNotEqual(self.preflight(runs_doc(run_obj()), jobs_doc(), CONFIRM="yes").returncode, 0)
        self.assertNotEqual(self.preflight(runs_doc(run_obj()), jobs_doc(), GITHUB_REF="refs/heads/feature/x").returncode, 0)
        self.assertEqual(self.calls(), "", "no API call before the local guards pass")


# ------------------------------------------------------------------ GB2C-03 --
class TrivyReportValidationTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="gb2c-trivy-")
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)

    def run_policy(self, payload, *extra) -> subprocess.CompletedProcess:
        p = os.path.join(self.tmp, "report.json")
        with open(p, "wb") as f:
            f.write(payload if isinstance(payload, bytes) else json.dumps(payload).encode())
        return subprocess.run([sys.executable, os.path.join(HERE, "trivy_policy.py"), p, *extra], capture_output=True, text=True)

    def test_malformed_reports_are_no_evidence(self):
        good = trivy_report()
        cases = {
            "empty object": {},
            "missing metadata": {k: v for k, v in good.items() if k != "Metadata"},
            "missing ArtifactName": {k: v for k, v in good.items() if k != "ArtifactName"},
            "wrong ArtifactType": dict(good, ArtifactType="filesystem"),
            "Results false": dict(good, Results=False),
            "Results empty map": dict(good, Results={}),
            "Results null": dict(good, Results=None),
            "Results absent (no section)": {k: v for k, v in good.items() if k != "Results"},
            "Results empty list": dict(good, Results=[]),
            "result without Target": dict(good, Results=[{"Class": "os-pkgs"}]),
            "result with unknown Class": dict(good, Results=[{"Target": "x", "Class": "vulns"}]),
            "Vulnerabilities map": dict(good, Results=[{"Target": "x", "Class": "os-pkgs", "Vulnerabilities": {}}]),
            "Vulnerabilities null": dict(good, Results=[{"Target": "x", "Class": "os-pkgs", "Vulnerabilities": None}]),
            "Secrets false": dict(good, Results=[{"Target": "x", "Class": "os-pkgs", "Secrets": False}]),
            "vulnerability without severity": trivy_report([{"VulnerabilityID": "CVE-1", "PkgName": "p", "InstalledVersion": "1", "FixedVersion": "2"}]),
            "vulnerability with bogus severity": trivy_report([{"VulnerabilityID": "CVE-1", "PkgName": "p", "InstalledVersion": "1", "Severity": "critical"}]),
            "vulnerability as string": dict(good, Results=[{"Target": "x", "Class": "os-pkgs", "Vulnerabilities": ["CVE-1"]}]),
            "secret without RuleID": trivy_report(secrets=[{"Category": "AWS", "Severity": "CRITICAL", "Title": "t"}]),
            "image id not a digest": dict(good, Metadata={"ImageID": "latest"}),
            "schema version 1": dict(good, SchemaVersion=1),
            "schema version true": dict(good, SchemaVersion=True),
            "top level list": [good],
            "truncated JSON": json.dumps(good).encode()[:-25],
            "empty file (scanner wrote nothing)": b"",
            "html error page": b"<html>502</html>",
        }
        for label, payload in cases.items():
            with self.subTest(label):
                r = self.run_policy(payload)
                self.assertEqual(r.returncode, 2, label + ": " + r.stdout + r.stderr)
                self.assertNotIn("trivy_policy: PASS", r.stdout)
                self.assertIn("no scan evidence", r.stderr)
        r = subprocess.run([sys.executable, os.path.join(HERE, "trivy_policy.py"), os.path.join(self.tmp, "missing.json")], capture_output=True, text=True)
        self.assertEqual(r.returncode, 2)

    def test_codex_fixtures_fail_closed(self):
        for payload in ({"SchemaVersion": 2, "Results": {}}, {"SchemaVersion": 2, "Results": False}, {"SchemaVersion": 2, "ArtifactName": "some image"},
                        {"SchemaVersion": 2, "Results": [{"Target": "image", "Vulnerabilities": {}, "Secrets": False}]}):
            with self.subTest(payload=payload):
                self.assertEqual(self.run_policy(payload).returncode, 2)

    def test_identity_mismatch_is_no_evidence(self):
        good = trivy_report()
        p = os.path.join(self.tmp, "report.json")
        r = self.run_policy(good, "--expect", p, "gbctl.oci.tar", IMAGE_ID)
        self.assertEqual(r.returncode, 2)
        self.assertIn("not the intended archive", r.stderr)
        r = self.run_policy(good, "--expect", p, "gb-server.oci.tar", "sha256:" + "11" * 32)
        self.assertEqual(r.returncode, 2)
        self.assertIn("a different image was scanned", r.stderr)
        r = self.run_policy(good, "--require-identity")
        self.assertEqual(r.returncode, 2)
        r = self.run_policy(good, "--require-identity", "--expect", p, "gb-server.oci.tar", IMAGE_ID)
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_genuine_outcomes_are_distinct(self):
        r = self.run_policy(trivy_report())
        self.assertEqual(r.returncode, 0)
        self.assertIn("Clean scan", r.stdout)
        high = {"VulnerabilityID": "CVE-2", "PkgName": "libc6", "InstalledVersion": "2.36", "FixedVersion": "2.37", "Severity": "HIGH"}
        crit_unfixed = {"VulnerabilityID": "CVE-3", "PkgName": "libc6", "InstalledVersion": "2.36", "Severity": "CRITICAL"}
        r = self.run_policy(trivy_report([high, crit_unfixed]))
        self.assertEqual(r.returncode, 0)
        self.assertIn("2 finding(s), none blocking", r.stdout)
        self.assertIn("| CRITICAL | 0 | 1 |", r.stdout)
        crit_fixed = dict(crit_unfixed, FixedVersion="2.37")
        r = self.run_policy(trivy_report([crit_fixed]))
        self.assertEqual(r.returncode, 1)
        self.assertIn("Policy violations", r.stdout)
        r = self.run_policy(trivy_report(secrets=[SECRET]))
        self.assertEqual(r.returncode, 1)
        self.assertIn("Secrets found", r.stdout)
        # Two sections, lang-pkgs present with zero findings (Trivy keeps empty results for known targets).
        rep = trivy_report()
        rep["Results"].append({"Target": "usr/local/bin/gb-server", "Class": "lang-pkgs", "Type": "gobinary"})
        self.assertEqual(self.run_policy(rep).returncode, 0)


# ------------------------------------------------------------------ GB2C-04 --
class KindCleanupTests(ScriptCase):
    """kind-cleanup.sh and the kind-e2e.sh exit trap with kind and docker
    stand-ins (GB2C-04, GB2C-04R). Deletion is authorised only by the
    ownership marker this run wrote after a successful creation; every
    unauthorised case asserts that `kind delete cluster` was never invoked."""

    IDENTITY = {"GITHUB_REPOSITORY": "phreddrick/GB", "GITHUB_RUN_ID": "1", "GITHUB_RUN_ATTEMPT": "1", "GITHUB_JOB": "kind-e2e"}

    def setUp(self):
        super().setUp()
        write_exec(os.path.join(self.bin, "kind"), KIND_MOCK)
        write_exec(os.path.join(self.bin, "docker"), DOCKER_MOCK)
        # kubectl is reached only after a (mock) successful creation; 97 marks it.
        write_exec(os.path.join(self.bin, "kubectl"), "#!/bin/sh\necho 'mock kubectl: no cluster' >&2\nexit 97\n")
        write_exec(os.path.join(self.bin, "sudo"), "#!/bin/sh\necho 'mock sudo: must not be called' >&2\nexit 97\n")
        self.work = os.path.join(self.tmp, "work")
        os.makedirs(self.work)
        self.marker = os.path.join(self.work, "ownership.json")
        self.state = os.path.join(self.tmp, "kind-state")
        os.makedirs(self.state)

    def env(self, extra: dict) -> dict:
        full = dict(self.IDENTITY)
        full.update({"MOCK_KIND_STATE": self.state, "MOCK_OWNERSHIP_PROBE": self.marker})
        full.update(extra)
        return full

    def write_marker(self, **over) -> None:
        rec = {"kind": "gb-kind-e2e-ownership", "state": "owned", "cluster": "gb-ci", "repository": "phreddrick/GB",
               "run_id": "1", "run_attempt": "1", "job": "kind-e2e", "created_at": "2026-10-31T00:00:00Z"}
        rec.update(over)
        with open(self.marker, "w", encoding="utf-8") as f:
            json.dump(rec, f)

    def marker_state(self):
        if not os.path.lexists(self.marker):
            return None
        with open(self.marker, encoding="utf-8") as f:
            return json.load(f)

    def cleanup(self, *args, cluster: str | None = "gb-ci", ownership: str | None = None, **env) -> subprocess.CompletedProcess:
        argv = ["--ownership", ownership or self.marker, "--timeout", "5"]
        if cluster:
            argv += ["--cluster", cluster]
        return self.run_script(os.path.join(HERE, "kind-cleanup.sh"), argv + list(args), self.env(env))

    def kind_e2e(self, **env) -> subprocess.CompletedProcess:
        server = os.path.join(self.tmp, "gb-server.oci.tar")
        gbctl = os.path.join(self.tmp, "gbctl.docker.tar")
        make_oci_archive(server)
        open(gbctl, "w").close()
        base = {"GB_KIND_E2E_ALLOW_LOCAL": "1", "KIND_NODE_IMAGE": "kindest/node:v1.36.4@sha256:" + "0" * 64, "KIND_CLUSTER": "gb-ci",
                "WORK_DIR": self.work, "DIAG_DIR": os.path.join(self.tmp, "diag"), "KUBECONFIG": os.path.join(self.work, "kubeconfig"),
                "KIND_OWNERSHIP_FILE": self.marker}
        base.update(env)
        return self.run_script(os.path.join(HERE, "kind-e2e.sh"), [server, gbctl], self.env(base))

    def assert_no_deletion(self, r: subprocess.CompletedProcess) -> None:
        self.assertNotIn("delete cluster", self.calls(), "kind delete cluster must never run without this run's ownership: " + r.stdout + r.stderr)
        self.assertNotIn("ALL PASS", r.stdout)

    def assert_refused(self, r: subprocess.CompletedProcess, text: str) -> None:
        self.assertEqual(r.returncode, 2, r.stdout + r.stderr)
        self.assertIn("REFUSED cleanup", r.stderr)
        self.assertIn(text, r.stderr)
        self.assertIn("nothing deleted", r.stderr)
        self.assert_no_deletion(r)

    def assert_cleanup_failed(self, r, main_status=0):
        self.assertNotIn("ALL PASS", r.stdout + r.stderr)
        self.assertEqual(r.returncode, 3 if main_status == 0 else main_status, r.stdout + r.stderr)
        self.assertIn("CLEANUP FAILED", r.stderr)
        self.assertEqual(self.marker_state()["state"], "owned", "a failed cleanup keeps the marker owned so the fallback may retry")

    # 1. cluster absent, created, cleaned (the cleanup half; creation is case 13)
    def test_01_owned_cluster_deleted_confirmed_and_marker_released(self):
        self.write_marker()
        r = self.cleanup()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("PASS ownership: marker", r.stdout)
        self.assertIn("kind delete cluster --name gb-ci", self.calls())
        self.assertIn("absent (confirmed by a successful listing)", r.stdout)
        self.assertIn("ownership marker released", r.stdout)
        self.assertIn("kind-e2e: ALL PASS", r.stdout)
        m = self.marker_state()
        self.assertEqual((m["state"], m["cluster"], m["run_id"]), ("released", "gb-ci", "1"))
        self.assertIn("released_at", m)

    # 2 + 3. a cluster of that name exists before the test; the workflow fallback runs anyway
    def test_02_03_codex_preexisting_cluster_reproduction_now_deletes_nothing(self):
        r = self.kind_e2e(MOCK_KIND_LIST="gb-ci")
        self.assertEqual(r.returncode, 2, r.stdout + r.stderr)
        self.assertIn("already exists; it is not ours", r.stderr)
        self.assertNotIn("create cluster", self.calls())
        self.assert_no_deletion(r)
        self.assertIsNone(self.marker_state(), "no ownership marker without a creation")
        before = self.calls()
        r = self.cleanup("--fallback", cluster=None, MOCK_KIND_LIST="gb-ci")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("no ownership marker", r.stdout)
        self.assertIn("nothing to delete", r.stdout)
        self.assert_no_deletion(r)
        self.assertEqual(self.calls(), before, "the fallback makes no kind or docker call without a marker")

    # 4. creation refused or failed before ownership exists
    def test_04_failed_creation_records_no_ownership_and_deletes_nothing(self):
        r = self.kind_e2e()  # the stand-in refuses `kind create cluster`
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("kind create cluster exited 1; no ownership recorded", r.stderr)
        self.assertIn("no ownership marker, nothing deleted", r.stderr)
        self.assertIn("kind create cluster --name gb-ci", self.calls())
        self.assert_no_deletion(r)
        self.assertIsNone(self.marker_state())
        r = self.cleanup("--fallback", cluster=None)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assert_no_deletion(r)

    # 5. creation fails but left something behind that is listed afterwards
    def test_05_failed_creation_with_ambiguous_partial_work_is_left_alone(self):
        r = self.kind_e2e(MOCK_KIND_CREATE_LISTS="1")
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertTrue(os.path.exists(os.path.join(self.state, "created-gb-ci")), "the stand-in left a listed cluster behind")
        self.assert_no_deletion(r)
        self.assertIsNone(self.marker_state())
        r = self.cleanup("--fallback", cluster=None)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assert_no_deletion(r)
        self.assertTrue(os.path.exists(os.path.join(self.state, "created-gb-ci")), "the ambiguous cluster remains; it was never ours to delete")

    # 6. marker absent
    def test_06_marker_absent(self):
        r = self.cleanup()
        self.assert_refused(r, "does not exist")
        self.assertEqual(self.calls(), "", "no kind or docker call without a marker")
        r = self.cleanup("--fallback", cluster=None, MOCK_KIND_LIST="gb-ci")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assert_no_deletion(r)
        self.assertEqual(self.calls(), "")

    # 7. marker names another cluster, or a name outside the disposable pattern
    def test_07_marker_wrong_cluster_name(self):
        self.write_marker(cluster="gb-ci-other")
        r = self.cleanup()
        self.assert_refused(r, "names cluster 'gb-ci-other', not the requested 'gb-ci'")
        for bad in ("kind", "GB-CI", "gb-ci;rm -rf", "prod-gb-ci", "gb-ci-" + "x" * 60):
            with self.subTest(cluster=bad):
                self.write_marker(cluster=bad)
                for mode in ((), ("--fallback",)):
                    r = self.cleanup(*mode, cluster=None, MOCK_KIND_LIST=bad)
                    self.assert_refused(r, "malformed")
        self.assertNotIn("delete", self.calls())

    # 8. marker from another run, attempt, job or repository
    def test_08_marker_belongs_to_another_run(self):
        for field, value, text in (("run_id", "2", "for run '2', this run is '1'"), ("run_attempt", "2", "for attempt '2', this attempt is '1'"),
                                   ("job", "other-job", "for job 'other-job', this job is 'kind-e2e'"), ("repository", "someone/else", "for repository 'someone/else'")):
            with self.subTest(field=field):
                self.write_marker(**{field: value})
                for mode in ((), ("--fallback",)):
                    r = self.cleanup(*mode, MOCK_KIND_LIST="gb-ci")
                    self.assert_refused(r, text)
        # Missing identity in the environment is also a refusal, not a default match.
        self.write_marker()
        r = self.run_script(os.path.join(HERE, "kind-cleanup.sh"), ["--ownership", self.marker, "--fallback"], {"MOCK_KIND_LIST": "gb-ci"})
        self.assert_refused(r, "GITHUB_REPOSITORY is not set")
        self.assertNotIn("delete", self.calls())

    # 9. marker malformed
    def test_09_marker_malformed(self):
        cases = {"not json": "{", "array": "[]", "empty": "", "wrong kind": json.dumps({"kind": "other", "state": "owned", "cluster": "gb-ci"}),
                 "no cluster": json.dumps({"kind": "gb-kind-e2e-ownership", "state": "owned", "repository": "phreddrick/GB", "run_id": "1", "run_attempt": "1", "job": "kind-e2e"}),
                 "numeric run id": json.dumps({"kind": "gb-kind-e2e-ownership", "state": "owned", "cluster": "gb-ci", "repository": "phreddrick/GB", "run_id": 1, "run_attempt": "1", "job": "kind-e2e"}),
                 "two objects": json.dumps({"kind": "gb-kind-e2e-ownership", "state": "owned", "cluster": "gb-ci", "repository": "phreddrick/GB", "run_id": "1", "run_attempt": "1", "job": "kind-e2e"}) * 2,
                 "unknown state": json.dumps({"kind": "gb-kind-e2e-ownership", "state": "maybe", "cluster": "gb-ci", "repository": "phreddrick/GB", "run_id": "1", "run_attempt": "1", "job": "kind-e2e"})}
        for name, text in cases.items():
            with self.subTest(case=name):
                with open(self.marker, "w") as f:
                    f.write(text)
                for mode in ((), ("--fallback",)):
                    r = self.cleanup(*mode, MOCK_KIND_LIST="gb-ci")
                    self.assert_refused(r, "malformed")
        os.unlink(self.marker)
        self.write_marker()
        os.rename(self.marker, self.marker + ".real")
        os.symlink(self.marker + ".real", self.marker)
        r = self.cleanup("--fallback", MOCK_KIND_LIST="gb-ci")
        self.assert_refused(r, "is a symlink")
        os.unlink(self.marker)
        os.mkdir(self.marker)
        r = self.cleanup("--fallback", MOCK_KIND_LIST="gb-ci")
        self.assert_refused(r, "not a regular file")
        self.assertNotIn("delete", self.calls())

    # 10. deletion command fails
    def test_10_deletion_request_failure(self):
        self.write_marker()
        r = self.cleanup(MOCK_KIND_DELETE_RC="1")
        self.assert_cleanup_failed(r)
        self.assertIn("kind delete cluster exited 1", r.stderr)

    # 11. inspection fails
    def test_11_inspection_failure_is_not_absence(self):
        self.write_marker()
        r = self.cleanup(MOCK_KIND_GET_RC="1")
        self.assert_cleanup_failed(r)
        self.assertIn("absence is NOT confirmed", r.stderr)
        self.write_marker()
        r = self.cleanup(MOCK_DOCKER_PS_RC="1")
        self.assert_cleanup_failed(r)
        self.assertIn("container absence is NOT confirmed", r.stderr)
        self.write_marker()
        r = self.cleanup(MOCK_KIND_DELETE_RC="1", MOCK_KIND_GET_RC="1", MOCK_DOCKER_PS_RC="1")  # Codex's runtime-unavailable reproduction
        self.assert_cleanup_failed(r)

    # 12. cluster (or its node containers) remain after deletion
    def test_12_cluster_remains_after_deletion(self):
        self.write_marker()
        r = self.cleanup(MOCK_KIND_LIST="gb-ci")
        self.assert_cleanup_failed(r)
        self.assertIn("still listed after deletion", r.stderr)
        self.write_marker()
        r = self.cleanup(MOCK_DOCKER_PS="gb-ci-control-plane")
        self.assert_cleanup_failed(r)
        self.assertIn("node containers remain: gb-ci-control-plane", r.stderr)

    # 13. main test fails after a successful creation (the real script, its trap and the marker)
    def test_13_main_failure_after_successful_creation_is_cleaned_with_the_marker(self):
        r = self.kind_e2e(MOCK_KIND_CREATE_RC="0")
        self.assertEqual(r.returncode, 97, r.stdout + r.stderr)  # the kubectl stand-in fails the first step after creation
        self.assertIn("ownership recorded in", r.stdout)
        self.assertIn("FAILED (test exit 97); cleanup confirmed", r.stderr)
        self.assertNotIn("ALL PASS", r.stdout + r.stderr)
        calls = self.calls()
        self.assertNotIn("marker-present-at-create", calls, "the marker is written only after `kind create cluster` exited 0")
        self.assertLess(calls.index("kind create cluster --name gb-ci"), calls.index("kind delete cluster --name gb-ci --kubeconfig"))
        m = self.marker_state()
        self.assertEqual((m["state"], m["cluster"], m["run_id"], m["run_attempt"], m["job"]), ("released", "gb-ci", "1", "1", "kind-e2e"))
        self.assertFalse(os.path.exists(os.path.join(self.state, "created-gb-ci")))

    def test_13b_derived_cluster_name_is_unique_to_run_and_attempt(self):
        r = self.kind_e2e(MOCK_KIND_CREATE_RC="0", KIND_CLUSTER="", GITHUB_RUN_ID="123456789", GITHUB_RUN_ATTEMPT="2")
        self.assertEqual(r.returncode, 97, r.stdout + r.stderr)
        import re
        names = set(re.findall(r"kind create cluster --name (\S+)", self.calls()))
        self.assertEqual(len(names), 1)
        name = names.pop()
        self.assertRegex(name, r"^gb-ci-r123456789a2-[0-9a-f]{6}$")
        self.assertIn(f"kind delete cluster --name {name} --kubeconfig", self.calls())
        self.assertEqual(self.marker_state()["cluster"], name)
        self.assertEqual(self.marker_state()["run_attempt"], "2")

    def test_13c_stale_marker_or_bad_identity_refuses_before_creation(self):
        self.write_marker()
        r = self.kind_e2e(MOCK_KIND_CREATE_RC="0")
        self.assertEqual(r.returncode, 2, r.stdout + r.stderr)
        self.assertIn("ownership marker", r.stderr)
        self.assertIn("already exists", r.stderr)
        self.assertEqual(self.calls(), "")
        os.unlink(self.marker)
        r = self.kind_e2e(MOCK_KIND_CREATE_RC="0", GITHUB_RUN_ID="")
        self.assertEqual(r.returncode, 2)
        self.assertIn("GITHUB_RUN_ID is not set", r.stderr)
        self.assertEqual(self.calls(), "")

    # 14. main test and cleanup both fail; the fallback then retries with the still-owned marker
    def test_14_main_and_cleanup_both_fail_then_fallback_retries_owned_cluster(self):
        r = self.kind_e2e(MOCK_KIND_CREATE_RC="0", MOCK_KIND_DELETE_RC="1")
        self.assertEqual(r.returncode, 97, r.stdout + r.stderr)
        self.assertIn("FAILED (test exit 97) AND CLEANUP FAILED", r.stderr)
        self.assertNotIn("ALL PASS", r.stdout + r.stderr)
        self.assertEqual(self.marker_state()["state"], "owned")
        self.assertTrue(os.path.exists(os.path.join(self.state, "created-gb-ci")))
        r = self.cleanup("--fallback", cluster=None)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("is still listed; the script's own cleanup did not release it", r.stdout)
        self.assertIn("fallback confirmed cluster gb-ci absent", r.stdout)
        self.assertNotIn("ALL PASS", r.stdout, "the fallback never prints the trap's verdict")
        self.assertEqual(self.marker_state()["state"], "released")

    # 15. cleanup already succeeded when the fallback executes
    def test_15_released_marker_means_nothing_to_delete(self):
        self.write_marker(state="released", released_at="2026-10-31T00:01:00Z")
        r = self.cleanup("--fallback", cluster=None, MOCK_KIND_LIST="gb-ci")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("already confirmed that gb-ci is absent", r.stdout)
        self.assert_no_deletion(r)
        self.assertEqual(self.calls(), "")
        r = self.cleanup()
        self.assert_refused(r, "already released")

    def test_main_failed_but_cleanup_succeeded_preserves_failure(self):
        self.write_marker()
        r = self.cleanup("--main-status", "1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("FAILED (test exit 1); cleanup confirmed", r.stderr)
        self.assertNotIn("ALL PASS", r.stdout)
        self.write_marker()
        r = self.cleanup("--main-status", "1", MOCK_KIND_DELETE_RC="1", MOCK_KIND_GET_RC="1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("FAILED (test exit 1) AND CLEANUP FAILED", r.stderr)

    def test_fallback_with_owned_marker_and_absent_cluster_deletes_nothing(self):
        self.write_marker()
        r = self.cleanup("--fallback", cluster=None)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertNotIn("delete cluster", self.calls(), "nothing to delete when the listing succeeds and shows no cluster")
        self.assertEqual(self.marker_state()["state"], "released")
        self.write_marker()
        r = self.cleanup("--fallback", cluster=None, MOCK_KIND_GET_RC="1")
        self.assert_cleanup_failed(r)
        self.assertIn("delete cluster", self.calls(), "an unreadable listing in fallback mode still attempts deletion of the owned cluster")

    def test_only_the_disposable_cluster_name_is_accepted(self):
        for name in ("kind", "production", "gb-ci;rm", "GB-CI"):
            with self.subTest(name=name):
                r = self.run_script(os.path.join(HERE, "kind-cleanup.sh"), ["--ownership", self.marker, "--cluster", name], self.IDENTITY)
                self.assertEqual(r.returncode, 2)
                self.assertEqual(self.calls(), "", "no kind or docker call for a refused name")
        r = self.kind_e2e(KIND_CLUSTER="kind")
        self.assertEqual(r.returncode, 2)
        self.assertEqual(self.calls(), "")
        r = self.kind_e2e(MOCK_KIND_GET_RC="1")
        self.assertEqual(r.returncode, 2)
        self.assertIn("cannot prove that no cluster named gb-ci exists", r.stderr)
        self.assertNotIn("create cluster", self.calls())

    def test_workflow_fallback_uses_the_ownership_contract(self):
        with open(os.path.join(REPO, ".github", "workflows", "ci.yml"), encoding="utf-8") as f:
            ci = f.read()
        self.assertIn('.github/scripts/kind-cleanup.sh --fallback --ownership "$KIND_OWNERSHIP_FILE" --timeout 300', ci)
        self.assertEqual(ci.count("KIND_OWNERSHIP_FILE: ${{ runner.temp }}/kind-e2e/ownership.json"), 2, "test and fallback steps share one marker path")
        self.assertNotIn("KIND_CLUSTER: gb-ci", ci, "the cluster name is derived from the run identity")
        self.assertNotIn("kind get clusters 2>/dev/null | grep -qx", ci)
        self.assertNotIn("kind-cleanup.sh --cluster", ci)


# ------------------------------------------------------------------ GB2C-05 --
class TokenCreatedPullRequestGuidanceTests(unittest.TestCase):
    """The operator-facing text must describe GitHub's current GITHUB_TOKEN behaviour."""

    def test_digest_pr_script_and_runbooks(self):
        files = [os.path.join(HERE, "digest-pr.sh"), os.path.join(REPO, ".github", "workflows", "digest-update-pr.yml"),
                 os.path.join(REPO, "docs", "operations", "bootstrap.md"), os.path.join(REPO, "docs", "operations", "deploy.md")]
        for path in files:
            with self.subTest(path=os.path.relpath(path, REPO)):
                with open(path, encoding="utf-8") as f:
                    text = f.read()
                self.assertIn("Approve workflows to run", text)
                for stale in ("add a label or close and reopen", "label or a reopen", "needs a label or reopen", "GITHUB_TOKEN events do not start workflows"):
                    self.assertNotIn(stale, text)
        with open(os.path.join(HERE, "digest-pr.sh"), encoding="utf-8") as f:
            script = f.read()
        self.assertIn("docs.github.com/en/actions/concepts/security/github_token", script)
        self.assertIn("Allow GitHub Actions to create and approve pull requests", script)
        self.assertNotIn("PAT", script.replace("PATH", ""))


if __name__ == "__main__":
    unittest.main()
