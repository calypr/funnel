package resources

import (
	"context"
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	namespace     = "test-namespace"
	jobsNamespace = "test-jobs-namespace"
	testTaskID    = "test-task-id"
)

// minimalWorkerTemplate is a valid Job template that avoids the pod-image lookup.
const minimalWorkerTemplate = `apiVersion: batch/v1
kind: Job
metadata:
  name: {{.TaskId}}
  namespace: {{.JobsNamespace}}
spec:
  template:
    spec:
      restartPolicy: OnFailure
      containers:
      - name: worker
        image: alpine`

const minimalServiceAccountTemplate = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: funnel-worker-sa-{{.Namespace}}-{{.TaskId}}
  namespace: {{.Namespace}}
  labels:
    app: funnel
    taskId: {{.TaskId}}`

const minimalRoleTemplate = `apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: funnel-worker-sa-{{.Namespace}}-{{.TaskId}}-role
  namespace: {{.Namespace}}
  labels:
    app: funnel
    taskId: {{.TaskId}}
rules: []`

const minimalRoleBindingTemplate = `apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: funnel-worker-sa-{{.Namespace}}-{{.TaskId}}-binding
  namespace: {{.Namespace}}
  labels:
    app: funnel
    taskId: {{.TaskId}}
subjects:
- kind: ServiceAccount
  name: funnel-worker-sa-{{.Namespace}}-{{.TaskId}}
  namespace: {{.Namespace}}
roleRef:
  kind: Role
  name: funnel-worker-sa-{{.Namespace}}-{{.TaskId}}-role
  apiGroup: rbac.authorization.k8s.io`

const minimalPVTemplate = `apiVersion: v1
kind: PersistentVolume
metadata:
  name: funnel-worker-pv-{{.TaskId}}
  labels:
    app: funnel
    taskId: {{.TaskId}}
spec:
  storageClassName: ""
  capacity:
    storage: 10Mi
  accessModes:
  - ReadWriteMany
  persistentVolumeReclaimPolicy: Retain
  hostPath:
    path: /tmp/funnel-{{.TaskId}}`

const minimalPVCTemplate = `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: funnel-worker-pvc-{{.TaskId}}
  namespace: {{.Namespace}}
  labels:
    app: funnel
    taskId: {{.TaskId}}
spec:
  storageClassName: ""
  accessModes:
  - ReadWriteMany
  resources:
    requests:
      storage: 10Mi
  volumeName: funnel-worker-pv-{{.TaskId}}`

var l = logger.NewLogger("test", logger.DefaultConfig())
var ctx = context.Background()

func TestCreateConfigMap(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.ConfigMapTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: funnel-worker-config-{{ .TaskId }}
  namespace: {{ .Namespace }}
  labels:
    app: funnel
data:
  funnel-worker.yaml: |
    placeholder`

	err := CreateConfigMap(ctx, testTaskID, conf, fake.NewSimpleClientset(), l, nil)
	if err != nil {
		t.Errorf("CreateConfigMap failed: %v", err)
	}
}

func TestDeleteConfigMap(t *testing.T) {
	cmName := "funnel-worker-config-" + testTaskID

	t.Run("labeled", func(t *testing.T) {
		fakeClient := fake.NewSimpleClientset()

		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName,
				Namespace: namespace,
				Labels: map[string]string{
					"app":    "funnel",
					"taskId": testTaskID,
				},
			},
		}
		_, err := fakeClient.CoreV1().ConfigMaps(namespace).Create(context.Background(), cm, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("Failed to create labeled ConfigMap: %v", err)
		}

		err = DeleteConfigMap(context.Background(), testTaskID, namespace, fakeClient, l)
		if err != nil {
			t.Errorf("DeleteConfigMap failed: %v", err)
		}

		_, err = fakeClient.CoreV1().ConfigMaps(namespace).Get(context.Background(), cmName, metav1.GetOptions{})
		if err == nil {
			t.Error("labeled ConfigMap was not deleted")
		}
	})

	t.Run("unlabeled", func(t *testing.T) {
		fakeClient := fake.NewSimpleClientset()

		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName,
				Namespace: namespace,
			},
		}
		_, err := fakeClient.CoreV1().ConfigMaps(namespace).Create(context.Background(), cm, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("Failed to create unlabeled ConfigMap: %v", err)
		}

		err = DeleteConfigMap(context.Background(), testTaskID, namespace, fakeClient, l)
		if err != nil {
			t.Errorf("DeleteConfigMap failed: %v", err)
		}

		_, err = fakeClient.CoreV1().ConfigMaps(namespace).Get(context.Background(), cmName, metav1.GetOptions{})
		if err == nil {
			t.Error("unlabeled ConfigMap was not deleted")
		}
	})
}

func TestCreateJob(t *testing.T) {
	task := &tes.Task{
		Id: testTaskID,
		Resources: &tes.Resources{
			CpuCores: 1,
			RamGb:    1.0,
			DiskGb:   10.0,
		},
	}

	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.WorkerTemplate = minimalWorkerTemplate
	_, err := CreateJob(ctx, task, conf, fake.NewSimpleClientset(), l)
	if err != nil {
		t.Errorf("CreateJob failed: %v", err)
	}
}

func TestDeleteJob(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()

	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = jobsNamespace

	// Create a test Job in the same namespace DeleteJob will use.
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testTaskID,
			Namespace: jobsNamespace,
		},
	}
	_, err := fakeClient.BatchV1().Jobs(jobsNamespace).Create(context.Background(), job, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test Job: %v", err)
	}

	err = DeleteJob(context.Background(), conf, testTaskID, fakeClient, l)
	if err != nil {
		t.Errorf("DeleteJob failed: %v", err)
	}

	// Verify deletion
	_, err = fakeClient.BatchV1().Jobs(jobsNamespace).Get(context.Background(), testTaskID, metav1.GetOptions{})
	if err == nil {
		t.Error("Job was not deleted")
	}
}

func TestCreatePV(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.PVTemplate = minimalPVTemplate
	err := CreatePV(ctx, testTaskID, conf, fake.NewSimpleClientset(), l)
	if err != nil {
		t.Errorf("CreatePV failed: %v", err)
	}
}

func TestDeletePV(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()

	// Create a test PV first
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "funnel-worker-pv-" + testTaskID,
			Labels: map[string]string{
				"app":       "funnel",
				"taskId":    testTaskID,
				"namespace": jobsNamespace,
			},
		},
	}
	_, err := fakeClient.CoreV1().PersistentVolumes().Create(context.Background(), pv, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test PV: %v", err)
	}

	err = DeletePV(context.Background(), testTaskID, jobsNamespace, fakeClient, l)
	if err != nil {
		t.Errorf("DeletePV failed: %v", err)
	}

	// Verify deletion
	_, err = fakeClient.CoreV1().PersistentVolumes().Get(context.Background(), "funnel-worker-pv-"+testTaskID, metav1.GetOptions{})
	if err == nil {
		t.Error("PV was not deleted")
	}
}

func TestCreatePVC(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.PVCTemplate = minimalPVCTemplate
	err := CreatePVC(ctx, testTaskID, conf, fake.NewSimpleClientset(), l, nil)
	if err != nil {
		t.Errorf("CreatePVC failed: %v", err)
	}
}

func TestDeletePVC(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()

	// Create a test PVC first
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "funnel-worker-pvc-" + testTaskID,
			Namespace: namespace,
		},
	}
	_, err := fakeClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test PVC: %v", err)
	}

	err = DeletePVC(context.Background(), testTaskID, namespace, fakeClient, l)
	if err != nil {
		t.Errorf("DeletePVC failed: %v", err)
	}

	// Verify deletion
	_, err = fakeClient.CoreV1().PersistentVolumeClaims(namespace).Get(context.Background(), "funnel-worker-pvc-"+testTaskID, metav1.GetOptions{})
	if err == nil {
		t.Error("PVC was not deleted")
	}
}

func TestCreateJobWithNoResources(t *testing.T) {
	task := &tes.Task{
		Id: testTaskID,
		// Intentionally omit Resources to test default handling
	}

	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.WorkerTemplate = minimalWorkerTemplate
	_, err := CreateJob(ctx, task, conf, fake.NewSimpleClientset(), l)
	if err != nil {
		t.Errorf("CreateJob failed with nil resources: %v", err)
	}
}

func TestDeleteNonExistentResources(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	nonExistentID := "non-existent-id"

	// DeleteConfigMap is a no-op when the resource doesn't exist.
	t.Run("ConfigMap", func(t *testing.T) {
		err := DeleteConfigMap(context.Background(), nonExistentID, namespace, fakeClient, l)
		if err != nil {
			t.Errorf("DeleteConfigMap returned unexpected error for non-existent resource: %v", err)
		}
	})

	// DeletePV and DeletePVC are no-ops when the resource doesn't exist.
	t.Run("PV", func(t *testing.T) {
		err := DeletePV(context.Background(), nonExistentID, jobsNamespace, fakeClient, l)
		if err != nil {
			t.Errorf("DeletePV returned unexpected error for non-existent resource: %v", err)
		}
	})

	t.Run("PVC", func(t *testing.T) {
		err := DeletePVC(context.Background(), nonExistentID, namespace, fakeClient, l)
		if err != nil {
			t.Errorf("DeletePVC returned unexpected error for non-existent resource: %v", err)
		}
	})
}

func TestCreateServiceAccount(t *testing.T) {
	task := &tes.Task{
		Id: testTaskID,
	}

	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.ServiceAccountTemplate = minimalServiceAccountTemplate
	err := CreateServiceAccount(ctx, task, conf, fake.NewSimpleClientset(), l, nil)
	if err != nil {
		t.Errorf("CreateServiceAccount failed: %v", err)
	}
}

func TestDeleteServiceAccount(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "funnel-worker-sa-" + testTaskID,
			Namespace: namespace,
			Labels: map[string]string{
				"app":    "funnel",
				"taskId": testTaskID,
			},
		},
	}
	_, err := fakeClient.CoreV1().ServiceAccounts(namespace).Create(context.Background(), sa, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test ServiceAccount: %v", err)
	}

	err = DeleteServiceAccount(context.Background(), testTaskID, namespace, fakeClient, l, nil)
	if err != nil {
		t.Errorf("DeleteServiceAccount failed: %v", err)
	}
}

func TestDeleteServiceAccountInUse(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "funnel-worker-sa-" + testTaskID,
			Namespace: namespace,
			Labels: map[string]string{
				"app":    "funnel",
				"taskId": testTaskID,
			},
		},
	}
	_, err := fakeClient.CoreV1().ServiceAccounts(namespace).Create(context.Background(), sa, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test ServiceAccount: %v", err)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: sa.Name,
		},
	}
	_, err = fakeClient.CoreV1().Pods(namespace).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test Pod: %v", err)
	}

	err = DeleteServiceAccount(context.Background(), testTaskID, namespace, fakeClient, l, nil)
	if err != nil {
		t.Fatalf("expected DeleteServiceAccount to succeed (skip) when ServiceAccount is in use, got error: %v", err)
	}

	// SA must still exist — it was skipped, not deleted.
	_, err = fakeClient.CoreV1().ServiceAccounts(namespace).Get(context.Background(), sa.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected ServiceAccount to remain when still in use: %v", err)
	}
}

func TestCreateRole(t *testing.T) {
	task := &tes.Task{
		Id: testTaskID,
	}

	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.RoleTemplate = minimalRoleTemplate
	err := CreateRole(ctx, task, conf, fake.NewSimpleClientset(), l, nil)
	if err != nil {
		t.Errorf("CreateRole failed: %v", err)
	}
}

func TestDeleteRole(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()

	role := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "funnel-worker-role-" + testTaskID,
			Namespace: namespace,
		},
	}
	_, err := fakeClient.CoreV1().ServiceAccounts(namespace).Create(context.Background(), role, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test Role: %v", err)
	}

	err = DeleteRole(context.Background(), testTaskID, namespace, fakeClient, l)
	if err != nil {
		t.Errorf("DeleteRole failed: %v", err)
	}
}

func TestCreateRoleBinding(t *testing.T) {
	task := &tes.Task{
		Id: testTaskID,
	}

	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.RoleBindingTemplate = minimalRoleBindingTemplate
	err := CreateRoleBinding(ctx, task, conf, fake.NewSimpleClientset(), l, nil)
	if err != nil {
		t.Errorf("CreateRoleBinding failed: %v", err)
	}
}

func TestDeleteRoleBinding(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()

	roleBinding := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "funnel-worker-rolebinding-" + testTaskID,
			Namespace: namespace,
		},
	}
	_, err := fakeClient.CoreV1().ServiceAccounts(namespace).Create(context.Background(), roleBinding, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test RoleBinding: %v", err)
	}

	err = DeleteRoleBinding(context.Background(), testTaskID, namespace, fakeClient, l)
	if err != nil {
		t.Errorf("DeleteRoleBinding failed: %v", err)
	}
}
