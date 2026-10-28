package config

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestSafeRedactsFreeFormConfiguration(t *testing.T) {
	volume, err := structpb.NewStruct(map[string]any{"secret": "volume-secret"})
	if err != nil {
		t.Fatal(err)
	}
	c := &Config{
		AWSBatch: &AWSBatch{AWSConfig: &AWSConfig{Endpoint: "https://user:batch-secret@batch.example/path-secret?token=query-secret#fragment-secret"}},
		AmazonS3: &AmazonS3Storage{SSE: &SSE{CustomerKeyFile: "keyfile-secret"}},
		Node:     &Node{Metadata: map[string]string{"arbitrary": "node-secret"}},
		Worker: &Worker{DriverCommand: "driver-secret", Container: &ContainerConfig{
			Env: map[string]string{"ARBITRARY": "environment-secret"}, Tags: map[string]string{"tag": "tag-secret"},
			Command: []string{"command-secret"}, RunCommand: "run-secret", PullCommand: "pull-secret", StopCommand: "stop-secret",
		}},
		Slurm:      &HPCBackend{Template: "slurm-secret"},
		Kubernetes: &Kubernetes{WorkerTemplate: "template-secret", NodeSelector: map[string]string{"key": "selector-secret"}, Tolerations: []*Toleration{{Value: "toleration-secret"}}, Storage: &KubernetesStorage{Volumes: []*structpb.Struct{volume}}},
	}
	safe := c.Safe()
	data, err := protojson.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"batch-secret", "path-secret", "query-secret", "fragment-secret", "keyfile-secret", "node-secret", "driver-secret", "environment-secret", "tag-secret", "command-secret", "run-secret", "pull-secret", "stop-secret", "slurm-secret", "template-secret", "selector-secret", "toleration-secret", "volume-secret"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("safe config exposes %q", secret)
		}
	}
	if !strings.Contains(safe.AWSBatch.AWSConfig.Endpoint, "batch.example") {
		t.Errorf("endpoint hostname missing: %q", safe.AWSBatch.AWSConfig.Endpoint)
	}
	if c.Worker.Container.Env["ARBITRARY"] != "environment-secret" || c.Kubernetes.Storage.Volumes[0].Fields["secret"].GetStringValue() != "volume-secret" {
		t.Fatal("Safe modified the original configuration")
	}
}

func TestRedactEndpoint(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"localhost:8000", "localhost:8000"},
		{"https://example.com", "https://example.com"},
		{"https://user:password@example.com/path?token=secret#fragment", "https://REDACTED@example.com/REDACTED?REDACTED#REDACTED"},
		{"user:password@host", "***"},
		{"https://%zz", "***"},
	} {
		if got := redactEndpoint(tc.input); got != tc.want {
			t.Errorf("redactEndpoint(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
