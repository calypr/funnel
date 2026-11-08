package metrics

import (
	"github.com/calypr/funnel/events"
	"github.com/prometheus/client_golang/prometheus"
)

// logEventCollector reads the logging pipeline's counters at scrape time.
// Pulling the values avoids a dependency from events back to server metrics.
type logEventCollector struct {
	processed *prometheus.Desc
	dropped   *prometheus.Desc
}

func newLogEventCollector() *logEventCollector {
	return &logEventCollector{
		processed: prometheus.NewDesc("funnel_logging_events_total", "Total number of log events processed.", []string{"type"}, nil),
		dropped:   prometheus.NewDesc("funnel_logging_events_dropped_total", "Total number of log events that were dropped due to busy writer channel.", []string{"type"}, nil),
	}
}

func (c *logEventCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.processed
	ch <- c.dropped
}

func (c *logEventCollector) Collect(ch chan<- prometheus.Metric) {
	for _, stream := range []struct {
		typ   events.Type
		label string
	}{{events.Type_EXECUTOR_STDOUT, "stdout"}, {events.Type_EXECUTOR_STDERR, "stderr"}} {
		processed, dropped := events.LogEventCounts(stream.typ)
		ch <- prometheus.MustNewConstMetric(c.processed, prometheus.CounterValue, float64(processed), stream.label)
		ch <- prometheus.MustNewConstMetric(c.dropped, prometheus.CounterValue, float64(dropped), stream.label)
	}
}
