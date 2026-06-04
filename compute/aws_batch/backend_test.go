package aws_batch

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/batch"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
)

// mockClient implements the client interface for testing.
type mockClient struct {
	SubmitJobFunc func(req *batch.SubmitJobInput) (*batch.SubmitJobOutput, error)
}

func (m *mockClient) SubmitJob(req *batch.SubmitJobInput) (*batch.SubmitJobOutput, error) {
	if m.SubmitJobFunc != nil {
		return m.SubmitJobFunc(req)
	}
	return &batch.SubmitJobOutput{JobId: aws.String("test-job-id")}, nil
}

func (m *mockClient) CancelJob(req *batch.CancelJobInput) (*batch.CancelJobOutput, error) {
	return &batch.CancelJobOutput{}, nil
}

func (m *mockClient) DescribeJobsWithContext(ctx context.Context, req *batch.DescribeJobsInput, opts ...request.Option) (*batch.DescribeJobsOutput, error) {
	return &batch.DescribeJobsOutput{}, nil
}

// noopEventWriter implements events.Writer for testing.
type noopEventWriter struct{}

func (n *noopEventWriter) WriteEvent(ctx context.Context, ev *events.Event) error {
	return nil
}

func (n *noopEventWriter) Close() {}

func TestCreateJobc(t *testing.T) {
	log := logger.NewLogger("test", logger.DefaultConfig())

	var capturedReq *batch.SubmitJobInput
	mockClient := &mockClient{
		SubmitJobFunc: func(req *batch.SubmitJobInput) (*batch.SubmitJobOutput, error) {
			capturedReq = req
			return &batch.SubmitJobOutput{JobId: aws.String("test-job-id")}, nil
		},
	}

	b := &Backend{
		client:   mockClient,
		conf:     &config.AWSBatch{JobDefinition: "test-job-def", JobQueue: "test-job-queue"},
		event:    &noopEventWriter{},
		database: nil,
		log:      log,
	}

	task := &tes.Task{
		Id:   "task1",
		Name: "task1",
		Executors: []*tes.Executor{
			{
				Image:   "alpine",
				Command: []string{"echo", "hello world"},
			},
		},
	}

	err := b.Submit(task)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the request was captured
	if capturedReq == nil {
		t.Fatal("expected SubmitJob to be called, but it was not")
	}

	// Verify the job name is set
	if *capturedReq.JobName != "task1" {
		t.Errorf("expected JobName to be 'task1', got %s", *capturedReq.JobName)
	}
}
