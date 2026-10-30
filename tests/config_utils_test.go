package tests

import (
	"net"
	"testing"

	"github.com/calypr/funnel/config"
)

func TestRandomPortConfigReturnsDistinctAvailablePorts(t *testing.T) {
	for i := 0; i < 20; i++ {
		conf := RandomPortConfig(config.DefaultConfig())
		if conf.Server.HTTPPort == conf.Server.RPCPort {
			t.Fatalf("HTTP and RPC ports are the same: %s", conf.Server.HTTPPort)
		}

		func() {
			listeners := make([]net.Listener, 0, 2)
			defer func() {
				for _, listener := range listeners {
					if err := listener.Close(); err != nil {
						t.Errorf("close reservation listener: %v", err)
					}
				}
			}()

			for _, port := range []string{conf.Server.HTTPPort, conf.Server.RPCPort} {
				listener, err := net.Listen("tcp", net.JoinHostPort("", port))
				if err != nil {
					t.Fatalf("port %s was unavailable after allocation: %v", port, err)
				}
				listeners = append(listeners, listener)
			}
		}()
	}
}
