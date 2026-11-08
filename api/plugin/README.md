# Authorization plugin SDK

Import `github.com/calypr/funnel/api/plugin` as `plugin` for the generated
messages, `Authorize` interface, handshake, and transport adapters. Both Funnel
and external plugin implementations use this package.

Implement `plugin.Authorize` and supply the implementation through
`&plugin.AuthorizePlugin{Impl: impl}` for net/rpc or
`&plugin.AuthorizeGRPCPlugin{Impl: impl}` for gRPC. Pass `plugin.Handshake` to
HashiCorp go-plugin on both sides. Host executable discovery and process lifecycle
management remain internal to Funnel.

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
