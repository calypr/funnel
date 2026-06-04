package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/ohsu-comp-bio/funnel/compute/aws_batch"
	"github.com/ohsu-comp-bio/funnel/compute/gcp_batch"
	"github.com/ohsu-comp-bio/funnel/compute/gridengine"
	"github.com/ohsu-comp-bio/funnel/compute/htcondor"
	"github.com/ohsu-comp-bio/funnel/compute/kubernetes"
	"github.com/ohsu-comp-bio/funnel/compute/local"
	"github.com/ohsu-comp-bio/funnel/compute/noop"
	"github.com/ohsu-comp-bio/funnel/compute/pbs"
	"github.com/ohsu-comp-bio/funnel/compute/scheduler"
	"github.com/ohsu-comp-bio/funnel/compute/slurm"
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
	"github.com/ohsu-comp-bio/funnel/metrics"
	"github.com/ohsu-comp-bio/funnel/plugins/shared"
	"github.com/ohsu-comp-bio/funnel/server"
	"github.com/ohsu-comp-bio/funnel/tes"
)

// Run runs the "server run" command.
func Run(ctx context.Context, conf *config.Config, log *logger.Logger) error {
	s, err := NewServer(ctx, conf, log)
	if err != nil {
		return err
	}

	return s.Run(ctx)
}

// Server is a Funnel server + scheduler.
type Server struct {
	*server.Server
	*scheduler.Scheduler
}

// Database represents the base funnel database interface
type Database interface {
	tes.ReadOnlyServer
	Init() error
}

// NewServer returns a new Funnel server + scheduler based on the given config.
func NewServer(ctx context.Context, conf *config.Config, log *logger.Logger) (*Server, error) {
	log.Debug("NewServer", "config", conf.Safe())

	var database Database
	var reader tes.ReadOnlyServer
	var nodes scheduler.SchedulerServiceServer
	var sched *scheduler.Scheduler
	var queue scheduler.TaskQueue

	writers := events.MultiWriter{}

	// Database
	switch strings.ToLower(conf.Database) {
	case "boltdb":
		b, err := boltdb.NewBoltDB(conf.BoltDB)
		if err != nil {
			return nil, dberr(err)
		}
		database = b
		reader = b
		nodes = b
		queue = b
		writers = append(writers, b)

	case "badger":
		b, err := badger.NewBadger(conf.Badger)
		if err != nil {
			return nil, dberr(err)
		}
		database = b
		reader = b
		writers = append(writers, b)

	case "datastore":
		d, err := datastore.NewDatastore(conf.Datastore)
		if err != nil {
			return nil, dberr(err)
		}
		database = d
		reader = d
		writers = append(writers, d)

	case "dynamodb":
		d, err := dynamodb.NewDynamoDB(conf.DynamoDB)
		if err != nil {
			return nil, dberr(err)
		}
		database = d
		reader = d
		writers = append(writers, d)

	case "elastic":
		e, err := elastic.NewElastic(conf.Elastic)
		if err != nil {
			return nil, dberr(err)
		}
		database = e
		reader = e
		nodes = e
		queue = e
		writers = append(writers, e)

	case "mongodb":
		m, err := mongodb.NewMongoDB(conf.MongoDB)
		if err != nil {
			return nil, dberr(err)
		}
		database = m
		reader = m
		nodes = m
		queue = m
		writers = append(writers, m)

	case "postgres", "psql":
		p, err := postgres.NewPostgres(conf.Postgres)
		if err != nil {
			return nil, dberr(err)
		}
		database = p
		reader = p
		nodes = p
		queue = p
		writers = append(writers, p)

	default:
		return nil, fmt.Errorf("unknown database: '%s'", conf.Database)
	}

	// Initialize the Database
	// Note: This is where Funnel waits until the given database is ready to accept requests.
	if err := database.Init(); err != nil {
		return nil, fmt.Errorf("error creating database resources: %v", err)
	}

	// Event writers
	var writer events.Writer
	var err error

	eventWriterSet := make(map[string]interface{})
	for _, w := range conf.EventWriters {
		eventWriterSet[strings.ToLower(w)] = nil
	}

	for e := range eventWriterSet {
		switch e {
		case strings.ToLower(conf.Database):
			continue
		case "log":
			if conf.Compute == "slurm" {
				writer = &events.Logger{Log: log}
			} else {
				continue
			}
		case "boltdb":
			writer, err = boltdb.NewBoltDB(conf.BoltDB)
		case "badger":
			writer, err = badger.NewBadger(conf.Badger)
		case "dynamodb":
			writer, err = dynamodb.NewDynamoDB(conf.DynamoDB)
		case "elastic":
			writer, err = elastic.NewElastic(conf.Elastic)
		case "kafka":
			writer, err = events.NewKafkaWriter(ctx, conf.Kafka)
		case "pubsub":
			writer, err = events.NewPubSubWriter(ctx, conf.PubSub)
		case "mongodb":
			writer, err = mongodb.NewMongoDB(conf.MongoDB)
		case "postgres", "psql":
			writer, err = postgres.NewPostgres(conf.Postgres)
		default:
			return nil, fmt.Errorf("unknown event writer: '%s'", e)
		}
		if err != nil {
			return nil, fmt.Errorf("error occurred while initializing the %s event writer: %v", e, err)
		}
		if writer != nil {
			writers = append(writers, writer)
		}
	}

	writer = &events.SystemLogFilter{Writer: &writers, Level: conf.Logger.Level}

	// Compute
	var compute events.Computer
	switch strings.ToLower(conf.Compute) {
	case "manual":
		if nodes == nil {
			return nil, fmt.Errorf(
				"cannot enable manual compute backend, database %s does not implement "+
					"the scheduler service", conf.Database)
		}
		if queue == nil {
			return nil, fmt.Errorf(
				"cannot enable manual compute backend, database %s does not implement "+
					"a task queue", conf.Database)
		}

		sched = &scheduler.Scheduler{
			Conf:  conf.Scheduler,
			Log:   log.Sub("scheduler"),
			Nodes: nodes,
			Queue: queue,
			Event: &events.ErrLogger{Writer: writer, Log: log.Sub("scheduler")},
		}
		compute = events.Backend{}

	case "aws-batch", "aws", "amazon":
		compute, err = aws_batch.NewBackend(ctx, conf.AWSBatch, reader, writer, log.Sub("aws-batch"))
		if err != nil {
			return nil, err
		}

	case "gcp-batch", "gcp", "google":
		compute, err = gcp_batch.NewBackend(ctx, conf.GCPBatch, reader, writer, log.Sub("gcp-batch"))
		if err != nil {
			return nil, err
		}

	case "gridengine":
		compute, err = gridengine.NewBackend(conf, reader, writer, log.Sub("gridengine"))
		if err != nil {
			return nil, err
		}

	case "htcondor":
		compute, err = htcondor.NewBackend(ctx, conf, reader, writer, log.Sub("htcondor"))
		if err != nil {
			return nil, err
		}

	case "kubernetes", "k8s":
		compute, err = kubernetes.NewBackend(ctx, conf, reader, writer, log.Sub("kubernetes"))
		if err != nil {
			return nil, err
		}

	case "local":
		compute, err = local.NewBackend(ctx, conf, log.Sub("local"))
		if err != nil {
			return nil, err
		}

	case "noop":
		compute = noop.NewBackend()

	case "pbs":
		compute, err = pbs.NewBackend(ctx, conf, reader, writer, log.Sub("pbs"))
		if err != nil {
			return nil, err
		}

	case "slurm":
		compute, err = slurm.NewBackend(ctx, conf, reader, writer, log.Sub("slurm"))
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unknown compute backend: '%s'", conf.Compute)
	}

	writer = &events.ErrLogger{Writer: writer, Log: log}

	if c, ok := reader.(metrics.TaskStateCounter); ok {
		go metrics.WatchTaskStates(ctx, c)
	}

	if nodes != nil {
		go metrics.WatchNodes(ctx, nodes)
	}

	serverConf := &Server{
		Server: &server.Server{
			RPCAddress:       ":" + conf.Server.RPCPort,
			HTTPPort:         conf.Server.HTTPPort,
			BasicAuth:        conf.Server.BasicAuth,
			OidcAuth:         conf.Server.OidcAuth,
			DisableHTTPCache: conf.Server.DisableHTTPCache,
			TaskAccess:       conf.Server.TaskAccess,
			Log:              log,
			Tasks: &server.TaskService{
				Name:    conf.Server.ServiceName,
				Event:   writer,
				Compute: compute,
				Read:    reader,
				Log:     log,
				Config:  conf,
			},
			Events:  &events.Service{Writer: writer},
			Nodes:   nodes,
			Plugins: conf.Plugins,
		},
		Scheduler: sched,
	}

	if conf.Plugins != nil {
		if conf.Plugins.Path == "" {
			return nil, fmt.Errorf("Plugin config is set but required plugin field 'Path' is not found")
		}
		serverConf.Server.Tasks.(*server.TaskService).PluginManager = &shared.Manager{}
		if conf.Plugins == nil {
			return nil, fmt.Errorf("Plugin config is set but no plugins are defined")
		}
		if conf.Plugins.Path == "" {
			return nil, fmt.Errorf("Plugin config is set but required plugin field 'Path' is not found")
		}
		log.Info("getting plugin client", "path", conf.Plugins.Path)
		plugin, err := serverConf.Server.Tasks.(*server.TaskService).PluginManager.Client(conf.Plugins.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to get plugin client: %w", err)
		}
		serverConf.Server.Tasks.(*server.TaskService).Plugin = plugin
	}

	return serverConf, nil
}

// Run runs a default Funnel server.
// This opens a database, and starts an API server, scheduler and task logger.
// This blocks indefinitely.
func (s *Server) Run(ctx context.Context) error {

	// Start server
	errch := make(chan error)
	go func() {
		errch <- s.Server.Serve(ctx)
	}()

	// Start Scheduler
	if s.Scheduler != nil {
		go func() {
			errch <- s.Scheduler.Run(ctx)
		}()
	}

	// Block until done.
	// Server and scheduler must be stopped via the context.
	return <-errch
}

func dberr(err error) error {
	return fmt.Errorf("error occurred while connecting to or creating the database: %v", err)
}
