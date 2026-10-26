package gce

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/calypr/funnel/cmd/node"
	"github.com/calypr/funnel/cmd/server"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/config/gce"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/util"
	"github.com/spf13/cobra"
)

// Cmd represents the 'funnel gce" CLI command set.
var Cmd = &cobra.Command{
	Use: "gce",
}

func init() {
	Cmd.AddCommand(runCmd)
}

var runCmd = &cobra.Command{
	Use: "run",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		ctx = util.SignalContext(ctx, time.Second*5, syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		conf := config.DefaultConfig()

		// Check that this is a GCE VM environment.
		// If not, fail.
		meta, merr := gce.LoadMetadata()
		if merr != nil {
			return fmt.Errorf("can't find GCE metadata. This command requires a GCE environment")
		}

		var err error
		conf, err = gce.WithMetadataConfig(conf, meta)
		if err != nil {
			return err
		}

		if conf.Node.ID != "" {
			return node.Run(ctx, conf, logger.NewLogger("node", conf.Logger))
		}

		return server.Run(ctx, conf, logger.NewLogger("server", conf.Logger))
	},
}
