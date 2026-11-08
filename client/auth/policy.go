// Package auth enforces credential transport rules for outbound API requests.
package auth

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Policy controls whether credentials may be sent over plaintext HTTP.
// The default permits HTTPS and loopback HTTP, and rejects HTTPS downgrades.
type Policy struct{ AllowInsecure bool }

// Do applies Basic authentication and enforces the policy on requests and redirects.
func (p Policy) Do(httpClient *http.Client, req *http.Request, user, password string) (*http.Response, error) {
	allowInsecure := p.AllowInsecure
	if user != "" || password != "" {
		req.SetBasicAuth(user, password)
	}
	client := *httpClient
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = credentialTransport{base: transport, allowInsecure: allowInsecure}
	checkRedirect := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		hasCredentials := req.Header.Get("Authorization") != "" || req.URL.User != nil || next.Header.Get("Authorization") != "" || next.URL.User != nil
		if hasCredentials && !allowInsecure {
			if next.URL.Scheme != "https" && via[len(via)-1].URL.Scheme == "https" {
				return errors.New("auth: refusing credential redirect from HTTPS to HTTP")
			}
			err := checkCredentialURL(next.URL, allowInsecure)
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

type credentialTransport struct {
	base          http.RoundTripper
	allowInsecure bool
}

func (t credentialTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Authorization") != "" || req.URL.User != nil {
		err := checkCredentialURL(req.URL, t.allowInsecure)
		if err != nil {
			return nil, err
		}
	}
	return t.base.RoundTrip(req)
}

func checkCredentialURL(u *url.URL, allowInsecure bool) error {
	if u.Scheme == "https" || allowInsecure {
		return nil
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if u.Scheme == "http" && (strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return errors.New("auth: refusing credentials over remote plaintext HTTP; use HTTPS or explicitly opt in to plaintext credentials")
}
