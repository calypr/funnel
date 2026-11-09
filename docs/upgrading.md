# Upgrade Funnel

Use this guide when upgrading to the unreleased Go package reorganization and
the accompanying configuration and scheduler changes. It covers downstream Go
applications, authorization plugins, and existing deployments.

The Go package moves preserve TES message field numbers, JSON names, and service
identities. They do not preserve Go import paths. Configuration schema changes
have separate migration requirements described below.

## Prepare the deployment

1. Back up the task database, configuration, custom templates, and HPC submission
   records. Test the upgrade and rollback against a restored copy first.
2. Finish tasks whose Kubernetes storage plans use versions 1, 2, or 3. Use a
   compatible older binary to clean up their Jobs before upgrading.
3. For HPC jobs submitted without trusted submission records, cancel them using
   the scheduler's own command before upgrading.
4. Stop older Funnel servers, schedulers, and nodes before starting upgraded
   instances. Upgrade the participating servers, nodes, workers, and plugins
   together. Do not mix scheduler versions during the assignment migration.

See [Kubernetes storage ownership and upgrades](compute/kubernetes-storage.md#ownership-and-upgrades)
and [scheduler assignment storage upgrades](compute/deployment.md#assignment-storage-upgrade)
for the backend-specific requirements.

## Update Go imports

Use `github.com/calypr/funnel` as the module prefix. Replace the former
`github.com/ohsu-comp-bio/funnel` prefix if your application still uses it.
The paths in this table are relative to that prefix.

| Previous import or use | Replacement |
| --- | --- |
| `tes` request, response, and enum types | `api/tes` |
| `tes.NewClient` and `tes.Client` | `client/tes.NewClient` and `client/tes.Client` |
| `config` schema types | `api/config` |
| `logger` configuration schema types | `api/logger` |
| `plugins/proto` messages and `plugins/shared` interfaces, handshake, and adapters | `api/plugin` |
| `server`, `compute`, `worker`, `storage`, `database`, `events`, and other application implementations | Private under `internal/`. Use the REST client or plugin contracts for external integrations. |

There are no forwarding packages for the old imports. Go rejects external
imports of `internal/` packages. The generated Go gRPC and gateway bindings are
also internal. Use the REST client, or generate bindings in your own module if
you maintain a direct gRPC integration.

Replace `tes.Marshaler`, `tes.MarshalToString`, and the client's `Marshaler`
field with explicit `google.golang.org/protobuf/encoding/protojson` calls.
Application defaults, YAML loading, validation, redaction, asset access, runtime
logging, and plugin process management are no longer public library functions.
For plugins, use the configuration supplied by the host. For external tooling,
own the required configuration loading and validation instead of importing the
application implementation. `funnel examples default-config` prints the bundled
configuration example.

The public `api/config` and `api/logger` packages contain schema types. They do
not replace the old runtime helpers such as `config.DefaultConfig`, `ParseFile`,
`ToYamlFile`, or `logger.NewLogger`. The former `util` helpers have moved to their
owning packages or the standard library. Build metadata is public in `version`.

### Configure the REST client

Import the client separately from the TES models. Credentials can come from the
environment or be set on the client:

```go
package example

import (
    "context"
    "net/http"
    "time"

    "github.com/calypr/funnel/api/tes"
    tesclient "github.com/calypr/funnel/client/tes"
)

func GetTask(ctx context.Context, address, token, id string) (*tes.Task, error) {
    client, err := tesclient.NewClient(address,
        tesclient.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}))
    if err != nil {
        return nil, err
    }
    client.Token = token
    return client.GetTask(ctx, &tes.GetTaskRequest{Id: id, View: "FULL"})
}
```

`NewClient` continues to read `FUNNEL_SERVER_USER`, `FUNNEL_SERVER_PASSWORD`,
`FUNNEL_SERVER_TOKEN`, and `FUNNEL_ALLOW_INSECURE_CREDENTIALS` at construction.
These variables remain supported by both Go applications and the CLI. Set
`User`, `Password`, or `Token` afterward, or supply a `ClientOption`, to override
the environment defaults. A token takes precedence over Basic credentials.

Use HTTPS for credentials sent to remote hosts. The REST client allows loopback
HTTP. For deployments that deliberately permit remote plaintext credentials,
pass `tesclient.WithInsecureCredentials()` in Go or set
`FUNNEL_ALLOW_INSECURE_CREDENTIALS=true` for Go clients, the CLI, and connecting processes.
Authenticated remote plaintext gRPC connections require the explicit setting
or a secure tunnel ending on loopback. The opt-in also permits authenticated
HTTPS-to-HTTP redirects. See [remote server connections](integrations/remote-server.md).

Follow pagination tokens when listing tasks or nodes. Node listings default to
500 entries and accept page sizes from 1 through 1000. Upgrade older gRPC node
clients to use the new pagination fields.

See [Go packages and the TES client](development/go-packages.md) for the full
package layout and client behavior.

## Rebuild authorization plugins

Update plugins to import `api/config`, `api/tes`, and `api/plugin`. Consolidate
the old `plugins/proto` and `plugins/shared` imports into one `api/plugin`
import. Implement `plugin.Authorize`, use `plugin.Handshake`, and choose
`plugin.AuthorizePlugin` for net/rpc or `plugin.AuthorizeGRPCPlugin` for gRPC.
Use your own logger instead of the former `plugins/shared.Logger`.

Rebuild plugin binaries against the same Funnel module version as the upgraded
host and deploy them together. The net/rpc adapters now carry complete protobuf
messages inside Gob. This preserves optional TES fields and configuration
oneofs without global Gob registrations. The encoding differs from older
net/rpc binaries, so both peers must be rebuilt. The gRPC wire format is unchanged.
Host process loading remains internal.

### Return the complete edited configuration

Local and Kubernetes backends now merge plugin responses with
`WithOverwriteWithEmptyValue`. False, zero, empty strings, cleared collections,
and removed map entries can clear inherited settings. Plugins that return
partial configurations can erase required server settings.

Clone the supplied configuration, edit that copy, and return it with the task:

```go
package example

import (
    "github.com/calypr/funnel/api/config"
    "github.com/calypr/funnel/api/plugin"
    "github.com/calypr/funnel/api/tes"
    "google.golang.org/protobuf/proto"
)

func ConfigureTask(input *config.Config, task *tes.Task) *plugin.JobResponse {
    effective := proto.Clone(input).(*config.Config)
    effective.Worker.LeaveWorkDir = false
    return &plugin.JobResponse{Code: 200, Config: effective, Task: task}
}
```

Preserve settings unrelated to your changes. Kubernetes task overrides cannot
change the backend's Jobs namespace. The server validates the resulting task
configuration. See the [Go package contracts](development/go-packages.md#application-structure)
and `api/plugin/README.md` in the source checkout.

## Migrate deployment configuration

Remove retired keys instead of leaving unused sections in a full configuration.
The strict configuration parser rejects unknown fields.

- Remove `Elastic`, even if another database is selected. Elasticsearch users
  must migrate their data to a supported backend.
- Set `FTPStorage.Endpoint` when using configured FTP credentials. The hostname
  and port must match the requested origin. See [FTP credentials](storage/ftp.md).
- For Slurm, PBS, HTCondor, and Grid Engine, set `HPCStateDir` to an absolute,
  persistent controller directory with mode `0700`. Keep it outside task work
  directories and enabled local-storage roots. Use a bounded
  `LocalStorage.AllowedDirs` list or disable local storage. Back up this directory
  with the database. See [trusted HPC submission records](compute/deployment.md#trusted-hpc-submission-records).
- Update custom runtime templates that use `index .Env "KEY"` or
  `index .Tags "KEY"`. Use `.GetEnvArgs`, `.GetTagArgs`, or a `range` with `eq`.
  Runtime argument placeholders must reach the runtime unchanged. See
  [compute deployment templates](compute/deployment.md).

### Replace retired Kubernetes storage settings

If your configuration uses retired storage keys, migrate those definitions to
native objects under `Kubernetes.Storage`.
The retired names include `PVCMode`, `StorageClassName`, `SharedPVCName`,
`S3FilesFilesystemId`, `Bindings`, and `MountedInputs`. Replace
`MountedRead.WorkerMount` and `MountedWrite.WorkerMount` with `Volume`, using
`SubPath` when selecting one of several mounts of a volume.

Use a static logical name such as `workspace` for a PVC template's
`metadata.name`. Funnel generates `<taskID>-workspace`; remove old volume entries
that refer to the rendered task-specific claim name. Keep `WorkerImage`
independent of the storage definition.

The legacy `PVTemplate`, `PVCTemplate`, and `JobTemplate` settings remain a
fallback when `Storage` is absent. Their presence alone does not require a
conversion to native storage configuration.

For old Chicago binary configurations, export using the original schema before
converting to named configuration. Its `S3FilesFilesystemId` field number
conflicts with upstream `ForbiddenPathPrefixes`. Loading that binary directly
with the canonical schema cannot distinguish the two fields. Move the filesystem
ID into the native CSI PV template and rebuild affected plugins.

See [Kubernetes storage](compute/kubernetes-storage.md#ownership-and-upgrades)
and [mounted inputs](storage/mounted-inputs.md) for complete migration examples.

## Upgrade scheduler storage

Convert standalone MongoDB deployments to a replica set or sharded cluster
before upgrading. A single-node replica set is sufficient for development.
Changing only the connection URI does not enable transactions. Follow the
[MongoDB upgrade procedure](databases/mongodb.md#upgrade-an-existing-standalone-server).

At first startup, the upgraded scheduler imports active assignments into durable
claims. Resolve reported ownership conflicts or duplicate MongoDB IDs before
resuming submissions. Do not bypass failed migrations by running older servers
alongside the upgraded deployment. See [assignment storage upgrades](compute/deployment.md#assignment-storage-upgrade)
and [MongoDB startup migration](databases/mongodb.md#funnels-startup-migration).

## Update development and deployment automation

Keep provisioning scripts, service definitions, and infrastructure manifests in
operator-maintained deployment code. The bundled GCE provisioning, VM installer,
and systemd examples have been removed. Runtime compute backends and container
images remain available. The root executable and CLI remain reachable through
`go install github.com/calypr/funnel`.

Update scripts that refer to the former repository layout:

- Read documentation from `docs/`; Hugo configuration stays in `website/`.
- Use `tests/integration`, `tests/e2e`, and `tests/benchmarks` for the respective
  suites. Shared integration configuration files live in `tests/testdata/config`.
- Run `make proto` for schema generation through the Buf configuration in `api/`.
  Run direct Buf commands from `api/`. Proto imports are relative to that directory.
  Update `plugins/proto/plugin.proto` to `plugin/plugin.proto`,
  `events/events.proto` to `internal/events/events.proto`, and
  `compute/scheduler/scheduler.proto` to `internal/scheduler/scheduler.proto`.
  The TES service declaration is now in `internal/tesrpc/service.proto`; shared
  TES messages remain in `tes/tes.proto`. Message namespaces and service names
  remain unchanged, but file moves break Buf's `FILE` compatibility policy.
- Update custom linker flags to use `github.com/calypr/funnel/version`.
- Remove calls to the retired `bundle-examples` Make target.

The source build currently requires Go 1.27.3, as declared in `go.mod`.
Integration tests clean up generated files by default. Pass
`-funnel-keep-artifacts` when retaining them for debugging. Resources still close
when files are retained. See [developer test instructions](development/developers.md#go-tests)
and `tests/README.md` in the source checkout.

## Verify the upgrade

Build and test downstream applications and rebuilt plugins against the selected
Funnel version. In a test deployment, verify authenticated task creation,
retrieval, cancellation, input downloads, output publication, and pagination.
If authentication is configured, check that unauthorized requests remain
rejected. Check that plugins preserve unrelated configuration settings when
clearing individual values.

Confirm that startup migrations finish before resuming normal submissions.
Keep backups and older binaries until the upgraded deployment and its rollback
procedure have been verified.
