package gcp_batch

import (
	"context"
	"errors"
	"testing"

	batch "cloud.google.com/go/batch/apiv1"
	"cloud.google.com/go/batch/apiv1/batchpb"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/tes"
	"github.com/googleapis/gax-go/v2"
)

type cancelingClient struct {
	mockClient
	cancel func(context.Context, *batchpb.CancelJobRequest) (*batch.CancelJobOperation, error)
}

func (c *cancelingClient) CancelJob(ctx context.Context, req *batchpb.CancelJobRequest, _ ...gax.CallOption) (*batch.CancelJobOperation, error) {
	return c.cancel(ctx, req)
}

type cancelTaskReader struct {
	tes.ReadOnlyServer
	task *tes.Task
	err  error
}

func (r cancelTaskReader) GetTask(context.Context, *tes.GetTaskRequest) (*tes.Task, error) {
	return r.task, r.err
}

func TestCancelDispatchesProviderRequest(t *testing.T) {
	const name = "projects/test/locations/region/jobs/task-1"
	for _, tc := range []struct {
		name, job            string
		readErr, providerErr error
		wantCall, wantErr    bool
	}{
		{"accepted", name, nil, nil, true, false},
		{"provider failure", name, nil, errors.New("provider failure"), true, true},
		{"missing metadata", "", nil, nil, true, false},
		{"unrelated metadata ignored", "projects/other/locations/elsewhere/jobs/task-2", nil, nil, true, false},
		{"read failure", name, errors.New("read failure"), nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			client := &cancelingClient{cancel: func(ctx context.Context, req *batchpb.CancelJobRequest) (*batch.CancelJobOperation, error) {
				called = true
				if ctx != t.Context() || req.Name != name {
					t.Error("lost caller context or provider resource name")
				}
				return nil, tc.providerErr
			}}
			b := &Backend{client: client, conf: &config.GCPBatch{Project: "test", Location: "region"}, database: cancelTaskReader{task: &tes.Task{State: tes.Canceled, Logs: []*tes.TaskLog{{Metadata: map[string]string{"gcpbatch_name": tc.job}}}}, err: tc.readErr}}
			err := b.Cancel(t.Context(), "task-1")
			if (err != nil) != tc.wantErr || called != tc.wantCall {
				t.Fatalf("called=%t error=%v", called, err)
			}
			if tc.providerErr != nil && !errors.Is(err, tc.providerErr) {
				t.Error("provider error was lost")
			}
		})
	}
}

func TestCancelRejectsInvalidTaskResourceSegment(t *testing.T) {
	for _, id := range []string{"", ".", "..", "task/part", "task\\part"} {
		called := false
		client := &cancelingClient{cancel: func(context.Context, *batchpb.CancelJobRequest) (*batch.CancelJobOperation, error) {
			called = true
			return nil, nil
		}}
		b := &Backend{client: client, conf: &config.GCPBatch{Project: "test", Location: "region"}, database: cancelTaskReader{task: &tes.Task{}}}
		if err := b.Cancel(t.Context(), id); err == nil || called {
			t.Errorf("invalid task resource segment %q reached provider", id)
		}
	}
}
