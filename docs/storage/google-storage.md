# Google Storage

Funnel supports using [Google Storage][gs] (GS) for file storage.

The Google storage client is enabled by default, and will try to automatically
load credentials from the environment. Alternatively, you
may explicitly set the credentials in the worker config:

```yaml
GoogleStorage:
  Disabled: false
  # Path to account credentials file.
  AccountFile: ""
```

### Example task
```json
{
  "name": "Hello world",
  "inputs": [{
    "url": "gs://funnel-bucket/hello.txt",
    "path": "/inputs/hello.txt"
  }],
  "outputs": [{
    "url": "gs://funnel-bucket/output.txt",
    "path": "/outputs/hello-out.txt"
  }],
  "executors": [{
    "image": "alpine",
    "command": ["cat", "/inputs/hello.txt"],
    "stdout": "/outputs/hello-out.txt",
  }]
}
```

[gs]: https://cloud.google.com/storage/
