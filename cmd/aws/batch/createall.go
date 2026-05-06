package batch

import (
	"fmt"

	"github.com/ohsu-comp-bio/funnel/cmd/util"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/spf13/cobra"
)

func init() {
	createCmd.SetGlobalNormalizationFunc(util.NormalizeFlags)
	f := createCmd.Flags()
	f.StringVarP(&funnelConfigFile, "config", "c", funnelConfigFile, "Funnel configuration file")
	f.StringVarP(&conf.Region, "Region", "r", conf.Region, "Region in which to create the Batch resources")
	f.StringVar(&conf.ComputeEnv.Name, "ComputeEnv.Name", conf.ComputeEnv.Name, "The name of the compute environment.")
	f.StringVar(&conf.ComputeEnv.ImageID, "ComputeEnv.ImageID", conf.ComputeEnv.ImageID, "The Amazon Machine Image (AMI) ID used for instances launched in the compute environment. By default, uses the latest Amazon ECS-optimized AMI.")
	f.Int64Var(&conf.ComputeEnv.MinVCPUs, "ComputeEnv.MinVCPUs", conf.ComputeEnv.MinVCPUs, "The minimum number of EC2 vCPUs that an environment should maintain. (default 0)")
	f.Int64Var(&conf.ComputeEnv.MaxVCPUs, "ComputeEnv.MaxVCPUs", conf.ComputeEnv.MaxVCPUs, "The maximum number of EC2 vCPUs that an environment can reach.")
	f.StringSliceVar(&conf.ComputeEnv.SecurityGroupIds, "ComputEnv.SecurityGroupIds", conf.ComputeEnv.SecurityGroupIds, "The EC2 security groups that are associated with instances launched in the compute environment. If none are specified all security groups will be used.")
	f.StringSliceVar(&conf.ComputeEnv.Subnets, "ComputeEnv.Subnets", conf.ComputeEnv.Subnets, "The VPC subnets into which the compute resources are launched. If none are specified all subnets will be used.")
	f.StringSliceVar(&conf.ComputeEnv.InstanceTypes, "ComputeEnv.InstanceTypes", conf.ComputeEnv.InstanceTypes, "The instances types that may be launched. You can also choose optimal to pick instance types on the fly that match the demand of your job queues.")
	f.StringVar(&conf.JobQueue.Name, "JobQueue.Name", conf.JobQueue.Name, "The name of the job queue.")
	f.Int64Var(&conf.JobQueue.Priority, "JobQueue.Priority", conf.JobQueue.Priority, "The priority of the job queue. Priority is determined in descending order.")
	f.StringVar(&conf.JobDef.Name, "JobDef.Name", conf.JobDef.Name, "The name of the job definition.")
	f.StringVar(&conf.JobDef.Image, "JobDef.Image", conf.JobDef.Image, "The docker image used to start a container.")
	f.Int64Var(&conf.JobDef.MemoryMiB, "JobDef.MemoryMiB", conf.JobDef.MemoryMiB, "The hard limit (in MiB) of memory to present to the container.")
	f.Int64Var(&conf.JobDef.VCPUs, "JobDef.VCPUs", conf.JobDef.VCPUs, "The number of vCPUs reserved for the container.")
	f.StringVar(&conf.JobDef.JobRoleArn, "JobDef.JobRoleArn", conf.JobDef.JobRoleArn, "The Amazon Resource Name (ARN) of the IAM role that the container can assume for AWS permissions. A role will be created if not provided.")
}

var createCmd = &cobra.Command{
	Use:   "create-all-resources",
	Short: "Create a compute environment, job queue and job definition in a specified region",
	RunE: func(cmd *cobra.Command, args []string) error {
		log := logger.NewLogger("batch-create-all-resources", logger.DefaultConfig())

		if conf.Region == "" {
			return fmt.Errorf("error must provide a region")
		}

		conf.Funnel.DynamoDB.AWSConfig.Region = conf.Region

		if funnelConfigFile != "" {
			funnelConf := config.EmptyConfig()
			config.ParseFile(funnelConfigFile, funnelConf)
			conf.Funnel = funnelConf
		}

		cli, err := newBatchSvc(conf)
		if err != nil {
			return err
		}

		a, err := cli.CreateComputeEnvironment()
		switch err.(type) {
		case nil:
			log.Info("Created ComputeEnvironment", "description", a)
		case errResourceExists:
			log.Error("ComputeEnvironment already exists", "description", a)
		default:
			return fmt.Errorf("failed to create ComputeEnvironment: %v", err)
		}

		b, err := cli.CreateJobQueue()
		switch err.(type) {
		case nil:
			log.Info("Created JobQueue", "description", b)
		case errResourceExists:
			log.Error("JobQueue already exists", "description", b)
		default:
			return fmt.Errorf("failed to create JobQueue: %v", err)
		}

		c, err := cli.CreateJobDefinition(false)
		switch err.(type) {
		case nil:
			log.Info("Created JobDefinition", "description", c)
		case errResourceExists:
			log.Error("JobDefinition already exists", "description", c)
		default:
			return fmt.Errorf("failed to create JobDefinition: %v", err)
		}

		return nil
	},
}
