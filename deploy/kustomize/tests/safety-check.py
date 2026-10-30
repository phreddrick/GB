#!/usr/bin/env python3
"""Offline safety check for the Phase 2B deployment artifacts.

Asserts three Phase 2B invariants (scope reduction of 2026-10-30, handoff
section 12.9; GB2B-07 correction, section 12.10):

  I.   no executable recovery automation exists in the repository's Phase 2B
       artifacts (S1, S2);
  II.  the two operator planning checklists contain no runnable recovery
       instructions (S4), and no other Phase 2B operational document carries a
       destructive or shell-evaluated command inside a code block (S3);
  III. the offline validation driver run.sh executes only approved validation
       targets (S5).

Rules (each reported as PASS/FAIL with its id):

  S1  deploy/runbooks does not exist, and every script under deploy/ is one of
      the four allow-listed offline validation files. A script is any file with
      a script extension, an executable bit, or a "#!" first line, regardless
      of its name.
  S2  no reference to the retired helper library anywhere in the Phase 2B
      documents and artifacts (names: gb-runbook-lib, test_runbook_lib,
      deploy/runbooks, rb_* functions, RB_* variables). The handoff may name
      them as history, but never inside a code block or as a command.
  S3  no code block in any Phase 2B operational document contains a
      destructive command (kubectl delete / scale, --force, --grace-period,
      --replicas, --cascade, rke2-killall, systemctl stop/mask/disable/kill,
      kill, rm -rf, argocd app delete, finalizer edits) or shell evaluation of
      Kubernetes API output ($(kubectl ...), VAR=$(kubectl ...),
      source/dot-sourcing, eval, set -a, export VAR=$(...)). Code blocks are
      fenced blocks of three or more backticks or tildes of any length
      (CommonMark closing rules; an unterminated fence runs to the end of the
      file), and indented blocks (lines starting with four spaces or a tab
      outside a fence). Read-only commands in these blocks are permitted.
  S4  the two operator planning checklists (dev-volume-recovery.md and
      storage-and-network-validation.md) carry the banner NOT APPROVED FOR
      EXECUTION; their only code blocks are terminated fences whose info string
      is exactly "yaml" (manifest data); they contain no indented block, no
      HTML <pre>, <code>, <script> or comment; and none of the S3 patterns
      appears anywhere in them, prose included. Any other code construct is
      rejected rather than classified.
  S5  no Job or CronJob manifest under deploy/kustomize, and the offline
      validation runner deploy/kustomize/tests/run.sh is byte-for-byte the
      reviewed one: a regular file (not a symlink) whose SHA-256 equals
      APPROVED_RUN_SH_SHA256. Any difference, however harmless it looks, is
      rejected. The approved digest is never derived from the file being
      checked: changing run.sh means reviewing the changed script and every
      target it invokes, then updating the constant by hand in the same change
      (GB2B-07, after two shell-parsing attempts were bypassed).

What this check is not: it is a narrow structural guard over this repository's
files. It does not understand shell or Markdown in general, does not inspect
files outside the listed scope, does not follow links or images, and says
nothing about what an operator types at a terminal. Pinning run.sh verifies
the runner's bytes, not the safety of the programs it invokes: validate.py,
mutations.py and this file are reviewed through their own checks and the
cumulative commit verification, and S1 keeps any other script out of deploy/.
Its patterns are listed above and in the tests README; anything not listed is
not checked.

Usage: safety-check.py [--root DIR] [--selftest]
  --selftest copies the in-scope files to a temporary directory, plants one
  violation per case and requires the check to fail for it for the intended
  rule, requires the must-pass cases (read-only code blocks, yaml data fences)
  to pass, and requires the clean copy to pass with the expected PASS lines; a
  case whose fixture document is absent from the tree is reported as skipped.
  Nothing outside the temporary directory is touched.
Exit status 0 when every rule passes, 1 otherwise. Standard library only.
"""
import argparse
import hashlib
import os
import re
import shutil
import stat
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))

TESTS_DIR = "deploy/kustomize/tests"
KUSTOMIZE_DIR = "deploy/kustomize"
ALLOWED_SCRIPTS = {
    f"{TESTS_DIR}/run.sh",
    f"{TESTS_DIR}/validate.py",
    f"{TESTS_DIR}/mutations.py",
    f"{TESTS_DIR}/safety-check.py",
}
# SHA-256 of the reviewed deploy/kustomize/tests/run.sh (reviewed 2026-10-30:
# it runs validate.py, mutations.py, safety-check.py with --selftest, and
# `go run github.com/yannh/kubeconform/cmd/kubeconform@v0.8.0`; nothing else).
# Update only after reviewing a changed runner and its targets; never compute
# it from the candidate under test.
APPROVED_RUN_SH_SHA256 = "9de4f58606298cac2f21d5180c098e1374cb1693aaac0da0b77f34660f09e46a"

CHECKLISTS = [
    "docs/operations/dev-volume-recovery.md",
    "docs/operations/storage-and-network-validation.md",
]
OPERATIONAL_DOCS = CHECKLISTS + [
    "docs/operations/bootstrap.md",
    "docs/operations/deploy.md",
    "docs/architecture/adr/0005-kustomize-and-gitops-ownership.md",
    "docs/architecture/adr/0006-image-registry-and-digest-pinning.md",
    "docs/architecture/adr/0007-node-placement-and-storage.md",
    "docs/architecture/adr/0008-explicit-universe-bootstrap.md",
    "docs/testing/phase2b-handoff.md",
    "deploy/docker/README.md",
    "deploy/kustomize/README.md",
    "deploy/kustomize/tests/README.md",
    "deploy/kustomize/schemas/README.md",
]
HANDOFF = "docs/testing/phase2b-handoff.md"
BANNER = "NOT APPROVED FOR EXECUTION"
# Where retired names are searched (S2): the Phase 2B scope, never source/ or go/.
SCAN_DIRS = ["deploy", "docs"]
SCAN_FILES = ["CLAUDE.md", "KUBERNETES.md", "README.md", "Dockerfile", "k8s-deployment.yaml",
              "go/README.md", "go/Makefile"]
SCAN_SKIP_PREFIX = ("docs/archive/",)
TEXT_SUFFIXES = (".md", ".sh", ".py", ".yaml", ".yml", ".json", ".txt", ".toml", ".cfg", "")
SCRIPT_SUFFIXES = (".sh", ".bash", ".zsh", ".ksh", ".py", ".pl", ".rb", ".js")

RETIRED = re.compile(r"gb-runbook-lib|test_runbook_lib|deploy/runbooks|\brb_[a-z][a-z0-9_]*|\bRB_[A-Z][A-Z_]*")
RETIRED_INVOKE = re.compile(r"(^|[\s;&|(])(source|\.|bash|sh|python3?)\s+\S*(gb-runbook-lib|test_runbook_lib|deploy/runbooks)")
DESTRUCTIVE = [
    (re.compile(r"\bkubectl\b[^\n`]*\bdelete\b"), "kubectl delete"),
    (re.compile(r"\bkubectl\b[^\n`]*\bscale\b"), "kubectl scale"),
    (re.compile(r"\bdelete\s+(pod|pods|pvc|persistentvolumeclaims?|pv|persistentvolumes?|namespace|namespaces|ns|volumes?\.longhorn\.io|statefulsets?|sts)\b"), "delete <kind>"),
    (re.compile(r"--force\b"), "--force"),
    (re.compile(r"--grace-period"), "--grace-period"),
    (re.compile(r"--replicas"), "--replicas"),
    (re.compile(r"--cascade"), "--cascade"),
    (re.compile(r"rke2-killall"), "rke2-killall"),
    (re.compile(r"\bsystemctl\s+(stop|mask|disable|kill)\b"), "systemctl stop/mask/disable/kill"),
    (re.compile(r"(^|[\s;&|(])kill(all)?\s"), "kill"),
    (re.compile(r"\brm\s+-[a-zA-Z]*[rf][a-zA-Z]*\s"), "rm -rf"),
    (re.compile(r"\bargocd\s+app\s+delete\b"), "argocd app delete"),
    (re.compile(r"\bpatch\b[^\n`]*finalizers|finalizers[^\n`]*\bpatch\b"), "finalizer edit"),
]
SHELL_EVAL = [
    (re.compile(r"\$\(\s*(kubectl|argocd|jq|helm|gbctl)\b"), "$(kubectl ...) command substitution"),
    (re.compile(r"""(^|[\s;&|(])(source|\.)\s+(["'$./~]|\S+\.(sh|env|bash)\b)""", re.M), "source / dot-sourcing"),
    (re.compile(r"\beval\b"), "eval"),
    (re.compile(r"\bset\s+-a\b"), "set -a"),
    (re.compile(r"\bexport\s+\w+=\$\("), "export VAR=$(...)"),
    (re.compile(r"\b\w+=\$\(\s*(kubectl|argocd|jq|helm|gbctl)\b"), "VAR=$(kubectl ...) assignment"),
]
HTML_CODE = re.compile(r"<\s*(pre|code|script)\b|<!--", re.I)

FENCE_OPEN = re.compile(r"^ {0,3}(`{3,}|~{3,})(.*)$")
INDENTED = re.compile(r"^( {4,}|\t)")


# ---------------------------------------------------------------- Markdown ---

def code_blocks(text):
    """Yield every code block as (kind, info, body, start_line, terminated).

    kind is "fence" or "indented". Fences follow CommonMark: an opening run of
    three or more backticks or tildes (up to three spaces of indentation), an
    info string, and a closing run of the same character at least as long as
    the opening one with nothing else on the line; a fence that never closes
    runs to the end of the file (terminated=False). Outside fences, a maximal
    run of lines starting with four spaces or a tab (blank lines allowed
    inside) is an indented block.
    """
    lines = text.split("\n")
    i, n = 0, len(lines)
    while i < n:
        m = FENCE_OPEN.match(lines[i])
        if m:
            marker, info = m.group(1), m.group(2).strip()
            ch, length = marker[0], len(marker)
            start = i + 1
            body, j, terminated = [], i + 1, False
            close = re.compile(r"^ {0,3}" + re.escape(ch) + "{" + str(length) + r",}[ \t]*$")
            while j < n:
                if close.match(lines[j]):
                    terminated = True
                    break
                body.append(lines[j])
                j += 1
            yield "fence", info, "\n".join(body), start, terminated
            i = j + 1 if terminated else n
            continue
        if INDENTED.match(lines[i]) and lines[i].strip():
            start = i + 1
            body, j = [], i
            while j < n and (INDENTED.match(lines[j]) or not lines[j].strip()):
                body.append(lines[j])
                j += 1
            while body and not body[-1].strip():
                body.pop()
            yield "indented", "", "\n".join(body), start, True
            i = j
            continue
        i += 1


def read(path):
    with open(path, encoding="utf-8", errors="replace") as fh:
        return fh.read()


def is_text(path):
    return os.path.splitext(path)[1] in TEXT_SUFFIXES and os.path.getsize(path) < 4_000_000


def is_script(path):
    if path.endswith(SCRIPT_SUFFIXES):
        return True
    if os.stat(path).st_mode & (stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH):
        return True
    try:
        with open(path, "rb") as fh:
            return fh.read(2) == b"#!"
    except OSError:
        return True


def scan_files(root):
    out = []
    for d in SCAN_DIRS:
        base = os.path.join(root, d)
        for dirpath, dirnames, filenames in os.walk(base):
            dirnames[:] = sorted(x for x in dirnames if x != ".git")
            for f in sorted(filenames):
                rel = os.path.relpath(os.path.join(dirpath, f), root)
                if rel.startswith(SCAN_SKIP_PREFIX):
                    continue
                out.append(rel)
    for f in SCAN_FILES:
        if os.path.isfile(os.path.join(root, f)):
            out.append(f)
    return out


class Report:
    def __init__(self):
        self.failures = []
        self.passes = []

    def fail(self, rule, msg):
        self.failures.append(f"FAIL {rule}: {msg}")

    def ok(self, rule, msg):
        self.passes.append(f"PASS {rule}: {msg}")

    def failed(self, rule):
        return any(f.startswith(f"FAIL {rule}:") for f in self.failures)


# ------------------------------------------------------------------- rules ---

def rule_s1(root, rep):
    if os.path.exists(os.path.join(root, "deploy", "runbooks")):
        rep.fail("S1", "deploy/runbooks exists (retired executable recovery library)")
    bad = []
    for dirpath, dirnames, filenames in os.walk(os.path.join(root, "deploy")):
        dirnames.sort()
        for f in sorted(filenames):
            p = os.path.join(dirpath, f)
            rel = os.path.relpath(p, root)
            if is_script(p) and rel not in ALLOWED_SCRIPTS:
                bad.append(rel)
    if bad:
        rep.fail("S1", "scripts under deploy/ outside the allow-list (by extension, executable bit or shebang): " + ", ".join(bad))
    if not rep.failed("S1"):
        rep.ok("S1", f"no deploy/runbooks; scripts under deploy/ are exactly the allow-listed {len(ALLOWED_SCRIPTS)}")


def rule_s2(root, rep):
    hits = []
    for rel in scan_files(root):
        p = os.path.join(root, rel)
        if not is_text(p):
            continue
        if rel == f"{TESTS_DIR}/safety-check.py":
            continue  # this file names the retired patterns by design
        text = read(p)
        if rel == HANDOFF:
            for kind, info, body, ln, term in code_blocks(text):
                if RETIRED.search(body):
                    hits.append(f"{rel}: {kind} block at line {ln} names the retired library")
            for i, line in enumerate(text.splitlines(), 1):
                if RETIRED_INVOKE.search(line):
                    hits.append(f"{rel}:{i}: invokes the retired library")
            continue
        for i, line in enumerate(text.splitlines(), 1):
            m = RETIRED.search(line)
            if m:
                hits.append(f"{rel}:{i}: {m.group(0)!r}")
    if hits:
        rep.fail("S2", "retired library referenced: " + "; ".join(hits[:20]) + (" ..." if len(hits) > 20 else ""))
    else:
        rep.ok("S2", "no reference to the retired helper library outside the handoff's history prose")


def patterns_in(text, where, hits):
    for i, line in enumerate(text.splitlines(), 1):
        for rx, name in DESTRUCTIVE + SHELL_EVAL:
            if rx.search(line):
                hits.append(f"{where}:{i}: {name}")


def rule_s3(root, rep):
    hits, seen, blocks = [], 0, 0
    for rel in OPERATIONAL_DOCS:
        p = os.path.join(root, rel)
        if not os.path.isfile(p):
            continue
        seen += 1
        for kind, info, body, ln, term in code_blocks(read(p)):
            blocks += 1
            patterns_in(body, f"{rel} {kind}@{ln}" + ("" if term else " (unterminated)"), hits)
    if hits:
        rep.fail("S3", "destructive command or shell evaluation inside a code block: " + "; ".join(hits[:20]))
    else:
        rep.ok("S3", f"no destructive command or shell evaluation in any of the {blocks} code blocks (fenced of any length, or indented) of {seen} operational documents")


def rule_s4(root, rep):
    present = 0
    for rel in CHECKLISTS:
        p = os.path.join(root, rel)
        if not os.path.isfile(p):
            continue
        present += 1
        text = read(p)
        if BANNER not in text:
            rep.fail("S4", f"{rel}: banner '{BANNER}' missing")
        for kind, info, body, ln, term in code_blocks(text):
            if kind == "indented":
                rep.fail("S4", f"{rel}: indented code block at line {ln}; a planning checklist carries no code block other than yaml data fences")
            elif not term:
                rep.fail("S4", f"{rel}: unterminated fence at line {ln}")
            elif info != "yaml":
                rep.fail("S4", f"{rel}: fence at line {ln} with info string {info!r}; only terminated 'yaml' data fences are permitted in a planning checklist")
        for i, line in enumerate(text.splitlines(), 1):
            if HTML_CODE.search(line):
                rep.fail("S4", f"{rel}:{i}: HTML code element or comment; not classifiable, rejected")
        hits = []
        patterns_in(text, rel, hits)
        if hits:
            rep.fail("S4", "destructive command or shell evaluation in a checklist (prose included): " + "; ".join(hits[:20]))
    if present and not rep.failed("S4"):
        rep.ok("S4", f"{present} planning checklist(s) carry the banner, contain only yaml data fences, and have no destructive or shell-evaluated step anywhere")
    elif not present:
        rep.ok("S4", "no planning checklist present (nothing to check)")


# ------------------------------------------------------------ run.sh (S5) ----

def runner_status(root):
    """Classify the offline validation runner: ("ok", digest) or ("<reason>", digest-or-None)."""
    path = os.path.join(root, TESTS_DIR, "run.sh")
    if os.path.islink(path):
        return "run.sh is a symbolic link; the approved runner must be a regular file", None
    if not os.path.lexists(path):
        return "run.sh is missing; the approved runner must be present", None
    if not stat.S_ISREG(os.lstat(path).st_mode):
        return "run.sh is not a regular file", None
    with open(path, "rb") as fh:
        digest = hashlib.sha256(fh.read()).hexdigest()
    if digest != APPROVED_RUN_SH_SHA256:
        return (f"run.sh content differs from the reviewed runner (sha256 {digest}, approved {APPROVED_RUN_SH_SHA256}); "
                "every change, however harmless it looks, is rejected until the changed script and its targets are reviewed "
                "and APPROVED_RUN_SH_SHA256 is updated by hand"), digest
    return "ok", digest


def rule_s5(root, rep):
    bad = []
    kdir = os.path.join(root, KUSTOMIZE_DIR)
    for dirpath, dirnames, filenames in os.walk(kdir):
        dirnames.sort()
        for f in sorted(filenames):
            if f.endswith((".yaml", ".yml")):
                rel = os.path.relpath(os.path.join(dirpath, f), root)
                for i, line in enumerate(read(os.path.join(dirpath, f)).splitlines(), 1):
                    if re.match(r"^\s*kind:\s*(Job|CronJob)\s*$", line):
                        bad.append(f"{rel}:{i}: {line.strip()}")
    status, digest = runner_status(root)
    if status != "ok":
        bad.append(status)
    if bad:
        rep.fail("S5", "; ".join(bad))
    else:
        rep.ok("S5", f"no Job/CronJob manifest; run.sh is a regular file byte-identical to the reviewed runner (sha256 {digest[:16]}...)")


def run_rules(root):
    rep = Report()
    for fn in (rule_s1, rule_s2, rule_s3, rule_s4, rule_s5):
        fn(root, rep)
    return rep


# ---------------------------------------------------------------- selftest ---

def selftest(root):
    """Plant violations in a temporary copy and require the intended FAIL for each;
    require the must-pass cases and the clean copy to pass."""
    tmp = tempfile.mkdtemp(prefix="gb-safety-selftest-")
    try:
        for d in ("deploy", "docs/operations", "docs/architecture/adr", "docs/testing"):
            src = os.path.join(root, d)
            if os.path.isdir(src):
                shutil.copytree(src, os.path.join(tmp, d), dirs_exist_ok=True)
        for f in SCAN_FILES:
            src = os.path.join(root, f)
            if os.path.isfile(src):
                os.makedirs(os.path.dirname(os.path.join(tmp, f)), exist_ok=True)
                shutil.copy2(src, os.path.join(tmp, f))
        base = run_rules(tmp)
        if base.failures:
            print("SELFTEST FAIL: the clean copy does not pass:\n  " + "\n  ".join(base.failures))
            return 1
        both_checklists = all(os.path.isfile(os.path.join(tmp, c)) for c in CHECKLISTS)
        expected_pass = ["PASS S1:", "PASS S2:", "PASS S3:", "PASS S5:"] + (["PASS S4: 2 planning checklist(s)"] if both_checklists else [])
        for prefix in expected_pass:
            if not any(p.startswith(prefix) for p in base.passes):
                print(f"SELFTEST FAIL: clean copy lacks the expected line {prefix!r}")
                return 1
        print("  selftest clean copy: approved scripts present, retired automation absent"
              + (", both planning checklists pass" if both_checklists else ", planning checklists not yet present"))

        class SkipCase(Exception):
            pass

        def need(r, rel):
            if not os.path.isfile(os.path.join(r, rel)):
                raise SkipCase(f"fixture {rel} absent in this tree")

        def plant(name, rule, mutate, must_pass=False):
            snap = tempfile.mkdtemp(prefix="gb-safety-case-")
            try:
                shutil.copytree(tmp, snap, dirs_exist_ok=True)
                try:
                    mutate(snap)
                except SkipCase as why:
                    print(f"  selftest {name:<44} expected {rule:<9} skipped ({why})")
                    return None
                rep = run_rules(snap)
            finally:
                shutil.rmtree(snap, ignore_errors=True)
            if must_pass:
                ok = not rep.failures
                print(f"  selftest {name:<44} expected PASS      {'ok' if ok else 'REJECTED: ' + '; '.join(rep.failures)[:120]}")
                return ok
            hit = [f for f in rep.failures if f.startswith(f"FAIL {rule}")]
            print(f"  selftest {name:<44} expected {rule:<9} {'ok' if hit else 'MISSED'}")
            return bool(hit)

        def append(rel, text):
            def _m(r):
                need(r, rel)
                with open(os.path.join(r, rel), "a", encoding="utf-8") as fh:
                    fh.write("\n" + text + "\n")
            return _m

        def write(rel, text, mode=None):
            def _m(r):
                p = os.path.join(r, rel)
                os.makedirs(os.path.dirname(p), exist_ok=True)
                with open(p, "w", encoding="utf-8") as fh:
                    fh.write(text)
                if mode is not None:
                    os.chmod(p, mode)
            return _m

        def replace(rel, old, new):
            def _m(r):
                need(r, rel)
                p = os.path.join(r, rel)
                t = read(p)
                assert old in t, f"selftest fixture: {old!r} not in {rel}"
                with open(p, "w", encoding="utf-8") as fh:
                    fh.write(t.replace(old, new, 1))
            return _m

        DEPLOY, BOOT, ADR7, RUNSH = "docs/operations/deploy.md", "docs/operations/bootstrap.md", "docs/architecture/adr/0007-node-placement-and-storage.md", f"{TESTS_DIR}/run.sh"
        DEL = "kubectl -n gb-dev delete pvc world-gb-engine-0"
        cases = [
            # S1: executable recovery automation present
            ("runbooks dir reappears", "S1", write("deploy/runbooks/gb-runbook-lib.sh", "#!/bin/bash\necho hi\n")),
            ("stray .sh under deploy", "S1", write("deploy/kustomize/cleanup.sh", "#!/bin/bash\n")),
            ("shebang file with .txt name, no exec bit", "S1", write(f"{TESTS_DIR}/recover.txt", "#!/bin/sh\n" + DEL + "\n", 0o644)),
            ("executable bit on a .json", "S1", write(f"{TESTS_DIR}/data.json", "{}\n", 0o755)),
            # S2: retired names
            ("rb_ name in a runbook", "S2", append(DEPLOY, "Run `rb_fence_check` first.")),
            ("library path in an ADR", "S2", append(ADR7, "See deploy/runbooks/gb-runbook-lib.sh.")),
            ("handoff invokes the library", "S2", append(HANDOFF, "```sh\nsource deploy/runbooks/gb-runbook-lib.sh\n```")),
            # S3: code blocks in operational documents (Codex GB2B-07 reproductions included)
            ("kubectl delete in a 3-backtick fence", "S3", append(DEPLOY, "```sh\n" + DEL + "\n```")),
            ("kubectl delete in a 4-backtick fence", "S3", append(DEPLOY, "````sh\n" + DEL + "\n````")),
            ("kubectl delete in a 4-tilde fence", "S3", append(DEPLOY, "~~~~bash\n" + DEL + "\n~~~~")),
            ("kubectl delete in a 6-backtick fence, no info", "S3", append(DEPLOY, "``````\n" + DEL + "\n``````")),
            ("kubectl delete in an unterminated fence", "S3", append(DEPLOY, "```sh\n" + DEL)),
            ("indented kubectl delete after a paragraph", "S3", append(DEPLOY, "Recovery:\n    " + DEL)),
            ("indented forced pod deletion", "S3", append(DEPLOY, "Recovery:\n    kubectl -n gb-dev delete pod gb-engine-0 --force --grace-period=0")),
            ("tab-indented kubectl delete", "S3", append(DEPLOY, "Recovery:\n\n\t" + DEL)),
            ("force flag in a fenced block", "S3", append(BOOT, "```sh\nkubectl delete pod x --grace-period=0 --force\n```")),
            ("API output assigned in shell", "S3", append(DEPLOY, "```sh\nPV=$(kubectl get pvc w -o jsonpath='{.spec.volumeName}')\n```")),
            ("evidence file sourced", "S3", append(BOOT, "```sh\nset -a; . ./identity.env; set +a\n```")),
            # S4: planning checklists
            ("checklist banner removed", "S4", replace(CHECKLISTS[0], BANNER, "draft")),
            ("3-backtick sh fence in a checklist", "S4", append(CHECKLISTS[1], "```sh\nkubectl -n rwop-test get pod\n```")),
            ("4-backtick sh fence in a checklist", "S4", append(CHECKLISTS[0], "````sh\nkubectl -n gb-dev get pod\n````")),
            ("5-tilde fence in a checklist", "S4", append(CHECKLISTS[0], "~~~~~\nkubectl -n gb-dev get pod\n~~~~~")),
            ("fence without info string in a checklist", "S4", append(CHECKLISTS[1], "```\nkubectl -n rwop-test get pvc\n```")),
            ("unterminated yaml fence in a checklist", "S4", append(CHECKLISTS[0], "```yaml\nkind: Pod")),
            ("indented read-only command in a checklist", "S4", append(CHECKLISTS[0], "Then:\n    kubectl -n gb-dev get pod gb-engine-0")),
            ("HTML pre element in a checklist", "S4", append(CHECKLISTS[1], "<pre>kubectl -n rwop-test get pod</pre>")),
            ("HTML comment in a checklist", "S4", append(CHECKLISTS[0], "<!-- kubectl -n gb-dev get pod -->")),
            ("scale command in checklist prose", "S4", append(CHECKLISTS[0], "Then run `kubectl -n gb-dev scale statefulset gb-engine --replicas=0`.")),
            ("kill in checklist prose", "S4", append(CHECKLISTS[0], "Then kill -9 the shim.")),
            # S5: the runner is pinned by content (Codex GB2B-07 reproductions included)
            ("Job manifest under kustomize", "S5", write("deploy/kustomize/base/server/recover-job.yaml", "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: x\n")),
            ("run.sh conditional bash bypass (Codex)", "S5",
             lambda r: (write(f"{TESTS_DIR}/recover.txt", DEL + "\n", 0o644)(r), append(RUNSH, 'if bash "$HERE/recover.txt"; then :; fi')(r))),
            ("run.sh conditional with unresolved variable", "S5",
             lambda r: (write(f"{TESTS_DIR}/recover.txt", DEL + "\n", 0o644)(r), append(RUNSH, 'if bash "${RECOVER_SCRIPT:-$HERE/recover.txt}"; then :; fi')(r))),
            ("run.sh bash of a .txt script (Codex indirect-script)", "S5",
             lambda r: (write(f"{TESTS_DIR}/recover.txt", DEL + "\n", 0o644)(r), append(RUNSH, 'bash "$HERE/recover.txt"')(r))),
            ("run.sh sh of a script hidden in a quoted $(...)", "S5",
             lambda r: (write(f"{TESTS_DIR}/recover.txt", DEL + "\n", 0o644)(r), append(RUNSH, 'echo "result: $(sh "$HERE/recover.txt")"')(r))),
            ("run.sh calls outside kustomize", "S5", append(RUNSH, 'python3 "$KROOT/../runbooks/tests/test_runbook_lib.py"')),
            ("run.sh python3 -c inline program", "S5", append(RUNSH, "python3 -c 'import os'")),
            ("run.sh go run of another module", "S5", append(RUNSH, "go run github.com/example/tool@v1.0.0")),
            ("run.sh bare kubectl", "S5", append(RUNSH, "kubectl get nodes")),
            ("run.sh one added comment line (looks safe)", "S5", append(RUNSH, "# harmless comment")),
            ("run.sh one trailing space (single byte)", "S5", lambda r: open(os.path.join(r, RUNSH), "a", encoding="utf-8").write(" ")),
            ("run.sh one byte removed", "S5", lambda r: (lambda p: open(p, "wb").write(open(p, "rb").read()[:-1]))(os.path.join(r, RUNSH))),
            ("run.sh replaced by a symlink to identical content", "S5",
             lambda r: (write("outside/run.sh", read(os.path.join(r, RUNSH)))(r), os.remove(os.path.join(r, RUNSH)), os.symlink(os.path.join(r, "outside/run.sh"), os.path.join(r, RUNSH)))),
            ("run.sh missing", "S5", lambda r: os.remove(os.path.join(r, RUNSH))),
            # must-pass: legitimate constructs stay accepted
            ("read-only sh fence in deploy.md (Codex read-only-doc)", "PASS", append(DEPLOY, "```sh\nkubectl -n gb-dev get pods\n```")),
            ("4-backtick yaml data fence in a checklist", "PASS", append(CHECKLISTS[1], "````yaml\nkind: ConfigMap\n````")),
            ("read-only command in a 4-backtick fence in deploy.md", "PASS", append(DEPLOY, "````sh\nkubectl -n gb-dev get pod -o wide\n````")),
            ("unchanged approved runner", "PASS", lambda r: None),
        ]
        results = [(n, plant(n, r, m, must_pass=(r == "PASS"))) for n, r, m in cases]
        missed = [n for n, ok in results if ok is False]
        skipped = [n for n, ok in results if ok is None]
        if missed:
            print("SELFTEST FAIL: " + ", ".join(missed))
            return 1
        detected = sum(1 for (n, ok), (_, r, _m) in zip(results, cases) if ok and r != "PASS")
        passed = sum(1 for (n, ok), (_, r, _m) in zip(results, cases) if ok and r == "PASS")
        print(f"SELFTEST OK: {detected} planted violations detected for the intended rule, {passed} must-pass cases accepted"
              + (f", {len(skipped)} skipped (fixture document absent)" if skipped else "")
              + ", clean copy passes")
        return 0
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--root", default=DEFAULT_ROOT)
    ap.add_argument("--selftest", action="store_true")
    a = ap.parse_args()
    root = os.path.abspath(a.root)
    if a.selftest:
        return selftest(root)
    rep = run_rules(root)
    for line in rep.passes + rep.failures:
        print(line)
    print(f"safety-check: {len(rep.passes)} passed, {len(rep.failures)} failed")
    return 1 if rep.failures else 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:  # never a traceback as a verdict
        print(f"FAIL safety-check internal error: {type(exc).__name__}: {exc}")
        sys.exit(1)
