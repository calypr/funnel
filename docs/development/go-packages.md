# Go packages and the TES client

Funnel's public Go API is the `github.com/calypr/funnel/tes` package. It contains
TES wire models and a REST client. Server implementations are under `internal/`,
where Go prevents external modules from importing them.

```go
client, err := tes.NewClient("https://example.org/prefix",
    tes.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}))
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

Import `context`, `net/http`, `time`, and `github.com/calypr/funnel/tes` as needed.
The client preserves URL prefixes and accepts a caller-owned HTTP client for
transports, timeouts, proxies, and testing. List responses return the next page
token; callers choose when to fetch another page. Requests use the caller's
context, and the server owns task validation and initialization.

The library does not read `FUNNEL_SERVER_USER`, `FUNNEL_SERVER_PASSWORD`,
`FUNNEL_SERVER_TOKEN`, or `FUNNEL_ALLOW_INSECURE_CREDENTIALS`. Set `User`,
`Password`, or `Token` explicitly. Bearer tokens take precedence over Basic
credentials. Credentials require HTTPS except for loopback hosts; applications
that deliberately permit plaintext credentials must pass
`tes.WithInsecureCredentials()`. The same rule covers redirects. The CLI retains
its environment-variable behavior through `internal/cli/tesclient`.

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
