---
title: HTTP(S)
menu:
  main:
    parent: Storage
---

# HTTP(S)

Funnel can download inputs from and upload outputs to HTTP(S) URLs that need no
credentials: public URLs, or presigned URLs issued by a cloud storage provider
such as Amazon S3 or Google Cloud Storage. No authentication mechanism is
allowed, so the URL itself must grant access.

- Inputs are downloaded with a GET request.
- Outputs are uploaded with a PUT request that carries a `Content-Length`
  header, as presigned S3 upload URLs require.

Presigned URLs work for `FILE` inputs and outputs. A presigned URL is signed for
one exact object, but Funnel derives the URL of each file in a `DIRECTORY`
output, and of each match of a wildcard output, by appending the file's path to
the path of the output URL, keeping its query string. Those derived URLs carry
no valid signature, so use one `FILE` output per presigned URL.

For `DIRECTORY` and wildcard outputs, point the output URL at a server that
signs each upload as it arrives, when the file name is known, and redirects it
to the storage provider:

- An upload follows a 307 or 308 redirect and resends the file to the new URL.
- Uploads send `Expect: 100-continue` and wait up to one second for the server
  to answer before sending the file. A server that redirects within that second
  doesn't receive the file; a slower one may receive some or all of it first.
- A 301, 302 or 303 response to an upload is reported as an error, because
  following it would turn the upload into a GET.
- Downloads follow redirects too.

The URL is stored with the task, including any signature in its query string,
so issue presigned URLs with a short expiry. The storage provider limits the
size of a single PUT (5 GB on Amazon S3).

The HTTP storage client is enabled by default, but may be explicitly disabled in the 
worker config:

```yaml
HTTPStorage:
  Disabled: false
  # Timeout for http(s) requests, including the transfer itself.
  Timeout: 30s
```

### Example task
```json
{
  "name": "Hello world",
  "inputs": [{
    "url": "http://fakedomain.com/hello.txt",
    "path": "/inputs/hello.txt"
  }],
  "outputs": [{
    "url": "https://my-bucket.s3.amazonaws.com/hello-upper.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=...",
    "path": "/outputs/hello-upper.txt",
    "type": "FILE"
  }],
  "executors": [{
    "image": "alpine",
    "command": ["sh", "-c", "tr a-z A-Z < /inputs/hello.txt > /outputs/hello-upper.txt"]
  }]
}
```
