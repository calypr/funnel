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

For a server configured with OIDC authentication, set `FUNNEL_SERVER_TOKEN`
to a valid bearer token. The HTTP task client sends it in the `Authorization`
header instead of Basic credentials when both are configured. The same HTTPS
and redirect restrictions apply. With no bearer token, existing Basic
authentication behavior is preserved, including credentials with an empty
password. The CLI does not refresh expired tokens; replace the environment
value and rerun the command when needed.

The internal gRPC client currently uses plaintext transport. It rejects
credentials for remote addresses by default; use a secure tunnel terminating
on loopback. This restriction does not add gRPC TLS support.

For a known trusted private deployment that requires remote plaintext
credentials, explicitly set `FUNNEL_ALLOW_INSECURE_CREDENTIALS=true`. This
also permits HTTP credential downgrades, and exposes credentials to anyone
who can observe that connection. Unauthenticated connections remain available
without this setting.
