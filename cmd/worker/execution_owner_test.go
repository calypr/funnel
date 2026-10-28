package worker

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
)

func TestNewWorkerReadsKubernetesPodIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, compute, namespace, pod, uid string
		wantOwner, wantError               bool
	}{
		{name: "submitted worker", compute: "kubernetes", namespace: "jobs", pod: "worker-a", uid: "uid-a", wantOwner: true},
		{name: "alias", compute: "k8s", namespace: "jobs", pod: "worker-a", uid: "uid-a", wantOwner: true},
		{name: "manual worker", compute: "kubernetes"},
		{name: "incomplete identity", compute: "kubernetes", pod: "worker-a", wantError: true},
		{name: "other backend", compute: "local", namespace: "jobs", pod: "worker-a", uid: "uid-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(events.OwnerNamespaceEnv, tc.namespace)
			t.Setenv(events.OwnerNameEnv, tc.pod)
			t.Setenv(events.OwnerUIDEnv, tc.uid)
			conf := config.DefaultConfig()
			conf.Compute = tc.compute
			conf.Worker.WorkDir = t.TempDir()
			conf.EventWriters = nil
			opts := &Options{TaskBase64: base64.StdEncoding.EncodeToString([]byte(`{"id":"owner-test","executors":[{"image":"alpine","command":["true"]}]}`))}
			w, err := NewWorker(context.Background(), conf, logger.NewLogger("test", logger.DefaultConfig()), opts)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "worker identity") {
					t.Fatalf("NewWorker error=%v, want identity validation failure", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			owner := w.Executor.ExecutionOwner
			if (owner != nil) != tc.wantOwner {
				t.Fatalf("owner=%v, want present=%v", owner, tc.wantOwner)
			}
			if owner != nil && (owner.Namespace != tc.namespace || owner.PodName != tc.pod || owner.PodUid != tc.uid) {
				t.Fatalf("owner=%v", owner)
			}
		})
	}
}
