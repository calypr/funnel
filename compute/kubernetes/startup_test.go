package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"google.golang.org/protobuf/types/known/durationpb"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type startupTaskDatabase struct {
	mockDatabase
	listed chan struct{}
}

func (d *startupTaskDatabase) ListTasks(context.Context, *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	select {
	case d.listed <- struct{}{}:
	default:
	}
	return &tes.ListTasksResponse{}, nil
}

func startupBackend(t *testing.T, handler http.HandlerFunc) (*Backend, context.Context, context.CancelFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = "test"
	conf.Kubernetes.JobsNamespace = "test"
	conf.Kubernetes.ReconcileRate = durationpb.New(5 * time.Millisecond)
	return &Backend{
		client: client, conf: conf,
		database: &startupTaskDatabase{listed: make(chan struct{}, 1)},
		event:    &noopEventWriter{},
		log:      logger.NewLogger("startup-test", logger.DefaultConfig()),
	}, ctx, cancel
}

func emptyKubernetesList(w http.ResponseWriter, r *http.Request) {
	version, kind := "v1", "ServiceAccountList"
	if strings.HasSuffix(r.URL.Path, "/jobs") {
		version, kind = "batch/v1", "JobList"
	} else if strings.HasSuffix(r.URL.Path, "/persistentvolumes") {
		kind = "PersistentVolumeList"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"apiVersion": version, "kind": kind, "items": []interface{}{}})
}

func TestStartupCleanupDoesNotBlockAndPrecedesReconciliation(t *testing.T) {
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	overlap := make(chan struct{}, 1)
	var requests atomic.Int32
	var released atomic.Bool
	backend, ctx, _ := startupBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(cleanupStarted)
			select {
			case <-releaseCleanup:
			case <-r.Context().Done():
				return
			}
		} else if !released.Load() {
			select {
			case overlap <- struct{}{}:
			default:
			}
		}
		emptyKubernetesList(w, r)
	})
	returned := make(chan struct{})
	go func() {
		backend.startReconciler(ctx)
		close(returned)
	}()
	select {
	case <-cleanupStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("startup cleanup did not reach the Kubernetes API")
	}
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("startup is blocked on backlog cleanup")
	}
	select {
	case <-overlap:
		t.Fatal("reconciliation overlapped blocked startup cleanup")
	case <-time.After(50 * time.Millisecond):
	}
	released.Store(true)
	close(releaseCleanup)
	select {
	case <-backend.database.(*startupTaskDatabase).listed:
	case <-time.After(2 * time.Second):
		t.Fatal("reconciliation did not start after cleanup finished")
	}
}

func TestStartupCleanupStopsOnCancellation(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	backend, ctx, cancel := startupBackend(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	})
	go backend.startReconciler(ctx)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("startup cleanup did not start")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not interrupt the cleanup request")
	}
}

func TestDisabledReconcilerSkipsStartupCleanup(t *testing.T) {
	var requests atomic.Int32
	backend, ctx, _ := startupBackend(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		emptyKubernetesList(w, r)
	})
	backend.conf.Kubernetes.DisableReconciler = true
	backend.startReconciler(ctx)
	time.Sleep(50 * time.Millisecond)
	if got := requests.Load(); got != 0 {
		t.Fatalf("disabled reconciler made %d Kubernetes requests", got)
	}
}

func TestDisabledStartupCleanupStillReconciles(t *testing.T) {
	backend, ctx, _ := startupBackend(t, emptyKubernetesList)
	backend.conf.Kubernetes.DisableJobCleanup = true
	backend.startReconciler(ctx)
	select {
	case <-backend.database.(*startupTaskDatabase).listed:
	case <-time.After(2 * time.Second):
		t.Fatal("disabling job cleanup also disabled reconciliation")
	}
}
