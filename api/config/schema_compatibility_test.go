package config_test

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	schema "github.com/calypr/funnel/api/config"
)

func TestKubernetesSchemaFieldCompatibility(t *testing.T) {
	kubernetes := (&schema.Kubernetes{}).ProtoReflect().Descriptor()
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

	configDescriptor := (&schema.Config{}).ProtoReflect().Descriptor()
	if configDescriptor.Fields().ByName("MountedInputs") != nil {
		t.Fatal("Config.MountedInputs must be removed")
	}
	if !configDescriptor.ReservedRanges().Has(34) || !configDescriptor.ReservedNames().Has("MountedInputs") {
		t.Fatal("Config.MountedInputs field 34 must be reserved")
	}

	storageDescriptor := (&schema.KubernetesStorage{}).ProtoReflect().Descriptor()
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

	readFields := (&schema.S3MountedRead{}).ProtoReflect().Descriptor().Fields()
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
	writeFields := (&schema.S3MountedWrite{}).ProtoReflect().Descriptor().Fields()
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
		(&schema.S3MountedRead{}).ProtoReflect().Descriptor(),
		(&schema.S3MountedWrite{}).ProtoReflect().Descriptor(),
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
