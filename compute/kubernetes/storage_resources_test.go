package kubernetes

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ohsu-comp-bio/funnel/compute/kubernetes/storageplan"
	"github.com/ohsu-comp-bio/funnel/config"
	"google.golang.org/protobuf/types/known/structpb"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	k8stesting "k8s.io/client-go/testing"
)

func TestReleaseStorageFinalizer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		uid       types.UID
		conflict  bool
		wantError bool
	}{
		{name: "release", uid: "job-uid"},
		{name: "retry conflict", uid: "job-uid", conflict: true},
		{name: "replacement Job", uid: "other-job", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "task", Namespace: "jobs", UID: tc.uid, Finalizers: []string{"external/finalizer", storageplan.Finalizer}}}
			client := fake.NewSimpleClientset(job)
			updates := 0
			client.PrependReactor("update", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
				updates++
				if tc.conflict && updates == 1 {
					concurrent := job.DeepCopy()
					concurrent.Finalizers = append(concurrent.Finalizers, "concurrent/finalizer")
					if err := client.Tracker().Update(batchv1.SchemeGroupVersion.WithResource("jobs"), concurrent, "jobs"); err != nil {
						t.Fatal(err)
					}
					return true, nil, apierrors.NewConflict(batchv1.Resource("jobs"), job.Name, errors.New("concurrent update"))
				}
				return false, nil, nil
			})
			err := (&Backend{client: client}).releaseStorageFinalizer(context.Background(), "task", "jobs", "job-uid")
			if (err != nil) != tc.wantError {
				t.Fatalf("got error %v, wantError %v", err, tc.wantError)
			}
			actual, err := client.BatchV1().Jobs("jobs").Get(context.Background(), "task", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"external/finalizer"}
			if tc.conflict {
				want = append(want, "concurrent/finalizer")
			}
			if tc.wantError {
				want = job.Finalizers
				if updates != 0 {
					t.Fatal("updated replacement Job")
				}
			}
			if !reflect.DeepEqual(actual.Finalizers, want) {
				t.Fatalf("finalizers got %v, want %v", actual.Finalizers, want)
			}
			if tc.conflict && updates != 2 {
				t.Fatalf("got %d updates, want conflict retry", updates)
			}
			if !tc.wantError {
				beforeRetry := updates
				if err := (&Backend{client: client}).releaseStorageFinalizer(context.Background(), "task", "jobs", "job-uid"); err != nil {
					t.Fatal(err)
				}
				if updates != beforeRetry {
					t.Fatal("updated Job after finalizer was already released")
				}
			}
		})
	}
	if err := (&Backend{client: fake.NewSimpleClientset()}).releaseStorageFinalizer(context.Background(), "missing", "jobs", "job-uid"); err != nil {
		t.Fatalf("missing Job should be idempotent: %v", err)
	}
}

func TestValidateExistingStorage(t *testing.T) {
	block := corev1.PersistentVolumeBlock
	for _, tc := range []struct {
		name       string
		mode       []corev1.PersistentVolumeAccessMode
		volumeMode *corev1.PersistentVolumeMode
		namespace  string
		missing    bool
		wantError  bool
	}{
		{name: "read write once", mode: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}},
		{name: "read write many", mode: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}},
		{name: "read only workspace", mode: []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany}, wantError: true},
		{name: "single pod workspace", mode: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOncePod}, wantError: true},
		{name: "mixed single pod workspace", mode: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany, corev1.ReadWriteOncePod}, wantError: true},
		{name: "block workspace", mode: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}, volumeMode: &block, wantError: true},
		{name: "missing workspace", missing: true, wantError: true},
		{name: "wrong namespace", namespace: "elsewhere", mode: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := &storageplan.Plan{Namespace: "jobs", WorkspaceVolume: "workspace", OwnedClaims: []string{"created"}, Volumes: []corev1.Volume{
				{Name: "workspace", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "shared"}}},
				{Name: "inputs", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data", ReadOnly: true}}},
				{Name: "owned", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "created"}}},
			}}
			claimNamespace := "jobs"
			if tc.namespace != "" {
				claimNamespace = tc.namespace
			}
			shared := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: claimNamespace}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: tc.mode, VolumeMode: tc.volumeMode}}
			data := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "jobs"}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany}}}
			client := fake.NewSimpleClientset(data)
			if !tc.missing {
				if _, err := client.CoreV1().PersistentVolumeClaims(claimNamespace).Create(context.Background(), shared, metav1.CreateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			err := (&Backend{client: client}).validateExistingStorage(context.Background(), plan)
			if (err != nil) != tc.wantError {
				t.Fatalf("got error %v, wantError %v", err, tc.wantError)
			}
			for _, action := range client.Actions() {
				if action.GetVerb() == "get" && action.(k8stesting.GetAction).GetName() == "created" {
					t.Fatal("looked up not-yet-created owned claim")
				}
				if action.GetVerb() == "update" || action.GetVerb() == "delete" || action.GetVerb() == "patch" {
					t.Fatalf("validation mutated storage: %v", action)
				}
			}
		})
	}
}

func boundStorageFixture() (*storageplan.Plan, metav1.OwnerReference) {
	controller := true
	owner := metav1.OwnerReference{APIVersion: "batch/v1", Kind: "Job", Name: "worker-task", UID: "job-uid", Controller: &controller}
	plan := &storageplan.Plan{
		PersistentVolumes: []*corev1.PersistentVolume{{
			ObjectMeta: metav1.ObjectMeta{Name: "pv-task", Labels: map[string]string{"custom": "kept"}},
			Spec: corev1.PersistentVolumeSpec{
				Capacity:               corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
				AccessModes:            []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				PersistentVolumeSource: corev1.PersistentVolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/data/task"}},
				ClaimRef:               &corev1.ObjectReference{Name: "pvc-task", Namespace: "jobs"},
			},
		}},
		Claims: []*corev1.PersistentVolumeClaim{{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-task", Namespace: "jobs"},
			Spec: corev1.PersistentVolumeClaimSpec{
				VolumeName: "pv-task", AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")}},
			},
		}},
		OwnedClaims: []string{"pvc-task"}, OwnedVolumes: []string{"pv-task"},
	}
	return plan, owner
}

func boundStorageJob(owner metav1.OwnerReference) *batchv1.Job {
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: owner.Name, Namespace: "jobs", UID: owner.UID}}
}

type storageCleanupContextClient struct {
	k8sclient.Interface
	t *testing.T
}

func (c storageCleanupContextClient) CoreV1() coreclient.CoreV1Interface {
	return storageCleanupContextCore{CoreV1Interface: c.Interface.CoreV1(), t: c.t}
}

type storageCleanupContextCore struct {
	coreclient.CoreV1Interface
	t *testing.T
}

func (c storageCleanupContextCore) PersistentVolumes() coreclient.PersistentVolumeInterface {
	return storageCleanupContextVolumes{PersistentVolumeInterface: c.CoreV1Interface.PersistentVolumes(), t: c.t}
}

type storageCleanupContextVolumes struct {
	coreclient.PersistentVolumeInterface
	t *testing.T
}

func (c storageCleanupContextVolumes) Delete(ctx context.Context, name string, options metav1.DeleteOptions) error {
	if ctx.Err() != nil {
		c.t.Fatalf("compensation reused canceled submission context: %v", ctx.Err())
	}
	if _, bounded := ctx.Deadline(); !bounded {
		c.t.Fatal("compensation requires a bounded cleanup context")
	}
	return c.PersistentVolumeInterface.Delete(ctx, name, options)
}

func TestCreateBoundStorageStopsWhenOwnerGone(t *testing.T) {
	for _, state := range []string{"missing", "replacement", "deleting"} {
		t.Run(state, func(t *testing.T) {
			plan, owner := boundStorageFixture()
			client := fake.NewSimpleClientset()
			if state != "missing" {
				job := boundStorageJob(owner)
				if state == "replacement" {
					job.UID = "replacement-job"
				} else {
					now := metav1.Now()
					job.DeletionTimestamp = &now
				}
				if err := client.Tracker().Create(batchv1.SchemeGroupVersion.WithResource("jobs"), job, "jobs"); err != nil {
					t.Fatal(err)
				}
			}
			if err := (&Backend{client: client}).createBoundStorage(context.Background(), "task", "jobs", plan, owner); err == nil {
				t.Fatal("created storage after the owner Job was gone")
			}
			for _, action := range client.Actions() {
				if action.GetResource().Resource != "jobs" {
					t.Fatalf("used storage API after the owner Job was gone: %v", action)
				}
			}
		})
	}
}

func TestCreateBoundStorageCompensatesAfterOwnerGone(t *testing.T) {
	for _, resource := range []string{"persistentvolumes", "persistentvolumeclaims"} {
		for _, state := range []string{"missing", "replacement", "deleting", "cleanup failure", "canceled context"} {
			t.Run(resource+"/"+state, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				plan, owner := boundStorageFixture()
				job := boundStorageJob(owner)
				client := fake.NewSimpleClientset(job)
				client.PrependReactor("create", "persistentvolumes", func(action k8stesting.Action) (bool, runtime.Object, error) {
					pv := action.(k8stesting.CreateAction).GetObject().(*corev1.PersistentVolume)
					pv.UID = "created-volume-uid"
					pv.Finalizers = []string{"kubernetes.io/pv-protection"}
					return false, nil, nil
				})
				client.PrependReactor("create", resource, func(k8stesting.Action) (bool, runtime.Object, error) {
					jobs := batchv1.SchemeGroupVersion.WithResource("jobs")
					if state == "missing" || state == "cleanup failure" || state == "canceled context" {
						if err := client.Tracker().Delete(jobs, "jobs", owner.Name); err != nil {
							t.Fatal(err)
						}
						if state == "canceled context" {
							cancel()
						}
					} else {
						changed := job.DeepCopy()
						if state == "replacement" {
							changed.UID = "replacement-job"
						} else {
							now := metav1.Now()
							changed.DeletionTimestamp = &now
						}
						if err := client.Tracker().Update(jobs, changed, "jobs"); err != nil {
							t.Fatal(err)
						}
					}
					return false, nil, nil
				})
				cleanupErr := errors.New("cleanup API unavailable")
				deletes := 0
				client.PrependReactor("delete", "persistentvolumes", func(action k8stesting.Action) (bool, runtime.Object, error) {
					deletes++
					options := action.(k8stesting.DeleteAction).GetDeleteOptions()
					if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != "created-volume-uid" {
						t.Fatalf("compensation lacks the volume UID precondition: %+v", options)
					}
					object, err := client.Tracker().Get(corev1.SchemeGroupVersion.WithResource("persistentvolumes"), "", "pv-task")
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(object.(*corev1.PersistentVolume).Finalizers, []string{"kubernetes.io/pv-protection"}) {
						t.Fatal("compensation removed native PV finalizers")
					}
					if state == "cleanup failure" {
						return true, nil, cleanupErr
					}
					return false, nil, nil
				})
				var backendClient k8sclient.Interface = client
				if state == "canceled context" {
					backendClient = storageCleanupContextClient{Interface: client, t: t}
				}
				err := (&Backend{client: backendClient}).createBoundStorage(ctx, "task", "jobs", plan, owner)
				if err == nil || !strings.Contains(err.Error(), "worker Job") {
					t.Fatalf("expected an owner Job error, got %v", err)
				}
				if deletes != 1 {
					t.Fatalf("got %d compensating deletions, want 1", deletes)
				}
				if state == "cleanup failure" {
					if !errors.Is(err, cleanupErr) {
						t.Fatalf("cleanup failure was lost: %v", err)
					}
				} else if _, err := client.CoreV1().PersistentVolumes().Get(context.Background(), "pv-task", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
					t.Fatalf("late-created PV survived compensation: %v", err)
				}
				if resource == "persistentvolumes" {
					for _, action := range client.Actions() {
						if action.GetVerb() == "create" && action.GetResource().Resource == "persistentvolumeclaims" {
							t.Fatal("continued creating claims after the owner Job was gone")
						}
					}
				}
				if state == "replacement" {
					actual, err := client.BatchV1().Jobs("jobs").Get(context.Background(), owner.Name, metav1.GetOptions{})
					if err != nil || actual.UID != "replacement-job" {
						t.Fatalf("replacement Job was modified: %v %v", actual, err)
					}
				}
			})
		}
	}
}

func TestCreateBoundStorageOwnsPairWithoutMutatingPlan(t *testing.T) {
	ctx := context.Background()
	plan, owner := boundStorageFixture()
	pvBefore, claimBefore := plan.PersistentVolumes[0].DeepCopy(), plan.Claims[0].DeepCopy()
	client := fake.NewSimpleClientset(boundStorageJob(owner))
	b := &Backend{client: client}
	if err := b.createBoundStorage(ctx, "task", "jobs", plan, owner); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.PersistentVolumes[0], pvBefore) || !reflect.DeepEqual(plan.Claims[0], claimBefore) {
		t.Fatal("creation mutated resolved storage plan")
	}
	pv, err := client.CoreV1().PersistentVolumes().Get(ctx, "pv-task", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := client.CoreV1().PersistentVolumeClaims("jobs").Get(ctx, "pvc-task", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !boundStorageIdentity(pv.ObjectMeta, "task", "jobs", owner) || len(pv.OwnerReferences) != 0 || pv.Labels["custom"] != "kept" {
		t.Fatalf("unexpected PV ownership: %+v", pv.ObjectMeta)
	}
	if !boundStorageIdentity(claim.ObjectMeta, "task", "jobs", owner) || !reflect.DeepEqual(claim.OwnerReferences, []metav1.OwnerReference{owner}) {
		t.Fatalf("unexpected PVC ownership: %+v", claim.ObjectMeta)
	}
	var created []string
	for _, action := range client.Actions() {
		if action.GetVerb() == "create" {
			created = append(created, action.GetResource().Resource)
		}
	}
	if !reflect.DeepEqual(created, []string{"persistentvolumes", "persistentvolumeclaims"}) {
		t.Fatalf("creation order: %v", created)
	}
	if err := b.createBoundStorage(ctx, "task", "jobs", plan, owner); err != nil {
		t.Fatalf("owned creation retry failed: %v", err)
	}
}

func TestCreateBoundStorageRejectsCollisions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*corev1.PersistentVolume, *corev1.PersistentVolumeClaim)
	}{
		{"external volume", func(pv *corev1.PersistentVolume, _ *corev1.PersistentVolumeClaim) { pv.Labels = nil }},
		{"different task", func(pv *corev1.PersistentVolume, _ *corev1.PersistentVolumeClaim) { pv.Labels["taskId"] = "other" }},
		{"replacement job", func(pv *corev1.PersistentVolume, _ *corev1.PersistentVolumeClaim) {
			pv.Annotations[storageOwnerUIDAnnotation] = "old-job"
		}},
		{"changed source", func(pv *corev1.PersistentVolume, _ *corev1.PersistentVolumeClaim) {
			pv.Spec.HostPath.Path = "/external"
		}},
		{"foreign binding", func(pv *corev1.PersistentVolume, _ *corev1.PersistentVolumeClaim) { pv.Spec.ClaimRef.Name = "external" }},
		{"external claim", func(_ *corev1.PersistentVolume, claim *corev1.PersistentVolumeClaim) { claim.Labels = nil }},
		{"foreign claim owner", func(_ *corev1.PersistentVolume, claim *corev1.PersistentVolumeClaim) {
			claim.OwnerReferences[0].UID = "other-job"
		}},
		{"changed request", func(_ *corev1.PersistentVolume, claim *corev1.PersistentVolumeClaim) {
			claim.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("2Gi")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, owner := boundStorageFixture()
			pv, claim := plan.PersistentVolumes[0].DeepCopy(), plan.Claims[0].DeepCopy()
			markBoundStorage(&pv.ObjectMeta, "task", "jobs", owner)
			markBoundStorage(&claim.ObjectMeta, "task", "jobs", owner)
			claim.OwnerReferences = []metav1.OwnerReference{owner}
			tc.change(pv, claim)
			client := fake.NewSimpleClientset(pv, claim, boundStorageJob(owner))
			b := &Backend{client: client}
			if err := b.createBoundStorage(context.Background(), "task", "jobs", plan, owner); err == nil {
				t.Fatal("accepted conflicting existing resource")
			}
			for _, action := range client.Actions() {
				if action.GetVerb() == "update" || action.GetVerb() == "patch" || action.GetVerb() == "delete" {
					t.Fatalf("modified existing resource: %v", action)
				}
			}
		})
	}
}

func TestCreateBoundStorageRetryAfterBindingDefaults(t *testing.T) {
	plan, owner := boundStorageFixture()
	pv, claim := plan.PersistentVolumes[0].DeepCopy(), plan.Claims[0].DeepCopy()
	markBoundStorage(&pv.ObjectMeta, "task", "jobs", owner)
	markBoundStorage(&claim.ObjectMeta, "task", "jobs", owner)
	claim.OwnerReferences = []metav1.OwnerReference{owner}
	mode, class := corev1.PersistentVolumeFilesystem, "default-class"
	pv.Spec.VolumeMode, claim.Spec.VolumeMode = &mode, &mode
	pv.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimRetain
	hostPathType := corev1.HostPathUnset
	pv.Spec.HostPath.Type = &hostPathType
	pv.Spec.ClaimRef.UID, pv.Spec.ClaimRef.ResourceVersion = "claim-uid", "123"
	pv.Spec.ClaimRef.APIVersion, pv.Spec.ClaimRef.Kind = "v1", "PersistentVolumeClaim"
	claim.Spec.StorageClassName = &class
	b := &Backend{client: fake.NewSimpleClientset(pv, claim, boundStorageJob(owner))}
	if err := b.createBoundStorage(context.Background(), "task", "jobs", plan, owner); err != nil {
		t.Fatal(err)
	}
}

func TestCreateBoundStorageDynamicClaimSkipsPersistentVolumes(t *testing.T) {
	plan, owner := boundStorageFixture()
	plan.PersistentVolumes, plan.OwnedVolumes = nil, nil
	plan.Claims[0].Spec.VolumeName = ""
	client := fake.NewSimpleClientset(boundStorageJob(owner))
	b := &Backend{client: client}
	if err := b.createBoundStorage(context.Background(), "task", "jobs", plan, owner); err != nil {
		t.Fatal(err)
	}
	claim, err := client.CoreV1().PersistentVolumeClaims("jobs").Get(context.Background(), "pvc-task", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(claim.OwnerReferences, []metav1.OwnerReference{owner}) || claim.Spec.VolumeName != "" {
		t.Fatalf("unexpected dynamic claim: %+v", claim)
	}
	for _, action := range client.Actions() {
		if action.GetResource().Resource == "persistentvolumes" {
			t.Fatalf("dynamic claim used cluster-scoped PV API: %v", action)
		}
	}
}

func TestDeleteBoundVolumesOnlyDeletesOwnedRecordedNames(t *testing.T) {
	ctx := context.Background()
	plan, owner := boundStorageFixture()
	owned := plan.PersistentVolumes[0].DeepCopy()
	markBoundStorage(&owned.ObjectMeta, "task", "jobs", owner)
	owned.UID, owned.Finalizers = "volume-uid", []string{"kubernetes.io/pv-protection"}
	external := owned.DeepCopy()
	external.Name, external.Labels = "external", nil
	otherTask := owned.DeepCopy()
	otherTask.Name, otherTask.Labels["taskId"] = "other-task", "other"
	otherNamespace := owned.DeepCopy()
	otherNamespace.Name, otherNamespace.Labels["namespace"] = "other-namespace", "elsewhere"
	unrecorded := owned.DeepCopy()
	unrecorded.Name = "unrecorded"
	client := fake.NewSimpleClientset(owned, external, otherTask, otherNamespace, unrecorded)
	deletes := 0
	client.PrependReactor("delete", "persistentvolumes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		deletes++
		deleteAction := action.(k8stesting.DeleteAction)
		if deleteAction.GetName() != owned.Name {
			t.Fatalf("deleted unexpected PV %q", deleteAction.GetName())
		}
		options := deleteAction.GetDeleteOptions()
		if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != owned.UID {
			t.Fatalf("missing UID precondition: %+v", options)
		}
		current, err := client.Tracker().Get(corev1.SchemeGroupVersion.WithResource("persistentvolumes"), "", owned.Name)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(current.(*corev1.PersistentVolume).Finalizers, owned.Finalizers) {
			t.Fatal("cleanup removed PV finalizers")
		}
		return false, nil, nil
	})
	b := &Backend{client: client}
	if err := b.deleteBoundVolumes(ctx, "task", "jobs", []string{owned.Name, external.Name, otherTask.Name, otherNamespace.Name, "missing"}, owner.UID); err != nil {
		t.Fatal(err)
	}
	if deletes != 1 {
		t.Fatalf("got %d deletes, want 1", deletes)
	}
	for _, name := range []string{external.Name, otherTask.Name, otherNamespace.Name, unrecorded.Name} {
		if _, err := client.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{}); err != nil {
			t.Fatalf("PV %s should remain: %v", name, err)
		}
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "update" || action.GetVerb() == "patch" || action.GetVerb() == "list" {
			t.Fatalf("unexpected cleanup action: %v", action)
		}
	}
}

func TestDeleteBoundVolumesContinuesAfterFailure(t *testing.T) {
	plan, owner := boundStorageFixture()
	pv := plan.PersistentVolumes[0].DeepCopy()
	markBoundStorage(&pv.ObjectMeta, "task", "jobs", owner)
	second := pv.DeepCopy()
	second.Name = "pv-second"
	client := fake.NewSimpleClientset(pv, second)
	client.PrependReactor("delete", "persistentvolumes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.(k8stesting.DeleteAction).GetName() == pv.Name {
			return true, nil, apierrors.NewConflict(corev1.Resource("persistentvolumes"), pv.Name, nil)
		}
		return false, nil, nil
	})
	b := &Backend{client: client}
	if err := b.deleteBoundVolumes(context.Background(), "task", "jobs", []string{pv.Name, second.Name}, owner.UID); err == nil {
		t.Fatal("expected deletion error")
	}
	if _, err := client.CoreV1().PersistentVolumes().Get(context.Background(), second.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("second PV not cleaned after first failed: %v", err)
	}
}

func TestDeleteBoundVolumesRejectsDifferentJobUID(t *testing.T) {
	plan, owner := boundStorageFixture()
	pv := plan.PersistentVolumes[0].DeepCopy()
	oldOwner := owner
	oldOwner.UID = "older-job-uid"
	markBoundStorage(&pv.ObjectMeta, "task", "jobs", oldOwner)
	client := fake.NewSimpleClientset(pv, boundStorageJob(owner))
	b := &Backend{client: client}
	if err := b.createBoundStorage(context.Background(), "task", "jobs", plan, owner); err == nil {
		t.Fatal("accepted PV from a different Job")
	}
	if err := b.deleteBoundVolumes(context.Background(), "task", "jobs", plan.OwnedVolumes, owner.UID); err != nil {
		t.Fatal(err)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "delete" {
			t.Fatal("rollback deleted PV belonging to a different Job")
		}
	}
}

func TestDeleteBoundVolumeUsesActualOrphanName(t *testing.T) {
	plan, owner := boundStorageFixture()
	pv := plan.PersistentVolumes[0].DeepCopy()
	pv.Name = "custom-template-volume-task"
	pv.UID = "orphan-volume-uid"
	markBoundStorage(&pv.ObjectMeta, "task", "jobs", owner)
	client := fake.NewSimpleClientset(pv)
	b := &Backend{client: client}
	if err := b.deleteBoundVolume(context.Background(), "task", "jobs", pv.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().PersistentVolumes().Get(context.Background(), pv.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("orphan PV with custom name was not deleted: %v", err)
	}
}

func TestBoundStorageRequiresJobUID(t *testing.T) {
	plan, owner := boundStorageFixture()
	owner.UID = types.UID("")
	client := fake.NewSimpleClientset()
	if err := (&Backend{client: client}).createBoundStorage(context.Background(), "task", "jobs", plan, owner); err == nil {
		t.Fatal("accepted missing worker UID")
	}
	if len(client.Actions()) != 0 {
		t.Fatal("made API calls without worker UID")
	}
}

func TestUsesPersistentVolumes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		storage *config.KubernetesStorage
		want    bool
	}{
		{"legacy", nil, true},
		{"existing claim", &config.KubernetesStorage{Volumes: []*structpb.Struct{{}}}, false},
		{"dynamic claim", &config.KubernetesStorage{VolumeClaimTemplates: []*structpb.Struct{{}}}, false},
		{"static volume", &config.KubernetesStorage{PersistentVolumeTemplates: []*structpb.Struct{{}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &Backend{conf: &config.Config{Kubernetes: &config.Kubernetes{Storage: tc.storage}}}
			if got := b.usesPersistentVolumes(); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
