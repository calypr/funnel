package tasks

import (
	"os"
	"testing"

	"github.com/calypr/funnel/internal/logger"
	"github.com/calypr/funnel/tests/internal/testenv"
)

var fun *testenv.Funnel
var log = logger.NewLogger("funnel-e2e-core", logger.DefaultConfig())

func TestMain(m *testing.M) {
	os.Exit(testenv.RunSuite(m, func(scope testenv.CleanupScope) bool {
		fun = testenv.NewFunnel(scope, testenv.DefaultConfig(scope))
		fun.StartServer()
		return true
	}))
}
