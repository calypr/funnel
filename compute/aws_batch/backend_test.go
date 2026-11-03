package aws_batch

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	"github.com/aws/aws-sdk-go-v2/service/batch/types"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
			if req.Parameters["taskID"] != "task-1" || req.ContainerOverrides.Vcpus != nil || req.ContainerOverrides.Memory != nil || !reflect.DeepEqual(resourceValues(req.ContainerOverrides.ResourceRequirements), map[string]string{"MEMORY": "3814", "VCPU": "2"}) {
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

func TestSubmitResourceRequirements(t *testing.T) {
	for _, tc := range []struct {
		name      string
		resources *tes.Resources
		want      map[string]string
	}{
		{"none", nil, map[string]string{}},
		{"memory only", &tes.Resources{RamGb: 4}, map[string]string{"MEMORY": "3814"}},
		{"cpu only", &tes.Resources{CpuCores: 2}, map[string]string{"VCPU": "2"}},
		{"both", &tes.Resources{RamGb: 4, CpuCores: 2}, map[string]string{"MEMORY": "3814", "VCPU": "2"}},
		{
			"gpu with cpu and memory",
			&tes.Resources{
				CpuCores:                2,
				RamGb:                   4,
				BackendParameters:       map[string]string{"gpu_count": "2"},
				BackendParametersStrict: true,
			},
			map[string]string{"MEMORY": "3814", "VCPU": "2", "GPU": "2"},
		},
		{
			"gpu count with leading zeros",
			&tes.Resources{BackendParameters: map[string]string{"gpu_count": "02"}},
			map[string]string{"GPU": "2"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &mockClient{SubmitJobFunc: func(_ context.Context, req *batch.SubmitJobInput) (*batch.SubmitJobOutput, error) {
				if req.ContainerOverrides.Memory != nil || req.ContainerOverrides.Vcpus != nil || !reflect.DeepEqual(resourceValues(req.ContainerOverrides.ResourceRequirements), tc.want) {
					t.Fatalf("unexpected overrides: %+v", req.ContainerOverrides)
				}
				return &batch.SubmitJobOutput{JobId: aws.String("job")}, nil
			}}
			backend := &Backend{client: client, conf: &config.AWSBatch{}, event: &noopEventWriter{}}
			if err := backend.Submit(&tes.Task{Id: "task", Resources: tc.resources}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSubmitRejectsInvalidGPUCountBeforeAWSRequest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
	}{
		{name: "empty"},
		{name: "zero", value: "0"},
		{name: "zero with leading zeros", value: "00"},
		{name: "negative", value: "-1"},
		{name: "explicit positive sign", value: "+1"},
		{name: "fraction", value: "1.5"},
		{name: "word", value: "one"},
		{name: "integer overflow", value: "18446744073709551616"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var submitted bool
			client := &mockClient{SubmitJobFunc: func(_ context.Context, _ *batch.SubmitJobInput) (*batch.SubmitJobOutput, error) {
				submitted = true
				return &batch.SubmitJobOutput{JobId: aws.String("job")}, nil
			}}
			backend := &Backend{client: client, conf: &config.AWSBatch{}, event: &noopEventWriter{}}
			task := &tes.Task{Id: "task", Resources: &tes.Resources{BackendParameters: map[string]string{"gpu_count": tc.value}}}

			if err := backend.Submit(task); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("Submit error = %v, want InvalidArgument", err)
			}
			if submitted {
				t.Fatal("SubmitJob was called for invalid gpu_count")
			}
		})
	}
}

func TestCheckBackendParameterSupport(t *testing.T) {
	backend := &Backend{}
	for _, tc := range []struct {
		name      string
		params    map[string]string
		wantError bool
	}{
		{name: "gpu accepted", params: map[string]string{"gpu_count": "2"}},
		{name: "unknown rejected", params: map[string]string{"unknown": "value"}, wantError: true},
		{name: "invalid gpu rejected", params: map[string]string{"gpu_count": "0"}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := backend.CheckBackendParameterSupport(&tes.Task{Resources: &tes.Resources{
				BackendParameters:       tc.params,
				BackendParametersStrict: true,
			}})
			if (err != nil) != tc.wantError {
				t.Fatalf("CheckBackendParameterSupport() error = %v, wantError %v", err, tc.wantError)
			}
			if tc.wantError && status.Code(err) != codes.InvalidArgument {
				t.Fatalf("CheckBackendParameterSupport() error = %v, want InvalidArgument", err)
			}
		})
	}
}

func resourceValues(requirements []types.ResourceRequirement) map[string]string {
	values := make(map[string]string)
	for _, requirement := range requirements {
		values[string(requirement.Type)] = aws.ToString(requirement.Value)
	}
	return values
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

func TestSubmitSelectsGPUQueueAndDefinition(t *testing.T) {
	submitted := false
	client := &mockClient{SubmitJobFunc: func(_ context.Context, req *batch.SubmitJobInput) (*batch.SubmitJobOutput, error) {
		submitted = true
		if aws.ToString(req.JobQueue) != "a100-queue" || aws.ToString(req.JobDefinition) != "gpu-worker:2" {
			t.Fatalf("unexpected routing: %+v", req)
		}
		if resourceValues(req.ContainerOverrides.ResourceRequirements)["GPU"] != "2" {
			t.Fatal("missing GPU reservation")
		}
		return &batch.SubmitJobOutput{JobId: aws.String("job")}, nil
	}}
	backend := &Backend{client: client, conf: &config.AWSBatch{JobQueue: "default", JobDefinition: "default"}, event: &noopEventWriter{}}
	err := backend.Submit(&tes.Task{Id: "task", Resources: &tes.Resources{BackendParametersStrict: true, BackendParameters: map[string]string{"GPU_COUNT": "2", "JOB_QUEUE": "a100-queue", "job_definition": "gpu-worker:2"}}})
	if err != nil || !submitted {
		t.Fatalf("submitted=%v error=%v", submitted, err)
	}
}
