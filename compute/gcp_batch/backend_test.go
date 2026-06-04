package gcp_batch

import (
	"context"
	"strings"
	"testing"

	batch "cloud.google.com/go/batch/apiv1"
	"cloud.google.com/go/batch/apiv1/batchpb"
	"github.com/googleapis/gax-go/v2"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
)

// Test helper function: extractGCSPath
func TestExtractGCSPath(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		wantBucket string
		wantObject string
	}{
		{
			name:       "basic gs URL",
			url:        "gs://bucket/file.txt",
			wantBucket: "bucket",
			wantObject: "file.txt",
		},
		{
			name:       "nested path",
			url:        "gs://bucket/path/to/file.txt",
			wantBucket: "bucket",
			wantObject: "path/to/file.txt",
		},
		{
			name:       "bucket only no slash",
			url:        "gs://bucket",
			wantBucket: "bucket",
			wantObject: "",
		},
		{
			name:       "bucket only with slash",
			url:        "gs://bucket/",
			wantBucket: "bucket",
			wantObject: "",
		},
		{
			name:       "non-GCS URL s3",
			url:        "s3://bucket/file.txt",
			wantBucket: "",
			wantObject: "",
		},
		{
			name:       "non-GCS URL http",
			url:        "https://example.com/file.txt",
			wantBucket: "",
			wantObject: "",
		},
		{
			name:       "empty URL",
			url:        "",
			wantBucket: "",
			wantObject: "",
		},
		{
			name:       "path with trailing slash",
			url:        "gs://bucket/path/to/dir/",
			wantBucket: "bucket",
			wantObject: "path/to/dir/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBucket, gotObject := extractGCSPath(tt.url)
			if gotBucket != tt.wantBucket {
				t.Errorf("extractGCSPath() bucket = %v, want %v", gotBucket, tt.wantBucket)
			}
			if gotObject != tt.wantObject {
				t.Errorf("extractGCSPath() object = %v, want %v", gotObject, tt.wantObject)
			}
		})
	}
}

// Test helper function: extractBucketName
func TestExtractBucketName(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"basic URL", "gs://bucket/file.txt", "bucket"},
		{"nested path", "gs://bucket/path/to/file.txt", "bucket"},
		{"bucket only", "gs://bucket", "bucket"},
		{"non-GCS URL", "s3://bucket/file.txt", ""},
		{"empty URL", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractBucketName(tt.url)
			if got != tt.want {
				t.Errorf("extractBucketName() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test path validation
func TestValidatePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"valid absolute path", "/input/file.txt", false},
		{"valid nested path", "/data/subdir/file.txt", false},
		{"empty path", "", false}, // Handled elsewhere
		{"relative path", "relative/path.txt", true},
		{"path with semicolon", "/tmp/file;rm -rf", true},
		{"path with pipe", "/tmp/file|cat", true},
		{"path with ampersand", "/tmp/file&&echo", true},
		{"path with dollar", "/tmp/file$var", true},
		{"path with backtick", "/tmp/file`cmd`", true},
		{"path with newline", "/tmp/file\ntest", true},
		{"path with redirect", "/tmp/file>out", true},
		{"path with paren", "/tmp/file(test)", true},
		{"path with spaces", "/input/file with spaces.txt", false}, // Spaces are OK
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test path collision detection
func TestDetectPathCollisions(t *testing.T) {
	tests := []struct {
		name    string
		inputs  []*tes.Input
		outputs []*tes.Output
		wantErr bool
	}{
		{
			name: "no collision - different paths",
			inputs: []*tes.Input{
				{Url: "gs://bucket/in1.txt", Path: "/input/file1.txt"},
				{Url: "gs://bucket/in2.txt", Path: "/input/file2.txt"},
			},
			wantErr: false,
		},
		{
			name: "collision - same input path different URLs",
			inputs: []*tes.Input{
				{Url: "gs://bucket/in1.txt", Path: "/input/file.txt"},
				{Url: "gs://bucket/in2.txt", Path: "/input/file.txt"},
			},
			wantErr: true,
		},
		{
			name: "no collision - same path same URL",
			inputs: []*tes.Input{
				{Url: "gs://bucket/in1.txt", Path: "/input/file.txt"},
				{Url: "gs://bucket/in1.txt", Path: "/input/file.txt"},
			},
			wantErr: false,
		},
		{
			name: "collision - input and output same path",
			inputs: []*tes.Input{
				{Url: "gs://bucket/in.txt", Path: "/data/file.txt"},
			},
			outputs: []*tes.Output{
				{Url: "gs://bucket/out.txt", Path: "/data/file.txt"},
			},
			wantErr: true,
		},
		{
			name: "no collision - empty paths skipped",
			inputs: []*tes.Input{
				{Url: "gs://bucket/in.txt", Path: ""},
				{Url: "gs://bucket/in2.txt", Path: "/input/file.txt"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := detectPathCollisions(tt.inputs, tt.outputs)
			if (err != nil) != tt.wantErr {
				t.Errorf("detectPathCollisions() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test Submit with multiple inputs and outputs
func TestSubmit_MultipleInputsOutputs(t *testing.T) {
	log := logger.NewLogger("test", logger.DefaultConfig())
	conf := &config.GCPBatch{
		Project:  "test-project",
		Location: "us-west1",
	}

	var capturedReq *batchpb.CreateJobRequest
	mockClient := &mockClient{
		CreateJobFunc: func(req *batchpb.CreateJobRequest) (*batchpb.Job, error) {
			capturedReq = req
			return &batchpb.Job{Name: "test-job", Uid: "test-uid"}, nil
		},
	}

	backend := &Backend{
		client: mockClient,
		conf:   conf,
		log:    log,
		event:  &noopEventWriter{},
	}

	task := &tes.Task{
		Id: "task1",
		Inputs: []*tes.Input{
			{Url: "gs://bucket1/input1.txt", Path: "/input/file1.txt"},
			{Url: "gs://bucket1/input2.txt", Path: "/input/file2.txt"},
			{Url: "gs://bucket2/input3.txt", Path: "/input/file3.txt"},
		},
		Outputs: []*tes.Output{
			{Url: "gs://bucket1/output1.txt", Path: "/output/result1.txt"},
			{Url: "gs://bucket3/output2.txt", Path: "/output/result2.txt"},
		},
		Executors: []*tes.Executor{
			{
				Image:   "alpine",
				Command: []string{"echo", "test"},
			},
		},
	}

	err := backend.Submit(task)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	// Verify volumes - should have 3 unique buckets
	volumes := capturedReq.Job.TaskGroups[0].TaskSpec.Volumes
	if len(volumes) != 3 {
		t.Errorf("Expected 3 volumes, got %d", len(volumes))
	}

	// Paths are rewritten directly in executor commands — no separate setup runnable.
	runnables := capturedReq.Job.TaskGroups[0].TaskSpec.Runnables
	if len(runnables) != 1 {
		t.Fatalf("Expected 1 runnable (executor only), got %d", len(runnables))
	}
	// The executor command ["echo", "test"] doesn't reference any I/O paths, so
	// verify correctness via the volumes instead — all 3 buckets must be mounted.
	if len(volumes) != 3 {
		t.Errorf("Expected 3 volumes for 3 unique buckets, got %d", len(volumes))
	}
}

// Test Submit with multiple executors
func TestSubmit_MultipleExecutors(t *testing.T) {
	log := logger.NewLogger("test", logger.DefaultConfig())
	conf := &config.GCPBatch{
		Project:  "test-project",
		Location: "us-west1",
	}

	var capturedReq *batchpb.CreateJobRequest
	mockClient := &mockClient{
		CreateJobFunc: func(req *batchpb.CreateJobRequest) (*batchpb.Job, error) {
			capturedReq = req
			return &batchpb.Job{Name: "test-job", Uid: "test-uid"}, nil
		},
	}

	backend := &Backend{
		client: mockClient,
		conf:   conf,
		log:    log,
		event:  &noopEventWriter{},
	}

	task := &tes.Task{
		Id: "task1",
		Inputs: []*tes.Input{
			{Url: "gs://bucket/input.txt", Path: "/data/input.txt"},
		},
		Outputs: []*tes.Output{
			{Url: "gs://bucket/output.txt", Path: "/data/output.txt"},
		},
		Executors: []*tes.Executor{
			{
				Image:   "alpine",
				Command: []string{"cat", "/data/input.txt"},
			},
			{
				Image:   "alpine",
				Command: []string{"wc", "/data/input.txt"},
			},
		},
	}

	err := backend.Submit(task)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	// Should create 1 runnable per executor; paths are rewritten inline, no setup runnable.
	runnables := capturedReq.Job.TaskGroups[0].TaskSpec.Runnables
	if len(runnables) != 2 {
		t.Fatalf("Expected 2 runnables (1 per executor), got %d", len(runnables))
	}
	// Both executors reference /data/input.txt which maps to gs://bucket/input.txt.
	for i, r := range runnables {
		cmds := r.GetContainer().Commands
		found := false
		for _, c := range cmds {
			if strings.Contains(c, "/mnt/disks/bucket/input.txt") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Runnable %d: expected rewritten path /mnt/disks/bucket/input.txt in commands %v", i, cmds)
		}
	}
}

// Test Submit with empty/missing fields
func TestSubmit_EmptyFields(t *testing.T) {
	log := logger.NewLogger("test", logger.DefaultConfig())
	conf := &config.GCPBatch{
		Project:  "test-project",
		Location: "us-west1",
	}

	var capturedReq *batchpb.CreateJobRequest
	mockClient := &mockClient{
		CreateJobFunc: func(req *batchpb.CreateJobRequest) (*batchpb.Job, error) {
			capturedReq = req
			return &batchpb.Job{Name: "test-job", Uid: "test-uid"}, nil
		},
	}

	backend := &Backend{
		client: mockClient,
		conf:   conf,
		log:    log,
		event:  &noopEventWriter{},
	}

	task := &tes.Task{
		Id: "task1",
		Inputs: []*tes.Input{
			{Url: "", Path: "/input/file.txt"},                       // Empty URL - should skip
			{Url: "gs://bucket/file.txt", Path: ""},                  // Empty path - should skip
			{Url: "gs://bucket/valid.txt", Path: "/input/valid.txt"}, // Valid
			{Url: "s3://bucket/file.txt", Path: "/input/s3file.txt"}, // Non-GCS - should skip
		},
		Executors: []*tes.Executor{
			{Image: "alpine", Command: []string{"echo", "test"}},
		},
	}

	err := backend.Submit(task)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	// Only 1 bucket should be mounted (the valid GCS one)
	volumes := capturedReq.Job.TaskGroups[0].TaskSpec.Volumes
	if len(volumes) != 1 {
		t.Errorf("Expected 1 volume, got %d", len(volumes))
	}
}

// Test Submit with no inputs/outputs
func TestSubmit_NoInputsOutputs(t *testing.T) {
	log := logger.NewLogger("test", logger.DefaultConfig())
	conf := &config.GCPBatch{
		Project:  "test-project",
		Location: "us-west1",
	}

	var capturedReq *batchpb.CreateJobRequest
	mockClient := &mockClient{
		CreateJobFunc: func(req *batchpb.CreateJobRequest) (*batchpb.Job, error) {
			capturedReq = req
			return &batchpb.Job{Name: "test-job", Uid: "test-uid"}, nil
		},
	}

	backend := &Backend{
		client: mockClient,
		conf:   conf,
		log:    log,
		event:  &noopEventWriter{},
	}

	task := &tes.Task{
		Id: "task1",
		Executors: []*tes.Executor{
			{Image: "alpine", Command: []string{"echo", "hello"}},
		},
	}

	err := backend.Submit(task)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	// No volumes should be created
	volumes := capturedReq.Job.TaskGroups[0].TaskSpec.Volumes
	if len(volumes) != 0 {
		t.Errorf("Expected 0 volumes, got %d", len(volumes))
	}

	// Command should be passed directly (no sh -c wrapping when no redirection needed)
	cmds := capturedReq.Job.TaskGroups[0].TaskSpec.Runnables[0].GetContainer().Commands
	if len(cmds) != 2 || cmds[0] != "echo" || cmds[1] != "hello" {
		t.Errorf("Expected direct command [echo hello], got %v", cmds)
	}
}

// Test Submit with executor Workdir sets --workdir docker option
func TestSubmit_ExecutorWorkdir(t *testing.T) {
	log := logger.NewLogger("test", logger.DefaultConfig())
	conf := &config.GCPBatch{
		Project:  "test-project",
		Location: "us-west1",
	}

	var capturedReq *batchpb.CreateJobRequest
	mockClient := &mockClient{
		CreateJobFunc: func(req *batchpb.CreateJobRequest) (*batchpb.Job, error) {
			capturedReq = req
			return &batchpb.Job{Name: "test-job", Uid: "test-uid"}, nil
		},
	}

	backend := &Backend{
		client: mockClient,
		conf:   conf,
		log:    log,
		event:  &noopEventWriter{},
	}

	task := &tes.Task{
		Id: "task1",
		Executors: []*tes.Executor{
			{Image: "alpine", Command: []string{"echo", "test"}, Workdir: "/work"},
			{Image: "alpine", Command: []string{"echo", "no-workdir"}},
		},
	}

	err := backend.Submit(task)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	runnables := capturedReq.Job.TaskGroups[0].TaskSpec.Runnables
	// No inputs/outputs so no setup runnable; just the 2 executor runnables.
	if len(runnables) != 2 {
		t.Fatalf("Expected 2 runnables, got %d", len(runnables))
	}

	opts0 := runnables[0].GetContainer().Options
	if !strings.Contains(opts0, "--workdir") || !strings.Contains(opts0, "/work") {
		t.Errorf("Expected --workdir /work in Options, got %q", opts0)
	}

	opts1 := runnables[1].GetContainer().Options
	if opts1 != "" {
		t.Errorf("Expected empty Options for executor without Workdir, got %q", opts1)
	}
}

// Mock client for testing
type mockClient struct {
	CreateJobFunc func(req *batchpb.CreateJobRequest) (*batchpb.Job, error)
}

func (m *mockClient) CreateJob(ctx context.Context, req *batchpb.CreateJobRequest, opts ...gax.CallOption) (*batchpb.Job, error) {
	if m.CreateJobFunc != nil {
		return m.CreateJobFunc(req)
	}
	return &batchpb.Job{Name: "test-job", Uid: "test-uid"}, nil
}

func (m *mockClient) GetJob(ctx context.Context, req *batchpb.GetJobRequest, opts ...gax.CallOption) (*batchpb.Job, error) {
	return &batchpb.Job{}, nil
}

func (m *mockClient) ListJobs(ctx context.Context, req *batchpb.ListJobsRequest, opts ...gax.CallOption) *batch.JobIterator {
	return nil
}

// Mock event writer for testing
type noopEventWriter struct{}

func (n *noopEventWriter) WriteEvent(ctx context.Context, ev *events.Event) error {
	return nil
}

func (n *noopEventWriter) Close() {}

// Test command construction with proper shell quoting
func TestSubmit_CommandConstruction(t *testing.T) {
	log := logger.NewLogger("test", logger.DefaultConfig())

	var capturedReq *batchpb.CreateJobRequest
	mockClient := &mockClient{
		CreateJobFunc: func(req *batchpb.CreateJobRequest) (*batchpb.Job, error) {
			capturedReq = req
			return &batchpb.Job{Name: "test-job", Uid: "test-uid"}, nil
		},
	}

	backend := &Backend{
		client: mockClient,
		conf:   &config.GCPBatch{Project: "test-project", Location: "us-west1"},
		log:    log,
		event:  &noopEventWriter{},
	}

	// Test with command that has spaces and quotes
	task := &tes.Task{
		Id: "task1",
		Executors: []*tes.Executor{
			{
				Image: "python:3.9",
				Command: []string{
					"python",
					"-c",
					"import sys; print('Hello World'); print(sys.argv)",
				},
			},
		},
	}

	err := backend.Submit(task)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Commands are passed directly without shell wrapping, so the original args are preserved.
	cmds := capturedReq.Job.TaskGroups[0].TaskSpec.Runnables[0].GetContainer().Commands
	if len(cmds) != 3 || cmds[0] != "python" || cmds[1] != "-c" {
		t.Errorf("Expected direct command [python -c <script>], got %v", cmds)
	}
	if !strings.Contains(cmds[2], "Hello World") {
		t.Errorf("Expected script body in Commands[2], got: %s", cmds[2])
	}
}
