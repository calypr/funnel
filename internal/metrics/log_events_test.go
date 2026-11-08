package metrics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/funnel/internal/events"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type logMetricWriter func(context.Context, *events.Event) error

func (f logMetricWriter) WriteEvent(ctx context.Context, e *events.Event) error { return f(ctx, e) }
func (logMetricWriter) Close()                                                  {}

func loggingCounters(t *testing.T) map[string]float64 {
	t.Helper()
	response := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("scrape: %d %s", response.Code, response.Body.String())
	}
	counters := make(map[string]float64)
	for _, line := range strings.Split(response.Body.String(), "\n") {
		if !strings.HasPrefix(line, "funnel_logging_events_") {
			continue
		}
		fields := strings.Fields(line)
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			t.Fatal(err)
		}
		counters[fields[0]] = value
	}
	return counters
}

func waitForLogs(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("log delivery did not finish")
	}
}

func TestLogMetricsTrackDeliveredSnapshots(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, permanentFailure := range []bool{false, true} {
			name := "final"
			if streaming {
				name = "stream"
			}
			if permanentFailure {
				name += "/failed"
			} else {
				name += "/retried"
			}
			t.Run(name, func(t *testing.T) {
				before := loggingCounters(t)
				attempts := make(map[events.Type]int)
				sink := logMetricWriter(func(_ context.Context, e *events.Event) error {
					attempts[e.Type]++
					if permanentFailure {
						return status.Error(codes.PermissionDenied, "denied")
					}
					if attempts[e.Type] == 1 {
						return status.Error(codes.Unavailable, "retry")
					}
					return nil
				})
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var wg sync.WaitGroup
				var stdout, stderr io.Writer
				if streaming {
					stdout, stderr = events.StreamLogTail(ctx, "metrics", 0, 0, 1024, time.Hour, sink, &wg)
				} else {
					stdout, stderr = events.LogTail(ctx, "metrics", 0, 0, 1024, sink, &wg)
				}
				for _, output := range []io.Writer{stdout, stderr} {
					if _, err := io.WriteString(output, "log line\n"); err != nil {
						t.Fatal(err)
					}
				}
				cancel()
				waitForLogs(t, &wg)
				after := loggingCounters(t)
				for _, stream := range []string{"stdout", "stderr"} {
					key := `funnel_logging_events_total{type="` + stream + `"}`
					if _, ok := after[key]; !ok {
						t.Fatalf("missing counter %s", key)
					}
					want := 1.0
					if permanentFailure {
						want = 0
					}
					if delta := after[key] - before[key]; delta != want {
						t.Errorf("%s increased by %v, want %v", key, delta, want)
					}
				}
			})
		}
	}
}

func TestLogMetricsTrackBackpressureDrops(t *testing.T) {
	before := loggingCounters(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var wg sync.WaitGroup
	defer func() {
		releaseOnce.Do(func() { close(release) })
		cancel()
		waitForLogs(t, &wg)
	}()
	sink := logMetricWriter(func(ctx context.Context, _ *events.Event) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	stdout, stderr := events.StreamLogTail(ctx, "drops", 0, 0, 1024, time.Nanosecond, sink, &wg)
	for _, output := range []io.Writer{stdout, stderr} {
		for range 300 {
			if _, err := io.WriteString(output, "more logs\n"); err != nil {
				t.Fatal(err)
			}
		}
	}
	after := loggingCounters(t)
	for _, stream := range []string{"stdout", "stderr"} {
		key := `funnel_logging_events_dropped_total{type="` + stream + `"}`
		if after[key] <= before[key] {
			t.Errorf("%s did not increase while the writer was blocked", key)
		}
		key = `funnel_logging_events_total{type="` + stream + `"}`
		if after[key] != before[key] {
			t.Errorf("%s counts undelivered events", key)
		}
	}
}
