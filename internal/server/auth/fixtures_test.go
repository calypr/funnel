package auth

import (
	"bytes"
	"testing"

	"github.com/calypr/funnel/internal/logger"
)

func serverCaptureLogger(t *testing.T) (*logger.Logger, *bytes.Buffer) {
	t.Helper()
	conf := logger.DebugConfig()
	conf.Formatter = "json"
	output := &bytes.Buffer{}
	log := logger.NewLogger("server-test", conf)
	log.SetOutput(output)
	return log, output
}
