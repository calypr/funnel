#!/usr/bin/env python3
"""Read the platform config identity from a Docker save archive."""

import hashlib
import json
import sys
import tarfile


def config_digest(archive_path: str, image: str) -> str:
    with tarfile.open(archive_path) as archive:
        manifest = json.load(archive.extractfile("manifest.json"))
        matches = [entry for entry in manifest if image in (entry.get("RepoTags") or [])]
        if len(matches) != 1:
            raise ValueError(f"expected one archived image tagged {image}, found {len(matches)}")
        config = archive.extractfile(matches[0]["Config"]).read()
        return "sha256:" + hashlib.sha256(config).hexdigest()


if __name__ == "__main__":
    print(config_digest(sys.argv[1], sys.argv[2]))
