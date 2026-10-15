package aws

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/ohsu-comp-bio/funnel/config"
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
		return aws.Config{}, err
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
