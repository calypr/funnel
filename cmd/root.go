// Package cmd contains the Funnel CLI commands.
package cmd

import (
	"github.com/ohsu-comp-bio/funnel/cmd/aws"
	"github.com/ohsu-comp-bio/funnel/cmd/examples"
	"github.com/ohsu-comp-bio/funnel/cmd/gce"
	"github.com/ohsu-comp-bio/funnel/cmd/kubernetes"
	"github.com/ohsu-comp-bio/funnel/cmd/node"
	"github.com/ohsu-comp-bio/funnel/cmd/run"
	"github.com/ohsu-comp-bio/funnel/cmd/server"
	"github.com/ohsu-comp-bio/funnel/cmd/storage"
	"github.com/ohsu-comp-bio/funnel/cmd/task"
	"github.com/ohsu-comp-bio/funnel/cmd/termdash"
	"github.com/ohsu-comp-bio/funnel/cmd/version"
	"github.com/ohsu-comp-bio/funnel/cmd/worker"
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
