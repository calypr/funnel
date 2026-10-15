package server

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jwx-go/jwkfetch/v4"
	"github.com/lestrrat-go/httprc/v3"
	"github.com/ohsu-comp-bio/funnel/config"
)

func TestParseJwtSubjectValidatesOIDCTokens(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	forgedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	var jwksRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jwksRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA",
			"use": "sig",
			"kid": "test-key",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	defer server.Close()

	ctx := context.Background()
	cache, err := jwkfetch.NewCache(ctx, httprc.NewClient())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := cache.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shut down JWKS cache: %v", err)
		}
	})
	refreshCtx, refreshCancel := context.WithTimeout(ctx, 5*time.Second)
	defer refreshCancel()
	if err := cache.Register(refreshCtx, server.URL,
		jwkfetch.WithMinInterval(time.Hour),
		jwkfetch.WithWaitReady(false),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Refresh(refreshCtx, server.URL); err != nil {
		t.Fatal(err)
	}
	fetchesBeforeValidation := jwksRequests.Load()
	if fetchesBeforeValidation == 0 {
		t.Fatal("JWKS cache did not fetch the key set")
	}

	c := &OidcConfig{
		local:  &config.OidcAuth{RequireAudience: "funnel", RequireScope: "run"},
		remote: OidcRemoteConfig{Issuer: "https://issuer.example", JwksURI: server.URL},
		jwks:   cache,
	}

	validClaims := map[string]any{
		"iss":   "https://issuer.example",
		"sub":   "alice",
		"aud":   "funnel",
		"scope": "openid run",
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	tests := []struct {
		name    string
		token   string
		wantSub string
	}{
		{name: "valid signature and claims", token: signOIDCTestToken(t, key, validClaims), wantSub: "alice"},
		{name: "forged signature", token: signOIDCTestToken(t, forgedKey, validClaims)},
		{name: "expired", token: signOIDCTestToken(t, key, withOIDCClaim(validClaims, "exp", time.Now().Add(-time.Minute).Unix()))},
		{name: "wrong issuer", token: signOIDCTestToken(t, key, withOIDCClaim(validClaims, "iss", "https://other.example"))},
		{name: "wrong audience", token: signOIDCTestToken(t, key, withOIDCClaim(validClaims, "aud", "other-client"))},
		{name: "scope missing required value", token: signOIDCTestToken(t, key, withOIDCClaim(validClaims, "scope", "openid"))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.ParseJwtSubject(tt.token); got != tt.wantSub {
				t.Fatalf("ParseJwtSubject() = %q, want %q", got, tt.wantSub)
			}
		})
	}

	if got := jwksRequests.Load(); got != fetchesBeforeValidation {
		t.Fatalf("JWKS requests during token validation = %d, want no additional fetch after cache initialization (%d)",
			got-fetchesBeforeValidation, fetchesBeforeValidation)
	}
}

func signOIDCTestToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"test-key","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	input := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func withOIDCClaim(claims map[string]any, name string, value any) map[string]any {
	updated := make(map[string]any, len(claims))
	for key, claim := range claims {
		updated[key] = claim
	}
	updated[name] = value
	return updated
}
