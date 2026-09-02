// Package kubernetes contains CLI commands for managing Funnel's Kubernetes resources.
package kubernetes

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"time"

	cmdutil "github.com/ohsu-comp-bio/funnel/cmd/util"
	k8sbackend "github.com/ohsu-comp-bio/funnel/compute/kubernetes"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/database/badger"
	"github.com/ohsu-comp-bio/funnel/database/boltdb"
	"github.com/ohsu-comp-bio/funnel/database/datastore"
	"github.com/ohsu-comp-bio/funnel/database/dynamodb"
	"github.com/ohsu-comp-bio/funnel/database/elastic"
	"github.com/ohsu-comp-bio/funnel/database/mongodb"
	"github.com/ohsu-comp-bio/funnel/database/postgres"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/util"
	"github.com/spf13/cobra"
)

// Cmd is the root "funnel kubernetes" command.
var Cmd = &cobra.Command{
	Use:     "kubernetes",
	Aliases: []string{"k8s"},
	Short:   "Funnel Kubernetes management commands.",
}

func init() {
	Cmd.AddCommand(cleanupCmd())
}

func cleanupCmd() *cobra.Command {
	var (
		configFile string
		flagConf   = config.EmptyConfig()
	)

	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Delete orphaned Funnel-managed Kubernetes resources with no matching task.",
		Long: `Scans Funnel-labeled Kubernetes resources (PVs, PVCs, ConfigMaps, ServiceAccounts,
Roles, RoleBindings) and deletes any whose task ID is no longer present or active
in the Funnel database. Intended to be run as a Kubernetes CronJob so that cleanup
is decoupled from the server lifecycle and multiple replicas do not race.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			conf, err := cmdutil.MergeConfigFileWithFlags(configFile, flagConf)
			if err != nil {
				return fmt.Errorf("error processing config: %v", err)
			}

			log := logger.NewLogger("kubernetes-cleanup", conf.Logger)

			ctx, cancel := context.WithCancel(context.Background())
			ctx = util.SignalContext(ctx, time.Millisecond*500, syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			// Open only the database — no HTTP/gRPC server needed.
			reader, err := openReader(ctx, conf)
			if err != nil {
				return fmt.Errorf("opening database: %v", err)
			}

			// Build a cleanup-only K8s backend. This connects to the same cluster as
			// the running Funnel Server (via in-cluster config) but, unlike the full
			// server backend, does not require a worker job template and does not start
			// the reconcile goroutine — cleanup only reads the database and deletes
			// orphaned resources. The event writer just logs, since cleanup never emits
			// task state events.
			backend, err := k8sbackend.NewCleanupBackend(conf, reader, &events.Logger{Log: log}, log)
			if err != nil {
				return fmt.Errorf("initializing kubernetes backend: %v", err)
			}

			log.Info("Starting orphaned resource cleanup",
				"namespace", conf.Kubernetes.JobsNamespace)

			// Reconcile task/job state and delete resources whose task is gone or terminal.
			backend.ReconcileOnce(ctx, false)

			log.Info("Orphaned resource cleanup complete")
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVarP(&configFile, "config", "c", "", "Path to Funnel config file")
	cmd.SetGlobalNormalizationFunc(cmdutil.NormalizeFlags)
	f.AddFlagSet(cmdutil.ServerFlags(flagConf, &configFile))

	return cmd
}

// openReader opens a read-only connection to the configured Funnel database.
func openReader(ctx context.Context, conf *config.Config) (tes.ReadOnlyServer, error) {
	switch strings.ToLower(conf.Database) {
	case "boltdb":
		return boltdb.NewBoltDB(conf.BoltDB)
	case "badger":
		return badger.NewBadger(conf.Badger)
	case "datastore":
		return datastore.NewDatastore(conf.Datastore)
	case "dynamodb":
		return dynamodb.NewDynamoDB(conf.DynamoDB)
	case "elastic":
		return elastic.NewElastic(conf.Elastic)
	case "mongodb":
		return mongodb.NewMongoDB(conf.MongoDB)
	case "postgres", "psql":
		return postgres.NewPostgres(conf.Postgres)
	default:
		return nil, fmt.Errorf("unknown database: '%s'", conf.Database)
	}
}
