// Package cmd contains the Funnel CLI commands.
package cli

import (
	"github.com/calypr/funnel/internal/cli/aws"
	"github.com/calypr/funnel/internal/cli/debug"
	"github.com/calypr/funnel/internal/cli/examples"
	"github.com/calypr/funnel/internal/cli/gce"
	"github.com/calypr/funnel/internal/cli/kubernetes"
	"github.com/calypr/funnel/internal/cli/node"
	"github.com/calypr/funnel/internal/cli/run"
	"github.com/calypr/funnel/internal/cli/server"
	"github.com/calypr/funnel/internal/cli/storage"
	"github.com/calypr/funnel/internal/cli/task"
	"github.com/calypr/funnel/internal/cli/termdash"
	"github.com/calypr/funnel/internal/cli/version"
	"github.com/calypr/funnel/internal/cli/worker"
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
