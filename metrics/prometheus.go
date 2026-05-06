package metrics

import (
	"context"
	"time"

	"github.com/alecthomas/units"
	"github.com/ohsu-comp-bio/funnel/compute/scheduler"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/prometheus/client_golang/prometheus"
)

func init() {
	prometheus.MustRegister(taskStates)
	prometheus.MustRegister(nodeStates)
	prometheus.MustRegister(nodeTotalCPU)
	prometheus.MustRegister(nodeTotalRAM)
	prometheus.MustRegister(nodeTotalDisk)
	prometheus.MustRegister(nodeAvailableCPU)
	prometheus.MustRegister(nodeAvailableRAM)
	prometheus.MustRegister(nodeAvailableDisk)
	prometheus.MustRegister(logEventsDropped)
	prometheus.MustRegister(logEventsTotal)
}

var taskStates = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "funnel",
		Subsystem: "tasks",
		Name:      "state_count",
		Help:      "Number of tasks in each state.",
	},
	[]string{"state"},
)

func init() {
	for key := range tes.State_value {
		taskStates.WithLabelValues(key).Set(0)
	}
}

// RecordLogEventDropped increments the counter for dropped log events.
func RecordLogEventDropped(eventType string) {
	logEventsDropped.WithLabelValues(eventType).Inc()
}

// RecordLogEvent increments the counter for processed log events.
func RecordLogEvent(eventType string) {
	logEventsTotal.WithLabelValues(eventType).Inc()
}

// TaskStateCounter is implemented by database backends which provide
// queries for counting tasks in each state.
type TaskStateCounter interface {
	// TaskStateCounts returns number of tasks in each state.
	TaskStateCounts(context.Context) (map[string]int32, error)
}

// WatchTaskStates updates the task state counter every 5 seconds.
// This blocks until the context is canceled.
func WatchTaskStates(ctx context.Context, counter TaskStateCounter) {

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			counts, err := counter.TaskStateCounts(ctx)
			if err != nil {
				break
			}
			for key, count := range counts {
				taskStates.WithLabelValues(key).Set(float64(count))
			}
		}
	}
}

var nodeStates = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "funnel",
		Subsystem: "nodes",
		Name:      "state_count",
		Help:      "Number of nodes in each state.",
	},
	[]string{"state"},
)

var nodeTotalCPU = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "funnel",
	Subsystem: "nodes",
	Name:      "total_cpus",
	Help:      "Total node CPUs.",
})

var nodeTotalRAM = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "funnel",
	Subsystem: "nodes",
	Name:      "total_ram_bytes",
	Help:      "Total node RAM, in bytes.",
})

var nodeTotalDisk = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "funnel",
	Subsystem: "nodes",
	Name:      "total_disk_bytes",
	Help:      "Total node disk space, in bytes.",
})

var nodeAvailableCPU = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "funnel",
	Subsystem: "nodes",
	Name:      "available_cpus",
	Help:      "Available node CPUs.",
})

var nodeAvailableRAM = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "funnel",
	Subsystem: "nodes",
	Name:      "available_ram_bytes",
	Help:      "Available node RAM, in bytes.",
})

var nodeAvailableDisk = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "funnel",
	Subsystem: "nodes",
	Name:      "available_disk_bytes",
	Help:      "Available node disk space, in bytes.",
})

var logEventsDropped = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "funnel",
		Subsystem: "logging",
		Name:      "events_dropped_total",
		Help:      "Total number of log events that were dropped due to busy writer channel.",
	},
	[]string{"type"},
)

var logEventsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "funnel",
		Subsystem: "logging",
		Name:      "events_total",
		Help:      "Total number of log events processed.",
	},
	[]string{"type"},
)

func resetNodes() {
	for key := range scheduler.NodeState_value {
		nodeStates.WithLabelValues(key).Set(0)
	}
}

func init() {
	resetNodes()
}

// WatchNodes updates the node state and resource counters every 5 seconds.
// This blocks until the context is canceled.
func WatchNodes(ctx context.Context, nodes scheduler.SchedulerServiceServer) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			resp, err := nodes.ListNodes(ctx, &scheduler.ListNodesRequest{})
			if err != nil {
				break
			}

			resetNodes()
			for _, node := range resp.Nodes {
				nodeStates.WithLabelValues(node.GetState().String()).Inc()
				res := node.GetResources()
				nodeTotalCPU.Set(float64(res.GetCpus()))
				nodeTotalRAM.Set(res.GetRamGb() * float64(units.GB))
				nodeTotalDisk.Set(res.GetDiskGb() * float64(units.GB))

				avail := node.GetAvailable()
				nodeAvailableCPU.Set(float64(avail.GetCpus()))
				nodeAvailableRAM.Set(avail.GetRamGb() * float64(units.GB))
				nodeAvailableDisk.Set(avail.GetDiskGb() * float64(units.GB))
			}
		}
	}
}
