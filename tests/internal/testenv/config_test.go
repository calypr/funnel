package testenv

import (
	"strconv"
	"testing"

	appconfig "github.com/calypr/funnel/internal/config"
)

func TestRandomPortConfigReturnsDistinctPortNumbers(t *testing.T) {
	for i := 0; i < 20; i++ {
		conf := RandomPortConfig(appconfig.DefaultConfig())
		if conf.Server.HTTPPort == conf.Server.RPCPort {
			t.Fatalf("HTTP and RPC ports are the same: %s", conf.Server.HTTPPort)
		}

		for _, port := range []string{conf.Server.HTTPPort, conf.Server.RPCPort} {
			number, err := strconv.Atoi(port)
			if err != nil || number < 1 || number > 65535 {
				t.Fatalf("invalid allocated port %q: %v", port, err)
			}
		}
		if conf.RPCClient.ServerAddress != appconfig.RPCAddress(conf.Server) {
			t.Fatal("RPC client address does not match allocated server address")
		}
	}
}
