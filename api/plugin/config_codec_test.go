package plugin

import (
	"bytes"
	"encoding/gob"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"

	schema "github.com/calypr/funnel/api/config"
)

func TestTimeoutConfigGobRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout *schema.TimeoutConfig
	}{
		{name: "duration", timeout: &schema.TimeoutConfig{TimeoutOption: &schema.TimeoutConfig_Duration{Duration: durationpb.New(time.Minute)}}},
		{name: "disabled", timeout: &schema.TimeoutConfig{TimeoutOption: &schema.TimeoutConfig_Disabled{Disabled: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := &Job{Config: &schema.Config{Scheduler: &schema.Scheduler{NodePingTimeout: tc.timeout}}}
			var wire bytes.Buffer
			if err := gob.NewEncoder(&wire).Encode(want); err != nil {
				t.Fatalf("encode plugin configuration: %v", err)
			}
			var got Job
			if err := gob.NewDecoder(&wire).Decode(&got); err != nil {
				t.Fatalf("decode plugin configuration: %v", err)
			}
			if !proto.Equal(want, &got) {
				t.Fatalf("plugin timeout changed: got %v, want %v", &got, want)
			}
		})
	}
}

func TestKubernetesStorageGobRoundTrip(t *testing.T) {
	volume, err := structpb.NewStruct(map[string]any{
		"name":    "task-data",
		"null":    nil,
		"count":   float64(3),
		"enabled": true,
		"nested":  map[string]any{"message": "nested string"},
		"values":  []any{"string", float64(4), true, nil, []any{"nested list"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := &Job{Config: &schema.Config{
		GenericS3: []*schema.GenericS3Storage{{
			MountedRead: &schema.S3MountedRead{Volume: "task-data"},
			MountedWrite: &schema.S3MountedWrite{
				Volume:    "task-data",
				MountView: &schema.S3MountedWrite_SubPath{SubPath: ""},
			},
		}},
		Kubernetes: &schema.Kubernetes{
			Storage: &schema.KubernetesStorage{
				Volumes: []*structpb.Struct{volume},
			},
		},
	}}

	var encoded bytes.Buffer
	if err := gob.NewEncoder(&encoded).Encode(want); err != nil {
		t.Fatal("encode config over gob:", err)
	}

	var got Job
	if err := gob.NewDecoder(&encoded).Decode(&got); err != nil {
		t.Fatal("decode config over gob:", err)
	}
	if !proto.Equal(&got, want) {
		t.Fatalf("gob round-trip mismatch:\n got: %v\nwant: %v", &got, want)
	}
	if got.Config.GenericS3[0].MountedRead.MountView != nil {
		t.Fatal("absent read SubPath became present during gob round-trip")
	}
	if got.Config.GenericS3[0].MountedWrite.GetSubPath() != "" {
		t.Fatal("present empty write SubPath did not survive gob round-trip")
	}
	if _, present := got.Config.GenericS3[0].MountedWrite.GetMountView().(*schema.S3MountedWrite_SubPath); !present {
		t.Fatal("empty write SubPath lost oneof presence during gob round-trip")
	}
}
