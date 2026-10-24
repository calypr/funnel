package kubernetes

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"google.golang.org/protobuf/types/known/structpb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMountedWriteClaimAccessAtSubmission(t *testing.T) {
	for _, source := range []string{"existing", "template"} {
		for _, tc := range []struct {
			name      string
			modes     []corev1.PersistentVolumeAccessMode
			readOnly  bool
			wantError bool
		}{
			{name: "read only output", modes: []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany}, wantError: true},
			{name: "missing access mode", wantError: true},
			{name: "read write once", modes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}},
			{name: "read write many", modes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}},
			{name: "worker only single pod", modes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOncePod}},
			{name: "mixed writable", modes: []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany, corev1.ReadWriteMany}},
			{name: "read only input", modes: []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany}, readOnly: true},
		} {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				client := bindingClient()
				storageConfig := createdStorage(t, false)
				storageConfig.VolumeMounts = []*structpb.Struct{nativeResource(t, "name: results\nmountPath: /results\nsubPath: data\n")}
				if source == "template" {
					modes := make([]string, len(tc.modes))
					for i, mode := range tc.modes {
						modes[i] = string(mode)
					}
					manifest := strings.Replace(boundClaimTemplate, "name: workspace", "name: results", 1)
					manifest = strings.Replace(manifest, "[ReadWriteMany]", "["+strings.Join(modes, ",")+"]", 1)
					storageConfig.VolumeClaimTemplates = append(storageConfig.VolumeClaimTemplates, nativeResource(t, manifest))
				} else {
					storageConfig.Volumes = []*structpb.Struct{nativeResource(t, "name: results\npersistentVolumeClaim:\n  claimName: output-data\n")}
					claim := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "output-data", Namespace: "tasks"}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: tc.modes}}
					if _, err := client.CoreV1().PersistentVolumeClaims("tasks").Create(ctx, claim, metav1.CreateOptions{}); err != nil {
						t.Fatal(err)
					}
				}
				conf := bindingConfig(t, storageConfig)
				conf.Worker.WorkDir = "/work"
				conf.Kubernetes.WorkerImage = "funnel:test"
				s3 := &config.GenericS3Storage{Endpoint: "https://s3.example.org", Bucket: "results", Key: "key", Secret: "secret"}
				if tc.readOnly {
					s3.MountedRead = &config.S3MountedRead{Volume: "results"}
					storageConfig.VolumeMounts[0] = nativeResource(t, "name: results\nmountPath: /results\nsubPath: data\nreadOnly: true\n")
				} else {
					s3.MountedWrite = &config.S3MountedWrite{Volume: "results"}
				}
				conf.GenericS3 = []*config.GenericS3Storage{s3}
				references := strings.Replace(boundClaimTemplate, "name: workspace", "name: references", 1)
				references = strings.Replace(references, "ReadWriteMany", "ReadOnlyMany", 1)
				storageConfig.VolumeClaimTemplates = append(storageConfig.VolumeClaimTemplates, nativeResource(t, references))
				storageConfig.VolumeMounts = append(storageConfig.VolumeMounts, nativeResource(t, "name: references\nmountPath: /references\nreadOnly: true\n"))
				conf.GenericS3 = append(conf.GenericS3, &config.GenericS3Storage{
					Endpoint: "https://s3.example.org", Bucket: "references", Key: "key", Secret: "secret",
					MountedRead: &config.S3MountedRead{Volume: "references"},
				})
				backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
				client.ClearActions()
				err := backend.Submit(ctx, &tes.Task{Id: "write-access"}, conf)
				if tc.wantError {
					if err == nil || !strings.Contains(err.Error(), "mounted write") || !strings.Contains(err.Error(), "writable access mode") {
						t.Fatalf("expected mounted-write claim access error, got %v", err)
					}
					for _, action := range client.Actions() {
						if action.GetVerb() != "get" {
							t.Fatalf("invalid claim reached resource mutation: %v", action)
						}
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := client.BatchV1().Jobs("tasks").Get(ctx, "write-access", metav1.GetOptions{}); err != nil {
					t.Fatalf("valid storage did not create worker Job: %v", err)
				}
			})
		}
	}
}
