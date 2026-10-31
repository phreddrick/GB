#!/usr/bin/env python3
"""Read the identity of an OCI image archive produced by `docker buildx build
--output type=oci,dest=<file>` (Phase 2C, ADR-0006).

    oci_digest.py ARCHIVE.tar            # prints a JSON record
    oci_digest.py ARCHIVE.tar --field manifest_digest

The record carries the facts the CI build, the publish job and the
digest-update workflow need, read from the archive itself and verified:

  manifest_digest     sha256 of the image manifest bytes. This is the digest a
                      registry assigns when exactly these bytes are pushed, and
                      therefore the value Kustomize references.
  manifest_media_type must be the OCI image manifest type (one platform-specific
                      manifest; the build disables provenance and SBOM
                      attestations so no image index is produced).
  config_digest       sha256 of the image config blob. `docker image inspect`
                      reports it as the image Id, which links a loaded image to
                      this archive.
  platform, user, entrypoint, labels, layer_count, compressed_size, annotations

Every blob the record is derived from is re-hashed and compared with the digest
that references it; an archive whose index references more than one manifest,
references an index instead of a manifest, or whose bytes do not match their
digests is rejected (exit 1). Standard library only.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import sys
import tarfile

IMAGE_MANIFEST = "application/vnd.oci.image.manifest.v1+json"
DOCKER_MANIFEST = "application/vnd.docker.distribution.manifest.v2+json"
INDEX_TYPES = {
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
}


class ArchiveError(Exception):
    pass


def _read(tar: tarfile.TarFile, name: str) -> bytes:
    for candidate in (name, "./" + name):
        try:
            member = tar.getmember(candidate)
        except KeyError:
            continue
        if not member.isfile():
            raise ArchiveError(f"{name} is not a regular file in the archive")
        f = tar.extractfile(member)
        if f is None:
            raise ArchiveError(f"{name} cannot be read")
        return f.read()
    raise ArchiveError(f"{name} is missing from the archive")


def _blob(tar: tarfile.TarFile, digest: str) -> bytes:
    if not isinstance(digest, str) or not digest.startswith("sha256:") or len(digest) != 71:
        raise ArchiveError(f"unsupported or malformed digest {digest!r}")
    hexpart = digest[7:]
    if any(c not in "0123456789abcdef" for c in hexpart):
        raise ArchiveError(f"malformed digest {digest!r}")
    data = _read(tar, f"blobs/sha256/{hexpart}")
    actual = "sha256:" + hashlib.sha256(data).hexdigest()
    if actual != digest:
        raise ArchiveError(f"blob {digest} has content digest {actual}: the archive is corrupt or tampered")
    return data


def inspect(path: str) -> dict:
    try:
        tar = tarfile.open(path, mode="r:*")
    except (OSError, tarfile.TarError) as e:
        raise ArchiveError(f"cannot open {path}: {e}") from e
    with tar:
        layout = json.loads(_read(tar, "oci-layout"))
        if layout.get("imageLayoutVersion") != "1.0.0":
            raise ArchiveError(f"unexpected oci-layout {layout!r}")
        index = json.loads(_read(tar, "index.json"))
        manifests = index.get("manifests")
        if not isinstance(manifests, list) or len(manifests) != 1:
            raise ArchiveError(
                f"index.json must reference exactly one manifest (found {0 if not isinstance(manifests, list) else len(manifests)}); "
                "a multi-platform build or attestation index is not the reviewed single-manifest form"
            )
        entry = manifests[0]
        media_type = entry.get("mediaType")
        if media_type in INDEX_TYPES:
            raise ArchiveError("index.json references an image index, not an image manifest; build with provenance=false, sbom=false and one platform")
        if media_type not in (IMAGE_MANIFEST, DOCKER_MANIFEST):
            raise ArchiveError(f"unexpected manifest media type {media_type!r}")
        manifest_digest = entry.get("digest")
        manifest_bytes = _blob(tar, manifest_digest)
        manifest = json.loads(manifest_bytes)
        if manifest.get("mediaType", media_type) != media_type:
            raise ArchiveError("manifest mediaType disagrees with index.json")
        config_ref = manifest.get("config", {})
        config_digest = config_ref.get("digest")
        config = json.loads(_blob(tar, config_digest))
        layers = manifest.get("layers", [])
        if not isinstance(layers, list) or not layers:
            raise ArchiveError("manifest has no layers")
        for layer in layers:
            _blob(tar, layer.get("digest"))
        cfg = config.get("config", {}) or {}
        return {
            "archive": path,
            "manifest_digest": manifest_digest,
            "manifest_media_type": media_type,
            "config_digest": config_digest,
            "platform": {"os": config.get("os"), "architecture": config.get("architecture")},
            "user": cfg.get("User"),
            "entrypoint": cfg.get("Entrypoint"),
            "cmd": cfg.get("Cmd"),
            "labels": cfg.get("Labels") or {},
            "layer_count": len(layers),
            "compressed_size": int(len(manifest_bytes)) + int(config_ref.get("size", 0)) + sum(int(l.get("size", 0)) for l in layers),
            "annotations": entry.get("annotations") or {},
        }


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("archive")
    ap.add_argument("--field", help="print only this top-level field")
    args = ap.parse_args(argv)
    try:
        rec = inspect(args.archive)
    except ArchiveError as e:
        print(f"oci_digest: FAIL {e}", file=sys.stderr)
        return 1
    except (json.JSONDecodeError, KeyError, TypeError, ValueError) as e:
        print(f"oci_digest: FAIL malformed archive content: {e}", file=sys.stderr)
        return 1
    if args.field:
        if args.field not in rec:
            print(f"oci_digest: FAIL no field {args.field}", file=sys.stderr)
            return 1
        value = rec[args.field]
        print(value if isinstance(value, str) else json.dumps(value))
    else:
        print(json.dumps(rec, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
