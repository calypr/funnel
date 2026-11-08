// Package tesclient resolves CLI environment settings for the public TES client.
package tesclient

import (
	"net/http"
	"os"

	"github.com/calypr/funnel/tes"
)

func options() []tes.ClientOption {
	if os.Getenv("FUNNEL_ALLOW_INSECURE_CREDENTIALS") == "true" {
		return []tes.ClientOption{tes.WithInsecureCredentials()}
	}
	return nil
}

// New creates a CLI client using FUNNEL_SERVER_* credentials.
func New(address string) (*tes.Client, error) {
	c, err := tes.NewClient(address, options()...)
	if err != nil {
		return nil, err
	}
	c.User = os.Getenv("FUNNEL_SERVER_USER")
	c.Password = os.Getenv("FUNNEL_SERVER_PASSWORD")
	c.Token = os.Getenv("FUNNEL_SERVER_TOKEN")
	return c, nil
}

// DoAuthenticatedRequest applies the CLI's explicit insecure-credential opt-in.
func DoAuthenticatedRequest(client *http.Client, req *http.Request, user, password string) (*http.Response, error) {
	return tes.DoAuthenticatedRequest(client, req, user, password, options()...)
}
