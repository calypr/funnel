package kubernetes

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/compute/kubernetes/storageplan"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func TestNativeStoragePermissionsLive(t *testing.T) {
	kubeconfig := os.Getenv("FUNNEL_KUBERNETES_TEST_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set FUNNEL_KUBERNETES_TEST_KUBECONFIG to run against a disposable cluster")
	}
	adminConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := kubernetes.NewForConfig(adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		withPV     bool
		verbs      []string
		deniedVerb string
	}{
		{name: "no-pv-permissions", withPV: true, deniedVerb: "create"},
		{name: "cannot-read-pv", withPV: true, verbs: []string{"create"}, deniedVerb: "get"},
		{name: "cannot-delete-pv", withPV: true, verbs: []string{"create", "get"}, deniedVerb: "delete"},
		{name: "named-pv-permissions", withPV: true, verbs: []string{"create", "get", "delete"}},
		{name: "dynamic-pvc-without-pv-permissions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			namespace, err := admin.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "funnel-pv-access-"}}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			ns := namespace.Name
			taskID := ns + "-task"
			pvName := "data-" + taskID
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cleanupCancel()
				_, patchErr := admin.BatchV1().Jobs(ns).Patch(cleanupCtx, taskID, types.MergePatchType, []byte(`{"metadata":{"finalizers":[]}}`), metav1.PatchOptions{})
				if patchErr != nil && !apierrors.IsNotFound(patchErr) {
					t.Errorf("remove test Job finalizers: %v", patchErr)
				}
				if err := admin.CoreV1().Namespaces().Delete(cleanupCtx, ns, metav1.DeleteOptions{}); err != nil {
					t.Errorf("delete test namespace: %v", err)
				}
				if err := admin.CoreV1().PersistentVolumes().Delete(cleanupCtx, pvName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
					t.Errorf("delete test PV: %v", err)
				}
				for _, err := range []error{
					admin.RbacV1().ClusterRoleBindings().Delete(cleanupCtx, ns, metav1.DeleteOptions{}),
					admin.RbacV1().ClusterRoles().Delete(cleanupCtx, ns, metav1.DeleteOptions{}),
				} {
					if err != nil && !apierrors.IsNotFound(err) {
						t.Errorf("delete test RBAC: %v", err)
					}
				}
			})
			_, err = admin.CoreV1().ServiceAccounts(ns).Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "funnel"}}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = admin.RbacV1().Roles(ns).Create(ctx, &rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{Name: "funnel"},
				Rules:      []rbacv1.PolicyRule{{APIGroups: []string{"", "batch"}, Resources: []string{"jobs", "pods", "persistentvolumeclaims"}, Verbs: []string{"*"}}},
			}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			subjects := []rbacv1.Subject{{Kind: "ServiceAccount", Name: "funnel", Namespace: ns}}
			_, err = admin.RbacV1().RoleBindings(ns).Create(ctx, &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "funnel"}, Subjects: subjects,
				RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "funnel"},
			}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(tc.verbs) > 0 {
				var rules []rbacv1.PolicyRule
				for _, verb := range tc.verbs {
					rule := rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"persistentvolumes"}, Verbs: []string{verb}}
					if verb != "create" {
						rule.ResourceNames = []string{pvName}
					}
					rules = append(rules, rule)
				}
				_, err = admin.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: ns}, Rules: rules}, metav1.CreateOptions{})
				if err != nil {
					t.Fatal(err)
				}
				_, err = admin.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
					ObjectMeta: metav1.ObjectMeta{Name: ns}, Subjects: subjects,
					RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: ns},
				}, metav1.CreateOptions{})
				if err != nil {
					t.Fatal(err)
				}
			}
			restrictedConfig := rest.CopyConfig(adminConfig)
			restrictedConfig.Impersonate = rest.ImpersonationConfig{
				UserName: "system:serviceaccount:" + ns + ":funnel",
				Groups:   []string{"system:authenticated", "system:serviceaccounts", "system:serviceaccounts:" + ns},
			}
			client, err := kubernetes.NewForConfig(restrictedConfig)
			if err != nil {
				t.Fatal(err)
			}
			conf := bindingConfig(t, createdStorage(t, tc.withPV))
			conf.Kubernetes.Namespace, conf.Kubernetes.JobsNamespace = ns, ns
			conf.Kubernetes.WorkerImage = "alpine:3.21"
			conf.Kubernetes.ConfigMapTemplate = ""
			conf.Kubernetes.WorkerTemplate = `apiVersion: batch/v1
kind: Job
metadata:
  name: "{{.TaskId}}"
  namespace: "{{.Namespace}}"
spec:
  suspend: true
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: worker
          image: alpine:3.21
          command: ["true"]
`
			backend := &Backend{client: client, conf: conf, event: events.Noop{}, log: logger.NewLogger("storage-permissions-live", logger.DefaultConfig())}
			submitErr := backend.Submit(ctx, &tes.Task{Id: taskID}, conf)
			if tc.deniedVerb != "" {
				if submitErr == nil || !strings.Contains(submitErr.Error(), tc.deniedVerb) || !strings.Contains(submitErr.Error(), "persistentvolumes") {
					t.Fatalf("expected an actionable %s permission error, got %v", tc.deniedVerb, submitErr)
				}
				job, err := admin.BatchV1().Jobs(ns).Get(ctx, taskID, metav1.GetOptions{})
				if err == nil {
					t.Fatalf("denied PV %s permission left a worker Job: deleting=%t finalizers=%v", tc.deniedVerb, job.DeletionTimestamp != nil, job.Finalizers)
				}
				if !apierrors.IsNotFound(err) {
					t.Fatal(err)
				}
				if _, err := admin.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
					t.Fatalf("denied PV permission created a PV: %v", err)
				}
				claims, err := admin.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{})
				if err != nil || len(claims.Items) != 0 {
					t.Fatalf("denied PV permission created claims: claims=%+v err=%v", claims, err)
				}
				t.Logf("denied PV %s permission before Job, PV, or PVC creation", tc.deniedVerb)
				return
			}
			if submitErr != nil {
				t.Fatal(submitErr)
			}
			if err := backend.Cancel(ctx, taskID); err != nil {
				t.Fatal(err)
			}
			job, err := admin.BatchV1().Jobs(ns).Get(ctx, taskID, metav1.GetOptions{})
			if !apierrors.IsNotFound(err) {
				if err != nil {
					t.Fatal(err)
				}
				if slices.Contains(job.Finalizers, storageplan.Finalizer) {
					t.Fatalf("successful cleanup retained the storage finalizer: %v", job.Finalizers)
				}
			}
			t.Log("submission and cleanup succeeded without PV list permission")
		})
	}
}
