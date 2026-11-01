---
title: Remote Funnel server
menu:
  main:
    parent: Integrations
---

# Connect to a remote Funnel server

By default, the Funnel CLI connects to `http://localhost:8000`. Use `-S` to
select another server for a command:

```sh
funnel -S https://funnel.example.com task list
```

To use the same server for multiple commands, set `FUNNEL_SERVER`:

```sh
export FUNNEL_SERVER=https://funnel.example.com
funnel task list
```

The `-S` flag takes precedence when both are set.

Set `FUNNEL_SERVER_USER` and `FUNNEL_SERVER_PASSWORD` to authenticate using
Basic authentication. Credentials require HTTPS for remote HTTP servers.
Plain HTTP is allowed for loopback development (`localhost`, `127.0.0.1`, or
`[::1]`). Authenticated HTTPS requests cannot redirect to HTTP.

The internal gRPC client currently uses plaintext transport. It rejects
credentials for remote addresses by default; use a secure tunnel terminating
on loopback. This restriction does not add gRPC TLS support.

For a known trusted private deployment that requires remote plaintext
credentials, explicitly set `FUNNEL_ALLOW_INSECURE_CREDENTIALS=true`. This
also permits HTTP credential downgrades, and exposes credentials to anyone
who can observe that connection. Unauthenticated connections remain available
without this setting.
