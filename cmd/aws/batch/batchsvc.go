package batch

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	"github.com/aws/aws-sdk-go-v2/service/batch/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	util "github.com/calypr/funnel/util/aws"
)

const defaultBatchRetryMaxAttempts = 4

type errResourceExists struct{}

func (e errResourceExists) Error() string {
	return "resource exists"
}

func newBatchSvc(conf *Config) (*batchsvc, error) {
	sess, err := util.NewAWSConfig(context.Background(), conf.AWSConfig)
	if err != nil {
		return nil, fmt.Errorf("error occurred creating aws session: %v", err)
	}
	if conf.AWSConfig.MaxRetries <= 0 && sess.RetryMaxAttempts == 0 {
		sess.RetryMaxAttempts = defaultBatchRetryMaxAttempts
	}
	return &batchsvc{
		sess: sess,
		conf: conf,
	}, nil
}

type batchsvc struct {
	sess aws.Config
	conf *Config
}

func (b *batchsvc) CreateComputeEnvironment() (*types.ComputeEnvironmentDetail, error) {
	batchCli := batch.NewFromConfig(b.sess)
	ec2Cli := ec2.NewFromConfig(b.sess)
	iamCli := iam.NewFromConfig(b.sess)

	resp, err := batchCli.DescribeComputeEnvironments(context.Background(), &batch.DescribeComputeEnvironmentsInput{
		ComputeEnvironments: []string{b.conf.ComputeEnv.Name},
	})
	if err != nil {
		return nil, fmt.Errorf("error describing ComputeEnvironment: %w", err)
	}
	if len(resp.ComputeEnvironments) > 0 {
		return &resp.ComputeEnvironments[0], errResourceExists{}
	}

	securityGroupIds := []string{}
	if len(b.conf.ComputeEnv.SecurityGroupIds) > 0 {
		securityGroupIds = b.conf.ComputeEnv.SecurityGroupIds
	} else {
		sgres, err := ec2Cli.DescribeSecurityGroups(context.Background(), nil)
		if err != nil {
			return nil, err
		}
		for _, s := range sgres.SecurityGroups {
			securityGroupIds = append(securityGroupIds, *s.GroupId)
		}
	}

	subnets := []string{}
	if len(b.conf.ComputeEnv.Subnets) > 0 {
		subnets = b.conf.ComputeEnv.Subnets
	} else {
		snres, err := ec2Cli.DescribeSubnets(context.Background(), nil)
		if err != nil {
			return nil, err
		}
		for _, s := range snres.Subnets {
			subnets = append(subnets, *s.SubnetId)
		}
	}

	var serviceRole string
	grres, err := iamCli.GetRole(context.Background(), &iam.GetRoleInput{RoleName: aws.String("AWSBatchServiceRole")})
	if err == nil {
		serviceRole = *grres.Role.Arn
	} else {
		bsrPolicy := AssumeRolePolicy{
			Version: "2012-10-17",
			Statement: []RoleStatement{
				{
					Effect:    "Allow",
					Principal: map[string]string{"Service": "batch.amazonaws.com"},
					Action:    "sts:AssumeRole",
				},
			},
		}
		bsrBinary, err := json.Marshal(bsrPolicy)
		if err != nil {
			return nil, fmt.Errorf("error marshaling assume role policy for AWSBatchServiceRole: %v", err)
		}
		cr, err := iamCli.CreateRole(context.Background(), &iam.CreateRoleInput{
			AssumeRolePolicyDocument: aws.String(string(bsrBinary)),
			RoleName:                 aws.String("AWSBatchServiceRole"),
		})
		if err != nil {
			return nil, fmt.Errorf("error creating AWSBatchServiceRole: %v", err)
		}
		_, err = iamCli.AttachRolePolicy(context.Background(), &iam.AttachRolePolicyInput{
			PolicyArn: aws.String("arn:aws:iam::aws:policy/service-role/AWSBatchServiceRole"),
			RoleName:  aws.String("AWSBatchServiceRole"),
		})
		if err != nil {
			return nil, fmt.Errorf("error attaching policies to AWSBatchServiceRole: %v", err)
		}
		serviceRole = *cr.Role.Arn
	}

	var instanceRole string
	grres, err = iamCli.GetRole(context.Background(), &iam.GetRoleInput{RoleName: aws.String("ecsInstanceRole")})
	if err == nil {
		instanceRole = *grres.Role.Arn
	} else {
		irPolicy := AssumeRolePolicy{
			Version: "2012-10-17",
			Statement: []RoleStatement{
				{
					Effect:    "Allow",
					Principal: map[string]string{"Service": "ec2.amazonaws.com"},
					Action:    "sts:AssumeRole",
				},
			},
		}
		irBinary, err := json.Marshal(irPolicy)
		if err != nil {
			return nil, fmt.Errorf("error marshaling assume role policy for ecsInstanceRole: %v", err)
		}
		cr, err := iamCli.CreateRole(context.Background(), &iam.CreateRoleInput{
			AssumeRolePolicyDocument: aws.String(string(irBinary)),
			RoleName:                 aws.String("ecsInstanceRole"),
		})
		if err != nil {
			return nil, fmt.Errorf("error creating ecsInstanceRole: %v", err)
		}
		_, err = iamCli.AttachRolePolicy(context.Background(), &iam.AttachRolePolicyInput{
			PolicyArn: aws.String("arn:aws:iam::aws:policy/AmazonEC2ContainerServiceforEC2Role"),
			RoleName:  aws.String("ecsInstanceRole"),
		})
		if err != nil {
			return nil, fmt.Errorf("error attaching policies to ecsInstanceRole: %v", err)
		}
		instanceRole = *cr.Role.Arn
	}

	input := &batch.CreateComputeEnvironmentInput{
		ComputeEnvironmentName: aws.String(b.conf.ComputeEnv.Name),
		ComputeResources: &types.ComputeResource{
			InstanceRole:     aws.String(instanceRole),
			InstanceTypes:    b.conf.ComputeEnv.InstanceTypes,
			MaxvCpus:         aws.Int32(int32(b.conf.ComputeEnv.MaxVCPUs)),
			MinvCpus:         aws.Int32(int32(b.conf.ComputeEnv.MinVCPUs)),
			SecurityGroupIds: securityGroupIds,
			Subnets:          subnets,
			Tags:             b.conf.ComputeEnv.Tags,
			Type:             "EC2",
			ImageId:          aws.String(b.conf.ComputeEnv.ImageID),
		},
		ServiceRole: aws.String(serviceRole),
		State:       "ENABLED",
		Type:        "MANAGED",
	}
	_, err = batchCli.CreateComputeEnvironment(context.Background(), input)
	if err != nil {
		return nil, fmt.Errorf("error creating ComputeEnvironment: %v", err)
	}

	resp, err = batchCli.DescribeComputeEnvironments(context.Background(), &batch.DescribeComputeEnvironmentsInput{
		ComputeEnvironments: []string{b.conf.ComputeEnv.Name},
	})
	if err != nil {
		return nil, fmt.Errorf("error getting ComputeEnvironmentDetail: %w", err)
	}
	if len(resp.ComputeEnvironments) > 0 {
		return &resp.ComputeEnvironments[0], nil
	}
	return nil, fmt.Errorf("unexpected error - failed to get ComputeEnvironmentDetail")
}

func (b *batchsvc) CreateJobQueue() (*types.JobQueueDetail, error) {
	batchCli := batch.NewFromConfig(b.sess)

	resp, err := batchCli.DescribeJobQueues(context.Background(), &batch.DescribeJobQueuesInput{
		JobQueues: []string{b.conf.JobQueue.Name},
	})
	if err != nil {
		return nil, fmt.Errorf("error describing JobQueue: %w", err)
	}
	if len(resp.JobQueues) > 0 {
		return &resp.JobQueues[0], errResourceExists{}
	}

	var envs []types.ComputeEnvironmentOrder
	for i, c := range b.conf.JobQueue.ComputeEnvs {
		envs = append(envs, types.ComputeEnvironmentOrder{
			ComputeEnvironment: aws.String(c),
			Order:              aws.Int32(int32(i)),
		})
	}

	input := &batch.CreateJobQueueInput{
		ComputeEnvironmentOrder: envs,
		JobQueueName:            aws.String(b.conf.JobQueue.Name),
		Priority:                aws.Int32(int32(b.conf.JobQueue.Priority)),
		State:                   "ENABLED",
	}
	_, err = batchCli.CreateJobQueue(context.Background(), input)
	if err != nil {
		return nil, fmt.Errorf("error creating JobQueue: %w", err)
	}

	resp, err = batchCli.DescribeJobQueues(context.Background(), &batch.DescribeJobQueuesInput{
		JobQueues: []string{b.conf.JobQueue.Name},
	})
	if err != nil {
		return nil, fmt.Errorf("error getting JobQueueDetail: %w", err)
	}
	if len(resp.JobQueues) > 0 {
		return &resp.JobQueues[0], nil
	}
	return nil, fmt.Errorf("unexpected error - failed to get JobQueueDetail")
}

func (b *batchsvc) CreateJobRole() (string, error) {
	iamCli := iam.NewFromConfig(b.sess)

	resp, err := iamCli.GetRole(context.Background(), &iam.GetRoleInput{
		RoleName: aws.String(b.conf.JobRole.RoleName),
	})
	if err == nil {
		return *resp.Role.Arn, errResourceExists{}
	}

	roleb, err := json.Marshal(b.conf.JobRole.Policies.AssumeRole)
	if err != nil {
		return "", fmt.Errorf("error creating assume role policy")
	}

	cr, err := iamCli.CreateRole(context.Background(), &iam.CreateRoleInput{
		AssumeRolePolicyDocument: aws.String(string(roleb)),
		RoleName:                 aws.String(b.conf.JobRole.RoleName),
	})
	if err != nil {
		return "", fmt.Errorf("error creating role: %v", err)
	}

	return *cr.Role.Arn, nil
}

func (b *batchsvc) AttachRolePolicies() error {
	iamCli := iam.NewFromConfig(b.sess)

	resp, err := iamCli.ListRolePolicies(context.Background(), &iam.ListRolePoliciesInput{
		RoleName: aws.String(b.conf.JobRole.RoleName),
	})
	if err != nil {
		return err
	}
	if len(resp.PolicyNames) > 0 {
		policies := ""
		for _, v := range resp.PolicyNames {
			policies += v
		}
		if strings.Contains(policies, b.conf.JobRole.DynamoDBPolicyName) && strings.Contains(policies, b.conf.JobRole.S3PolicyName) {
			return errResourceExists{}
		}
	}

	s3b, err := json.Marshal(b.conf.JobRole.Policies.S3)
	if err != nil {
		return fmt.Errorf("error creating s3 policy")
	}

	dynb, err := json.Marshal(b.conf.JobRole.Policies.DynamoDB)
	if err != nil {
		return fmt.Errorf("error creating dynamodb policy")
	}

	_, err = iamCli.PutRolePolicy(context.Background(), &iam.PutRolePolicyInput{
		RoleName:       aws.String(b.conf.JobRole.RoleName),
		PolicyDocument: aws.String(string(s3b)),
		PolicyName:     aws.String(b.conf.JobRole.S3PolicyName),
	})
	if err != nil {
		return err
	}

	_, err = iamCli.PutRolePolicy(context.Background(), &iam.PutRolePolicyInput{
		RoleName:       aws.String(b.conf.JobRole.RoleName),
		PolicyDocument: aws.String(string(dynb)),
		PolicyName:     aws.String(b.conf.JobRole.DynamoDBPolicyName),
	})
	if err != nil {
		return err
	}

	return nil
}

func (b *batchsvc) CreateJobDefinition(overwrite bool) (*types.JobDefinition, error) {
	batchCli := batch.NewFromConfig(b.sess)

	if !overwrite {
		// TODO need paging if there are more than 100 revisions
		resp, err := batchCli.DescribeJobDefinitions(context.Background(), &batch.DescribeJobDefinitionsInput{
			JobDefinitionName: aws.String(b.conf.JobDef.Name),
			Status:            aws.String("ACTIVE"),
			MaxResults:        aws.Int32(100),
		})
		if err != nil {
			return nil, fmt.Errorf("error describing JobDefinition: %w", err)
		}
		if len(resp.JobDefinitions) > 0 {
			jobDefs := resp.JobDefinitions
			sort.Sort(byRevision(jobDefs))
			return &jobDefs[0], errResourceExists{}
		}
	}

	var jobRole string
	var err error
	if b.conf.JobDef.JobRoleArn != "" {
		jobRole = b.conf.JobDef.JobRoleArn
	} else {
		jobRole, err = b.CreateJobRole()
		if err != nil {
			_, ok := err.(errResourceExists)
			if !ok {
				return nil, err
			}
		}
		err = b.AttachRolePolicies()
		if err != nil {
			_, ok := err.(errResourceExists)
			if !ok {
				return nil, err
			}
		}
	}

	jobDef := &batch.RegisterJobDefinitionInput{
		ContainerProperties: &types.ContainerProperties{
			Image: aws.String(b.conf.JobDef.Image),
			ResourceRequirements: []types.ResourceRequirement{
				{Type: types.ResourceTypeMemory, Value: aws.String(strconv.FormatInt(b.conf.JobDef.MemoryMiB, 10))},
				{Type: types.ResourceTypeVcpu, Value: aws.String(strconv.FormatInt(b.conf.JobDef.VCPUs, 10))},
			},
			Privileged: aws.Bool(true),
			MountPoints: []types.MountPoint{
				{
					SourceVolume:  aws.String("docker_sock"),
					ContainerPath: aws.String("/var/run/docker.sock"),
				},
				{
					SourceVolume:  aws.String("funnel-work-dir"),
					ContainerPath: aws.String(b.conf.Funnel.Worker.WorkDir),
				},
			},
			Volumes: []types.Volume{
				{
					Name: aws.String("docker_sock"),
					Host: &types.Host{
						SourcePath: aws.String("/var/run/docker.sock"),
					},
				},
				{
					Name: aws.String("funnel-work-dir"),
					Host: &types.Host{
						SourcePath: aws.String(b.conf.Funnel.Worker.WorkDir),
					},
				},
			},
			Command: []string{
				"worker",
				"run",
				"--Compute",
				"aws-batch",
				"--Worker.WorkDir",
				b.conf.Funnel.Worker.WorkDir,
				"--Database",
				b.conf.Funnel.Database,
				"--DynamoDB.Region",
				b.conf.Funnel.DynamoDB.AWSConfig.Region,
				"--DynamoDB.TableBasename",
				b.conf.Funnel.DynamoDB.TableBasename,
				"--taskID",
				// This is a template variable that will be replaced with the taskID.
				"Ref::taskID",
			},
			JobRoleArn: aws.String(jobRole),
		},
		JobDefinitionName: aws.String(b.conf.JobDef.Name),
		Type:              "container",
	}
	for _, val := range b.conf.Funnel.EventWriters {
		jobDef.ContainerProperties.Command = append(jobDef.ContainerProperties.Command, "--EventWriters", val)
	}

	_, err = batchCli.RegisterJobDefinition(context.Background(), jobDef)
	if err != nil {
		return nil, fmt.Errorf("error registering JobDefinition: %v", err)
	}

	// TODO need paging if there are more than 100 revisions
	resp, err := batchCli.DescribeJobDefinitions(context.Background(), &batch.DescribeJobDefinitionsInput{
		JobDefinitionName: aws.String(b.conf.JobDef.Name),
		Status:            aws.String("ACTIVE"),
		MaxResults:        aws.Int32(100),
	})
	if err != nil {
		return nil, fmt.Errorf("error getting JobDefintion: %w", err)
	}
	if len(resp.JobDefinitions) > 0 {
		jobDefs := resp.JobDefinitions
		sort.Sort(byRevision(jobDefs))
		return &jobDefs[0], nil
	}
	return nil, fmt.Errorf("unexpected error - failed to get JobDefintion")
}
