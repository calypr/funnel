package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
)

func TestConfiguredFileEventWriter(t *testing.T) {
	conf := config.DefaultConfig()
	conf.EventFile = filepath.Join(t.TempDir(), "events.ndjson")
	var builder eventWriterBuilder
	builder.Add(context.Background(), "file", conf, logger.NewLogger("test", logger.DefaultConfig()))
	writer, err := builder.Writer()
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteEvent(context.Background(), events.NewState("task", tes.State_COMPLETE)); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	data, err := os.ReadFile(conf.EventFile)
	if err != nil {
		t.Fatal(err)
	}
	var event events.Event
	if err := events.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Id != "task" || event.GetState() != tes.State_COMPLETE {
		t.Fatalf("unexpected event: %v", &event)
	}
}
