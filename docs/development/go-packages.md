# Go packages and the TES client

For existing consumers, follow the [Funnel upgrade guide](../upgrading.md) to
migrate imports, client credentials, and plugins.

Funnel separates TES wire models from the REST client:

- `github.com/calypr/funnel/api/tes`: shared generated TES models, enum conveniences,
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
`github.com/calypr/funnel/api/tes` for the models. Import `context`, `net/http`, and
`time` as needed.
The client preserves URL prefixes and accepts a caller-owned HTTP client for
transports, timeouts, proxies, and testing. List responses return the next page
token; callers choose when to fetch another page. Requests use the caller's
context, and the server owns task validation and initialization.

`NewClient` reads `FUNNEL_SERVER_USER`, `FUNNEL_SERVER_PASSWORD`,
`FUNNEL_SERVER_TOKEN`, and `FUNNEL_ALLOW_INSECURE_CREDENTIALS` as constructor
defaults, preserving existing library and CLI behavior. Set `User`, `Password`,
or `Token` afterward, or use a `ClientOption`, to override those defaults.
Bearer tokens take precedence over Basic credentials. Credentials require HTTPS
except for loopback hosts. Remote plaintext credentials and authenticated
HTTPS-to-HTTP redirects require `FUNNEL_ALLOW_INSECURE_CREDENTIALS=true` or
`tesclient.WithInsecureCredentials()`. CLI commands use the same public constructor.

The client and dashboard node requests share a focused `internal/httpauth` credential
transport policy. It is a private helper with only standard-library dependencies.
This policy covers plaintext restrictions and redirects; it
does not read process environment variables. HTTP response parsing remains
private to the TES client. The CLI owns output formatting, and callers that need
model JSON serialization can use protobuf's `protojson` package directly.

The former `tes.NewClient` API moves to `client/tes.NewClient`; its request and
response types use `api/tes` imports. There is no forwarding client in the
model package. The mutable `tes.Marshaler` global, `tes.MarshalToString` wrapper,
and the client's formatting-only `Marshaler` field have been removed.

The root `version` package exposes build metadata without importing application
implementation. Makefile and release builds populate it through linker flags.

## Application structure

- `internal/cli`: commands and application assembly; root `main.go` remains the
  executable entry point, including `go install github.com/calypr/funnel`.
- `internal/server`: listener lifecycle, service registration, route assembly, and
  graceful shutdown. It serves generated RPC interfaces rather than concrete task
  service implementations.
- `internal/server/auth`: Basic/OIDC authentication, JWKS, audit, and event-write
  authorization.
- `internal/server/middleware`: RPC recovery/debug logging and HTTP message/body
  limits and plugin header forwarding.
- `internal/server/gateway`: JSON marshaling, task-view presentation, content
  negotiation, and HTTP error responses.
- `internal/server/taskservice`: TES task operations, plugin actions, and compute/
  event coordination. Its Close waits for submissions and closes owned plugin
  processes once.
- `internal/identity`: request identity and task-access policy shared by server
  authentication and database implementations.
- `internal/compute`, `internal/database`, `internal/worker`, `internal/storage`,
  `internal/events`, and `internal/metrics`: application subsystems.
- `internal/compute`: shared backend contracts and validation; HPC implementations
  live under `internal/compute/hpc`.
- `internal/worker/workspace`: task path mapping, confinement, and mounted volumes.
- `internal/cli/gce`: the GCE command and its metadata-to-config adapter.
- `internal/task`: task validation, initialization, state transitions,
  projections, log mutation, and server pagination policy.
- `internal/tesrpc`: generated gRPC and HTTP gateway service bindings.
- `internal/pluginmanager`: application plugin loading.
- `internal/config`: application defaults, YAML loading/writing, templates and
  examples, redaction, and response-limit resolution.
- `internal/config/aws`: AWS SDK configuration loading shared by AWS consumers;
  the parent config package does not import it.
- `internal/logger`: runtime logging, logging defaults, and server interceptors.
- `internal/storage`: storage enablement and credential/environment selection.
- `internal/compute/kubernetes/taskresources`: Kubernetes task defaults and limits.
- `internal/webdash`: Go dashboard serving and embedded build output. Frontend
  source and npm commands remain in `webdash/`.

The public `api/config` and `api/logger` packages contain generated schemas.
The net/rpc adapters in `api/plugin` carry complete protobuf messages inside Gob
to preserve optional fields and oneofs without global type registrations.
Server address formatting lives in `internal/config`, alongside application
configuration defaults and loading. `api/plugin` contains the public plugin contract, handshake, and net/rpc and
gRPC adapters. Host process discovery, launching, and logging stay in
`internal/pluginmanager`. The former `plugins/shared` package has been removed;
plugin implementations should import `api/plugin`. External Go consumers must update imports and rebuild plugins. Plugin configuration, TES messages,
protobuf field numbers, service names, and JSON names remain compatible.
Application implementation imports such as `funnel/server`, `funnel/worker`,
and TES server helpers are no longer public Go APIs. External applications should
use the REST client; application extensions must live inside this module or use
the plugin contracts. No forwarding packages preserve the former import paths.

The shared integration harness lives in `tests/internal/testenv`. Go restricts its
imports to the tests tree; individual package fixtures remain beside their owners.
Directory creation uses `os.MkdirAll` directly, with `filepath.Dir` when creating
parents for a file. Existing regular files cannot satisfy directory requests.

## Generation and verification

Run `make proto` to generate public message types from the TES OpenAPI schema
and service bindings from the same source. The schemas share the protobuf
package `tes`; Go code uses separate public and internal packages. The REST
schema lives at `api/tes/tes.swagger.json`. All seven protobuf sources and the
Buf module, dependency lock, and generation configuration live under `api/`.
The TES-specific converter lives in `api/tes/internal/openapi2proto`.
`make proto` converts the upstream TES OpenAPI schema to protobuf, then runs
`buf generate` from `api/`. It installs pinned local Go plugins;
`api/buf.gen.yaml` controls messages, gRPC, gateway, and Swagger generation.
Go output follows `go_package`, keeping private bindings under top-level
`internal/`. There is one generation configuration; the former unused Python
generation template has been removed.

Proto imports are relative to `api/`, preserving logical paths such as
`tes/tes.proto` and `config/config.proto`. The plugin schema moves from
`plugins/proto/plugin.proto` to `plugin/plugin.proto`, and the scheduler schema
moves from `internal/compute/scheduler/scheduler.proto` to
`internal/scheduler/scheduler.proto`. Downstream proto consumers must update
these imports. Protobuf namespaces and service identities stay unchanged; these
file moves intentionally break the configured Buf `FILE` compatibility policy.
Run Buf commands from `api/`, including `buf lint` and `buf breaking --against`
with the desired baseline.

Run `python3 .github/scripts/check-public-api.py` to check the client dependency
boundary, compile an external client and plugin consumer, and verify Go rejects
external imports of server implementations. CI runs this check on pull requests.

This remains one Go module. The client compiles without server, cloud, database,
Kubernetes, gRPC, or gateway packages, but independent client dependency versions
and releases would require a separate module.

Configuration type imports remain `github.com/calypr/funnel/api/config` and
`github.com/calypr/funnel/api/logger`. Application helpers such as `DefaultConfig`,
`ParseFile`, `ToYamlFile`, and `Safe` are private to Funnel. Plugins should use
the configuration supplied by the host and protobuf serialization directly.
The public schemas have no dependency on YAML loaders, validators, Kubernetes,
or application internals. CI checks this and exercises plugin protobuf and Gob
serialization from a separate module.
