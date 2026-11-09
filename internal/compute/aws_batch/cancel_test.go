package aws_batch

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	batchtypes "github.com/aws/aws-sdk-go-v2/service/batch/types"
	"github.com/calypr/funnel/api/tes"
)

type terminatingBatchClient struct {
	mockClient
	terminate   func(context.Context, *batch.TerminateJobInput) (*batch.TerminateJobOutput, error)
	taskID      string
	describeErr error
}

func (c *terminatingBatchClient) DescribeJobs(context.Context, *batch.DescribeJobsInput, ...func(*batch.Options)) (*batch.DescribeJobsOutput, error) {
	if c.describeErr != nil {
		return nil, c.describeErr
	}
	if c.taskID == "" {
		return &batch.DescribeJobsOutput{}, nil
	}
	return &batch.DescribeJobsOutput{Jobs: []batchtypes.JobDetail{{JobId: aws.String("provider-job"), Parameters: map[string]string{"taskID": c.taskID}}}}, nil
}

func (c *terminatingBatchClient) TerminateJob(ctx context.Context, input *batch.TerminateJobInput, _ ...func(*batch.Options)) (*batch.TerminateJobOutput, error) {
	return c.terminate(ctx, input)
}

func TestCancelTerminatesProviderJobAfterStateChange(t *testing.T) {
	for _, state := range []tes.State{tes.Queued, tes.Running, tes.Canceled} {
		t.Run(state.String(), func(t *testing.T) {
			wantErr := errors.New("provider response")
			called := false
			client := &terminatingBatchClient{taskID: "task-1", terminate: func(ctx context.Context, req *batch.TerminateJobInput) (*batch.TerminateJobOutput, error) {
				called = true
				if ctx != t.Context() || aws.ToString(req.JobId) != "provider-job" {
					t.Error("lost caller context or provider job identity")
				}
				return nil, wantErr
			}}
			b := &Backend{client: client, database: batchTaskReader{task: &tes.Task{Id: "task-1", State: state, Logs: []*tes.TaskLog{{Metadata: map[string]string{"awsbatch_id": "provider-job"}}}}}}
			if err := b.Cancel(t.Context(), "task-1"); !errors.Is(err, wantErr) {
				t.Errorf("cancel error = %v, want provider error", err)
			}
			if !called {
				t.Error("cancellation did not reach provider termination")
			}
		})
	}
}

func TestCancelRequiresProviderTaskBinding(t *testing.T) {
	for _, tc := range []struct {
		name, providerTask string
		lookupErr          error
	}{
		{"different task", "task-2", nil},
		{"missing job", "", nil},
		{"provider lookup failure", "task-1", errors.New("lookup failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			client := &terminatingBatchClient{taskID: tc.providerTask, describeErr: tc.lookupErr, terminate: func(context.Context, *batch.TerminateJobInput) (*batch.TerminateJobOutput, error) {
				called = true
				return nil, nil
			}}
			b := &Backend{client: client, database: batchTaskReader{task: &tes.Task{Id: "task-1", State: tes.Canceled, Logs: []*tes.TaskLog{{Metadata: map[string]string{"awsbatch_id": "provider-job"}}}}}}
			err := b.Cancel(t.Context(), "task-1")
			if err == nil || called {
				t.Errorf("termination called=%t error=%v without a provider task binding", called, err)
			}
			if tc.lookupErr != nil && !errors.Is(err, tc.lookupErr) {
				t.Error("provider lookup error was lost")
			}
		})
	}
}
