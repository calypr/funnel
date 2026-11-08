# FTP

Funnel supports download and uploading files via FTP.

Credentials may come from the URL, for example
`ftp://username:password@ftp.host.tld/file`, or from the worker configuration.
Configured credentials require a trusted `Endpoint`:

```yaml
FTPStorage:
  Endpoint: "ftp://ftp.host.tld:21"
  User: "username"
  Password: "password"
```

Funnel sends configured credentials only when the requested hostname and port
match that endpoint. Hostname comparison ignores case; an omitted port means
`21`. A different origin is rejected before connecting. Existing deployments
using configured credentials must set `Endpoint` when upgrading. The endpoint
must be an FTP origin without userinfo, a file path, a query, or a fragment.

Credentials explicitly included in a task URL replace the configured credentials
completely, including an omitted URL password. The public anonymous defaults
(`anonymous` / `anonymous`) do not require an endpoint.

The FTP storage client is enabled by default, but may be explicitly disabled in the 
worker config:

```yaml
FTPStorage:
  Disabled: false
```

### Example task
```json
{
  "name": "Hello world",
  "inputs": [{
    "url": "ftp://my.ftpserver.xyz/hello.txt",
    "path": "/inputs/hello.txt"
  }, {
    "url": "ftp://user:mypassword123@my.ftpserver.xyz/hello.txt",
    "path": "/inputs/hello.txt"
  }],
  "executors": [{
    "image": "alpine",
    "command": ["cat", "/inputs/hello.txt"],
  }]
}
```
