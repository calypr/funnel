package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/calypr/funnel/api/tes"
	appconfig "github.com/calypr/funnel/internal/config"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/storage"
)

func TestWorkerContainerTagsRespectOptInAndRemainLiteral(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			capture := filepath.Join(dir, "argv")
			marker := filepath.Join(dir, "must-not-exist")
			t.Setenv("FUNNEL_TEST_CAPTURE_ARGS", capture)
			driver := filepath.Join(dir, "driver")
			script := "#!/bin/sh\nif [ \"$1\" = run ]; then printf '%s\\n' \"$@\" > \"$FUNNEL_TEST_CAPTURE_ARGS\"; fi\n"
			if err := os.WriteFile(driver, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			conf := appconfig.DefaultConfig().Worker
			conf.WorkDir = filepath.Join(dir, "work")
			conf.Container.DriverCommand = driver
			conf.Container.EnableTags = enabled
			conf.PollingRate = durationpb.New(10 * time.Millisecond)
			value := "study 'quoted' $HOME $(touch " + marker + ") --privileged"
			task := &tes.Task{Id: "labels", State: tes.Queued, Tags: map[string]string{"study": value}, Executors: []*tes.Executor{{Image: "test", Command: []string{"true"}}}}
			worker := &DefaultWorker{Conf: conf, Store: &storage.Mux{}, TaskReader: &Base64TaskReader{task: task}, EventWriter: events.Noop{}}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := worker.Run(ctx); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
			labels := 0
			for i, arg := range args {
				if arg == "--privileged" {
					t.Fatal("tag injected a driver option")
				}
				if arg == "--label" {
					labels++
					if i+1 >= len(args) || args[i+1] != "study="+value {
						t.Fatalf("label value changed: %q", args)
					}
				}
			}
			want := 0
			if enabled {
				want = 1
			}
			if labels != want {
				t.Fatalf("label count = %d, want %d; argv = %q", labels, want, args)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("tag command was executed: %v", err)
			}
		})
	}
}
