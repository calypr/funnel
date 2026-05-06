package batch

import (
	"fmt"

	"github.com/ohsu-comp-bio/funnel/cmd/util"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/spf13/cobra"
)

var force = false

func init() {
	jobDefCmd.SetGlobalNormalizationFunc(util.NormalizeFlags)
	f := jobDefCmd.Flags()
	f.StringVarP(&funnelConfigFile, "config", "c", funnelConfigFile, "Funnel configuration file")
	f.BoolVar(&force, "force", force, "If the JobDefinition exists, this flag controls whether a new revision be created.")
	f.StringVarP(&conf.Region, "Region", "r", conf.Region, "Region in which to create the Batch resources")
	f.StringVar(&conf.JobDef.Name, "JobDef.Name", conf.JobDef.Name, "The name of the job definition.")
	f.StringVar(&conf.JobDef.Image, "JobDef.Image", conf.JobDef.Image, "The docker image used to start a container.")
	f.Int64Var(&conf.JobDef.MemoryMiB, "JobDef.MemoryMiB", conf.JobDef.MemoryMiB, "The hard limit (in MiB) of memory to present to the container.")
	f.Int64Var(&conf.JobDef.VCPUs, "JobDef.VCPUs", conf.JobDef.VCPUs, "The number of vCPUs reserved for the container.")
	f.StringVar(&conf.JobDef.JobRoleArn, "JobDef.JobRoleArn", conf.JobDef.JobRoleArn, "The Amazon Resource Name (ARN) of the IAM role that the container can assume for AWS permissions. A role will be created if not provided.")
}

var jobDefCmd = &cobra.Command{
	Use:   "create-job-def",
	Short: "Revise a job definition",
	RunE: func(cmd *cobra.Command, args []string) error {
		log := logger.NewLogger("batch-create-job-def", logger.DefaultConfig())

		if conf.Region == "" {
			return fmt.Errorf("error must provide a region")
		}

		conf.Funnel.DynamoDB.AWSConfig.Region = conf.Region

		if funnelConfigFile != "" {
			funnelConf := &config.Config{}
			config.ParseFile(funnelConfigFile, funnelConf)
			conf.Funnel = funnelConf
		}

		cli, err := newBatchSvc(conf)
		if err != nil {
			return err
		}

		c, err := cli.CreateJobDefinition(force)
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
