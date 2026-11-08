package server

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/calypr/funnel/config"
	appconfig "github.com/calypr/funnel/internal/config"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/logger"
	pluginproto "github.com/calypr/funnel/plugins/proto"
	"github.com/calypr/funnel/tes"
)

func TestDebugInterceptorLogsOnlyMethodAndStatus(t *testing.T) {
	const secret = "RPC_PAYLOAD_SENTINEL"
	log, output := serverCaptureLogger(t)
	interceptor := newDebugInterceptor(log)
	request := &struct{ Authorization string }{Authorization: secret}
	response := &struct{ Data string }{Data: secret}
	callErr := errors.New(secret)
	gotResponse, gotErr := interceptor(context.Background(), request,
		&grpc.UnaryServerInfo{FullMethod: "/tes.TaskService/CreateTask"},
		func(_ context.Context, gotReq interface{}) (interface{}, error) {
			if gotReq != request {
				t.Fatal("interceptor changed the request")
			}
			return response, callErr
		},
	)
	if gotResponse != response || gotErr != callErr {
		t.Fatal("interceptor changed the handler response or error")
	}
	got := output.String()
	if strings.Contains(got, secret) {
		t.Fatalf("RPC log contains request, response, or error payload: %s", got)
	}
	if !strings.Contains(got, "/tes.TaskService/CreateTask") || !strings.Contains(got, status.Code(callErr).String()) {
		t.Fatalf("RPC log omits method or status metadata: %s", got)
	}
}

func TestHandleErrorLogsMetadataAndPreservesResponse(t *testing.T) {
	const secret = "HTTP_ERROR_SENTINEL"
	log, output := serverCaptureLogger(t)
	server := &Server{Log: log}
	req := httptest.NewRequest("GET", "/v1/tasks?token="+secret, nil)
	resp := httptest.NewRecorder()
	server.handleError(resp, req, secret, 422)
	if !strings.Contains(resp.Body.String(), secret) {
		t.Fatalf("HTTP error response = %q, want original error text", resp.Body.String())
	}
	got := output.String()
	if strings.Contains(got, secret) || strings.Contains(got, "url") {
		t.Fatalf("HTTP error log contains raw error or URL: %s", got)
	}
	if !strings.Contains(got, "GET") || !strings.Contains(got, "422") {
		t.Fatalf("HTTP error log omits method or status: %s", got)
	}
}

func TestDebugInterceptorPanicLogsTypeWithoutPayload(t *testing.T) {
	log, output := serverCaptureLogger(t)
	_, err := newDebugInterceptor(log)(t.Context(), nil, &grpc.UnaryServerInfo{FullMethod: "/test/Panic"}, func(context.Context, interface{}) (interface{}, error) {
		panic("PANIC_PAYLOAD_SENTINEL")
	})
	if status.Code(err).String() != "Internal" {
		t.Fatalf("panic status = %v", err)
	}
	got := output.String()
	if strings.Contains(got, "PANIC_PAYLOAD_SENTINEL") {
		t.Error("panic payload reached application logs")
	}
	if !strings.Contains(got, "panic_type") || !strings.Contains(got, "/test/Panic") {
		t.Error("panic identity was not logged")
	}
}

func TestCreateTaskPluginLogOmitsPayloadAndPreservesTask(t *testing.T) {
	const (
		messageSecret = "PLUGIN_MESSAGE_SENTINEL"
		taskSecret    = "PLUGIN_TASK_SENTINEL"
		configSecret  = "PLUGIN_CONFIG_SENTINEL"
	)
	log, output := serverCaptureLogger(t)
	pluginConfig := appconfig.DefaultConfig()
	pluginConfig.Database = configSecret
	pluginTask := &tes.Task{
		Id: "plugin-task",
		Executors: []*tes.Executor{{
			Image:   "alpine",
			Command: []string{"echo", taskSecret},
			Env:     map[string]string{"TOKEN": taskSecret},
		}},
	}
	store := &capturingEventWriter{}
	service := &TaskService{
		Event:   store,
		Compute: events.Backend{},
		Log:     log,
		Config:  &config.Config{Plugins: &config.Plugins{}},
		Plugin: testAuthorize{response: &pluginproto.JobResponse{
			Code:    200,
			Message: messageSecret,
			UserId:  "plugin-user",
			Task:    pluginTask,
			Config:  pluginConfig,
		}},
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-test", "value"))
	if _, err := service.CreateTask(ctx, &tes.Task{Id: "request-task"}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	service.Close()

	got := output.String()
	for _, secret := range []string{messageSecret, taskSecret, configSecret} {
		if strings.Contains(got, secret) {
			t.Errorf("plugin response log contains payload sentinel %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, `"response_code":200`) || !strings.Contains(got, "request-task") {
		t.Errorf("plugin response log omits safe response metadata: %s", got)
	}
	if store.event == nil || store.event.GetTask().GetExecutors()[0].GetEnv()["TOKEN"] != taskSecret {
		t.Fatal("stored task payload changed while logging plugin response")
	}
}

func TestHandleDoPluginActionErrorOmitsTaskPayload(t *testing.T) {
	const (
		messageSecret = "PLUGIN_ERROR_MESSAGE_SENTINEL"
		taskSecret    = "PLUGIN_ERROR_TASK_SENTINEL"
	)
	service := &TaskService{
		Config: &config.Config{Plugins: &config.Plugins{}},
		Plugin: testAuthorize{response: &pluginproto.JobResponse{
			Code:    400,
			Message: messageSecret,
			Task: &tes.Task{Executors: []*tes.Executor{{
				Env: map[string]string{"TOKEN": taskSecret},
			}}},
		}},
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-test", "value"))
	_, err := service.HandleDoPluginAction(ctx, nil, pluginproto.Type_CREATE)
	if err == nil || !strings.Contains(err.Error(), messageSecret) {
		t.Fatalf("HandleDoPluginAction error = %v, want original plugin message", err)
	}
	if strings.Contains(err.Error(), taskSecret) {
		t.Fatalf("HandleDoPluginAction error exposes plugin task: %v", err)
	}
}

func serverCaptureLogger(t *testing.T) (*logger.Logger, *bytes.Buffer) {
	t.Helper()
	conf := logger.DebugConfig()
	conf.Formatter = "json"
	output := &bytes.Buffer{}
	log := logger.NewLogger("server-test", conf)
	log.SetOutput(output)
	return log, output
}

type testAuthorize struct {
	response *pluginproto.JobResponse
}

func (p testAuthorize) PluginAction(map[string]string, map[string]*pluginproto.StringList, *config.Config, *tes.Task, pluginproto.Type) (*pluginproto.JobResponse, error) {
	return p.response, nil
}

type capturingEventWriter struct {
	event *events.Event
}

func (w *capturingEventWriter) WriteEvent(_ context.Context, ev *events.Event) error {
	w.event = ev
	return nil
}

func (*capturingEventWriter) Close() {}
