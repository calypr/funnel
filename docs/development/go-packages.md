# Go packages and the TES client

Funnel separates its public Go API into two packages:

- `github.com/calypr/funnel/tes`: shared generated TES models, enum conveniences,
  and request filters. It has no HTTP client or server dependencies.
- `github.com/calypr/funnel/client/tes`: TES REST operations, client configuration,
  HTTP responses, and task polling.

Server implementations are under `internal/`, where Go prevents external modules
from importing them.

```go
client, err := tesclient.NewClient("https://example.org/prefix",
    tesclient.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}))
if err != nil {
    return err
}
client.Token = token
result, err := client.CreateTask(ctx, &tes.Task{
    Executors: []*tes.Executor{{
        Image: "alpine",
        Command: []string{"echo", "hello"},
    }},
})
```

Import `github.com/calypr/funnel/client/tes` as `tesclient` and
`github.com/calypr/funnel/tes` for the models. Import `context`, `net/http`, and
`time` as needed.
The client preserves URL prefixes and accepts a caller-owned HTTP client for
transports, timeouts, proxies, and testing. List responses return the next page
token; callers choose when to fetch another page. Requests use the caller's
context, and the server owns task validation and initialization.

The library does not read `FUNNEL_SERVER_USER`, `FUNNEL_SERVER_PASSWORD`,
`FUNNEL_SERVER_TOKEN`, or `FUNNEL_ALLOW_INSECURE_CREDENTIALS`. Set `User`,
`Password`, or `Token` explicitly. Bearer tokens take precedence over Basic
credentials. Credentials require HTTPS except for loopback hosts; applications
that deliberately permit plaintext credentials must pass
`tesclient.WithInsecureCredentials()`. The same rule covers redirects. The CLI retains
its environment-variable behavior through `internal/cli/tesenv`.

The client and dashboard node requests share a focused `client/auth` credential
transport policy. This policy covers plaintext restrictions and redirects; it
does not read process environment variables. HTTP response parsing remains
private to the TES client. The CLI owns output formatting, and callers that need
model JSON serialization can use protobuf's `protojson` package directly.

The former `tes.NewClient` API moves to `client/tes.NewClient`; its request and
response types retain their `tes` imports. There is no forwarding client in the
model package. The mutable `tes.Marshaler` global, `tes.MarshalToString` wrapper,
and the client's formatting-only `Marshaler` field have been removed.

## Application structure

- `internal/cli`: commands and application assembly; root `main.go` remains the
  executable entry point, including `go install github.com/calypr/funnel`.
- `internal/server`: HTTP, authentication, and task service implementation.
- `internal/compute`, `internal/database`, `internal/worker`, `internal/storage`,
  `internal/events`, and `internal/metrics`: application subsystems.
- `internal/taskmodel`: task validation, initialization, state transitions,
  projections, log mutation, and server pagination policy.
- `internal/tesrpc`: generated gRPC and HTTP gateway service bindings.
- `internal/pluginmanager`: application plugin loading.
- `internal/logger`: runtime logging and server interceptors.
- `internal/webdash`: Go dashboard serving and embedded build output. Frontend
  source and npm commands remain in `webdash/`.

The `config`, logger schemas/defaults, `plugins/proto`, and `plugins/shared` remain
public for existing plugin integrations. Plugin configuration, TES messages,
protobuf field numbers, service names, and JSON names remain compatible.
Application implementation imports such as `funnel/server`, `funnel/worker`,
and TES server helpers are no longer public Go APIs. External applications should
use the REST client; application extensions must live inside this module or use
the plugin contracts. No forwarding packages preserve the former import paths.

## Generation and verification

Run `make proto` to generate public message types from the TES OpenAPI schema
and service bindings from the same source. The schemas share the protobuf
package `tes`; Go code uses separate public and internal packages. The REST
schema remains at `tes/tes.swagger.json`. `make proto` installs pinned Go plugins;
`buf.gen.go.yaml` controls Go generation. `buf.gen.yaml` retains the optional
multi-language generation configuration.

Run `python3 .github/scripts/check-public-api.py` to check the client dependency
boundary, compile an external client and plugin consumer, and verify Go rejects
external imports of server implementations. CI runs this check on pull requests.

This remains one Go module. The client compiles without server, cloud, database,
Kubernetes, gRPC, or gateway packages, but independent client dependency versions
and releases would require a separate module.
