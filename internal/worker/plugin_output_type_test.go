package worker

import (
	"bytes"
	"context"
	"encoding/gob"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/funnel/api/plugin"
	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/storage"
)

func TestPluginGobPreservesExplicitFileOutputValidation(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "result.txt"), []byte("result"), 0644); err != nil {
		t.Fatal(err)
	}
	outputs := []*tes.Output{{
		Path: directory,
		Url:  "s3://bucket/output",
		Type: tes.File.Enum(),
	}}
	writer := events.NewTaskWriter("plugin-output", 0, events.Noop{})
	_, beforeGobErr := FlattenOutputs(context.Background(), outputs, storage.Fake{}, writer)
	if beforeGobErr == nil || !strings.Contains(beforeGobErr.Error(), "declared as a file") {
		t.Fatalf("explicit FILE output pointing to a directory returned %v, want declared-as-file error", beforeGobErr)
	}

	var encoded bytes.Buffer
	if err := gob.NewEncoder(&encoded).Encode(&plugin.Job{Task: &tes.Task{Outputs: outputs}}); err != nil {
		t.Fatal("encode plugin job:", err)
	}
	var decoded plugin.Job
	if err := gob.NewDecoder(&encoded).Decode(&decoded); err != nil {
		t.Fatal("decode plugin job:", err)
	}

	flat, afterGobErr := FlattenOutputs(context.Background(), decoded.Task.Outputs, storage.Fake{}, writer)
	if afterGobErr == nil || !strings.Contains(afterGobErr.Error(), "declared as a file") {
		t.Fatalf("explicit FILE output returned %v after plugin Gob and flattened to %v, want declared-as-file error", afterGobErr, flat)
	}
}
