package worker

import (
	"context"
	"fmt"
	"syscall"
	"time"

	cmdutil "github.com/ohsu-comp-bio/funnel/cmd/util"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/util"
	"github.com/spf13/cobra"
)

// Options holds a few CLI options for worker entrypoints.
type Options struct {
	TaskID     string
	TaskFile   string
	TaskBase64 string
}

// NewCommand returns the worker command
func NewCommand() *cobra.Command {
	cmd, _ := newCommandHooks()
	return cmd
}

type hooks struct {
	Run func(ctx context.Context, conf *config.Config, log *logger.Logger, opts *Options) error
}

func newCommandHooks() (*cobra.Command, *hooks) {
	hooks := &hooks{
		Run: Run,
	}

	var (
		configFile string
		conf       *config.Config
		flagConf   *config.Config = config.EmptyConfig()
	)
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Funnel worker commands.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			var err error
			conf, err = cmdutil.MergeConfigFileWithFlags(configFile, flagConf)
			if err != nil {
				return fmt.Errorf("error processing config: %v", err)
			}

			return nil
		},
	}
	workerFlags := cmdutil.WorkerFlags(flagConf, &configFile)
	cmd.SetGlobalNormalizationFunc(cmdutil.NormalizeFlags)
	f := cmd.PersistentFlags()
	f.AddFlagSet(workerFlags)

	run := &cobra.Command{
		Use:   "run",
		Short: "Run a task directly, bypassing the server.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.TaskID == "" && opts.TaskFile == "" && opts.TaskBase64 == "" {
				return fmt.Errorf("no task was provided")
			}

			log := logger.NewLogger("worker", conf.Logger)
			logger.SetGRPCLogger(log.Sub("worker-grpc"))
			ctx, cancel := context.WithCancel(context.Background())
			ctx = util.SignalContext(ctx, time.Millisecond*500, syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return hooks.Run(ctx, conf, log, opts)
		},
	}

	f = run.Flags()
	f.StringVarP(&opts.TaskID, "taskID", "t", opts.TaskID, "Task ID")
	f.StringVarP(&opts.TaskFile, "taskFile", "f", opts.TaskFile, "Task file")
	f.StringVarP(&opts.TaskBase64, "taskBase64", "b", opts.TaskBase64, "Task base64")
	cmd.AddCommand(run)

	return cmd, hooks
}
