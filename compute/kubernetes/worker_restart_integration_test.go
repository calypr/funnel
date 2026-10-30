package kubernetes

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/funnel/compute/kubernetes/resources"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/database/boltdb"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type lostOwnerClaimRPC struct {
	events.UnimplementedEventServiceServer
	writer               events.Writer
	claimer              events.ExecutionClaimer
	markRunning          bool
	failBeforeFirstClaim bool
	preclaimFailure      sync.Once
	firstOwner           chan *events.ExecutionOwner
	replacementOwner     chan *events.ExecutionOwner
}

type workerRestartScenario struct {
	name                 string
	markRunning          bool
	deleteOwner          bool
	withSidecar          bool
	failBeforeFirstClaim bool
	wantState            tes.State
}

func (s *lostOwnerClaimRPC) WriteEvent(ctx context.Context, event *events.Event) (*events.WriteEventResponse, error) {
	if err := s.writer.WriteEvent(ctx, event); err != nil {
		return nil, err
	}
	return &events.WriteEventResponse{}, nil
}

func (s *lostOwnerClaimRPC) ClaimExecution(ctx context.Context, req *events.ClaimExecutionRequest) (*events.ClaimExecutionResponse, error) {
	preclaimFailure := false
	s.preclaimFailure.Do(func() { preclaimFailure = s.failBeforeFirstClaim })
	if preclaimFailure {
		return nil, status.Error(codes.Unavailable, "injected worker claim failure before claim")
	}
	owner := req.GetOwner()
	claimed, err := s.claimer.ClaimExecution(ctx, req.GetTaskId(), owner)
	if err != nil {
		return nil, err
	}
	if !claimed {
		select {
		case s.replacementOwner <- owner:
		default:
		}
		return &events.ClaimExecutionResponse{Claimed: false}, nil
	}
	select {
	case s.firstOwner <- owner:
	default:
	}
	if s.markRunning {
		if err := s.writer.WriteEvent(ctx, events.NewState(req.GetTaskId(), tes.State_RUNNING)); err != nil {
			return nil, err
		}
	}
	return nil, status.Error(codes.Unavailable, "injected lost execution claim response")
}

const liveRestartWorkerTemplate = `apiVersion: batch/v1
kind: Job
metadata:
  name: {{.TaskId}}
  namespace: {{.JobsNamespace}}
  labels:
    app: funnel-worker
spec:
  backoffLimit: {{.BackoffLimit}}
  template:
    metadata:
      labels:
        app: funnel-worker
    spec:
      restartPolicy: OnFailure
      containers:
        - name: worker
          image: {{.Image}}
          imagePullPolicy: IfNotPresent
          args: [worker, run, --config, /config/config.yaml, --taskID, "{{.TaskId}}"]
          volumeMounts:
            - name: config
              mountPath: /config
              readOnly: true
      volumes:
        - name: config
          configMap:
            name: owner-recovery-worker-config
`

const liveRestartSidecarWorkerTemplate = `apiVersion: batch/v1
kind: Job
metadata:
  name: {{.TaskId}}
  namespace: {{.JobsNamespace}}
  labels:
    app: funnel-worker
spec:
  backoffLimit: {{.BackoffLimit}}
  template:
    metadata:
      labels:
        app: funnel-worker
      annotations:
        funnel.calypr.org/worker-container: worker
    spec:
      terminationGracePeriodSeconds: 1
      restartPolicy: OnFailure
      containers:
        - name: worker
          image: {{.Image}}
          imagePullPolicy: IfNotPresent
          args: [worker, run, --config, /config/config.yaml, --taskID, "{{.TaskId}}"]
          volumeMounts:
            - name: config
              mountPath: /config
              readOnly: true
        - name: sidecar
          image: {{.Image}}
          imagePullPolicy: IfNotPresent
          command: ["/bin/sh", "-c", "sleep 3600"]
      volumes:
        - name: config
          configMap:
            name: owner-recovery-worker-config
`

func TestWorkerRestartRecoveryLive(t *testing.T) {
	kubeconfig := os.Getenv("FUNNEL_KUBERNETES_TEST_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set FUNNEL_KUBERNETES_TEST_KUBECONFIG to run against a disposable cluster")
	}
	image := os.Getenv("FUNNEL_E2E_IMAGE")
	if image == "" {
		t.Fatal("FUNNEL_E2E_IMAGE must name the Funnel image imported into the cluster")
	}
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []workerRestartScenario{
		{name: "initializing", wantState: tes.State_INITIALIZING},
		{name: "running", markRunning: true, wantState: tes.State_RUNNING},
		{name: "deleted-owner", deleteOwner: true, wantState: tes.State_INITIALIZING},
		{name: "sidecar-before-and-after-claim", markRunning: true, withSidecar: true, failBeforeFirstClaim: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runWorkerRestartRecoveryLive(t, client, image, tc)
		})
	}
}

func runWorkerRestartRecoveryLive(t *testing.T, client kubernetes.Interface, image string, scenario workerRestartScenario) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	namespace, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "funnel-worker-restart-"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ns := namespace.Name
	defer func() {
		if t.Failed() {
			logLiveStorageFailure(t, client, ns)
		}
	}()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := client.CoreV1().Namespaces().Delete(cleanupCtx, ns, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("deleting namespace: %v", err)
		}
	})

	serverConfig := config.DefaultConfig()
	serverConfig.BoltDB.Path = filepath.Join(t.TempDir(), "tasks.db")
	serverConfig.Kubernetes.Namespace = ns
	serverConfig.Kubernetes.JobsNamespace = ns
	db, err := boltdb.NewBoltDB(serverConfig.BoltDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	taskID := "worker-restart-" + scenario.name
	task := &tes.Task{
		Id:    taskID,
		State: tes.State_INITIALIZING,
		Resources: &tes.Resources{BackendParameters: map[string]string{
			"backoff_limit": "3",
		}},
		Executors: []*tes.Executor{{Image: "busybox:1.36", Command: []string{"/bin/sh", "-c", "sleep 60"}}},
	}
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	rpcServer := grpc.NewServer()
	fixture := &lostOwnerClaimRPC{
		writer:               db,
		claimer:              db,
		markRunning:          scenario.markRunning,
		failBeforeFirstClaim: scenario.failBeforeFirstClaim,
		firstOwner:           make(chan *events.ExecutionOwner, 1),
		replacementOwner:     make(chan *events.ExecutionOwner, 1),
	}
	events.RegisterEventServiceServer(rpcServer, fixture)
	tes.RegisterTaskServiceServer(rpcServer, &taskReaderRPC{database: db})
	serveErr := make(chan error, 1)
	go func() { serveErr <- rpcServer.Serve(listener) }()
	defer func() {
		rpcServer.Stop()
		err := <-serveErr
		if err != nil {
			t.Errorf("serve worker fixture RPC: %v", err)
		}
	}()

	workerConfig := config.EmptyConfig()
	workerConfig.Compute = "kubernetes"
	workerConfig.Database = "rpc"
	workerConfig.RPCClient.ServerAddress = fmt.Sprintf("host.k3d.internal:%d", listener.Addr().(*net.TCPAddr).Port)
	workerConfig.Worker.WorkDir = "/tmp/funnel-worker-restart"
	configData, err := config.ToYaml(workerConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().ConfigMaps(ns).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "owner-recovery-worker-config", Namespace: ns},
		Data:       map[string]string{"config.yaml": string(configData)},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	serverConfig.Kubernetes.WorkerTemplate = liveRestartWorkerTemplate
	if scenario.withSidecar {
		serverConfig.Kubernetes.WorkerTemplate = liveRestartSidecarWorkerTemplate
	}
	serverConfig.Kubernetes.WorkerImage = image
	job, err := resources.CreateJob(ctx, task, serverConfig, client, logger.NewLogger("worker-restart-live", logger.DefaultConfig()), nil)
	if err != nil {
		t.Fatalf("creating worker job: %v", err)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 3 {
		t.Fatalf("worker backoffLimit = %v, want 3", job.Spec.BackoffLimit)
	}
	if job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("worker restartPolicy = %s, want Never", job.Spec.Template.Spec.RestartPolicy)
	}
	if scenario.failBeforeFirstClaim {
		runWorkerSidecarRetryRecoveryLive(t, ctx, client, ns, taskID, serverConfig, db, fixture)
		return
	}

	var owner *events.ExecutionOwner
	select {
	case owner = <-fixture.firstOwner:
	case <-ctx.Done():
		t.Fatalf("waiting for the first owner claim: %v", ctx.Err())
	}
	if owner == nil || owner.Namespace != ns || owner.PodName == "" || owner.PodUid == "" {
		t.Fatalf("first worker supplied incomplete owner identity: %+v", owner)
	}
	replacement, err := waitForOwnerRetry(ctx, client, ns, taskID, owner.PodUid)
	if err != nil {
		t.Fatal(err)
	}

	got, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: taskID, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != scenario.wantState {
		t.Fatalf("before reconciliation state = %s, want %s", got.State, scenario.wantState)
	}
	storedOwner, err := events.TaskExecutionOwner(got)
	if err != nil {
		t.Fatalf("reading persisted execution owner: %v", err)
	}
	if storedOwner == nil || storedOwner.GetNamespace() != owner.GetNamespace() || storedOwner.GetPodName() != owner.GetPodName() || storedOwner.GetPodUid() != owner.GetPodUid() {
		t.Fatalf("persisted owner = %+v, want first worker owner %+v", storedOwner, owner)
	}
	if err := assertNoExecutorWork(got, client, ctx, ns, taskID); err != nil {
		t.Fatal(err)
	}

	if scenario.wantState == tes.State_INITIALIZING {
		select {
		case deniedOwner := <-fixture.replacementOwner:
			if deniedOwner == nil || deniedOwner.GetPodUid() != replacement.GetPodUid() {
				t.Fatalf("denied claim owner = %+v, want replacement pod %+v", deniedOwner, replacement)
			}
		case <-ctx.Done():
			t.Fatalf("waiting for replacement claim denial: %v", ctx.Err())
		case <-time.After(10 * time.Second):
			t.Fatal("replacement worker did not attempt its claim")
		}
	}
	claimed, err := db.ClaimExecution(ctx, taskID, replacement)
	if err != nil {
		t.Fatalf("re-claiming execution for replacement pod: %v", err)
	}
	if claimed {
		t.Fatalf("replacement pod %q acquired the execution claim", replacement.GetPodName())
	}

	if scenario.deleteOwner {
		if err := client.CoreV1().Pods(ns).Delete(ctx, owner.PodName, metav1.DeleteOptions{}); err != nil {
			t.Fatal(err)
		}
		for {
			_, err := client.CoreV1().Pods(ns).Get(ctx, owner.PodName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	backend := &Backend{
		client:   client,
		conf:     serverConfig,
		event:    db,
		database: db,
		log:      logger.NewLogger("worker-restart-live", logger.DefaultConfig()),
	}
	backend.reconcileOnce(ctx, false)
	got, err = db.GetTask(ctx, &tes.GetTaskRequest{Id: taskID, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != tes.State_SYSTEM_ERROR {
		t.Fatalf("after owner reconciliation state = %s, want SYSTEM_ERROR", got.State)
	}
	if len(got.Logs) == 0 {
		t.Fatal("SYSTEM_ERROR has no task log")
	}
	diagnostic := strings.Join(got.Logs[0].SystemLogs, "\n")
	if !strings.Contains(diagnostic, owner.GetPodName()) || !strings.Contains(diagnostic, owner.GetPodUid()) {
		t.Fatalf("SYSTEM_ERROR diagnostic %q does not identify failed owner %q/%q", diagnostic, owner.GetPodName(), owner.GetPodUid())
	}
	if err := assertNoExecutorWork(got, client, ctx, ns, taskID); err != nil {
		t.Fatal(err)
	}
	t.Logf("lost claim response for pod %s/%s; replacement stayed idle; reconciliation recorded SYSTEM_ERROR", owner.GetPodName(), owner.GetPodUid())
}

func runWorkerSidecarRetryRecoveryLive(t *testing.T, ctx context.Context, client kubernetes.Interface, namespace, taskID string, conf *config.Config, db *boltdb.BoltDB, fixture *lostOwnerClaimRPC) {
	t.Helper()
	backend := &Backend{
		client:   client,
		conf:     conf,
		event:    db,
		database: db,
		log:      logger.NewLogger("worker-sidecar-recovery-live", logger.DefaultConfig()),
	}
	firstPod, err := waitForWorkerExitWithRunningSidecar(ctx, client, namespace, taskID, "")
	if err != nil {
		t.Fatal(err)
	}
	before, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: taskID, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if before.State != tes.State_QUEUED {
		t.Fatalf("task state before preclaim retry = %s, want QUEUED", before.State)
	}
	if owner, err := events.TaskExecutionOwner(before); err != nil || owner != nil {
		t.Fatalf("owner before preclaim retry = %+v, %v, want none", owner, err)
	}
	if err := assertNoExecutorWork(before, client, ctx, namespace, taskID); err != nil {
		t.Fatal(err)
	}

	backend.reconcileOnce(ctx, false)
	replacement, job, err := waitForUnclaimedJobRetry(ctx, client, namespace, taskID, firstPod.UID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 3 || job.Status.Failed < 1 {
		t.Fatalf("Job retry state = backoffLimit %v, failed %d, want backoffLimit 3 and at least one failure", job.Spec.BackoffLimit, job.Status.Failed)
	}
	if err := waitForPodSidecarStopped(ctx, client, namespace, firstPod.Name, firstPod.UID); err != nil {
		t.Fatal(err)
	}

	var owner *events.ExecutionOwner
	select {
	case owner = <-fixture.firstOwner:
	case <-ctx.Done():
		t.Fatalf("waiting for replacement worker to acquire the first execution claim: %v", ctx.Err())
	}
	if owner == nil || owner.PodUid == string(firstPod.UID) || owner.PodUid != string(replacement.UID) {
		t.Fatalf("replacement claim owner = %+v, want replacement Pod UID %q", owner, replacement.UID)
	}
	claimedPod, err := waitForWorkerExitWithRunningSidecar(ctx, client, namespace, taskID, owner.PodUid)
	if err != nil {
		t.Fatal(err)
	}
	if claimedPod.Status.Phase != corev1.PodRunning {
		t.Fatalf("claimed worker Pod phase = %s, want Running while sidecar stays alive", claimedPod.Status.Phase)
	}
	claimedTask, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: taskID, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	storedOwner, err := events.TaskExecutionOwner(claimedTask)
	if err != nil || storedOwner == nil || storedOwner.PodUid != owner.PodUid {
		t.Fatalf("recorded owner = %+v, %v, want %q", storedOwner, err, owner.PodUid)
	}
	if err := assertNoExecutorWork(claimedTask, client, ctx, namespace, taskID); err != nil {
		t.Fatal(err)
	}

	backend.reconcileOnce(ctx, false)
	after, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: taskID, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if after.State != tes.State_SYSTEM_ERROR {
		t.Fatalf("task state after claimed worker exits with sidecar alive = %s, want SYSTEM_ERROR", after.State)
	}
	if len(after.Logs) == 0 || len(after.Logs[0].SystemLogs) == 0 || !strings.Contains(strings.Join(after.Logs[0].SystemLogs, "\n"), owner.PodUid) {
		t.Fatalf("SYSTEM_ERROR diagnostic does not identify claimed Pod UID %q: %+v", owner.PodUid, after.GetLogs())
	}
	if err := assertNoExecutorWork(after, client, ctx, namespace, taskID); err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimExecution(ctx, taskID, &events.ExecutionOwner{Namespace: namespace, PodName: "replacement-worker", PodUid: "replacement-worker-uid"})
	if err != nil || claimed {
		t.Fatalf("new worker claim after postclaim failure = %t, %v, want false", claimed, err)
	}
	t.Logf("preclaim worker Pod %s failed and stopped its sidecar; Job recorded failure %d and retried as %s; the replacement claim stayed permanent when its worker exited with the sidecar still running", firstPod.Name, job.Status.Failed, replacement.Name)
}

func waitForWorkerExitWithRunningSidecar(ctx context.Context, client kubernetes.Interface, namespace, taskID, podUID string) (*corev1.Pod, error) {
	for {
		pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + taskID})
		if err != nil {
			return nil, err
		}
		for i := range pods.Items {
			pod := &pods.Items[i]
			if podUID != "" && string(pod.UID) != podUID {
				continue
			}
			if pod.Status.Phase != corev1.PodRunning {
				continue
			}
			workerFailed, sidecarRunning := false, false
			for _, status := range pod.Status.ContainerStatuses {
				switch status.Name {
				case "worker":
					workerFailed = status.State.Terminated != nil && status.State.Terminated.ExitCode != 0
				case "sidecar":
					sidecarRunning = status.State.Running != nil
				}
			}
			if workerFailed && sidecarRunning {
				return pod.DeepCopy(), nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for worker container failure with a live sidecar: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitForUnclaimedJobRetry(ctx context.Context, client kubernetes.Interface, namespace, taskID string, failedUID types.UID) (*corev1.Pod, *batchv1.Job, error) {
	for {
		job, err := client.BatchV1().Jobs(namespace).Get(ctx, taskID, metav1.GetOptions{})
		if err != nil {
			return nil, nil, err
		}
		pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + taskID})
		if err != nil {
			return nil, nil, err
		}
		var replacement *corev1.Pod
		for i := range pods.Items {
			pod := &pods.Items[i]
			if pod.UID == failedUID || !podControlledByJob(pod, job) || pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
				continue
			}
			replacement = pod.DeepCopy()
			break
		}
		if job.Status.Failed > 0 && job.Status.Active > 0 && replacement != nil {
			return replacement, job, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("waiting for a failed Job attempt and active replacement (failed=%d active=%d): %w", job.Status.Failed, job.Status.Active, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitForPodSidecarStopped(ctx context.Context, client kubernetes.Interface, namespace, podName string, podUID types.UID) error {
	for {
		pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) || (err == nil && pod.UID != podUID) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == "sidecar" && status.State.Terminated != nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for sidecar termination in Pod %s/%s: %w", namespace, podName, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitForOwnerRetry(ctx context.Context, client kubernetes.Interface, namespace, taskID, failedUID string) (*events.ExecutionOwner, error) {
	for {
		job, err := client.BatchV1().Jobs(namespace).Get(ctx, taskID, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + taskID})
		if err != nil {
			return nil, err
		}
		failedOwner := false
		var activeReplacement *events.ExecutionOwner
		for i := range pods.Items {
			pod := &pods.Items[i]
			if string(pod.UID) == failedUID && pod.Status.Phase == corev1.PodFailed {
				failedOwner = true
			}
			if string(pod.UID) != failedUID && pod.Status.Phase == corev1.PodRunning {
				activeReplacement = &events.ExecutionOwner{Namespace: pod.Namespace, PodName: pod.Name, PodUid: string(pod.UID)}
			}
		}
		if failedOwner && activeReplacement != nil && job.Status.Failed > 0 && job.Status.Active > 0 {
			return activeReplacement, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for a failed owner pod and running replacement (job failed=%d active=%d): %w", job.Status.Failed, job.Status.Active, ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

func assertNoExecutorWork(task *tes.Task, client kubernetes.Interface, ctx context.Context, namespace, taskID string) error {
	for i, taskLog := range task.GetLogs() {
		if len(taskLog.GetLogs()) != 0 || len(taskLog.GetOutputs()) != 0 {
			return fmt.Errorf("task log %d contains executor logs or outputs: %+v", i, taskLog)
		}
	}
	jobs, err := client.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing jobs: %w", err)
	}
	for _, job := range jobs.Items {
		if strings.HasPrefix(job.Name, taskID+"-") {
			return fmt.Errorf("executor job %q exists for task %q", job.Name, taskID)
		}
	}
	return nil
}
