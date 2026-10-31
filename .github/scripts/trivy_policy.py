#!/usr/bin/env python3
"""Evaluate Trivy JSON reports against the Phase 2 image policy (migration plan
13.1, ADR-0006 decision 5): a build fails on any CRITICAL vulnerability that
has a fix available, and on any secret found inside the image. Everything
else is reported, never hidden.

    trivy_policy.py REPORT.json [REPORT.json ...] [--summary FILE]
                    [--expect REPORT.json ARTIFACT_NAME IMAGE_ID]... [--require-identity]

Fail closed on the report itself (GB2C-03). The report must be what Trivy
v0.76.1 writes for `trivy image --input <archive> --format json --scanners
vuln,secret` (types.Report in pkg/types/report.go of that tag):

  * an object with SchemaVersion 2, a non-empty ArtifactName, ArtifactType
    "container_image", a CreatedAt timestamp and a Metadata object whose
    ImageID is the sha256 config digest of the scanned image;
  * Results, when present, is a list of result objects, each with a non-empty
    Target and a Class from Trivy's ResultClass set; Vulnerabilities and
    Secrets, when present, are lists whose entries carry the fields the
    policy reads (VulnerabilityID, PkgName, InstalledVersion, Severity;
    RuleID, Category, Severity, Title). Trivy omits empty lists (omitempty),
    so an absent key is "none found" for that section; any other type
    (false, {}, null, a string) is malformed and never read as empty;
  * at least one result section must exist: Trivy appends an os-pkgs result
    for a detected OS even with zero vulnerabilities (pkg/scan/ospkg), and the
    distroless base carries a dpkg status database, so a report without any
    result section is an incomplete scan, not a clean one;
  * with --expect, ArtifactName must be the scanned archive and
    Metadata.ImageID the archive's config digest (read by oci_digest.py), so
    the report is evidence about the intended runtime image; --require-identity
    makes a report without an expectation an error (CI always passes both).

Outcomes, kept distinct in the output and the exit status:
  0  clean scan (no finding at all), or findings none of which violate policy;
  1  policy violated (a CRITICAL with FixedVersion, or any secret);
  2  no evidence: report missing, empty, truncated, not JSON, malformed,
     wrong schema, wrong artifact identity, or no result section.
Standard library only.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

SEVERITIES = ["CRITICAL", "HIGH", "MEDIUM", "LOW", "UNKNOWN"]
FAIL_SEVERITIES = {"CRITICAL"}
RESULT_CLASSES = {"unknown", "os-pkgs", "lang-pkgs", "config", "secret", "license", "license-file", "crypto", "custom"}
ARTIFACT_TYPE = "container_image"
IMAGE_ID_RE = re.compile(r"^sha256:[0-9a-f]{64}$")


class ReportError(Exception):
    pass


def _is_int(v) -> bool:
    return isinstance(v, int) and not isinstance(v, bool)


def _nonempty_str(obj: dict, key: str, where: str) -> str:
    v = obj.get(key)
    if not isinstance(v, str) or not v:
        raise ReportError(f"{where}: {key} must be a non-empty string, got {v!r}")
    return v


def _optional_list(obj: dict, key: str, where: str) -> list:
    """Trivy omits empty slices; a present key must be a list of objects."""
    if key not in obj:
        return []
    v = obj[key]
    if not isinstance(v, list):
        raise ReportError(f"{where}: {key} is {type(v).__name__} ({v!r}), not a list; not read as empty")
    for i, item in enumerate(v):
        if not isinstance(item, dict):
            raise ReportError(f"{where}: {key}[{i}] is not an object")
    return v


def load(path: str) -> dict:
    try:
        with open(path, "rb") as f:
            raw = f.read()
    except OSError as e:
        raise ReportError(f"{path}: cannot read: {e}") from e
    if not raw.strip():
        raise ReportError(f"{path}: empty file: the scanner wrote no report")
    try:
        data = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as e:
        raise ReportError(f"{path}: not JSON (truncated or malformed): {e}") from e
    if not isinstance(data, dict):
        raise ReportError(f"{path}: top level is {type(data).__name__}, not an object")
    return data


def validate(report: dict, path: str) -> None:
    """Raise ReportError unless the report has the pinned Trivy shape."""
    if not _is_int(report.get("SchemaVersion")) or report["SchemaVersion"] != 2:
        raise ReportError(f"{path}: SchemaVersion must be 2, got {report.get('SchemaVersion')!r}")
    _nonempty_str(report, "ArtifactName", path)
    if report.get("ArtifactType") != ARTIFACT_TYPE:
        raise ReportError(f"{path}: ArtifactType must be {ARTIFACT_TYPE!r}, got {report.get('ArtifactType')!r}")
    _nonempty_str(report, "CreatedAt", path)
    meta = report.get("Metadata")
    if not isinstance(meta, dict):
        raise ReportError(f"{path}: Metadata must be an object, got {type(meta).__name__}")
    image_id = _nonempty_str(meta, "ImageID", f"{path}: Metadata")
    if not IMAGE_ID_RE.match(image_id):
        raise ReportError(f"{path}: Metadata.ImageID {image_id!r} is not a sha256 digest")
    results = _optional_list(report, "Results", path)
    if not results:
        raise ReportError(f"{path}: no result section: the scan produced no target (an os-pkgs result exists for every detected OS); not a clean scan")
    for i, result in enumerate(results):
        where = f"{path}: Results[{i}]"
        _nonempty_str(result, "Target", where)
        cls = result.get("Class")
        if cls not in RESULT_CLASSES:
            raise ReportError(f"{where}: Class {cls!r} is not a Trivy result class")
        for j, v in enumerate(_optional_list(result, "Vulnerabilities", where)):
            vw = f"{where}.Vulnerabilities[{j}]"
            for key in ("VulnerabilityID", "PkgName", "InstalledVersion", "Severity"):
                _nonempty_str(v, key, vw)
            if v["Severity"] not in SEVERITIES:
                raise ReportError(f"{vw}: Severity {v['Severity']!r} is not one of {SEVERITIES}")
            if "FixedVersion" in v and not isinstance(v["FixedVersion"], str):
                raise ReportError(f"{vw}: FixedVersion must be a string when present")
        for j, s in enumerate(_optional_list(result, "Secrets", where)):
            sw = f"{where}.Secrets[{j}]"
            for key in ("RuleID", "Category", "Severity", "Title"):
                _nonempty_str(s, key, sw)


def check_identity(report: dict, path: str, artifact_name: str, image_id: str) -> None:
    got_name = report["ArtifactName"]
    if os.path.abspath(got_name) != os.path.abspath(artifact_name):
        raise ReportError(f"{path}: ArtifactName {got_name!r} is not the intended archive {artifact_name!r}")
    got_id = report["Metadata"]["ImageID"]
    if got_id != image_id:
        raise ReportError(f"{path}: Metadata.ImageID {got_id} is not the archive's config digest {image_id}: a different image was scanned")


def evaluate(report: dict) -> tuple[dict, list[dict], list[dict], int]:
    """Return (counts[severity][fixable|unfixable], violations, secrets, total findings).

    Call validate() first; this function reads the shape validate() accepted.
    """
    counts = {s: {"fixable": 0, "unfixable": 0} for s in SEVERITIES}
    violations: list[dict] = []
    secrets: list[dict] = []
    total = 0
    for result in report.get("Results", []):
        target = result["Target"]
        for v in result.get("Vulnerabilities", []):
            total += 1
            sev = v["Severity"]
            fixed = (v.get("FixedVersion") or "").strip()
            counts[sev]["fixable" if fixed else "unfixable"] += 1
            if sev in FAIL_SEVERITIES and fixed:
                violations.append({
                    "id": v["VulnerabilityID"], "pkg": v["PkgName"], "installed": v["InstalledVersion"],
                    "fixed": fixed, "severity": sev, "target": target,
                })
        for s in result.get("Secrets", []):
            total += 1
            secrets.append({"rule": s["RuleID"], "title": s["Title"], "target": target, "severity": s["Severity"]})
    return counts, violations, secrets, total


def render(name: str, report: dict, counts: dict, violations: list[dict], secrets: list[dict], total: int, identity: str) -> str:
    out = [f"### Trivy policy: {name}", "",
           f"Image `{report['Metadata']['ImageID']}`, {len(report['Results'])} result section(s): "
           + ", ".join(f"{r['Target']} ({r.get('Class')})" for r in report["Results"]),
           f"Identity: {identity}", "",
           "| Severity | fixable | unfixable |", "|---|---:|---:|"]
    for s in SEVERITIES:
        out.append(f"| {s} | {counts[s]['fixable']} | {counts[s]['unfixable']} |")
    out.append("")
    if violations:
        out.append("**Policy violations (CRITICAL with a fix available):**")
        for v in violations:
            out.append(f"- {v['id']} {v['pkg']} {v['installed']} -> {v['fixed']} ({v['target']})")
    if secrets:
        out.append("**Secrets found in the image (always a violation):**")
        for s in secrets:
            out.append(f"- {s['rule']}: {s['title']} ({s['target']})")
    if not violations and not secrets:
        if total == 0:
            out.append("Clean scan: the report is well-formed and lists no vulnerability and no secret.")
        else:
            out.append(f"{total} finding(s), none blocking: no CRITICAL fixable vulnerability and no secret. Unfixable and lower-severity findings above are reported, not suppressed.")
    out.append("")
    return "\n".join(out)


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("reports", nargs="+")
    ap.add_argument("--summary", default=os.environ.get("GITHUB_STEP_SUMMARY"))
    ap.add_argument("--expect", nargs=3, action="append", default=[], metavar=("REPORT", "ARTIFACT_NAME", "IMAGE_ID"),
                    help="the archive path and config digest the report must be about")
    ap.add_argument("--require-identity", action="store_true", help="every report must have an --expect entry")
    args = ap.parse_args(argv)
    expectations = {os.path.abspath(r): (a, i) for r, a, i in args.expect}
    status = 0
    texts = []
    for path in args.reports:
        try:
            report = load(path)
            validate(report, path)
            exp = expectations.get(os.path.abspath(path))
            if exp is not None:
                check_identity(report, path, *exp)
                identity = f"asserted (archive {exp[0]}, config digest {exp[1]})"
            elif args.require_identity:
                raise ReportError(f"{path}: no --expect entry for this report and --require-identity is set")
            else:
                identity = "not asserted (no --expect for this report)"
        except ReportError as e:
            print(f"trivy_policy: ERROR no scan evidence: {e}", file=sys.stderr)
            print("trivy_policy: FAIL (report missing, malformed or about the wrong image)")
            return 2
        counts, violations, secrets, total = evaluate(report)
        text = render(report["ArtifactName"], report, counts, violations, secrets, total, identity)
        print(text)
        texts.append(text)
        if violations or secrets:
            status = 1
    if args.summary:
        try:
            with open(args.summary, "a", encoding="utf-8") as f:
                f.write("\n".join(texts) + "\n")
        except OSError as e:
            print(f"trivy_policy: cannot write summary: {e}", file=sys.stderr)
    print("trivy_policy: " + ("PASS" if status == 0 else "FAIL (policy violated)"))
    return status


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
