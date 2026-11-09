# Local

Funnel supports using the local filesystem for file storage.

Funnel limits which directories may be accessed, by default only allowing directories 
under the current working directory of the Funnel worker.

Config:
```yaml
LocalStorage:
  # Whitelist of local directory paths which Funnel is allowed to access.
  AllowedDirs:
    - ./
    - /path/to/allowed/dir
    - ...etc
```

### Example task

Files must be absolute paths in `file:///path/to/file.txt` URL form.

```
{
  "name": "Hello world",
  "inputs": [{
    "url": "file:///path/to/funnel-data/hello.txt",
    "path": "/inputs/hello.txt"
  }],
  "outputs": [{
    "url": "file:///path/to/funnel-data/output.txt",
    "path": "/outputs/hello-out.txt"
  }],
  "executors": [{
    "image": "alpine",
    "command": ["cat", "/inputs/hello.txt"],
    "stdout": "/outputs/hello-out.txt",
  }]
}
```

### Directory boundaries and file transfers

Funnel checks storage URLs against `AllowedDirs` during reads, writes, and listings.
Paths and relative symlinks cannot escape the selected allowed directory. Absolute
symlinks are rejected, including those pointing inside an allowed directory. Use
relative symlinks for files within that directory.

Local transfers copy files rather than creating hard links. This keeps file access
within the allowed directory during the transfer, but requires space for the copy.
The worker's task staging paths are separate from the storage URL allowlist.

### File ownership behavior

One difficult area of files and Docker containers is file owner/group management. 
If a Docker container runs as root, it's likely that the file will end up being owned 
by root on the host system. In this case, some step (Funnel or another task) will 
likely fail to access it. This is a tricky problem with no good solution yet. 
See [issue 66](https://github.com/ohsu-comp-bio/funnel/issues/66).
