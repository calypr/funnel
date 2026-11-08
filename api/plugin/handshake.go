package plugin

import (
	hplugin "github.com/hashicorp/go-plugin"
)

// Handshake is a common handshake that is shared by plugin and host.
var Handshake = hplugin.HandshakeConfig{
	// This isn't required when using VersionedPlugins
	ProtocolVersion:  1,
	MagicCookieKey:   "AUTHORIZE_PLUGIN",
	MagicCookieValue: "authorize",
}

// PluginMap is the map of plugins we can dispense.
var PluginMap = map[string]hplugin.Plugin{
	"authorize_grpc": &AuthorizeGRPCPlugin{},
	"authorize":      &AuthorizePlugin{},
}
