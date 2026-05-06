package aws

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/ohsu-comp-bio/funnel/config"
)

// NewAWSSession returns a new session.Session instance.
func NewAWSSession(conf *config.AWSConfig) (*session.Session, error) {
	if conf == nil {
		return nil, fmt.Errorf("Config provided is nil")
	}
	awsConf := aws.NewConfig()

	if conf.DisableAutoCredentialLoad {
		awsConf.Credentials = nil
	}

	if conf.Endpoint != "" {
		re := regexp.MustCompile(`^s3.*\.amazonaws\.com/`)
		if !re.MatchString(conf.Endpoint) && !strings.HasPrefix(conf.Endpoint, "https://") {
			awsConf.WithDisableSSL(true)
		}
		if !re.MatchString(conf.Endpoint) {
			awsConf.WithS3ForcePathStyle(true)
		}
		awsConf.WithEndpoint(conf.Endpoint)
	}

	if conf.Region != "" {
		awsConf.WithRegion(conf.Region)
	}

	if conf.MaxRetries > 0 {
		awsConf.WithMaxRetries(int(conf.MaxRetries))
	}

	if conf.Key != "" && conf.Secret != "" {
		creds := credentials.NewStaticCredentialsFromCreds(credentials.Value{
			AccessKeyID:     conf.Key,
			SecretAccessKey: conf.Secret,
		})
		awsConf.WithCredentials(creds)
	}

	return session.NewSession(awsConf)
}
