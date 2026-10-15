package storageplan

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ohsu-comp-bio/funnel/storage"
	corev1 "k8s.io/api/core/v1"
)

func TestDecodeOwnershipForLegacyOverlappingWriteRoutes(t *testing.T) {
	plan := &Plan{
		Version:         planVersion,
		TaskID:          "legacy-overlap",
		Namespace:       "tasks",
		WorkDir:         "/work",
		WorkspaceVolume: "workspace",
		WorkerContainer: "main",
		Volumes: []corev1.Volume{
			{Name: "workspace", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "legacy-overlap-workspace"}}},
			{Name: "parent", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "legacy-overlap-workspace"}}},
			{Name: "child", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "legacy-overlap-workspace"}}},
		},
		WorkerMounts: []corev1.VolumeMount{
			{Name: "parent", MountPath: "/outputs/parent", SubPath: "."},
			{Name: "child", MountPath: "/outputs/child", SubPath: "nested"},
		},
		OwnedClaims:  []string{"legacy-overlap-workspace"},
		OwnedVolumes: []string{"data-legacy-overlap"},
		S3WriteMounts: []storage.S3WriteMount{
			{Endpoint: "https://s3.example.test", Bucket: "one", Root: "/outputs/parent", Timeout: time.Second},
			{Endpoint: "https://s3.example.test", Bucket: "two", Root: "/outputs/child", Timeout: time.Second},
		},
	}
	snapshotBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := string(snapshotBytes)
	if _, err := Decode(snapshot); err == nil || !strings.Contains(err.Error(), "overlapping or unknown subPaths") {
		t.Fatalf("runnable decoder accepted legacy overlapping routes: %v", err)
	}
	got, err := DecodeOwnership(snapshot)
	if err != nil {
		t.Fatalf("ownership decoder rejected legacy plan: %v", err)
	}
	want := &Ownership{
		TaskID:          plan.TaskID,
		Namespace:       plan.Namespace,
		WorkerContainer: plan.WorkerContainer,
		OwnedVolumes:    []string{"data-legacy-overlap"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ownership = %+v, want %+v", got, want)
	}
}

func TestDecodeOwnershipKeepsSnapshotAndOwnershipValidation(t *testing.T) {
	plan := &Plan{
		Version:         planVersion,
		TaskID:          "task-123",
		Namespace:       "tasks",
		WorkDir:         "/work",
		WorkspaceVolume: "workspace",
		WorkerContainer: "worker",
		Volumes: []corev1.Volume{{
			Name:         "workspace",
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "task-123-workspace"}},
		}},
		OwnedClaims: []string{"task-123-workspace"},
	}
	snapshotBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := string(snapshotBytes)
	unknownField := strings.TrimSuffix(snapshot, "}") + `,"Unexpected":true}`
	wrongVersion := strings.Replace(snapshot, `"Version":4`, `"Version":3`, 1)
	invalidOwnedName := strings.Replace(snapshot, `"OwnedClaims":["task-123-workspace"]`, `"OwnedClaims":["unrelated"]`, 1)
	invalidTaskID := strings.Replace(snapshot, `"TaskID":"task-123"`, `"TaskID":"../bad"`, 1)
	invalidMount := strings.Replace(snapshot, `"WorkerMounts":null`, `"WorkerMounts":[{"Name":"workspace","MountPath":"/bad/../mount"}]`, 1)
	for name, value := range map[string]string{
		"unknown field":         unknownField,
		"unsupported version":   wrongVersion,
		"invalid owned name":    invalidOwnedName,
		"invalid task identity": invalidTaskID,
		"invalid worker mount":  invalidMount,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeOwnership(value); err == nil {
				t.Fatalf("ownership decoder accepted %s", name)
			}
		})
	}
}
