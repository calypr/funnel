package datastore

import (
	"context"
	"net"
	"strings"
	"testing"

	cloud "cloud.google.com/go/datastore"
	pb "cloud.google.com/go/datastore/apiv1/datastorepb"
	"github.com/calypr/funnel/tes"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type budgetDatastoreServer struct {
	pb.UnimplementedDatastoreServer
}

func (budgetDatastoreServer) RunQuery(_ context.Context, req *pb.RunQueryRequest) (*pb.RunQueryResponse, error) {
	var results []*pb.EntityResult
	start := string(req.GetQuery().StartCursor)
	for _, id := range []string{"c", "b", "a"} {
		if start != "" && id >= start {
			continue
		}
		value := func(s string) *pb.Value { return &pb.Value{ValueType: &pb.Value_StringValue{StringValue: s}} }
		results = append(results, &pb.EntityResult{Cursor: []byte(id), Entity: &pb.Entity{Key: &pb.Key{PartitionId: &pb.PartitionId{ProjectId: "test"}, Path: []*pb.Key_PathElement{{Kind: "Task", IdType: &pb.Key_PathElement_Name{Name: id}}}}, Properties: map[string]*pb.Value{"Id": value(id), "Name": value(strings.Repeat("x", 150))}}})
	}
	return &pb.RunQueryResponse{Batch: &pb.QueryResultBatch{EntityResults: results, MoreResults: pb.QueryResultBatch_NO_MORE_RESULTS}}, nil
}
func TestDatastoreByteBudgetResumes(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	rpc := grpc.NewServer()
	pb.RegisterDatastoreServer(rpc, budgetDatastoreServer{})
	go func() { _ = rpc.Serve(listener) }()
	defer rpc.Stop()
	defer listener.Close()
	conn, err := grpc.NewClient("passthrough:///datastore", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client, err := cloud.NewClient(t.Context(), "test", option.WithGRPCConn(conn), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	db := &Datastore{client: client}
	ctx := tes.WithTaskPageBytes(t.Context(), 230)
	token := ""
	for _, want := range []string{"c", "b", "a"} {
		page, err := db.ListTasks(ctx, &tes.ListTasksRequest{View: "BASIC", PageSize: 3, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Tasks) != 1 || page.Tasks[0].Id != want {
			t.Fatalf("expected %s got %v", want, page)
		}
		token = page.NextPageToken
	}
	if token != "" {
		t.Fatalf("unexpected cursor %q", token)
	}
	_, err = db.ListTasks(tes.WithTaskPageBytes(t.Context(), 20), &tes.ListTasksRequest{View: "BASIC"})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("got %v", err)
	}
}
