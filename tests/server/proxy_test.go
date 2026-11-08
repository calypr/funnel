package server

import (
	"net/http"
	"testing"

	appconfig "github.com/calypr/funnel/internal/config"

	"github.com/calypr/funnel/tests"
)

func TestListNoCacheHeader(t *testing.T) {
	tests.SetLogOutput(log, t)
	resp, err := http.Get(appconfig.HTTPAddress(fun.Conf.Server) + "/v1/tasks")

	if err != nil {
		panic(err)
	}

	cch := resp.Header["Cache-Control"]
	if len(cch) < 1 || cch[0] != "no-store" {
		t.Error("Expected cache-control: no-store from List endpoint")
	}
}

func TestGetNoCacheHeader(t *testing.T) {
	tests.SetLogOutput(log, t)
	resp, err := http.Get(appconfig.HTTPAddress(fun.Conf.Server) + "/v1/tasks/1")

	if err != nil {
		panic(err)
	}

	cch := resp.Header["Cache-Control"]
	if len(cch) < 1 || cch[0] != "no-store" {
		t.Error("Expected cache-control: no-store from Get endpoint")
	}
}
