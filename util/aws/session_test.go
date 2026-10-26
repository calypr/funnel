package aws

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/calypr/funnel/config"
)

func TestAWSConfigCredentials(t *testing.T) {
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_ACCESS_KEY_ID", "environment-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "environment-secret")
	t.Setenv("AWS_SESSION_TOKEN", "environment-token")
	for _, test := range []struct {
		name       string
		config     *config.AWSConfig
		key, token string
		anonymous  bool
	}{
		{name: "default chain", config: &config.AWSConfig{}, key: "environment-key", token: "environment-token"},
		{name: "disabled chain", config: &config.AWSConfig{DisableAutoCredentialLoad: true}, anonymous: true},
		{name: "explicit keys", config: &config.AWSConfig{Key: "explicit-key", Secret: "explicit-secret"}, key: "explicit-key"},
		{name: "explicit keys with disabled chain", config: &config.AWSConfig{Key: "explicit-key", Secret: "explicit-secret", DisableAutoCredentialLoad: true}, key: "explicit-key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := NewAWSConfig(t.Context(), test.config)
			if err != nil {
				t.Fatal(err)
			}
			if test.anonymous {
				if !aws.IsCredentialsProvider(cfg.Credentials, aws.AnonymousCredentials{}) {
					t.Fatalf("credentials = %T, want anonymous", cfg.Credentials)
				}
				return
			}
			value, err := cfg.Credentials.Retrieve(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if value.AccessKeyID != test.key || value.SessionToken != test.token {
				t.Fatal("credential source or session token changed")
			}
		})
	}
}

func TestAWSConfigSharedCredentialsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	credentials := filepath.Join(dir, "credentials")
	if err := os.WriteFile(credentials, []byte("[default]\naws_access_key_id = shared-key\naws_secret_access_key = shared-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentials)
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_REGION", "us-west-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	cfg, err := NewAWSConfig(t.Context(), &config.AWSConfig{Region: "eu-west-1", Endpoint: "localhost:9000", MaxRetries: 4})
	if err != nil {
		t.Fatal(err)
	}
	value, err := cfg.Credentials.Retrieve(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if value.AccessKeyID != "shared-key" {
		t.Fatal("shared credentials were not loaded")
	}
	if cfg.Region != "eu-west-1" || aws.ToString(cfg.BaseEndpoint) != "http://localhost:9000" || cfg.RetryMaxAttempts != 5 {
		t.Fatalf("overrides = region %q, endpoint %q, attempts %d", cfg.Region, aws.ToString(cfg.BaseEndpoint), cfg.RetryMaxAttempts)
	}
}

func TestAWSConfigExplicitCredentialsIgnoreMissingProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_PROFILE", "missing")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_SDK_LOAD_CONFIG", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_REGION", "us-west-1")
	t.Setenv("AWS_DEFAULT_REGION", "us-east-2")
	t.Setenv("AWS_MAX_ATTEMPTS", "6")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	cfg, err := NewAWSConfig(t.Context(), &config.AWSConfig{
		Key:      "explicit-key",
		Secret:   "explicit-secret",
		Endpoint: "localhost:9000",
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := cfg.Credentials.Retrieve(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if value.AccessKeyID != "explicit-key" || value.SecretAccessKey != "explicit-secret" {
		t.Fatalf("credentials = %q/%q, want explicit keys", value.AccessKeyID, value.SecretAccessKey)
	}
	if cfg.Region != "us-west-1" || aws.ToString(cfg.BaseEndpoint) != "http://localhost:9000" || cfg.RetryMaxAttempts != 6 {
		t.Fatalf("config = region %q, endpoint %q, attempts %d", cfg.Region, aws.ToString(cfg.BaseEndpoint), cfg.RetryMaxAttempts)
	}
}

func TestAWSConfigExplicitCredentialsPreserveCABundle(t *testing.T) {
	dir := t.TempDir()
	caBundle := filepath.Join(dir, "empty-ca.pem")
	if err := os.WriteFile(caBundle, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_PROFILE", "missing")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_SDK_LOAD_CONFIG", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_CA_BUNDLE", caBundle)

	_, err := NewAWSConfig(t.Context(), &config.AWSConfig{Key: "explicit-key", Secret: "explicit-secret"})
	if err == nil || !strings.Contains(err.Error(), "custom CA bundle") {
		t.Fatalf("NewAWSConfig error = %v, want custom CA bundle validation error", err)
	}
}

func TestAWSConfigAnonymousCredentialsIgnoreMissingProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_PROFILE", "missing")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_REGION", "us-west-1")
	t.Setenv("AWS_MAX_ATTEMPTS", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	cfg, err := NewAWSConfig(t.Context(), &config.AWSConfig{DisableAutoCredentialLoad: true})
	if err != nil {
		t.Fatal(err)
	}
	if !aws.IsCredentialsProvider(cfg.Credentials, aws.AnonymousCredentials{}) {
		t.Fatalf("credentials = %T, want anonymous", cfg.Credentials)
	}
	if cfg.Region != "us-west-1" {
		t.Fatalf("region = %q, want environment region", cfg.Region)
	}
}

func TestAWSConfigEnvironmentCredentialsIgnoreMissingProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_PROFILE", "missing")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_ACCESS_KEY_ID", "environment-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "environment-secret")
	t.Setenv("AWS_SESSION_TOKEN", "environment-token")
	t.Setenv("AWS_REGION", "us-west-1")
	t.Setenv("AWS_ENDPOINT_URL", "https://aws.example.test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	cfg, err := NewAWSConfig(t.Context(), &config.AWSConfig{})
	if err != nil {
		t.Fatal(err)
	}
	value, err := cfg.Credentials.Retrieve(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if value.AccessKeyID != "environment-key" || value.SecretAccessKey != "environment-secret" || value.SessionToken != "environment-token" {
		t.Fatalf("credential source or session token changed: %q/%q/%q", value.AccessKeyID, value.SecretAccessKey, value.SessionToken)
	}
	if cfg.Region != "us-west-1" {
		t.Fatalf("region = %q, want environment region", cfg.Region)
	}
	if aws.ToString(cfg.BaseEndpoint) != "https://aws.example.test" {
		t.Fatalf("base endpoint = %q, want environment endpoint", aws.ToString(cfg.BaseEndpoint))
	}
}
