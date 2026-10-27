package config

import (
	"net/url"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// Safe returns a copy of the config with sensitive fields redacted
func (c *Config) Safe() *Config {
	if c == nil {
		return nil
	}

	safe := proto.Clone(c).(*Config)

	// Database credentials
	if safe.MongoDB != nil {
		safe.MongoDB.Password = redact(safe.MongoDB.Password)
		for i, addr := range safe.MongoDB.Addrs {
			safe.MongoDB.Addrs[i] = redactEndpoint(addr)
		}
	}

	if safe.Postgres != nil {
		safe.Postgres.Host = redactEndpoint(safe.Postgres.Host)
		safe.Postgres.User = redact(safe.Postgres.User)
		safe.Postgres.Password = redact(safe.Postgres.Password)
		safe.Postgres.AdminUser = redact(safe.Postgres.AdminUser)
		safe.Postgres.AdminPassword = redact(safe.Postgres.AdminPassword)
	}

	if safe.Kafka != nil {
		for i, server := range safe.Kafka.Servers {
			safe.Kafka.Servers[i] = redactEndpoint(server)
		}
	}

	// Cloud provider credentials
	if safe.AWSBatch != nil && safe.AWSBatch.AWSConfig != nil {
		safe.AWSBatch.AWSConfig.Endpoint = redactEndpoint(safe.AWSBatch.AWSConfig.Endpoint)
		safe.AWSBatch.AWSConfig.Key = redact(safe.AWSBatch.AWSConfig.Key)
		safe.AWSBatch.AWSConfig.Secret = redact(safe.AWSBatch.AWSConfig.Secret)
	}

	if safe.DynamoDB != nil && safe.DynamoDB.AWSConfig != nil {
		safe.DynamoDB.AWSConfig.Endpoint = redactEndpoint(safe.DynamoDB.AWSConfig.Endpoint)
		safe.DynamoDB.AWSConfig.Key = redact(safe.DynamoDB.AWSConfig.Key)
		safe.DynamoDB.AWSConfig.Secret = redact(safe.DynamoDB.AWSConfig.Secret)
	}

	if safe.AmazonS3 != nil && safe.AmazonS3.AWSConfig != nil {
		safe.AmazonS3.AWSConfig.Endpoint = redactEndpoint(safe.AmazonS3.AWSConfig.Endpoint)
		safe.AmazonS3.AWSConfig.Key = redact(safe.AmazonS3.AWSConfig.Key)
		safe.AmazonS3.AWSConfig.Secret = redact(safe.AmazonS3.AWSConfig.Secret)
	}
	if safe.AmazonS3 != nil && safe.AmazonS3.SSE != nil {
		safe.AmazonS3.SSE.CustomerKeyFile = redact(safe.AmazonS3.SSE.CustomerKeyFile)
	}

	for _, s3 := range safe.GenericS3 {
		if s3 == nil {
			continue
		}
		s3.Key = redact(s3.Key)
		s3.Endpoint = redactEndpoint(s3.Endpoint)
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
			safe.Server.OidcAuth.ServiceConfigURL = redactEndpoint(safe.Server.OidcAuth.ServiceConfigURL)
			safe.Server.OidcAuth.RedirectURL = redactEndpoint(safe.Server.OidcAuth.RedirectURL)
		}
	}

	if safe.RPCClient != nil && safe.RPCClient.Credential != nil {
		safe.RPCClient.Credential.Password = redact(safe.RPCClient.Credential.Password)
	}
	if safe.RPCClient != nil {
		safe.RPCClient.ServerAddress = redactEndpoint(safe.RPCClient.ServerAddress)
	}

	// Storage credentials
	if safe.Swift != nil {
		safe.Swift.Password = redact(safe.Swift.Password)
		safe.Swift.AuthURL = redactEndpoint(safe.Swift.AuthURL)
	}

	if safe.FTPStorage != nil {
		safe.FTPStorage.Password = redact(safe.FTPStorage.Password)
	}

	if safe.Plugins != nil && safe.Plugins.Params != nil {
		for key, param := range safe.Plugins.Params {
			safe.Plugins.Params[key] = redact(param)
		}
	}

	if safe.Node != nil {
		redactMap(safe.Node.Metadata)
	}
	if safe.Worker != nil {
		safe.Worker.DriverCommand = redact(safe.Worker.DriverCommand)
		if c := safe.Worker.Container; c != nil {
			for i := range c.Command {
				c.Command[i] = redact(c.Command[i])
			}
			redactMap(c.Env)
			redactMap(c.Tags)
			c.DriverCommand = redact(c.DriverCommand)
			c.RunCommand = redact(c.RunCommand)
			c.PullCommand = redact(c.PullCommand)
			c.StopCommand = redact(c.StopCommand)
		}
	}
	for _, h := range []*HPCBackend{safe.HTCondor, safe.Slurm, safe.PBS} {
		if h != nil {
			h.Template = redact(h.Template)
		}
	}
	if safe.GridEngine != nil {
		safe.GridEngine.Template = redact(safe.GridEngine.Template)
	}
	if k := safe.Kubernetes; k != nil {
		k.WorkerTemplate = redact(k.WorkerTemplate)
		k.ExecutorTemplate = redact(k.ExecutorTemplate)
		k.PVTemplate = redact(k.PVTemplate)
		k.PVCTemplate = redact(k.PVCTemplate)
		k.ConfigMapTemplate = redact(k.ConfigMapTemplate)
		k.ServiceAccountTemplate = redact(k.ServiceAccountTemplate)
		k.RoleTemplate = redact(k.RoleTemplate)
		k.RoleBindingTemplate = redact(k.RoleBindingTemplate)
		redactMap(k.NodeSelector)
		for _, t := range k.Tolerations {
			if t != nil {
				t.Value = redact(t.Value)
			}
		}
		if s := k.Storage; s != nil {
			for _, structs := range [][]*structpb.Struct{s.Volumes, s.VolumeMounts, s.VolumeClaimTemplates, s.PersistentVolumeTemplates} {
				for _, item := range structs {
					if item != nil {
						item.Fields = map[string]*structpb.Value{"redacted": structpb.NewStringValue("***")}
					}
				}
			}
		}
	}

	return safe
}

func redactMap(values map[string]string) {
	for key, value := range values {
		values[key] = redact(value)
	}
}

func redactEndpoint(value string) string {
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		if strings.ContainsAny(value, "@?#") {
			return redact(value)
		}
		return value
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		return redact(value)
	}
	if u.User != nil {
		u.User = url.User("REDACTED")
	}
	if u.Path != "" && u.Path != "/" {
		u.Path = "/REDACTED"
		u.RawPath = ""
	}
	if u.RawQuery != "" {
		u.RawQuery = "REDACTED"
	}
	if u.Fragment != "" {
		u.Fragment = "REDACTED"
	}
	return u.String()
}

func redact(str string) string {
	if str == "" {
		return ""
	}
	return "***"
}
