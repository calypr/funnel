package kubernetes

import (
	"context"
	"errors"
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestReconcileMissingWorkerJob(t *testing.T) {
	const (
		namespace = "worker-jobs"
		taskID    = "task-missing-job"
	)

	tests := []struct {
		name          string
		state         tes.State
		jobOnGet      *batchv1.Job
		getError      error
		wantSystemErr bool
		wantSystemLog string
	}{
		{
			name:  "queued task may not have a job yet",
			state: tes.State_QUEUED,
		},
		{
			name:          "job created after initial list is reconciled",
			state:         tes.State_RUNNING,
			wantSystemErr: true,
			wantSystemLog: "Kubernetes job in FAILED state",
			jobOnGet: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      taskID,
					Namespace: namespace,
					Labels:    map[string]string{"app": "funnel-worker"},
				},
				Status: batchv1.JobStatus{
					Failed: 1,
					Conditions: []batchv1.JobCondition{{
						Type:   batchv1.JobFailed,
						Status: corev1.ConditionTrue,
					}},
				},
			},
		},
		{
			name:          "missing running job is a system error",
			state:         tes.State_RUNNING,
			wantSystemErr: true,
			wantSystemLog: "Kubernetes job not found for task",
		},
		{
			name:          "missing initializing job is a system error",
			state:         tes.State_INITIALIZING,
			wantSystemErr: true,
			wantSystemLog: "Kubernetes job not found for task",
		},
		{
			name:     "transient get failure defers reconciliation",
			state:    tes.State_RUNNING,
			getError: errors.New("temporary Kubernetes API error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			task := &tes.Task{Id: taskID, State: tt.state}
			database := &mockReadOnlyServer{tasks: []*tes.Task{task}}
			event := &capturingEventWriter{}
			client := fake.NewSimpleClientset()
			var getCount int
			var gotNamespace, gotName string
			client.PrependReactor("get", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
				get := action.(k8stesting.GetAction)
				getCount++
				gotNamespace, gotName = get.GetNamespace(), get.GetName()
				if tt.getError != nil {
					return true, nil, tt.getError
				}
				if tt.jobOnGet != nil {
					return true, tt.jobOnGet, nil
				}
				return false, nil, nil
			})

			conf := config.DefaultConfig()
			conf.Kubernetes.Namespace = namespace
			conf.Kubernetes.JobsNamespace = namespace
			backend := &Backend{
				client:   client,
				event:    event,
				database: database,
				log:      logger.NewLogger("reconcile-test", logger.DefaultConfig()),
				conf:     conf,
			}

			backend.reconcileOnce(ctx, true)

			if getCount != 1 || gotNamespace != namespace || gotName != taskID {
				t.Fatalf("worker Job lookup = (%d, %q, %q), want one lookup for %s/%s", getCount, gotNamespace, gotName, namespace, taskID)
			}
			if got := event.hasSystemError(taskID); got != tt.wantSystemErr {
				t.Fatalf("SYSTEM_ERROR = %t, want %t", got, tt.wantSystemErr)
			}
			var gotSystemLog string
			for _, ev := range event.events {
				if log := ev.GetSystemLog(); log != nil {
					gotSystemLog = log.GetMsg()
				}
			}
			if gotSystemLog != tt.wantSystemLog {
				t.Fatalf("system log = %q, want %q", gotSystemLog, tt.wantSystemLog)
			}
		})
	}
}
