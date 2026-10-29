package mongodb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests/mongotest"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestLegacyNumericOutputSize(t *testing.T) {
	db := newMongoTestDB(t, mongotest.Start(t), "legacy_output_size")
	ctx := context.Background()
	_, err := db.tasks().InsertOne(ctx, bson.M{
		"id": "legacy-output", "state": tes.State_RUNNING,
		"logs": bson.A{bson.M{"outputs": bson.A{
			bson.M{"path": "/legacy", "url": "s3://bucket/legacy", "sizebytes": int64(1234567890123)},
			bson.M{"path": "/small", "sizebytes": int32(42)},
			bson.M{"path": "/current", "sizebytes": "7"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range []string{tes.View_BASIC.String(), tes.View_FULL.String()} {
		t.Run(view, func(t *testing.T) {
			response, err := db.ListTasks(ctx, &tes.ListTasksRequest{View: view, State: tes.State_RUNNING})
			if err != nil {
				t.Fatalf("ListTasks: %v", err)
			}
			if len(response.Tasks) != 1 {
				t.Fatalf("ListTasks tasks = %d, want 1", len(response.Tasks))
			}
			checkOutputSizes(t, response.Tasks[0])
			task, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: "legacy-output", View: view})
			if err != nil {
				t.Fatalf("GetTask: %v", err)
			}
			checkOutputSizes(t, task)
		})
	}
	mux := runtime.NewServeMux()
	if err := tes.RegisterTaskServiceHandlerServer(ctx, mux, &server.TaskService{Read: db}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/tasks/legacy-output", "/tasks/legacy-output", "/v1/tasks", "/v1/tasks/legacy-output?view=FULL", "/v1/tasks?view=BASIC"} {
		t.Run("HTTP "+path, func(t *testing.T) {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "legacy-output") {
				t.Fatalf("GET %s: status %d, body %s", path, response.Code, response.Body.String())
			}
		})
	}
	_, err = db.tasks().InsertOne(ctx, bson.M{
		"id": "invalid-output", "logs": bson.A{bson.M{"outputs": bson.A{bson.M{"sizebytes": true}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: "invalid-output", View: tes.View_FULL.String()}); err == nil {
		t.Fatal("GetTask accepted an invalid output size")
	}
}

func checkOutputSizes(t *testing.T, task *tes.Task) {
	t.Helper()
	if len(task.GetLogs()) != 1 || len(task.GetLogs()[0].GetOutputs()) != 3 {
		t.Fatalf("unexpected task logs: %v", task.GetLogs())
	}
	outputs := task.GetLogs()[0].GetOutputs()
	for i, want := range []string{"1234567890123", "42", "7"} {
		if got := outputs[i].GetSizeBytes(); got != want {
			t.Errorf("output %d size = %q, want %q", i, got, want)
		}
	}
	if outputs[0].GetPath() != "/legacy" || outputs[0].GetUrl() != "s3://bucket/legacy" {
		t.Fatalf("legacy output fields changed: %v", outputs[0])
	}
}
