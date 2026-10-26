package worker

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/funnel/compute/kubernetes/storageplan"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestWorkerUsesSubmittedStorageSnapshot(t *testing.T) {
	submitted := config.DefaultConfig()
	submitted.Kubernetes.JobsNamespace = "submitted-tasks"
	submitted.Worker.WorkDir = "/submitted-work"
	mount := t.TempDir()
	if err := os.WriteFile(filepath.Join(mount, "input"), []byte("submitted mount"), 0600); err != nil {
		t.Fatal(err)
	}
	submitted.Kubernetes.Storage = nativeWorkerStorage(t, fmt.Sprintf(`{
 "WorkspaceVolume":"workspace",
 "volumes":[
  {"name":"workspace","persistentVolumeClaim":{"claimName":"submitted-claim"}},
  {"name":"reference","persistentVolumeClaim":{"claimName":"reference-claim"}}
 ],
 "volumeMounts":[{"name":"reference","mountPath":%q,"readOnly":true}]
 }`, mount))
	submitted.GenericS3 = []*config.GenericS3Storage{{Endpoint: "http://s3.invalid", Bucket: "bucket", Key: "key", Secret: "secret", MountedRead: &config.S3MountedRead{Prefix: "ref", Volume: "reference"}}}
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
	conf.GenericS3 = []*config.GenericS3Storage{{Endpoint: "http://s3.invalid", Bucket: "bucket", Key: "new-key", Secret: "new-secret", MountedRead: &config.S3MountedRead{Prefix: "wrong", Volume: "unknown-static-mount"}}}
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
	destination := filepath.Join(t.TempDir(), "input")
	if _, err := w.Store.Get(context.Background(), "s3://bucket/ref/input", destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "submitted mount" || conf.GenericS3[0].MountedRead.Volume != "unknown-static-mount" {
		t.Fatal("worker did not use the submitted mount independently of its static config")
	}
}

func TestWorkerRejectsMountedReadWithoutSubmittedPlan(t *testing.T) {
	t.Setenv(storageplan.Env, "")
	for _, compute := range []string{"local", "kubernetes"} {
		t.Run(compute, func(t *testing.T) {
			conf := config.DefaultConfig()
			conf.Compute = compute
			conf.EventWriters = nil
			conf.GenericS3 = []*config.GenericS3Storage{{MountedRead: &config.S3MountedRead{Volume: "reference"}}}
			opts := &Options{TaskBase64: base64.StdEncoding.EncodeToString([]byte(`{"id":"task","executors":[{"image":"alpine","command":["true"]}]}`))}
			_, err := NewWorker(context.Background(), conf, logger.NewLogger("test", logger.DefaultConfig()), opts)
			if err == nil || !strings.Contains(err.Error(), "requires a submitted Kubernetes storage plan") {
				t.Fatalf("unresolved mounted read accepted: %v", err)
			}
		})
	}
}

func TestWorkerEmptySnapshotDoesNotActivateStaticMountedRead(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Compute = "kubernetes"
	conf.EventWriters = nil
	conf.AmazonS3.Disabled = true
	conf.GoogleStorage.Disabled = true
	conf.Kubernetes.JobsNamespace = "tasks"
	conf.Kubernetes.Storage = nativeWorkerStorage(t, `{"WorkspaceVolume":"workspace","volumes":[{"name":"workspace","persistentVolumeClaim":{"claimName":"workspace"}}]}`)
	plan, err := storageplan.Resolve(&tes.Task{Id: "snapshot-test"}, conf)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := plan.Encode()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(storageplan.Env, snapshot)
	conf.GenericS3 = []*config.GenericS3Storage{{Endpoint: "http://s3.invalid", Bucket: "bucket", Key: "key", Secret: "secret", MountedRead: &config.S3MountedRead{Volume: "missing"}, MountedWrite: &config.S3MountedWrite{Volume: "missing-output"}}}
	opts := &Options{TaskBase64: base64.StdEncoding.EncodeToString([]byte(`{"id":"snapshot-test","executors":[{"image":"alpine","command":["true"]}]}`))}
	w, err := NewWorker(context.Background(), conf, logger.NewLogger("test", logger.DefaultConfig()), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if len(w.Executor.StoragePlan.S3ReadMounts) != 0 || len(w.Executor.StoragePlan.S3WriteMounts) != 0 {
		t.Fatal("static config changed submitted mount list")
	}
}

func TestWorkerRejectsMountedWriteWithoutSubmittedPlan(t *testing.T) {
	t.Setenv(storageplan.Env, "")
	for _, compute := range []string{"local", "kubernetes"} {
		t.Run(compute, func(t *testing.T) {
			conf := config.DefaultConfig()
			conf.Compute = compute
			conf.EventWriters = nil
			conf.GenericS3 = []*config.GenericS3Storage{{MountedWrite: &config.S3MountedWrite{Volume: "outputs"}}}
			opts := &Options{TaskBase64: base64.StdEncoding.EncodeToString([]byte(`{"id":"task","executors":[{"image":"alpine","command":["true"]}]}`))}
			_, err := NewWorker(context.Background(), conf, logger.NewLogger("test", logger.DefaultConfig()), opts)
			if err == nil || !strings.Contains(err.Error(), "requires a submitted Kubernetes storage plan") {
				t.Fatalf("unresolved mounted write accepted: %v", err)
			}
		})
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
