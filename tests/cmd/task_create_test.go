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

	a, _ := os.ReadFile("hello-world.json")
	b, _ := os.ReadFile("hello-world.json")

	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	in.Write(a)
	in.Write(b)

	err := taskCmd.Create(conf.Server.HTTPAddress(), []string{"hello-world.json"}, in, out)
	if err != nil {
		t.Fatal(err)
	}

	ids := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(ids) != 3 {
		t.Fatal("err", ids)
	}
}
