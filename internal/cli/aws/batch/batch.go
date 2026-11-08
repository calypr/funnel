package batch

import (
	"github.com/spf13/cobra"
)

var conf = DefaultConfig()
var funnelConfigFile string

// Cmd represents the aws batch command
var Cmd = &cobra.Command{
	Use:   "batch",
	Short: "Utilities for managing funnel resources on AWS Batch",
}

func init() {
	Cmd.AddCommand(createCmd)
	Cmd.AddCommand(jobDefCmd)
}
