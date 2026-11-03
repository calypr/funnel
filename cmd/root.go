// Package cmd contains the Funnel CLI commands.
package cmd

import (
	"github.com/calypr/funnel/cmd/aws"
	"github.com/calypr/funnel/cmd/debug"
	"github.com/calypr/funnel/cmd/examples"
	"github.com/calypr/funnel/cmd/gce"
	"github.com/calypr/funnel/cmd/kubernetes"
	"github.com/calypr/funnel/cmd/node"
	"github.com/calypr/funnel/cmd/run"
	"github.com/calypr/funnel/cmd/server"
	"github.com/calypr/funnel/cmd/storage"
	"github.com/calypr/funnel/cmd/task"
	"github.com/calypr/funnel/cmd/termdash"
	"github.com/calypr/funnel/cmd/version"
	"github.com/calypr/funnel/cmd/worker"
	"github.com/spf13/cobra"
)

// RootCmd represents the root command
var RootCmd = &cobra.Command{
	Use:           "funnel",
	SilenceErrors: true,
	SilenceUsage:  true,
}

func init() {
	RootCmd.AddCommand(aws.Cmd)
	RootCmd.AddCommand(debug.NewCommand())
	RootCmd.AddCommand(examples.Cmd)
	RootCmd.AddCommand(gce.Cmd)
	RootCmd.AddCommand(kubernetes.Cmd)
	RootCmd.AddCommand(completionCmd)
	RootCmd.AddCommand(genMarkdownCmd)
	RootCmd.AddCommand(node.NewCommand())
	RootCmd.AddCommand(run.Cmd)
	RootCmd.AddCommand(server.NewCommand())
	RootCmd.AddCommand(storage.NewCommand())
	RootCmd.AddCommand(task.NewCommand())
	RootCmd.AddCommand(termdash.Cmd)
	RootCmd.AddCommand(version.Cmd)
	RootCmd.AddCommand(worker.NewCommand())
}
