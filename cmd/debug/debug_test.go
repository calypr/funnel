package debug

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugReportOmitsFreeFormSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `Compute: aws-batch
Database: postgres
Postgres:
  Password: dummy-db-password
Worker:
  Container:
    Env:
      CUSTOM: dummy-env-secret
    RunCommand: docker --token=dummy-command-secret
Node:
  Metadata:
    custom: dummy-metadata-secret
Kubernetes:
  WorkerTemplate: "value: dummy-template-secret"
Plugins:
  Params:
    custom: dummy-plugin-secret
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", path, "--Compute", "kubernetes"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Config struct {
			Compute  string `json:"Compute"`
			Database string `json:"Database"`
		} `json:"config"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Config.Compute != "kubernetes" || got.Config.Database != "postgres" {
		t.Fatalf("wrong config: %+v", got)
	}
	for _, secret := range []string{"dummy-url-password", "dummy-db-password", "dummy-env-secret", "dummy-command-secret", "dummy-metadata-secret", "dummy-template-secret", "dummy-plugin-secret"} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("report exposed %q", secret)
		}
	}
}
