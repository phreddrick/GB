#!/usr/bin/env python3
"""Regression tests for docker_image_identity.py (hosted run 41231194459: the
runner's Docker engine reported the loaded image's Id as the OCI manifest
digest, not the config digest image-checks.sh assumed). Synthetic data only:

    python3 -m unittest discover -s .github/scripts -p 'test_*.py' -v

Each case builds a buildx-style OCI archive (gzip layer) and a `docker image
save` archive in one of the layouts Docker writes: the containerd image store
(OCI layout plus manifest.json, compressed layer blobs, Id = manifest digest),
the classic store on Docker 25+ (manifest.json plus a Docker-generated OCI
index, uncompressed layers, Id = config digest) and the legacy layout
(<config>.json, <id>/layer.tar, no index).

OciLayerIdentityTests (GB2C-06) build OCI archives whose blobs and manifest are
internally consistent but whose layers do not decode to the config's
rootfs.diff_ids, including the independent reviewer's A/B reproduction. The
binding itself now lives in the shared validator (oci_digest._verify_layers,
run by oci_digest.inspect for every caller); these tests prove that verify()
still refuses each such archive through it, and test_scripts.py
SharedValidatorLayerTests prove the same for inspect() called directly.
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
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import docker_image_identity as dii  # noqa: E402
import oci_digest  # noqa: E402
from oci_digest import ArchiveError  # noqa: E402

TAG = "gb-server:ci"
OTHER = "sha256:" + "ef" * 32


def _digest(b: bytes) -> str:
    return "sha256:" + hashlib.sha256(b).hexdigest()


def _layer_tar(content: bytes) -> bytes:
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w", format=tarfile.PAX_FORMAT) as tar:
        ti = tarfile.TarInfo("usr/local/bin/gb-server")
        ti.size, ti.mode, ti.mtime = len(content), 0o755, 0
        tar.addfile(ti, io.BytesIO(content))
    return buf.getvalue()


def _write_tar(path: str, files: dict) -> None:
    with tarfile.open(path, "w") as tar:
        for name, data in files.items():
            ti = tarfile.TarInfo(name)
            ti.size = len(data)
            tar.addfile(ti, io.BytesIO(data))


GZIP_LAYER = "application/vnd.oci.image.layer.v1.tar+gzip"
TAR_LAYER = "application/vnd.oci.image.layer.v1.tar"


def _oci_archive(path: str, config: bytes, descs: list, blobs: list) -> str:
    """A buildx-style OCI archive whose manifest lists `descs` (layer descriptors) and
    which stores `blobs`; every stored blob hashes to its name. Returns the manifest digest."""
    manifest = json.dumps({
        "schemaVersion": 2, "mediaType": oci_digest.IMAGE_MANIFEST,
        "config": {"mediaType": "application/vnd.oci.image.config.v1+json", "digest": _digest(config), "size": len(config)},
        "layers": descs,
    }).encode()
    index = {"schemaVersion": 2, "manifests": [{"mediaType": oci_digest.IMAGE_MANIFEST, "digest": _digest(manifest), "size": len(manifest)}]}
    files = {"oci-layout": b'{"imageLayoutVersion":"1.0.0"}', "index.json": json.dumps(index).encode(),
             f"blobs/sha256/{_digest(config)[7:]}": config, f"blobs/sha256/{_digest(manifest)[7:]}": manifest}
    for blob in blobs:
        files[f"blobs/sha256/{_digest(blob)[7:]}"] = blob
    _write_tar(path, files)
    return _digest(manifest)


def _desc(media_type: str, blob: bytes, **extra) -> dict:
    return {"mediaType": media_type, "digest": _digest(blob), "size": len(blob), **extra}


class Image:
    """One synthetic image: uncompressed layer, its gzip blob, config, manifest."""

    def __init__(self, label: str = "a"):
        self.layer = _layer_tar(b"\x7fELF" + label.encode() * 64)
        self.layer_gz = gzip.compress(self.layer, mtime=0)
        self.config = json.dumps({
            "architecture": "amd64", "os": "linux",
            "config": {"User": "65532:65532", "Entrypoint": ["/usr/local/bin/gb-server"], "Labels": {"t": label}},
            "rootfs": {"type": "layers", "diff_ids": [_digest(self.layer)]},
        }).encode()
        self.manifest = self._manifest(oci_digest.IMAGE_MANIFEST, self.layer_gz, "application/vnd.oci.image.layer.v1.tar+gzip")
        self.config_digest = _digest(self.config)
        self.manifest_digest = _digest(self.manifest)

    def _manifest(self, media_type: str, layer: bytes, layer_type: str) -> bytes:
        return json.dumps({
            "schemaVersion": 2, "mediaType": media_type,
            "config": {"mediaType": "application/vnd.oci.image.config.v1+json", "digest": _digest(self.config), "size": len(self.config)},
            "layers": [{"mediaType": layer_type, "digest": _digest(layer), "size": len(layer)}],
        }).encode()

    def oci_archive(self, path: str, *, corrupt_layer: bool = False) -> None:
        index = {"schemaVersion": 2, "manifests": [{"mediaType": oci_digest.IMAGE_MANIFEST, "digest": self.manifest_digest, "size": len(self.manifest)}]}
        _write_tar(path, {
            "oci-layout": b'{"imageLayoutVersion":"1.0.0"}',
            "index.json": json.dumps(index).encode(),
            f"blobs/sha256/{self.config_digest[7:]}": self.config,
            f"blobs/sha256/{self.manifest_digest[7:]}": self.manifest,
            f"blobs/sha256/{_digest(self.layer_gz)[7:]}": (b"X" + self.layer_gz[1:]) if corrupt_layer else self.layer_gz,
        })

    def saved_containerd(self, path: str, *, tags=(TAG,), layer_bytes: bytes | None = None, extra_entry: bool = False,
                         drop_manifest: bool = False) -> None:
        """`docker image save` on the containerd image store."""
        layer = self.layer_gz if layer_bytes is None else layer_bytes
        layer_name = f"blobs/sha256/{_digest(self.layer_gz)[7:]}"
        entry = {"Config": f"blobs/sha256/{self.config_digest[7:]}", "RepoTags": list(tags), "Layers": [layer_name]}
        index = {"schemaVersion": 2, "manifests": [] if drop_manifest else [
            {"mediaType": oci_digest.IMAGE_MANIFEST, "digest": self.manifest_digest, "size": len(self.manifest),
             "annotations": {"io.containerd.image.name": "docker.io/library/" + TAG}}]}
        files = {
            "oci-layout": b'{"imageLayoutVersion":"1.0.0"}',
            "index.json": json.dumps(index).encode(),
            "manifest.json": json.dumps([entry, entry] if extra_entry else [entry]).encode(),
            f"blobs/sha256/{self.config_digest[7:]}": self.config,
            layer_name: layer,
        }
        if not drop_manifest:
            files[f"blobs/sha256/{self.manifest_digest[7:]}"] = self.manifest
        _write_tar(path, files)

    def saved_classic(self, path: str) -> None:
        """`docker image save` on the classic store (Docker 25+): uncompressed layers, Docker's own manifest."""
        docker_manifest = self._manifest(oci_digest.IMAGE_MANIFEST, self.layer, "application/vnd.oci.image.layer.v1.tar")
        layer_name = f"blobs/sha256/{_digest(self.layer)[7:]}"
        index = {"schemaVersion": 2, "manifests": [{"mediaType": oci_digest.IMAGE_MANIFEST, "digest": _digest(docker_manifest), "size": len(docker_manifest)}]}
        _write_tar(path, {
            "oci-layout": b'{"imageLayoutVersion":"1.0.0"}',
            "index.json": json.dumps(index).encode(),
            "manifest.json": json.dumps([{"Config": f"blobs/sha256/{self.config_digest[7:]}", "RepoTags": [TAG], "Layers": [layer_name]}]).encode(),
            f"blobs/sha256/{self.config_digest[7:]}": self.config,
            f"blobs/sha256/{_digest(docker_manifest)[7:]}": docker_manifest,
            layer_name: self.layer,
        })

    def saved_legacy(self, path: str, *, layer_bytes: bytes | None = None) -> None:
        """`docker save` before Docker 25: <config hex>.json and <id>/layer.tar, no OCI index."""
        _write_tar(path, {
            "manifest.json": json.dumps([{"Config": f"{self.config_digest[7:]}.json", "RepoTags": [TAG], "Layers": ["0123abcd/layer.tar"]}]).encode(),
            f"{self.config_digest[7:]}.json": self.config,
            "0123abcd/layer.tar": self.layer if layer_bytes is None else layer_bytes,
        })


class DockerImageIdentityTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.tmp)
        self.img = Image("a")
        self.oci = self.p("img.oci.tar")
        self.img.oci_archive(self.oci)
        self.saved = self.p("saved.tar")

    def p(self, name: str) -> str:
        return os.path.join(self.tmp, name)

    def assertRejected(self, docker_id: str, fragment: str):
        with self.assertRaises(ArchiveError) as cm:
            dii.verify(self.oci, self.saved, docker_id, TAG)
        self.assertIn(fragment, str(cm.exception))

    # ---------------------------------------------------- accepted identities --
    def test_containerd_store_manifest_id_accepted(self):
        """The hosted-runner case: Id is the OCI manifest digest."""
        self.img.saved_containerd(self.saved)
        rec = dii.verify(self.oci, self.saved, self.img.manifest_digest, TAG)
        self.assertEqual(rec["docker_id_kind"], "manifest")
        self.assertTrue(rec["saved_manifest_verified"])
        self.assertEqual(rec["saved_layers_verified"], 1)
        self.assertEqual((rec["manifest_digest"], rec["config_digest"]), (self.img.manifest_digest, self.img.config_digest))
        self.assertNotEqual(rec["manifest_digest"], rec["config_digest"])

    def test_containerd_store_config_id_accepted(self):
        self.img.saved_containerd(self.saved)
        self.assertEqual(dii.verify(self.oci, self.saved, self.img.config_digest, TAG)["docker_id_kind"], "config")

    def test_classic_store_config_id_accepted(self):
        self.img.saved_classic(self.saved)
        rec = dii.verify(self.oci, self.saved, self.img.config_digest, TAG)
        self.assertEqual(rec["docker_id_kind"], "config")
        self.assertFalse(rec["saved_manifest_verified"])  # Docker rewrote the manifest (uncompressed layers)

    def test_legacy_save_layout_config_id_accepted(self):
        self.img.saved_legacy(self.saved)
        self.assertEqual(dii.verify(self.oci, self.saved, self.img.config_digest, TAG)["docker_id_kind"], "config")

    def test_fully_qualified_repo_tag_accepted(self):
        self.img.saved_containerd(self.saved, tags=("docker.io/library/" + TAG,))
        dii.verify(self.oci, self.saved, self.img.manifest_digest, TAG)

    # ---------------------------------------------------- rejected identities --
    def test_unrelated_id_rejected(self):
        self.img.saved_containerd(self.saved)
        self.assertRejected(OTHER, "neither the archive manifest digest")

    def test_malformed_id_rejected(self):
        self.img.saved_containerd(self.saved)
        for bad in ("", "sha256:abc", self.img.manifest_digest.upper(), self.img.manifest_digest[7:], "sha512:" + "a" * 128):
            with self.subTest(bad=bad):
                self.assertRejected(bad, "is not a sha256 digest")

    def test_manifest_id_without_saved_manifest_rejected(self):
        """An Id equal to the manifest digest is not trusted unless the loaded image really holds that manifest."""
        self.img.saved_classic(self.saved)
        self.assertRejected(self.img.manifest_digest, "does not contain that manifest")
        self.img.saved_containerd(self.saved, drop_manifest=True)
        self.assertRejected(self.img.manifest_digest, "does not contain that manifest")
        self.img.saved_legacy(self.saved)
        self.assertRejected(self.img.manifest_digest, "does not contain that manifest")

    def test_different_loaded_image_rejected_for_either_id(self):
        """Docker reports this archive's digest but holds other content (another build, same tag)."""
        other = Image("b")
        other.saved_containerd(self.saved)
        for docker_id in (self.img.manifest_digest, self.img.config_digest):
            with self.subTest(docker_id=docker_id):
                self.assertRejected(docker_id, "a different image was loaded")

    def test_corrupt_saved_layer_rejected(self):
        self.img.saved_containerd(self.saved, layer_bytes=b"X" + self.img.layer_gz[1:])
        self.assertRejected(self.img.manifest_digest, "the saved image is corrupt")

    def test_saved_layer_content_mismatch_rejected(self):
        """A non-content-addressed legacy layer whose uncompressed bytes differ from the config diff_id."""
        self.img.saved_legacy(self.saved, layer_bytes=_layer_tar(b"tampered"))
        self.assertRejected(self.img.config_digest, "config diff_id is")

    def test_truncated_gzip_layer_rejected(self):
        self.img.saved_containerd(self.saved, layer_bytes=self.img.layer_gz[:-12])
        self.assertRejected(self.img.manifest_digest, "the saved image is corrupt")
        # Not content-addressed (legacy layout): the gzip stream itself must be rejected as truncated.
        self.img.saved_legacy(self.saved, layer_bytes=self.img.layer_gz[:-12])
        with self.assertRaises(EOFError):
            dii.verify(self.oci, self.saved, self.img.config_digest, TAG)

    def test_zstd_layer_rejected(self):
        self.img.saved_containerd(self.saved, layer_bytes=b"\x28\xb5\x2f\xfd" + b"\0" * 32)
        self.assertRejected(self.img.manifest_digest, "zstd")

    def test_wrong_tag_rejected(self):
        self.img.saved_containerd(self.saved, tags=("gbctl:ci",))
        self.assertRejected(self.img.manifest_digest, "not gb-server:ci")

    def test_two_saved_images_rejected(self):
        self.img.saved_containerd(self.saved, extra_entry=True)
        self.assertRejected(self.img.manifest_digest, "exactly one image")

    def test_corrupt_oci_archive_rejected(self):
        self.img.oci_archive(self.oci, corrupt_layer=True)
        self.img.saved_containerd(self.saved)
        self.assertRejected(self.img.manifest_digest, "corrupt or tampered")

    # -------------------------------------------------------------------- CLI --
    def run_cli(self, docker_id: str):
        return subprocess.run([sys.executable, os.path.join(HERE, "docker_image_identity.py"), "--oci", self.oci,
                               "--saved", self.saved, "--docker-id", docker_id, "--tag", TAG], capture_output=True, text=True)

    def test_cli(self):
        self.img.saved_containerd(self.saved)
        ok = self.run_cli(self.img.manifest_digest)
        self.assertEqual(ok.returncode, 0, ok.stderr)
        self.assertEqual(json.loads(ok.stdout)["docker_id_kind"], "manifest")
        bad = self.run_cli(OTHER)
        self.assertEqual(bad.returncode, 1)
        self.assertEqual(bad.stdout, "")
        self.assertIn("docker_image_identity: FAIL", bad.stderr)
        self.img.saved_containerd(self.saved, layer_bytes=self.img.layer_gz[:-12])
        trunc = self.run_cli(self.img.manifest_digest)
        self.assertEqual((trunc.returncode, trunc.stdout), (1, ""))


class OciLayerIdentityTests(unittest.TestCase):
    """GB2C-06: the OCI archive's layers must decode to its config's rootfs.diff_ids;
    blob digests alone do not tie a compressed layer to the config."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.tmp)
        self.a, self.b = Image("a"), Image("b")
        self.oci = os.path.join(self.tmp, "img.oci.tar")
        self.saved = os.path.join(self.tmp, "saved.tar")
        self.a.saved_legacy(self.saved)  # a legitimate Docker save of image A

    def archive(self, descs: list, blobs: list, config: bytes | None = None) -> str:
        return _oci_archive(self.oci, self.a.config if config is None else config, descs, blobs)

    def assertRejected(self, fragment: str, docker_id: str | None = None):
        with self.assertRaises(ArchiveError) as cm:
            dii.verify(self.oci, self.saved, docker_id or self.a.config_digest, TAG)
        self.assertIn(fragment, str(cm.exception))

    def test_reviewer_ab_mismatched_layer_rejected(self):
        """Independent review reproduction: A's config and diff_ids, a manifest referencing B's
        gzip layer, valid blob and manifest hashes, compared with a legitimate save of A by A's
        config digest. The pre-fix checker exited 0."""
        self.archive([_desc(GZIP_LAYER, self.b.layer_gz)], [self.b.layer_gz])
        with self.assertRaises(ArchiveError) as cm:  # every blob hashes to its digest, yet the shared validator refuses it
            oci_digest.inspect(self.oci)
        self.assertIn("the archive manifest does not carry its config's layers", str(cm.exception))
        self.assertRejected("the archive manifest does not carry its config's layers")
        for saved in (self.a.saved_legacy, self.a.saved_containerd, self.a.saved_classic):
            with self.subTest(saved=saved.__name__):
                saved(self.saved)
                self.assertRejected("config diff_id is")
        self.assertEqual(self.run_cli().returncode, 1)

    def test_docker_save_matches_config_but_oci_layer_differs_rejected(self):
        """Docker's saved image is A in every respect; only the OCI archive's layer content differs."""
        other = gzip.compress(_layer_tar(b"\x7fELFnot-a"), mtime=0)
        self.archive([_desc(GZIP_LAYER, other)], [other])
        self.a.saved_containerd(self.saved)
        self.assertRejected("config diff_id is")

    def test_gzip_layer_matching_diff_id_accepted(self):
        for media_type in (GZIP_LAYER, "application/vnd.docker.image.rootfs.diff.tar.gzip"):
            with self.subTest(media_type=media_type):
                self.archive([_desc(media_type, self.a.layer_gz)], [self.a.layer_gz])
                self.assertEqual(dii.verify(self.oci, self.saved, self.a.config_digest, TAG)["docker_id_kind"], "config")

    def test_uncompressed_layer_matching_diff_id_accepted(self):
        md = self.archive([_desc(TAR_LAYER, self.a.layer)], [self.a.layer])
        self.assertEqual(dii.verify(self.oci, self.saved, self.a.config_digest, TAG)["docker_id_kind"], "config")
        self.a.saved_classic(self.saved)  # Docker's own manifest is byte-identical to this uncompressed one
        rec = dii.verify(self.oci, self.saved, md, TAG)
        self.assertEqual((rec["docker_id_kind"], rec["saved_manifest_verified"]), ("manifest", True))

    def test_layer_count_mismatch_rejected(self):
        self.archive([_desc(GZIP_LAYER, self.a.layer_gz)] * 2, [self.a.layer_gz])
        self.assertRejected("archive manifest lists 2 layers, config lists 1 diff_ids")
        two = json.loads(self.a.config)
        two["rootfs"]["diff_ids"] *= 2
        self.archive([_desc(GZIP_LAYER, self.a.layer_gz)], [self.a.layer_gz], json.dumps(two).encode())
        self.assertRejected("archive manifest lists 1 layers, config lists 2 diff_ids", _digest(json.dumps(two).encode()))

    def test_decompressed_layer_diff_id_mismatch_rejected(self):
        other = gzip.compress(self.a.layer + b"\0" * 512, mtime=0)  # valid gzip, different content
        self.archive([_desc(GZIP_LAYER, other)], [other])
        self.assertRejected("config diff_id is")
        self.archive([_desc(TAR_LAYER, self.b.layer)], [self.b.layer])
        self.assertRejected("config diff_id is")

    def test_truncated_or_corrupt_gzip_layer_rejected(self):
        gz = self.a.layer_gz
        mid = len(gz) // 2
        for label, blob in (("truncated", gz[:-12]), ("truncated body", gz[:mid]),
                            ("corrupt deflate", gz[:12] + bytes([gz[12] ^ 0xFF]) + gz[13:]),
                            ("bad crc", gz[:-8] + bytes([gz[-8] ^ 0xFF]) + gz[-7:]),
                            ("trailing garbage", gz + b"garbage")):
            with self.subTest(label=label):
                self.archive([_desc(GZIP_LAYER, blob)], [blob])
                self.assertRejected("archive layer 0")

    def test_unsupported_compression_or_media_type_rejected(self):
        zstd = b"\x28\xb5\x2f\xfd" + b"\0" * 32
        cases = (
            ("zstd media type", _desc("application/vnd.oci.image.layer.v1.tar+zstd", zstd), zstd, "unsupported media type"),
            ("non-distributable", _desc("application/vnd.oci.image.layer.nondistributable.v1.tar+gzip", self.a.layer_gz), self.a.layer_gz, "unsupported media type"),
            ("unknown", _desc("application/octet-stream", self.a.layer), self.a.layer, "unsupported media type"),
            ("missing", {k: v for k, v in _desc(TAR_LAYER, self.a.layer).items() if k != "mediaType"}, self.a.layer, "unsupported media type"),
            ("zstd bytes as tar", _desc(TAR_LAYER, zstd), zstd, "its blob is compressed"),
            ("gzip bytes as tar", _desc(TAR_LAYER, self.a.layer_gz), self.a.layer_gz, "its blob is compressed"),
            ("tar bytes as gzip", _desc(GZIP_LAYER, self.a.layer), self.a.layer, "is not a gzip stream"),
            ("foreign layer", _desc(GZIP_LAYER, self.a.layer_gz, urls=["https://example.invalid/l"]), self.a.layer_gz, "foreign layer"),
        )
        for label, desc, blob, fragment in cases:
            with self.subTest(label=label):
                self.archive([desc], [blob])
                self.assertRejected(fragment)

    def test_invalid_layer_metadata_rejected(self):
        gz = self.a.layer_gz
        for label, desc, fragment in (
            ("size too large", _desc(GZIP_LAYER, gz, size=len(gz) + 1), "is not its blob size"),
            ("size as string", _desc(GZIP_LAYER, gz, size=str(len(gz))), "is not its blob size"),
            ("size missing", {k: v for k, v in _desc(GZIP_LAYER, gz).items() if k != "size"}, "is not its blob size"),
        ):
            with self.subTest(label=label):
                self.archive([desc], [gz])
                self.assertRejected(fragment)
        self.archive(["sha256:" + "0" * 64], [gz])  # a descriptor that is not an object: refused by the shared validator
        self.assertRejected("archive layer 0 descriptor is not an object")
        bad_desc = self.run_cli()
        self.assertEqual((bad_desc.returncode, bad_desc.stdout), (1, ""))
        self.assertIn("docker_image_identity: FAIL archive layer 0 descriptor is not an object", bad_desc.stderr)
        cfg = json.loads(self.a.config)
        cfg["rootfs"]["diff_ids"] = ["sha256:" + "A" * 64]
        bad = json.dumps(cfg).encode()
        self.archive([_desc(GZIP_LAYER, gz)], [gz], bad)
        self.assertRejected("is not a sha256 digest", _digest(bad))
        cfg["rootfs"] = {"type": "other", "diff_ids": json.loads(self.a.config)["rootfs"]["diff_ids"]}
        bad = json.dumps(cfg).encode()
        self.archive([_desc(GZIP_LAYER, gz)], [gz], bad)
        self.assertRejected("rootfs is not of type layers", _digest(bad))

    def test_missing_layer_blob_rejected(self):
        self.archive([_desc(GZIP_LAYER, self.a.layer_gz)], [])
        self.assertRejected("is missing from the archive")

    def run_cli(self):
        return subprocess.run([sys.executable, os.path.join(HERE, "docker_image_identity.py"), "--oci", self.oci, "--saved", self.saved,
                               "--docker-id", self.a.config_digest, "--tag", TAG], capture_output=True, text=True)


class ImageChecksWiringTests(unittest.TestCase):
    """image-checks.sh must prove identity through the helper, not compare the Id with the config digest alone."""

    def test_image_checks_uses_saved_image_verification(self):
        with open(os.path.join(HERE, "image-checks.sh")) as f:
            text = f.read()
        self.assertIn('docker image save "$tag" -o "$WORK/$t.saved.tar"', text)
        self.assertIn('docker_image_identity.py" --oci "$oci" --saved "$WORK/$t.saved.tar" --docker-id "$id" --tag "$tag"', text)
        self.assertNotIn('[ "$id" = "${CONFIG[$t]}" ]', text)


if __name__ == "__main__":
    unittest.main()
