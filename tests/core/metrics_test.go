package core

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/k0kubun/pp"

	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests"
	"github.com/prometheus/common/expfmt"
)

func TestMetrics(t *testing.T) {
	conf := tests.DefaultConfig()
	if conf.Database != "mongodb" {
		t.Skip("metrics are only supported by mongodb")
	}

	fun := tests.NewFunnel(conf)
	fun.StartServer()
	tests.SetLogOutput(log, t)

	id1 := fun.Run(`'echo hello world'`)
	id2 := fun.Run(`'echo hello world'`)
	id3 := fun.Run(`'exit 1'`)
	id4 := fun.Run(`'sleep 100'`)
	_ = id4

	fun.Wait(id1)
	fun.Wait(id2)
	fun.Wait(id3)

	bg := context.Background()
	resp, err := fun.HTTP.GetServiceInfo(bg, &tes.GetServiceInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}

	_ = resp //TODO: Add more tests here

	/*
		// Metrics no longer in service info as of TES 1.1
		log.Info("INFO", resp)
		counts := resp.TaskStateCounts
		if len(counts) != 9 || counts["COMPLETE"] != 2 || counts["EXECUTOR_ERROR"] != 1 || counts["RUNNING"] != 1 {
			t.Error("unexpected counts from service info")
		}
	*/

	// TODO unfortunately, we have to wait for the prometheus poller to update
	time.Sleep(20 * time.Second)

	hresp, err := http.Get(fun.Conf.Server.HTTPAddress() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := hresp.Body.Close()
		if err != nil {
			t.Errorf("close metrics response body: %v", err)
		}
	}()

	parser := expfmt.TextParser{}
	met, err := parser.TextToMetricFamilies(hresp.Body)
	if err != nil {
		t.Fatal(err)
	}

	// It's tedious to check these so just find one.
	for _, m := range met["funnel_tasks_state_count"].Metric {
		if *m.Label[0].Name == "state" && (*m.Label[0].Value == "EXECUTOR_ERROR" || *m.Label[0].Value == "RUNNING") {
			if *m.Gauge.Value != 1 {
				t.Errorf("unexpected counts from prometheus endpoint: state: %s, count: %v != 1", *m.Label[0].Value, *m.Gauge.Value)
			}
		}
	}
	_, err = pp.Println(met["funnel_tasks_state_count"].Metric)
	if err != nil {
		t.Fatalf("print task state metrics: %v", err)
	}
}
