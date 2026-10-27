package kubernetes

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type rejectCompletionRPC struct {
	events.UnimplementedEventServiceServer
	writer   events.Writer
	rejected atomic.Bool
}

type taskReaderRPC struct {
	tes.UnimplementedTaskServiceServer
	database tes.ReadOnlyServer
}

func (s *taskReaderRPC) GetTask(ctx context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	return s.database.GetTask(ctx, req)
}

func (s *rejectCompletionRPC) WriteEvent(ctx context.Context, event *events.Event) (*events.WriteEventResponse, error) {
	if event.Type == events.Type_TASK_STATE && event.GetState() == tes.Complete {
		s.rejected.Store(true)
		return nil, status.Error(codes.FailedPrecondition, "injected final state write failure")
	}
	if err := s.writer.WriteEvent(ctx, event); err != nil {
		return nil, err
	}
	return &events.WriteEventResponse{}, nil
}

func TestWorkerFinalStateFailureRecoveryLive(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	namespace, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "funnel-final-state-live-"}}, metav1.CreateOptions{})
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
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.CoreV1().Namespaces().Delete(c, ns, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("deleting namespace: %v", err)
		}
	})
	conf := config.DefaultConfig()
	conf.BoltDB.Path = filepath.Join(t.TempDir(), "tasks.db")
	conf.Kubernetes.Namespace, conf.Kubernetes.JobsNamespace = ns, ns
	conf.Kubernetes.ConfigMapTemplate, conf.Kubernetes.ServiceAccountTemplate, conf.Kubernetes.RoleTemplate, conf.Kubernetes.RoleBindingTemplate = "", "", "", ""
	db, err := boltdb.NewBoltDB(conf.BoltDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	task := &tes.Task{Id: "final-state-failure", State: tes.Queued, Executors: []*tes.Executor{{Image: "stand-in", Command: []string{"true"}}}}
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	rpcServer := grpc.NewServer()
	fixture := &rejectCompletionRPC{writer: db}
	events.RegisterEventServiceServer(rpcServer, fixture)
	tes.RegisterTaskServiceServer(rpcServer, &taskReaderRPC{database: db})
	go func() { _ = rpcServer.Serve(listener) }()
	defer rpcServer.Stop()
	port := listener.Addr().(*net.TCPAddr).Port
	workerConfig := config.EmptyConfig()
	workerConfig.RPCClient.ServerAddress = fmt.Sprintf("host.k3d.internal:%d", port)
	workerConfig.Worker.WorkDir = "/tmp/funnel-work"
	workerConfig.Worker.Container.DriverCommand = "/bin/true"
	workerConfig.Worker.Container.PullCommand = "pull"
	workerConfig.Worker.Container.RunCommand = "run {{.Image}} {{.Command}}"
	configData, err := config.ToYaml(workerConfig)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CoreV1().ConfigMaps(ns).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "worker-config", Namespace: ns}, Data: map[string]string{"config.yaml": string(configData)}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	backoff := int32(0)
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: task.Id, Namespace: ns, Labels: map[string]string{"app": "funnel-worker"}}, Spec: batchv1.JobSpec{BackoffLimit: &backoff, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers:    []corev1.Container{{Name: "worker", Image: image, ImagePullPolicy: corev1.PullIfNotPresent, Args: []string{"worker", "run", "--config", "/config/config.yaml", "--taskID", task.Id}, VolumeMounts: []corev1.VolumeMount{{Name: "config", MountPath: "/config", ReadOnly: true}}}},
		Volumes:       []corev1.Volume{{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "worker-config"}}}}},
	}}}}
	if _, err := client.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	var finished *batchv1.Job
	for {
		finished, err = client.BatchV1().Jobs(ns).Get(ctx, task.Id, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if finished.Status.Failed > 0 || finished.Status.Succeeded > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for worker job: %v", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if finished.Status.Succeeded > 0 {
		t.Fatal("worker job succeeded despite rejected final state write")
	}
	if !fixture.rejected.Load() {
		t.Fatal("worker did not attempt final COMPLETE write")
	}
	for {
		finished, err = client.BatchV1().Jobs(ns).Get(ctx, task.Id, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if (&Backend{}).isJobDone(finished.Status) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for worker job failure condition: %v", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	got, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: task.Id, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != tes.Running {
		t.Fatalf("after rejected final write state=%s, want RUNNING", got.State)
	}
	backend := &Backend{client: client, conf: conf, event: db, database: db, log: logger.NewLogger("final-state-live", logger.DefaultConfig())}
	backend.reconcileOnce(ctx, false)
	got, err = db.GetTask(ctx, &tes.GetTaskRequest{Id: task.Id, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != tes.SystemError {
		t.Fatalf("after reconciliation state=%s, want SYSTEM_ERROR", got.State)
	}
	if len(got.Logs) == 0 || !strings.Contains(strings.Join(got.Logs[0].SystemLogs, "\n"), "Kubernetes job in FAILED state") {
		t.Fatalf("missing failure diagnostic: %+v", got.Logs)
	}
	for {
		_, err := client.BatchV1().Jobs(ns).Get(ctx, task.Id, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			break
		}
		if err != nil {
			t.Fatalf("checking worker job cleanup: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("worker job was not deleted after failure was recorded: %v", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Log("worker job failed after rejected COMPLETE write; reconciliation recorded SYSTEM_ERROR in BoltDB")
}
