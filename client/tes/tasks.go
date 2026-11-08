package tes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/calypr/funnel/api/tes"
)

// GetTask returns the raw bytes from GET /v1/tasks/{id}
func (c *Client) GetTask(ctx context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	// Send request
	u := c.address + "/v1/tasks/" + url.PathEscape(req.Id) + "?" + url.Values{"view": []string{req.View}}.Encode()
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("tes: creating GET request: %w", err)
	}
	body, err := checkHTTPResponse(c.do(hreq))
	if err != nil {
		return nil, err
	}
	// Parse response
	resp := &tes.Task{}
	err = protojson.Unmarshal(body, resp)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// ListTasks returns the result of GET /v1/tasks
func (c *Client) ListTasks(ctx context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	// Build url query parameters
	v := url.Values{}
	addInt32(v, "page_size", req.GetPageSize())
	addString(v, "page_token", req.GetPageToken())
	addString(v, "view", req.GetView())
	addString(v, "name_prefix", req.GetNamePrefix())

	if req.GetState() != tes.Unknown {
		addString(v, "state", req.State.String())
	}

	for key, val := range req.GetTags() {
		v.Add("tag_key", key)
		v.Add("tag_value", val)
	}

	// Send request
	u := c.address + "/v1/tasks?" + v.Encode()
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("tes: creating GET request: %w", err)
	}
	body, err := checkHTTPResponse(c.do(hreq))
	if err != nil {
		return nil, err
	}
	// Parse response
	resp := &tes.ListTasksResponse{}
	err = protojson.Unmarshal(body, resp)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// CreateTask POSTs a Task message to /v1/tasks
func (c *Client) CreateTask(ctx context.Context, task *tes.Task) (*tes.CreateTaskResponse, error) {
	b, err := (protojson.MarshalOptions{Indent: "  "}).Marshal(task)
	if err != nil {
		return nil, fmt.Errorf("error marshaling task message: %v", err)
	}

	// Send request
	u := c.address + "/v1/tasks"
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("tes: creating POST request: %w", err)
	}
	hreq.Header.Add("Content-Type", "application/json")
	body, err := checkHTTPResponse(c.do(hreq))
	if err != nil {
		return nil, err
	}

	// Parse response
	resp := &tes.CreateTaskResponse{}
	err = protojson.Unmarshal(body, resp)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// CancelTask POSTs to /v1/tasks/{id}:cancel
func (c *Client) CancelTask(ctx context.Context, req *tes.CancelTaskRequest) (*tes.CancelTaskResponse, error) {
	result, err := c.CancelTaskWithMessage(ctx, req)
	if err != nil {
		return nil, err
	}
	return result.Response, nil
}

// CancelTaskResult wraps the response with optional metadata
type CancelTaskResult struct {
	Response *tes.CancelTaskResponse
	Message  string // Informational message from server
}

// CancelTaskWithMessage POSTs to /v1/tasks/{id}:cancel and returns any informational message
func (c *Client) CancelTaskWithMessage(ctx context.Context, req *tes.CancelTaskRequest) (*CancelTaskResult, error) {
	u := c.address + "/v1/tasks/" + url.PathEscape(req.Id) + ":cancel"
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return nil, fmt.Errorf("tes: creating POST request: %w", err)
	}
	hreq.Header.Add("Content-Type", "application/json")
	// Execute request and capture response
	httpResp, err := c.do(hreq)
	if err != nil {
		return nil, err
	}

	body, err := checkHTTPResponse(httpResp, err)
	if err != nil {
		return nil, err
	}

	// Check if the response contains a message field (from middleware)
	var rawResponse map[string]interface{}
	if err := json.Unmarshal(body, &rawResponse); err == nil {
		if msg, ok := rawResponse["message"].(string); ok {
			// This is a message response, not a protobuf response
			return &CancelTaskResult{
				Response: &tes.CancelTaskResponse{},
				Message:  msg,
			}, nil
		}
	}

	// Parse as normal protobuf response
	resp := &tes.CancelTaskResponse{}
	err = protojson.Unmarshal(body, resp)
	if err != nil {
		return nil, err
	}

	// Check for message in header (fallback for direct gRPC)
	message := httpResp.Header.Get("Grpc-Metadata-X-Funnel-Message")

	return &CancelTaskResult{
		Response: resp,
		Message:  message,
	}, nil
}
