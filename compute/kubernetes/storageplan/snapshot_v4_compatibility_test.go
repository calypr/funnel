package storageplan

import "testing"

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
