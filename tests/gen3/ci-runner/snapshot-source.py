#!/usr/bin/env python3
"""Package the actual working tree without depending on VM shared-file caches."""
import hashlib
import io
import json
import pathlib
import stat
import subprocess
import sys
import tarfile

root, destination = map(pathlib.Path, sys.argv[1:])
head = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
paths = subprocess.check_output(["git", "-C", str(root), "ls-files", "-co", "--exclude-standard", "-z"])
manifest = {"head": head, "files": {}}
with tarfile.open(destination, "w") as archive:
    for raw in sorted(set(paths.split(b"\0")) - {b""}):
        relative = raw.decode("utf-8", "surrogateescape")
        path = root / relative
        if not path.exists() and not path.is_symlink():
            continue
        info = archive.gettarinfo(str(path), arcname="source/" + relative)
        if info.isfile():
            data = path.read_bytes()
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
            digest = hashlib.sha256(data).hexdigest()
        elif info.issym():
            archive.addfile(info)
            digest = hashlib.sha256(info.linkname.encode()).hexdigest()
        elif info.isdir():
            archive.addfile(info)
            digest = hashlib.sha256(b"").hexdigest()
        else:
            raise ValueError(f"Unsupported source entry: {relative}")
        manifest["files"][relative] = {"sha256": digest, "mode": stat.S_IMODE(path.lstat().st_mode), "directory": info.isdir()}
    data = json.dumps(manifest, sort_keys=True).encode()
    info = tarfile.TarInfo("manifest.json")
    info.size = len(data)
    archive.addfile(info, io.BytesIO(data))
print(f"source_head={head} files={len(manifest['files'])} archive_sha256={hashlib.sha256(destination.read_bytes()).hexdigest()}")
