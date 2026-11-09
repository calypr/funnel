// Plugin manager used by the main application to load and invoke plugins.
//
// Adapted from 'RPC-based plugins in Go' by Eli Bendersky (@eliben):
// ref: https://eli.thegreenplace.net/2023/rpc-based-plugins-in-go/
// ref: https://github.com/eliben/code-for-blog/blob/main/2023/go-plugin-htmlize-rpc/plugin/manager.go
package pluginmanager

import (
	"fmt"
	"os/exec"
	"path/filepath"

	pluginapi "github.com/calypr/funnel/api/plugin"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
)

// Manager loads and manages Authorizer plugins for this application.
//
// After creating a Manager value, call LoadPlugins with a directory path to
// discover and load plugins. At the end of the program call Close to kill and
// clean up all plugin processes.
type Manager struct {
	pluginClients []*goplugin.Client
}

// LoadPlugins takes a directory path and assumes that all files within it
// are plugin binaries. It runs all these binaries in sub-processes,
// establishes RPC communication with the plugins, and registers them for
// the hooks they declare to support.
func (m *Manager) LoadPlugin(path string) error {
	binaries, err := goplugin.Discover("*", filepath.Dir(path))
	if err != nil {
		return err
	}

	for _, bpath := range binaries {
		client := goplugin.NewClient(&goplugin.ClientConfig{
			HandshakeConfig: pluginapi.Handshake,
			Plugins:         pluginapi.PluginMap,
			Logger:          hclog.New(&hclog.LoggerOptions{Name: "plugin", Level: hclog.Trace}),
			Cmd:             exec.Command(bpath),
			AllowedProtocols: []goplugin.Protocol{
				goplugin.ProtocolNetRPC, goplugin.ProtocolGRPC}})

		m.pluginClients = append(m.pluginClients, client)
	}

	return nil
}

func (m *Manager) Client(path string) (pluginapi.Authorize, error) {
	if err := m.LoadPlugin(path); err != nil {
		return nil, fmt.Errorf("failed to load plugins: %w", err)
	}

	if len(m.pluginClients) == 0 {
		return nil, fmt.Errorf("no plugins loaded")
	}

	// Connect via RPC
	// Here we just get the first plugin found in the specified plugin directory.
	// In future applications we'll want to have some logic to select the appropriate plugins.
	client, err := m.pluginClients[0].Client()
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	// Request the plugin
	raw, err := client.Dispense("authorize")
	if err != nil {
		return nil, fmt.Errorf("failed to dispense plugin: %w", err)
	}

	authorize := raw.(pluginapi.Authorize)

	return authorize, nil
}

func (m *Manager) Close() {
	for _, client := range m.pluginClients {
		client.Kill()
	}
}
