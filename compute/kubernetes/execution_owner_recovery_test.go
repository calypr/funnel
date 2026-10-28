package kubernetes

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/database/boltdb"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	executionOwnerTestNamespace   = "worker-jobs"
	testWorkerContainerAnnotation = "funnel.calypr.org/worker-container"
)

type executionOwnerRecoveryFixture struct {
	backend *Backend
	db      *boltdb.BoltDB
	client  *fake.Clientset
	job     *batchv1.Job
}

func newExecutionOwnerRecoveryFixture(t *testing.T, state tes.State, owner *events.ExecutionOwner, jobStatus batchv1.JobStatus, pod *corev1.Pod) *executionOwnerRecoveryFixture {
	return newExecutionOwnerRecoveryFixtureWithClaim(t, true, state, owner, jobStatus, pod)
}

func newUnclaimedExecutionOwnerRecoveryFixture(t *testing.T, state tes.State, jobStatus batchv1.JobStatus, pod *corev1.Pod) *executionOwnerRecoveryFixture {
	return newExecutionOwnerRecoveryFixtureWithClaim(t, false, state, nil, jobStatus, pod)
}

func newExecutionOwnerRecoveryFixtureWithClaim(t *testing.T, claimed bool, state tes.State, owner *events.ExecutionOwner, jobStatus batchv1.JobStatus, pod *corev1.Pod) *executionOwnerRecoveryFixture {
	t.Helper()
	ctx := context.Background()
	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = executionOwnerTestNamespace
	conf.Kubernetes.JobsNamespace = executionOwnerTestNamespace
	conf.BoltDB.Path = filepath.Join(t.TempDir(), "tasks.db")
	db, err := boltdb.NewBoltDB(conf.BoltDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Init(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	job := executionOwnerWorkerJob(jobStatus)
	if err := db.WriteEvent(ctx, events.NewTaskCreated(&tes.Task{Id: job.Name})); err != nil {
		t.Fatal(err)
	}
	if claimed {
		acquired, err := db.ClaimExecution(ctx, job.Name, owner)
		if err != nil || !acquired {
			t.Fatalf("claiming task with recorded worker owner = %t, %v", acquired, err)
		}
	}
	if state == tes.State_INITIALIZING {
		if err := db.WriteEvent(ctx, events.NewState(job.Name, tes.State_INITIALIZING)); err != nil {
			t.Fatal(err)
		}
	} else if state == tes.State_RUNNING {
		if err := db.WriteEvent(ctx, events.NewState(job.Name, tes.State_RUNNING)); err != nil {
			t.Fatal(err)
		}
	} else if tes.TerminalState(state) {
		if err := db.WriteEvent(ctx, events.NewState(job.Name, state)); err != nil {
			t.Fatal(err)
		}
	}

	var objects []runtime.Object
	objects = append(objects, job)
	if pod != nil {
		objects = append(objects, pod)
	}
	client := fake.NewSimpleClientset(objects...)
	backend := &Backend{
		client:   client,
		event:    db,
		database: db,
		log:      logger.NewLogger("execution-owner-recovery-test", logger.DefaultConfig()),
		conf:     conf,
	}
	return &executionOwnerRecoveryFixture{backend: backend, db: db, client: client, job: job}
}

func executionOwnerWorkerJob(status batchv1.JobStatus) *batchv1.Job {
	backoffLimit := int32(1)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "owner-death-task",
			Namespace: executionOwnerTestNamespace,
			UID:       types.UID("job-uid"),
			Labels:    map[string]string{"app": "funnel-worker"},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{testWorkerContainerAnnotation: "worker"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "worker"}, {Name: "sidecar"}}},
			},
		},
		Status: status,
	}
}

func executionOwnerPod(owner *events.ExecutionOwner, phase corev1.PodPhase) *corev1.Pod {
	controller := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      owner.PodName,
			Namespace: owner.Namespace,
			UID:       types.UID(owner.PodUid),
			Labels:    map[string]string{"job-name": "owner-death-task"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "batch/v1",
				Kind:       "Job",
				Name:       "owner-death-task",
				UID:        types.UID("job-uid"),
				Controller: &controller,
			}},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func executionOwnerTask(t *testing.T, db *boltdb.BoltDB, taskID string) *tes.Task {
	t.Helper()
	task, err := db.GetTask(context.Background(), &tes.GetTaskRequest{Id: taskID, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestReconcileFailsTaskWhenRecordedWorkerPodExited(t *testing.T) {
	for _, state := range []tes.State{tes.State_INITIALIZING, tes.State_RUNNING} {
		for _, phase := range []corev1.PodPhase{corev1.PodFailed, corev1.PodSucceeded} {
			t.Run(state.String()+"/"+string(phase), func(t *testing.T) {
				owner := &events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "owner-pod", PodUid: "owner-pod-uid"}
				f := newExecutionOwnerRecoveryFixture(t, state, owner, batchv1.JobStatus{Active: 1, Failed: 1}, executionOwnerPod(owner, phase))
				f.backend.reconcileJob(context.Background(), f.job, false)

				got := executionOwnerTask(t, f.db, f.job.Name)
				if got.State != tes.State_SYSTEM_ERROR {
					t.Fatalf("state after the recorded worker Pod exited = %s, want SYSTEM_ERROR", got.State)
				}
				if len(got.Logs) == 0 || len(got.Logs[0].SystemLogs) == 0 {
					t.Fatal("owner-death reconciliation did not write a diagnostic")
				}
			})
		}
	}
}

func TestReconcileFailsTaskWhenRecordedWorkerContainerExitedWithSidecar(t *testing.T) {
	owner := &events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "owner-pod", PodUid: "owner-pod-uid"}
	pod := executionOwnerPod(owner, corev1.PodRunning)
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{Name: "worker", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}},
		{Name: "sidecar", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
	}
	f := newExecutionOwnerRecoveryFixture(t, tes.State_RUNNING, owner, batchv1.JobStatus{Active: 1}, pod)

	f.backend.reconcileJob(context.Background(), f.job, false)

	if got := executionOwnerTask(t, f.db, f.job.Name).State; got != tes.State_SYSTEM_ERROR {
		t.Fatalf("state after recorded worker container exited = %s, want SYSTEM_ERROR", got)
	}
	if _, err := f.client.BatchV1().Jobs(f.job.Namespace).Get(context.Background(), f.job.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("worker Job after owner failure = %v, want deleted", err)
	}
}

func TestReconcileRetiresUnclaimedWorkerPodAfterWorkerContainerExit(t *testing.T) {
	pod := executionOwnerPod(&events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "unclaimed-pod", PodUid: "unclaimed-pod-uid"}, corev1.PodRunning)
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{Name: "worker", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}},
		{Name: "sidecar", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
	}
	f := newUnclaimedExecutionOwnerRecoveryFixture(t, tes.State_INITIALIZING, batchv1.JobStatus{Active: 1}, pod)
	var deletedOptions metav1.DeleteOptions
	f.client.PrependReactor("delete", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		deletedOptions = action.(k8stesting.DeleteAction).GetDeleteOptions()
		return false, nil, nil
	})

	f.backend.reconcileJob(context.Background(), f.job, true)

	if _, err := f.client.CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("worker Pod after preclaim retirement = %v, want deleted", err)
	}
	if got := executionOwnerTask(t, f.db, f.job.Name).State; got != tes.State_INITIALIZING {
		t.Fatalf("task state after preclaim retirement = %s, want INITIALIZING", got)
	}
	if _, err := f.client.BatchV1().Jobs(f.job.Namespace).Get(context.Background(), f.job.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("worker Job after preclaim retirement = %v, want retained", err)
	}
	if deletedOptions.Preconditions == nil || deletedOptions.Preconditions.UID == nil || *deletedOptions.Preconditions.UID != pod.UID {
		t.Fatalf("Pod delete precondition = %+v, want UID %q", deletedOptions.Preconditions, pod.UID)
	}
	claimed, err := f.db.ClaimExecution(context.Background(), f.job.Name, &events.ExecutionOwner{Namespace: pod.Namespace, PodName: "replacement-pod", PodUid: "replacement-pod-uid"})
	if err != nil || !claimed {
		t.Fatalf("replacement claim after preclaim retirement = %t, %v, want true", claimed, err)
	}
}

func TestReconcileDoesNotRetireAmbiguousUnclaimedWorkerPod(t *testing.T) {
	pod := executionOwnerPod(&events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "unclaimed-pod", PodUid: "unclaimed-pod-uid"}, corev1.PodRunning)
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{Name: "worker", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}},
		{Name: "sidecar", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
	}
	f := newUnclaimedExecutionOwnerRecoveryFixture(t, tes.State_INITIALIZING, batchv1.JobStatus{Active: 1}, pod)
	f.job.Spec.Template.Annotations = nil

	f.backend.reconcileJob(context.Background(), f.job, false)

	if _, err := f.client.CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("ambiguous worker Pod was retired without a selected container: %v", err)
	}
}

func TestReconcileDoesNotRetireFailedDuplicateWhenRecordedOwnerIsHealthy(t *testing.T) {
	owner := &events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "owner-pod", PodUid: "owner-pod-uid"}
	duplicateOwner := &events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "duplicate-pod", PodUid: "duplicate-pod-uid"}
	duplicate := executionOwnerPod(duplicateOwner, corev1.PodRunning)
	duplicate.Status.ContainerStatuses = []corev1.ContainerStatus{
		{Name: "worker", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}},
		{Name: "sidecar", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
	}
	f := newExecutionOwnerRecoveryFixture(t, tes.State_RUNNING, owner, batchv1.JobStatus{Active: 1, Failed: 1}, executionOwnerPod(owner, corev1.PodRunning))
	if _, err := f.client.CoreV1().Pods(duplicate.Namespace).Create(context.Background(), duplicate, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	f.backend.reconcileJob(context.Background(), f.job, false)

	if got := executionOwnerTask(t, f.db, f.job.Name).State; got != tes.State_RUNNING {
		t.Fatalf("state with healthy recorded owner and failed duplicate = %s, want RUNNING", got)
	}
	if _, err := f.client.CoreV1().Pods(duplicate.Namespace).Get(context.Background(), duplicate.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("failed duplicate Pod was retired while another owner is healthy: %v", err)
	}
}

func TestReconcileRetriesUnclaimedPodRetirementAfterTransientDeleteError(t *testing.T) {
	pod := executionOwnerPod(&events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "unclaimed-pod", PodUid: "unclaimed-pod-uid"}, corev1.PodRunning)
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{Name: "worker", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}},
		{Name: "sidecar", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
	}
	f := newUnclaimedExecutionOwnerRecoveryFixture(t, tes.State_INITIALIZING, batchv1.JobStatus{Active: 1}, pod)
	deleteErr := errors.New("temporary Kubernetes API error")
	var deletes int
	f.client.PrependReactor("delete", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		deletes++
		if deletes == 1 {
			return true, nil, deleteErr
		}
		return false, nil, nil
	})

	f.backend.reconcileJob(context.Background(), f.job, false)
	if got := executionOwnerTask(t, f.db, f.job.Name).State; got != tes.State_INITIALIZING {
		t.Fatalf("task state after transient Pod delete error = %s, want INITIALIZING", got)
	}
	if _, err := f.client.CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("worker Pod after transient delete error = %v, want retained", err)
	}

	f.backend.reconcileJob(context.Background(), f.job, false)
	if deletes != 2 {
		t.Fatalf("Pod delete attempts = %d, want 2", deletes)
	}
	if _, err := f.client.CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("worker Pod after delete retry = %v, want deleted", err)
	}
}

func TestReconcileKeepsPermanentNilOwnerClaimWhenRetiringWorkerPod(t *testing.T) {
	pod := executionOwnerPod(&events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "claimed-pod", PodUid: "claimed-pod-uid"}, corev1.PodRunning)
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{Name: "worker", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}},
		{Name: "sidecar", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
	}
	f := newExecutionOwnerRecoveryFixture(t, tes.State_INITIALIZING, nil, batchv1.JobStatus{Active: 1}, pod)
	f.backend.reconcileJob(context.Background(), f.job, false)

	if _, err := f.client.CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("claimed worker Pod after retirement = %v, want deleted", err)
	}
	claimed, err := f.db.ClaimExecution(context.Background(), f.job.Name, &events.ExecutionOwner{Namespace: pod.Namespace, PodName: "replacement-pod", PodUid: "replacement-pod-uid"})
	if err != nil || claimed {
		t.Fatalf("replacement claim after retiring nil-owner claim = %t, %v, want false", claimed, err)
	}
}

func TestReconcileDefersWhenOwnerIsHealthyOrCannotBeProvenDead(t *testing.T) {
	baseOwner := &events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "owner-pod", PodUid: "owner-pod-uid"}
	tests := []struct {
		name  string
		owner *events.ExecutionOwner
		pod   *corev1.Pod
	}{
		{name: "legacy task without an owner", pod: nil},
		{name: "healthy owner with failed duplicate", owner: baseOwner, pod: executionOwnerPod(baseOwner, corev1.PodRunning)},
		{name: "worker and sidecar containers are running", owner: baseOwner, pod: func() *corev1.Pod {
			p := executionOwnerPod(baseOwner, corev1.PodRunning)
			p.Status.ContainerStatuses = []corev1.ContainerStatus{
				{Name: "worker", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
				{Name: "sidecar", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			}
			return p
		}()},
		{name: "pending owner", owner: baseOwner, pod: executionOwnerPod(baseOwner, corev1.PodPending)},
		{name: "unknown phase", owner: baseOwner, pod: executionOwnerPod(baseOwner, corev1.PodUnknown)},
		{name: "unrelated job controller", owner: baseOwner, pod: func() *corev1.Pod {
			p := executionOwnerPod(baseOwner, corev1.PodFailed)
			p.OwnerReferences[0].UID = "other-job-uid"
			return p
		}()},
		{name: "non-controller job owner reference", owner: baseOwner, pod: func() *corev1.Pod {
			p := executionOwnerPod(baseOwner, corev1.PodFailed)
			p.OwnerReferences[0].Controller = nil
			return p
		}()},
		{name: "owner namespace differs from worker job", owner: &events.ExecutionOwner{Namespace: "other-namespace", PodName: baseOwner.PodName, PodUid: baseOwner.PodUid}, pod: executionOwnerPod(&events.ExecutionOwner{Namespace: "other-namespace", PodName: baseOwner.PodName, PodUid: baseOwner.PodUid}, corev1.PodFailed)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newExecutionOwnerRecoveryFixture(t, tes.State_RUNNING, tt.owner, batchv1.JobStatus{Active: 1, Failed: 1}, tt.pod)
			f.backend.reconcileJob(context.Background(), f.job, false)
			if got := executionOwnerTask(t, f.db, f.job.Name).State; got != tes.State_RUNNING {
				t.Fatalf("state after inconclusive owner evidence = %s, want RUNNING", got)
			}
			if _, err := f.client.BatchV1().Jobs(f.job.Namespace).Get(context.Background(), f.job.Name, metav1.GetOptions{}); err != nil {
				t.Fatalf("worker Job was cleaned up without proof that its owner exited: %v", err)
			}
		})
	}
}

func TestReconcileDefersWhenOwnerPodReadFails(t *testing.T) {
	owner := &events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "owner-pod", PodUid: "owner-pod-uid"}
	f := newExecutionOwnerRecoveryFixture(t, tes.State_RUNNING, owner, batchv1.JobStatus{Active: 1, Failed: 1}, executionOwnerPod(owner, corev1.PodFailed))
	readErr := errors.New("temporary Kubernetes API error")
	var ownerReads int
	f.client.PrependReactor("get", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		get := action.(k8stesting.GetAction)
		if get.GetNamespace() == owner.Namespace && get.GetName() == owner.PodName {
			ownerReads++
			return true, nil, readErr
		}
		return false, nil, nil
	})

	f.backend.reconcileJob(context.Background(), f.job, false)
	if ownerReads != 1 {
		t.Fatalf("owner Pod reads = %d, want one", ownerReads)
	}
	if got := executionOwnerTask(t, f.db, f.job.Name).State; got != tes.State_RUNNING {
		t.Fatalf("state after owner Pod read error = %s, want RUNNING", got)
	}
	if _, err := f.client.BatchV1().Jobs(f.job.Namespace).Get(context.Background(), f.job.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("worker Job was cleaned up after owner Pod read error: %v", err)
	}
}

func TestReconcilePreservesTerminalTaskWhenRecordedOwnerExited(t *testing.T) {
	owner := &events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "owner-pod", PodUid: "owner-pod-uid"}
	f := newExecutionOwnerRecoveryFixture(t, tes.State_COMPLETE, owner, batchv1.JobStatus{Active: 1, Failed: 1}, executionOwnerPod(owner, corev1.PodFailed))
	f.backend.reconcileJob(context.Background(), f.job, false)
	if got := executionOwnerTask(t, f.db, f.job.Name).State; got != tes.State_COMPLETE {
		t.Fatalf("terminal task state = %s, want COMPLETE", got)
	}
	if _, err := f.client.BatchV1().Jobs(f.job.Namespace).Get(context.Background(), f.job.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("owner-death handling cleaned a terminal task Job: %v", err)
	}
}

func TestReconcileOwnerDeathIsIdempotent(t *testing.T) {
	owner := &events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "owner-pod", PodUid: "owner-pod-uid"}
	f := newExecutionOwnerRecoveryFixture(t, tes.State_RUNNING, owner, batchv1.JobStatus{Active: 1, Failed: 1}, executionOwnerPod(owner, corev1.PodFailed))
	f.backend.reconcileOnce(context.Background(), false)
	f.backend.reconcileOnce(context.Background(), false)

	got := executionOwnerTask(t, f.db, f.job.Name)
	if got.State != tes.State_SYSTEM_ERROR {
		t.Fatalf("state after repeated reconciliation = %s, want SYSTEM_ERROR", got.State)
	}
	if got := len(got.Logs[0].SystemLogs); got != 1 {
		t.Fatalf("owner-death diagnostic count = %d, want one", got)
	}
	if _, err := f.client.BatchV1().Jobs(f.job.Namespace).Get(context.Background(), f.job.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("worker Job after repeated reconciliation = %v, want deleted", err)
	}
}

type failOwnerDeathStateOnce struct {
	events.Writer
	failed bool
}

func (w *failOwnerDeathStateOnce) WriteEvent(ctx context.Context, ev *events.Event) error {
	if ev.Type == events.Type_TASK_STATE && ev.GetState() == tes.State_SYSTEM_ERROR && !w.failed {
		w.failed = true
		return errors.New("temporary task state write failure")
	}
	return w.Writer.WriteEvent(ctx, ev)
}

func TestReconcileRetainsResourcesWhenOwnerDeathStateWriteFails(t *testing.T) {
	owner := &events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "owner-pod", PodUid: "owner-pod-uid"}
	f := newExecutionOwnerRecoveryFixture(t, tes.State_RUNNING, owner, batchv1.JobStatus{Active: 1, Failed: 1}, executionOwnerPod(owner, corev1.PodFailed))
	f.backend.event = &failOwnerDeathStateOnce{Writer: f.db}

	f.backend.reconcileJob(context.Background(), f.job, false)
	if got := executionOwnerTask(t, f.db, f.job.Name).State; got != tes.State_RUNNING {
		t.Fatalf("state after failed SYSTEM_ERROR write = %s, want RUNNING", got)
	}
	if _, err := f.client.BatchV1().Jobs(f.job.Namespace).Get(context.Background(), f.job.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("worker Job was cleaned up before SYSTEM_ERROR was written: %v", err)
	}

	f.backend.reconcileJob(context.Background(), f.job, false)
	if got := executionOwnerTask(t, f.db, f.job.Name).State; got != tes.State_SYSTEM_ERROR {
		t.Fatalf("state after owner-death retry = %s, want SYSTEM_ERROR", got)
	}
	if _, err := f.client.BatchV1().Jobs(f.job.Namespace).Get(context.Background(), f.job.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("worker Job after successful owner-death write = %v, want deleted", err)
	}
}

func TestReconcileDefersMalformedExecutionOwner(t *testing.T) {
	owner := &events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "owner-pod", PodUid: "owner-pod-uid"}
	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = executionOwnerTestNamespace
	conf.Kubernetes.JobsNamespace = executionOwnerTestNamespace
	task := &tes.Task{
		Id:    "owner-death-task",
		State: tes.State_RUNNING,
		Logs: []*tes.TaskLog{{Metadata: map[string]string{
			events.ExecutionOwnerMetadataKey: "{malformed",
		}}},
	}
	job := executionOwnerWorkerJob(batchv1.JobStatus{Active: 1, Failed: 1})
	client := fake.NewSimpleClientset(job, executionOwnerPod(owner, corev1.PodFailed))
	writer := &capturingEventWriter{}
	backend := &Backend{
		client:   client,
		event:    writer,
		database: &mockReadOnlyServer{tasks: []*tes.Task{task}},
		log:      logger.NewLogger("execution-owner-recovery-test", logger.DefaultConfig()),
		conf:     conf,
	}
	backend.reconcileJob(context.Background(), job, true)
	if writer.hasSystemError(task.Id) {
		t.Fatal("malformed owner metadata caused SYSTEM_ERROR")
	}
}

func TestReconcileFailsTaskWhenRecordedWorkerPodIsLost(t *testing.T) {
	owner := &events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "owner-pod", PodUid: "owner-pod-uid"}
	replacement := executionOwnerPod(owner, corev1.PodRunning)
	replacement.UID = "replacement-pod-uid"
	for _, state := range []tes.State{tes.State_INITIALIZING, tes.State_RUNNING} {
		for _, tc := range []struct {
			name string
			pod  *corev1.Pod
		}{
			{name: "deleted owner"},
			{name: "reused pod name", pod: replacement},
		} {
			t.Run(state.String()+"/"+tc.name, func(t *testing.T) {
				f := newExecutionOwnerRecoveryFixture(t, state, owner, batchv1.JobStatus{Active: 1, Failed: 1}, tc.pod)
				f.backend.reconcileJob(context.Background(), f.job, false)
				if got := executionOwnerTask(t, f.db, f.job.Name).State; got != tes.State_SYSTEM_ERROR {
					t.Fatalf("state after losing owner=%s, want SYSTEM_ERROR", got)
				}
				if claimed, err := f.db.ClaimExecution(context.Background(), f.job.Name, nil); claimed || err != nil {
					t.Fatalf("claim after owner loss=%v,%v", claimed, err)
				}
			})
		}
	}
}
