package config

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	schema "github.com/calypr/funnel/config"
)

func TestSafeRedactsCredentialContainers(t *testing.T) {
	c := &schema.Config{
		MongoDB: &schema.MongoDB{Addrs: []string{"mongodb://user:fixture-mongo@db.example/tasks", "db.example:27017"}},
		Worker:  &schema.Worker{Container: &schema.ContainerConfig{Env: map[string]string{"TOKEN": "fixture-env"}}},
		Swift:   &schema.SwiftStorage{AuthURL: "https://user:fixture-swift@swift.example/auth?key=fixture-query"},
	}
	before := proto.Clone(c)
	safe := Safe(c)
	body, err := protojson.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"fixture-mongo", "fixture-env", "fixture-swift", "fixture-query"} {
		if strings.Contains(string(body), value) {
			t.Errorf("safe serialization retains %s", value)
		}
	}
	if safe.MongoDB.Addrs[1] != "db.example:27017" {
		t.Error("plain MongoDB address was changed")
	}
	if !proto.Equal(c, before) {
		t.Error("Safe mutated the original configuration")
	}
}

var redacted = redact("example")

// TestSafeNilConfig verifies that Safe() on a nil config returns nil.
func TestSafeNilConfig(t *testing.T) {
	var c *schema.Config
	if Safe(c) != nil {
		t.Fatal("expected nil for nil config")
	}
}

// TestSafeEmptyConfig verifies that Safe() on an empty config does not panic.
func TestSafeEmptyConfig(t *testing.T) {
	c := &schema.Config{}
	safe := Safe(c)
	if safe == nil {
		t.Fatal("expected non-nil safe config")
	}
}

// TestSafeDoesNotMutateOriginal verifies that Safe() returns a copy and
// does not modify any fields on the original config.
func TestSafeDoesNotMutateOriginal(t *testing.T) {
	c := &schema.Config{
		MongoDB: &schema.MongoDB{Password: "mongopass"},
		Postgres: &schema.Postgres{
			User: "pguser", Password: "pgpass",
			AdminUser: "pgadmin", AdminPassword: "pgadminpass",
		},
		AWSBatch: &schema.AWSBatch{
			AWSConfig: &schema.AWSConfig{Key: "batchkey", Secret: "batchsecret"},
		},
		Swift:      &schema.SwiftStorage{Password: "swiftpass"},
		FTPStorage: &schema.FTPStorage{Password: "ftppass"},
		Server: &schema.Server{
			BasicAuth: []*schema.BasicCredential{{User: "user", Password: "basicpass"}},
			OidcAuth:  &schema.OidcAuth{ClientSecret: "oidcsecret"},
		},
		RPCClient: &schema.RPCClient{Credential: &schema.BasicCredential{Password: "rpcpass"}},
		Plugins:   &schema.Plugins{Params: map[string]string{"key": "pluginparam"}},
	}

	_ = Safe(c)

	if c.MongoDB.Password != "mongopass" {
		t.Error("original MongoDB.Password was mutated")
	}
	if c.Postgres.Password != "pgpass" {
		t.Error("original Postgres.Password was mutated")
	}
	if c.AWSBatch.AWSConfig.Secret != "batchsecret" {
		t.Error("original AWSBatch.AWSConfig.Secret was mutated")
	}
	if c.Swift.Password != "swiftpass" {
		t.Error("original Swift.Password was mutated")
	}
	if c.FTPStorage.Password != "ftppass" {
		t.Error("original FTPStorage.Password was mutated")
	}
	if c.Server.BasicAuth[0].Password != "basicpass" {
		t.Error("original Server.BasicAuth[0].Password was mutated")
	}
	if c.Server.OidcAuth.ClientSecret != "oidcsecret" {
		t.Error("original Server.OidcAuth.ClientSecret was mutated")
	}
	if c.RPCClient.Credential.Password != "rpcpass" {
		t.Error("original RPCClient.Credential.Password was mutated")
	}
	if c.Plugins.Params["key"] != "pluginparam" {
		t.Error("original Plugins.Params was mutated")
	}
}

// TestSafeMongoDBRedaction verifies MongoDB password redaction.
func TestSafeMongoDBRedaction(t *testing.T) {
	c := &schema.Config{
		MongoDB: &schema.MongoDB{
			Username: "mongouser",
			Password: "supersecret",
		},
	}
	safe := Safe(c)

	if safe.MongoDB.Password != redacted {
		t.Errorf("expected MongoDB.Password to be redacted, got %q", safe.MongoDB.Password)
	}
	// Non-sensitive field must be preserved
	if safe.MongoDB.Username != "mongouser" {
		t.Errorf("expected MongoDB.Username to be preserved, got %q", safe.MongoDB.Username)
	}
}

// TestSafeMongoDBEmptyPassword verifies that an empty MongoDB password stays empty.
func TestSafeMongoDBEmptyPassword(t *testing.T) {
	c := &schema.Config{MongoDB: &schema.MongoDB{Password: ""}}
	safe := Safe(c)
	if safe.MongoDB.Password != "" {
		t.Errorf("expected empty MongoDB.Password to stay empty, got %q", safe.MongoDB.Password)
	}
}

// TestSafePostgresRedaction verifies all four Postgres credential fields.
func TestSafePostgresRedaction(t *testing.T) {
	c := &schema.Config{
		Postgres: &schema.Postgres{
			Host:          "pghost",
			User:          "pguser",
			Password:      "pgpass",
			AdminUser:     "pgadmin",
			AdminPassword: "pgadminpass",
		},
	}
	safe := Safe(c)

	for field, got := range map[string]string{
		"User":          safe.Postgres.User,
		"Password":      safe.Postgres.Password,
		"AdminUser":     safe.Postgres.AdminUser,
		"AdminPassword": safe.Postgres.AdminPassword,
	} {
		if got != redacted {
			t.Errorf("expected Postgres.%s to be redacted, got %q", field, got)
		}
	}
	if safe.Postgres.Host != "pghost" {
		t.Errorf("expected Postgres.Host to be preserved, got %q", safe.Postgres.Host)
	}
}

// TestSafePostgresEmptyCredentials verifies that empty Postgres credentials stay empty.
func TestSafePostgresEmptyCredentials(t *testing.T) {
	c := &schema.Config{
		Postgres: &schema.Postgres{User: "", Password: "", AdminUser: "", AdminPassword: ""},
	}
	safe := Safe(c)
	if safe.Postgres.User != "" || safe.Postgres.Password != "" ||
		safe.Postgres.AdminUser != "" || safe.Postgres.AdminPassword != "" {
		t.Error("expected empty Postgres credentials to stay empty")
	}
}

// TestSafeAWSBatchRedaction verifies AWSBatch Key and Secret redaction.
func TestSafeAWSBatchRedaction(t *testing.T) {
	c := &schema.Config{
		AWSBatch: &schema.AWSBatch{
			JobDefinition: "my-job-def",
			AWSConfig: &schema.AWSConfig{
				Key:    "AKIAIOSFODNN7EXAMPLE",
				Secret: "wJalrXUtnFEMI/K7MDENG",
				Region: "us-east-1",
			},
		},
	}
	safe := Safe(c)

	if safe.AWSBatch.AWSConfig.Key != redacted {
		t.Errorf("expected AWSBatch.AWSConfig.Key to be redacted, got %q", safe.AWSBatch.AWSConfig.Key)
	}
	if safe.AWSBatch.AWSConfig.Secret != redacted {
		t.Errorf("expected AWSBatch.AWSConfig.Secret to be redacted, got %q", safe.AWSBatch.AWSConfig.Secret)
	}
	if safe.AWSBatch.AWSConfig.Region != "us-east-1" {
		t.Errorf("expected AWSBatch.AWSConfig.Region to be preserved, got %q", safe.AWSBatch.AWSConfig.Region)
	}
	if safe.AWSBatch.JobDefinition != "my-job-def" {
		t.Errorf("expected AWSBatch.JobDefinition to be preserved, got %q", safe.AWSBatch.JobDefinition)
	}
}

// TestSafeAWSBatchNilAWSConfig verifies no panic when AWSBatch.AWSConfig is nil.
func TestSafeAWSBatchNilAWSConfig(t *testing.T) {
	c := &schema.Config{AWSBatch: &schema.AWSBatch{JobDefinition: "jd"}}
	safe := Safe(c)
	if safe.AWSBatch.AWSConfig != nil {
		t.Errorf("expected AWSBatch.AWSConfig to remain nil")
	}
}

// TestSafeDynamoDBRedaction verifies DynamoDB Key and Secret redaction.
func TestSafeDynamoDBRedaction(t *testing.T) {
	c := &schema.Config{
		DynamoDB: &schema.DynamoDB{
			TableBasename: "funnel",
			AWSConfig: &schema.AWSConfig{
				Key:    "dynamo-key",
				Secret: "dynamo-secret",
			},
		},
	}
	safe := Safe(c)

	if safe.DynamoDB.AWSConfig.Key != redacted {
		t.Errorf("expected DynamoDB.AWSConfig.Key to be redacted, got %q", safe.DynamoDB.AWSConfig.Key)
	}
	if safe.DynamoDB.AWSConfig.Secret != redacted {
		t.Errorf("expected DynamoDB.AWSConfig.Secret to be redacted, got %q", safe.DynamoDB.AWSConfig.Secret)
	}
}

// TestSafeAmazonS3Redaction verifies AmazonS3 Key and Secret redaction.
func TestSafeAmazonS3Redaction(t *testing.T) {
	c := &schema.Config{
		AmazonS3: &schema.AmazonS3Storage{
			AWSConfig: &schema.AWSConfig{
				Key:    "s3-key",
				Secret: "s3-secret",
			},
		},
	}
	safe := Safe(c)

	if safe.AmazonS3.AWSConfig.Key != redacted {
		t.Errorf("expected AmazonS3.AWSConfig.Key to be redacted, got %q", safe.AmazonS3.AWSConfig.Key)
	}
	if safe.AmazonS3.AWSConfig.Secret != redacted {
		t.Errorf("expected AmazonS3.AWSConfig.Secret to be redacted, got %q", safe.AmazonS3.AWSConfig.Secret)
	}
}

// TestSafeGenericS3Redaction verifies GenericS3 Key and Secret redaction across multiple entries.
func TestSafeGenericS3Redaction(t *testing.T) {
	c := &schema.Config{
		GenericS3: []*schema.GenericS3Storage{
			{Endpoint: "https://s3.example.com", Key: "gs3-key", Secret: "gs3-secret"},
			{Endpoint: "https://s3-2.example.com", Key: "gs3-key2", Secret: "gs3-secret2"},
		},
	}
	safe := Safe(c)

	if safe.GenericS3[0].Key != redacted {
		t.Errorf("expected GenericS3[0].Key to be redacted, got %q", safe.GenericS3[0].Key)
	}
	if safe.GenericS3[0].Secret != redacted {
		t.Errorf("expected GenericS3[0].Secret to be redacted, got %q", safe.GenericS3[0].Secret)
	}
	if safe.GenericS3[0].Endpoint != "https://s3.example.com" {
		t.Errorf("expected GenericS3[0].Endpoint to be preserved, got %q", safe.GenericS3[0].Endpoint)
	}
	if safe.GenericS3[1].Key != redacted {
		t.Errorf("expected GenericS3[1].Key to be redacted, got %q", safe.GenericS3[1].Key)
	}
	if safe.GenericS3[1].Secret != redacted {
		t.Errorf("expected GenericS3[1].Secret to be redacted, got %q", safe.GenericS3[1].Secret)
	}
}

// TestSafePubSubRedaction verifies PubSub CredentialsFile redaction.
func TestSafePubSubRedaction(t *testing.T) {
	c := &schema.Config{
		PubSub: &schema.PubSub{
			Topic:           "my-topic",
			CredentialsFile: "/path/to/creds.json",
		},
	}
	safe := Safe(c)

	if safe.PubSub.CredentialsFile != redacted {
		t.Errorf("expected PubSub.CredentialsFile to be redacted, got %q", safe.PubSub.CredentialsFile)
	}
	if safe.PubSub.Topic != "my-topic" {
		t.Errorf("expected PubSub.Topic to be preserved, got %q", safe.PubSub.Topic)
	}
}

// TestSafeDatastoreRedaction verifies Datastore CredentialsFile redaction.
func TestSafeDatastoreRedaction(t *testing.T) {
	c := &schema.Config{
		Datastore: &schema.Datastore{
			Project:         "my-project",
			CredentialsFile: "/path/to/creds.json",
		},
	}
	safe := Safe(c)

	if safe.Datastore.CredentialsFile != redacted {
		t.Errorf("expected Datastore.CredentialsFile to be redacted, got %q", safe.Datastore.CredentialsFile)
	}
	if safe.Datastore.Project != "my-project" {
		t.Errorf("expected Datastore.Project to be preserved, got %q", safe.Datastore.Project)
	}
}

// TestSafeGoogleStorageRedaction verifies GoogleStorage CredentialsFile redaction.
func TestSafeGoogleStorageRedaction(t *testing.T) {
	c := &schema.Config{
		GoogleStorage: &schema.GoogleCloudStorage{
			CredentialsFile: "/path/to/creds.json",
		},
	}
	safe := Safe(c)

	if safe.GoogleStorage.CredentialsFile != redacted {
		t.Errorf("expected GoogleStorage.CredentialsFile to be redacted, got %q", safe.GoogleStorage.CredentialsFile)
	}
}

// TestSafeServerBasicAuthRedaction verifies Server BasicAuth password redaction
// across multiple entries.
func TestSafeServerBasicAuthRedaction(t *testing.T) {
	c := &schema.Config{
		Server: &schema.Server{
			BasicAuth: []*schema.BasicCredential{
				{User: "alice", Password: "alicepass"},
				{User: "bob", Password: "bobpass"},
			},
		},
	}
	safe := Safe(c)

	if safe.Server.BasicAuth[0].Password != redacted {
		t.Errorf("expected Server.BasicAuth[0].Password to be redacted, got %q", safe.Server.BasicAuth[0].Password)
	}
	if safe.Server.BasicAuth[0].User != "alice" {
		t.Errorf("expected Server.BasicAuth[0].User to be preserved, got %q", safe.Server.BasicAuth[0].User)
	}
	if safe.Server.BasicAuth[1].Password != redacted {
		t.Errorf("expected Server.BasicAuth[1].Password to be redacted, got %q", safe.Server.BasicAuth[1].Password)
	}
}

// TestSafeServerOidcAuthRedaction verifies OidcAuth ClientSecret redaction.
func TestSafeServerOidcAuthRedaction(t *testing.T) {
	c := &schema.Config{
		Server: &schema.Server{
			OidcAuth: &schema.OidcAuth{
				ClientId:     "my-client-id",
				ClientSecret: "my-client-secret",
			},
		},
	}
	safe := Safe(c)

	if safe.Server.OidcAuth.ClientSecret != redacted {
		t.Errorf("expected Server.OidcAuth.ClientSecret to be redacted, got %q", safe.Server.OidcAuth.ClientSecret)
	}
	if safe.Server.OidcAuth.ClientId != "my-client-id" {
		t.Errorf("expected Server.OidcAuth.ClientId to be preserved, got %q", safe.Server.OidcAuth.ClientId)
	}
}

// TestSafeRPCClientRedaction verifies RPCClient credential password redaction.
func TestSafeRPCClientRedaction(t *testing.T) {
	c := &schema.Config{
		RPCClient: &schema.RPCClient{
			ServerAddress: "funnel:9090",
			Credential:    &schema.BasicCredential{User: "rpcuser", Password: "rpcpass"},
		},
	}
	safe := Safe(c)

	if safe.RPCClient.Credential.Password != redacted {
		t.Errorf("expected RPCClient.Credential.Password to be redacted, got %q", safe.RPCClient.Credential.Password)
	}
	if safe.RPCClient.ServerAddress != "funnel:9090" {
		t.Errorf("expected RPCClient.ServerAddress to be preserved, got %q", safe.RPCClient.ServerAddress)
	}
}

// TestSafeRPCClientNilCredential verifies no panic when RPCClient.Credential is nil.
func TestSafeRPCClientNilCredential(t *testing.T) {
	c := &schema.Config{RPCClient: &schema.RPCClient{ServerAddress: "funnel:9090"}}
	safe := Safe(c)
	if safe.RPCClient.Credential != nil {
		t.Error("expected RPCClient.Credential to remain nil")
	}
}

// TestSafeSwiftRedaction verifies Swift password redaction.
func TestSafeSwiftRedaction(t *testing.T) {
	c := &schema.Config{
		Swift: &schema.SwiftStorage{
			UserName: "swiftuser",
			Password: "swiftpass",
		},
	}
	safe := Safe(c)

	if safe.Swift.Password != redacted {
		t.Errorf("expected Swift.Password to be redacted, got %q", safe.Swift.Password)
	}
	if safe.Swift.UserName != "swiftuser" {
		t.Errorf("expected Swift.UserName to be preserved, got %q", safe.Swift.UserName)
	}
}

// TestSafeFTPStorageRedaction verifies FTPStorage password redaction.
func TestSafeFTPStorageRedaction(t *testing.T) {
	c := &schema.Config{
		FTPStorage: &schema.FTPStorage{
			User:     "ftpuser",
			Password: "ftppass",
		},
	}
	safe := Safe(c)

	if safe.FTPStorage.Password != redacted {
		t.Errorf("expected FTPStorage.Password to be redacted, got %q", safe.FTPStorage.Password)
	}
	if safe.FTPStorage.User != "ftpuser" {
		t.Errorf("expected FTPStorage.User to be preserved, got %q", safe.FTPStorage.User)
	}
}

// TestSafeNilPlugins verifies that a nil Plugins field does not panic.
func TestSafeNilPlugins(t *testing.T) {
	c := &schema.Config{}
	safe := Safe(c)
	if safe.Plugins != nil {
		t.Error("expected Plugins to remain nil")
	}
}

// TestSafePluginsParamsRedaction verifies that all Plugins.Params values are redacted.
func TestSafePluginsParamsRedaction(t *testing.T) {
	c := &schema.Config{
		Plugins: &schema.Plugins{
			Path: "/path/to/plugin",
			Params: map[string]string{
				"api_key": "secretkey",
				"token":   "secrettoken",
				"empty":   "",
			},
		},
	}
	safe := Safe(c)

	if safe.Plugins.Params["api_key"] != redacted {
		t.Errorf("expected Plugins.Params[api_key] to be redacted, got %q", safe.Plugins.Params["api_key"])
	}
	if safe.Plugins.Params["token"] != redacted {
		t.Errorf("expected Plugins.Params[token] to be redacted, got %q", safe.Plugins.Params["token"])
	}
	if safe.Plugins.Params["empty"] != "" {
		t.Errorf("expected empty Plugins.Params[empty] to stay empty, got %q", safe.Plugins.Params["empty"])
	}
	if safe.Plugins.Path != "/path/to/plugin" {
		t.Errorf("expected Plugins.Path to be preserved, got %q", safe.Plugins.Path)
	}
}
