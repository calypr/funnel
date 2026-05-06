package config

import (
	"github.com/ohsu-comp-bio/funnel/logger"
	durationpb "google.golang.org/protobuf/types/known/durationpb"
)

func EmptyConfig() *Config {
	return &Config{
		RPCClient:     &RPCClient{Credential: &BasicCredential{}, Timeout: &TimeoutConfig{}},
		Scheduler:     &Scheduler{ScheduleRate: &durationpb.Duration{}, NodePingTimeout: &TimeoutConfig{}, NodeInitTimeout: &TimeoutConfig{}, NodeDeadTimeout: &TimeoutConfig{}},
		Node:          &Node{Resources: &Resources{}, Timeout: &TimeoutConfig{}, Metadata: map[string]string{}},
		Worker:        &Worker{Container: &ContainerConfig{}, PollingRate: &durationpb.Duration{}, LogUpdateRate: &durationpb.Duration{}},
		Logger:        &logger.LoggerConfig{JsonFormat: &logger.JSONFormatConfig{}, TextFormat: &logger.TextFormatConfig{}},
		BoltDB:        &BoltDB{},
		Badger:        &Badger{},
		DynamoDB:      &DynamoDB{AWSConfig: &AWSConfig{}},
		Elastic:       &Elastic{},
		MongoDB:       &MongoDB{Timeout: &TimeoutConfig{}},
		Postgres:      &Postgres{Timeout: &TimeoutConfig{}},
		Kafka:         &Kafka{},
		LocalStorage:  &LocalStorage{},
		HTTPStorage:   &HTTPStorage{Timeout: &TimeoutConfig{}},
		FTPStorage:    &FTPStorage{Timeout: &TimeoutConfig{}},
		AmazonS3:      &AmazonS3Storage{SSE: &SSE{}, AWSConfig: &AWSConfig{}},
		Swift:         &SwiftStorage{},
		HTCondor:      &HPCBackend{ReconcileRate: &durationpb.Duration{}},
		Slurm:         &HPCBackend{ReconcileRate: &durationpb.Duration{}},
		PBS:           &HPCBackend{ReconcileRate: &durationpb.Duration{}},
		GridEngine:    &GridEngine{},
		AWSBatch:      &AWSBatch{AWSConfig: &AWSConfig{}, ReconcileRate: &durationpb.Duration{}},
		Kubernetes:    &Kubernetes{ReconcileRate: &durationpb.Duration{}},
		GoogleStorage: &GoogleCloudStorage{},
		PubSub:        &PubSub{},
		Datastore:     &Datastore{},
		GenericS3:     []*GenericS3Storage{},
		Server:        &Server{BasicAuth: []*BasicCredential{}, OidcAuth: &OidcAuth{}},
		EventWriters:  []string{},
	}
}
