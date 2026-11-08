package tes

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/calypr/funnel/client/auth"
)

// NewClient returns a new HTTP client for accessing
// Create/List/Get/Cancel Task endpoints. "address" is the address
// of the TES server.
func NewClient(address string, options ...ClientOption) (*Client, error) {
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	endpoint, err := url.Parse(address)
	if err != nil {
		return nil, fmt.Errorf("tes: invalid server address: %w", err)
	}
	if (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return nil, fmt.Errorf("tes: server address must be an HTTP(S) URL without query or fragment")
	}
	c := &Client{address: strings.TrimRight(endpoint.String(), "/"), client: &http.Client{Timeout: 60 * time.Second}}
	for _, option := range options {
		option(c)
	}
	if c.client == nil {
		return nil, fmt.Errorf("tes: HTTP client must not be nil")
	}
	return c, nil
}

// ClientOption configures a REST client.
type ClientOption func(*Client)

// WithHTTPClient uses the caller's HTTP client, including its transport and timeout.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) { c.client = client }
}

// WithInsecureCredentials explicitly permits credentials over plaintext HTTP.
// HTTPS-to-HTTP credential redirects are permitted only with this option.
func WithInsecureCredentials() ClientOption {
	return func(c *Client) { c.allowInsecureCredentials = true }
}

// Client represents the HTTP Task client.
type Client struct {
	allowInsecureCredentials bool
	address                  string
	client                   *http.Client
	User                     string
	Password                 string
	Token                    string
}

// do applies authentication and transport policy, including redirects.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	policy := auth.Policy{AllowInsecure: c.allowInsecureCredentials}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
		return policy.Do(c.client, req, "", "")
	}
	return policy.Do(c.client, req, c.User, c.Password)
}
