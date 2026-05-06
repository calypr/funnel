package batch

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/batch"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/iam"
	util "github.com/ohsu-comp-bio/funnel/util/aws"
)

type errResourceExists struct{}

func (e errResourceExists) Error() string {
	return "resource exists"
}

func newBatchSvc(conf *Config) (*batchsvc, error) {
	sess, err := util.NewAWSSession(conf.AWSConfig)
	if err != nil {
		return nil, fmt.Errorf("error occurred creating aws session: %v", err)
	}
	return &batchsvc{
		sess: sess,
		conf: conf,
	}, nil
}

type batchsvc struct {
	sess *session.Session
	conf *Config
}

func (b *batchsvc) CreateComputeEnvironment() (*batch.ComputeEnvironmentDetail, error) {
	batchCli := batch.New(b.sess)
	ec2Cli := ec2.New(b.sess)
	iamCli := iam.New(b.sess)

	resp, _ := batchCli.DescribeComputeEnvironments(&batch.DescribeComputeEnvironmentsInput{
		ComputeEnvironments: []*string{aws.String(b.conf.ComputeEnv.Name)},
	})
	if len(resp.ComputeEnvironments) > 0 {
		return resp.ComputeEnvironments[0], errResourceExists{}
	}

	securityGroupIds := []string{}
	if len(b.conf.ComputeEnv.SecurityGroupIds) > 0 {
		securityGroupIds = b.conf.ComputeEnv.SecurityGroupIds
	} else {
		sgres, err := ec2Cli.DescribeSecurityGroups(nil)
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
		snres, err := ec2Cli.DescribeSubnets(nil)
		if err != nil {
			return nil, err
		}
		for _, s := range snres.Subnets {
			subnets = append(subnets, *s.SubnetId)
		}
	}

	var serviceRole string
	grres, err := iamCli.GetRole(&iam.GetRoleInput{RoleName: aws.String("AWSBatchServiceRole")})
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
		cr, err := iamCli.CreateRole(&iam.CreateRoleInput{
			AssumeRolePolicyDocument: aws.String(string(bsrBinary)),
			RoleName:                 aws.String("AWSBatchServiceRole"),
		})
		if err != nil {
			return nil, fmt.Errorf("error creating AWSBatchServiceRole: %v", err)
		}
		_, err = iamCli.AttachRolePolicy(&iam.AttachRolePolicyInput{
			PolicyArn: aws.String("arn:aws:iam::aws:policy/service-role/AWSBatchServiceRole"),
			RoleName:  aws.String("AWSBatchServiceRole"),
		})
		if err != nil {
			return nil, fmt.Errorf("error attaching policies to AWSBatchServiceRole: %v", err)
		}
		serviceRole = *cr.Role.Arn
	}

	var instanceRole string
	grres, err = iamCli.GetRole(&iam.GetRoleInput{RoleName: aws.String("ecsInstanceRole")})
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
		cr, err := iamCli.CreateRole(&iam.CreateRoleInput{
			AssumeRolePolicyDocument: aws.String(string(irBinary)),
			RoleName:                 aws.String("ecsInstanceRole"),
		})
		if err != nil {
			return nil, fmt.Errorf("error creating ecsInstanceRole: %v", err)
		}
		_, err = iamCli.AttachRolePolicy(&iam.AttachRolePolicyInput{
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
		ComputeResources: &batch.ComputeResource{
			InstanceRole:     aws.String(instanceRole),
			InstanceTypes:    convertStringSlice(b.conf.ComputeEnv.InstanceTypes),
			MaxvCpus:         aws.Int64(b.conf.ComputeEnv.MaxVCPUs),
			MinvCpus:         aws.Int64(b.conf.ComputeEnv.MinVCPUs),
			SecurityGroupIds: convertStringSlice(securityGroupIds),
			Subnets:          convertStringSlice(subnets),
			Tags:             convertStringMap(b.conf.ComputeEnv.Tags),
			Type:             aws.String("EC2"),
			ImageId:          aws.String(b.conf.ComputeEnv.ImageID),
		},
		ServiceRole: aws.String(serviceRole),
		State:       aws.String("ENABLED"),
		Type:        aws.String("MANAGED"),
	}
	_, err = batchCli.CreateComputeEnvironment(input)
	if err != nil {
		return nil, fmt.Errorf("error creating ComputeEnvironment: %v", err)
	}

	resp, err = batchCli.DescribeComputeEnvironments(&batch.DescribeComputeEnvironmentsInput{
		ComputeEnvironments: []*string{aws.String(b.conf.ComputeEnv.Name)},
	})
	if err != nil {
		return nil, fmt.Errorf("error getting ComputeEnvironmentDetail: %v", err)
	}
	if len(resp.ComputeEnvironments) > 0 {
		return resp.ComputeEnvironments[0], nil
	}
	return nil, fmt.Errorf("unexpected error - failed to get ComputeEnvironmentDetail")
}

func (b *batchsvc) CreateJobQueue() (*batch.JobQueueDetail, error) {
	batchCli := batch.New(b.sess)

	resp, _ := batchCli.DescribeJobQueues(&batch.DescribeJobQueuesInput{
		JobQueues: []*string{aws.String(conf.JobQueue.Name)},
	})
	if len(resp.JobQueues) > 0 {
		return resp.JobQueues[0], errResourceExists{}
	}

	var envs []*batch.ComputeEnvironmentOrder
	for i, c := range b.conf.JobQueue.ComputeEnvs {
		envs = append(envs, &batch.ComputeEnvironmentOrder{
			ComputeEnvironment: aws.String(c),
			Order:              aws.Int64(int64(i)),
		})
	}

	input := &batch.CreateJobQueueInput{
		ComputeEnvironmentOrder: envs,
		JobQueueName:            aws.String(b.conf.JobQueue.Name),
		Priority:                aws.Int64(b.conf.JobQueue.Priority),
		State:                   aws.String("ENABLED"),
	}
	_, err := batchCli.CreateJobQueue(input)
	if err != nil {
		return nil, fmt.Errorf("error creating JobQueue: %v", err)
	}

	resp, err = batchCli.DescribeJobQueues(&batch.DescribeJobQueuesInput{
		JobQueues: []*string{aws.String(conf.JobQueue.Name)},
	})
	if err != nil {
		return nil, fmt.Errorf("error getting JobQueueDetail: %v", err)
	}
	if len(resp.JobQueues) > 0 {
		return resp.JobQueues[0], nil
	}
	return nil, fmt.Errorf("unexpected error - failed to get JobQueueDetail")
}

func (b *batchsvc) CreateJobRole() (string, error) {
	iamCli := iam.New(b.sess)

	resp, err := iamCli.GetRole(&iam.GetRoleInput{
		RoleName: aws.String(b.conf.JobRole.RoleName),
	})
	if err == nil {
		return *resp.Role.Arn, errResourceExists{}
	}

	roleb, err := json.Marshal(b.conf.JobRole.Policies.AssumeRole)
	if err != nil {
		return "", fmt.Errorf("error creating assume role policy")
	}

	cr, err := iamCli.CreateRole(&iam.CreateRoleInput{
		AssumeRolePolicyDocument: aws.String(string(roleb)),
		RoleName:                 aws.String(b.conf.JobRole.RoleName),
	})
	if err != nil {
		return "", fmt.Errorf("error creating role: %v", err)
	}

	return *cr.Role.Arn, nil
}

func (b *batchsvc) AttachRolePolicies() error {
	iamCli := iam.New(b.sess)

	resp, err := iamCli.ListRolePolicies(&iam.ListRolePoliciesInput{
		RoleName: aws.String(b.conf.JobRole.RoleName),
	})
	if err != nil {
		return err
	}
	if len(resp.PolicyNames) > 0 {
		policies := ""
		for _, v := range resp.PolicyNames {
			policies += *v
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

	_, err = iamCli.PutRolePolicy(&iam.PutRolePolicyInput{
		RoleName:       aws.String(b.conf.JobRole.RoleName),
		PolicyDocument: aws.String(string(s3b)),
		PolicyName:     aws.String(b.conf.JobRole.S3PolicyName),
	})
	if err != nil {
		return err
	}

	_, err = iamCli.PutRolePolicy(&iam.PutRolePolicyInput{
		RoleName:       aws.String(b.conf.JobRole.RoleName),
		PolicyDocument: aws.String(string(dynb)),
		PolicyName:     aws.String(b.conf.JobRole.DynamoDBPolicyName),
	})
	if err != nil {
		return err
	}

	return nil
}

func (b *batchsvc) CreateJobDefinition(overwrite bool) (*batch.JobDefinition, error) {
	batchCli := batch.New(b.sess)

	if !overwrite {
		// TODO need paging if there are more than 100 revisions
		resp, _ := batchCli.DescribeJobDefinitions(&batch.DescribeJobDefinitionsInput{
			JobDefinitionName: aws.String(b.conf.JobDef.Name),
			Status:            aws.String("ACTIVE"),
			MaxResults:        aws.Int64(100),
		})
		if len(resp.JobDefinitions) > 0 {
			jobDefs := resp.JobDefinitions
			sort.Sort(byRevision(jobDefs))
			return jobDefs[0], errResourceExists{}
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
		ContainerProperties: &batch.ContainerProperties{
			Image:      aws.String(b.conf.JobDef.Image),
			Memory:     aws.Int64(b.conf.JobDef.MemoryMiB),
			Vcpus:      aws.Int64(b.conf.JobDef.VCPUs),
			Privileged: aws.Bool(true),
			MountPoints: []*batch.MountPoint{
				{
					SourceVolume:  aws.String("docker_sock"),
					ContainerPath: aws.String("/var/run/docker.sock"),
				},
				{
					SourceVolume:  aws.String("funnel-work-dir"),
					ContainerPath: aws.String(b.conf.Funnel.Worker.WorkDir),
				},
			},
			Volumes: []*batch.Volume{
				{
					Name: aws.String("docker_sock"),
					Host: &batch.Host{
						SourcePath: aws.String("/var/run/docker.sock"),
					},
				},
				{
					Name: aws.String("funnel-work-dir"),
					Host: &batch.Host{
						SourcePath: aws.String(b.conf.Funnel.Worker.WorkDir),
					},
				},
			},
			Command: []*string{
				aws.String("worker"),
				aws.String("run"),
				aws.String("--Worker.WorkDir"),
				aws.String(b.conf.Funnel.Worker.WorkDir),
				aws.String("--Database"),
				aws.String(b.conf.Funnel.Database),
				aws.String("--DynamoDB.Region"),
				aws.String(b.conf.Funnel.DynamoDB.AWSConfig.Region),
				aws.String("--DynamoDB.TableBasename"),
				aws.String(b.conf.Funnel.DynamoDB.TableBasename),
				aws.String("--taskID"),
				// This is a template variable that will be replaced with the taskID.
				aws.String("Ref::taskID"),
			},
			JobRoleArn: aws.String(jobRole),
		},
		JobDefinitionName: aws.String(b.conf.JobDef.Name),
		Type:              aws.String("container"),
	}
	for _, val := range b.conf.Funnel.EventWriters {
		jobDef.ContainerProperties.Command = append(jobDef.ContainerProperties.Command, aws.String("--EventWriters"), aws.String(val))
	}

	_, err = batchCli.RegisterJobDefinition(jobDef)
	if err != nil {
		return nil, fmt.Errorf("error registering JobDefinition: %v", err)
	}

	// TODO need paging if there are more than 100 revisions
	resp, err := batchCli.DescribeJobDefinitions(&batch.DescribeJobDefinitionsInput{
		JobDefinitionName: aws.String(b.conf.JobDef.Name),
		Status:            aws.String("ACTIVE"),
		MaxResults:        aws.Int64(100),
	})
	if err != nil {
		return nil, fmt.Errorf("error getting JobDefintion: %v", err)
	}
	if len(resp.JobDefinitions) > 0 {
		jobDefs := resp.JobDefinitions
		sort.Sort(byRevision(jobDefs))
		return jobDefs[0], nil
	}
	return nil, fmt.Errorf("unexpected error - failed to get JobDefintion")
}

func convertStringSlice(s []string) []*string {
	var ret []*string
	for _, t := range s {
		ret = append(ret, aws.String(t))
	}
	return ret
}

func convertStringMap(s map[string]string) map[string]*string {
	m := map[string]*string{}
	for k, v := range s {
		m[k] = aws.String(v)
	}
	return m
}
