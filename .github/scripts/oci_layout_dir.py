#!/usr/bin/env python3
"""Materialise a verified OCI image layout directory from a buildx OCI archive,
for scanners that read layout directories but not layout tarballs (Phase 2C;
hosted run 41250221737: Trivy v0.76.1 `--input` opens a Docker-save tarball or
an OCI layout *directory*, never an OCI layout tarball).

    oci_layout_dir.py ARCHIVE.oci.tar DEST_DIR      # prints a JSON record

The archive stays the artifact of record (it is what is published); the
directory is a scanner input derived from it under these rules:

  * the archive is verified first with oci_digest.inspect (single manifest,
    every blob re-hashed, descriptor sizes, and its ordered layers decoded to
    the config's rootfs.diff_ids, GB2C-06; the shared validator returns no
    record otherwise, so nothing weaker is ever extracted);
  * DEST_DIR must not exist yet (it is created with mode 0700; a pre-existing
    path is refused, never reused or emptied) and must not contain ':' or '@',
    which Trivy would cut off as an image reference;
  * only members at the layout's own paths are extracted, each as a regular
    file created exclusively with mode 0600: `oci-layout`, `index.json` and
    the referenced blobs `blobs/sha256/<hex>` (manifest, config, layers), each
    re-hashed while it is written; any other member (a path outside those
    names, `..`, an absolute path, a symlink, a hard link, a device, a FIFO,
    a directory other than `blobs` or `blobs/sha256`) is rejected; an
    unreferenced blob is skipped and listed, not extracted;
  * the finished directory is read back with oci_digest.inspect and must
    report the same manifest digest, config digest and diff_ids as the
    archive, and must contain nothing else.

Any failure removes the partially written directory and exits 1. The caller
passes DEST_DIR to the scanner and, after the scan, trivy_policy.py
--expect-layout re-reads both the directory and the archive and compares them
with the report. Standard library only.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import stat
import sys
import tarfile

import oci_digest
from oci_digest import ArchiveError

BLOB_RE = re.compile(r"^blobs/sha256/([0-9a-f]{64})$")
LAYOUT_FILES = ("oci-layout", "index.json")
LAYOUT_DIRS = ("blobs", "blobs/sha256")
CHUNK = 1 << 20


def _norm(name: str) -> str:
    """The layout path of a tar member, or an ArchiveError for an unsafe one."""
    if name.startswith("/") or "\\" in name or "\0" in name:
        raise ArchiveError(f"member {name!r} is an absolute or malformed path")
    parts = [p for p in name.split("/") if p not in ("", ".")]
    if any(p == ".." for p in parts):
        raise ArchiveError(f"member {name!r} escapes the layout (..)")
    return "/".join(parts)


def _write_member(tar: tarfile.TarFile, member: tarfile.TarInfo, dest: str, want_digest: str | None) -> int:
    """Create `dest` exclusively and copy the member's bytes into it, hashing them."""
    src = tar.extractfile(member)
    if src is None:
        raise ArchiveError(f"member {member.name!r} cannot be read")
    fd = os.open(dest, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    h = hashlib.sha256()
    written = 0
    with os.fdopen(fd, "wb") as out:
        for block in iter(lambda: src.read(CHUNK), b""):
            h.update(block)
            out.write(block)
            written += len(block)
    if written != member.size:
        raise ArchiveError(f"member {member.name!r}: wrote {written} bytes, header says {member.size}")
    if want_digest is not None and "sha256:" + h.hexdigest() != want_digest:
        raise ArchiveError(f"member {member.name!r} has content digest sha256:{h.hexdigest()}, not its name: the archive is corrupt")
    return written


def materialise(archive: str, dest: str) -> dict:
    if ":" in dest or "@" in dest:
        raise ArchiveError(f"destination {dest!r} contains ':' or '@', which the scanner would treat as an image reference")
    if os.path.lexists(dest):
        raise ArchiveError(f"destination {dest} already exists; refusing to reuse it")
    source = oci_digest.inspect(archive)  # the archive must be one internally consistent image first (layers bound to diff_ids)
    referenced = {source["manifest_digest"], source["config_digest"], *source["layer_digests"]}
    parent = os.path.dirname(os.path.abspath(dest))
    if not os.path.isdir(parent):
        raise ArchiveError(f"parent directory {parent} does not exist")
    os.mkdir(dest, 0o700)
    created = True
    try:
        seen: set[str] = set()
        skipped: list[str] = []
        with tarfile.open(archive, mode="r:*") as tar:
            for member in tar:
                name = _norm(member.name)
                if name == "":
                    continue  # the archive root entry
                if member.isdir():
                    if name not in LAYOUT_DIRS:
                        raise ArchiveError(f"unexpected directory {member.name!r} in the archive")
                    continue
                if not member.isfile():
                    kind = "symlink" if member.issym() else "hard link" if member.islnk() else "special file"
                    raise ArchiveError(f"member {member.name!r} is a {kind}; only regular files are extracted")
                if name in seen:
                    raise ArchiveError(f"member {member.name!r} appears twice")
                seen.add(name)
                m = BLOB_RE.match(name)
                if name in LAYOUT_FILES:
                    _write_member(tar, member, os.path.join(dest, name), None)
                elif m:
                    digest = "sha256:" + m.group(1)
                    if digest not in referenced:
                        skipped.append(name)
                        continue
                    os.makedirs(os.path.join(dest, "blobs", "sha256"), mode=0o700, exist_ok=True)
                    _write_member(tar, member, os.path.join(dest, "blobs", "sha256", m.group(1)), digest)
                else:
                    raise ArchiveError(f"unexpected member {member.name!r} in the archive: not part of an OCI layout")
        missing = [n for n in LAYOUT_FILES if n not in seen]
        if missing:
            raise ArchiveError(f"archive lacks {', '.join(missing)}")
        extracted_blobs = {n[len("blobs/sha256/"):] for n in seen if BLOB_RE.match(n)} - {s[len("blobs/sha256/"):] for s in skipped}
        want_blobs = {d[7:] for d in referenced}
        if extracted_blobs != want_blobs:
            raise ArchiveError(f"extracted blobs {sorted(extracted_blobs)} are not the referenced blobs {sorted(want_blobs)}")
        # Nothing else may be in the directory, and what is there must be regular files.
        for root, dirs, files in os.walk(dest):
            rel = os.path.relpath(root, dest).replace(os.sep, "/")
            rel = "" if rel == "." else rel
            for d in dirs:
                p = f"{rel}/{d}" if rel else d
                if p not in LAYOUT_DIRS or os.path.islink(os.path.join(root, d)):
                    raise ArchiveError(f"unexpected entry {p!r} in the layout directory")
            for f in files:
                p = f"{rel}/{f}" if rel else f
                st = os.lstat(os.path.join(root, f))
                if not stat.S_ISREG(st.st_mode) or (p not in LAYOUT_FILES and not BLOB_RE.match(p)):
                    raise ArchiveError(f"unexpected entry {p!r} in the layout directory")
        result = oci_digest.inspect(dest)  # the directory must read, and verify in full, as the same image
        for key in ("manifest_digest", "config_digest", "diff_ids", "layer_digests", "manifest_media_type"):
            if result[key] != source[key]:
                raise ArchiveError(f"layout directory {key} {result[key]!r} differs from the archive's {source[key]!r}")
    except BaseException:
        if created:
            shutil.rmtree(dest, ignore_errors=True)
        raise
    return {
        "archive": archive,
        "layout_dir": dest,
        "manifest_digest": source["manifest_digest"],
        "config_digest": source["config_digest"],
        "layer_count": len(source["layer_digests"]),
        "blobs_extracted": sorted(want_blobs),
        "blobs_skipped_unreferenced": skipped,
    }


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("archive", help="the verified buildx OCI archive")
    ap.add_argument("dest", help="directory to create (must not exist; no ':' or '@' in the path)")
    args = ap.parse_args(argv)
    try:
        rec = materialise(args.archive, args.dest)
    except ArchiveError as e:
        print(f"oci_layout_dir: FAIL {e}", file=sys.stderr)
        return 1
    except (OSError, tarfile.TarError, json.JSONDecodeError, KeyError, TypeError, ValueError, AttributeError, EOFError) as e:
        print(f"oci_layout_dir: FAIL malformed archive content: {e}", file=sys.stderr)
        return 1
    print(json.dumps(rec, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
