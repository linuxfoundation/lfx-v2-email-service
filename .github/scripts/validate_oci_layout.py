# Copyright The Linux Foundation and each contributor to LFX.
# SPDX-License-Identifier: MIT
"""Validate a PR-built OCI image layout before ko-publish-branch.yaml pushes it.

Accept only ko's shape: one multi-arch index of plain image manifests. A
manifest with a subject would make the push also write a sha256-<digest>
referrers tag outside the pr-<number>- namespace.

crane decodes these documents with Go's encoding/json, which matches field
names case-insensitively (with Unicode folding) and lets the last duplicate
win, so only unambiguous JSON with ASCII, canonically spelled, non-duplicate
keys is accepted.
"""

import hashlib
import json
import os
import re
import sys

INDEX_TYPES = {
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
}
IMAGE_TYPES = {
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
}
CANONICAL = {
    key.lower(): key
    for key in (
        "schemaVersion",
        "mediaType",
        "artifactType",
        "config",
        "layers",
        "manifests",
        "subject",
        "annotations",
        "digest",
        "size",
        "urls",
        "data",
        "platform",
        "architecture",
        "os",
        "os.version",
        "os.features",
        "variant",
        "features",
    )
}
DIGEST_RE = re.compile(r"sha256:[0-9a-f]{64}")


class LayoutError(Exception):
    """The layout is not one this pipeline may push."""


def _unambiguous(items):
    seen = set()
    for key, _ in items:
        if not key.isascii():
            raise LayoutError("Image layout JSON has a non-ASCII key")
        folded = key.lower()
        if folded in seen:
            raise LayoutError("Image layout JSON has duplicate keys")
        seen.add(folded)
        if CANONICAL.get(folded, key) != key:
            raise LayoutError("Image layout JSON has a noncanonical key")
    return dict(items)


def _load(path):
    with open(path, "rb") as f:
        data = f.read()
    try:
        doc = json.loads(data.decode("utf-8"), object_pairs_hook=_unambiguous)
    except ValueError as e:
        raise LayoutError("Image layout JSON is not valid") from e
    if not isinstance(doc, dict):
        raise LayoutError("Image layout JSON is not an object")
    return data, doc


def _blob(layout, desc):
    digest = desc.get("digest") if isinstance(desc, dict) else None
    if not isinstance(digest, str) or not DIGEST_RE.fullmatch(digest):
        raise LayoutError("Unexpected digest in image layout")
    hex_digest = digest[len("sha256:"):]
    path = os.path.join(layout, "blobs", "sha256", hex_digest)
    if os.path.islink(path) or not os.path.isfile(path):
        raise LayoutError("Image layout blob is missing")
    data, doc = _load(path)
    if hashlib.sha256(data).hexdigest() != hex_digest:
        raise LayoutError("Image layout blob does not match its digest")
    return doc


def _plain(doc, types):
    return (
        "subject" not in doc
        and "artifactType" not in doc
        and doc.get("mediaType") in types
    )


def validate(layout):
    """Raise LayoutError unless layout holds exactly ko's multi-arch shape."""
    _, top = _load(os.path.join(layout, "index.json"))
    entries = top.get("manifests")
    if (
        not isinstance(entries, list)
        or len(entries) != 1
        or not isinstance(entries[0], dict)
        or entries[0].get("mediaType") not in INDEX_TYPES
    ):
        raise LayoutError("Image layout must hold exactly one image index")
    index = _blob(layout, entries[0])
    children = index.get("manifests")
    if not _plain(index, INDEX_TYPES) or not isinstance(children, list) or not children:
        raise LayoutError("Image index has an unexpected shape")
    for child in children:
        if not isinstance(child, dict) or child.get("mediaType") not in IMAGE_TYPES:
            raise LayoutError("Image index has an unexpected shape")
        if not _plain(_blob(layout, child), IMAGE_TYPES):
            raise LayoutError("Image manifest has an unexpected shape")


def main(argv):
    if len(argv) != 2:
        print(f"usage: {argv[0]} <oci-layout-dir>", file=sys.stderr)
        return 2
    try:
        validate(argv[1])
    except LayoutError as e:
        print(f"::error::{e}")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
