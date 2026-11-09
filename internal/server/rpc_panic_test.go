package server

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/tesrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const rpcPanicSecret = "RPC_AUTHORIZATION_PANIC_SENTINEL"

type panickingTaskLookup struct {
	tesrpc.UnimplementedTaskServiceServer
}

func (panickingTaskLookup) GetTask(_ context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	if req.Id == "panic" {
		panic(rpcPanicSecret)
	}
	return &tes.Task{Id: req.Id, State: tes.State_COMPLETE}, nil
}

func TestServeRecoversInterceptorAndHandlerPanics(t *testing.T) {
	srv := newTestServer(freeTCPAddress(t), "0")
	log, output := serverCaptureLogger(t)
	srv.Log = log
	srv.BasicAuth = []*config.BasicCredential{{User: "alice", Password: "synthetic"}}
	srv.Tasks = panickingTaskLookup{}
	srv.Events = &events.UnimplementedEventServiceServer{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	stop := sync.OnceFunc(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	t.Cleanup(stop)
	conn, err := grpc.NewClient(srv.RPCAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	requestCtx, requestCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer requestCancel()
	requestCtx = metadata.NewOutgoingContext(requestCtx, metadata.Pairs(
		"authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("alice:synthetic")),
	))
	tasks := tesrpc.NewTaskServiceClient(conn)
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"event authorization", func() error {
			_, err := events.NewEventServiceClient(conn).WriteEvent(requestCtx, events.NewState("panic", tes.State_RUNNING), grpc.WaitForReady(true))
			return err
		}},
		{"task handler", func() error {
			_, err := tasks.GetTask(requestCtx, &tes.GetTaskRequest{Id: "panic"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if status.Code(err) != codes.Internal || status.Convert(err).Message() != "internal server error" {
				t.Fatalf("panic response = %v, want generic Internal error", err)
			}
		})
	}
	got, err := tasks.GetTask(requestCtx, &tes.GetTaskRequest{Id: "healthy"})
	if err != nil || got.GetId() != "healthy" {
		t.Fatalf("request after panics: task=%v error=%v", got, err)
	}
	stop()
	logs := output.String()
	if strings.Contains(logs, rpcPanicSecret) {
		t.Fatal("panic payload reached application logs")
	}
	if strings.Count(logs, `"msg":"AUDIT"`) != 3 || strings.Count(logs, `"code":"Internal"`) != 2 ||
		!strings.Contains(logs, `"userID":"alice"`) || !strings.Contains(logs, `"code":"OK"`) {
		t.Fatalf("missing panic or successful-request audit entries: %s", logs)
	}
}
