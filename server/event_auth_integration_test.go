package server_test

import (
	"context"
	"encoding/base64"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/database/boltdb"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestServerRejectsCrossOwnerEventsAndClaims(t *testing.T) {
	db, err := boltdb.NewBoltDB(&config.BoltDB{Path: filepath.Join(t.TempDir(), "tasks.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.Init()
	if err != nil {
		t.Fatal(err)
	}
	alice := context.WithValue(context.Background(), server.UserInfoKey, &server.UserInfo{Username: "alice"})
	err = db.WriteEvent(alice, events.NewTaskCreated(&tes.Task{Id: "owned-task", State: tes.Queued}))
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := lis.Addr().String()
	err = lis.Close()
	if err != nil {
		t.Fatal(err)
	}
	log := logger.NewLogger("auth-integration", logger.DefaultConfig())
	log.Discard()
	srv := &server.Server{RPCAddress: address, HTTPPort: "0", Log: log, Tasks: &server.TaskService{Read: db, Log: log, Config: config.DefaultConfig()}, Events: &events.Service{Writer: db, Claimer: db}, TaskAccess: server.AccessOwner, BasicAuth: []*config.BasicCredential{{User: "alice", Password: "synthetic"}, {User: "bob", Password: "synthetic"}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server did not stop")
		}
	}()
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := conn.Close()
		if err != nil {
			t.Error(err)
		}
	}()
	client := events.NewEventServiceClient(conn)
	for _, user := range []string{"bob", "alice"} {
		requestCtx, requestCancel := context.WithTimeout(context.Background(), 10*time.Second)
		requestCtx = metadata.NewOutgoingContext(requestCtx, metadata.Pairs("authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":synthetic"))))
		_, err = client.WriteEvent(requestCtx, events.NewSystemLog("owned-task", 0, 0, "info", "marker-"+user, nil), grpc.WaitForReady(true))
		if user == "alice" && err != nil {
			requestCancel()
			t.Fatalf("owner event rejected: %v", err)
		}
		if user == "bob" && status.Code(err) != codes.PermissionDenied {
			requestCancel()
			t.Fatalf("cross-owner result: %v", err)
		}
		claim, claimErr := client.ClaimExecution(requestCtx, &events.ClaimExecutionRequest{TaskId: "owned-task"}, grpc.WaitForReady(true))
		requestCancel()
		if user == "alice" && (claimErr != nil || !claim.GetClaimed()) {
			t.Fatalf("owner claim rejected: response=%v error=%v", claim, claimErr)
		}
		if user == "bob" && status.Code(claimErr) != codes.PermissionDenied {
			t.Fatalf("cross-owner claim result: %v", claimErr)
		}
	}
	task, err := db.GetTask(alice, &tes.GetTaskRequest{Id: "owned-task", View: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Logs) != 1 || len(task.Logs[0].SystemLogs) != 1 {
		t.Fatalf("unexpected stored events: %v", task.Logs)
	}
}
