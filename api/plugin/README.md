# Authorization plugin SDK

Import `github.com/calypr/funnel/api/plugin` as `plugin` for the generated
messages, `Authorize` interface, handshake, and transport adapters. Both Funnel
and external plugin implementations use this package.

Implement `plugin.Authorize` and supply the implementation through
`&plugin.AuthorizePlugin{Impl: impl}` for net/rpc or
`&plugin.AuthorizeGRPCPlugin{Impl: impl}` for gRPC. Pass `plugin.Handshake` to
HashiCorp go-plugin on both sides. Host executable discovery and process lifecycle
management remain internal to Funnel.

## Bearer authentication

At startup Funnel sends one `Type_CAPABILITIES` action. A plugin that
authenticates bearer tokens returns `Type_AUTHENTICATE` in
`JobResponse.Capabilities`. Plugins that return an error or an empty list keep
their task-only behavior, so existing plugins need no change.

When the plugin reports `Type_AUTHENTICATE`, Funnel sends the bearer header in
a `Type_AUTHENTICATE` action before running each requested RPC. This action
receives plugin parameters but no task or server configuration. Return HTTP
status 200 and a stable, verified subject in `JobResponse.UserId`. Funnel uses
that subject for task access checks and does not grant administrator
privileges. Keep project or task authorization in the existing task actions.

Basic credentials keep working alongside plugin authentication, for example for
workers. Funnel refuses to start if `Server.OidcAuth` is also configured,
because both would claim the same bearer tokens.

Owner-restricted task access requires an active authenticator. Funnel refuses to start when no configured authentication method is available.

## Task configuration

Plugins receive the complete server configuration in `PluginAction`. Return that
configuration with the changes needed for the task. Local and Kubernetes backends
merge the response into a per-task clone using `mergo.WithOverwriteWithEmptyValue`,
matching uc-cdis Funnel. Empty values take precedence over server defaults.

```go
effective := proto.Clone(inputConfig).(*config.Config)
effective.Worker.LeaveWorkDir = false
effective.Worker.ScratchPath = ""
delete(effective.Worker.Container.Env, "TEMP_TOKEN")

return &pluginproto.JobResponse{
	Code:   200,
	Config: effective,
}, nil
```

The example uses `proto` from `google.golang.org/protobuf/proto` and `pluginproto`
from `github.com/calypr/funnel/api/plugin`.

Return the complete edited input, including settings unrelated to the plugin's
changes. Partial responses can erase required configuration. False, zero, empty
strings, cleared collections, and removed map entries override inherited values.
The server's shared configuration stays unchanged between tasks. Existing task
validation still applies, including Kubernetes' fixed Jobs namespace.

No response flag or protobuf schema change is required. Plugins that previously
returned partial configurations must return the complete edited input instead.
