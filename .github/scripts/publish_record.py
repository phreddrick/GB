#!/usr/bin/env python3
"""Validate a publish record written by publish-push.sh (GB2C-01).

    publish_record.py RECORD.json --expect-status STATUS --expect-commit SHA
                      --expect-namespace ghcr.io/phreddrick [--images-dir DIR]

The record is the only machine-readable statement of what a publish run did,
so publish-push.sh renders it to a temporary file, runs this validator on it,
and only then moves it into place. A record that is empty, truncated, not an
object, missing a field, internally contradictory (a "published" status with
an unverified image, a pull reference that does not match the digest, a
master tag on a partial publication) or inconsistent with the caller's own
view (--expect-*) makes the run fail even when every push succeeded: a
release without a valid record is not reported as a release.

Statuses: started (initial, before any push; never final), published (both
images pushed, verified by digest, both tagged master), partial (at least one
image was pushed and the run did not end in published: GHCR publication of two
images is not atomic), failed (nothing was pushed). Image states: not_pushed,
push_failed, pushed_unverified (bytes are on the registry under the commit tag
but verification failed), verified.

With --images-dir, local_manifest_digest and config_digest of every image whose
archive exists are re-read from the archive with oci_digest.py (source artifact
identity). Exit 0 valid, 1 invalid, 2 unreadable. Standard library only.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import oci_digest  # noqa: E402

SHA_RE = re.compile(r"^[0-9a-f]{40}$")
DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
STATUSES = ("started", "published", "partial", "failed")
STATES = ("not_pushed", "push_failed", "pushed_unverified", "verified")
PUSHED_STATES = ("pushed_unverified", "verified")
IMAGES = {"gb-server": "server", "gbctl": "gbctl"}
TOP_STRINGS = ("source_commit", "workflow_run", "platform", "registry", "dockerfile", "context", "digest_kind")


class RecordError(Exception):
    pass


def load(path: str) -> dict:
    try:
        with open(path, "rb") as f:
            raw = f.read()
    except OSError as e:
        raise RecordError(f"cannot read {path}: {e}") from e
    if not raw.strip():
        raise RecordError(f"{path} is empty")
    try:
        data = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as e:
        raise RecordError(f"{path} is not valid JSON (truncated or malformed): {e}") from e
    if not isinstance(data, dict):
        raise RecordError(f"{path}: top level is {type(data).__name__}, not an object")
    return data


def _str(rec: dict, key: str, problems: list[str], *, nonempty: bool = True) -> str:
    v = rec.get(key)
    if not isinstance(v, str) or (nonempty and not v):
        problems.append(f"{key}: expected a non-empty string, got {v!r}")
        return ""
    return v


def validate(rec: dict, *, expect_status: str | None, expect_commit: str | None, expect_namespace: str | None,
             images_dir: str | None) -> list[str]:
    """Return the list of problems (empty when the record is valid)."""
    p: list[str] = []
    if rec.get("kind") != "gb-publish-record":
        p.append(f"kind: expected gb-publish-record, got {rec.get('kind')!r}")
    if rec.get("schema_version") != 2 or isinstance(rec.get("schema_version"), bool):
        p.append(f"schema_version: expected 2, got {rec.get('schema_version')!r}")
    status = rec.get("status")
    if status not in STATUSES:
        p.append(f"status: expected one of {STATUSES}, got {status!r}")
        status = None
    if expect_status is not None and status != expect_status:
        p.append(f"status: record says {status!r}, the publisher says {expect_status!r}")
    if status == "started":
        p.append("status: 'started' is the pre-push placeholder and is never a final record")
    for key in TOP_STRINGS:
        _str(rec, key, p)
    commit = rec.get("source_commit")
    if isinstance(commit, str) and not SHA_RE.match(commit):
        p.append(f"source_commit: not a 40-hex commit: {commit!r}")
    if expect_commit is not None and commit != expect_commit:
        p.append(f"source_commit: record {commit!r} differs from the publisher's {expect_commit!r}")
    if rec.get("platform") != "linux/amd64":
        p.append(f"platform: expected linux/amd64, got {rec.get('platform')!r}")
    if not isinstance(rec.get("complete"), bool):
        p.append(f"complete: expected a boolean, got {rec.get('complete')!r}")
    elif status is not None and rec["complete"] != (status == "published"):
        p.append(f"complete: {rec['complete']} contradicts status {status!r}")
    master = rec.get("master_tag_applied")
    if not isinstance(master, bool):
        p.append(f"master_tag_applied: expected a boolean, got {master!r}")
        master = False
    if status == "published" and not master:
        p.append("master_tag_applied must be true when status is published")
    if status in ("failed", "started") and master:
        p.append(f"master_tag_applied cannot be true when status is {status}")
    for key in ("scan", "sbom"):
        if not isinstance(rec.get(key), dict):
            p.append(f"{key}: expected an object")
    if isinstance(rec.get("scan"), dict) and not isinstance(rec["scan"].get("tool"), str):
        p.append("scan.tool: expected a string (the scanner version line)")

    images = rec.get("images")
    if not isinstance(images, dict) or set(images) != set(IMAGES):
        p.append(f"images: expected exactly the keys {sorted(IMAGES)}, got {sorted(images) if isinstance(images, dict) else images!r}")
        images = {}
    states: dict[str, str] = {}
    pushed_refs: list[tuple[str, str]] = []
    for name, target in IMAGES.items():
        img = images.get(name)
        if not isinstance(img, dict):
            if name in images:
                p.append(f"images.{name}: expected an object")
            continue
        tag = f"images.{name}"
        repo = img.get("repository")
        if expect_namespace is not None and repo != f"{expect_namespace}/{name}":
            p.append(f"{tag}.repository: expected {expect_namespace}/{name}, got {repo!r}")
        elif not isinstance(repo, str) or not repo.endswith("/" + name):
            p.append(f"{tag}.repository: {repo!r} does not name image {name}")
        if img.get("target") != target:
            p.append(f"{tag}.target: expected {target!r}, got {img.get('target')!r}")
        state = img.get("state")
        if state not in STATES:
            p.append(f"{tag}.state: expected one of {STATES}, got {state!r}")
            continue
        states[name] = state
        local, remote, config = img.get("local_manifest_digest"), img.get("published_manifest_digest"), img.get("config_digest")
        size = img.get("compressed_size")
        if not isinstance(size, int) or isinstance(size, bool) or size < 0:
            p.append(f"{tag}.compressed_size: expected a non-negative integer, got {size!r}")
        for key, val in (("local_manifest_digest", local), ("config_digest", config)):
            if val is not None and (not isinstance(val, str) or not DIGEST_RE.match(val)):
                p.append(f"{tag}.{key}: not a sha256 digest: {val!r}")
        if state != "not_pushed" and (local is None or config is None):
            p.append(f"{tag}: state {state} requires the archive identity (local_manifest_digest, config_digest)")
        if state in PUSHED_STATES:
            if not isinstance(remote, str) or not remote:
                p.append(f"{tag}.published_manifest_digest: required for state {state}, got {remote!r}")
            elif state == "verified" and remote != local:
                p.append(f"{tag}: state verified but published digest {remote} differs from the archive digest {local}")
            elif state == "verified" and not DIGEST_RE.match(remote):
                p.append(f"{tag}.published_manifest_digest: not a sha256 digest: {remote!r}")
            if isinstance(remote, str) and remote:
                pushed_refs.append((name, remote))
        elif remote is not None:
            p.append(f"{tag}.published_manifest_digest must be null for state {state}, got {remote!r}")
        tags = img.get("tags")
        if not isinstance(tags, list) or not all(isinstance(t, str) for t in tags):
            p.append(f"{tag}.tags: expected a list of strings")
        else:
            if state in PUSHED_STATES:
                if commit not in tags:
                    p.append(f"{tag}.tags: pushed image must carry the commit tag")
                if ("master" in tags) != master:
                    p.append(f"{tag}.tags: master tag {'present' if 'master' in tags else 'absent'} but master_tag_applied is {master}")
            elif tags:
                p.append(f"{tag}.tags must be empty for state {state}, got {tags}")
        pull = img.get("pull")
        if state == "verified":
            want = f"{repo}@{remote}" if isinstance(repo, str) and isinstance(remote, str) else None
            if pull != want:
                p.append(f"{tag}.pull: expected {want!r}, got {pull!r}")
        elif pull is not None:
            p.append(f"{tag}.pull must be null unless the image is verified, got {pull!r}")
        if images_dir is not None and local is not None:
            archive = os.path.join(images_dir, f"{name}.oci.tar")
            try:
                info = oci_digest.inspect(archive)
            except (oci_digest.ArchiveError, OSError) as e:
                p.append(f"{tag}: cannot re-read the source archive {archive}: {e}")
            else:
                if info["manifest_digest"] != local:
                    p.append(f"{tag}.local_manifest_digest {local} is not the archive's {info['manifest_digest']}")
                if config is not None and info["config_digest"] != config:
                    p.append(f"{tag}.config_digest {config} is not the archive's {info['config_digest']}")

    if len(states) == len(IMAGES) and status is not None:
        all_verified = all(s == "verified" for s in states.values())
        any_pushed = any(s in PUSHED_STATES for s in states.values())
        if status == "published" and not (all_verified and master):
            p.append(f"status published requires both images verified and master tags applied (states {states}, master {master})")
        if status == "partial" and not any_pushed:
            p.append(f"status partial but no image was pushed (states {states})")
        if status == "partial" and all_verified and master:
            p.append("status partial but both images are verified and tagged master: that is a publication")
        if status == "failed" and any_pushed:
            p.append(f"status failed but an image was pushed (states {states}); that is a partial publication")
        if master and not all_verified:
            p.append(f"master_tag_applied is true but not every image is verified (states {states})")

    published = rec.get("published")
    if not isinstance(published, list):
        p.append("published: expected a list of the pushed images")
    else:
        got = sorted((e.get("name"), e.get("digest")) for e in published if isinstance(e, dict))
        if got != sorted(pushed_refs):
            p.append(f"published: {got} does not match the pushed images {sorted(pushed_refs)}")
    return p


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("record")
    ap.add_argument("--expect-status", choices=STATUSES)
    ap.add_argument("--expect-commit")
    ap.add_argument("--expect-namespace")
    ap.add_argument("--images-dir")
    args = ap.parse_args(argv)
    try:
        rec = load(args.record)
    except RecordError as e:
        print(f"publish_record: ERROR {e}", file=sys.stderr)
        return 2
    problems = validate(rec, expect_status=args.expect_status, expect_commit=args.expect_commit,
                        expect_namespace=args.expect_namespace, images_dir=args.images_dir)
    for prob in problems:
        print(f"publish_record: INVALID {prob}", file=sys.stderr)
    if problems:
        return 1
    print(f"publish_record: valid ({rec['status']}; images " + ", ".join(f"{n}={rec['images'][n]['state']}" for n in IMAGES) + ")")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
