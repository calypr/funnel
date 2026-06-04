package gcp_batch

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/logging/apiv2/loggingpb"
	"github.com/ohsu-comp-bio/funnel/events"
	"google.golang.org/api/iterator"
)

// fetchLogs attempts to fetch logs from Google Cloud Logging for a GCP Batch job
func (b *Backend) fetchLogs(ctx context.Context, taskID, gcpJobUid string) ([]*events.SystemLog, error) {
	if b.loggingAdminClient == nil {
		return nil, fmt.Errorf("Cloud Logging Admin client not initialized - logs can be found in GCP Console")
	}

	// Build filter to query logs for this specific job
	// Using the job_uid label as shown in the user's example
	filter := fmt.Sprintf(`labels.job_uid="%s"`, gcpJobUid)

	var systemLogs []*events.SystemLog

	// Create a log entry iterator using the admin client
	iter := b.loggingAdminClient.ListLogEntries(ctx, &loggingpb.ListLogEntriesRequest{
		ResourceNames: []string{fmt.Sprintf("projects/%s", b.conf.Project)},
		Filter:        filter,
	})

	// Iterate through log entries
	for {
		entry, err := iter.Next()
		if err != nil {
			if err == iterator.Done {
				break
			}
			return nil, fmt.Errorf("failed to read log entries: %w", err)
		}

		// Convert log entry to SystemLog
		var msg string
		switch payload := entry.Payload.(type) {
		case *loggingpb.LogEntry_TextPayload:
			msg = payload.TextPayload
		case *loggingpb.LogEntry_JsonPayload:
			msg = fmt.Sprintf("%v", payload.JsonPayload)
		case *loggingpb.LogEntry_ProtoPayload:
			msg = fmt.Sprintf("%v", payload.ProtoPayload)
		default:
			msg = ""
		}

		level := "info"

		systemLog := &events.SystemLog{
			Msg:   msg,
			Level: level,
			Fields: map[string]string{
				"gcpbatch_name":   taskID,
				"gcpbatch_job_id": gcpJobUid,
				"timestamp":       entry.Timestamp.AsTime().Format(time.RFC3339),
			},
		}

		// Include log severity if available
		if entry.Severity != 0 { // 0 is the default value for LogSeverity_DEFAULT
			systemLog.Level = entry.Severity.String()
		}

		// Include resource information if available
		if entry.Resource != nil {
			systemLog.Fields["resource_type"] = entry.Resource.Type
			if len(entry.Resource.Labels) > 0 {
				for k, v := range entry.Resource.Labels {
					systemLog.Fields["resource_"+k] = v
				}
			}
		}

		// Include labels if available
		if len(entry.Labels) > 0 {
			for k, v := range entry.Labels {
				systemLog.Fields["label_"+k] = v
			}
		}

		systemLogs = append(systemLogs, systemLog)
	}

	if len(systemLogs) == 0 {
		return nil, fmt.Errorf("no logs found for job %s (job_uid: %s)", taskID, gcpJobUid)
	}

	b.log.Debug("Retrieved logs from Cloud Logging",
		"taskID", taskID,
		"jobID", gcpJobUid,
		"logCount", len(systemLogs))

	return systemLogs, nil
}
