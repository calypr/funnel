package events

import (
	"testing"

	"github.com/calypr/funnel/tes"
)

func TestExecutionOwnerMetadataRoundTrip(t *testing.T) {
	want := &ExecutionOwner{Namespace: "jobs", PodName: "worker-abc", PodUid: "pod-uid"}
	metadata, err := ExecutionOwnerMetadata(want)
	if err != nil {
		t.Fatalf("ExecutionOwnerMetadata() error = %v", err)
	}
	got, err := TaskExecutionOwner(&tes.Task{Logs: []*tes.TaskLog{{Metadata: metadata}}})
	if err != nil {
		t.Fatalf("TaskExecutionOwner() error = %v", err)
	}
	if got == nil || got.GetNamespace() != want.GetNamespace() || got.GetPodName() != want.GetPodName() || got.GetPodUid() != want.GetPodUid() {
		t.Fatalf("TaskExecutionOwner() = %v, want %v", got, want)
	}
}

func TestExecutionOwnerMetadataValidation(t *testing.T) {
	if metadata, err := ExecutionOwnerMetadata(nil); err != nil || metadata != nil {
		t.Fatalf("ExecutionOwnerMetadata(nil) = %v, %v, want nil, nil", metadata, err)
	}
	if _, err := ExecutionOwnerMetadata(&ExecutionOwner{Namespace: "jobs", PodName: "worker-abc"}); err == nil {
		t.Fatal("ExecutionOwnerMetadata() accepted an owner without a pod UID")
	}
}

func TestTaskExecutionOwnerIgnoresMissingAndRejectsMalformedMetadata(t *testing.T) {
	for _, task := range []*tes.Task{nil, {}, {Logs: []*tes.TaskLog{nil}}} {
		if owner, err := TaskExecutionOwner(task); err != nil || owner != nil {
			t.Fatalf("TaskExecutionOwner(%v) = %v, %v, want nil, nil", task, owner, err)
		}
	}

	for _, value := range []string{
		"{",
		`{"namespace":"jobs","pod_name":"worker-abc"}`,
		`{"namespace":"jobs","pod_name":"worker-abc","pod_uid":"pod-uid","unknown":true}`,
		`{"namespace":"jobs","pod_name":"worker-abc","pod_uid":"pod-uid"} {}`,
	} {
		task := &tes.Task{Logs: []*tes.TaskLog{{Metadata: map[string]string{ExecutionOwnerMetadataKey: value}}}}
		if owner, err := TaskExecutionOwner(task); err == nil || owner != nil {
			t.Fatalf("TaskExecutionOwner() = %v, %v for malformed %q, want an error", owner, err, value)
		}
	}
}
