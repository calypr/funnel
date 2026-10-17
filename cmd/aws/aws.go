package aws

import (
	"github.com/calypr/funnel/cmd/aws/batch"
	"github.com/spf13/cobra"
)

// Cmd represents the task command
var Cmd = &cobra.Command{
	Use:   "aws",
	Short: "Development utilities for creating funnel resources on AWS",
}

func init() {
	Cmd.AddCommand(batch.Cmd)
}
