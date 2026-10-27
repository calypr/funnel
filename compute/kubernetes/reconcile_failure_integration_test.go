package kubernetes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/database/boltdb"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type failOnceStateWriter struct {
	events.Writer
	failed bool
}

func (w *failOnceStateWriter) WriteEvent(ctx context.Context, event *events.Event) error {
	if event.Type == events.Type_TASK_STATE && event.GetState() == tes.SystemError && !w.failed {
		w.failed = true
		return errors.New("simulated temporary failure writing task state")
	}
	return w.Writer.WriteEvent(ctx, event)
}

func TestReconcileFailureRecoveryLive(t *testing.T) {
	kubeconfig := os.Getenv("FUNNEL_KUBERNETES_TEST_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set FUNNEL_KUBERNETES_TEST_KUBECONFIG to run against a disposable cluster")
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
	namespace, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "funnel-reconcile-live-"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ns := namespace.Name
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.CoreV1().Namespaces().Delete(cleanupCtx, ns, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("deleting namespace: %v", err)
		}
	})
	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace, conf.Kubernetes.JobsNamespace = ns, ns
	conf.Kubernetes.ConfigMapTemplate, conf.Kubernetes.ServiceAccountTemplate, conf.Kubernetes.RoleTemplate, conf.Kubernetes.RoleBindingTemplate = "", "", "", ""
	conf.BoltDB.Path = filepath.Join(t.TempDir(), "tasks.db")
	db, err := boltdb.NewBoltDB(conf.BoltDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	task := &tes.Task{Id: "reconcile-failure", State: tes.Queued}
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}
	backoff := int32(0)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: task.Id, Namespace: ns, Labels: map[string]string{"app": "funnel-worker"}},
		Spec: batchv1.JobSpec{BackoffLimit: &backoff, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			RestartPolicy:      corev1.RestartPolicyNever,
			ServiceAccountName: "missing-service-account",
			Containers:         []corev1.Container{{Name: "worker", Image: "alpine:3.21"}},
		}}},
	}
	if _, err := client.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	writer := &failOnceStateWriter{Writer: db}
	backend := &Backend{client: client, conf: conf, event: writer, database: db, log: logger.NewLogger("reconcile-live", logger.DefaultConfig())}
	for {
		if count, _ := backend.hasJobFailedCreateEvent(ctx, task.Id); count > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for worker startup failure: %v", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Log("Kubernetes reported repeated failures creating the worker pod")
	backend.reconcileOnce(ctx, false)
	if !writer.failed {
		t.Fatal("did not inject task state write failure")
	}
	got, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: task.Id, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != tes.Queued {
		t.Fatalf("after failed write task state = %s, want QUEUED", got.State)
	}
	retained, err := client.BatchV1().Jobs(ns).Get(ctx, task.Id, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("job was not retained after failed write: %v", err)
	}
	if retained.DeletionTimestamp != nil {
		t.Fatal("job was marked for deletion after failed write")
	}
	t.Log("task remained queued and Kubernetes job was retained after the injected write failure")
	backend.reconcileOnce(ctx, false)
	got, err = db.GetTask(ctx, &tes.GetTaskRequest{Id: task.Id, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != tes.SystemError {
		t.Fatalf("after retry task state = %s, want SYSTEM_ERROR", got.State)
	}
	if len(got.Logs) == 0 || len(got.Logs[0].SystemLogs) == 0 || !strings.Contains(strings.Join(got.Logs[0].SystemLogs, "\n"), "failed to create pod") {
		t.Fatalf("missing startup failure diagnostic in system logs: %+v", got.Logs)
	}
	for {
		_, err := client.BatchV1().Jobs(ns).Get(ctx, task.Id, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			break
		}
		if err != nil {
			t.Fatalf("checking job cleanup: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("job was not deleted after failure was recorded: %v", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Log("retry recorded SYSTEM_ERROR and diagnostic in BoltDB, then removed the Kubernetes job")
}
