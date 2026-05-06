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

	// Verify symlink commands are present in the generated command
	runnables := capturedReq.Job.TaskGroups[0].TaskSpec.Runnables
	if len(runnables) != 1 {
		t.Fatalf("Expected 1 runnable, got %d", len(runnables))
	}

	cmd := runnables[0].GetContainer().Commands[2] // [sh, -c, <full_command>]

	// Check for input symlinks
	if !strings.Contains(cmd, "ln -sf /mnt/disks/bucket1/input1.txt /input/file1.txt") {
		t.Error("Missing input1 symlink command")
	}
	if !strings.Contains(cmd, "ln -sf /mnt/disks/bucket2/input3.txt /input/file3.txt") {
		t.Error("Missing input3 symlink command")
	}

	// Check for output symlinks
	if !strings.Contains(cmd, "ln -sf /mnt/disks/bucket1/output1.txt /output/result1.txt") {
		t.Error("Missing output1 symlink command")
	}
	if !strings.Contains(cmd, "ln -sf /mnt/disks/bucket3/output2.txt /output/result2.txt") {
		t.Error("Missing output2 symlink command")
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

	// Should create 2 runnables, one per executor
	runnables := capturedReq.Job.TaskGroups[0].TaskSpec.Runnables
	if len(runnables) != 2 {
		t.Fatalf("Expected 2 runnables, got %d", len(runnables))
	}

	// Both runnables should have symlink commands
	for i, runnable := range runnables {
		cmd := runnable.GetContainer().Commands[2]
		if !strings.Contains(cmd, "ln -sf") {
			t.Errorf("Runnable %d missing symlink commands", i)
		}
		if !strings.Contains(cmd, "/data/input.txt") {
			t.Errorf("Runnable %d missing input path", i)
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

	// Only valid symlink should be present
	cmd := capturedReq.Job.TaskGroups[0].TaskSpec.Runnables[0].GetContainer().Commands[2]
	if !strings.Contains(cmd, "ln -sf /mnt/disks/bucket/valid.txt /input/valid.txt") {
		t.Error("Missing valid symlink command")
	}

	// Should not contain invalid entries
	if strings.Contains(cmd, "s3file.txt") {
		t.Error("Should not create symlink for S3 URL")
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

	// Command should still work, just no symlinks
	cmd := capturedReq.Job.TaskGroups[0].TaskSpec.Runnables[0].GetContainer().Commands[2]
	if !strings.Contains(cmd, "echo hello") {
		t.Error("Executor command not present")
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

	// Verify the command was properly quoted
	cmd := capturedReq.Job.TaskGroups[0].TaskSpec.Runnables[0].GetContainer().Commands[2]

	// Should contain properly escaped quotes, not broken by spaces
	if !strings.Contains(cmd, "python -c") {
		t.Errorf("Command should contain 'python -c', got: %s", cmd)
	}

	// The entire python script should be treated as one argument to -c
	if strings.Contains(cmd, "python -c import sys;") {
		t.Errorf("Command incorrectly split - 'import sys;' should be quoted as single arg to -c")
	}
}
