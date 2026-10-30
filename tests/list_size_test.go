package tests

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/database/boltdb"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

func TestListTasksLargeResponses(t *testing.T) {
	for _, payload := range []string{"executor logs", "output file logs"} {
		t.Run(payload, func(t *testing.T) {
			db, err := boltdb.NewBoltDB(&config.BoltDB{Path: filepath.Join(t.TempDir(), "tasks.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.Init(); err != nil {
				t.Fatal(err)
			}
			task := &tes.Task{Id: "large-task", State: tes.State_COMPLETE, CreationTime: "2026-01-01T00:00:00Z"}
			task.Executors = []*tes.Executor{{Image: "alpine", Command: []string{"true"}}}
			logs := &tes.TaskLog{}
			if payload == "executor logs" {
				logs.Logs = []*tes.ExecutorLog{{Stdout: strings.Repeat("x", 5*1024*1024)}}
			} else {
				for i := 0; i < 12000; i++ {
					logs.Outputs = append(logs.Outputs, &tes.OutputFileLog{Path: fmt.Sprintf("/outputs/%05d-%s", i, strings.Repeat("x", 200)), Url: "s3://bucket/" + strings.Repeat("y", 200), SizeBytes: "123"})
				}
			}
			task.Logs = []*tes.TaskLog{logs}
			if err := db.WriteEvent(context.Background(), events.NewTaskCreated(task)); err != nil {
				t.Fatal(err)
			}
			if payload == "executor logs" {
				if err := db.WriteEvent(context.Background(), &events.Event{Id: task.Id, Type: events.Type_EXECUTOR_START_TIME, Data: &events.Event_StartTime{StartTime: "2026-01-01T00:00:00Z"}}); err != nil {
					t.Fatal(err)
				}
				if err := db.WriteEvent(context.Background(), &events.Event{Id: task.Id, Type: events.Type_EXECUTOR_STDOUT, Data: &events.Event_Stdout{Stdout: logs.Logs[0].Stdout}}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := db.WriteEvent(context.Background(), events.NewOutputs(task.Id, 0, logs.Outputs)); err != nil {
					t.Fatal(err)
				}
			}
			listener := bufconn.Listen(1024 * 1024)
			rpc := grpc.NewServer(grpc.MaxSendMsgSize(8*1024*1024), grpc.UnaryInterceptor(server.NewAuthentication(nil, nil, server.AccessAll).Interceptor))
			serviceConf := config.DefaultConfig()
			tes.RegisterTaskServiceServer(rpc, &server.TaskService{Read: db, Config: serviceConf})
			serveDone := make(chan error, 1)
			go func() { serveDone <- rpc.Serve(listener) }()
			defer func() {
				rpc.Stop()
				if err := <-serveDone; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
					t.Errorf("serve list size test RPC: %v", err)
				}
			}()
			connection, err := grpc.NewClient("passthrough:///list-size", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := connection.Close(); err != nil {
					t.Errorf("close list size test connection: %v", err)
				}
			}()
			client := tes.NewTaskServiceClient(connection)
			for _, view := range []string{"MINIMAL", "BASIC", "FULL"} {
				t.Run(view, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					req := &tes.ListTasksRequest{View: view, PageSize: 1}
					direct, err := db.ListTasks(tes.WithTaskPageBytes(context.Background(), 8*1024*1024), req)
					if err != nil {
						t.Fatal(err)
					}
					t.Logf("actual stored-task response: %d protobuf bytes", proto.Size(direct))
					response, err := client.ListTasks(ctx, req)
					oversized := view == "FULL" || (payload == "output file logs" && view == "BASIC")
					if oversized {
						if status.Code(err) != codes.ResourceExhausted || !strings.Contains(err.Error(), "large-task") || !strings.Contains(err.Error(), "MINIMAL") {
							t.Fatalf("expected actionable size error: %v", err)
						}
						_, getErr := client.GetTask(ctx, &tes.GetTaskRequest{Id: "large-task", View: view})
						if status.Code(getErr) != codes.ResourceExhausted {
							t.Fatalf("expected bounded GetTask error, got %v", getErr)
						}
						serviceConf.Server.MaxResponseBytes = 8 * 1024 * 1024
						defer func() { serviceConf.Server.MaxResponseBytes = 0 }()
						response, err = client.ListTasks(ctx, req, grpc.MaxCallRecvMsgSize(8*1024*1024))
						if _, getErr := client.GetTask(ctx, &tes.GetTaskRequest{Id: "large-task", View: view}, grpc.MaxCallRecvMsgSize(8*1024*1024)); getErr != nil {
							t.Fatalf("configured GetTask failed: %v", getErr)
						}
					}
					if err != nil {
						t.Fatalf("listing with supported limit: %v", err)
					}
					if len(response.Tasks) != 1 {
						t.Fatalf("got %d tasks", len(response.Tasks))
					}
				})
			}
		})
	}
}

func TestLargeListThroughConfiguredHTTPGateway(t *testing.T) {
	db, err := boltdb.NewBoltDB(&config.BoltDB{Path: filepath.Join(t.TempDir(), "tasks.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	task := &tes.Task{Id: "gateway-large", State: tes.State_COMPLETE}
	if err := db.WriteEvent(t.Context(), events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteEvent(t.Context(), events.NewOutputs(task.Id, 0, []*tes.OutputFileLog{{Path: "/result", Url: strings.Repeat("x", 5*1024*1024)}})); err != nil {
		t.Fatal(err)
	}
	f := newServerTestFunnel(t, freeTCPPort(t, "127.0.0.1"), freeTCPPort(t, ""))
	f.Conf.Server.MaxResponseBytes = 8 * 1024 * 1024
	f.Server.MaxResponseBytes = f.Conf.Server.ResponseBytes()
	f.Server.Tasks = &server.TaskService{Read: db, Config: f.Conf}
	t.Cleanup(f.Cleanup)
	f.StartServer()
	client, err := tes.NewClient(f.Conf.Server.HTTPAddress())
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.ListTasks(t.Context(), &tes.ListTasksRequest{View: "BASIC", PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Tasks) != 1 || len(response.Tasks[0].Logs[0].Outputs[0].Url) != 5*1024*1024 {
		t.Fatal("gateway did not preserve large task")
	}
}
