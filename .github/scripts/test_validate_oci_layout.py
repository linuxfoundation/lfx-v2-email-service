# Copyright The Linux Foundation and each contributor to LFX.
# SPDX-License-Identifier: MIT
"""Tests for validate_oci_layout.py; run with python3 -m unittest."""

import hashlib
import json
import os
import tempfile
import unittest

from validate_oci_layout import LayoutError, validate

OCI_INDEX = "application/vnd.oci.image.index.v1+json"
OCI_MANIFEST = "application/vnd.oci.image.manifest.v1+json"
SUBJECT = (
    '{"mediaType":"' + OCI_INDEX + '","digest":"sha256:' + "a" * 64 + '","size":1}'
)


def manifest_json(arch):
    return json.dumps(
        {
            "schemaVersion": 2,
            "mediaType": OCI_MANIFEST,
            "config": {
                "mediaType": "application/vnd.oci.image.config.v1+json",
                "digest": "sha256:" + hashlib.sha256(arch.encode()).hexdigest(),
                "size": 1,
            },
            "layers": [],
        }
    ).encode()


class LayoutBuilder:
    """Writes a ko-shaped layout; the raw arguments override document bytes."""

    def __init__(self, root):
        self.root = root
        os.makedirs(os.path.join(root, "blobs", "sha256"))

    def blob(self, data):
        digest = hashlib.sha256(data).hexdigest()
        with open(os.path.join(self.root, "blobs", "sha256", digest), "wb") as f:
            f.write(data)
        return "sha256:" + digest, len(data)

    def build(self, index_raw=None, manifest_raw=None, top_raw=None, child_type=OCI_MANIFEST):
        children = []
        for arch in ("amd64", "arm64"):
            data = manifest_raw(arch) if manifest_raw else manifest_json(arch)
            digest, size = self.blob(data)
            children.append(
                {
                    "mediaType": child_type,
                    "digest": digest,
                    "size": size,
                    "platform": {"architecture": arch, "os": "linux"},
                }
            )
        index = json.dumps(
            {"schemaVersion": 2, "mediaType": OCI_INDEX, "manifests": children}
        ).encode()
        if index_raw:
            index = index_raw(index)
        digest, size = self.blob(index)
        top = json.dumps(
            {
                "schemaVersion": 2,
                "mediaType": OCI_INDEX,
                "manifests": [{"mediaType": OCI_INDEX, "digest": digest, "size": size}],
            }
        ).encode()
        if top_raw:
            top = top_raw(top, digest)
        with open(os.path.join(self.root, "index.json"), "wb") as f:
            f.write(top)
        return digest


def prefix_key(raw, member):
    """Insert a raw JSON member at the start of a serialized object."""
    return b"{" + member.encode() + b"," + raw[1:]


class ValidateTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.layout = os.path.join(self.tmp.name, "oci-layout")
        self.builder = LayoutBuilder(self.layout)

    def tearDown(self):
        self.tmp.cleanup()

    def assertRejected(self, message):
        with self.assertRaises(LayoutError) as ctx:
            validate(self.layout)
        self.assertIn(message, str(ctx.exception))

    def test_accepts_two_platform_layout(self):
        self.builder.build()
        validate(self.layout)

    def test_rejects_extra_index_entries(self):
        def two_entries(top, _):
            doc = json.loads(top)
            doc["manifests"] *= 2
            return json.dumps(doc).encode()

        self.builder.build(top_raw=two_entries)
        self.assertRejected("exactly one image index")

    def test_rejects_subject_on_index(self):
        self.builder.build(index_raw=lambda raw: prefix_key(raw, '"subject":' + SUBJECT))
        self.assertRejected("Image index has an unexpected shape")

    def test_rejects_subject_on_manifest(self):
        self.builder.build(
            manifest_raw=lambda arch: prefix_key(manifest_json(arch), '"subject":' + SUBJECT)
        )
        self.assertRejected("Image manifest has an unexpected shape")

    def test_rejects_artifact_type_on_manifest(self):
        self.builder.build(
            manifest_raw=lambda arch: prefix_key(manifest_json(arch), '"artifactType":"x"')
        )
        self.assertRejected("Image manifest has an unexpected shape")

    def test_rejects_case_variant_subject(self):
        for key in ('"Subject"', '"SUBJECT"', '"\\u0053ubject"'):
            with self.subTest(key=key):
                self.tearDown()
                self.setUp()
                self.builder.build(
                    manifest_raw=lambda arch, k=key: prefix_key(
                        manifest_json(arch), k + ":" + SUBJECT
                    )
                )
                self.assertRejected("noncanonical key")

    def test_rejects_unicode_folded_subject(self):
        # Go folds U+017F (long s) to "s" when matching struct fields.
        self.builder.build(
            manifest_raw=lambda arch: prefix_key(manifest_json(arch), '"\\u017fubject":' + SUBJECT)
        )
        self.assertRejected("non-ASCII key")

    def test_rejects_duplicate_manifests_key(self):
        self.builder.build(index_raw=lambda raw: prefix_key(raw, '"manifests":[]'))
        self.assertRejected("duplicate keys")

    def test_rejects_case_variant_duplicate_manifests_key(self):
        self.builder.build(index_raw=lambda raw: raw[:-1] + b',"Manifests":[]}')
        self.assertRejected("duplicate keys")

    def test_rejects_nested_index(self):
        self.builder.build(child_type=OCI_INDEX)
        self.assertRejected("Image index has an unexpected shape")

    def test_rejects_digest_mismatch(self):
        digest = self.builder.build()
        path = os.path.join(self.layout, "blobs", "sha256", digest[len("sha256:"):])
        with open(path, "ab") as f:
            f.write(b" ")
        self.assertRejected("does not match its digest")

    def test_rejects_missing_blob(self):
        digest = self.builder.build()
        os.remove(os.path.join(self.layout, "blobs", "sha256", digest[len("sha256:"):]))
        self.assertRejected("blob is missing")

    def test_rejects_symlinked_blob(self):
        digest = self.builder.build()
        path = os.path.join(self.layout, "blobs", "sha256", digest[len("sha256:"):])
        target = os.path.join(self.tmp.name, "target")
        os.rename(path, target)
        os.symlink(target, path)
        self.assertRejected("blob is missing")

    def test_rejects_malformed_digest(self):
        def traversal(top, _):
            doc = json.loads(top)
            doc["manifests"][0]["digest"] = "sha256:../../index.json"
            return json.dumps(doc).encode()

        self.builder.build(top_raw=traversal)
        self.assertRejected("Unexpected digest")

    def test_rejects_byte_order_mark(self):
        self.builder.build(top_raw=lambda top, _: b"\xef\xbb\xbf" + top)
        self.assertRejected("not valid")

    def test_rejects_invalid_utf8(self):
        self.builder.build(top_raw=lambda top, _: top[:-1] + b',"x":"\xff"}')
        self.assertRejected("not valid")


if __name__ == "__main__":
    unittest.main()
