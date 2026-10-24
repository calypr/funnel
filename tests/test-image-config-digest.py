#!/usr/bin/env python3
"""Check config identity for classic and containerd Docker save layouts."""

import hashlib
import importlib.util
import io
import json
import tarfile
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "image_config_digest", Path(__file__).with_name("image-config-digest.py")
)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ImageConfigDigestTest(unittest.TestCase):
    def test_saved_config_is_independent_of_index_identity(self):
        config = b'{"architecture":"amd64","os":"linux","config":{}}'
        expected = "sha256:" + hashlib.sha256(config).hexdigest()
        index = b'{"schemaVersion":2,"manifests":[]}'
        index_digest = "sha256:" + hashlib.sha256(index).hexdigest()
        self.assertNotEqual(expected, index_digest)
        for config_path in (expected[7:] + ".json", "blobs/sha256/" + expected[7:]):
            with self.subTest(config_path=config_path), tempfile.TemporaryDirectory() as directory:
                archive_path = Path(directory) / "image.tar"
                manifest = [{"Config": config_path, "RepoTags": ["fixture:candidate"], "Layers": []}]
                with tarfile.open(archive_path, "w") as archive:
                    for name, data in (("manifest.json", json.dumps(manifest).encode()),
                                       ("index.json", index), (config_path, config)):
                        entry = tarfile.TarInfo(name)
                        entry.size = len(data)
                        archive.addfile(entry, io.BytesIO(data))
                self.assertEqual(module.config_digest(str(archive_path), "fixture:candidate"), expected)
                with self.assertRaises(ValueError):
                    module.config_digest(str(archive_path), "fixture:wrong")


if __name__ == "__main__":
    unittest.main()
