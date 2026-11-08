package core

import (
	"os"
	"testing"

	"github.com/calypr/funnel/internal/logger"
	logconfig "github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tests"
)

var fun *tests.Funnel
var log = logger.NewLogger("funnel-e2e-core", logconfig.DefaultConfig())

func TestMain(m *testing.M) {
	tests.ParseConfig()
	fun = tests.NewFunnel(tests.DefaultConfig())
	fun.StartServer()
	e := m.Run()
	os.Exit(e)
}
