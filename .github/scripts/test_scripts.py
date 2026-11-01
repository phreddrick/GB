#!/usr/bin/env python3
"""Unit tests for the Phase 2C workflow helpers, with synthetic data only (no
registry, no runner, no cluster):

    python3 -m unittest discover -s .github/scripts -p 'test_*.py' -v

Covers oci_digest (a synthetic OCI layout archive, corrupted blobs, an
attestation index, the same archive extracted as a directory and read through
descriptor-confined opens that follow no symlink, GB2C-07; and, since the
GB2C-06 binding moved into the shared validator, inspect() itself refusing
every archive or directory whose manifest layers do not decode to the config's
rootfs.diff_ids, SharedValidatorLayerTests), oci_layout_dir
(the scanner input: safe extraction of the verified archive into a fresh layout
directory that reads back as the same image; unsafe members, pre-existing
destinations and tampering rejected; both workflows wired to it, hosted run
41250221737), set_image_digest (the real dev entry point text, invalid
and sentinel digests, tags, duplicate entries, no-op), trivy_policy (fixable
and unfixable CRITICAL findings, secrets, identity assertion) and
workflow_policy_check (the repository's own workflows must pass; synthetic
violations of each rule must fail). The release-safety regressions for the
independent review findings GB2C-01 to GB2C-05 (publish record, CI
provenance, malformed scan reports, kind cleanup, token-created pull
requests) are in test_release_safety.py.
"""
from __future__ import annotations

import gzip
import hashlib
import io
import json
import os
import shutil
import subprocess
import sys
import tarfile
import stat
import tempfile
import textwrap
import unittest
from unittest import mock

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", ".."))
sys.path.insert(0, HERE)

import docker_image_identity  # noqa: E402
import oci_digest  # noqa: E402
import oci_layout_dir  # noqa: E402
import set_image_digest  # noqa: E402
import trivy_policy  # noqa: E402
import workflow_policy_check  # noqa: E402

DEV_ENTRY = os.path.join(REPO, "deploy", "kustomize", "apps", "dev", "server", "kustomization.yaml")
IMAGE = "ghcr.io/phreddrick/gb-server"
GOOD = "sha256:" + "ab" * 32
GOOD2 = "sha256:" + "cd" * 32


# ----------------------------------------------------------------- helpers --
def _digest(b: bytes) -> str:
    return "sha256:" + hashlib.sha256(b).hexdigest()


def _layer_tar(name: str, content: bytes) -> bytes:
    """A deterministic one-file tar stream: the bytes a DiffID is computed over."""
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w", format=tarfile.PAX_FORMAT) as tar:
        ti = tarfile.TarInfo(name)
        ti.size, ti.mode, ti.mtime = len(content), 0o755, 0
        tar.addfile(ti, io.BytesIO(content))
    return buf.getvalue()


LAYER_A = _layer_tar("usr/local/bin/gb-server", b"\x7fELF-a" * 32)
LAYER_B = _layer_tar("usr/local/bin/gb-server", b"\x7fELF-b" * 32)
LAYER_C = _layer_tar("etc/passwd", b"nonroot:x:65532:65532\n")
GZIP_LAYER = "application/vnd.oci.image.layer.v1.tar+gzip"
TAR_LAYER = "application/vnd.oci.image.layer.v1.tar"


def make_oci_archive(path: str, *, manifests: int = 1, index_entry: bool = False, corrupt_layer: bool = False, labels=None,
                     layers: list | None = None, media_type: str = GZIP_LAYER, blobs: list | None = None,
                     diff_ids: list | None = None, rootfs=..., descriptors: list | None = None,
                     config_size_delta: int = 0, manifest_size_delta: int = 0) -> dict:
    """Write a minimal but well-formed OCI layout tarball, deterministic by default: one
    gzip layer (LAYER_A) whose DiffID is in the config. `layers` are uncompressed tar
    streams (the DiffIDs are computed from them); `blobs` override the stored layer blobs
    (default: gzip of each layer, or the layer itself for the tar media type); `diff_ids`,
    `rootfs` and `descriptors` override the config's and the manifest's values; the size
    deltas mis-state a descriptor size. Every stored blob always hashes to its name."""
    if layers is None:  # deterministic, but distinct per archive name (gb-server.oci.tar and gbctl.oci.tar differ)
        layers = [_layer_tar("usr/local/bin/" + os.path.basename(path).split(".")[0], b"\x7fELF-" + os.path.basename(path).encode() * 16)]
    if blobs is None:
        blobs = [gzip.compress(l, mtime=0) if media_type in oci_digest.OCI_GZIP_LAYERS else l for l in layers]
    if diff_ids is None:
        diff_ids = [_digest(l) for l in layers]
    if rootfs is ...:
        rootfs = {"type": "layers", "diff_ids": diff_ids}
    config_doc = {
        "architecture": "amd64", "os": "linux",
        "config": {"User": "65532:65532", "Entrypoint": ["/usr/local/bin/gb-server"], "Labels": labels or {"a": "b"}},
    }
    if rootfs is not None:
        config_doc["rootfs"] = rootfs
    config = json.dumps(config_doc).encode()
    if descriptors is None:
        descriptors = [{"mediaType": media_type, "digest": _digest(b), "size": len(b)} for b in blobs]
    manifest = json.dumps({
        "schemaVersion": 2, "mediaType": oci_digest.IMAGE_MANIFEST,
        "config": {"mediaType": "application/vnd.oci.image.config.v1+json", "digest": _digest(config), "size": len(config) + config_size_delta},
        "layers": descriptors,
    }).encode()
    entry = {"mediaType": oci_digest.IMAGE_MANIFEST, "digest": _digest(manifest), "size": len(manifest) + manifest_size_delta,
             "annotations": {"io.containerd.image.name": "docker.io/library/gb-server:ci"}}
    if index_entry:
        entry["mediaType"] = "application/vnd.oci.image.index.v1+json"
    index = json.dumps({"schemaVersion": 2, "manifests": [entry] * manifests}).encode()
    stored = {_digest(config): config, _digest(manifest): manifest}
    for b in blobs:
        stored[_digest(b)] = b
    if corrupt_layer:
        d = _digest(blobs[0])
        stored[d] = b"X" + blobs[0][1:]  # the name no longer matches the content
    with tarfile.open(path, "w") as tar:
        def add(name: str, data: bytes) -> None:
            ti = tarfile.TarInfo(name)
            ti.size = len(data)
            tar.addfile(ti, io.BytesIO(data))
        add("oci-layout", b'{"imageLayoutVersion":"1.0.0"}')
        add("index.json", index)
        for d, data in stored.items():
            add(f"blobs/sha256/{d[7:]}", data)
    return {"manifest_digest": _digest(manifest), "config_digest": _digest(config), "diff_ids": diff_ids}


def gb2c06_archive(path: str, **labels) -> dict:
    """The independent reviewer's GB2C-06 counterexample: image A's config (and therefore
    A's rootfs.diff_ids) with a manifest that references image B's gzip layer; every blob
    and the manifest hash correctly, so per-blob digest checks alone accept it."""
    return make_oci_archive(path, layers=[LAYER_B], diff_ids=[_digest(LAYER_A)], **labels)


def blobs_hash_to_their_names(path: str) -> bool:
    """True when every blobs/sha256/<hex> member of the tarball hashes to <hex>."""
    with tarfile.open(path) as tar:
        names = [m.name for m in tar if m.name.startswith("blobs/sha256/")]
        return bool(names) and all(hashlib.sha256(tar.extractfile(n).read()).hexdigest() == n[len("blobs/sha256/"):] for n in names)


# --------------------------------------------------------------- oci_digest --
class OciDigestTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.tmp)

    def test_reads_single_manifest(self):
        p = os.path.join(self.tmp, "a.tar")
        want = make_oci_archive(p)
        rec = oci_digest.inspect(p)
        self.assertEqual(rec["manifest_digest"], want["manifest_digest"])
        self.assertEqual(rec["config_digest"], want["config_digest"])
        self.assertEqual(rec["platform"], {"os": "linux", "architecture": "amd64"})
        self.assertEqual(rec["user"], "65532:65532")
        self.assertEqual(rec["layer_count"], 1)
        self.assertEqual(rec["diff_ids"], want["diff_ids"])
        self.assertEqual(rec["layer_digests"], [_digest(gzip.compress(_layer_tar("usr/local/bin/a", b"\x7fELF-" + b"a.tar" * 16), mtime=0))])
        self.assertEqual(rec["annotations"]["io.containerd.image.name"], "docker.io/library/gb-server:ci")

    def test_rejects_two_manifests(self):
        p = os.path.join(self.tmp, "a.tar")
        make_oci_archive(p, manifests=2)
        with self.assertRaises(oci_digest.ArchiveError):
            oci_digest.inspect(p)

    def test_rejects_attestation_index(self):
        p = os.path.join(self.tmp, "a.tar")
        make_oci_archive(p, index_entry=True)
        with self.assertRaises(oci_digest.ArchiveError):
            oci_digest.inspect(p)

    def test_rejects_corrupt_blob(self):
        p = os.path.join(self.tmp, "a.tar")
        make_oci_archive(p, corrupt_layer=True)
        with self.assertRaises(oci_digest.ArchiveError):
            oci_digest.inspect(p)

    def test_cli_field_and_missing_file(self):
        p = os.path.join(self.tmp, "a.tar")
        want = make_oci_archive(p)
        out = subprocess.run([sys.executable, os.path.join(HERE, "oci_digest.py"), p, "--field", "manifest_digest"], capture_output=True, text=True)
        self.assertEqual(out.returncode, 0, out.stderr)
        self.assertEqual(out.stdout.strip(), want["manifest_digest"])
        out = subprocess.run([sys.executable, os.path.join(HERE, "oci_digest.py"), os.path.join(self.tmp, "none.tar")], capture_output=True, text=True)
        self.assertEqual(out.returncode, 1)


class SharedValidatorLayerTests(unittest.TestCase):
    """GB2C-06 in the shared validator: oci_digest.inspect() returns no record unless the
    manifest's ordered layers decode to the config's rootfs.diff_ids, so every direct caller
    (image-checks.sh, oci_layout_dir.py, trivy_policy.py, publish-push.sh, publish_record.py,
    kind-e2e.sh) refuses an archive whose manifest, config and filesystem are not one image.
    Every test here calls inspect() directly, never the Docker image checker."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.tmp)
        self.archive = os.path.join(self.tmp, "img.oci.tar")
        self.cli = [sys.executable, os.path.join(HERE, "oci_digest.py")]

    def make(self, **kw) -> dict:
        return make_oci_archive(self.archive, **kw)

    def assertRejected(self, fragment: str, path: str | None = None) -> oci_digest.ArchiveError:
        with self.assertRaises(oci_digest.ArchiveError) as cm:
            oci_digest.inspect(path or self.archive)
        self.assertIn(fragment, str(cm.exception))
        return cm.exception

    @staticmethod
    def desc(media_type, blob: bytes, **extra) -> dict:
        d = {"mediaType": media_type, "digest": _digest(blob), "size": len(blob), **extra}
        return {k: v for k, v in d.items() if v is not ...}

    # ------------------------------------------------------------- accepted --
    def test_valid_single_layer_gzip_image(self):
        for media_type in sorted(oci_digest.OCI_GZIP_LAYERS):
            with self.subTest(media_type=media_type):
                want = self.make(layers=[LAYER_A], media_type=media_type)
                rec = oci_digest.inspect(self.archive)
                self.assertEqual((rec["manifest_digest"], rec["config_digest"]), (want["manifest_digest"], want["config_digest"]))
                self.assertEqual(rec["diff_ids"], [_digest(LAYER_A)])
                self.assertEqual(rec["layer_digests"], [_digest(gzip.compress(LAYER_A, mtime=0))])
                self.assertEqual(rec["layer_count"], 1)
                self.assertEqual(rec["manifest_media_type"], oci_digest.IMAGE_MANIFEST)

    def test_valid_uncompressed_image(self):
        want = self.make(layers=[LAYER_A], media_type=TAR_LAYER)
        rec = oci_digest.inspect(self.archive)
        self.assertEqual(rec["manifest_digest"], want["manifest_digest"])
        self.assertEqual(rec["layer_digests"], rec["diff_ids"])  # uncompressed: the blob digest is the DiffID
        self.assertEqual(rec["diff_ids"], [_digest(LAYER_A)])

    def test_valid_ordered_multi_layer_image(self):
        layers = [LAYER_A, LAYER_C, LAYER_B]
        want = self.make(layers=layers)
        rec = oci_digest.inspect(self.archive)
        self.assertEqual(rec["manifest_digest"], want["manifest_digest"])
        self.assertEqual(rec["diff_ids"], [_digest(l) for l in layers])
        self.assertEqual(rec["layer_digests"], [_digest(gzip.compress(l, mtime=0)) for l in layers])
        self.assertEqual(rec["layer_count"], 3)
        self.assertEqual(rec["compressed_size"], sum(len(gzip.compress(l, mtime=0)) for l in layers) + self.config_and_manifest_bytes())
        # Mixed encodings, one descriptor at a time, are also accepted when each decodes to its diff_id.
        blobs = [gzip.compress(LAYER_A, mtime=0), LAYER_C, gzip.compress(LAYER_B, mtime=0)]
        self.make(layers=layers, blobs=blobs, descriptors=[self.desc(GZIP_LAYER, blobs[0]), self.desc(TAR_LAYER, blobs[1]),
                                                           self.desc("application/vnd.docker.image.rootfs.diff.tar.gzip", blobs[2])])
        self.assertEqual(oci_digest.inspect(self.archive)["diff_ids"], [_digest(l) for l in layers])

    def config_and_manifest_bytes(self) -> int:
        with tarfile.open(self.archive) as tar:
            index = json.load(tar.extractfile("index.json"))
            entry = index["manifests"][0]
            manifest = json.load(tar.extractfile("blobs/sha256/" + entry["digest"][7:]))
            return entry["size"] + manifest["config"]["size"]

    # ------------------------------------------------------- the GB2C-06 case --
    def test_reviewer_config_a_with_layer_b_rejected_by_inspect(self):
        """The counterexample: configuration A, filesystem layer B, correct blob and manifest
        hashes. inspect() itself refuses it, with no Docker image checker involved."""
        gb2c06_archive(self.archive)
        self.assertTrue(blobs_hash_to_their_names(self.archive), "the attack is invisible to per-blob digest checks")
        e = self.assertRejected("the archive manifest does not carry its config's layers")
        self.assertIn(_digest(LAYER_B), str(e))  # the layer's real content digest
        self.assertIn(_digest(LAYER_A), str(e))  # the config's diff_id
        # The CLI form that image-checks.sh, publish-push.sh and kind-e2e.sh run.
        for args in ([self.archive], [self.archive, "--field", "config_digest"], [self.archive, "--field", "manifest_digest"]):
            with self.subTest(args=args[1:]):
                r = subprocess.run(self.cli + args, capture_output=True, text=True)
                self.assertEqual((r.returncode, r.stdout), (1, ""))
                self.assertIn("oci_digest: FAIL", r.stderr)
                self.assertIn("does not carry its config's layers", r.stderr)

    def test_layer_binding_is_load_bearing(self):
        """Mutation guard: with the layer check removed, the same archive is accepted, so the
        regression above depends on _verify_layers being called by inspect()."""
        gb2c06_archive(self.archive)
        with mock.patch.object(oci_digest, "_verify_layers", return_value=[_digest(gzip.compress(LAYER_B, mtime=0))]):
            oci_digest.inspect(self.archive)
        self.assertRejected("does not carry its config's layers")

    # ------------------------------------------------------------- rejected --
    def test_reversed_layers_rejected(self):
        self.make(layers=[LAYER_A, LAYER_B], diff_ids=[_digest(LAYER_B), _digest(LAYER_A)])
        self.assertTrue(blobs_hash_to_their_names(self.archive))
        e = self.assertRejected("archive layer 0 ")
        self.assertIn("does not carry its config's layers", str(e))

    def test_incorrect_diff_id_rejected(self):
        bogus = "sha256:" + "5" * 64
        self.make(layers=[LAYER_A], diff_ids=[bogus])
        self.assertRejected(f"config diff_id is {bogus}")
        self.make(layers=[LAYER_A, LAYER_B], diff_ids=[_digest(LAYER_A), bogus])
        self.assertRejected("archive layer 1 ")

    def test_layer_count_mismatch_rejected(self):
        self.make(layers=[LAYER_A, LAYER_B], diff_ids=[_digest(LAYER_A)])
        self.assertRejected("archive manifest lists 2 layers, config lists 1 diff_ids")
        self.make(layers=[LAYER_A], diff_ids=[_digest(LAYER_A), _digest(LAYER_B)])
        self.assertRejected("archive manifest lists 1 layers, config lists 2 diff_ids")

    def test_invalid_rootfs_rejected(self):
        good = [_digest(LAYER_A)]
        for label, rootfs in (("other type", {"type": "other", "diff_ids": good}), ("missing type", {"diff_ids": good}),
                              ("not an object", "layers"), ("absent", None)):
            with self.subTest(label=label):
                self.make(layers=[LAYER_A], rootfs=rootfs)
                self.assertRejected("rootfs is not of type layers")

    def test_missing_or_malformed_diff_ids_rejected(self):
        for label, rootfs in (("missing", {"type": "layers"}), ("empty", {"type": "layers", "diff_ids": []}),
                              ("not a list", {"type": "layers", "diff_ids": _digest(LAYER_A)})):
            with self.subTest(label=label):
                self.make(layers=[LAYER_A], rootfs=rootfs)
                self.assertRejected("has no rootfs.diff_ids")
        for label, bad in (("upper case", "sha256:" + "A" * 64), ("short", "sha256:abc"), ("no algorithm", "0" * 64),
                           ("sha512", "sha512:" + "0" * 128), ("integer", 5), ("null", None)):
            with self.subTest(label=label):
                self.make(layers=[LAYER_A], diff_ids=[bad])
                self.assertRejected("is not a sha256 digest")

    def test_corrupt_or_truncated_compressed_layer_rejected(self):
        gz = gzip.compress(LAYER_A, mtime=0)
        mid = len(gz) // 2
        for label, blob, fragment in (("truncated", gz[:-12], "truncated or corrupt"), ("truncated body", gz[:mid], "truncated or corrupt"),
                                      ("corrupt deflate", gz[:12] + bytes([gz[12] ^ 0xFF]) + gz[13:], "truncated or corrupt"),
                                      ("bad crc", gz[:-8] + bytes([gz[-8] ^ 0xFF]) + gz[-7:], "truncated or corrupt"),
                                      ("trailing garbage", gz + b"garbage", "archive layer 0"),
                                      ("not gzip at all", LAYER_A, "is not a gzip stream")):
            with self.subTest(label=label):
                self.make(layers=[LAYER_A], blobs=[blob])
                self.assertTrue(blobs_hash_to_their_names(self.archive))
                self.assertRejected(fragment)

    def test_incorrect_descriptor_size_rejected(self):
        gz = gzip.compress(LAYER_A, mtime=0)
        for label, size in (("too large", len(gz) + 1), ("too small", len(gz) - 1), ("string", str(len(gz))), ("bool", True), ("missing", ...)):
            with self.subTest(label=label):
                self.make(layers=[LAYER_A], descriptors=[self.desc(GZIP_LAYER, gz, size=size)])
                self.assertRejected("archive layer 0 descriptor size")
        self.make(layers=[LAYER_A], config_size_delta=1)
        self.assertRejected("manifest config descriptor size")
        self.make(layers=[LAYER_A], manifest_size_delta=-1)
        self.assertRejected("index.json manifest descriptor size")

    def test_unsupported_or_foreign_layer_formats_rejected(self):
        gz = gzip.compress(LAYER_A, mtime=0)
        zstd = b"\x28\xb5\x2f\xfd" + b"\0" * 32
        cases = (
            ("zstd media type", self.desc("application/vnd.oci.image.layer.v1.tar+zstd", zstd), zstd, "unsupported media type"),
            ("non-distributable", self.desc("application/vnd.oci.image.layer.nondistributable.v1.tar+gzip", gz), gz, "unsupported media type"),
            ("unknown", self.desc("application/octet-stream", LAYER_A), LAYER_A, "unsupported media type"),
            ("missing media type", self.desc(..., LAYER_A), LAYER_A, "unsupported media type"),
            ("foreign layer", self.desc(GZIP_LAYER, gz, urls=["https://example.invalid/l"]), gz, "foreign layer"),
            ("zstd bytes as tar", self.desc(TAR_LAYER, zstd), zstd, "its blob is compressed"),
            ("gzip bytes as tar", self.desc(TAR_LAYER, gz), gz, "its blob is compressed"),
            ("descriptor not an object", "sha256:" + "0" * 64, gz, "descriptor is not an object"),
        )
        for label, desc, blob, fragment in cases:
            with self.subTest(label=label):
                self.make(layers=[LAYER_A], blobs=[blob], descriptors=[desc])
                self.assertRejected(fragment)

    def test_missing_or_corrupted_referenced_blob_rejected(self):
        want = self.make(layers=[LAYER_A])
        base = _members_of(self.archive)
        layer_name = "blobs/sha256/" + _digest(gzip.compress(LAYER_A, mtime=0))[7:]
        config_name = "blobs/sha256/" + want["config_digest"][7:]
        for label, name in (("layer", layer_name), ("config", config_name)):
            with self.subTest(label=f"missing {label}"):
                _tar_with(self.archive, [m for m in base if m[0] != name])
                self.assertRejected("is missing from the archive")
            with self.subTest(label=f"corrupted {label}"):
                _tar_with(self.archive, [m if m[0] != name else (name, "file", b"X" + m[2][1:]) for m in base])
                self.assertRejected("corrupt or tampered")

    # --------------------------------------------- tarball and directory alike --
    def test_equivalent_tarball_and_layout_directory_agree(self):
        self.make(layers=[LAYER_A, LAYER_C])
        dest = os.path.join(self.tmp, "layout")
        oci_layout_dir.materialise(self.archive, dest)
        a, d = oci_digest.inspect(self.archive), oci_digest.inspect(dest)
        self.assertEqual((a.pop("archive"), d.pop("archive")), (self.archive, dest))
        self.assertEqual(a, d)

    def test_invalid_tarball_and_layout_directory_both_rejected(self):
        gb2c06_archive(self.archive)
        self.assertRejected("does not carry its config's layers")
        dest = os.path.join(self.tmp, "layout")
        with self.assertRaises(oci_digest.ArchiveError):
            oci_layout_dir.materialise(self.archive, dest)  # the materialiser never produces such a directory
        self.assertFalse(os.path.lexists(dest))
        # The same bytes laid out as a directory by other means are refused by the same check.
        os.makedirs(os.path.join(dest, "blobs", "sha256"))
        for name, _, data in _members_of(self.archive):
            with open(os.path.join(dest, name), "wb") as f:
                f.write(data)
        self.assertRejected("does not carry its config's layers", dest)
        # A valid layout whose layer blob is replaced after materialisation is refused too.
        self.make(layers=[LAYER_A])
        dest2 = os.path.join(self.tmp, "layout2")
        oci_layout_dir.materialise(self.archive, dest2)
        blob = os.path.join(dest2, "blobs", "sha256", _digest(gzip.compress(LAYER_A, mtime=0))[7:])
        with open(blob, "wb") as f:
            f.write(gzip.compress(LAYER_B, mtime=0))
        self.assertRejected("corrupt or tampered", dest2)

    def test_confinement_and_canonical_path_rules_still_apply(self):
        """GB2C-07/07R are unchanged by the move: the strengthened validator still reads a
        directory only through descriptor-confined opens of a canonically spelled root."""
        self.make(layers=[LAYER_A])
        real = os.path.join(self.tmp, "real")
        oci_layout_dir.materialise(self.archive, real)
        oci_digest.inspect(real)
        link = os.path.join(self.tmp, "link")
        os.symlink(real, link)
        for spelled in (link, link + "/", link + "/.", real + "/", real + "//", os.path.join(self.tmp, ".", "real"), real + "/../real"):
            with self.subTest(path=spelled):
                with self.assertRaises(oci_digest.ArchiveError):
                    oci_digest.inspect(spelled)
        moved = os.path.join(self.tmp, "moved")
        shutil.move(os.path.join(real, "blobs"), moved)
        os.symlink(moved, os.path.join(real, "blobs"))
        self.assertRejected("symlink", real)


# ----------------------------------------------------------- oci_layout_dir --
def _tar_with(path: str, members: list) -> None:
    """A tarball from (name, kind, data_or_target) tuples: kind is 'file', 'dir', 'sym' or 'hard'."""
    with tarfile.open(path, "w") as tar:
        for name, kind, payload in members:
            ti = tarfile.TarInfo(name)
            if kind == "file":
                ti.size = len(payload)
                tar.addfile(ti, io.BytesIO(payload))
            elif kind == "dir":
                ti.type = tarfile.DIRTYPE
                tar.addfile(ti)
            elif kind == "sym":
                ti.type, ti.linkname = tarfile.SYMTYPE, payload
                tar.addfile(ti)
            elif kind == "hard":
                ti.type, ti.linkname = tarfile.LNKTYPE, payload
                tar.addfile(ti)


def _members_of(path: str) -> list:
    """The regular-file members of a layout tarball as (name, 'file', bytes), for re-packing."""
    out = []
    with tarfile.open(path) as tar:
        for m in tar:
            if m.isfile():
                out.append((m.name, "file", tar.extractfile(m).read()))
    return out


class OciLayoutDirTests(unittest.TestCase):
    """oci_layout_dir.py: the archive verified by oci_digest is extracted, allow-listed path
    by allow-listed path, into a fresh directory that must read back as the same image."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.tmp)
        self.archive = os.path.join(self.tmp, "img.oci.tar")
        self.want = make_oci_archive(self.archive)
        self.dest = os.path.join(self.tmp, "layout", "gb-server")
        os.mkdir(os.path.join(self.tmp, "layout"))

    def materialise(self, archive=None, dest=None) -> dict:
        return oci_layout_dir.materialise(archive or self.archive, dest or self.dest)

    def assertRefused(self, fragment: str, archive=None, dest=None):
        with self.assertRaises(oci_digest.ArchiveError) as cm:
            self.materialise(archive, dest)
        self.assertIn(fragment, str(cm.exception))
        self.assertFalse(os.path.lexists(dest or self.dest), "a refused extraction leaves no directory behind")

    def test_valid_archive_becomes_a_layout_directory_of_the_same_image(self):
        rec = self.materialise()
        self.assertEqual((rec["manifest_digest"], rec["config_digest"]), (self.want["manifest_digest"], self.want["config_digest"]))
        self.assertEqual(rec["blobs_skipped_unreferenced"], [])
        # The directory is read by the production validator and names the same image.
        got = oci_digest.inspect(self.dest)
        src = oci_digest.inspect(self.archive)
        for key in ("manifest_digest", "config_digest", "layer_digests", "diff_ids", "manifest_media_type", "layer_count"):
            self.assertEqual(got[key], src[key], key)
        entries = sorted(os.path.relpath(os.path.join(r, f), self.dest) for r, _, fs in os.walk(self.dest) for f in fs)
        self.assertEqual(entries, sorted(["index.json", "oci-layout"] + [f"blobs/sha256/{d[7:]}" for d in {src["manifest_digest"], src["config_digest"], *src["layer_digests"]}]))
        self.assertEqual(stat.S_IMODE(os.stat(self.dest).st_mode), 0o700)
        for e in entries:
            self.assertFalse(os.path.islink(os.path.join(self.dest, e)))
        # The materialised directory is a valid scanner input path: no ':' or '@'.
        self.assertNotRegex(self.dest, "[:@]")

    def test_multi_layer_archive_round_trips(self):
        make_oci_archive(self.archive, layers=[LAYER_A, LAYER_C, LAYER_B])
        rec = self.materialise()
        self.assertEqual(rec["layer_count"], 3)
        self.assertEqual(oci_digest.inspect(self.dest)["layer_digests"], oci_digest.inspect(self.archive)["layer_digests"])

    def test_unreferenced_blob_is_not_extracted(self):
        members = _members_of(self.archive) + [("blobs/sha256/" + "7" * 64, "file", b"")]  # name is not its hash either
        _tar_with(self.archive, members)
        rec = self.materialise()
        self.assertEqual(rec["blobs_skipped_unreferenced"], ["blobs/sha256/" + "7" * 64])
        self.assertFalse(os.path.exists(os.path.join(self.dest, "blobs", "sha256", "7" * 64)))

    def test_unsafe_members_rejected(self):
        base = _members_of(self.archive)
        blob = next(n for n, _, _ in base if n.startswith("blobs/"))
        cases = {
            "traversal": [("../evil", "file", b"x")], "nested traversal": [("blobs/../../evil", "file", b"x")],
            "absolute": [("/etc/evil", "file", b"x")],
            "symlink member": [("blobs/sha256/" + "8" * 64, "sym", "/etc/passwd")],
            "symlinked index": [("index.json", "sym", "oci-layout")],
            "hard link": [("blobs/sha256/" + "9" * 64, "hard", blob)],
            "unrelated file": [("manifest.json", "file", b"[]")],
            "unrelated directory": [("extra", "dir", None), ("extra/x", "file", b"x")],
            "blob with a bad name": [("blobs/sha256/not-hex", "file", b"x")],
            "blob under another algorithm": [("blobs/sha512/" + "a" * 128, "file", b"x")],
            "duplicate member": [base[-1]],
        }
        for label, extra in cases.items():
            with self.subTest(label=label):
                _tar_with(self.archive, (base + extra) if label != "symlinked index" else [m for m in base if m[0] != "index.json"] + extra)
                with self.assertRaises(oci_digest.ArchiveError):
                    self.materialise()
                self.assertFalse(os.path.lexists(self.dest))

    def test_missing_layout_files_rejected(self):
        base = _members_of(self.archive)
        for missing in ("index.json", "oci-layout"):
            with self.subTest(missing=missing):
                _tar_with(self.archive, [m for m in base if m[0] != missing])
                self.assertRefused("is missing from the archive")

    def test_missing_or_modified_referenced_blob_rejected(self):
        base = _members_of(self.archive)
        blob = next(m for m in base if m[0].startswith("blobs/"))
        _tar_with(self.archive, [m for m in base if m is not blob])
        self.assertRefused("is missing from the archive")
        _tar_with(self.archive, [m if m is not blob else (blob[0], "file", b"X" + blob[2][1:]) for m in base])
        self.assertRefused("corrupt or tampered")

    def test_archive_failing_the_validator_is_not_extracted(self):
        make_oci_archive(self.archive, manifests=2)
        self.assertRefused("exactly one manifest")
        make_oci_archive(self.archive, layers=[LAYER_B], diff_ids=[_digest(LAYER_A)])  # GB2C-06 archive
        self.assertRefused("does not carry its config's layers")

    def test_destination_rules(self):
        os.mkdir(self.dest)
        with self.assertRaises(oci_digest.ArchiveError) as cm:
            self.materialise()
        self.assertIn("already exists", str(cm.exception))
        self.assertTrue(os.path.isdir(self.dest), "a pre-existing destination is refused, never removed or reused")
        shutil.rmtree(self.dest)
        with open(self.dest, "w") as f:
            f.write("file")
        with self.assertRaises(oci_digest.ArchiveError):
            self.materialise()
        os.unlink(self.dest)
        os.symlink(self.tmp, self.dest)
        with self.assertRaises(oci_digest.ArchiveError):
            self.materialise()
        os.unlink(self.dest)
        for bad in (os.path.join(self.tmp, "layout", "gb-server:ci"), os.path.join(self.tmp, "layout", "img@sha256")):
            with self.subTest(bad=bad):
                self.assertRefused("':' or '@'", dest=bad)
        self.assertRefused("parent directory", dest=os.path.join(self.tmp, "nope", "gb-server"))

    def test_cli(self):
        cli = [sys.executable, os.path.join(HERE, "oci_layout_dir.py")]
        ok = subprocess.run(cli + [self.archive, self.dest], capture_output=True, text=True)
        self.assertEqual(ok.returncode, 0, ok.stderr)
        self.assertEqual(json.loads(ok.stdout)["config_digest"], self.want["config_digest"])
        again = subprocess.run(cli + [self.archive, self.dest], capture_output=True, text=True)
        self.assertEqual((again.returncode, again.stdout), (1, ""))
        self.assertIn("oci_layout_dir: FAIL", again.stderr)
        missing = subprocess.run(cli + [os.path.join(self.tmp, "none.tar"), self.dest + "2"], capture_output=True, text=True)
        self.assertEqual(missing.returncode, 1)
        self.assertFalse(os.path.exists(self.dest + "2"))


class LayoutConfinementTests(unittest.TestCase):
    """GB2C-07: a layout directory is re-read after the scan through directory file
    descriptors that follow no symlink at any component, so a layout whose `blobs`,
    `blobs/sha256`, a blob, `index.json`, `oci-layout` or root was replaced by a symlink
    (even one pointing at identical bytes) is rejected without reading the target."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.tmp)
        self.archive = os.path.join(self.tmp, "img.oci.tar")
        self.want = make_oci_archive(self.archive, layers=[LAYER_A, LAYER_B])
        os.mkdir(os.path.join(self.tmp, "layout"))
        self.layout = os.path.join(self.tmp, "layout", "gb-server")
        oci_layout_dir.materialise(self.archive, self.layout)
        self.outside = os.path.join(self.tmp, "outside")  # where moved content lives
        self.opened: list = []  # every os.open call: (path, dir_fd)
        real_open = os.open

        def recording_open(path, flags, mode=0o777, *, dir_fd=None):
            self.opened.append((os.fsdecode(path) if not isinstance(path, int) else path, dir_fd))
            return real_open(path, flags, mode, dir_fd=dir_fd)

        self.recording_open = recording_open

    # ---------------------------------------------------------------- helpers --
    def report_file(self) -> str:
        rep = trivy_report()
        rep["ArtifactName"], rep["Metadata"]["ImageID"] = self.layout, self.want["config_digest"]
        p = os.path.join(self.tmp, "report.json")
        with open(p, "w") as f:
            json.dump(rep, f)
        return p

    def policy(self) -> subprocess.CompletedProcess:
        p = self.report_file()
        return subprocess.run([sys.executable, os.path.join(HERE, "trivy_policy.py"), p, "--require-identity", "--expect-layout", p, self.layout, self.archive],
                              capture_output=True, text=True)

    def swap_for_symlink(self, rel: str, *, modify: bool = False) -> str:
        """Move layout/<rel> to outside/<rel> and put a symlink to it in its place."""
        src = os.path.join(self.layout, rel)
        dst = os.path.join(self.outside, rel)
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        shutil.move(src, dst)
        if modify:
            target = dst if os.path.isfile(dst) else os.path.join(dst, "sha256", self.want["config_digest"][7:]) if rel == "blobs" else os.path.join(dst, self.want["config_digest"][7:])
            with open(target, "ab") as f:
                f.write(b" ")
        os.symlink(dst, src)
        return dst

    def assertUnsafeRejected(self, target: str, *, fragment: str = "symlink"):
        """inspect() and the policy both refuse, name the unsafe structure, and never open the target."""
        self.opened.clear()
        with mock.patch("os.open", self.recording_open):
            with self.assertRaises(oci_digest.ArchiveError) as cm:
                oci_digest.inspect(self.layout)
        self.assertIn(fragment, str(cm.exception))
        self.assertIn("unsafe layout structure", str(cm.exception))
        outside_opens = [o for o in self.opened if isinstance(o[0], str) and (o[0].startswith(self.outside) or (o[1] is None and o[0] not in ("/", ".")))]
        self.assertEqual(outside_opens, [], "nothing outside the layout root was opened by name")
        self.assertTrue(all(o[1] is not None or o[0] in ("/", ".") for o in self.opened), "only '/' or '.' is opened by path; everything else relative to a descriptor (GB2C-07R walks the root too)")
        r = self.policy()
        self.assertEqual(r.returncode, 2, r.stdout + r.stderr)
        self.assertNotIn("trivy_policy: PASS", r.stdout)
        self.assertIn("no scan evidence", r.stderr)
        self.assertIn(fragment, r.stderr)

    # ------------------------------------------------------------------ cases --
    def test_01_valid_layout_passes(self):
        rec = oci_digest.inspect(self.layout)
        self.assertEqual((rec["manifest_digest"], rec["config_digest"]), (self.want["manifest_digest"], self.want["config_digest"]))
        r = self.policy()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("trivy_policy: PASS", r.stdout)

    def test_02_symlinked_blobs_rejected(self):
        """The reviewer's reproduction: identical bytes behind a symlinked `blobs` tree."""
        self.assertUnsafeRejected(self.swap_for_symlink("blobs"))

    def test_03_symlinked_blobs_sha256_rejected(self):
        self.assertUnsafeRejected(self.swap_for_symlink("blobs/sha256"))

    def test_04_symlinked_blob_rejected(self):
        for digest in (self.want["config_digest"], self.want["manifest_digest"]):
            with self.subTest(digest=digest):
                self.assertUnsafeRejected(self.swap_for_symlink(f"blobs/sha256/{digest[7:]}"))
                shutil.rmtree(self.layout); shutil.rmtree(self.outside, ignore_errors=True)
                oci_layout_dir.materialise(self.archive, self.layout)

    def test_05_symlinked_index_rejected(self):
        self.assertUnsafeRejected(self.swap_for_symlink("index.json"))

    def test_06_symlinked_oci_layout_rejected(self):
        self.assertUnsafeRejected(self.swap_for_symlink("oci-layout"))

    def test_07_symlinked_root_rejected(self):
        real = os.path.join(self.tmp, "real-layout")
        shutil.move(self.layout, real)
        os.symlink(real, self.layout)
        with self.assertRaises(oci_digest.ArchiveError) as cm:
            oci_digest.inspect(self.layout)
        self.assertIn("symlink", str(cm.exception))
        r = self.policy()
        self.assertEqual(r.returncode, 2)
        self.assertNotIn("trivy_policy: PASS", r.stdout)
        self.assertIn("is a symlink (unsafe layout structure)", r.stderr)

    def test_08_symlink_to_identical_bytes_still_rejected(self):
        """Following the link would have validated: the rejection proves it was not followed."""
        dst = self.swap_for_symlink("blobs")
        self.assertEqual(sorted(os.listdir(os.path.join(dst, "sha256"))), sorted(d[7:] for d in {self.want["manifest_digest"], self.want["config_digest"], *oci_digest.inspect(self.archive)["layer_digests"]}))
        self.assertUnsafeRejected(dst)

    def test_09_symlink_to_modified_bytes_rejected(self):
        self.assertUnsafeRejected(self.swap_for_symlink("blobs", modify=True))

    def test_10_component_swapped_after_root_was_opened_fails_safely(self):
        """A `blobs` directory that is real when the root is opened and a symlink by the time
        the reader descends: the descent opens `blobs` relative to the root descriptor with
        O_NOFOLLOW, so the substituted target is never opened."""
        real_open = os.open
        swapped = {"done": False}

        def swapping_open(path, flags, mode=0o777, *, dir_fd=None):
            if dir_fd is not None and os.fsdecode(path) == "blobs" and not swapped["done"]:
                swapped["done"] = True
                dst = os.path.join(self.outside, "blobs")
                os.makedirs(self.outside, exist_ok=True)
                shutil.move(os.path.join(self.layout, "blobs"), dst)
                os.symlink(dst, os.path.join(self.layout, "blobs"))
            self.opened.append((os.fsdecode(path) if not isinstance(path, int) else path, dir_fd))
            return real_open(path, flags, mode, dir_fd=dir_fd)

        with mock.patch("os.open", swapping_open):
            with self.assertRaises(oci_digest.ArchiveError) as cm:
                oci_digest.inspect(self.layout)
        self.assertTrue(swapped["done"], "the substitution happened during the read")
        self.assertIn("blobs is a symlink", str(cm.exception))
        self.assertFalse(any(isinstance(o[0], str) and o[0].startswith(self.outside) for o in self.opened))
        r = self.policy()
        self.assertEqual(r.returncode, 2)
        self.assertNotIn("trivy_policy: PASS", r.stdout)

    def test_11_invalid_paths_rejected(self):
        with oci_digest._open(self.layout) as src:
            for bad in ("../index.json", "/etc/passwd", "blobs/../index.json", "blobs//sha256", "./index.json", ""):
                with self.subTest(bad=bad):
                    with self.assertRaises(oci_digest.ArchiveError) as cm:
                        src.read(bad)
                    self.assertIn("not a plain relative layout path", str(cm.exception))
            with self.assertRaises(oci_digest.ArchiveError):
                src.read("blobs")  # a directory where a file is expected
        # A FIFO where a blob should be is not a regular file.
        fifo = os.path.join(self.layout, "blobs", "sha256", self.want["config_digest"][7:])
        os.unlink(fifo)
        os.mkfifo(fifo)
        with self.assertRaises(oci_digest.ArchiveError) as cm:
            oci_digest.inspect(self.layout)
        self.assertIn("not a regular file", str(cm.exception))

    # ------------------------------------------------- GB2C-07R: root spellings --
    def policy_for(self, name: str) -> subprocess.CompletedProcess:
        """The policy with ArtifactName and --expect-layout both set to `name`."""
        rep = trivy_report()
        rep["ArtifactName"], rep["Metadata"]["ImageID"] = name, self.want["config_digest"]
        p = os.path.join(self.tmp, "report-root.json")
        with open(p, "w") as f:
            json.dump(rep, f)
        return subprocess.run([sys.executable, os.path.join(HERE, "trivy_policy.py"), p, "--require-identity", "--expect-layout", p, name, self.archive],
                              capture_output=True, text=True, cwd=self.tmp)

    def assertRootRefused(self, name: str, fragment: str, *, cwd=None):
        self.opened.clear()
        with mock.patch("os.open", self.recording_open):
            with self.assertRaises(oci_digest.ArchiveError) as cm:
                if cwd:
                    old = os.getcwd(); os.chdir(cwd)
                    try:
                        oci_digest.inspect(name)
                    finally:
                        os.chdir(old)
                else:
                    oci_digest.inspect(name)
        self.assertIn(fragment, str(cm.exception))
        self.assertFalse(any(isinstance(o[0], str) and o[0].startswith(self.outside) for o in self.opened), "the moved content was never opened")
        self.assertTrue(all(o[1] is not None or o[0] in ("/", ".") for o in self.opened), "only '/' or '.' is opened by path; everything else relative to a descriptor")
        r = self.policy_for(name)
        self.assertEqual(r.returncode, 2, name + ": " + r.stdout + r.stderr)
        self.assertNotIn("trivy_policy: PASS", r.stdout)
        self.assertIn("no scan evidence", r.stderr)

    def symlink_root(self) -> str:
        real = os.path.join(self.tmp, "outside")
        shutil.move(self.layout, real)
        os.symlink(real, self.layout)
        return real

    def test_13_canonical_absolute_and_relative_roots_accepted(self):
        self.assertEqual(oci_digest.inspect(self.layout)["config_digest"], self.want["config_digest"])
        self.assertEqual(self.policy_for(self.layout).returncode, 0)
        rel = os.path.relpath(self.layout, self.tmp)  # layout/gb-server, from self.tmp
        old = os.getcwd(); os.chdir(self.tmp)
        try:
            self.assertEqual(oci_digest.inspect(rel)["config_digest"], self.want["config_digest"])
        finally:
            os.chdir(old)
        r = self.policy_for(rel)  # cwd=self.tmp
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("trivy_policy: PASS", r.stdout)

    def test_14_symlinked_root_rejected_under_every_spelling(self):
        """The reviewer's reproduction: `layout`, `layout/` and `layout/.`."""
        self.symlink_root()
        self.assertRootRefused(self.layout, "is a symlink")
        for suffix in ("/", "/.", "//", "/./", "/./."):
            with self.subTest(suffix=suffix):
                self.assertRootRefused(self.layout + suffix, "not a canonical directory path")

    def test_15_symlinked_parent_directory_rejected(self):
        parent = os.path.dirname(self.layout)  # <tmp>/layout
        real_parent = os.path.join(self.tmp, "outside")
        shutil.move(parent, real_parent)
        os.symlink(real_parent, parent)
        self.assertRootRefused(self.layout, f"{parent} is a symlink")

    def test_16_redundant_separators_and_dot_components_rejected(self):
        for bad in (self.layout.replace("/layout/", "//layout/"), self.layout.replace("/layout/", "/layout/./"), "/" + self.layout, "./" + os.path.relpath(self.layout, self.tmp)):
            with self.subTest(bad=bad):
                self.assertRootRefused(bad, "not a canonical directory path", cwd=self.tmp)

    def test_17_parent_directory_traversal_rejected(self):
        for bad in (os.path.join(self.tmp, "layout", "..", "layout", "gb-server"), os.path.join(self.layout, "..", "gb-server"), "../" + os.path.basename(self.tmp) + "/layout/gb-server"):
            with self.subTest(bad=bad):
                self.assertRootRefused(bad, "not a canonical directory path", cwd=self.tmp)

    def test_18_root_substituted_between_validation_and_open_fails_safely(self):
        """The root is real when the pathname is validated and a symlink when its last
        component is opened: the descriptor-relative O_NOFOLLOW open refuses it."""
        real_open = os.open
        leaf = os.path.basename(self.layout)
        swapped = {"done": False}

        def swapping_open(path, flags, mode=0o777, *, dir_fd=None):
            if dir_fd is not None and os.fsdecode(path) == leaf and not swapped["done"]:
                swapped["done"] = True
                self.symlink_root()
            self.opened.append((os.fsdecode(path) if not isinstance(path, int) else path, dir_fd))
            return real_open(path, flags, mode, dir_fd=dir_fd)

        with mock.patch("os.open", swapping_open):
            with self.assertRaises(oci_digest.ArchiveError) as cm:
                oci_digest.inspect(self.layout)
        self.assertTrue(swapped["done"])
        self.assertIn(f"{self.layout} is a symlink", str(cm.exception))
        self.assertFalse(any(isinstance(o[0], str) and o[0].startswith(self.outside) for o in self.opened))
        r = self.policy_for(self.layout)  # now a plain symlinked root
        self.assertEqual(r.returncode, 2)
        self.assertNotIn("trivy_policy: PASS", r.stdout)

    def test_19_artifact_name_must_equal_the_input_string(self):
        """A report naming `layout/` while the policy is told `layout` (or vice versa) is not
        evidence about the same input; no normalisation is applied."""
        rep = trivy_report()
        rep["ArtifactName"], rep["Metadata"]["ImageID"] = self.layout + "/", self.want["config_digest"]
        p = os.path.join(self.tmp, "report-alias.json")
        with open(p, "w") as f:
            json.dump(rep, f)
        r = subprocess.run([sys.executable, os.path.join(HERE, "trivy_policy.py"), p, "--require-identity", "--expect-layout", p, self.layout, self.archive], capture_output=True, text=True)
        self.assertEqual(r.returncode, 2)
        self.assertIn("is not the scanned layout directory", r.stderr)
        self.assertNotIn("trivy_policy: PASS", r.stdout)

    def test_12_archive_validation_and_materialisation_unchanged(self):
        rec = oci_digest.inspect(self.archive)
        self.assertEqual(rec["config_digest"], self.want["config_digest"])
        self.assertEqual(rec["layer_count"], 2)
        # The materialiser reads its own output through the confined reader: making that reader
        # refuse makes materialisation fail, so it cannot bypass the stricter contract.
        other = os.path.join(self.tmp, "layout", "again")
        with mock.patch.object(oci_digest._DirSource, "read", side_effect=oci_digest.ArchiveError("confined reader refused")):
            with self.assertRaises(oci_digest.ArchiveError) as cm:
                oci_layout_dir.materialise(self.archive, other)
        self.assertIn("confined reader refused", str(cm.exception))
        self.assertFalse(os.path.exists(other))
        # Descriptors are released: materialise and inspect many times without leaking.
        for i in range(64):
            oci_digest.inspect(self.layout)
        self.assertTrue(oci_digest._confined_reads_supported())


class ScannerInputWiringTests(unittest.TestCase):
    """Both workflows must scan the materialised layout directory, never the .oci.tar, and the
    policy step must re-verify that directory against the archive (hosted run 41250221737)."""

    TRIVY_ACTION = "aquasecurity/trivy-action@"

    def workflow(self, name: str) -> dict:
        import yaml
        with open(os.path.join(REPO, ".github", "workflows", name)) as f:
            return yaml.safe_load(f)

    def check(self, name: str, job: str):
        steps = self.workflow(name)["jobs"][job]["steps"]
        scans = [s for s in steps if str(s.get("uses", "")).startswith(self.TRIVY_ACTION)]
        self.assertEqual(len(scans), 2, f"{name}: two Trivy scans expected")
        scan_inputs = []
        for s in scans:
            inp = s["with"]["input"]
            self.assertFalse(inp.endswith(".oci.tar"), f"{name}: {inp} is a tarball; Trivy --input cannot read an OCI layout tarball")
            self.assertRegex(inp, r"^\$\{\{ runner\.temp \}\}/layout/(gb-server|gbctl)$")
            self.assertEqual(s["with"]["scan-type"], "image")
            self.assertEqual(s["with"]["scanners"], "vuln,secret")
            scan_inputs.append(inp)
        self.assertEqual(sorted(scan_inputs), ["${{ runner.temp }}/layout/gb-server", "${{ runner.temp }}/layout/gbctl"])
        first_scan = min(steps.index(s) for s in scans)
        materialise = [i for i, s in enumerate(steps) if "oci_layout_dir.py" in str(s.get("run", ""))]
        self.assertTrue(materialise and materialise[0] < first_scan, f"{name}: the layout directories must be materialised before the scans")
        m = steps[materialise[0]]
        self.assertEqual(m["env"]["LAYOUT_DIR"], "${{ runner.temp }}/layout")
        self.assertIn('oci_layout_dir.py "$IMAGES_DIR/$t.oci.tar" "$LAYOUT_DIR/$t"', m["run"])
        policy = [s for s in steps if "trivy_policy.py" in str(s.get("run", ""))]
        self.assertEqual(len(policy), 1)
        run = policy[0]["run"]
        self.assertGreater(steps.index(policy[0]), max(steps.index(s) for s in scans), "policy runs after both scans")
        self.assertEqual(policy[0]["env"]["LAYOUT_DIR"], "${{ runner.temp }}/layout")
        self.assertIn("--require-identity", run)
        for t in ("gb-server", "gbctl"):
            self.assertIn(f'--expect-layout "$SCAN_DIR/trivy-{t}.json" "$LAYOUT_DIR/{t}" "$IMAGES_DIR/{t}.oci.tar"', run)
        self.assertNotIn("--expect ", run.replace("--expect-layout", ""))

    def test_ci_image_scan_job(self):
        self.check("ci.yml", "image-scan")

    def test_publish_job(self):
        self.check("publish-images.yml", "publish")


# --------------------------------------------------------- set_image_digest --
class SetImageDigestTests(unittest.TestCase):
    def setUp(self):
        with open(DEV_ENTRY, encoding="utf-8") as f:
            self.text = f.read()

    def test_replaces_only_the_digest_line(self):
        new, old, dig = set_image_digest.set_digest(self.text, IMAGE, GOOD)
        self.assertEqual(old, set_image_digest.SENTINEL)
        self.assertEqual(dig, GOOD)
        a, b = self.text.splitlines(), new.splitlines()
        self.assertEqual(len(a), len(b))
        changed = [i for i in range(len(a)) if a[i] != b[i]]
        self.assertEqual(len(changed), 1)
        self.assertIn("digest:", a[changed[0]])
        self.assertEqual(b[changed[0]], a[changed[0]].replace(set_image_digest.SENTINEL, GOOD))
        # Idempotent and detectable as a no-op.
        new2, old2, _ = set_image_digest.set_digest(new, IMAGE, GOOD)
        self.assertEqual(new2, new)
        self.assertEqual(old2, GOOD)
        # And replaceable again by a later digest.
        new3, old3, _ = set_image_digest.set_digest(new, IMAGE, GOOD2)
        self.assertEqual(old3, GOOD)
        self.assertIn(GOOD2, new3)

    def test_invalid_digests(self):
        for bad in ["sha256:" + "0" * 64, "sha256:" + "ab" * 31, "sha256:" + "AB" * 32, "ab" * 32, "sha512:" + "ab" * 32,
                    "sha256:" + "ab" * 32 + "\n", f"{IMAGE}@sha256:" + "ab" * 32, "", "latest"]:
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                set_image_digest.set_digest(self.text, IMAGE, bad)

    def test_wrong_image_name(self):
        with self.assertRaises(set_image_digest.ShapeError):
            set_image_digest.set_digest(self.text, "ghcr.io/phreddrick/gbctl", GOOD)

    def test_tag_entry_refused(self):
        tagged = self.text.replace("    digest: " + set_image_digest.SENTINEL, "    newTag: v1\n    digest: " + set_image_digest.SENTINEL)
        with self.assertRaises(set_image_digest.ShapeError):
            set_image_digest.set_digest(tagged, IMAGE, GOOD)

    def test_duplicate_entry_refused(self):
        dup = self.text.replace("images:\n", "images:\n  - name: ghcr.io/phreddrick/gb-server\n    digest: " + GOOD2 + "\n", 1)
        with self.assertRaises(set_image_digest.ShapeError):
            set_image_digest.set_digest(dup, IMAGE, GOOD)

    def test_cli_exit_codes(self):
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp)
        p = os.path.join(tmp, "kustomization.yaml")
        shutil.copy(DEV_ENTRY, p)
        cli = [sys.executable, os.path.join(HERE, "set_image_digest.py"), "--file", p, "--image", IMAGE]
        self.assertEqual(subprocess.run(cli + ["--check"], capture_output=True, text=True).stdout.strip(), set_image_digest.SENTINEL)
        self.assertEqual(subprocess.run(cli + ["--digest", "sha256:dead"], capture_output=True).returncode, 1)
        self.assertEqual(subprocess.run(cli + ["--digest", GOOD], capture_output=True).returncode, 0)
        self.assertEqual(subprocess.run(cli + ["--digest", GOOD], capture_output=True).returncode, 3)
        with open(p, encoding="utf-8") as f:
            self.assertIn(GOOD, f.read())
        self.assertEqual(subprocess.run(cli[:-2] + ["--image", "ghcr.io/other/x", "--digest", GOOD2], capture_output=True).returncode, 2)


# ------------------------------------------------------------- trivy_policy --
IMAGE_ID = "sha256:" + "ef" * 32


def trivy_report(vulns=(), secrets=()):
    """A report shaped like Trivy v0.76.1 output for an OCI archive (see test_release_safety.py for the negative shapes)."""
    result = {"Target": "gb-server.oci.tar (debian 12.12)", "Class": "os-pkgs", "Type": "debian"}
    if vulns:
        result["Vulnerabilities"] = list(vulns)
    if secrets:
        result["Secrets"] = list(secrets)
    return {"SchemaVersion": 2, "CreatedAt": "2026-10-30T00:00:00Z", "ArtifactName": "gb-server.oci.tar",
            "ArtifactType": "container_image", "Metadata": {"ImageID": IMAGE_ID, "OS": {"Family": "debian", "Name": "12.12"}},
            "Results": [result]}


SECRET = {"RuleID": "aws-access-key-id", "Category": "AWS", "Title": "AWS Access Key ID", "Severity": "CRITICAL"}


class TrivyPolicyTests(unittest.TestCase):
    def test_critical_fixable_fails(self):
        counts, viol, sec, total = trivy_policy.evaluate(trivy_report([{"VulnerabilityID": "CVE-1", "Severity": "CRITICAL", "FixedVersion": "1.2", "PkgName": "p", "InstalledVersion": "1.0"}]))
        self.assertEqual(counts["CRITICAL"]["fixable"], 1)
        self.assertEqual(total, 1)
        self.assertEqual(len(viol), 1)
        self.assertEqual(sec, [])

    def test_critical_unfixable_and_high_fixable_pass(self):
        counts, viol, _, _ = trivy_policy.evaluate(trivy_report([
            {"VulnerabilityID": "CVE-2", "Severity": "CRITICAL", "FixedVersion": "", "PkgName": "p", "InstalledVersion": "1.0"},
            {"VulnerabilityID": "CVE-3", "Severity": "HIGH", "FixedVersion": "2", "PkgName": "q", "InstalledVersion": "1"},
        ]))
        self.assertEqual(counts["CRITICAL"]["unfixable"], 1)
        self.assertEqual(counts["HIGH"]["fixable"], 1)
        self.assertEqual(viol, [])

    def test_secret_fails(self):
        _, viol, sec, _ = trivy_policy.evaluate(trivy_report(secrets=[SECRET]))
        self.assertEqual(viol, [])
        self.assertEqual(len(sec), 1)

    def test_cli(self):
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp)
        cli = [sys.executable, os.path.join(HERE, "trivy_policy.py")]
        good, bad, junk, empty = (os.path.join(tmp, n) for n in ("good.json", "bad.json", "junk.json", "empty.json"))
        with open(good, "w") as f:
            json.dump(trivy_report(), f)
        with open(bad, "w") as f:
            json.dump(trivy_report([{"VulnerabilityID": "CVE-1", "Severity": "CRITICAL", "FixedVersion": "1.2", "PkgName": "p", "InstalledVersion": "1.0"}]), f)
        with open(junk, "w") as f:
            f.write("{not json")
        open(empty, "w").close()
        summary = os.path.join(tmp, "summary.md")
        self.assertEqual(subprocess.run(cli + [good, "--summary", summary], capture_output=True).returncode, 0)
        self.assertTrue(os.path.getsize(summary) > 0)
        # Identity asserted: the expected archive name and config digest must match the report.
        self.assertEqual(subprocess.run(cli + [good, "--require-identity", "--expect", good, "gb-server.oci.tar", IMAGE_ID], capture_output=True).returncode, 0)
        self.assertEqual(subprocess.run(cli + [good, "--require-identity"], capture_output=True).returncode, 2)
        self.assertEqual(subprocess.run(cli + [good, "--expect", good, "gb-server.oci.tar", "sha256:" + "00" * 32], capture_output=True).returncode, 2)
        self.assertEqual(subprocess.run(cli + [good, bad], capture_output=True).returncode, 1)
        self.assertEqual(subprocess.run(cli + [junk], capture_output=True).returncode, 2)
        self.assertEqual(subprocess.run(cli + [empty], capture_output=True).returncode, 2)
        self.assertEqual(subprocess.run(cli + [os.path.join(tmp, "missing.json")], capture_output=True).returncode, 2)

    def test_layout_identity(self):
        """--expect-layout: ArtifactName is the scanned layout directory, which must still be the
        archive's image, and ImageID the archive's config digest (Trivy's archiveImage.Name() and ID())."""
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp)
        archive = os.path.join(tmp, "gb-server.oci.tar")
        want = make_oci_archive(archive)
        layout = os.path.join(tmp, "layout", "gb-server")
        os.mkdir(os.path.join(tmp, "layout"))
        oci_layout_dir.materialise(archive, layout)
        other = os.path.join(tmp, "other.oci.tar")
        make_oci_archive(other, layers=[LAYER_B])
        other_layout = os.path.join(tmp, "layout", "other")
        oci_layout_dir.materialise(other, other_layout)
        cli = [sys.executable, os.path.join(HERE, "trivy_policy.py")]

        counter = iter(range(1000))

        def report(name=layout, image_id=want["config_digest"]):
            rep = trivy_report()
            rep["ArtifactName"], rep["Metadata"]["ImageID"] = name, image_id
            p = os.path.join(tmp, f"report-{next(counter)}.json")
            with open(p, "w") as f:
                json.dump(rep, f)
            return p

        def run(p, *args):
            return subprocess.run(cli + [p, *args], capture_output=True, text=True)

        good = report()
        ok = run(good, "--require-identity", "--expect-layout", good, layout, archive)
        self.assertEqual(ok.returncode, 0, ok.stderr)
        self.assertIn("re-verified as the image of", ok.stdout)
        cases = {
            "ArtifactName is the tarball, not the directory": (report(name=archive), layout, archive, "not the scanned layout directory"),
            "ArtifactName is another directory": (report(name=other_layout), layout, archive, "not the scanned layout directory"),
            "ImageID is not the archive config digest": (report(image_id="sha256:" + "11" * 32), layout, archive, "a different image was scanned"),
            "ImageID is the manifest digest": (report(image_id=want["manifest_digest"]), layout, archive, "a different image was scanned"),
            "layout directory is another image": (report(name=other_layout), other_layout, archive, "differs from the archive's"),
            "layout directory missing": (report(name=os.path.join(tmp, "layout", "gone")), os.path.join(tmp, "layout", "gone"), archive, "missing or not a directory"),
            "archive missing": (good, layout, os.path.join(tmp, "none.tar"), "cannot re-verify"),
            "directory with a reference separator": (report(name=layout + ":ci"), layout + ":ci", archive, "':' or '@'"),
        }
        for label, (p, d, a, fragment) in cases.items():
            with self.subTest(label=label):
                r = run(p, "--require-identity", "--expect-layout", p, d, a)
                self.assertEqual(r.returncode, 2, label + ": " + r.stdout + r.stderr)
                self.assertIn(fragment, r.stderr)
                self.assertIn("no scan evidence", r.stderr)
        # Tampering with the scanned directory after the scan is detected at policy time.
        blob = os.path.join(layout, "blobs", "sha256", want["config_digest"][7:])
        os.chmod(blob, 0o600)
        with open(blob, "ab") as f:
            f.write(b" ")
        r = run(good, "--expect-layout", good, layout, archive)
        self.assertEqual(r.returncode, 2)
        self.assertIn("cannot re-verify", r.stderr)
        # A report may not carry both kinds of expectation; --expect alone keeps its old meaning.
        self.assertEqual(run(good, "--expect", good, layout, want["config_digest"], "--expect-layout", good, layout, archive).returncode, 2)
        self.assertEqual(run(good, "--expect", good, layout, want["config_digest"]).returncode, 0)


# ---------------------------------------------------- workflow_policy_check --
CHECKOUT = "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1"
MINIMAL = textwrap.dedent(f"""\
    name: t
    on:
      pull_request:
    permissions:
      contents: read
    jobs:
      a:
        name: build, test, lint (fresh checkout)
        runs-on: ubuntu-26.04
        timeout-minutes: 5
        permissions:
          contents: read
        steps:
          - uses: {CHECKOUT}
            with:
              persist-credentials: false
          - run: echo "$X"
            env:
              X: ${{{{ github.event.pull_request.title }}}}
      b:
        name: docker build from checkout (no push)
        runs-on: ubuntu-26.04
        timeout-minutes: 5
        permissions:
          contents: read
        steps:
          - run: true
    """)
PUBLISH_OK = textwrap.dedent(f"""\
    name: p
    on:
      workflow_dispatch:
    permissions:
      contents: read
    concurrency:
      group: ghcr-publish
      cancel-in-progress: false
    jobs:
      preflight:
        runs-on: ubuntu-26.04
        timeout-minutes: 5
        permissions:
          contents: read
          actions: read
        steps:
          - run: true
      publish:
        if: github.ref == 'refs/heads/master'
        environment: ghcr-publish
        runs-on: ubuntu-26.04
        timeout-minutes: 5
        permissions:
          contents: read
          packages: write
        steps:
          - uses: {CHECKOUT}
            with:
              persist-credentials: false
    """)


class WorkflowPolicyTests(unittest.TestCase):
    def _run(self, files: dict[str, str]) -> int:
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp)
        d = os.path.join(tmp, ".github", "workflows")
        os.makedirs(d)
        for n, t in files.items():
            with open(os.path.join(d, n), "w") as f:
                f.write(t)
        with open(os.devnull, "w") as devnull:
            old = sys.stdout
            sys.stdout = devnull
            try:
                return workflow_policy_check.run(d)
            finally:
                sys.stdout = old

    def test_repository_workflows_pass(self):
        self.assertEqual(workflow_policy_check.run(os.path.join(REPO, ".github", "workflows")), 0)

    def test_minimal_pass(self):
        self.assertEqual(self._run({"ci.yml": MINIMAL, "publish-images.yml": PUBLISH_OK}), 0)

    def test_violations(self):
        cases = {
            "W1 pull_request_target": MINIMAL.replace("pull_request:", "pull_request_target:"),
            "W2 missing job permissions": MINIMAL.replace("    permissions:\n      contents: read\n    steps:\n      - run: true", "    steps:\n      - run: true"),
            "W2 write in ci": MINIMAL.replace("      contents: read\n    steps:\n      - run: true", "      contents: write\n    steps:\n      - run: true"),
            "W3 unpinned": MINIMAL.replace(CHECKOUT, "actions/checkout@v7"),
            "W3 no version comment": MINIMAL.replace(" # v7.0.1", ""),
            "W4 no timeout": MINIMAL.replace("    timeout-minutes: 5\n    permissions:\n      contents: read\n    steps:\n      - run: true", "    permissions:\n      contents: read\n    steps:\n      - run: true"),
            "W4 wrong runner": MINIMAL.replace("ubuntu-26.04", "ubuntu-latest", 1),
            "W5 injection": MINIMAL.replace('- run: echo "$X"', '- run: echo "${{ github.event.pull_request.title }}"'),
            "W5 inputs in run": MINIMAL.replace('- run: echo "$X"', '- run: echo "${{ inputs.digest }}"'),
            "W6 PAT": MINIMAL.replace("X: ${{ github.event.pull_request.title }}", "X: ${{ secrets.GHCR_PAT }}"),
            "W7 persist credentials": MINIMAL.replace("persist-credentials: false", "persist-credentials: true"),
            "W8 required name": MINIMAL.replace("name: docker build from checkout (no push)", "name: docker build"),
        }
        for label, text in cases.items():
            with self.subTest(label):
                self.assertNotEqual(text, MINIMAL, "replacement did not apply")
                self.assertEqual(self._run({"ci.yml": text, "publish-images.yml": PUBLISH_OK}), 1)
        pub_cases = {
            "publish on push": PUBLISH_OK.replace("  workflow_dispatch:", "  push:\n    branches: [master]"),
            "publish without environment": PUBLISH_OK.replace("    environment: ghcr-publish\n", ""),
            "publish without master guard": PUBLISH_OK.replace("    if: github.ref == 'refs/heads/master'\n", ""),
            "publish without concurrency": PUBLISH_OK.replace("concurrency:\n  group: ghcr-publish\n  cancel-in-progress: false\n", ""),
            "second packages write": PUBLISH_OK.replace("      actions: read", "      packages: write"),
            "preflight with checks instead of actions": PUBLISH_OK.replace("      actions: read", "      checks: read"),
        }
        for label, text in pub_cases.items():
            with self.subTest(label):
                self.assertNotEqual(text, PUBLISH_OK, "replacement did not apply")
                self.assertEqual(self._run({"ci.yml": MINIMAL, "publish-images.yml": text}), 1)
        # No publishing job at all is also a failure: the policy expects exactly one.
        self.assertEqual(self._run({"ci.yml": MINIMAL}), 1)


if __name__ == "__main__":
    unittest.main()
