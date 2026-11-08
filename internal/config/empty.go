package config

import (
	durationpb "google.golang.org/protobuf/types/known/durationpb"

	schema "github.com/calypr/funnel/config"
	"github.com/calypr/funnel/logger"
)

func EmptyConfig() *schema.Config {
	return &schema.Config{
		RPCClient:     &schema.RPCClient{Credential: &schema.BasicCredential{}, Timeout: &schema.TimeoutConfig{}},
		Scheduler:     &schema.Scheduler{ScheduleRate: &durationpb.Duration{}, NodePingTimeout: &schema.TimeoutConfig{}, NodeInitTimeout: &schema.TimeoutConfig{}, NodeDeadTimeout: &schema.TimeoutConfig{}},
		Node:          &schema.Node{Resources: &schema.Resources{}, Timeout: &schema.TimeoutConfig{}, Metadata: map[string]string{}},
		Worker:        &schema.Worker{Container: &schema.ContainerConfig{}, PollingRate: &durationpb.Duration{}, LogUpdateRate: &durationpb.Duration{}},
		Logger:        &logger.LoggerConfig{JsonFormat: &logger.JSONFormatConfig{}, TextFormat: &logger.TextFormatConfig{}},
		BoltDB:        &schema.BoltDB{},
		Badger:        &schema.Badger{},
		DynamoDB:      &schema.DynamoDB{AWSConfig: &schema.AWSConfig{}},
		MongoDB:       &schema.MongoDB{Timeout: &schema.TimeoutConfig{}},
		Postgres:      &schema.Postgres{Timeout: &schema.TimeoutConfig{}},
		Kafka:         &schema.Kafka{},
		LocalStorage:  &schema.LocalStorage{},
		HTTPStorage:   &schema.HTTPStorage{Timeout: &schema.TimeoutConfig{}},
		FTPStorage:    &schema.FTPStorage{Timeout: &schema.TimeoutConfig{}},
		AmazonS3:      &schema.AmazonS3Storage{SSE: &schema.SSE{}, AWSConfig: &schema.AWSConfig{}},
		Swift:         &schema.SwiftStorage{},
		HTCondor:      &schema.HPCBackend{ReconcileRate: &durationpb.Duration{}},
		Slurm:         &schema.HPCBackend{ReconcileRate: &durationpb.Duration{}},
		PBS:           &schema.HPCBackend{ReconcileRate: &durationpb.Duration{}},
		GridEngine:    &schema.GridEngine{},
		AWSBatch:      &schema.AWSBatch{AWSConfig: &schema.AWSConfig{}, ReconcileRate: &durationpb.Duration{}},
		GCPBatch:      &schema.GCPBatch{Project: "", Location: "", ReconcileRate: &durationpb.Duration{}},
		Kubernetes:    &schema.Kubernetes{ReconcileRate: &durationpb.Duration{}},
		GoogleStorage: &schema.GoogleCloudStorage{},
		PubSub:        &schema.PubSub{},
		Datastore:     &schema.Datastore{},
		GenericS3:     []*schema.GenericS3Storage{},
		Server:        &schema.Server{BasicAuth: []*schema.BasicCredential{}, OidcAuth: &schema.OidcAuth{}},
		EventWriters:  []string{},
	}
}
