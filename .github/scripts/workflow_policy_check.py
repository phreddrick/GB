#!/usr/bin/env python3
"""Static policy check over .github/workflows (Phase 2C, migration plan 13.1,
brief sections 3, 5 and 12). It encodes the workflow-safety decisions as
assertions so a later edit that weakens them fails CI instead of passing
review unnoticed.

    workflow_policy_check.py [--dir .github/workflows]

Rules (all of them, every workflow file):

  W1  triggers are only push, pull_request, workflow_dispatch and
      workflow_call; pull_request_target, workflow_run, schedule, issue and
      comment events are refused (untrusted code never meets privileges).
  W2  every job declares `permissions` explicitly, and the scopes equal the
      allow-list below for that (file, job); any job not listed may hold
      `contents: read` only. `packages: write` exists in exactly one job, the
      publishing job, whose workflow is workflow_dispatch-only, carries a
      `concurrency` group, and whose job has `environment: ghcr-publish` and
      a master-ref guard in its `if`.
  W3  every external `uses:` is pinned to a 40-hex commit with a `# vX.Y.Z`
      comment on the same line; local reusable workflows (`./.github/...`)
      must exist.
  W4  every job has `timeout-minutes` (reusable-workflow calls excepted) and
      runs on `ubuntu-26.04`.
  W5  `run:` scripts expand only `${{ env.* }}` and `${{ runner.* }}`; event
      payload fields, inputs and step outputs reach the shell through `env:`
      (no command injection through branch names, titles or inputs).
  W6  the only secret referenced is `secrets.GITHUB_TOKEN` (never a personal
      access token) and `secrets: inherit` is not used.
  W7  every actions/checkout step sets `persist-credentials: false`, except
      the jobs listed as allowed to push.
  W8  ci.yml keeps the two job names the branch ruleset requires.

Exit status 0 when every rule holds, 1 otherwise. Requires PyYAML.
"""
from __future__ import annotations

import argparse
import os
import re
import sys

try:
    import yaml
except ImportError:  # pragma: no cover
    sys.exit("workflow_policy_check: PyYAML is required (python3-yaml)")

ALLOWED_EVENTS = {"push", "pull_request", "workflow_dispatch", "workflow_call"}
RUNNER = "ubuntu-26.04"
REQUIRED_CI_JOB_NAMES = {"build, test, lint (fresh checkout)", "docker build from checkout (no push)"}
PUBLISH_FILE = "publish-images.yml"
PUBLISH_JOB = "publish"
PUBLISH_ENVIRONMENT = "ghcr-publish"
READ_ONLY = {"contents": "read"}
# (file, job id) -> exact permissions. Anything else must be READ_ONLY.
PERMISSION_ALLOWLIST: dict[tuple[str, str], dict[str, str]] = {
    (PUBLISH_FILE, "preflight"): {"contents": "read", "actions": "read"},  # workflow-runs API, read only (GB2C-02)
    (PUBLISH_FILE, PUBLISH_JOB): {"contents": "read", "packages": "write"},
    (PUBLISH_FILE, "digest-pr"): {"contents": "write", "pull-requests": "write", "packages": "read"},
    ("digest-update-pr.yml", "digest-pr"): {"contents": "write", "pull-requests": "write", "packages": "read"},
}
# Jobs that may keep the checkout token (they push a branch with it).
MAY_PERSIST_CREDENTIALS = {("digest-update-pr.yml", "digest-pr")}

USES_RE = re.compile(r"^(?P<action>[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(/[A-Za-z0-9_./-]+)?)@(?P<ref>[0-9a-f]{40})$")
EXPR_RE = re.compile(r"\$\{\{\s*(?P<expr>[^}]*?)\s*\}\}")
SECRET_RE = re.compile(r"secrets\.(?P<name>[A-Za-z0-9_]+)")


class Report:
    def __init__(self) -> None:
        self.failures: list[str] = []
        self.oks: list[str] = []

    def fail(self, msg: str) -> None:
        self.failures.append(msg)
        print(f"FAIL {msg}")

    def ok(self, msg: str) -> None:
        self.oks.append(msg)
        print(f"PASS {msg}")


def load_workflows(directory: str) -> dict[str, tuple[dict, str]]:
    out = {}
    for name in sorted(os.listdir(directory)):
        if not name.endswith((".yml", ".yaml")):
            continue
        path = os.path.join(directory, name)
        with open(path, encoding="utf-8") as f:
            text = f.read()
        doc = yaml.safe_load(text)
        if not isinstance(doc, dict):
            raise ValueError(f"{name}: not a mapping")
        out[name] = (doc, text)
    return out


def triggers(doc: dict) -> dict:
    on = doc.get("on", doc.get(True))  # YAML 1.1 parses the bare key `on` as True
    if isinstance(on, str):
        return {on: None}
    if isinstance(on, list):
        return {k: None for k in on}
    return on or {}


def check_file(name: str, doc: dict, text: str, directory: str, rep: Report, state: dict) -> None:
    ev = triggers(doc)
    bad = sorted(set(ev) - ALLOWED_EVENTS)
    (rep.fail if bad else rep.ok)(f"[{name}] W1 triggers {sorted(ev)}" + (f": refused {bad}" if bad else ""))

    top_perm = doc.get("permissions")
    if top_perm not in (None, READ_ONLY, "read-all"):
        rep.fail(f"[{name}] W2 top-level permissions must be absent or contents: read (got {top_perm!r})")
    if top_perm is None:
        rep.fail(f"[{name}] W2 top-level permissions block missing (set contents: read so new jobs start read-only)")

    jobs = doc.get("jobs") or {}
    if not isinstance(jobs, dict) or not jobs:
        rep.fail(f"[{name}] no jobs")
        return
    if name == "ci.yml":
        names = {j.get("name") for j in jobs.values() if isinstance(j, dict)}
        missing = REQUIRED_CI_JOB_NAMES - names
        (rep.fail if missing else rep.ok)(f"[{name}] W8 required job names present" + (f"; missing {sorted(missing)}" if missing else ""))

    for jid, job in jobs.items():
        tag = f"[{name}:{jid}]"
        if not isinstance(job, dict):
            rep.fail(f"{tag} job is not a mapping")
            continue
        reusable = "uses" in job and "steps" not in job
        perms = job.get("permissions")
        if perms is None:
            rep.fail(f"{tag} W2 job has no explicit permissions block")
        else:
            if perms == "read-all" or perms == "write-all" or not isinstance(perms, dict):
                rep.fail(f"{tag} W2 permissions must be an explicit mapping (got {perms!r})")
                perms = {}
            expected = PERMISSION_ALLOWLIST.get((name, jid), READ_ONLY)
            if perms != expected:
                rep.fail(f"{tag} W2 permissions {perms} differ from the allow-list {expected}")
            else:
                rep.ok(f"{tag} W2 permissions {perms}")
            if isinstance(perms, dict) and perms.get("packages") == "write":
                state.setdefault("packages_write", []).append((name, jid))
                if set(ev) != {"workflow_dispatch"}:
                    rep.fail(f"{tag} W2 packages: write in a workflow whose triggers are {sorted(ev)}; only workflow_dispatch is reviewed")
                if job.get("environment") not in (PUBLISH_ENVIRONMENT, {"name": PUBLISH_ENVIRONMENT}):
                    rep.fail(f"{tag} W2 publishing job must run in environment {PUBLISH_ENVIRONMENT}")
                cond = str(job.get("if", ""))
                if "github.ref == 'refs/heads/master'" not in cond:
                    rep.fail(f"{tag} W2 publishing job lacks the master-ref guard in its if:")
                if "concurrency" not in doc:
                    rep.fail(f"{tag} W2 publishing workflow has no concurrency group")
        if reusable:
            target = str(job.get("uses"))
            if target.startswith("./"):
                if not os.path.exists(os.path.join(directory, "..", "..", target[2:])):
                    rep.fail(f"{tag} W3 local reusable workflow {target} does not exist")
                else:
                    rep.ok(f"{tag} W3 local reusable workflow {target}")
            else:
                rep.fail(f"{tag} W3 external reusable workflow {target} is not allowed")
            if job.get("secrets") is not None:
                rep.fail(f"{tag} W6 secrets passed to a reusable workflow")
            continue
        if job.get("runs-on") != RUNNER:
            rep.fail(f"{tag} W4 runs-on is {job.get('runs-on')!r}, expected {RUNNER}")
        if not isinstance(job.get("timeout-minutes"), int):
            rep.fail(f"{tag} W4 timeout-minutes missing")
        for i, step in enumerate(job.get("steps") or []):
            stag = f"{tag} step {i + 1} ({step.get('name') or step.get('uses') or 'run'})"
            uses = step.get("uses")
            if uses:
                if uses.startswith("./"):
                    rep.fail(f"{stag} W3 local composite actions are not used in this repository")
                elif not USES_RE.match(uses):
                    rep.fail(f"{stag} W3 uses {uses!r} is not pinned to a 40-hex commit")
                else:
                    # The same line in the source must carry a version comment.
                    line = next((l for l in text.splitlines() if uses in l), "")
                    if not re.search(r"#\s*v\d+(\.\d+){0,2}\b", line):
                        rep.fail(f"{stag} W3 pinned action lacks a '# vX.Y.Z' comment on its line")
                if uses.startswith("actions/checkout@"):
                    with_ = step.get("with") or {}
                    if with_.get("persist-credentials") is not False and (name, jid) not in MAY_PERSIST_CREDENTIALS:
                        rep.fail(f"{stag} W7 checkout must set persist-credentials: false")
            run = step.get("run")
            if isinstance(run, str):
                for m in EXPR_RE.finditer(run):
                    expr = m.group("expr")
                    if not re.match(r"^(env|runner)\.[A-Za-z0-9_.-]+$", expr):
                        rep.fail(f"{stag} W5 run: expands '${{{{ {expr} }}}}'; pass it through env: instead")
    for m in SECRET_RE.finditer(text):
        if m.group("name") != "GITHUB_TOKEN":
            rep.fail(f"[{name}] W6 references secrets.{m.group('name')}; only GITHUB_TOKEN is allowed")
    if re.search(r"^\s*secrets:\s*inherit\s*$", text, re.M):
        rep.fail(f"[{name}] W6 secrets: inherit is not allowed")


def run(directory: str) -> int:
    rep = Report()
    try:
        wfs = load_workflows(directory)
    except (OSError, ValueError, yaml.YAMLError) as e:
        print(f"FAIL cannot load workflows: {e}")
        return 1
    if not wfs:
        print(f"FAIL no workflows under {directory}")
        return 1
    state: dict = {}
    for name, (doc, text) in wfs.items():
        check_file(name, doc, text, directory, rep, state)
    pw = state.get("packages_write", [])
    if pw != [(PUBLISH_FILE, PUBLISH_JOB)]:
        rep.fail(f"W2 packages: write must appear exactly once, in {PUBLISH_FILE}:{PUBLISH_JOB} (found {pw})")
    else:
        rep.ok(f"W2 packages: write appears only in {PUBLISH_FILE}:{PUBLISH_JOB}")
    print(f"workflow_policy_check: {len(rep.oks)} passed, {len(rep.failures)} failed")
    return 1 if rep.failures else 0


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    here = os.path.dirname(os.path.abspath(__file__))
    ap.add_argument("--dir", default=os.path.join(here, "..", "workflows"))
    args = ap.parse_args(argv)
    return run(os.path.abspath(args.dir))


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
