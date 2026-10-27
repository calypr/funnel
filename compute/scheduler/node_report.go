package scheduler

import (
	"errors"
	"google.golang.org/protobuf/proto"
)

// ErrNodeChanged means a health decision used an obsolete observation.
var ErrNodeChanged = errors.New("node changed since observation")

// NodeReportChanged excludes assignments, available capacity, and heartbeat time.
func NodeReportChanged(report, stored *Node) bool {
	if report.State != stored.State || !proto.Equal(report.Resources, stored.Resources) || report.Hostname != stored.Hostname || report.Preemptible != stored.Preemptible || report.Zone != stored.Zone {
		return true
	}
	for k, v := range report.Metadata {
		if old, ok := stored.Metadata[k]; !ok || old != v {
			return true
		}
	}
	return false
}

// ApplyNodeReport retains the scheduler-owned reservation fields.
func ApplyNodeReport(stored, report *Node) {
	stored.State = report.State
	stored.Resources = nil
	if report.Resources != nil {
		stored.Resources = proto.Clone(report.Resources).(*Resources)
	}
	stored.Hostname, stored.Zone, stored.Preemptible = report.Hostname, report.Zone, report.Preemptible
	if stored.Metadata == nil {
		stored.Metadata = make(map[string]string)
	}
	for k, v := range report.Metadata {
		stored.Metadata[k] = v
	}
}
