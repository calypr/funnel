package tes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
)

// NewClient returns a new HTTP client for accessing
// Create/List/Get/Cancel Task endpoints. "address" is the address
// of the TES server.
func NewClient(address string) (*Client, error) {
	user := os.Getenv("FUNNEL_SERVER_USER")
	password := os.Getenv("FUNNEL_SERVER_PASSWORD")
	token := os.Getenv("FUNNEL_SERVER_TOKEN")

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
		Token:     token,
	}, nil
}

// Client represents the HTTP Task client.
type Client struct {
	address   string
	client    *http.Client
	Marshaler *protojson.MarshalOptions
	User      string
	Password  string
	Token     string
}

// do applies the credential policy at send time, including redirected requests.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
		return DoAuthenticatedRequest(c.client, req, "", "")
	}
	return DoAuthenticatedRequest(c.client, req, c.User, c.Password)
}

// DoAuthenticatedRequest sends a request with the same credential transport policy
// as the TES client, including checks on redirected requests.
func DoAuthenticatedRequest(httpClient *http.Client, req *http.Request, user, password string) (*http.Response, error) {
	if user != "" || password != "" {
		req.SetBasicAuth(user, password)
	}
	client := *httpClient
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = credentialTransport{base: transport}
	checkRedirect := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		hasCredentials := req.Header.Get("Authorization") != "" || req.URL.User != nil || next.Header.Get("Authorization") != "" || next.URL.User != nil
		if hasCredentials && os.Getenv("FUNNEL_ALLOW_INSECURE_CREDENTIALS") != "true" {
			if next.URL.Scheme != "https" && via[len(via)-1].URL.Scheme == "https" {
				return errors.New("tes: refusing credential redirect from HTTPS to HTTP")
			}
			err := checkCredentialURL(next.URL)
			if err != nil {
				return err
			}
		}
		if checkRedirect != nil {
			return checkRedirect(next, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return client.Do(req)
}

type credentialTransport struct{ base http.RoundTripper }

func (t credentialTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Authorization") != "" || req.URL.User != nil {
		err := checkCredentialURL(req.URL)
		if err != nil {
			return nil, err
		}
	}
	return t.base.RoundTrip(req)
}

func checkCredentialURL(u *url.URL) error {
	if u.Scheme == "https" || os.Getenv("FUNNEL_ALLOW_INSECURE_CREDENTIALS") == "true" {
		return nil
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if u.Scheme == "http" && (strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return errors.New("tes: refusing credentials over remote plaintext HTTP; use HTTPS or explicitly set FUNNEL_ALLOW_INSECURE_CREDENTIALS=true for a trusted private deployment")
}

// GetTask returns the raw bytes from GET /v1/tasks/{id}
func (c *Client) GetTask(ctx context.Context, req *GetTaskRequest) (*Task, error) {
	// Send request
	u := c.address + "/v1/tasks/" + req.Id + "?view=" + req.View
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("tes: creating GET request: %w", err)
	}
	body, err := CheckHTTPResponse(c.do(hreq))
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
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("tes: creating GET request: %w", err)
	}
	body, err := CheckHTTPResponse(c.do(hreq))
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
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("tes: creating POST request: %w", err)
	}
	hreq.Header.Add("Content-Type", "application/json")
	body, err := CheckHTTPResponse(c.do(hreq))
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
	defer httpResp.Body.Close() //nolint:errcheck // The response body is read-only cleanup after response handling.

	body, err := CheckHTTPResponse(httpResp, err)
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
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("tes: creating GET request: %w", err)
	}
	body, err := CheckHTTPResponse(c.do(hreq))
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
