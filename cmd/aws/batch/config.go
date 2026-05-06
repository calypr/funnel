package batch

import (
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Config represents configuration of the AWS proxy, including
// the compute environment, job queue, and base job definition.
type Config struct {
	*config.AWSConfig
	ComputeEnv *ComputeEnvConfig
	JobQueue   *JobQueueConfig
	JobDef     *JobDefinitionConfig
	JobRole    *JobRoleConfig
	Funnel     *config.Config
}

// ComputeEnvConfig represents configuration of the AWS Batch
// Compute Environment.
type ComputeEnvConfig struct {
	Name             string
	MinVCPUs         int64
	MaxVCPUs         int64
	SecurityGroupIds []string
	Subnets          []string
	Tags             map[string]string
	ServiceRole      string
	InstanceRole     string
	InstanceTypes    []string
	ImageID          string
}

// JobQueueConfig represents configuration of the AWS Batch
// Job Queue.
type JobQueueConfig struct {
	Name        string
	Priority    int64
	ComputeEnvs []string
}

// JobDefinitionConfig represents configuration of the AWS Batch Job Definition
type JobDefinitionConfig struct {
	Name       string
	Image      string
	MemoryMiB  int64
	VCPUs      int64
	JobRoleArn string
}

// Policy represents an AWS policy
type Policy struct {
	Version   string
	Statement []Statement
}

// Statement represents an AWS policy statement
type Statement struct {
	Effect   string
	Action   []string
	Resource string
}

// AssumeRolePolicy represents an AWS policy
type AssumeRolePolicy struct {
	Version   string
	Statement []RoleStatement
}

// RoleStatement represents an AWS policy statement
type RoleStatement struct {
	Sid       string
	Effect    string
	Action    string
	Principal map[string]string
}

// JobRoleConfig represents configuration of the AWS Batch
// JobRole.
type JobRoleConfig struct {
	RoleName           string
	S3PolicyName       string
	DynamoDBPolicyName string
	Policies           struct {
		AssumeRole AssumeRolePolicy
		S3         Policy
		DynamoDB   Policy
	}
}

// DefaultConfig returns default configuration of for AWS Batch resource creation.
func DefaultConfig() *Config {
	c := &Config{
		Funnel:    config.DefaultConfig(),
		AWSConfig: &config.AWSConfig{},
		ComputeEnv: &ComputeEnvConfig{
			Name:          "funnel-compute-environment",
			InstanceTypes: []string{"optimal"},
			MinVCPUs:      0,
			MaxVCPUs:      256,
			Tags: map[string]string{
				"Name": "Funnel",
			},
		},
		JobQueue: &JobQueueConfig{
			Name:     "funnel-job-queue",
			Priority: 1,
			ComputeEnvs: []string{
				"funnel-compute-environment",
			},
		},
		JobRole: &JobRoleConfig{
			RoleName:           "FunnelEcsTaskRole",
			DynamoDBPolicyName: "FunnelDynamoDB",
			S3PolicyName:       "FunnelS3",
		},
		JobDef: &JobDefinitionConfig{
			Name:      "funnel-job-def",
			Image:     "quay.io/ohsu-comp-bio/funnel:latest",
			VCPUs:     1,
			MemoryMiB: 128,
		},
	}

	c.JobRole.Policies.AssumeRole = AssumeRolePolicy{
		Version: "2012-10-17",
		Statement: []RoleStatement{
			{
				Effect:    "Allow",
				Principal: map[string]string{"Service": "ecs-tasks.amazonaws.com"},
				Action:    "sts:AssumeRole",
			},
		},
	}
	c.JobRole.Policies.S3 = Policy{
		Version: "2012-10-17",
		Statement: []Statement{
			{
				Effect: "Allow",
				Action: []string{
					"s3:GetBucketLocation",
					"s3:GetObject",
					"s3:ListObjects",
					"s3:ListBucket",
					"s3:CreateBucket",
					"s3:PutObject",
				},
				Resource: "*",
			},
		},
	}
	c.JobRole.Policies.DynamoDB = Policy{
		Version: "2012-10-17",
		Statement: []Statement{
			{
				Effect: "Allow",
				Action: []string{
					"dynamodb:GetItem",
					"dynamodb:PutItem",
					"dynamodb:UpdateItem",
					"dynamodb:Query",
				},
				Resource: "*",
			},
		},
	}

	c.Funnel.Database = "dynamodb"
	c.Funnel.EventWriters = []string{"dynamodb", "log"}
	c.Funnel.DynamoDB.TableBasename = "funnel"
	c.Funnel.DynamoDB.AWSConfig.Region = ""
	c.Funnel.Worker.WorkDir = "/opt/funnel-work-dir"
	c.Funnel.Worker.LogUpdateRate = durationpb.New(time.Minute * 5)
	c.Funnel.Worker.LogTailSize = 10000

	return c
}
