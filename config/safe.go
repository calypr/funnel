package config

import "google.golang.org/protobuf/proto"

// Safe returns a copy of the config with sensitive fields redacted
func (c *Config) Safe() *Config {
	if c == nil {
		return nil
	}

	safe := proto.Clone(c).(*Config)

	// Database credentials
	if safe.MongoDB != nil {
		safe.MongoDB.Password = redact(safe.MongoDB.Password)
	}

	if safe.Postgres != nil {
		safe.Postgres.User = redact(safe.Postgres.User)
		safe.Postgres.Password = redact(safe.Postgres.Password)
		safe.Postgres.AdminUser = redact(safe.Postgres.AdminUser)
		safe.Postgres.AdminPassword = redact(safe.Postgres.AdminPassword)
	}

	if safe.Elastic != nil {
		safe.Elastic.Password = redact(safe.Elastic.Password)
		safe.Elastic.APIKey = redact(safe.Elastic.APIKey)
		safe.Elastic.ServiceToken = redact(safe.Elastic.ServiceToken)
	}

	// Cloud provider credentials
	if safe.AWSBatch != nil && safe.AWSBatch.AWSConfig != nil {
		safe.AWSBatch.AWSConfig.Key = redact(safe.AWSBatch.AWSConfig.Key)
		safe.AWSBatch.AWSConfig.Secret = redact(safe.AWSBatch.AWSConfig.Secret)
	}

	if safe.DynamoDB != nil && safe.DynamoDB.AWSConfig != nil {
		safe.DynamoDB.AWSConfig.Key = redact(safe.DynamoDB.AWSConfig.Key)
		safe.DynamoDB.AWSConfig.Secret = redact(safe.DynamoDB.AWSConfig.Secret)
	}

	if safe.AmazonS3 != nil && safe.AmazonS3.AWSConfig != nil {
		safe.AmazonS3.AWSConfig.Key = redact(safe.AmazonS3.AWSConfig.Key)
		safe.AmazonS3.AWSConfig.Secret = redact(safe.AmazonS3.AWSConfig.Secret)
	}

	for _, s3 := range safe.GenericS3 {
		if s3 == nil {
			continue
		}
		s3.Key = redact(s3.Key)
		s3.Secret = redact(s3.Secret)
	}

	if safe.PubSub != nil {
		safe.PubSub.CredentialsFile = redact(safe.PubSub.CredentialsFile)
	}

	if safe.Datastore != nil {
		safe.Datastore.CredentialsFile = redact(safe.Datastore.CredentialsFile)
	}

	if safe.GoogleStorage != nil {
		safe.GoogleStorage.CredentialsFile = redact(safe.GoogleStorage.CredentialsFile)
	}

	// Auth credentials
	if safe.Server != nil {
		for _, cred := range safe.Server.BasicAuth {
			if cred == nil {
				continue
			}
			cred.Password = redact(cred.Password)
		}

		if safe.Server.OidcAuth != nil {
			safe.Server.OidcAuth.ClientSecret = redact(safe.Server.OidcAuth.ClientSecret)
		}
	}

	if safe.RPCClient != nil && safe.RPCClient.Credential != nil {
		safe.RPCClient.Credential.Password = redact(safe.RPCClient.Credential.Password)
	}

	// Storage credentials
	if safe.Swift != nil {
		safe.Swift.Password = redact(safe.Swift.Password)
	}

	if safe.FTPStorage != nil {
		safe.FTPStorage.Password = redact(safe.FTPStorage.Password)
	}

	if safe.Plugins != nil && safe.Plugins.Params != nil {
		for key, param := range safe.Plugins.Params {
			safe.Plugins.Params[key] = redact(param)
		}
	}

	return safe
}

func redact(str string) string {
	if str == "" {
		return ""
	}
	return "***"
}
