#!/usr/bin/env python3
"""Read the identity of an OCI image archive produced by `docker buildx build
--output type=oci,dest=<file>`, or of an OCI image layout directory extracted
from one by oci_layout_dir.py (Phase 2C, ADR-0006).

    oci_digest.py ARCHIVE.tar            # prints a JSON record
    oci_digest.py ARCHIVE.tar --field manifest_digest
    oci_digest.py LAYOUT_DIR             # the same checks on a layout directory

The record carries the facts the CI build, the publish job and the
digest-update workflow need, read from the archive itself and verified:

  manifest_digest     sha256 of the image manifest bytes. This is the digest a
                      registry assigns when exactly these bytes are pushed, and
                      therefore the value Kustomize references.
  manifest_media_type must be the OCI image manifest type (one platform-specific
                      manifest; the build disables provenance and SBOM
                      attestations so no image index is produced).
  config_digest       sha256 of the image config blob. The classic Docker image
                      store reports it as the image Id, the containerd image
                      store reports manifest_digest instead (hosted run
                      41231194459); docker_image_identity.py links a loaded
                      image to this archive in either case. Trivy reports it
                      as Metadata.ImageID.
  layer_digests       the manifest's layer blob digests, in order (the
                      referenced content of the image together with the
                      manifest and config blobs).
  diff_ids            the config's rootfs.diff_ids as written (bound to the
                      layers by docker_image_identity._verify_oci_layers,
                      which the image checks and oci_layout_dir.py run).
  platform, user, entrypoint, labels, layer_count, compressed_size, annotations

Every blob the record is derived from is re-hashed and compared with the digest
that references it; an archive whose index references more than one manifest,
references an index instead of a manifest, or whose bytes do not match their
digests is rejected (exit 1). A layout directory is read the same way through
directory file descriptors (O_DIRECTORY|O_NOFOLLOW at every component, fstat on
what was opened; GB2C-07): a symlink anywhere in the layout, including its root,
is refused, so nothing outside the directory is ever read. Standard library
only.
"""
from __future__ import annotations

import argparse
import contextlib
import errno
import hashlib
import json
import os
import stat
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


class _TarSource:
    """Members of an OCI layout tarball, by layout path."""

    def __init__(self, tar: tarfile.TarFile):
        self.tar = tar

    def read(self, name: str) -> bytes:
        for candidate in (name, "./" + name):
            try:
                member = self.tar.getmember(candidate)
            except KeyError:
                continue
            if not member.isfile():
                raise ArchiveError(f"{name} is not a regular file in the archive")
            f = self.tar.extractfile(member)
            if f is None:
                raise ArchiveError(f"{name} cannot be read")
            return f.read()
        raise ArchiveError(f"{name} is missing from the archive")


_DIR_FLAGS = getattr(os, "O_RDONLY", 0) | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_CLOEXEC", 0)
# O_NONBLOCK: opening a FIFO left in place of a blob must not block; fstat then rejects it.
_FILE_FLAGS = getattr(os, "O_RDONLY", 0) | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_CLOEXEC", 0) | getattr(os, "O_NONBLOCK", 0)


def _confined_reads_supported() -> bool:
    """Decided once at import from the platform's os module (not re-evaluated per read)."""
    return _CONFINED_READS_SUPPORTED


_CONFINED_READS_SUPPORTED = hasattr(os, "O_NOFOLLOW") and hasattr(os, "O_DIRECTORY") and os.open in os.supports_dir_fd


def canonical_dir_components(path: str) -> tuple[bool, list[str]]:
    """Validate a directory pathname lexically, as supplied, before anything is opened
    (GB2C-07R): it must be non-empty, free of NUL, and spelled canonically, with no trailing
    separator, no doubled separator and no `.` or `..` component (`layout/` and `layout/.`
    make the kernel resolve a symlinked `layout` as an intermediate component, which
    O_NOFOLLOW does not cover). Returns (absolute, components); raises ArchiveError."""
    if not isinstance(path, str) or not path or "\0" in path:
        raise ArchiveError(f"{path!r} is not a usable directory path")
    absolute = path.startswith("/")
    body = path[1:] if absolute else path
    components = body.split("/") if body else []
    if (absolute and not components) or any(c in ("", ".", "..") for c in components):
        raise ArchiveError(
            f"{path!r} is not a canonical directory path (no trailing or doubled separator, no '.' or '..' component); refusing to open it"
        )
    return absolute, components


class _DirSource:
    """Files of an OCI layout directory, by layout path, read through directory file
    descriptors (GB2C-07, GB2C-07R): the root pathname is validated lexically as supplied
    (canonical_dir_components), then walked component by component from `/` or the current
    directory, each component opened relative to its already-open parent with
    O_DIRECTORY|O_NOFOLLOW, so a symlinked root under any spelling and a symlinked ancestor
    are refused; every further layout component is opened relative to its parent with
    O_NOFOLLOW (directories also with O_DIRECTORY), the type of each opened descriptor is taken
    from fstat, and the bytes are read from the final descriptor. No path is resolved by name
    after it was checked, so a symlinked `blobs`, `blobs/sha256`, blob, `index.json` or
    `oci-layout`, a component swapped for a symlink between two opens, a non-regular file,
    an empty, `.` or `..` component and anything outside the layout are all refused. Platforms
    without dir_fd-relative opens are refused explicitly rather than read by name."""

    def __init__(self, root: str):
        if not _confined_reads_supported():
            raise ArchiveError("this platform lacks dir_fd-relative O_NOFOLLOW opens; a layout directory cannot be read safely here")
        self.root = root
        self.fd = None
        absolute, components = canonical_dir_components(root)
        fd = self._open_dir("/" if absolute else ".", None, "/" if absolute else ".")
        try:
            for i, component in enumerate(components):
                shown = ("/" if absolute else "") + "/".join(components[: i + 1])
                nfd = self._open_dir(component, fd, shown)
                os.close(fd)
                fd = nfd
        except BaseException:
            os.close(fd)
            raise
        self.fd = fd

    @staticmethod
    def _refused(component: str, dir_fd, shown: str, what: str) -> ArchiveError:
        """The diagnostic for a component the open refused. Linux reports a symlink met with
        O_NOFOLLOW as ELOOP, or as ENOTDIR when O_DIRECTORY is also set; after the refusal a
        dir_fd-relative lstat (which opens and follows nothing) names the kind."""
        try:
            is_link = stat.S_ISLNK(os.lstat(component, dir_fd=dir_fd).st_mode)
        except OSError:
            is_link = False
        if is_link:
            return ArchiveError(f"{shown} is a symlink; the layout may contain regular files and directories only (unsafe layout structure)")
        return ArchiveError(f"{shown} is not a {what} in the layout")

    @staticmethod
    def _open_dir(component: str, dir_fd, shown: str) -> int:
        try:
            fd = os.open(component, _DIR_FLAGS, dir_fd=dir_fd)
        except FileNotFoundError:
            raise ArchiveError(f"{shown} is missing from the archive") from None
        except OSError as e:
            if e.errno in (errno.ENOTDIR, errno.ELOOP):
                raise _DirSource._refused(component, dir_fd, shown, "directory") from None
            raise ArchiveError(f"{shown} cannot be opened: {e}") from e
        try:
            if not stat.S_ISDIR(os.fstat(fd).st_mode):
                raise ArchiveError(f"{shown} is not a directory in the layout")
        except BaseException:
            os.close(fd)
            raise
        return fd

    def read(self, name: str) -> bytes:
        parts = name.split("/")
        if not parts or any(p in ("", ".", "..") or "\0" in p for p in parts) or name.startswith("/"):
            raise ArchiveError(f"{name!r} is not a plain relative layout path")
        shown = lambda i: "/".join(parts[: i + 1])  # noqa: E731
        fds: list[int] = []
        try:
            parent = self.fd
            for i, part in enumerate(parts[:-1]):
                parent = self._open_dir(part, parent, shown(i))
                fds.append(parent)
            leaf = parts[-1]
            try:
                fd = os.open(leaf, _FILE_FLAGS, dir_fd=parent)
            except FileNotFoundError:
                raise ArchiveError(f"{name} is missing from the archive") from None
            except OSError as e:
                if e.errno in (errno.ENOTDIR, errno.ELOOP):
                    raise self._refused(leaf, parent, name, "regular file") from None
                raise ArchiveError(f"{name} cannot be read: {e}") from e
            fds.append(fd)
            if not stat.S_ISREG(os.fstat(fd).st_mode):
                raise ArchiveError(f"{name} is not a regular file in the archive")
            with os.fdopen(os.dup(fd), "rb") as f:
                return f.read()
        finally:
            for fd in fds:
                os.close(fd)

    def close(self) -> None:
        if self.fd is not None:
            os.close(self.fd)
            self.fd = None


def _read(src, name: str) -> bytes:
    """Read a layout path from a source, or from a bare tarfile.TarFile (callers that
    already hold the archive open, such as docker_image_identity.py)."""
    if isinstance(src, tarfile.TarFile):
        src = _TarSource(src)
    return src.read(name)


def _blob(tar, digest: str) -> bytes:
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


def _open(path: str):
    """A context manager yielding the layout source: a directory (opened as a confined
    descriptor, never through a symlinked root) or a tarball."""
    if os.path.islink(path) or os.path.isdir(path):
        src = _DirSource(path)  # a non-canonical spelling, a symlinked root or ancestor fails here
        return contextlib.closing(src)
    try:
        tar = tarfile.open(path, mode="r:*")
    except (OSError, tarfile.TarError) as e:
        raise ArchiveError(f"cannot open {path}: {e}") from e

    class _Ctx:
        def __enter__(self):
            return _TarSource(tar)

        def __exit__(self, *exc):
            tar.close()
            return False

    return _Ctx()


def inspect(path: str) -> dict:
    """The verified identity record of an OCI layout tarball or directory."""
    with _open(path) as tar:
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
            "layer_digests": [l.get("digest") for l in layers],
            "diff_ids": (config.get("rootfs") or {}).get("diff_ids") if isinstance(config.get("rootfs"), dict) else None,
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
