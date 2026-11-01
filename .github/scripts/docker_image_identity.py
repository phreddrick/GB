#!/usr/bin/env python3
"""Prove that the image Docker loaded is the image in a verified OCI archive
(Phase 2C, ADR-0006).

    docker_image_identity.py --oci ARCHIVE.oci.tar --saved SAVED.tar \\
        --docker-id "$(docker image inspect TAG --format '{{.Id}}')" --tag TAG

`docker image inspect .Id` does not name the same descriptor on every Docker
image store:

  classic graphdriver store    Id is the image config digest.
  containerd image store       Id is the digest of the image's target
                               descriptor, here the OCI image manifest
                               (hosted run 41231194459 reported an Id equal
                               to the manifest digest, not the config
                               digest).

The Id is therefore only accepted when it equals the archive's verified
manifest digest or its verified config digest, and in either case the loaded
image is checked independently, from the bytes of `docker image save TAG`:

  * manifest.json holds exactly one image, tagged TAG; the config bytes it
    names hash to the archive's config digest (so the image configuration,
    including user, entrypoint, labels and rootfs.diff_ids, is the validated
    one);
  * every layer it names, uncompressed, hashes to the matching
    rootfs.diff_ids entry of that config, and a content-addressed layer or
    config path (blobs/sha256/<hex>) hashes to its own name;
  * when Docker reported the manifest digest, the saved OCI index must name
    that manifest, its bytes must hash to it, and it must reference the
    archive's config digest.

The OCI archive itself is re-verified with oci_digest.inspect, which returns
no record unless every blob re-hashes to its digest and the manifest's ordered
layers decode to the config's rootfs.diff_ids (GB2C-06; the check lives in the
shared validator so that every caller, not only this one, enforces it).
Otherwise an archive whose manifest references another image's layers would
pass, because Docker's saved layers are only compared with the config. Prints
a JSON record; exit 1 with a reason on any mismatch. Standard library only.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
import tarfile
import zlib

import oci_digest
from oci_digest import CHUNK, DIGEST_RE, GZIP_MAGIC, ZSTD_MAGIC, ArchiveError, _gunzip_digest

BLOB_PATH_RE = re.compile(r"^(?:\./)?blobs/sha256/([0-9a-f]{64})$")


def _member(tar: tarfile.TarFile, name: str) -> tarfile.TarInfo:
    for candidate in (name, "./" + name, name[2:] if name.startswith("./") else None):
        if not candidate:
            continue
        try:
            member = tar.getmember(candidate)
        except KeyError:
            continue
        if not member.isfile():
            raise ArchiveError(f"{name} is not a regular file in the saved image")
        return member
    raise ArchiveError(f"{name} is missing from the saved image")


def _raw_and_content_digest(tar: tarfile.TarFile, name: str) -> tuple[str, str]:
    """sha256 of the stored bytes (checked against a blobs/sha256/<hex> name) and of
    the uncompressed content (gzip or none)."""
    member = _member(tar, name)
    f = tar.extractfile(member)
    if f is None:
        raise ArchiveError(f"{name} cannot be read")
    head = f.read(4)
    f.seek(0)
    if head.startswith(ZSTD_MAGIC):
        raise ArchiveError(f"{name} is zstd-compressed; this checker verifies uncompressed and gzip layers only")
    raw = hashlib.sha256()
    for block in iter(lambda: f.read(CHUNK), b""):
        raw.update(block)
    raw_digest = "sha256:" + raw.hexdigest()
    _check_content_address(name, raw_digest)  # before decompressing: corruption is reported as such
    if not head.startswith(GZIP_MAGIC):
        return raw_digest, raw_digest
    f.seek(0)
    return raw_digest, _gunzip_digest(f)


def _check_content_address(name: str, raw_digest: str) -> None:
    m = BLOB_PATH_RE.match(name)
    if m and "sha256:" + m.group(1) != raw_digest:
        raise ArchiveError(f"{name} has content digest {raw_digest}: the saved image is corrupt")


def _saved_manifest_entry(tar: tarfile.TarFile, tag: str | None) -> dict:
    entries = json.loads(tar.extractfile(_member(tar, "manifest.json")).read())
    if not isinstance(entries, list) or len(entries) != 1:
        raise ArchiveError(f"saved manifest.json must describe exactly one image (found {len(entries) if isinstance(entries, list) else 'none'})")
    entry = entries[0]
    if tag is not None:
        tags = {t.removeprefix("docker.io/library/") for t in (entry.get("RepoTags") or [])}
        if tag.removeprefix("docker.io/library/") not in tags:
            raise ArchiveError(f"saved image is tagged {sorted(tags)}, not {tag}")
    return entry


def _index_manifests(tar: tarfile.TarFile, index: dict, depth: int = 0) -> list[str]:
    """Image manifest digests reachable from an OCI index (nested indexes followed)."""
    if depth > 3:
        raise ArchiveError("saved OCI index nesting is too deep")
    found = []
    for desc in index.get("manifests") or []:
        media_type, digest = desc.get("mediaType"), desc.get("digest")
        if media_type in oci_digest.INDEX_TYPES:
            found += _index_manifests(tar, json.loads(oci_digest._blob(tar, digest)), depth + 1)
        elif media_type in (oci_digest.IMAGE_MANIFEST, oci_digest.DOCKER_MANIFEST):
            found.append(digest)
    return found


def verify(oci_path: str, saved_path: str, docker_id: str, tag: str | None = None) -> dict:
    archive = oci_digest.inspect(oci_path)  # blobs, descriptor sizes and the layer-to-diff_id binding (GB2C-06) verified here
    manifest_digest, config_digest = archive["manifest_digest"], archive["config_digest"]
    if manifest_digest == config_digest:
        raise ArchiveError("archive manifest and config digests are equal; the archive is not a valid image")
    if not isinstance(docker_id, str) or not DIGEST_RE.match(docker_id):
        raise ArchiveError(f"Docker image Id {docker_id!r} is not a sha256 digest")
    if docker_id == config_digest:
        id_kind = "config"
    elif docker_id == manifest_digest:
        id_kind = "manifest"
    else:
        raise ArchiveError(
            f"Docker image Id {docker_id} is neither the archive manifest digest {manifest_digest} "
            f"nor its config digest {config_digest}: a different image was loaded"
        )

    try:
        tar = tarfile.open(saved_path, mode="r:*")
    except (OSError, tarfile.TarError) as e:
        raise ArchiveError(f"cannot open saved image {saved_path}: {e}") from e
    with tar:
        entry = _saved_manifest_entry(tar, tag)
        config_name = entry.get("Config")
        if not isinstance(config_name, str) or not config_name:
            raise ArchiveError("saved manifest.json names no Config")
        config_bytes = tar.extractfile(_member(tar, config_name)).read()
        saved_config_digest = "sha256:" + hashlib.sha256(config_bytes).hexdigest()
        _check_content_address(config_name, saved_config_digest)
        if saved_config_digest != config_digest:
            raise ArchiveError(
                f"loaded image config has digest {saved_config_digest}, not the archive config digest {config_digest}: a different image was loaded"
            )

        diff_ids = (json.loads(config_bytes).get("rootfs") or {}).get("diff_ids")
        layers = entry.get("Layers")
        if not isinstance(diff_ids, list) or not diff_ids:
            raise ArchiveError("config has no rootfs.diff_ids")
        if not isinstance(layers, list) or len(layers) != len(diff_ids):
            raise ArchiveError(f"saved image has {len(layers) if isinstance(layers, list) else 'no'} layers, config lists {len(diff_ids)} diff_ids")
        for i, (name, want) in enumerate(zip(layers, diff_ids)):
            _, content_digest = _raw_and_content_digest(tar, name)
            if content_digest != want:
                raise ArchiveError(f"saved layer {i} ({name}) has uncompressed digest {content_digest}, config diff_id is {want}")

        saved_manifest_verified = False
        index_names = [m.name for m in tar.getmembers() if m.name in ("index.json", "./index.json")]
        if index_names:
            index = json.loads(tar.extractfile(index_names[0]).read())
            if manifest_digest in _index_manifests(tar, index):
                manifest = json.loads(oci_digest._blob(tar, manifest_digest))
                if (manifest.get("config") or {}).get("digest") != config_digest:
                    raise ArchiveError(f"saved manifest {manifest_digest} does not reference config {config_digest}")
                saved_manifest_verified = True
        if id_kind == "manifest" and not saved_manifest_verified:
            raise ArchiveError(
                f"Docker reports the manifest digest {manifest_digest} as the image Id, but the saved image does not contain that manifest"
            )

    return {
        "docker_id": docker_id,
        "docker_id_kind": id_kind,
        "manifest_digest": manifest_digest,
        "config_digest": config_digest,
        "saved_config_verified": True,
        "saved_layers_verified": len(layers),
        "saved_manifest_verified": saved_manifest_verified,
    }


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--oci", required=True, help="the buildx OCI archive (verified with oci_digest.inspect)")
    ap.add_argument("--saved", required=True, help="output of `docker image save TAG`")
    ap.add_argument("--docker-id", required=True, help="`docker image inspect TAG --format '{{.Id}}'`")
    ap.add_argument("--tag", help="the tag the saved image must carry")
    args = ap.parse_args(argv)
    try:
        rec = verify(args.oci, args.saved, args.docker_id, args.tag)
    except ArchiveError as e:
        print(f"docker_image_identity: FAIL {e}", file=sys.stderr)
        return 1
    except (json.JSONDecodeError, KeyError, TypeError, ValueError, AttributeError, EOFError, zlib.error, OSError, tarfile.TarError) as e:
        print(f"docker_image_identity: FAIL malformed image content: {e}", file=sys.stderr)
        return 1
    print(json.dumps(rec, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
