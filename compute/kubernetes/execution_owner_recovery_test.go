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

const executionOwnerTestNamespace = "worker-jobs"

type executionOwnerRecoveryFixture struct {
	backend *Backend
	db      *boltdb.BoltDB
	client  *fake.Clientset
	job     *batchv1.Job
}

func newExecutionOwnerRecoveryFixture(t *testing.T, state tes.State, owner *events.ExecutionOwner, jobStatus batchv1.JobStatus, pod *corev1.Pod) *executionOwnerRecoveryFixture {
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
	claimed, err := db.ClaimExecution(ctx, job.Name, owner)
	if err != nil || !claimed {
		t.Fatalf("claiming task with recorded worker owner = %t, %v", claimed, err)
	}
	if state == tes.State_RUNNING {
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
		Spec:   batchv1.JobSpec{BackoffLimit: &backoffLimit},
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

func TestReconcileDefersWhenOwnerIsHealthyOrCannotBeProvenDead(t *testing.T) {
	baseOwner := &events.ExecutionOwner{Namespace: executionOwnerTestNamespace, PodName: "owner-pod", PodUid: "owner-pod-uid"}
	tests := []struct {
		name  string
		owner *events.ExecutionOwner
		pod   *corev1.Pod
	}{
		{name: "legacy task without an owner", pod: nil},
		{name: "healthy owner with failed duplicate", owner: baseOwner, pod: executionOwnerPod(baseOwner, corev1.PodRunning)},
		{name: "worker container exited while sidecar keeps pod running", owner: baseOwner, pod: func() *corev1.Pod {
			p := executionOwnerPod(baseOwner, corev1.PodRunning)
			p.Status.ContainerStatuses = []corev1.ContainerStatus{
				{Name: "worker", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}},
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
