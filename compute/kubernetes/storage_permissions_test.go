package kubernetes

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/funnel/compute/kubernetes/storageplan"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	plugin "github.com/calypr/funnel/plugins/proto"
	"github.com/calypr/funnel/tes"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	fakerest "k8s.io/client-go/rest/fake"
	k8stesting "k8s.io/client-go/testing"
)

func respondToPVPermissionReviews(client *fake.Clientset, respond func(*authorizationv1.SelfSubjectAccessReview) (*authorizationv1.SelfSubjectAccessReview, error)) {
	client.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		review := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
		response, err := respond(review)
		return true, response, err
	})
}

func allowPVPermissions(client *fake.Clientset) {
	respondToPVPermissionReviews(client, func(review *authorizationv1.SelfSubjectAccessReview) (*authorizationv1.SelfSubjectAccessReview, error) {
		response := review.DeepCopy()
		response.Status.Allowed = true
		return response, nil
	})
}

func permissionReviews(client *fake.Clientset) []*authorizationv1.SelfSubjectAccessReview {
	var reviews []*authorizationv1.SelfSubjectAccessReview
	for _, action := range client.Actions() {
		if action.GetVerb() != "create" || action.GetResource().Group != "authorization.k8s.io" || action.GetResource().Version != "v1" || action.GetResource().Resource != "selfsubjectaccessreviews" {
			continue
		}
		review, ok := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
		if !ok {
			continue
		}
		reviews = append(reviews, review)
	}
	return reviews
}

func assertNoPVTaskResources(t *testing.T, client *fake.Clientset, taskID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("permission denial left Job %q: %v", taskID, err)
	}
	if _, err := client.CoreV1().PersistentVolumes().Get(ctx, "data-"+taskID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("permission denial left PV %q: %v", "data-"+taskID, err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("tasks").Get(ctx, taskID+"-workspace", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("permission denial left PVC %q: %v", taskID+"-workspace", err)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "create" && (action.GetResource().Resource == "jobs" || action.GetResource().Resource == "persistentvolumes" || action.GetResource().Resource == "persistentvolumeclaims") {
			t.Errorf("permission denial created workload resource: %v", action)
		}
	}
}

func TestNativePVPermissionDenialsLeaveNoWorkloadResources(t *testing.T) {
	for _, tc := range []struct {
		name       string
		taskID     string
		verb       string
		pvName     string
		reason     string
		noOpinion  bool
		reviewErr  error
		evaluation string
		wantErrors []string
	}{
		{name: "create denied", taskID: "create-denied", verb: "create", reason: "create denied by policy", wantErrors: []string{"create", "persistentvolumes", "create denied by policy"}},
		{name: "get denied", taskID: "get-denied", verb: "get", pvName: "data-get-denied", reason: "named PV get denied", wantErrors: []string{"get", "data-get-denied", "named PV get denied"}},
		{name: "delete denied", taskID: "delete-denied", verb: "delete", pvName: "data-delete-denied", reason: "named PV delete denied", wantErrors: []string{"delete", "data-delete-denied", "named PV delete denied"}},
		{name: "get no opinion", taskID: "get-no-opinion", verb: "get", pvName: "data-get-no-opinion", reason: "authorizer returned no opinion", noOpinion: true, evaluation: "RBAC evaluation was incomplete", wantErrors: []string{"get", "data-get-no-opinion", "authorizer returned no opinion", "RBAC evaluation was incomplete"}},
		{name: "review API failure", taskID: "review-api-failure", verb: "create", reviewErr: errors.New("authorization API unavailable"), wantErrors: []string{"authorization API unavailable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := bindingClient()
			respondToPVPermissionReviews(client, func(review *authorizationv1.SelfSubjectAccessReview) (*authorizationv1.SelfSubjectAccessReview, error) {
				attributes := review.Spec.ResourceAttributes
				if tc.reviewErr != nil {
					return nil, tc.reviewErr
				}
				if attributes.Verb != tc.verb || attributes.Name != tc.pvName {
					response := review.DeepCopy()
					response.Status.Allowed = true
					return response, nil
				}
				response := review.DeepCopy()
				response.Status.Reason = tc.reason
				response.Status.EvaluationError = tc.evaluation
				response.Status.Denied = !tc.noOpinion
				return response, nil
			})
			conf := bindingConfig(t, createdStorage(t, true))
			backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
			taskID := tc.taskID
			err := backend.Submit(context.Background(), &tes.Task{Id: taskID}, conf)
			if err == nil {
				t.Errorf("submission succeeded without permission for %s %s", tc.verb, tc.pvName)
			}
			for _, want := range tc.wantErrors {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("submission error %v does not contain %q", err, want)
				}
			}
			assertNoPVTaskResources(t, client, taskID)
		})
	}
}

func multiPVStorage(t *testing.T) *config.KubernetesStorage {
	t.Helper()
	storageConfig := createdStorage(t, true)
	secondClaim := strings.Replace(boundPairClaimTemplate, "workspace", "cache", 1)
	secondClaim = strings.Replace(secondClaim, "data-{{.TaskId}}", "scratch-{{.TaskId}}", 1)
	secondPV := strings.Replace(boundVolumeTemplate, "data-", "scratch-", 1)
	storageConfig.VolumeClaimTemplates = append(storageConfig.VolumeClaimTemplates, nativeResource(t, secondClaim))
	storageConfig.PersistentVolumeTemplates = append(storageConfig.PersistentVolumeTemplates, nativeResource(t, secondPV))
	return storageConfig
}

func TestNativePVPermissionsUseResolvedNames(t *testing.T) {
	client := bindingClient()
	allowed := map[string]bool{
		"create/":              true,
		"get/data-multi":       true,
		"delete/data-multi":    true,
		"get/scratch-multi":    true,
		"delete/scratch-multi": true,
	}
	respondToPVPermissionReviews(client, func(review *authorizationv1.SelfSubjectAccessReview) (*authorizationv1.SelfSubjectAccessReview, error) {
		attributes := review.Spec.ResourceAttributes
		key := attributes.Verb + "/" + attributes.Name
		response := review.DeepCopy()
		response.Status.Allowed = allowed[key]
		response.Status.Reason = "name-limited PV role"
		response.Status.EvaluationError = "another authorizer reported an error"
		return response, nil
	})
	conf := bindingConfig(t, multiPVStorage(t))
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
	if err := backend.Submit(context.Background(), &tes.Task{Id: "multi"}, conf); err != nil {
		t.Fatalf("name-limited permissions should allow the resolved PVs even with an evaluation error: %v", err)
	}

	reviews := permissionReviews(client)
	want := []string{"create/", "get/data-multi", "delete/data-multi", "get/scratch-multi", "delete/scratch-multi"}
	if len(reviews) != len(want) {
		t.Fatalf("got %d PV permission reviews, want %d", len(reviews), len(want))
	}
	for i, review := range reviews {
		attributes := review.Spec.ResourceAttributes
		got := attributes.Verb + "/" + attributes.Name
		if got != want[i] || attributes.Group != "" || attributes.Version != "v1" || attributes.Resource != "persistentvolumes" || attributes.Namespace != "" {
			t.Errorf("permission review %d = %q group=%q version=%q resource=%q namespace=%q", i, got, attributes.Group, attributes.Version, attributes.Resource, attributes.Namespace)
		}
	}
}

func TestNativePVPermissionsAreCheckedOnEverySubmission(t *testing.T) {
	client := bindingClient()
	conf := bindingConfig(t, createdStorage(t, true))
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
	ctx := context.Background()
	task := &tes.Task{Id: "repeat"}
	if err := backend.Submit(ctx, task, conf); err != nil {
		t.Fatalf("initial submission: %v", err)
	}
	client.ClearActions()
	respondToPVPermissionReviews(client, func(review *authorizationv1.SelfSubjectAccessReview) (*authorizationv1.SelfSubjectAccessReview, error) {
		response := review.DeepCopy()
		response.Status.Denied = true
		response.Status.Reason = "permission was revoked"
		return response, nil
	})
	if err := backend.Submit(ctx, task, conf); err == nil || !strings.Contains(err.Error(), "permission was revoked") {
		t.Fatalf("repeated submission must observe fresh permission state, got %v", err)
	}
	if len(permissionReviews(client)) != 1 {
		t.Fatalf("repeated submission did not perform a fresh permission review: %v", client.Actions())
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "create" && (action.GetResource().Resource == "jobs" || action.GetResource().Resource == "persistentvolumes" || action.GetResource().Resource == "persistentvolumeclaims") {
			t.Errorf("rejected repeated submission created workload resource: %v", action)
		}
	}
}

func TestNativePVPermissionPreflightUsesPluginResolvedPlan(t *testing.T) {
	client := bindingClient()
	respondToPVPermissionReviews(client, func(review *authorizationv1.SelfSubjectAccessReview) (*authorizationv1.SelfSubjectAccessReview, error) {
		response := review.DeepCopy()
		response.Status.Denied = true
		response.Status.Reason = "plugin PV permission denied"
		return response, nil
	})
	conf := bindingConfig(t, nil)
	conf.Plugins = &config.Plugins{}
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
	effectiveConfig := &config.Config{Kubernetes: &config.Kubernetes{Storage: createdStorage(t, true)}}
	ctx := context.WithValue(context.Background(), "pluginResponse", &plugin.JobResponse{Config: effectiveConfig})
	taskID := "plugin"
	err := backend.WriteEvent(ctx, events.NewTaskCreated(&tes.Task{Id: taskID}))
	if err == nil || !strings.Contains(err.Error(), "plugin PV permission denied") {
		t.Errorf("plugin-resolved PV plan should be checked, got %v", err)
	}
	assertNoPVTaskResources(t, client, taskID)
	reviews := permissionReviews(client)
	if len(reviews) != 1 || reviews[0].Spec.ResourceAttributes.Name != "" {
		t.Fatalf("expected create permission for the plugin-resolved PV plan, got %v", reviews)
	}
}

func TestNoPersistentVolumePlansSkipPermissionReviews(t *testing.T) {
	for _, tc := range []struct {
		name    string
		storage *config.KubernetesStorage
		seedPVC bool
	}{
		{name: "nil plan"},
		{name: "dynamic claim", storage: createdStorage(t, false)},
		{name: "existing claim", storage: existingStorage(t, "shared"), seedPVC: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := bindingClient()
			if tc.seedPVC {
				_, err := client.CoreV1().PersistentVolumeClaims("tasks").Create(context.Background(), &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "tasks"},
					Spec:       corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}},
				}, metav1.CreateOptions{})
				if err != nil {
					t.Fatal(err)
				}
			}
			conf := bindingConfig(t, tc.storage)
			backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
			if err := backend.Submit(context.Background(), &tes.Task{Id: "no-pv"}, conf); err != nil {
				t.Fatalf("submit without task-owned PV: %v", err)
			}
			if reviews := permissionReviews(client); len(reviews) != 0 {
				t.Fatalf("plan without task-owned PV issued permission reviews: %v", reviews)
			}
		})
	}
}

func TestPVPermissionReviewUsesCallerContext(t *testing.T) {
	client, err := kubeclient.NewForConfigAndClient(&rest.Config{Host: "http://localhost"}, fakerest.CreateHTTPClient(func(request *http.Request) (*http.Response, error) {
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("permission request did not inherit caller cancellation")
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plan := &storageplan.Plan{PersistentVolumes: []*corev1.PersistentVolume{{ObjectMeta: metav1.ObjectMeta{Name: "data-cancelled"}}}}
	backend := &Backend{client: client}
	if err := backend.validatePersistentVolumePermissions(ctx, plan); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller context was not preserved: %v", err)
	}
}
