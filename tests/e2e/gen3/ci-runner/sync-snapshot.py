#!/usr/bin/env python3
"""Install and verify an immutable local source snapshot on the Linux host."""
import hashlib
import json
import os
import pathlib
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile

package, snapshot = map(pathlib.Path, sys.argv[1:3])
repository = pathlib.Path(sys.argv[3])
snapshot.parent.mkdir(parents=True, exist_ok=True)
with tempfile.TemporaryDirectory(prefix="source-", dir=snapshot.parent) as temporary:
    staging = pathlib.Path(temporary)
    with tarfile.open(package) as archive:
        archive.extractall(staging, filter="data")
    manifest = json.loads((staging / "manifest.json").read_text())
    head = manifest["head"]
    if len(head) != 40 or any(c not in "0123456789abcdef" for c in head):
        raise ValueError("Invalid source commit")
    snapshot.mkdir(parents=True, exist_ok=True)
    git_dir = snapshot / ".git"
    if (git_dir / "objects/info/alternates").exists():
        # Replace the old shared clone; all subsequent Git reads stay on Linux.
        backup = snapshot / ".gen3-local/shared-git-backup"
        if backup.exists():
            raise RuntimeError("Shared Git backup already exists")
        shutil.move(git_dir, backup)
    if not git_dir.exists():
        subprocess.run(["git", "init", str(snapshot)], check=True)
    git = ["git", "-C", str(snapshot)]
    if subprocess.run(git + ["cat-file", "-e", head + "^{commit}"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
        subprocess.run(git + ["fetch", str(repository), "HEAD"], check=True)
    subprocess.run(git + ["checkout", "--force", "--detach", head], check=True)
    subprocess.run(git + ["clean", "-fd", "-e", ".gen3-local/"], check=True)
    tracked = subprocess.check_output(git + ["ls-files", "-z"])
    for raw in tracked.split(b"\0"):
        if raw and os.fsdecode(raw) not in manifest["files"]:
            (snapshot / os.fsdecode(raw)).unlink(missing_ok=True)
    for relative, expected in manifest["files"].items():
        source, target = staging / "source" / relative, snapshot / relative
        if expected["directory"]:
            if not source.is_dir():
                raise ValueError(f"Source directory missing: {relative}")
            target.mkdir(parents=True, exist_ok=True)
            target.chmod(expected["mode"])
            continue
        if source.is_symlink():
            data = os.readlink(source).encode()
        else:
            data = source.read_bytes()
        if hashlib.sha256(data).hexdigest() != expected["sha256"]:
            raise ValueError(f"Source hash mismatch: {relative}")
        target.parent.mkdir(parents=True, exist_ok=True)
        if target.is_symlink():
            target.unlink()
        shutil.copy2(source, target, follow_symlinks=False)
        if not target.is_symlink():
            target.chmod(expected["mode"])
            actual = hashlib.sha256(target.read_bytes()).hexdigest()
            if actual != expected["sha256"] or stat.S_IMODE(target.stat().st_mode) != expected["mode"]:
                raise ValueError(f"Installed source mismatch: {relative}")
    print(f"Verified source: head={head} files={len(manifest['files'])}; Git objects and working files are on Linux", flush=True)
