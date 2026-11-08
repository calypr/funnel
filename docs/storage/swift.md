# OpenStack Swift

Funnel supports using [OpenStack Swift][swift] for file storage.

The Swift storage client is enabled by default, and will try to automatically
load credentials from the environment. Alternatively, you
may explicitly set the credentials in the worker config:

```yaml
Swift:
  Disabled: false
  UserName: ""
  Password: ""
  AuthURL: ""
  TenantName: ""
  TenantID: ""
  RegionName: ""
  # 500 MB
  ChunkSizeBytes: 500000000
```

### Example task
```json
{
  "name": "Hello world",
  "inputs": [{
    "url": "swift://funnel-bucket/hello.txt",
    "path": "/inputs/hello.txt"
  }],
  "outputs": [{
    "url": "swift://funnel-bucket/output.txt",
    "path": "/outputs/hello-out.txt"
  }],
  "executors": [{
    "image": "alpine",
    "command": ["cat", "/inputs/hello.txt"],
    "stdout": "/outputs/hello-out.txt",
  }]
}
```

### Known Issues:

The config currently only supports OpenStack v2 auth. See [issue #336](https://github.com/ohsu-comp-bio/funnel/issues/336).

[swift]: https://docs.openstack.org/swift/latest/
