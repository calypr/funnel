package aws_batch

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
)

// mockClient implements the client interface for testing.
type mockClient struct {
	CancelJobFunc func(context.Context, *batch.CancelJobInput) (*batch.CancelJobOutput, error)
	SubmitJobFunc func(ctx context.Context, req *batch.SubmitJobInput) (*batch.SubmitJobOutput, error)
}

func (m *mockClient) SubmitJob(ctx context.Context, req *batch.SubmitJobInput, opts ...func(*batch.Options)) (*batch.SubmitJobOutput, error) {
	if m.SubmitJobFunc != nil {
		return m.SubmitJobFunc(ctx, req)
	}
	return &batch.SubmitJobOutput{JobId: aws.String("test-job-id")}, nil
}

func (m *mockClient) CancelJob(ctx context.Context, req *batch.CancelJobInput, opts ...func(*batch.Options)) (*batch.CancelJobOutput, error) {
	if m.CancelJobFunc != nil {
		return m.CancelJobFunc(ctx, req)
	}
	return &batch.CancelJobOutput{}, nil
}

func (m *mockClient) DescribeJobs(ctx context.Context, req *batch.DescribeJobsInput, opts ...func(*batch.Options)) (*batch.DescribeJobsOutput, error) {
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
		SubmitJobFunc: func(ctx context.Context, req *batch.SubmitJobInput) (*batch.SubmitJobOutput, error) {
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

type batchTaskReader struct {
	tes.ReadOnlyServer
	task *tes.Task
}

func (r batchTaskReader) GetTask(context.Context, *tes.GetTaskRequest) (*tes.Task, error) {
	return r.task, nil
}

func TestBatchRequestContexts(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var submitted, canceled bool
	client := &mockClient{
		SubmitJobFunc: func(got context.Context, req *batch.SubmitJobInput) (*batch.SubmitJobOutput, error) {
			submitted = true
			if got != ctx {
				t.Error("submission lost caller context")
			}
			if req.Parameters["taskID"] != "task-1" || aws.ToInt32(req.ContainerOverrides.Vcpus) != 2 || aws.ToInt32(req.ContainerOverrides.Memory) != 3814 {
				t.Fatalf("incorrect resource overrides %v", req)
			}
			return nil, got.Err()
		},
		CancelJobFunc: func(got context.Context, req *batch.CancelJobInput) (*batch.CancelJobOutput, error) {
			canceled = true
			if got != ctx {
				t.Error("cancellation lost caller context")
			}
			if aws.ToString(req.JobId) != "batch-job" {
				t.Error("cancellation lost AWS job ID")
			}
			return nil, got.Err()
		},
	}
	backend := &Backend{client: client, conf: &config.AWSBatch{JobDefinition: "definition", JobQueue: "queue"}, event: &noopEventWriter{}, database: batchTaskReader{task: &tes.Task{Id: "task-1", State: tes.Queued, Logs: []*tes.TaskLog{{Metadata: map[string]string{"awsbatch_id": "batch-job"}}}}}}
	task := &tes.Task{Id: "task-1", Name: "task", Resources: &tes.Resources{CpuCores: 2, RamGb: 4}}
	if err := backend.WriteEvent(ctx, events.NewTaskCreated(task)); !errors.Is(err, context.Canceled) {
		t.Fatalf("submit error = %v", err)
	}
	if err := backend.Cancel(ctx, "task-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	if !submitted || !canceled {
		t.Fatal("submission or cancellation was skipped")
	}
}

func TestNewBackendPreservesRetryAttempts(t *testing.T) {
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_MAX_ATTEMPTS", "")
	for _, test := range []struct {
		name       string
		maxRetries int32
		want       int
	}{
		{name: "legacy default", want: 4},
		{name: "configured retries", maxRetries: 7, want: 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			conf := &config.AWSBatch{
				AWSConfig:         &config.AWSConfig{Region: "us-east-1", DisableAutoCredentialLoad: true, MaxRetries: test.maxRetries},
				DisableReconciler: true,
			}
			backend, err := NewBackend(t.Context(), conf, nil, &noopEventWriter{}, logger.NewLogger("test", logger.DefaultConfig()))
			if err != nil {
				t.Fatal(err)
			}
			client := backend.client.(*batch.Client)
			if got := client.Options().RetryMaxAttempts; got != test.want {
				t.Fatalf("retry attempts = %d, want %d", got, test.want)
			}
		})
	}
}
