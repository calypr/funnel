# FTP

Funnel supports download and uploading files via FTP.

Currently authentication credentials are take from the URL, e.g. `ftp://username:password@ftp.host.tld`. This will be improved soon to allow credentials to be added to the configuration file.

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
