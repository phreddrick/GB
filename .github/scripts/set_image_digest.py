#!/usr/bin/env python3
"""Replace the pinned image digest in a Kustomize entry point, and nothing else
(Phase 2C digest-update pull request, ADR-0006 decision 3).

    set_image_digest.py --file deploy/kustomize/apps/dev/server/kustomization.yaml \
        --image ghcr.io/phreddrick/gb-server --digest sha256:<64 hex>

The edit is textual and minimal: the one line `digest: <value>` that belongs
to the `images:` entry whose `name:` is --image is rewritten with the new
value; indentation, comments, ordering and every other byte are preserved.
Before writing, the script requires:

  * the digest to be `sha256:` followed by exactly 64 lower-case hex digits and
    not the documented all-zero UNPUBLISHED sentinel;
  * exactly one `images` entry for --image, carrying exactly one `digest:` line
    and no `newName`/`newTag` (a tag would make the reference mutable);
  * the parsed YAML of the result to differ from the original in exactly that
    one value (PyYAML is required for this structural comparison).

Exit status: 0 the file was changed; 3 the file already carries the digest
(nothing written, a distinct code so a caller can skip a duplicate pull
request); 1 invalid input; 2 the file does not have the expected shape.
With --check the file is validated and the current digest printed; nothing is
written.
"""
from __future__ import annotations

import argparse
import copy
import re
import sys

try:
    import yaml
except ImportError:  # pragma: no cover
    yaml = None

DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
SENTINEL = "sha256:" + "0" * 64
NAME_RE = re.compile(r"^(?P<indent>\s*)-\s+name:\s*(?P<name>\S+)\s*$")
DIGEST_LINE_RE = re.compile(r"^(?P<prefix>\s+digest:\s*)(?P<value>\S+)(?P<suffix>\s*(#.*)?)$")
KEY_RE = re.compile(r"^(?P<indent>\s*)(?P<key>[A-Za-z_][\w-]*):")


class ShapeError(Exception):
    pass


def validate_digest(digest: str) -> str:
    if not isinstance(digest, str) or not DIGEST_RE.fullmatch(digest):
        raise ValueError(f"digest must be sha256: followed by 64 lower-case hex digits (got {digest!r})")
    if digest == SENTINEL:
        raise ValueError("the all-zero sentinel is not a published digest")
    return digest


def locate(lines: list[str], image: str) -> tuple[int, str]:
    """Return (line index of the digest line, current value) for the images entry of `image`."""
    # Find the top-level `images:` block.
    starts = [i for i, l in enumerate(lines) if re.match(r"^images:\s*(#.*)?$", l)]
    if len(starts) != 1:
        raise ShapeError(f"expected exactly one top-level images: block, found {len(starts)}")
    start = starts[0]
    end = len(lines)
    for j in range(start + 1, len(lines)):
        if lines[j].strip() and not lines[j].startswith((" ", "\t", "#")):
            end = j
            break
    # Entries inside the block.
    entries: list[tuple[int, str, int]] = []  # (line, name, indent)
    for j in range(start + 1, end):
        m = NAME_RE.match(lines[j])
        if m:
            entries.append((j, m.group("name"), len(m.group("indent"))))
    matches = [e for e in entries if e[1] == image]
    if len(matches) != 1:
        raise ShapeError(f"expected exactly one images entry named {image}, found {len(matches)} (entries: {[e[1] for e in entries]})")
    at, _, indent = matches[0]
    # The entry extends to the next `- name:` at the same indent or the end of the block.
    entry_end = end
    for j in range(at + 1, end):
        m = NAME_RE.match(lines[j])
        if m and len(m.group("indent")) == indent:
            entry_end = j
            break
    digest_lines = []
    for j in range(at + 1, entry_end):
        stripped = lines[j].strip()
        if not stripped or stripped.startswith("#"):
            continue
        km = KEY_RE.match(lines[j])
        key = km.group("key") if km else None
        if key in ("newName", "newTag"):
            raise ShapeError(f"the images entry for {image} sets {key}; a tag or rename is not a digest pin")
        if key == "digest":
            dm = DIGEST_LINE_RE.match(lines[j])
            if not dm:
                raise ShapeError(f"unparseable digest line {lines[j]!r}")
            digest_lines.append((j, dm.group("value")))
    if len(digest_lines) != 1:
        raise ShapeError(f"expected exactly one digest: line in the images entry for {image}, found {len(digest_lines)}")
    return digest_lines[0]


def _structural_diff(before: str, after: str, image: str, old: str, new: str) -> None:
    if yaml is None:
        raise ShapeError("PyYAML is required for the structural comparison (python3-yaml)")
    a = yaml.safe_load(before)
    b = yaml.safe_load(after)
    if not isinstance(a, dict) or not isinstance(b, dict):
        raise ShapeError("kustomization is not a mapping")
    expected = copy.deepcopy(a)
    hits = [e for e in expected.get("images", []) if isinstance(e, dict) and e.get("name") == image]
    if len(hits) != 1 or hits[0].get("digest") != old:
        raise ShapeError("parsed images entry does not match the textual match")
    hits[0]["digest"] = new
    if b != expected:
        raise ShapeError("the rewritten file differs from the original in more than the digest value")


def set_digest(text: str, image: str, digest: str) -> tuple[str, str, str]:
    """Return (new_text, old_digest, new_digest). Raises ValueError/ShapeError."""
    digest = validate_digest(digest)
    lines = text.splitlines(keepends=True)
    idx, old = locate(lines, image)
    if old != SENTINEL:
        try:
            validate_digest(old)
        except ValueError as e:
            raise ShapeError(f"the current value is neither the sentinel nor a valid digest: {e}") from e
    m = DIGEST_LINE_RE.match(lines[idx])
    assert m is not None
    lines[idx] = f"{m.group('prefix')}{digest}{m.group('suffix') or ''}"
    if not lines[idx].endswith("\n") and text.endswith("\n"):
        lines[idx] += "\n"
    new_text = "".join(lines)
    if old != digest:
        _structural_diff(text, new_text, image, old, digest)
    return new_text, old, digest


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--file", required=True)
    ap.add_argument("--image", required=True)
    ap.add_argument("--digest", help="new digest (required unless --check)")
    ap.add_argument("--check", action="store_true", help="validate the file shape and print the current digest; write nothing")
    args = ap.parse_args(argv)
    try:
        with open(args.file, encoding="utf-8") as f:
            text = f.read()
    except OSError as e:
        print(f"set_image_digest: cannot read {args.file}: {e}", file=sys.stderr)
        return 1
    try:
        if args.check:
            _, current = locate(text.splitlines(keepends=True), args.image)
            print(current)
            return 0
        if not args.digest:
            print("set_image_digest: --digest is required", file=sys.stderr)
            return 1
        new_text, old, new = set_digest(text, args.image, args.digest)
    except ValueError as e:
        print(f"set_image_digest: invalid input: {e}", file=sys.stderr)
        return 1
    except ShapeError as e:
        print(f"set_image_digest: refusing to edit {args.file}: {e}", file=sys.stderr)
        return 2
    if old == new:
        print(f"set_image_digest: {args.file} already pins {new}; nothing written")
        return 3
    with open(args.file, "w", encoding="utf-8") as f:
        f.write(new_text)
    print(f"set_image_digest: {args.file}: {old} -> {new}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
