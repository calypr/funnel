package worker

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/ohsu-comp-bio/funnel/compute/kubernetes/storageplan"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestWorkerUsesSubmittedStorageSnapshot(t *testing.T) {
	submitted := config.DefaultConfig()
	submitted.Kubernetes.JobsNamespace = "submitted-tasks"
	submitted.Worker.WorkDir = "/submitted-work"
	submitted.Kubernetes.Storage = nativeWorkerStorage(t, `{"WorkspaceVolume":"workspace","volumes":[{"name":"workspace","persistentVolumeClaim":{"claimName":"submitted-claim"}}]}`)
	plan, err := storageplan.Resolve(&tes.Task{Id: "snapshot-test"}, submitted)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := plan.Encode()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(storageplan.Env, snapshot)

	conf := config.DefaultConfig()
	conf.Compute = "kubernetes"
	conf.EventWriters = nil
	conf.GoogleStorage.Disabled = true
	conf.AmazonS3.Disabled = true
	conf.Worker.WorkDir = "/different-work"
	conf.Kubernetes.JobsNamespace = "different-tasks"
	conf.Kubernetes.Storage = &config.KubernetesStorage{}
	opts := &Options{TaskBase64: base64.StdEncoding.EncodeToString([]byte(`{"id":"snapshot-test","executors":[{"image":"alpine:3.21","command":["true"]}]}`))}
	w, err := NewWorker(context.Background(), conf, logger.NewLogger("test", logger.DefaultConfig()), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.Conf.WorkDir != "/submitted-work" || w.Executor.JobsNamespace != "submitted-tasks" || w.Executor.StoragePlan.Volumes[0].PersistentVolumeClaim.ClaimName != "submitted-claim" {
		t.Fatalf("worker did not use submitted storage plan: %+v", w.Executor)
	}
	if conf.Worker.WorkDir != "/different-work" || conf.Kubernetes.JobsNamespace != "different-tasks" {
		t.Fatal("worker startup changed the supplied configuration")
	}
}

func nativeWorkerStorage(t *testing.T, raw string) *config.KubernetesStorage {
	t.Helper()
	result := &config.KubernetesStorage{}
	if err := protojson.Unmarshal([]byte(raw), result); err != nil {
		t.Fatal(err)
	}
	return result
}
