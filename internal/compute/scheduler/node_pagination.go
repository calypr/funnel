package scheduler

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	DefaultNodePageSize = 500
	MaxNodePageSize     = 1000
)

// NodePage contains the validated storage query for a ListNodes request.
type NodePage struct {
	AfterID string
	Limit   int
}

// ParseNodePage validates a ListNodes request and decodes its continuation token.
func ParseNodePage(req *ListNodesRequest) (NodePage, error) {
	page := NodePage{Limit: DefaultNodePageSize}
	if req == nil {
		return page, nil
	}

	if req.PageSize < 0 || req.PageSize > MaxNodePageSize {
		return NodePage{}, status.Errorf(codes.InvalidArgument,
			"page_size must be between 0 and %d", MaxNodePageSize)
	}
	if req.PageSize > 0 {
		page.Limit = int(req.PageSize)
	}
	if req.PageToken == "" {
		return page, nil
	}

	version, encodedID, ok := strings.Cut(req.PageToken, ".")
	if !ok || version != "v1" || encodedID == "" {
		return NodePage{}, status.Error(codes.InvalidArgument, "invalid page_token")
	}
	lastID, err := base64.RawURLEncoding.DecodeString(encodedID)
	if err != nil || len(lastID) == 0 || !utf8.Valid(lastID) || strings.ContainsRune(string(lastID), '\x00') {
		return NodePage{}, status.Error(codes.InvalidArgument, "invalid page_token")
	}
	page.AfterID = string(lastID)
	return page, nil
}

// NewNodePageResponse trims a lookahead node and returns a cursor when another
// page exists. Store implementations must fetch at most limit+1 nodes.
func NewNodePageResponse(nodes []*Node, limit int) *ListNodesResponse {
	if len(nodes) <= limit {
		return &ListNodesResponse{Nodes: nodes}
	}
	page := nodes[:limit]
	return &ListNodesResponse{
		Nodes:         page,
		NextPageToken: encodeNodePageToken(page[len(page)-1].GetId()),
	}
}

func encodeNodePageToken(lastID string) string {
	return "v1." + base64.RawURLEncoding.EncodeToString([]byte(lastID))
}

// NodeLister is the node-listing surface needed by ListAllNodes.
type NodeLister interface {
	ListNodes(context.Context, *ListNodesRequest) (*ListNodesResponse, error)
}

// ListAllNodes returns every node, or nil if any page cannot be read.
func ListAllNodes(ctx context.Context, source NodeLister) ([]*Node, error) {
	var nodes []*Node
	pageToken := ""
	seenTokens := make(map[string]struct{})
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("listing nodes: %w", err)
		}
		resp, err := source.ListNodes(ctx, &ListNodesRequest{
			PageSize:  MaxNodePageSize,
			PageToken: pageToken,
		})
		if err != nil {
			return nil, fmt.Errorf("listing nodes: %w", err)
		}
		if resp == nil {
			return nil, fmt.Errorf("listing nodes: empty response")
		}
		nodes = append(nodes, resp.Nodes...)
		if resp.NextPageToken == "" {
			return nodes, nil
		}
		if len(resp.Nodes) == 0 {
			return nil, fmt.Errorf("listing nodes: continuation token without nodes")
		}
		if _, seen := seenTokens[resp.NextPageToken]; seen {
			return nil, fmt.Errorf("listing nodes: repeated continuation token")
		}
		seenTokens[resp.NextPageToken] = struct{}{}
		pageToken = resp.NextPageToken
	}
}
