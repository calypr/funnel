package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	taskCmd "github.com/calypr/funnel/cmd/task"
	"github.com/calypr/funnel/tests"
)

func TestCreateStdin(t *testing.T) {
	tests.ParseConfig()
	conf := tests.DefaultConfig()
	conf.Compute = "noop"
	fun := tests.NewFunnel(conf)
	fun.StartServer()

	a, err := os.ReadFile("hello-world.json")
	if err != nil {
		t.Fatalf("read task fixture: %v", err)
	}

	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	in.Write(a)
	in.Write(a)

	err = taskCmd.Create(conf.Server.HTTPAddress(), []string{"hello-world.json"}, in, out)
	if err != nil {
		t.Fatal(err)
	}

	ids := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(ids) != 3 {
		t.Fatal("err", ids)
	}
}
