package config

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
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
	want := &Config{
		GenericS3: []*GenericS3Storage{{
			Endpoint: "https://s3.example.test",
			Key:      "access",
			Secret:   "secret",
			Bucket:   "bucket",
			MountedWrite: &S3MountedWrite{
				Volume:    "task-data",
				MountView: &S3MountedWrite_SubPath{SubPath: "results"},
				Prefix:    "results/",
				Timeout:   durationpb.New(90 * time.Second),
			},
			MountedRead: &S3MountedRead{
				Volume:    "task-data",
				MountView: &S3MountedRead_SubPath{SubPath: ""},
				Prefix:    "prefix/",
			},
		}},
		Kubernetes: &Kubernetes{
			Storage: &KubernetesStorage{
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

func TestKubernetesStorageGobRoundTrip(t *testing.T) {
	volume := mustStruct(t, map[string]any{
		"name":    "task-data",
		"null":    nil,
		"count":   float64(3),
		"enabled": true,
		"nested":  map[string]any{"message": "nested string"},
		"values":  []any{"string", float64(4), true, nil, []any{"nested list"}},
	})
	want := &Config{
		GenericS3: []*GenericS3Storage{{
			MountedRead: &S3MountedRead{Volume: "task-data"},
			MountedWrite: &S3MountedWrite{
				Volume:    "task-data",
				MountView: &S3MountedWrite_SubPath{SubPath: ""},
			},
		}},
		Kubernetes: &Kubernetes{
			Storage: &KubernetesStorage{
				Volumes: []*structpb.Struct{volume},
			},
		},
	}

	var encoded bytes.Buffer
	if err := gob.NewEncoder(&encoded).Encode(want); err != nil {
		t.Fatal("encode config over gob:", err)
	}

	var got Config
	if err := gob.NewDecoder(&encoded).Decode(&got); err != nil {
		t.Fatal("decode config over gob:", err)
	}
	if !proto.Equal(&got, want) {
		t.Fatalf("gob round-trip mismatch:\n got: %v\nwant: %v", &got, want)
	}
	if got.GenericS3[0].MountedRead.MountView != nil {
		t.Fatal("absent read SubPath became present during gob round-trip")
	}
	if got.GenericS3[0].MountedWrite.GetSubPath() != "" {
		t.Fatal("present empty write SubPath did not survive gob round-trip")
	}
	if _, present := got.GenericS3[0].MountedWrite.GetMountView().(*S3MountedWrite_SubPath); !present {
		t.Fatal("empty write SubPath lost oneof presence during gob round-trip")
	}
}

func TestKubernetesSchemaFieldCompatibility(t *testing.T) {
	kubernetes := (&Kubernetes{}).ProtoReflect().Descriptor()
	for name, number := range map[protoreflect.Name]protoreflect.FieldNumber{
		"ForbiddenPathPrefixes": 20,
		"WorkerImage":           24,
		"Storage":               25,
	} {
		field := kubernetes.Fields().ByName(name)
		if field == nil {
			t.Fatalf("Kubernetes field %q is missing", name)
		}
		if field.Number() != number {
			t.Errorf("Kubernetes field %q number = %d, want %d", name, field.Number(), number)
		}
	}
	for _, number := range []protoreflect.FieldNumber{21, 22, 23} {
		if !kubernetes.ReservedRanges().Has(number) {
			t.Errorf("Kubernetes field number %d is not reserved", number)
		}
	}
	for _, name := range []protoreflect.Name{"PVCMode", "StorageClassName", "SharedPVCName", "S3FilesFilesystemId"} {
		if !kubernetes.ReservedNames().Has(name) {
			t.Errorf("Kubernetes field name %q is not reserved", name)
		}
	}

	configDescriptor := (&Config{}).ProtoReflect().Descriptor()
	if configDescriptor.Fields().ByName("MountedInputs") != nil {
		t.Fatal("Config.MountedInputs must be removed")
	}
	if !configDescriptor.ReservedRanges().Has(34) || !configDescriptor.ReservedNames().Has("MountedInputs") {
		t.Fatal("Config.MountedInputs field 34 must be reserved")
	}

	storageDescriptor := (&KubernetesStorage{}).ProtoReflect().Descriptor()
	for name, number := range map[protoreflect.Name]protoreflect.FieldNumber{
		"WorkspaceVolume":           1,
		"WorkerContainer":           3,
		"ExecutorContainer":         4,
		"volumes":                   6,
		"volumeMounts":              7,
		"volumeClaimTemplates":      8,
		"persistentVolumeTemplates": 9,
	} {
		field := storageDescriptor.Fields().ByName(name)
		if field == nil || field.Number() != number {
			t.Fatalf("KubernetesStorage.%s field = %v, want %d", name, field, number)
		}
		if name == "volumes" || name == "volumeMounts" || name == "volumeClaimTemplates" || name == "persistentVolumeTemplates" {
			if !field.IsList() || field.Kind() != protoreflect.MessageKind || field.Message().FullName() != "google.protobuf.Struct" {
				t.Errorf("KubernetesStorage.%s field = %v, want repeated google.protobuf.Struct", name, field)
			}
		}
	}
	for _, number := range []protoreflect.FieldNumber{2, 5} {
		if !storageDescriptor.ReservedRanges().Has(number) {
			t.Errorf("KubernetesStorage field number %d is not reserved", number)
		}
	}
	for _, name := range []protoreflect.Name{"Bindings", "MountedInputs"} {
		if !storageDescriptor.ReservedNames().Has(name) {
			t.Errorf("KubernetesStorage field name %q is not reserved", name)
		}
	}

	readFields := (&S3MountedRead{}).ProtoReflect().Descriptor().Fields()
	for name, number := range map[protoreflect.Name]protoreflect.FieldNumber{
		"Prefix":  2,
		"Volume":  3,
		"SubPath": 4,
	} {
		field := readFields.ByName(name)
		if field == nil || field.Number() != number {
			t.Fatalf("S3MountedRead.%s field = %v, want %d", name, field, number)
		}
	}
	writeFields := (&S3MountedWrite{}).ProtoReflect().Descriptor().Fields()
	for name, number := range map[protoreflect.Name]protoreflect.FieldNumber{
		"Prefix":  2,
		"Timeout": 3,
		"Volume":  4,
		"SubPath": 5,
	} {
		field := writeFields.ByName(name)
		if field == nil || field.Number() != number {
			t.Fatalf("S3MountedWrite.%s field = %v, want %d", name, field, number)
		}
	}
	for _, message := range []protoreflect.MessageDescriptor{
		(&S3MountedRead{}).ProtoReflect().Descriptor(),
		(&S3MountedWrite{}).ProtoReflect().Descriptor(),
	} {
		if !message.ReservedRanges().Has(1) || !message.ReservedNames().Has("WorkerMount") {
			t.Errorf("%s must reserve WorkerMount field 1", message.Name())
		}
		subPath := message.Fields().ByName("SubPath")
		if subPath == nil || subPath.ContainingOneof() == nil || subPath.ContainingOneof().Name() != "MountView" {
			t.Errorf("%s.SubPath must retain presence in MountView oneof", message.Name())
		}
	}

	fileMessages := configDescriptor.ParentFile().Messages()
	for _, name := range []protoreflect.Name{"KubernetesStorageBinding", "KubernetesWorkerMount", "KubernetesStorageTemplates", "KubernetesMountedInput"} {
		if fileMessages.ByName(name) != nil {
			t.Errorf("removed message %s is still in the schema", name)
		}
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
