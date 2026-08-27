package tes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/ohsu-comp-bio/funnel/util"
	"golang.org/x/net/context"
	"google.golang.org/protobuf/encoding/protojson"
)

// NewClient returns a new HTTP client for accessing
// Create/List/Get/Cancel Task endpoints. "address" is the address
// of the TES server.
func NewClient(address string) (*Client, error) {
	user := os.Getenv("FUNNEL_SERVER_USER")
	password := os.Getenv("FUNNEL_SERVER_PASSWORD")

	re := regexp.MustCompile("^(.+://)?(.[^/]+)(.+)?$")
	endpoint := re.ReplaceAllString(address, "$1$2")

	reScheme := regexp.MustCompile("^.+://")
	if reScheme.MatchString(endpoint) {
		if !strings.HasPrefix(endpoint, "http") {
			return nil, fmt.Errorf("invalid protocol: '%s'; expected: 'http://' or 'https://'", reScheme.FindString(endpoint))
		}
	} else {
		endpoint = "http://" + endpoint
	}

	return &Client{
		address: endpoint,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
		Marshaler: &Marshaler,
		User:      user,
		Password:  password,
	}, nil
}

// Client represents the HTTP Task client.
type Client struct {
	address   string
	client    *http.Client
	Marshaler *protojson.MarshalOptions
	User      string
	Password  string
}

// GetTask returns the raw bytes from GET /v1/tasks/{id}
func (c *Client) GetTask(ctx context.Context, req *GetTaskRequest) (*Task, error) {
	// Send request
	u := c.address + "/v1/tasks/" + req.Id + "?view=" + req.View
	hreq, _ := http.NewRequest("GET", u, nil)
	hreq = hreq.WithContext(ctx)
	hreq.SetBasicAuth(c.User, c.Password)
	body, err := util.CheckHTTPResponse(c.client.Do(hreq))
	if err != nil {
		return nil, err
	}
	// Parse response
	resp := &Task{}
	err = protojson.Unmarshal(body, resp)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// ListTasks returns the result of GET /v1/tasks
func (c *Client) ListTasks(ctx context.Context, req *ListTasksRequest) (*ListTasksResponse, error) {
	// Build url query parameters
	v := url.Values{}
	addInt32(v, "page_size", req.GetPageSize())
	addString(v, "page_token", req.GetPageToken())
	addString(v, "view", req.GetView())
	addString(v, "name_prefix", req.GetNamePrefix())

	if req.GetState() != Unknown {
		addString(v, "state", req.State.String())
	}

	for key, val := range req.GetTags() {
		v.Add("tag_key", key)
		v.Add("tag_value", val)
	}

	// Send request
	u := c.address + "/v1/tasks?" + v.Encode()
	hreq, _ := http.NewRequest("GET", u, nil)
	hreq = hreq.WithContext(ctx)
	hreq.SetBasicAuth(c.User, c.Password)
	body, err := util.CheckHTTPResponse(c.client.Do(hreq))
	if err != nil {
		return nil, err
	}
	// Parse response
	resp := &ListTasksResponse{}
	err = protojson.Unmarshal(body, resp)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// CreateTask POSTs a Task message to /v1/tasks
func (c *Client) CreateTask(ctx context.Context, task *Task) (*CreateTaskResponse, error) {
	verr := Validate(task)
	if verr != nil {
		return nil, fmt.Errorf("invalid task message: %v", verr)
	}

	b, err := Marshaler.Marshal(task)
	if err != nil {
		return nil, fmt.Errorf("error marshaling task message: %v", err)
	}

	// Send request
	u := c.address + "/v1/tasks"
	hreq, _ := http.NewRequest("POST", u, bytes.NewReader(b))
	hreq = hreq.WithContext(ctx)
	hreq.Header.Add("Content-Type", "application/json")
	hreq.SetBasicAuth(c.User, c.Password)
	body, err := util.CheckHTTPResponse(c.client.Do(hreq))
	if err != nil {
		return nil, err
	}

	// Parse response
	resp := &CreateTaskResponse{}
	err = protojson.Unmarshal(body, resp)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// CancelTask POSTs to /v1/tasks/{id}:cancel
func (c *Client) CancelTask(ctx context.Context, req *CancelTaskRequest) (*CancelTaskResponse, error) {
	result, err := c.CancelTaskWithMessage(ctx, req)
	if err != nil {
		return nil, err
	}
	return result.Response, nil
}

// CancelTaskResult wraps the response with optional metadata
type CancelTaskResult struct {
	Response *CancelTaskResponse
	Message  string // Informational message from server
}

// CancelTaskWithMessage POSTs to /v1/tasks/{id}:cancel and returns any informational message
func (c *Client) CancelTaskWithMessage(ctx context.Context, req *CancelTaskRequest) (*CancelTaskResult, error) {
	u := c.address + "/v1/tasks/" + req.Id + ":cancel"
	hreq, _ := http.NewRequest("POST", u, nil)
	hreq = hreq.WithContext(ctx)
	hreq.Header.Add("Content-Type", "application/json")
	hreq.SetBasicAuth(c.User, c.Password)

	// Execute request and capture response
	httpResp, err := c.client.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	body, err := util.CheckHTTPResponse(httpResp, err)
	if err != nil {
		return nil, err
	}

	// Check if the response contains a message field (from middleware)
	var rawResponse map[string]interface{}
	if err := json.Unmarshal(body, &rawResponse); err == nil {
		if msg, ok := rawResponse["message"].(string); ok {
			// This is a message response, not a protobuf response
			return &CancelTaskResult{
				Response: &CancelTaskResponse{},
				Message:  msg,
			}, nil
		}
	}

	// Parse as normal protobuf response
	resp := &CancelTaskResponse{}
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

// GetServiceInfo returns result of GET /v1/service-info
func (c *Client) GetServiceInfo(ctx context.Context, req *GetServiceInfoRequest) (*ServiceInfo, error) {
	u := c.address + "/v1/service-info"
	hreq, _ := http.NewRequest("GET", u, nil)
	hreq = hreq.WithContext(ctx)
	hreq.SetBasicAuth(c.User, c.Password)
	body, err := util.CheckHTTPResponse(c.client.Do(hreq))
	if err != nil {
		return nil, err
	}

	// Parse response
	resp := &ServiceInfo{}
	err = protojson.Unmarshal(body, resp)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// WaitForTask polls /v1/tasks/{id} for each Id provided and returns
// once all tasks are in a terminal state.
func (c *Client) WaitForTask(ctx context.Context, taskIDs ...string) error {
	for range time.NewTicker(time.Second * 2).C {
		done := false
		for _, id := range taskIDs {
			r, err := c.GetTask(ctx, &GetTaskRequest{
				Id:   id,
				View: View_MINIMAL.String(),
			})
			if err != nil {
				return err
			}
			switch r.State {
			case State_COMPLETE:
				done = true
			case State_EXECUTOR_ERROR, State_SYSTEM_ERROR, State_CANCELED:
				errMsg := fmt.Sprintf("Task %s exited with state %s", id, r.State.String())
				return errors.New(errMsg)
			default:
				done = false
			}
		}
		if done {
			return nil
		}
	}
	return nil
}
