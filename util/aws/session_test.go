package aws

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/ohsu-comp-bio/funnel/config"
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
		config     config.AWSConfig
		key, token string
		anonymous  bool
	}{
		{name: "default chain", key: "environment-key", token: "environment-token"},
		{name: "disabled chain", config: config.AWSConfig{DisableAutoCredentialLoad: true}, anonymous: true},
		{name: "explicit keys", config: config.AWSConfig{Key: "explicit-key", Secret: "explicit-secret"}, key: "explicit-key"},
		{name: "explicit keys with disabled chain", config: config.AWSConfig{Key: "explicit-key", Secret: "explicit-secret", DisableAutoCredentialLoad: true}, key: "explicit-key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := NewAWSConfig(t.Context(), &test.config)
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
