package storageplan

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/storage"
	"github.com/calypr/funnel/tes"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

func object(values map[string]interface{}) *structpb.Struct {
	result, err := structpb.NewStruct(values)
	if err != nil {
		panic(err)
	}
	return result
}

func nativeYAML(t *testing.T, raw string) *structpb.Struct {
	t.Helper()
	data, err := yaml.YAMLToJSONStrict([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]interface{}
	if err := json.Unmarshal(data, &values); err != nil {
		t.Fatal(err)
	}
	return object(values)
}

func existingConfig() *config.Config {
	c := config.DefaultConfig()
	c.Kubernetes.JobsNamespace = "jobs"
	c.Worker.WorkDir = "/work"
	c.Kubernetes.Storage = &config.KubernetesStorage{
		WorkspaceVolume: "workspace",
		Volumes: []*structpb.Struct{
			object(map[string]interface{}{
				"name":                  "workspace",
				"persistentVolumeClaim": map[string]interface{}{"claimName": "shared-data"},
			}),
		},
	}
	return c
}

func addVolume(c *config.Config, name, claim string) {
	c.Kubernetes.Storage.Volumes = append(c.Kubernetes.Storage.Volumes, object(map[string]interface{}{
		"name": name, "persistentVolumeClaim": map[string]interface{}{"claimName": claim},
	}))
}

func setVolume(c *config.Config, index int, name, claim string) {
	c.Kubernetes.Storage.Volumes[index] = object(map[string]interface{}{
		"name": name, "persistentVolumeClaim": map[string]interface{}{"claimName": claim},
	})
}

func addMount(c *config.Config, volume, mountPath string, extra map[string]interface{}) {
	mount := map[string]interface{}{"name": volume, "mountPath": mountPath}
	for key, value := range extra {
		mount[key] = value
	}
	c.Kubernetes.Storage.VolumeMounts = append(c.Kubernetes.Storage.VolumeMounts, object(mount))
}

func mountedS3(bucket, prefix, volume string) *config.GenericS3Storage {
	return &config.GenericS3Storage{
		Endpoint: "https://s3.example.test/custom/path",
		Key:      "access-key", Secret: "access-secret", Bucket: bucket,
		MountedRead: &config.S3MountedRead{Volume: volume, Prefix: prefix},
	}
}

func mountedS3Write(bucket, prefix, volume string, timeout *durationpb.Duration) *config.GenericS3Storage {
	backend := mountedS3(bucket, prefix, volume)
	backend.MountedRead = nil
	backend.MountedWrite = &config.S3MountedWrite{Volume: volume, Prefix: prefix, Timeout: timeout}
	return backend
}

func withReadSubPath(backend *config.GenericS3Storage, subPath string) {
	backend.MountedRead.MountView = &config.S3MountedRead_SubPath{SubPath: subPath}
}

func withWriteSubPath(backend *config.GenericS3Storage, subPath string) {
	backend.MountedWrite.MountView = &config.S3MountedWrite_SubPath{SubPath: subPath}
}

const pvcTemplate = "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: workspace\nspec:\n  accessModes: [ReadWriteMany]\n  storageClassName: fast\n  resources:\n    requests:\n      storage: 5Gi\n"

const pairedPVC = "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: workspace\nspec:\n  volumeName: disk-{{.TaskId}}\n  accessModes: [ReadWriteMany]\n  storageClassName: fast\n  resources:\n    requests:\n      storage: 5Gi\n"

const pvTemplate = "apiVersion: v1\nkind: PersistentVolume\nmetadata:\n  name: disk-{{.TaskId}}\nspec:\n  capacity:\n    storage: 5Gi\n  accessModes: [ReadWriteMany]\n  storageClassName: fast\n  persistentVolumeReclaimPolicy: Retain\n  csi:\n    driver: example.csi\n    volumeHandle: handle-{{.TaskId}}\n    volumeAttributes:\n      bucket: native-bucket\n"

func TestExistingClaimWorkerExecutorSnapshot(t *testing.T) {
	c := existingConfig()
	addVolume(c, "cache", "shared-cache")
	addMount(c, "cache", "/cache", map[string]interface{}{"readOnly": true, "subPath": "cache"})
	p, err := Resolve(&tes.Task{Id: "task-123"}, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.OwnedClaims) != 0 || len(p.OwnedVolumes) != 0 {
		t.Fatalf("external claims became owned: %+v", p)
	}
	worker := testJob()
	if err := p.ApplyWorker(worker, "/work", "task-123"); err != nil {
		t.Fatal(err)
	}
	if len(worker.Spec.Template.Spec.Volumes) != 2 {
		t.Fatal("worker did not receive both PVC volumes")
	}
	workerMount := worker.Spec.Template.Spec.Containers[0].VolumeMounts[0]
	if workerMount.MountPath != "/work/task-123" || workerMount.SubPath != "task-123" {
		t.Fatalf("bad workspace mount: %+v", workerMount)
	}
	snapshot := worker.Annotations[Annotation]
	if env := worker.Spec.Template.Spec.Containers[0].Env[0]; env.Name != Env || env.Value != snapshot {
		t.Fatal("worker snapshot differs from annotation")
	}
	restarted, err := Decode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot, err := restarted.Encode(); err != nil || snapshot != worker.Annotations[Annotation] {
		t.Fatalf("snapshot did not round trip: %s, %v", snapshot, err)
	}
	executor := testJob()
	if err := restarted.ApplyExecutor(executor, "task-123", []corev1.VolumeMount{{MountPath: "/data/input", ReadOnly: true}, {MountPath: "/results"}}); err != nil {
		t.Fatal(err)
	}
	if len(executor.Spec.Template.Spec.Volumes) != 1 || executor.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "shared-data" {
		t.Fatal("executor did not use workspace claim")
	}
	got := executor.Spec.Template.Spec.Containers[0].VolumeMounts
	if got[0].SubPath != "task-123/data/input" || !got[0].ReadOnly || got[1].SubPath != "task-123/results" {
		t.Fatalf("executor mapping incorrect: %+v", got)
	}
}

func TestWorkspaceAllowsReadOnlyRootView(t *testing.T) {
	c := existingConfig()
	addMount(c, "workspace", "/bucket", map[string]interface{}{"readOnly": true})
	p, err := Resolve(&tes.Task{Id: "task-123"}, c)
	if err != nil {
		t.Fatal(err)
	}
	job := testJob()
	if err := p.ApplyWorker(job, "/work", "task-123"); err != nil {
		t.Fatal(err)
	}
	mounts := job.Spec.Template.Spec.Containers[0].VolumeMounts
	if len(mounts) != 2 || mounts[0].ReadOnly || mounts[0].SubPath != "task-123" || !mounts[1].ReadOnly || mounts[1].SubPath != "" || mounts[1].MountPath != "/bucket" {
		t.Fatalf("workspace and root view access changed: %+v", mounts)
	}
}

func TestS3ReadMountTracksNativeMountPathAndSnapshot(t *testing.T) {
	c := existingConfig()
	addMount(c, "workspace", "/reference", map[string]interface{}{"readOnly": true, "subPathExpr": "reference-$(POD_NAME)"})
	c.GenericS3 = []*config.GenericS3Storage{mountedS3("reference-bucket", "reference/", "workspace")}
	p, err := Resolve(&tes.Task{Id: "task-123"}, c)
	if err != nil {
		t.Fatal(err)
	}
	want := storage.S3ReadMount{Endpoint: "https://s3.example.test/custom/path", Bucket: "reference-bucket", Prefix: "reference/", Root: "/reference"}
	if len(p.S3ReadMounts) != 1 || p.S3ReadMounts[0] != want {
		t.Fatalf("S3 read identity or native root changed: %+v", p.S3ReadMounts)
	}
	if len(p.WorkerMounts) != 1 || p.WorkerMounts[0].MountPath != "/reference" || p.WorkerMounts[0].SubPathExpr != "reference-$(POD_NAME)" || !p.WorkerMounts[0].ReadOnly {
		t.Fatalf("native mount fields changed: %+v", p.WorkerMounts)
	}
	worker := testJob()
	if err := p.ApplyWorker(worker, "/work", "task-123"); err != nil {
		t.Fatal(err)
	}
	if mount := worker.Spec.Template.Spec.Containers[0].VolumeMounts[1]; mount.MountPath != "/reference" || mount.SubPathExpr != "reference-$(POD_NAME)" {
		t.Fatalf("worker mount changed: %+v", mount)
	}
	snapshot, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snapshot, "access-key") || strings.Contains(snapshot, "access-secret") {
		t.Fatalf("storage snapshot contains S3 credentials: %s", snapshot)
	}
	restored, err := Decode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if restored.S3ReadMounts[0] != want || restored.WorkerMounts[0].SubPathExpr != "reference-$(POD_NAME)" {
		t.Fatalf("snapshot lost S3 or mount identity: %+v", restored)
	}
}

func TestS3MountSelectionByVolumeAndSubPath(t *testing.T) {
	t.Run("unique volume without selector allows subPathExpr", func(t *testing.T) {
		c := existingConfig()
		addMount(c, "workspace", "/reference", map[string]interface{}{"subPathExpr": "pod-$(POD_NAME)"})
		c.GenericS3 = []*config.GenericS3Storage{mountedS3("bucket", "prefix/", "workspace")}
		p, err := Resolve(&tes.Task{Id: "task-123"}, c)
		if err != nil {
			t.Fatal(err)
		}
		if p.S3ReadMounts[0].Root != "/reference" {
			t.Fatalf("selected root = %q", p.S3ReadMounts[0].Root)
		}
	})
	t.Run("ambiguous volume without selector fails", func(t *testing.T) {
		c := existingConfig()
		addMount(c, "workspace", "/reference-a", map[string]interface{}{"subPath": "a"})
		addMount(c, "workspace", "/reference-b", map[string]interface{}{"subPath": "b"})
		c.GenericS3 = []*config.GenericS3Storage{mountedS3("bucket", "prefix/", "workspace")}
		if _, err := Resolve(&tes.Task{Id: "task-123"}, c); err == nil || !strings.Contains(err.Error(), "specify SubPath") {
			t.Fatalf("ambiguous S3 volume accepted: %v", err)
		}
	})
	t.Run("explicit empty subPath selects root mount", func(t *testing.T) {
		c := existingConfig()
		addMount(c, "workspace", "/reference-root", nil)
		addMount(c, "workspace", "/reference-child", map[string]interface{}{"subPath": "child"})
		backend := mountedS3("bucket", "prefix/", "workspace")
		withReadSubPath(backend, "")
		c.GenericS3 = []*config.GenericS3Storage{backend}
		p, err := Resolve(&tes.Task{Id: "task-123"}, c)
		if err != nil {
			t.Fatal(err)
		}
		if p.S3ReadMounts[0].Root != "/reference-root" {
			t.Fatalf("empty selector selected wrong root: %+v", p.S3ReadMounts[0])
		}
	})
	t.Run("selector rejects subPathExpr", func(t *testing.T) {
		c := existingConfig()
		addMount(c, "workspace", "/reference", map[string]interface{}{"subPathExpr": "pod-$(POD_NAME)"})
		backend := mountedS3("bucket", "prefix/", "workspace")
		withReadSubPath(backend, "")
		c.GenericS3 = []*config.GenericS3Storage{backend}
		if _, err := Resolve(&tes.Task{Id: "task-123"}, c); err == nil || !strings.Contains(err.Error(), "no static mount") {
			t.Fatalf("dynamic mount matched static selector: %v", err)
		}
	})
}

func TestS3MountedReadValidationAndIdentityOrder(t *testing.T) {
	c := existingConfig()
	c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{object(map[string]interface{}{"metadata": map[string]interface{}{"name": "unused"}})}
	c.GenericS3 = []*config.GenericS3Storage{mountedS3("bucket", "input/", "missing")}
	if _, err := Resolve(&tes.Task{Id: "task-123"}, c); err == nil || !strings.Contains(err.Error(), "unknown worker volume") {
		t.Fatalf("template parsing preceded S3 identity validation: %v", err)
	}

	tests := []struct {
		name   string
		change func(*config.Config)
		want   string
	}{
		{"disabled", func(c *config.Config) {
			b := mountedS3("bucket", "input/", "workspace")
			b.Disabled = true
			c.GenericS3 = []*config.GenericS3Storage{b}
		}, "disabled or invalid"},
		{"missing bucket", func(c *config.Config) { c.GenericS3 = []*config.GenericS3Storage{mountedS3("", "input/", "workspace")} }, "requires a bucket"},
		{"unknown volume", func(c *config.Config) {
			c.GenericS3 = []*config.GenericS3Storage{mountedS3("bucket", "input/", "missing")}
		}, "unknown worker volume"},
		{"invalid volume", func(c *config.Config) {
			c.GenericS3 = []*config.GenericS3Storage{mountedS3("bucket", "input/", "bad/name")}
		}, "invalid volume reference"},
		{"duplicate API-only identity", func(c *config.Config) {
			other := mountedS3("bucket", "", "")
			other.MountedRead = nil
			c.GenericS3 = []*config.GenericS3Storage{mountedS3("bucket", "input/", "workspace"), other}
		}, "duplicate mounted S3 backend"},
		{"duplicate endpoint and bucket", func(c *config.Config) {
			c.GenericS3 = []*config.GenericS3Storage{mountedS3("bucket", "one/", "workspace"), mountedS3("bucket", "two/", "workspace")}
		}, "duplicate mounted S3 backend"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := existingConfig()
			tt.change(c)
			if _, err := Resolve(&tes.Task{Id: "task-123"}, c); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
	for _, change := range []func(*config.Config){
		func(c *config.Config) { c.Kubernetes.Storage = nil },
		func(c *config.Config) { c.Kubernetes = nil },
	} {
		c := existingConfig()
		c.GenericS3 = []*config.GenericS3Storage{mountedS3("bucket", "input/", "workspace")}
		change(c)
		if _, err := Resolve(&tes.Task{Id: "task-123"}, c); err == nil || !strings.Contains(err.Error(), "require Kubernetes.Storage") {
			t.Fatalf("mounted read without Kubernetes.Storage accepted: %v", err)
		}
	}
}

func TestS3MountedWriteBehaviorAndValidation(t *testing.T) {
	c := existingConfig()
	addVolume(c, "results", "shared-results")
	addMount(c, "results", "/outputs", map[string]interface{}{"subPath": "task-results"})
	backend := mountedS3Write("results-bucket", "results/", "results", nil)
	withWriteSubPath(backend, "task-results")
	c.GenericS3 = []*config.GenericS3Storage{backend}
	p, err := Resolve(&tes.Task{Id: "task-123"}, c)
	if err != nil {
		t.Fatal(err)
	}
	want := storage.S3WriteMount{Endpoint: "https://s3.example.test/custom/path", Bucket: "results-bucket", Prefix: "results/", Root: "/outputs", Timeout: 3 * time.Hour}
	if len(p.S3WriteMounts) != 1 || p.S3WriteMounts[0] != want {
		t.Fatalf("S3 write mount mismatch: %+v", p.S3WriteMounts)
	}
	snapshot, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snapshot, "access-key") || strings.Contains(snapshot, "access-secret") {
		t.Fatalf("snapshot contains credentials: %s", snapshot)
	}
	decoded, err := Decode(snapshot)
	if err != nil || decoded.S3WriteMounts[0] != want {
		t.Fatalf("snapshot lost S3 write identity: %+v, %v", decoded, err)
	}
	wrongRoot := *decoded
	wrongRoot.S3WriteMounts = append([]storage.S3WriteMount(nil), decoded.S3WriteMounts...)
	wrongRoot.S3WriteMounts[0].Root = "/unmounted"
	if _, err := wrongRoot.Encode(); err == nil || !strings.Contains(err.Error(), "writable worker mount") {
		t.Fatalf("unmounted S3 write root accepted: %v", err)
	}
	for _, mutate := range []func(map[string]interface{}){
		func(payload map[string]interface{}) {
			mounts := payload["WorkerMounts"].([]interface{})
			mounts[0].(map[string]interface{})["readOnly"] = true
		},
		func(payload map[string]interface{}) {
			volumes := payload["Volumes"].([]interface{})
			for _, item := range volumes {
				volume := item.(map[string]interface{})
				if volume["name"] == "results" {
					volume["persistentVolumeClaim"].(map[string]interface{})["readOnly"] = true
				}
			}
		},
	} {
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(snapshot), &payload); err != nil {
			t.Fatal(err)
		}
		mutate(payload)
		invalid, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(string(invalid)); err == nil || !strings.Contains(err.Error(), "writable worker mount") {
			t.Fatalf("read-only S3 write snapshot accepted: %v", err)
		}
	}
	backend.MountedWrite.Timeout = durationpb.New(45 * time.Minute)
	p, err = Resolve(&tes.Task{Id: "task-123"}, c)
	if err != nil {
		t.Fatal(err)
	}
	want.Timeout = 45 * time.Minute
	if p.S3WriteMounts[0] != want {
		t.Fatalf("custom timeout mismatch: %+v", p.S3WriteMounts)
	}

	for _, tc := range []struct {
		name   string
		change func(*config.Config)
		want   string
	}{
		{"disabled backend", func(c *config.Config) {
			b := mountedS3Write("bucket", "output/", "workspace", nil)
			b.Disabled = true
			c.GenericS3 = []*config.GenericS3Storage{b}
		}, "disabled or invalid"},
		{"missing bucket", func(c *config.Config) {
			c.GenericS3 = []*config.GenericS3Storage{mountedS3Write("", "output/", "workspace", nil)}
		}, "requires a bucket"},
		{"unknown volume", func(c *config.Config) {
			c.GenericS3 = []*config.GenericS3Storage{mountedS3Write("bucket", "output/", "missing", nil)}
		}, "unknown worker volume"},
		{"duplicate API-only identity", func(c *config.Config) {
			other := mountedS3Write("bucket", "", "", nil)
			other.MountedWrite = nil
			c.GenericS3 = []*config.GenericS3Storage{mountedS3Write("bucket", "output/", "workspace", nil), other}
		}, "duplicate mounted S3 backend"},
		{"read-only mount", func(c *config.Config) {
			c.Kubernetes.Storage.VolumeMounts[0] = object(map[string]interface{}{"name": "workspace", "mountPath": "/outputs", "readOnly": true})
		}, "must be writable"},
		{"zero timeout", func(c *config.Config) { c.GenericS3[0].MountedWrite.Timeout = durationpb.New(0) }, "must be positive"},
		{"negative timeout", func(c *config.Config) { c.GenericS3[0].MountedWrite.Timeout = durationpb.New(-time.Second) }, "must be positive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := existingConfig()
			addMount(bad, "workspace", "/outputs", nil)
			bad.GenericS3 = []*config.GenericS3Storage{mountedS3Write("bucket", "output/", "workspace", nil)}
			tc.change(bad)
			if _, err := Resolve(&tes.Task{Id: "task-123"}, bad); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	for _, change := range []func(*config.Config){
		func(c *config.Config) { c.Kubernetes.Storage = nil },
		func(c *config.Config) { c.Kubernetes = nil },
	} {
		withoutStorage := existingConfig()
		withoutStorage.GenericS3 = []*config.GenericS3Storage{mountedS3Write("bucket", "output/", "workspace", nil)}
		change(withoutStorage)
		if _, err := Resolve(&tes.Task{Id: "task-123"}, withoutStorage); err == nil || !strings.Contains(err.Error(), "require Kubernetes.Storage") {
			t.Fatalf("mounted write without Kubernetes.Storage accepted: %v", err)
		}
	}
}

func TestS3MountedWriteRoutesRejectPhysicalPVCOverlap(t *testing.T) {
	for _, pair := range [][2]string{{"results", "results/child"}, {"", "results"}, {".", "results"}, {"results", "."}} {
		t.Run("overlapping subPaths "+pair[0]+" and "+pair[1], func(t *testing.T) {
			c := existingConfig()
			addVolume(c, "workspace-child", "shared-data")
			addMount(c, "workspace", "/outputs/parent", map[string]interface{}{"subPath": pair[0]})
			addMount(c, "workspace-child", "/outputs/child", map[string]interface{}{"subPath": pair[1]})
			first, second := mountedS3Write("one", "out/", "workspace", nil), mountedS3Write("two", "out/", "workspace-child", nil)
			withWriteSubPath(first, pair[0])
			withWriteSubPath(second, pair[1])
			c.GenericS3 = []*config.GenericS3Storage{first, second}
			if _, err := Resolve(&tes.Task{Id: "task-123"}, c); err == nil || !strings.Contains(err.Error(), "overlap") {
				t.Fatalf("overlapping write routes accepted: %v", err)
			}
		})
	}

	t.Run("unknown dynamic subPath sharing", func(t *testing.T) {
		c := existingConfig()
		addVolume(c, "workspace-child", "shared-data")
		addMount(c, "workspace", "/outputs/dynamic", map[string]interface{}{"subPathExpr": "task-$(POD_NAME)"})
		addMount(c, "workspace-child", "/outputs/static", map[string]interface{}{"subPath": "results"})
		first, second := mountedS3Write("one", "out/", "workspace", nil), mountedS3Write("two", "out/", "workspace-child", nil)
		withWriteSubPath(second, "results")
		c.GenericS3 = []*config.GenericS3Storage{first, second}
		if _, err := Resolve(&tes.Task{Id: "task-123"}, c); err == nil || !strings.Contains(err.Error(), "overlap") {
			t.Fatalf("dynamic write route sharing accepted: %v", err)
		}
	})

	t.Run("read and write may share one route", func(t *testing.T) {
		c := existingConfig()
		addMount(c, "workspace", "/outputs", map[string]interface{}{"subPath": "results"})
		backend := mountedS3Write("one", "out/", "workspace", nil)
		backend.MountedRead = &config.S3MountedRead{Volume: "workspace", Prefix: "in/", MountView: &config.S3MountedRead_SubPath{SubPath: "results"}}
		withWriteSubPath(backend, "results")
		c.GenericS3 = []*config.GenericS3Storage{backend}
		if _, err := Resolve(&tes.Task{Id: "task-123"}, c); err != nil {
			t.Fatalf("read and write on one route were rejected: %v", err)
		}
	})
}

func TestNamedContainerSelection(t *testing.T) {
	c := existingConfig()
	c.Kubernetes.Storage.WorkerContainer = "funnel-worker"
	c.Kubernetes.Storage.ExecutorContainer = "task-runner"
	addVolume(c, "cache", "shared-cache")
	addMount(c, "cache", "/cache", map[string]interface{}{"readOnly": true, "subPath": "cache"})
	p, err := Resolve(&tes.Task{Id: "task-123"}, c)
	if err != nil {
		t.Fatal(err)
	}
	worker := testJob()
	worker.Spec.Template.Spec.Containers = []corev1.Container{{Name: "sidecar"}, {Name: "funnel-worker"}}
	if err := p.ApplyWorker(worker, "/work", "task-123"); err != nil {
		t.Fatal(err)
	}
	if len(worker.Spec.Template.Spec.Containers[0].VolumeMounts) != 0 || len(worker.Spec.Template.Spec.Containers[0].Env) != 0 {
		t.Fatal("worker sidecar received storage")
	}
	snapshot, err := Decode(worker.Annotations[Annotation])
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.WorkerContainer != "funnel-worker" || snapshot.ExecutorContainer != "task-runner" {
		t.Fatalf("container selection was not persisted: %+v", snapshot)
	}
	executor := testJob()
	executor.Spec.Template.Spec.Containers = []corev1.Container{{Name: "sidecar"}, {Name: "task-runner"}}
	if err := snapshot.ApplyExecutor(executor, "task-123", []corev1.VolumeMount{{MountPath: "/inputs"}}); err != nil {
		t.Fatal(err)
	}
	if len(executor.Spec.Template.Spec.Containers[0].VolumeMounts) != 0 || len(executor.Spec.Template.Spec.Containers[1].VolumeMounts) != 1 {
		t.Fatal("executor mounts were applied to the wrong container")
	}
}

func TestMultipleContainersRequireNamedStorageContainers(t *testing.T) {
	p, err := Resolve(&tes.Task{Id: "task-123"}, existingConfig())
	if err != nil {
		t.Fatal(err)
	}
	worker := testJob()
	worker.Spec.Template.Spec.Containers = []corev1.Container{{Name: "sidecar"}, {Name: "worker"}}
	if err := p.ApplyWorker(worker, "/work", "task-123"); err == nil || !strings.Contains(err.Error(), "Kubernetes.Storage.WorkerContainer") {
		t.Fatalf("multiple worker containers without selection accepted: %v", err)
	}
	executor := testJob()
	executor.Spec.Template.Spec.Containers = []corev1.Container{{Name: "sidecar"}, {Name: "runner"}}
	if err := p.ApplyExecutor(executor, "task-123", []corev1.VolumeMount{{MountPath: "/inputs"}}); err == nil || !strings.Contains(err.Error(), "Kubernetes.Storage.ExecutorContainer") {
		t.Fatalf("multiple executor containers without selection accepted: %v", err)
	}
	c := existingConfig()
	c.Kubernetes.Storage.WorkerContainer = "missing"
	p, err = Resolve(&tes.Task{Id: "task-123"}, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ApplyWorker(testJob(), "/work", "task-123"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unknown worker container accepted: %v", err)
	}
	c = existingConfig()
	c.Kubernetes.Storage.ExecutorContainer = "missing"
	p, err = Resolve(&tes.Task{Id: "task-123"}, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ApplyExecutor(testJob(), "task-123", nil); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unknown executor container accepted: %v", err)
	}
}

func TestNativeTemplatesPreservedAndExplicitlyLinked(t *testing.T) {
	for _, pair := range []bool{false, true} {
		t.Run(map[bool]string{false: "PVC only", true: "PV pair"}[pair], func(t *testing.T) {
			c := existingConfig()
			c.Kubernetes.Storage.Volumes = nil
			claimText := pvcTemplate
			if pair {
				claimText = pairedPVC
				c.Kubernetes.Storage.PersistentVolumeTemplates = []*structpb.Struct{nativeYAML(t, pvTemplate)}
			}
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, claimText)}
			p, err := Resolve(&tes.Task{Id: "task-123"}, c)
			if err != nil {
				t.Fatal(err)
			}
			claim := p.Claims[0]
			if claim.Name != "task-123-workspace" || claim.Namespace != "jobs" || claim.Spec.StorageClassName == nil || *claim.Spec.StorageClassName != "fast" || claim.Spec.Resources.Requests.Storage().String() != "5Gi" || claim.Spec.AccessModes[0] != corev1.ReadWriteMany {
				t.Fatalf("native PVC fields changed: %+v", claim)
			}
			if len(p.Volumes) != 1 || p.Volumes[0].Name != "workspace" || p.Volumes[0].PersistentVolumeClaim.ClaimName != claim.Name {
				t.Fatalf("PVC template did not create its logical volume: %+v", p.Volumes)
			}
			if pair {
				pv := p.PersistentVolumes[0]
				if pv.Spec.CSI.Driver != "example.csi" || pv.Spec.CSI.VolumeAttributes["bucket"] != "native-bucket" || pv.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
					t.Fatal("native PV fields changed")
				}
				if claim.Spec.VolumeName != pv.Name || pv.Spec.ClaimRef.Name != claim.Name || pv.Spec.ClaimRef.Namespace != "jobs" {
					t.Fatal("explicit PV/PVC link or claimRef changed")
				}
			} else if len(p.PersistentVolumes) != 0 || claim.Spec.VolumeName != "" {
				t.Fatal("PVC-only configuration acquired a PV")
			}
			snapshot, err := p.Encode()
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := Decode(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if len(decoded.Claims) != 0 || len(decoded.PersistentVolumes) != 0 || !reflect.DeepEqual(p.OwnedClaims, decoded.OwnedClaims) || !reflect.DeepEqual(p.OwnedVolumes, decoded.OwnedVolumes) {
				t.Fatal("snapshot changed ownership or retained create objects")
			}
		})
	}
}

func TestClaimTemplateDefaultsAndDeterministicNames(t *testing.T) {
	c := existingConfig()
	c.Kubernetes.Storage.Volumes = nil
	raw := strings.Replace(pvcTemplate, "apiVersion: v1\n", "", 1)
	raw = strings.Replace(raw, "kind: PersistentVolumeClaim\n", "", 1)
	c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, raw)}

	first, err := Resolve(&tes.Task{Id: "task-123"}, c)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Resolve(&tes.Task{Id: "task-123"}, c)
	if err != nil {
		t.Fatal(err)
	}
	if first.Claims[0].APIVersion != "v1" || first.Claims[0].Kind != "PersistentVolumeClaim" {
		t.Fatalf("omitted PVC type metadata was not defaulted: %+v", first.Claims[0].TypeMeta)
	}
	if first.Claims[0].Name != "task-123-workspace" || !reflect.DeepEqual(first.OwnedClaims, second.OwnedClaims) || !reflect.DeepEqual(first.Volumes, second.Volumes) {
		t.Fatalf("PVC names changed across retries: first=%+v second=%+v", first, second)
	}
}

func TestGeneratedClaimNameMustFitDNSSubdomain(t *testing.T) {
	c := existingConfig()
	c.Kubernetes.Storage.Volumes = nil
	c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, pvcTemplate)}
	taskID := strings.Join([]string{
		strings.Repeat("a", 63),
		strings.Repeat("b", 63),
		strings.Repeat("c", 63),
		strings.Repeat("d", 61),
	}, ".")
	if len(taskID) != 253 {
		t.Fatalf("test task ID length = %d, want 253", len(taskID))
	}
	if _, err := Resolve(&tes.Task{Id: taskID}, c); err == nil || !strings.Contains(err.Error(), "generated PVC name") {
		t.Fatalf("overlong generated PVC name accepted: %v", err)
	}
}

func TestSharedPVCAndOperatorPVReferences(t *testing.T) {
	t.Run("logical volume may have multiple mounts", func(t *testing.T) {
		c := existingConfig()
		c.Kubernetes.Storage.Volumes = nil
		c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, pvcTemplate)}
		addMount(c, "workspace", "/workspace-view-a", nil)
		addMount(c, "workspace", "/workspace-view-b", map[string]interface{}{"readOnly": true})
		p, err := Resolve(&tes.Task{Id: "task-123"}, c)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Claims) != 1 || len(p.OwnedClaims) != 1 || len(p.Volumes) != 1 || p.Volumes[0].Name != "workspace" || len(p.WorkerMounts) != 2 || p.WorkerMounts[0].Name != p.Volumes[0].Name || p.Volumes[0].PersistentVolumeClaim.ClaimName != "task-123-workspace" {
			t.Fatalf("logical PVC volume or mounts changed: %+v", p)
		}
	})
	t.Run("PVC may name operator-managed PV", func(t *testing.T) {
		c := existingConfig()
		c.Kubernetes.Storage.Volumes = nil
		claim := strings.Replace(pvcTemplate, "  accessModes:", "  volumeName: operator-disk\n  accessModes:", 1)
		c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, claim)}
		p, err := Resolve(&tes.Task{Id: "task-123"}, c)
		if err != nil {
			t.Fatal(err)
		}
		if p.Claims[0].Spec.VolumeName != "operator-disk" || len(p.PersistentVolumes) != 0 {
			t.Fatalf("operator PV reference changed: %+v", p.Claims[0])
		}
	})
}

func TestStrictNativeDecodingAndScalarRendering(t *testing.T) {
	c := existingConfig()
	c.Kubernetes.Storage.Volumes = []*structpb.Struct{nativeYAML(t, "name: workspace\npersistentVolumeClaim:\n  claimName: shared-{{.TaskId}}\n")}
	c.Kubernetes.Storage.VolumeMounts = []*structpb.Struct{nativeYAML(t, "name: workspace\nmountPath: /data/{{.TaskId}}\nreadOnly: true\n")}
	p, err := Resolve(&tes.Task{Id: "task-123"}, c)
	if err != nil {
		t.Fatal(err)
	}
	if p.Volumes[0].PersistentVolumeClaim.ClaimName != "shared-task-123" || p.WorkerMounts[0].MountPath != "/data/task-123" {
		t.Fatalf("scalar templates did not resolve: %+v %+v", p.Volumes, p.WorkerMounts)
	}
	cases := []struct {
		name   string
		mutate func(*testing.T, *config.KubernetesStorage)
	}{
		{"unknown volume field", func(t *testing.T, s *config.KubernetesStorage) {
			s.Volumes = []*structpb.Struct{nativeYAML(t, "name: workspace\nunknownField: true\npersistentVolumeClaim:\n  claimName: shared-data\n")}
		}},
		{"unknown mount field", func(t *testing.T, s *config.KubernetesStorage) {
			s.VolumeMounts = []*structpb.Struct{nativeYAML(t, "name: workspace\nmountPath: /data\nunknownField: true\n")}
		}},
		{"unknown PVC field", func(t *testing.T, s *config.KubernetesStorage) {
			s.Volumes = nil
			s.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, strings.Replace(pvcTemplate, "  accessModes:", "  unknownField: true\n  accessModes:", 1))}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := existingConfig()
			tc.mutate(t, bad.Kubernetes.Storage)
			if _, err := Resolve(&tes.Task{Id: "task-123"}, bad); err == nil {
				t.Fatal("unknown Kubernetes field accepted")
			}
		})
	}
}

func TestInvalidNativeStorageAndLinks(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, *config.Config)
	}{
		{"unknown workspace", func(_ *testing.T, c *config.Config) { c.Kubernetes.Storage.WorkspaceVolume = "missing" }},
		{"duplicate volume", func(_ *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = append(c.Kubernetes.Storage.Volumes, c.Kubernetes.Storage.Volumes[0])
		}},
		{"invalid claim name", func(_ *testing.T, c *config.Config) { setVolume(c, 0, "workspace", "../claim") }},
		{"non-PVC source", func(_ *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes[0] = object(map[string]interface{}{"name": "workspace", "hostPath": map[string]interface{}{"path": "/data"}})
		}},
		{"multiple volume sources", func(_ *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes[0] = object(map[string]interface{}{"name": "workspace", "persistentVolumeClaim": map[string]interface{}{"claimName": "shared-data"}, "hostPath": map[string]interface{}{"path": "/data"}})
		}},
		{"relative workdir", func(_ *testing.T, c *config.Config) { c.Worker.WorkDir = "relative" }},
		{"mount traversal", func(_ *testing.T, c *config.Config) { addMount(c, "workspace", "/data/../etc", nil) }},
		{"unknown mount volume", func(_ *testing.T, c *config.Config) { addMount(c, "missing", "/data", nil) }},
		{"workspace mount overlay", func(_ *testing.T, c *config.Config) { addMount(c, "workspace", "/work", nil) }},
		{"both subPath forms", func(_ *testing.T, c *config.Config) {
			addMount(c, "workspace", "/data", map[string]interface{}{"subPath": "a", "subPathExpr": "b"})
		}},
		{"block PVC", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, strings.Replace(pvcTemplate, "  accessModes:", "  volumeMode: Block\n  accessModes:", 1))}
		}},
		{"RWOP workspace", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, strings.Replace(pvcTemplate, "ReadWriteMany", "ReadWriteOncePod", 1))}
		}},
		{"read-only workspace PVC", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, strings.Replace(pvcTemplate, "ReadWriteMany", "ReadOnlyMany", 1))}
		}},
		{"foreign PVC namespace", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			raw := strings.Replace(pvcTemplate, "metadata:\n", "metadata:\n  namespace: foreign\n", 1)
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, raw)}
		}},
		{"wrong PVC kind", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, strings.Replace(pvcTemplate, "PersistentVolumeClaim", "Secret", 1))}
		}},
		{"PVC owner reference", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			raw := strings.Replace(pvcTemplate, "metadata:\n", "metadata:\n  ownerReferences:\n  - apiVersion: batch/v1\n    kind: Job\n    name: foreign\n    uid: foreign\n", 1)
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, raw)}
		}},
		{"task-templated logical name requires migration", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			raw := strings.Replace(pvcTemplate, "name: workspace", "name: claim-{{.TaskId}}", 1)
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, raw)}
		}},
		{"explicit volume collides with logical volume", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, pvcTemplate)}
		}},
		{"explicit volume cannot reference generated claim", func(t *testing.T, c *config.Config) {
			addVolume(c, "external-ref", "task-123-cache")
			raw := strings.Replace(pvcTemplate, "name: workspace", "name: cache", 1)
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, raw)}
		}},
		{"duplicate PVC template", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			item := nativeYAML(t, pvcTemplate)
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{item, item}
		}},
		{"conflicting PVC volumeName", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			raw := strings.Replace(pairedPVC, "disk-{{.TaskId}}", "other", 1)
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, raw)}
			c.Kubernetes.Storage.PersistentVolumeTemplates = []*structpb.Struct{nativeYAML(t, pvTemplate)}
		}},
		{"orphan PV template", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.PersistentVolumeTemplates = []*structpb.Struct{nativeYAML(t, pvTemplate)}
		}},
		{"PV without source", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, pairedPVC)}
			c.Kubernetes.Storage.PersistentVolumeTemplates = []*structpb.Struct{nativeYAML(t, "apiVersion: v1\nkind: PersistentVolume\nmetadata:\n  name: disk-{{.TaskId}}\nspec: {}\n")}
		}},
		{"multiple PV sources", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, pairedPVC)}
			c.Kubernetes.Storage.PersistentVolumeTemplates = []*structpb.Struct{nativeYAML(t, pvTemplate+"  hostPath:\n    path: /tmp/host\n")}
		}},
		{"PV owner reference", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, pairedPVC)}
			raw := strings.Replace(pvTemplate, "metadata:\n", "metadata:\n  ownerReferences:\n  - apiVersion: batch/v1\n    kind: Job\n    name: foreign\n    uid: foreign\n", 1)
			c.Kubernetes.Storage.PersistentVolumeTemplates = []*structpb.Struct{nativeYAML(t, raw)}
		}},
		{"block PV", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, pairedPVC)}
			c.Kubernetes.Storage.PersistentVolumeTemplates = []*structpb.Struct{nativeYAML(t, strings.Replace(pvTemplate, "  capacity:", "  volumeMode: Block\n  capacity:", 1))}
		}},
		{"foreign claimRef", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, pairedPVC)}
			raw := strings.Replace(pvTemplate, "spec:\n", "spec:\n  claimRef:\n    name: foreign\n", 1)
			c.Kubernetes.Storage.PersistentVolumeTemplates = []*structpb.Struct{nativeYAML(t, raw)}
		}},
		{"multiple PVCs point to one PV", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			other := strings.Replace(pairedPVC, "name: workspace", "name: cache", 1)
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, pairedPVC), nativeYAML(t, other)}
			c.Kubernetes.Storage.PersistentVolumeTemplates = []*structpb.Struct{nativeYAML(t, pvTemplate)}
		}},
		{"wrong PV kind", func(t *testing.T, c *config.Config) {
			c.Kubernetes.Storage.Volumes = nil
			c.Kubernetes.Storage.VolumeClaimTemplates = []*structpb.Struct{nativeYAML(t, pairedPVC)}
			c.Kubernetes.Storage.PersistentVolumeTemplates = []*structpb.Struct{nativeYAML(t, strings.Replace(pvTemplate, "PersistentVolume", "Secret", 1))}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := existingConfig()
			tt.change(t, c)
			if _, err := Resolve(&tes.Task{Id: "task-123"}, c); err == nil {
				t.Fatal("invalid native storage configuration accepted")
			}
		})
	}
}

func TestMountFailuresDoNotModifyJob(t *testing.T) {
	p, err := Resolve(&tes.Task{Id: "task-123"}, existingConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, mount := range []corev1.VolumeMount{{MountPath: "relative"}, {MountPath: "/a/../b"}, {MountPath: "/a", SubPath: "override"}} {
		job := testJob()
		before := job.DeepCopy()
		if err := p.ApplyExecutor(job, "task-123", []corev1.VolumeMount{mount}); err == nil {
			t.Fatal("bad executor mount accepted")
		}
		if !reflect.DeepEqual(job, before) {
			t.Fatal("failed application modified job")
		}
	}
	job := testJob()
	job.Spec.Template.Spec.Volumes = []corev1.Volume{{Name: "workspace"}}
	before := job.DeepCopy()
	if err := p.ApplyWorker(job, "/work", "task-123"); err == nil {
		t.Fatal("volume collision accepted")
	}
	if !reflect.DeepEqual(job, before) {
		t.Fatal("failed application modified job")
	}
	if err := p.ApplyWorker(testJob(), "/different", "task-123"); err == nil {
		t.Fatal("different work directory accepted")
	}
	if err := p.ApplyExecutor(testJob(), "other-task", nil); err == nil {
		t.Fatal("different task accepted")
	}
}

func TestInvalidSnapshot(t *testing.T) {
	p, err := Resolve(&tes.Task{Id: "task-123"}, existingConfig())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	invalid := []string{
		"null", "{}", snapshot + "{}",
		strings.Replace(snapshot, "\"Version\":4", "\"Version\":3", 1),
		strings.Replace(snapshot, "\"Version\":4", "\"Version\":5", 1),
		strings.Replace(snapshot, "\"WorkspaceVolume\":\"workspace\"", "\"WorkspaceVolume\":\"missing\"", 1),
		strings.Replace(snapshot, "\"OwnedClaims\":null", "\"OwnedClaims\":[\"unmounted\"]", 1),
	}
	for _, value := range invalid {
		if _, err := Decode(value); err == nil {
			t.Fatalf("invalid snapshot accepted: %s", value)
		}
	}
	if _, err := Decode(strings.Replace(snapshot, "\"Version\":4", "\"Version\":3", 1)); err == nil || !strings.Contains(err.Error(), "version 4 is required") {
		t.Fatalf("version 3 snapshot was not rejected clearly: %v", err)
	}
}

func TestLegacyResolveAndDottedTaskID(t *testing.T) {
	if p, err := Resolve(&tes.Task{Id: "task-123"}, config.DefaultConfig()); err != nil || p != nil {
		t.Fatalf("legacy config produced a plan: %+v %v", p, err)
	}
	if _, err := Resolve(&tes.Task{Id: "task.123"}, existingConfig()); err != nil {
		t.Fatalf("valid dotted task ID rejected: %v", err)
	}
}

func testJob() *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "task-123", Namespace: "jobs"},
		Spec:       batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main"}}}}},
	}
}
