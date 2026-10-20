package aws

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/calypr/funnel/config"
)

// NewAWSConfig loads shared AWS settings and applies Funnel's explicit overrides.
func NewAWSConfig(ctx context.Context, conf *config.AWSConfig) (aws.Config, error) {
	if conf == nil {
		return aws.Config{}, fmt.Errorf("config provided is nil")
	}
	var options []func(*awsconfig.LoadOptions) error
	if conf.Region != "" {
		options = append(options, awsconfig.WithRegion(conf.Region))
	}
	if conf.MaxRetries > 0 {
		options = append(options, awsconfig.WithRetryMaxAttempts(int(conf.MaxRetries)+1))
	}
	if conf.Key != "" && conf.Secret != "" {
		options = append(options, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(conf.Key, conf.Secret, "")))
	} else if conf.DisableAutoCredentialLoad {
		options = append(options, awsconfig.WithCredentialsProvider(aws.AnonymousCredentials{}))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		var missingProfile awsconfig.SharedConfigProfileNotExistError
		if (!hasStaticCredentials(conf) && !conf.DisableAutoCredentialLoad) || !errors.As(err, &missingProfile) {
			return aws.Config{}, err
		}
		envConfig, err := awsconfig.NewEnvConfig()
		if err != nil {
			return aws.Config{}, err
		}
		cfg, err = configWithExplicitCredentials(conf, envConfig)
		if err != nil {
			return aws.Config{}, err
		}
	}
	if conf.Endpoint != "" {
		endpoint := conf.Endpoint
		if !strings.Contains(endpoint, "://") {
			if regexp.MustCompile(`^s3.*\.amazonaws\.com/`).MatchString(endpoint) {
				endpoint = "https://" + endpoint
			} else {
				endpoint = "http://" + endpoint
			}
		}
		cfg.BaseEndpoint = aws.String(endpoint)
	}
	return cfg, nil
}

func hasStaticCredentials(conf *config.AWSConfig) bool {
	return (conf.Key != "" && conf.Secret != "") || (envCredentialValue("AWS_ACCESS_KEY_ID", "AWS_ACCESS_KEY") != "" && envCredentialValue("AWS_SECRET_ACCESS_KEY", "AWS_SECRET_KEY") != "")
}

func configWithExplicitCredentials(conf *config.AWSConfig, envConfig awsconfig.EnvConfig) (aws.Config, error) {
	cfg := aws.Config{ConfigSources: []interface{}{envConfig}}
	cfg.Region = conf.Region
	if cfg.Region == "" {
		cfg.Region = envConfig.Region
	}
	ignoreConfiguredEndpoints := envConfig.IgnoreConfiguredEndpoints != nil && *envConfig.IgnoreConfiguredEndpoints
	if envConfig.BaseEndpoint != "" && !ignoreConfiguredEndpoints {
		cfg.BaseEndpoint = aws.String(envConfig.BaseEndpoint)
	}

	switch {
	case conf.Key != "" && conf.Secret != "":
		cfg.Credentials = credentials.NewStaticCredentialsProvider(conf.Key, conf.Secret, "")
	case conf.DisableAutoCredentialLoad:
		cfg.Credentials = aws.AnonymousCredentials{}
	case envConfig.Credentials.HasKeys():
		cfg.Credentials = credentials.NewStaticCredentialsProvider(envConfig.Credentials.AccessKeyID, envConfig.Credentials.SecretAccessKey, envConfig.Credentials.SessionToken)
	}

	if conf.MaxRetries > 0 {
		cfg.RetryMaxAttempts = int(conf.MaxRetries) + 1
	} else {
		cfg.RetryMaxAttempts = envConfig.RetryMaxAttempts
	}
	cfg.RetryMode = envConfig.RetryMode
	cfg.DefaultsMode = envConfig.DefaultsMode
	cfg.AppID = envConfig.AppID

	if envConfig.CustomCABundle != "" {
		pemCerts, err := os.ReadFile(envConfig.CustomCABundle)
		if err != nil {
			return aws.Config{}, fmt.Errorf("failed to load custom CA bundle PEM file: %w", err)
		}
		rootCAs := x509.NewCertPool()
		if !rootCAs.AppendCertsFromPEM(pemCerts) {
			return aws.Config{}, fmt.Errorf("failed to load custom CA bundle PEM file")
		}
		cfg.HTTPClient = awshttp.NewBuildableClient().WithTransportOptions(func(transport *http.Transport) {
			if transport.TLSClientConfig == nil {
				transport.TLSClientConfig = &tls.Config{}
			}
			transport.TLSClientConfig.RootCAs = rootCAs
		})
	}

	return cfg, nil
}

func envCredentialValue(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}
