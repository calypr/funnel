package worker

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestWorkerBatchInstanceTypeMetadata(t *testing.T) {
	for _, tc := range []struct {
		name        string
		batch, fail bool
		want        string
	}{
		{name: "batch", batch: true, want: "m7i.large"},
		{name: "unavailable", batch: true, fail: true},
		{name: "not a batch job"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if tc.fail {
					http.Error(w, "not available", http.StatusForbidden)
					return
				}
				switch {
				case r.Method == http.MethodPut && r.URL.Path == "/latest/api/token":
					w.Header().Set("X-aws-ec2-metadata-token-ttl-seconds", "300")
					if _, err := io.WriteString(w, "test-token"); err != nil {
						t.Errorf("write token response: %v", err)
					}
				case r.Method == http.MethodGet && r.URL.Path == "/latest/meta-data/instance-type" && r.Header.Get("X-aws-ec2-metadata-token") == "test-token":
					if _, err := io.WriteString(w, "m7i.large\n"); err != nil {
						t.Errorf("write instance type response: %v", err)
					}
				default:
					t.Errorf("unexpected metadata request %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", http.StatusBadRequest)
				}
			}))
			defer server.Close()
			t.Setenv("AWS_EC2_METADATA_SERVICE_ENDPOINT", server.URL)
			t.Setenv("AWS_EC2_METADATA_DISABLED", "false")
			if tc.batch {
				t.Setenv("AWS_BATCH_JOB_ID", "batch-1")
			} else {
				t.Setenv("AWS_BATCH_JOB_ID", "")
			}
			task := &tes.Task{Id: "metadata-test", State: tes.Queued}
			writer := &publicationEvents{}
			w := &DefaultWorker{Conf: &config.Worker{WorkDir: t.TempDir(), PollingRate: durationpb.New(time.Hour), MaxParallelTransfers: 1}, TaskReader: &Base64TaskReader{task: task}, EventWriter: writer}
			if err := w.Run(context.Background()); err != nil {
				t.Fatalf("worker failed: %v", err)
			}
			var got string
			for _, ev := range writer.snapshot() {
				if metadata := ev.GetMetadata(); metadata != nil {
					got = metadata.Value["instance_type"]
					if got != "" {
						break
					}
				}
			}
			if got != tc.want {
				t.Errorf("instance type = %q, want %q", got, tc.want)
			}
			if !tc.batch && requests.Load() != 0 {
				t.Errorf("non-Batch worker called metadata service %d times", requests.Load())
			}
			if tc.batch && requests.Load() == 0 {
				t.Error("Batch worker did not contact metadata service")
			}
		})
	}
}

func TestBatchMetadataRespectsDeadline(t *testing.T) {
	t.Setenv("AWS_BATCH_JOB_ID", "batch-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "false")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	t.Setenv("AWS_EC2_METADATA_SERVICE_ENDPOINT", server.URL)
	writer := &publicationEvents{}
	started := time.Now()
	writeBatchInstanceType(context.Background(), events.NewTaskWriter("task", 0, writer))
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("metadata lookup took %s; expected the worker's timeout to stop it", elapsed)
	}
	for _, ev := range writer.snapshot() {
		if metadata := ev.GetMetadata(); metadata != nil && strings.TrimSpace(metadata.Value["instance_type"]) != "" {
			t.Fatal("unexpected metadata")
		}
	}
}
