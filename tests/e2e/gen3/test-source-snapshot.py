#!/usr/bin/env python3
"""Prove local source transport preserves working files and rejects stale bytes."""
import io
import pathlib
import stat
import subprocess
import sys
import tarfile
import tempfile
import unittest

HELPERS = pathlib.Path(__file__).parent / "ci-runner"


class SourceSnapshotTest(unittest.TestCase):
    def test_round_trip_and_corruption(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = pathlib.Path(temporary)
            source, target = base / "source", base / "linux"
            source.mkdir()

            def git(*args):
                return subprocess.check_output(["git", "-C", str(source), *args], stderr=subprocess.DEVNULL, text=True).strip()

            def run(helper, *args):
                return subprocess.run([sys.executable, str(HELPERS / helper), *map(str, args)], capture_output=True, text=True, check=True)

            git("init")
            git("config", "user.name", "Snapshot test")
            git("config", "user.email", "snapshot@example.invalid")
            (source / "tracked").write_text("committed\n")
            (source / "deleted").write_text("remove me\n")
            (source / ".gitignore").write_text(".gen3-local/\n")
            git("add", ".")
            git("commit", "-m", "fixture")
            git("update-index", "--add", "--cacheinfo", f"160000,{git('rev-parse', 'HEAD')},schemas")
            git("commit", "-m", "empty submodule")
            (source / "schemas").mkdir()
            (source / "tracked").write_text("working version\n")
            (source / "deleted").unlink()
            (source / "untracked.sh").write_text("#!/bin/sh\nexit 0\n")
            (source / "untracked.sh").chmod(0o755)
            (source / "link").symlink_to("tracked")
            repository = base / "repository.bundle"
            git("bundle", "create", str(repository), "HEAD")
            package = base / "source.tar"
            run("snapshot-source.py", source, package)
            run("sync-snapshot.py", package, target, repository)
            self.assertEqual((target / "tracked").read_text(), "working version\n")
            self.assertFalse((target / "deleted").exists())
            self.assertEqual(stat.S_IMODE((target / "untracked.sh").stat().st_mode), 0o755)
            self.assertEqual((target / "link").readlink(), pathlib.Path("tracked"))
            self.assertTrue((target / "schemas").is_dir())
            (target / "stale").write_text("previous run")
            run("sync-snapshot.py", package, target, repository)
            self.assertFalse((target / "stale").exists())
            corrupt = base / "corrupt.tar"
            with tarfile.open(package) as original, tarfile.open(corrupt, "w") as changed:
                for member in original.getmembers():
                    content = original.extractfile(member).read() if member.isfile() else None
                    if member.name == "source/tracked":
                        content = b"stale cached bytes\n"
                        member.size = len(content)
                    changed.addfile(member, io.BytesIO(content) if content is not None else None)
            result = subprocess.run([sys.executable, str(HELPERS / "sync-snapshot.py"), str(corrupt), str(target), str(repository)], capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Source hash mismatch: tracked", result.stderr)


if __name__ == "__main__":
    unittest.main()
