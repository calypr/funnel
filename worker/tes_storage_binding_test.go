package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/calypr/funnel/compute/kubernetes/storageplan"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/storage"
	"github.com/calypr/funnel/tes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestTESRequestStagesIdenticallyAcrossStorageBindings(t *testing.T) {
	request := []byte(`{"id":"portable-task","inputs":[{"url":"s3://reference/genome.fa","path":"/inputs/genome.fa","type":"FILE"}],"outputs":[{"url":"s3://results/result.txt","path":"/outputs/result.txt","type":"FILE"}],"volumes":["/scratch"],"executors":[{"image":"alpine:3.21","command":["sh","-c","cat /inputs/genome.fa > /outputs/result.txt"]}]}`)
	for _, deployment := range []string{"chicago", "shared-cluster"} {
		t.Run(deployment, func(t *testing.T) {
			task := &tes.Task{}
			if err := protojson.Unmarshal(request, task); err != nil {
				t.Fatal(err)
			}
			original := proto.Clone(task).(*tes.Task)
			mountRoot := filepath.Join(t.TempDir(), deployment)
			if err := os.MkdirAll(mountRoot, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(mountRoot, "genome.fa"), []byte(">chr1\nACGT\n"), 0600); err != nil {
				t.Fatal(err)
			}
			conf := config.DefaultConfig()
			conf.Kubernetes.JobsNamespace = "tasks"
			conf.Worker.WorkDir = t.TempDir()
			conf.Kubernetes.Storage = &config.KubernetesStorage{}
			if err := protojson.Unmarshal([]byte(fmt.Sprintf(`{
 "WorkspaceVolume":"workspace",
 "volumes":[
  {"name":"workspace","persistentVolumeClaim":{"claimName":%q}},
  {"name":"reference","persistentVolumeClaim":{"claimName":%q}}
 ],
 "volumeMounts":[
 {"name":"reference","mountPath":%q,"subPath":"release-1","readOnly":true},
 {"name":"reference","mountPath":%q,"subPath":"release-2","readOnly":true}
 ]
 }`, deployment+"-workspace", deployment+"-reference", mountRoot, mountRoot+"-other")), conf.Kubernetes.Storage); err != nil {
				t.Fatal(err)
			}
			conf.GenericS3 = []*config.GenericS3Storage{{Endpoint: "http://s3.invalid", Bucket: "reference", Key: "key", Secret: "secret", MountedRead: &config.S3MountedRead{Volume: "reference", MountView: &config.S3MountedRead_SubPath{SubPath: "release-1"}}}}
			conf.AmazonS3.Disabled = true
			conf.GoogleStorage.Disabled = true
			plan, err := storageplan.Resolve(task, conf)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := plan.Encode()
			if err != nil {
				t.Fatal(err)
			}
			submitted, err := storageplan.Decode(encoded)
			if err != nil {
				t.Fatal(err)
			}
			store, err := storage.NewWorkerMux(conf, submitted.S3ReadMounts)
			if err != nil {
				t.Fatal(err)
			}
			mapper := NewFileMapper(filepath.Join(conf.Worker.WorkDir, task.Id))
			if err := mapper.MapTask(task, "kubernetes"); err != nil {
				t.Fatal(err)
			}
			if err := DownloadInputs(context.Background(), mapper.Inputs, store, events.NewTaskWriter(task.Id, 0, events.Noop{}), 1); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(mapper.WorkDir, "inputs", "genome.fa"))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != ">chr1\nACGT\n" {
				t.Fatalf("TES input bytes changed: %q", data)
			}
			scratch, err := os.ReadDir(filepath.Join(mapper.WorkDir, "scratch"))
			if err != nil {
				t.Fatal(err)
			}
			if len(scratch) != 0 {
				t.Fatalf("TES scratch volume contains reference data: %v", scratch)
			}
			executor := &batchv1.Job{Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "executor"}}}}}}
			mounts := make([]corev1.VolumeMount, 0, len(mapper.Volumes))
			for _, volume := range mapper.Volumes {
				mounts = append(mounts, corev1.VolumeMount{MountPath: volume.ContainerPath, ReadOnly: volume.Readonly})
			}
			if err := submitted.ApplyExecutor(executor, task.Id, mounts); err != nil {
				t.Fatal(err)
			}
			volumes := executor.Spec.Template.Spec.Volumes
			if len(volumes) != 1 || volumes[0].PersistentVolumeClaim.ClaimName != deployment+"-workspace" {
				t.Fatalf("executor received a deployment data volume: %+v", volumes)
			}
			if !proto.Equal(task, original) {
				t.Fatalf("storage deployment changed TES request: %v", task)
			}
		})
	}
}
