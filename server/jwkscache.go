package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lestrrat-go/httprc/v3"
	"github.com/lestrrat-go/jwx/v4/jwk"
)

// jwksCache keeps a JWK Set hot for a registered URL, refreshing it in the
// background via httprc. This is a minimal replacement for jwx-go/jwkfetch,
// covering funnel's single-JWKS usage.
type jwksCache struct {
	url  string
	ctrl httprc.Controller
	r    *httprc.ResourceBase[jwk.Set]
}

const jwksMaxBodySize = 10 << 20 // 10MB

type jwksTransformer struct{}

func (jwksTransformer) Transform(_ context.Context, res *http.Response) (jwk.Set, error) {
	buf, err := io.ReadAll(io.LimitReader(res.Body, jwksMaxBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read JWKS response body: %w", err)
	}
	if int64(len(buf)) > jwksMaxBodySize {
		return nil, fmt.Errorf("JWKS response body too large (limit %d bytes)", jwksMaxBodySize)
	}
	set, err := jwk.Parse(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JWKS: %w", err)
	}
	return set, nil
}

// newJWKSCache fetches the JWKS and starts background refreshing at the given URL.
// Callers must invoke Shutdown when done.
func newJWKSCache(ctx context.Context, jwksURL string) (*jwksCache, error) {
	return newJWKSCacheWithInterval(ctx, jwksURL, 15*time.Minute)
}

func newJWKSCacheWithInterval(ctx context.Context, jwksURL string, minInterval time.Duration) (*jwksCache, error) {
	ctrl, err := httprc.NewClient().Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start httprc client: %w", err)
	}
	r, err := httprc.NewResource[jwk.Set](jwksURL, jwksTransformer{},
		httprc.WithMinInterval(minInterval),
	)
	if err != nil {
		_ = ctrl.Shutdown(time.Second)
		return nil, fmt.Errorf("failed to create JWKS resource: %w", err)
	}
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ctrl.Add(initCtx, r); err != nil {
		_ = ctrl.Shutdown(time.Second)
		return nil, fmt.Errorf("failed to register JWKS %q: %w", jwksURL, err)
	}
	return &jwksCache{url: jwksURL, ctrl: ctrl, r: r}, nil
}

// Fetch returns the cached JWK Set for the registered URL.
func (c *jwksCache) Fetch(_ context.Context, u string) (jwk.Set, error) {
	if u != c.url {
		return nil, fmt.Errorf("jwksCache: url %q is not registered", u)
	}
	set := c.r.Resource()
	if set == nil {
		return nil, fmt.Errorf("jwksCache: JWKS %q is not ready", u)
	}
	return set, nil
}

// Refresh synchronously re-fetches the JWKS and returns the fresh set.
func (c *jwksCache) Refresh(ctx context.Context) (jwk.Set, error) {
	if err := c.r.Sync(ctx); err != nil {
		return nil, err
	}
	return c.r.Resource(), nil
}

// Shutdown stops the background refresh workers.
func (c *jwksCache) Shutdown(ctx context.Context) error {
	timeout := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			timeout = time.Millisecond
		}
	}
	return c.ctrl.Shutdown(timeout)
}
