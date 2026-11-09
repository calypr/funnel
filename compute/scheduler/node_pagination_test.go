package scheduler

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestParseNodePage(t *testing.T) {
	tests := []struct {
		name     string
		request  *ListNodesRequest
		wantPage NodePage
		wantCode codes.Code
	}{
		{name: "nil request uses default", wantPage: NodePage{Limit: DefaultNodePageSize}},
		{name: "zero size uses default", request: &ListNodesRequest{}, wantPage: NodePage{Limit: DefaultNodePageSize}},
		{name: "minimum size", request: &ListNodesRequest{PageSize: 1}, wantPage: NodePage{Limit: 1}},
		{name: "maximum size", request: &ListNodesRequest{PageSize: MaxNodePageSize}, wantPage: NodePage{Limit: MaxNodePageSize}},
		{name: "negative size", request: &ListNodesRequest{PageSize: -1}, wantCode: codes.InvalidArgument},
		{name: "oversized", request: &ListNodesRequest{PageSize: MaxNodePageSize + 1}, wantCode: codes.InvalidArgument},
		{name: "invalid version", request: &ListNodesRequest{PageToken: "v2.YQ"}, wantCode: codes.InvalidArgument},
		{name: "invalid encoding", request: &ListNodesRequest{PageToken: "v1.!"}, wantCode: codes.InvalidArgument},
		{name: "empty cursor", request: &ListNodesRequest{PageToken: "v1."}, wantCode: codes.InvalidArgument},
		{name: "binary cursor", request: &ListNodesRequest{PageToken: "v1._w"}, wantCode: codes.InvalidArgument},
		{name: "NUL cursor", request: &ListNodesRequest{PageToken: "v1.AA"}, wantCode: codes.InvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			page, err := ParseNodePage(test.request)
			if test.wantCode != codes.OK {
				if status.Code(err) != test.wantCode {
					t.Fatalf("ParseNodePage error = %v, want %s", err, test.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseNodePage: %v", err)
			}
			if page != test.wantPage {
				t.Fatalf("ParseNodePage = %+v, want %+v", page, test.wantPage)
			}
		})
	}

	request := &ListNodesRequest{PageSize: 17, PageToken: encodeNodePageToken("node/ümlaut")}
	page, err := ParseNodePage(request)
	if err != nil {
		t.Fatal(err)
	}
	if page != (NodePage{AfterID: "node/ümlaut", Limit: 17}) {
		t.Fatalf("decoded page = %+v", page)
	}
}

func TestNewNodePageResponseUsesLookahead(t *testing.T) {
	nodes := []*Node{{Id: "a"}, {Id: "b"}, {Id: "c"}, {Id: "d"}, {Id: "e"}}
	first := NewNodePageResponse(nodes, 2)
	if len(first.Nodes) != 2 || first.Nodes[0].Id != "a" || first.Nodes[1].Id != "b" {
		t.Fatalf("first page nodes = %v", first.Nodes)
	}
	page, err := ParseNodePage(&ListNodesRequest{PageToken: first.NextPageToken})
	if err != nil || page.AfterID != "b" {
		t.Fatalf("first page token decodes to (%+v, %v); want cursor b", page, err)
	}

	second := NewNodePageResponse(nodes[2:], 2)
	if len(second.Nodes) != 2 || second.NextPageToken == "" {
		t.Fatalf("second page = nodes %v token %q; want 2 nodes and continuation", second.Nodes, second.NextPageToken)
	}
	third := NewNodePageResponse(nodes[4:], 2)
	if len(third.Nodes) != 1 || third.NextPageToken != "" {
		t.Fatalf("partial final page = nodes %v token %q", third.Nodes, third.NextPageToken)
	}

	exactFirst := NewNodePageResponse(nodes[:3], 2)
	if len(exactFirst.Nodes) != 2 || exactFirst.NextPageToken == "" {
		t.Fatalf("exact-multiple first page = nodes %v token %q", exactFirst.Nodes, exactFirst.NextPageToken)
	}
	exactLast := NewNodePageResponse(nodes[3:], 2)
	if len(exactLast.Nodes) != 2 || exactLast.NextPageToken != "" {
		t.Fatalf("exact-multiple final page = nodes %v token %q", exactLast.Nodes, exactLast.NextPageToken)
	}
}

type nodeListerFunc func(context.Context, *ListNodesRequest) (*ListNodesResponse, error)

func (f nodeListerFunc) ListNodes(ctx context.Context, req *ListNodesRequest) (*ListNodesResponse, error) {
	return f(ctx, req)
}

func TestListAllNodes(t *testing.T) {
	t.Run("follows pages and uses bounded maximum", func(t *testing.T) {
		calls := 0
		lister := nodeListerFunc(func(_ context.Context, req *ListNodesRequest) (*ListNodesResponse, error) {
			calls++
			if req.PageSize != MaxNodePageSize {
				t.Fatalf("page_size = %d, want %d", req.PageSize, MaxNodePageSize)
			}
			switch calls {
			case 1:
				if req.PageToken != "" {
					t.Fatalf("first page token = %q", req.PageToken)
				}
				return &ListNodesResponse{Nodes: []*Node{{Id: "a"}}, NextPageToken: "cursor-a"}, nil
			case 2:
				if req.PageToken != "cursor-a" {
					t.Fatalf("second page token = %q", req.PageToken)
				}
				return &ListNodesResponse{Nodes: []*Node{{Id: "b"}}}, nil
			default:
				t.Fatalf("unexpected ListNodes call %d", calls)
				return nil, nil
			}
		})
		nodes, err := ListAllNodes(context.Background(), lister)
		if err != nil {
			t.Fatal(err)
		}
		if calls != 2 || len(nodes) != 2 || nodes[0].Id != "a" || nodes[1].Id != "b" {
			t.Fatalf("ListAllNodes = %v after %d calls", nodes, calls)
		}
	})

	t.Run("later page failure returns no partial nodes", func(t *testing.T) {
		calls := 0
		failure := errors.New("later page failed")
		lister := nodeListerFunc(func(_ context.Context, _ *ListNodesRequest) (*ListNodesResponse, error) {
			calls++
			if calls == 1 {
				return &ListNodesResponse{Nodes: []*Node{{Id: "partial"}}, NextPageToken: "cursor"}, nil
			}
			return nil, failure
		})
		nodes, err := ListAllNodes(context.Background(), lister)
		if !errors.Is(err, failure) || nodes != nil {
			t.Fatalf("ListAllNodes = (%v, %v); want (nil, later-page error)", nodes, err)
		}
	})

	t.Run("repeated token stops traversal", func(t *testing.T) {
		calls := 0
		lister := nodeListerFunc(func(_ context.Context, _ *ListNodesRequest) (*ListNodesResponse, error) {
			calls++
			return &ListNodesResponse{Nodes: []*Node{{Id: fmt.Sprint(calls)}}, NextPageToken: "same"}, nil
		})
		nodes, err := ListAllNodes(context.Background(), lister)
		if err == nil || nodes != nil || calls != 2 {
			t.Fatalf("ListAllNodes = (%v, %v) after %d calls; want nonprogress error and nil nodes", nodes, err, calls)
		}
	})

	t.Run("continuation without nodes stops traversal", func(t *testing.T) {
		lister := nodeListerFunc(func(context.Context, *ListNodesRequest) (*ListNodesResponse, error) {
			return &ListNodesResponse{NextPageToken: "cursor"}, nil
		})
		nodes, err := ListAllNodes(context.Background(), lister)
		if err == nil || nodes != nil {
			t.Fatalf("ListAllNodes = (%v, %v); want no-progress error and nil nodes", nodes, err)
		}
	})

	t.Run("canceled context avoids request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		called := false
		lister := nodeListerFunc(func(context.Context, *ListNodesRequest) (*ListNodesResponse, error) {
			called = true
			return &ListNodesResponse{}, nil
		})
		nodes, err := ListAllNodes(ctx, lister)
		if !errors.Is(err, context.Canceled) || nodes != nil || called {
			t.Fatalf("ListAllNodes = (%v, %v), called=%v; want canceled error without request", nodes, err, called)
		}
	})
}
