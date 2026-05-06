// Plugin manager used by the main application to load and invoke plugins.
//
// Adapted from 'RPC-based plugins in Go' by Eli Bendersky (@eliben):
// ref: https://eli.thegreenplace.net/2023/rpc-based-plugins-in-go/
// ref: https://github.com/eliben/code-for-blog/blob/main/2023/go-plugin-htmlize-rpc/plugin/manager.go
package shared

import (
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/hashicorp/go-plugin"
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
		client := plugin.NewClient(&plugin.ClientConfig{
			HandshakeConfig: Handshake,
			Plugins:         PluginMap,
			Logger:          Logger,
			Cmd:             exec.Command(bpath),
			AllowedProtocols: []plugin.Protocol{
				plugin.ProtocolNetRPC, plugin.ProtocolGRPC}})

		m.pluginClients = append(m.pluginClients, client)
	}

	return nil
}

func (m *Manager) Client(path string) (Authorize, error) {
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

	// We should have an Authorize function now! This feels like a normal interface
	// implementation but is in fact over an RPC connection.
	authorize := raw.(Authorize)

	return authorize, nil
}

func (m *Manager) Close() {
	for _, client := range m.pluginClients {
		client.Kill()
	}
}
