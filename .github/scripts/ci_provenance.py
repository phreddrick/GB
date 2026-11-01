#!/usr/bin/env python3
"""Authenticate the CI evidence a publication relies on (GB2C-02).

publish-preflight.sh must not accept "some successful check runs with the
right names": a check run from an unrelated workflow, a feature branch, a
pull request, another repository or another commit carries the same names.
This module resolves ONE workflow run of the approved ci workflow through the
workflow-runs API and then requires every mandatory job to belong to that run
and that attempt.

    ci_provenance.py select-run  --runs runs.json --repository phreddrick/GB
                                 --workflow-path .github/workflows/ci.yml
                                 --sha <40 hex> --branch master --event push
                                 --out selected.json
    ci_provenance.py verify-jobs --jobs jobs.json --run-id N --run-attempt N
                                 --sha <40 hex> --required-checks names.txt

Selection policy (deterministic, fail closed):
  1. runs.json is the response of GET /repos/{repo}/actions/workflows/ci.yml/
     runs?head_sha=<sha>&per_page=100. It must be an object with an integer
     total_count equal to the number of entries in workflow_runs; anything
     else (an error body, text, a truncated page) is a malformed response.
  2. A run is a *candidate* only if repository.full_name AND
     head_repository.full_name equal the repository, path equals the approved
     workflow path, head_sha equals the commit, head_branch equals the branch
     and event equals the event. Every other run is listed as ignored with
     the field that excluded it.
  3. A candidate is *eligible* only if status is "completed" and conclusion
     is "success". Newer is not better: an in-progress, cancelled or failed
     newer run never outranks anything, and never qualifies.
  4. Exactly one eligible run is required. None: refused, with every
     candidate and its status printed. More than one: refused as ambiguous
     (the owner deletes the superseded run in the Actions UI, or publishes a
     later commit); nothing picks "the latest" silently.
  5. The run's run_attempt (its latest attempt) is the attempt whose jobs are
     fetched: GET /repos/{repo}/actions/runs/{id}/attempts/{n}/jobs.
  6. Every job in that response must carry run_id == id, run_attempt == n and
     head_sha == sha (no mixture of runs or attempts). Each required job name
     must appear exactly once with status "completed" and conclusion
     "success"; "skipped", "neutral", "cancelled", "failure", a missing job
     or a duplicate name is a refusal. A run completed with "Re-run failed
     jobs" therefore does not qualify when carried-over jobs report an
     earlier attempt; use "Re-run all jobs".

Exit status: 0 accepted, 1 refused, 2 malformed or unreadable response.
Standard library only; never calls the network (the shell fetches with gh).
"""
from __future__ import annotations

import argparse
import json
import re
import sys

SHA_RE = re.compile(r"^[0-9a-f]{40}$")


class Malformed(Exception):
    pass


def load_json(path: str, what: str) -> dict:
    try:
        with open(path, "rb") as f:
            raw = f.read()
    except OSError as e:
        raise Malformed(f"{what}: cannot read {path}: {e}") from e
    if not raw.strip():
        raise Malformed(f"{what}: {path} is empty")
    try:
        data = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as e:
        raise Malformed(f"{what}: {path} is not JSON ({e}); not an API response") from e
    if not isinstance(data, dict):
        raise Malformed(f"{what}: top level is {type(data).__name__}, not an object")
    if "message" in data and ("workflow_runs" not in data and "jobs" not in data):
        raise Malformed(f"{what}: API error: {data.get('message')!r}")
    return data


def _list(data: dict, key: str, what: str) -> list:
    items = data.get(key)
    total = data.get("total_count")
    if not isinstance(items, list):
        raise Malformed(f"{what}: {key} is {type(items).__name__}, not a list")
    if not isinstance(total, int) or isinstance(total, bool):
        raise Malformed(f"{what}: total_count is {total!r}, not an integer")
    if total != len(items):
        raise Malformed(f"{what}: total_count {total} but {len(items)} {key} entries were returned (paginated or truncated response)")
    for i, item in enumerate(items):
        if not isinstance(item, dict):
            raise Malformed(f"{what}: {key}[{i}] is not an object")
    return items


def _int(obj: dict, key: str, what: str) -> int:
    v = obj.get(key)
    if not isinstance(v, int) or isinstance(v, bool):
        raise Malformed(f"{what}: {key} is {v!r}, not an integer")
    return v


def _nested_name(run: dict, key: str) -> str:
    v = run.get(key)
    if isinstance(v, dict) and isinstance(v.get("full_name"), str):
        return v["full_name"]
    return ""


def select_run(runs: dict, *, repository: str, workflow_path: str, sha: str, branch: str, event: str) -> tuple[dict | None, list[str]]:
    """Return (selected run or None, lines to print)."""
    lines: list[str] = []
    items = _list(runs, "workflow_runs", "workflow-runs response")
    repo_cf = repository.casefold()
    candidates: list[dict] = []
    for run in items:
        rid = _int(run, "id", "workflow run")
        why = None
        if _nested_name(run, "repository").casefold() != repo_cf:
            why = f"repository {_nested_name(run, 'repository')!r}"
        elif _nested_name(run, "head_repository").casefold() != repo_cf:
            why = f"head_repository {_nested_name(run, 'head_repository')!r} (fork)"
        elif run.get("path") != workflow_path:
            why = f"workflow path {run.get('path')!r}"
        elif run.get("head_sha") != sha:
            why = f"head_sha {run.get('head_sha')!r}"
        elif run.get("head_branch") != branch:
            why = f"head_branch {run.get('head_branch')!r}"
        elif run.get("event") != event:
            why = f"event {run.get('event')!r}"
        if why:
            lines.append(f"ignored run {rid}: {why} is not the required identity")
            continue
        _int(run, "run_attempt", f"workflow run {rid}")
        candidates.append(run)
    eligible = [r for r in candidates if r.get("status") == "completed" and r.get("conclusion") == "success"]
    for r in candidates:
        mark = "ELIGIBLE" if r in eligible else "not eligible"
        lines.append(f"candidate run {r['id']} attempt {r['run_attempt']}: status {r.get('status')!r} conclusion {r.get('conclusion')!r} -> {mark} ({r.get('html_url', '')})")
    if not candidates:
        lines.append(f"FAIL no run of {workflow_path} for {sha} on {branch} ({event}) in {repository}; nothing can vouch for this commit")
        return None, lines
    if not eligible:
        lines.append(f"FAIL no completed, successful run of {workflow_path} for {sha}; an unfinished, cancelled or failed run is not evidence")
        return None, lines
    if len(eligible) > 1:
        ids = ", ".join(str(r["id"]) for r in eligible)
        lines.append(f"FAIL ambiguous: {len(eligible)} eligible runs ({ids}); the newest is not chosen silently. Delete the superseded run or publish a later commit")
        return None, lines
    run = eligible[0]
    lines.append(f"PASS selected run {run['id']} attempt {run['run_attempt']} of {workflow_path} ({event} on {branch}, {sha}) in {repository}")
    return run, lines


def verify_jobs(jobs: dict, *, run_id: int, run_attempt: int, sha: str, required: list[str]) -> tuple[bool, list[str]]:
    lines: list[str] = []
    items = _list(jobs, "jobs", "jobs response")
    ok = True
    by_name: dict[str, list[dict]] = {}
    for job in items:
        jid = _int(job, "id", "job")
        name = job.get("name")
        if not isinstance(name, str) or not name:
            raise Malformed(f"job {jid}: name is {name!r}")
        if _int(job, "run_id", f"job {jid}") != run_id:
            lines.append(f"FAIL job {jid} '{name}' belongs to run {job['run_id']}, not {run_id} (mixed runs)")
            ok = False
        if _int(job, "run_attempt", f"job {jid}") != run_attempt:
            lines.append(f"FAIL job {jid} '{name}' is attempt {job['run_attempt']}, not {run_attempt} (mixed attempts; use 'Re-run all jobs')")
            ok = False
        if job.get("head_sha") != sha:
            lines.append(f"FAIL job {jid} '{name}' ran for {job.get('head_sha')!r}, not {sha}")
            ok = False
        by_name.setdefault(name, []).append(job)
    for name in required:
        found = by_name.get(name, [])
        if not found:
            lines.append(f"FAIL required job '{name}' is missing from run {run_id} attempt {run_attempt}")
            ok = False
            continue
        if len(found) > 1:
            lines.append(f"FAIL required job '{name}' appears {len(found)} times in run {run_id} attempt {run_attempt}; ambiguous")
            ok = False
            continue
        job = found[0]
        if job.get("status") != "completed" or job.get("conclusion") != "success":
            lines.append(f"FAIL required job '{name}' is {job.get('status')!r}/{job.get('conclusion')!r}, not completed/success")
            ok = False
            continue
        lines.append(f"PASS required job '{name}' completed with success in run {run_id} attempt {run_attempt}")
    extra = sorted(set(by_name) - set(required))
    if extra:
        lines.append(f"note: run also contains jobs not required for publication: {extra}")
    return ok, lines


def read_required(path: str) -> list[str]:
    with open(path, encoding="utf-8") as f:
        names = [line.strip() for line in f if line.strip()]
    if not names:
        raise Malformed(f"{path}: the required-checks list is empty")
    if len(set(names)) != len(names):
        raise Malformed(f"{path}: duplicate names in the required-checks list")
    return names


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("select-run")
    s.add_argument("--runs", required=True)
    s.add_argument("--repository", required=True)
    s.add_argument("--workflow-path", required=True)
    s.add_argument("--sha", required=True)
    s.add_argument("--branch", required=True)
    s.add_argument("--event", required=True)
    s.add_argument("--out", required=True)
    v = sub.add_parser("verify-jobs")
    v.add_argument("--jobs", required=True)
    v.add_argument("--run-id", required=True, type=int)
    v.add_argument("--run-attempt", required=True, type=int)
    v.add_argument("--sha", required=True)
    v.add_argument("--required-checks", required=True)
    args = ap.parse_args(argv)
    if not SHA_RE.match(args.sha):
        print(f"ci_provenance: --sha {args.sha!r} is not a 40-hex commit", file=sys.stderr)
        return 1
    try:
        if args.cmd == "select-run":
            run, lines = select_run(load_json(args.runs, "workflow-runs response"), repository=args.repository,
                                    workflow_path=args.workflow_path, sha=args.sha, branch=args.branch, event=args.event)
            print("\n".join(lines))
            if run is None:
                return 1
            with open(args.out, "w", encoding="utf-8") as f:
                json.dump({"run_id": run["id"], "run_attempt": run["run_attempt"], "html_url": run.get("html_url"),
                           "head_sha": run["head_sha"], "path": run["path"], "event": run["event"]}, f)
            return 0
        ok, lines = verify_jobs(load_json(args.jobs, "jobs response"), run_id=args.run_id, run_attempt=args.run_attempt,
                                sha=args.sha, required=read_required(args.required_checks))
        print("\n".join(lines))
        return 0 if ok else 1
    except Malformed as e:
        print(f"ci_provenance: MALFORMED {e}", file=sys.stderr)
        return 2
    except OSError as e:
        print(f"ci_provenance: ERROR {e}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
