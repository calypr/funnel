package config

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"

	schema "github.com/calypr/funnel/api/config"
)

func TestKubernetesStorageConfigRoundTrip(t *testing.T) {
	input := []byte(`
Kubernetes:
  Storage:
    WorkspaceVolume: workspace
    WorkerContainer: funnel-worker
    ExecutorContainer: funnel-executor
    volumes:
      - name: workspace
        persistentVolumeClaim:
          claimName: shared-workspace
    volumeMounts:
      - name: task-data
        mountPath: /data
        subPath: inputs
    volumeClaimTemplates:
      - apiVersion: v1
        kind: PersistentVolumeClaim
        metadata:
          name: task-data
        spec:
          volumeName: pv-{{.TaskId}}
    persistentVolumeTemplates:
      - apiVersion: v1
        kind: PersistentVolume
        metadata:
          name: pv-{{.TaskId}}
GenericS3:
  - Endpoint: https://s3.example.test
    Key: access
    Secret: secret
    Bucket: bucket
    MountedRead:
      Volume: task-data
      SubPath: ""
      Prefix: prefix/
    MountedWrite:
      Volume: task-data
      SubPath: results
      Prefix: results/
      Timeout: 90s
`)

	got := EmptyConfig()
	if err := Parse(input, got); err != nil {
		t.Fatal("parse storage config:", err)
	}

	volume := mustStruct(t, map[string]any{
		"name":                  "workspace",
		"persistentVolumeClaim": map[string]any{"claimName": "shared-workspace"},
	})
	volumeMount := mustStruct(t, map[string]any{
		"name":      "task-data",
		"mountPath": "/data",
		"subPath":   "inputs",
	})
	claimTemplate := mustStruct(t, map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata":   map[string]any{"name": "task-data"},
		"spec":       map[string]any{"volumeName": "pv-{{.TaskId}}"},
	})
	pvTemplate := mustStruct(t, map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolume",
		"metadata":   map[string]any{"name": "pv-{{.TaskId}}"},
	})
	want := &schema.Config{
		GenericS3: []*schema.GenericS3Storage{{
			Endpoint: "https://s3.example.test",
			Key:      "access",
			Secret:   "secret",
			Bucket:   "bucket",
			MountedWrite: &schema.S3MountedWrite{
				Volume:    "task-data",
				MountView: &schema.S3MountedWrite_SubPath{SubPath: "results"},
				Prefix:    "results/",
				Timeout:   durationpb.New(90 * time.Second),
			},
			MountedRead: &schema.S3MountedRead{
				Volume:    "task-data",
				MountView: &schema.S3MountedRead_SubPath{SubPath: ""},
				Prefix:    "prefix/",
			},
		}},
		Kubernetes: &schema.Kubernetes{
			Storage: &schema.KubernetesStorage{
				WorkspaceVolume:           "workspace",
				WorkerContainer:           "funnel-worker",
				ExecutorContainer:         "funnel-executor",
				Volumes:                   []*structpb.Struct{volume},
				VolumeMounts:              []*structpb.Struct{volumeMount},
				VolumeClaimTemplates:      []*structpb.Struct{claimTemplate},
				PersistentVolumeTemplates: []*structpb.Struct{pvTemplate},
			},
		},
	}
	if !proto.Equal(got, want) {
		t.Fatalf("parsed storage config mismatch:\n got: %v\nwant: %v", got, want)
	}

	encoded, err := ToYaml(got)
	if err != nil {
		t.Fatal("marshal storage config:", err)
	}
	roundTripped := EmptyConfig()
	if err := Parse(encoded, roundTripped); err != nil {
		t.Fatal("parse marshaled storage config:", err)
	}
	if !proto.Equal(roundTripped, want) {
		t.Fatalf("round-tripped storage config mismatch:\n got: %v\nwant: %v", roundTripped, want)
	}
}

func TestKubernetesStorageAbsentKeepsLegacyConfigShape(t *testing.T) {
	conf := EmptyConfig()
	if err := Parse([]byte("Kubernetes: {}\n"), conf); err != nil {
		t.Fatal("parse legacy Kubernetes config:", err)
	}
	if conf.Kubernetes.Storage != nil {
		t.Fatalf("expected absent Storage to remain unset, got %v", conf.Kubernetes.Storage)
	}
}

func TestRemovedKubernetesStorageKeysAreRejected(t *testing.T) {
	for _, name := range []string{"PVCMode", "StorageClassName", "SharedPVCName", "S3FilesFilesystemId"} {
		t.Run(name, func(t *testing.T) {
			conf := EmptyConfig()
			raw := []byte(fmt.Sprintf("Kubernetes:\n  %s: true\n", name))
			err := Parse(raw, conf)
			if err == nil {
				t.Fatalf("expected removed field %q to be rejected", name)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf(`unknown field %q`, name)) {
				t.Fatalf("expected unknown field error for %q, got %v", name, err)
			}
		})
	}
}

func TestOldKubernetesStorageShapesAreRejected(t *testing.T) {
	for name, raw := range map[string]string{
		"bindings": `Kubernetes:
  Storage:
    Bindings:
      - Name: data
        ExistingClaim: shared-data
`,
		"S3 worker mount": `GenericS3:
  - MountedRead:
      WorkerMount: data
      Prefix: input/
`,
		"S3 write worker mount": `GenericS3:
  - MountedWrite:
      WorkerMount: data
      Prefix: output/
`,
		"string volume object": `Kubernetes:
  Storage:
    volumes:
      - "name: data"
`,
		"string volume mount object": `Kubernetes:
  Storage:
    volumeMounts:
      - "mountPath: /data"
`,
		"string claim template object": `Kubernetes:
  Storage:
    volumeClaimTemplates:
      - "kind: PersistentVolumeClaim"
`,
		"string persistent volume template object": `Kubernetes:
  Storage:
    persistentVolumeTemplates:
      - "kind: PersistentVolume"
`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := Parse([]byte(raw), EmptyConfig()); err == nil {
				t.Fatal("old Kubernetes storage shape was accepted")
			}
		})
	}
}

func mustStruct(t *testing.T, value map[string]any) *structpb.Struct {
	t.Helper()
	message, err := structpb.NewStruct(value)
	if err != nil {
		t.Fatal("create protobuf Struct:", err)
	}
	return message
}
