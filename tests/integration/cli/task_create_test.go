package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	appconfig "github.com/calypr/funnel/internal/config"

	taskCmd "github.com/calypr/funnel/internal/cli/task"
	"github.com/calypr/funnel/tests/internal/testenv"
)

func TestCreateStdin(t *testing.T) {
	testenv.ParseConfig()
	conf := testenv.DefaultConfig(t)
	conf.Compute = "noop"
	fun := testenv.NewFunnel(t, conf)
	fun.StartServer()

	a, err := os.ReadFile("testdata/hello-world.json")
	if err != nil {
		t.Fatalf("read task fixture: %v", err)
	}

	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	in.Write(a)
	in.Write(a)

	err = taskCmd.Create(appconfig.HTTPAddress(conf.Server), []string{"testdata/hello-world.json"}, in, out)
	if err != nil {
		t.Fatal(err)
	}

	ids := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(ids) != 3 {
		t.Fatal("err", ids)
	}
}
