package storageplan

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/funnel/storage"
	"github.com/calypr/funnel/tes"
	corev1 "k8s.io/api/core/v1"
)

type previousVersion4Plan struct {
	Version           int
	TaskID            string
	Namespace         string
	WorkDir           string
	WorkspaceVolume   string
	WorkerContainer   string
	ExecutorContainer string
	Volumes           []corev1.Volume
	WorkerMounts      []corev1.VolumeMount
	OwnedClaims       []string
	OwnedVolumes      []string
}

func decodeWithPreviousVersion4Schema(snapshot string) error {
	var plan previousVersion4Plan
	decoder := json.NewDecoder(strings.NewReader(snapshot))
	decoder.DisallowUnknownFields()
	return decoder.Decode(&plan)
}

func TestNewVersion4SnapshotsWithoutS3RoutesRemainCompatible(t *testing.T) {
	for _, tc := range []struct {
		name      string
		nilRoutes bool
	}{
		{name: "nil routes", nilRoutes: true},
		{name: "empty routes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := Resolve(&tes.Task{Id: "task-123"}, existingConfig())
			if err != nil {
				t.Fatal(err)
			}
			if tc.nilRoutes {
				plan.S3ReadMounts = nil
				plan.S3WriteMounts = nil
			} else {
				plan.S3ReadMounts = []storage.S3ReadMount{}
				plan.S3WriteMounts = []storage.S3WriteMount{}
			}
			snapshot, err := plan.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if err := decodeWithPreviousVersion4Schema(snapshot); err != nil {
				t.Fatalf("previous v4 worker decoder rejected encoded plan: %v", err)
			}
			if _, err := Decode(snapshot); err != nil {
				t.Fatalf("current decoder rejected encoded plan: %v", err)
			}
		})
	}
}

func TestVersion4SnapshotSurvivesNativeConfigRefactor(t *testing.T) {
	// Version 4 records resolved mounts, not the configuration's former mount aliases.
	snapshot := `{
  "Version":4,"TaskID":"task-a","Namespace":"tasks","WorkDir":"/work",
  "WorkspaceVolume":"workspace","WorkerContainer":"","ExecutorContainer":"",
  "S3ReadMounts":[{"Endpoint":"https://s3.example.org","Bucket":"data","Prefix":"in/","Root":"/reference"}],
  "S3WriteMounts":[{"Endpoint":"https://s3.example.org","Bucket":"data","Prefix":"out/","Root":"/results","Timeout":10800000000000}],
  "Volumes":[{"name":"workspace","persistentVolumeClaim":{"claimName":"work-task-a"}}],
  "WorkerMounts":[
   {"name":"workspace","mountPath":"/reference","subPath":"in","readOnly":true},
   {"name":"workspace","mountPath":"/results","subPath":"out"}
  ],
  "OwnedClaims":["work-task-a"],"OwnedVolumes":["data-task-a"]
 }`
	plan, err := Decode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != 4 || plan.S3ReadMounts[0].Root != "/reference" || plan.S3WriteMounts[0].Root != "/results" {
		t.Fatalf("existing snapshot mount selection changed: %+v", plan)
	}
	if len(plan.OwnedClaims) != 1 || plan.OwnedClaims[0] != "work-task-a" || len(plan.OwnedVolumes) != 1 || plan.OwnedVolumes[0] != "data-task-a" {
		t.Fatalf("existing cleanup ownership changed: %+v", plan)
	}
	encoded, err := plan.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["S3ReadMounts"]; !ok {
		t.Fatalf("nonempty S3 read mounts were omitted from snapshot: %s", encoded)
	}
	if _, ok := fields["S3WriteMounts"]; !ok {
		t.Fatalf("nonempty S3 write mounts were omitted from snapshot: %s", encoded)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.S3ReadMounts) != 1 || decoded.S3ReadMounts[0] != plan.S3ReadMounts[0] || len(decoded.S3WriteMounts) != 1 || decoded.S3WriteMounts[0] != plan.S3WriteMounts[0] {
		t.Fatalf("current round trip changed nonempty S3 mounts: %+v", decoded)
	}
	job := testJob()
	job.Namespace = "tasks"
	job.Name = "task-a"
	if err := plan.ApplyWorker(job, "/work", "task-a"); err != nil {
		t.Fatal(err)
	}
	if len(job.Spec.Template.Spec.Containers[0].VolumeMounts) != 3 {
		t.Fatalf("existing worker views lost: %+v", job.Spec.Template.Spec.Containers[0].VolumeMounts)
	}
}
