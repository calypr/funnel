package server

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/calypr/funnel/internal/server/taskservice"

	"github.com/calypr/funnel/internal/compute"

	"github.com/calypr/funnel/api/config"
	cmdutil "github.com/calypr/funnel/internal/cli/util"
	"github.com/calypr/funnel/internal/compute/aws_batch"
	"github.com/calypr/funnel/internal/compute/gcp_batch"
	"github.com/calypr/funnel/internal/compute/hpc/gridengine"
	"github.com/calypr/funnel/internal/compute/hpc/htcondor"
	"github.com/calypr/funnel/internal/compute/hpc/pbs"
	"github.com/calypr/funnel/internal/compute/hpc/slurm"
	"github.com/calypr/funnel/internal/compute/kubernetes"
	"github.com/calypr/funnel/internal/compute/local"
	"github.com/calypr/funnel/internal/compute/noop"
	"github.com/calypr/funnel/internal/compute/scheduler"
	appconfig "github.com/calypr/funnel/internal/config"
	"github.com/calypr/funnel/internal/database/badger"
	"github.com/calypr/funnel/internal/database/boltdb"
	"github.com/calypr/funnel/internal/database/datastore"
	"github.com/calypr/funnel/internal/database/dynamodb"
	"github.com/calypr/funnel/internal/database/mongodb"
	"github.com/calypr/funnel/internal/database/postgres"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/logger"
	"github.com/calypr/funnel/internal/metrics"
	"github.com/calypr/funnel/internal/pluginmanager"
	"github.com/calypr/funnel/internal/server"
	taskpolicy "github.com/calypr/funnel/internal/task"
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
	closeOnce      sync.Once
	cancelLifetime context.CancelFunc
	writers        events.MultiWriter
	tasks          *taskservice.Service
}

// Database represents the base funnel database interface
type Database interface {
	taskpolicy.ReadOnlyServer
	Init() error
}

// NewServer returns a new Funnel server + scheduler based on the given config.
func NewServer(ctx context.Context, conf *config.Config, log *logger.Logger) (serverResult *Server, returnErr error) {
	lifetimeCtx, cancelLifetime := context.WithCancel(ctx)
	var writers events.MultiWriter
	defer func() {
		if serverResult == nil {
			cancelLifetime()
			writers.Close()
		}
	}()

	var database Database
	var reader taskpolicy.ReadOnlyServer
	var nodes scheduler.SchedulerServiceServer
	var atomicAssigner scheduler.AtomicTaskAssigner
	var sched *scheduler.Scheduler
	var queue scheduler.TaskQueue

	log.Info("Initializing database", "backend", conf.Database)
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
	if assigner, ok := database.(scheduler.AtomicTaskAssigner); ok {
		atomicAssigner = assigner
	}

	executionClaimer, _ := database.(events.ExecutionClaimer)

	// Initialize the Database
	// Note: This is where Funnel waits until the given database is ready to accept requests.
	if err := database.Init(); err != nil {
		return nil, fmt.Errorf("error creating database resources: %v", err)
	}
	log.Info("Database initialized", "backend", conf.Database)

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
		case "file":
			writer, err = cmdutil.NewEventFileWriter(conf.EventFile, log)
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
		case "kafka":
			writer, err = events.NewKafkaWriter(lifetimeCtx, conf.Kafka)
		case "pubsub":
			writer, err = events.NewPubSubWriter(lifetimeCtx, conf.PubSub)
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
	var computer compute.Computer
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

		if atomicAssigner == nil {
			return nil, fmt.Errorf("database %s does not implement atomic task assignment", conf.Database)
		}
		if _, ok := nodes.(scheduler.NodeHealthStore); !ok {
			return nil, fmt.Errorf("database %s does not implement heartbeat-fenced node state updates", conf.Database)
		}

		sched = &scheduler.Scheduler{
			Conf:           conf.Scheduler,
			Log:            log.Sub("scheduler"),
			Nodes:          nodes,
			Queue:          queue,
			AtomicAssigner: atomicAssigner,
			Event:          &events.ErrLogger{Writer: writer, Log: log.Sub("scheduler")},
		}
		computer = compute.Backend{}

	case "aws-batch", "aws", "amazon":
		computer, err = aws_batch.NewBackend(lifetimeCtx, conf.AWSBatch, reader, writer, log.Sub("aws-batch"))
		if err != nil {
			return nil, err
		}

	case "gcp-batch", "gcp", "google":
		computer, err = gcp_batch.NewBackend(lifetimeCtx, conf.GCPBatch, reader, writer, log.Sub("gcp-batch"))
		if err != nil {
			return nil, err
		}

	case "gridengine":
		computer, err = gridengine.NewBackend(conf, reader, writer, log.Sub("gridengine"))
		if err != nil {
			return nil, err
		}

	case "htcondor":
		computer, err = htcondor.NewBackend(lifetimeCtx, conf, reader, writer, log.Sub("htcondor"))
		if err != nil {
			return nil, err
		}

	case "kubernetes", "k8s":
		computer, err = kubernetes.NewBackend(lifetimeCtx, conf, reader, writer, log.Sub("kubernetes"))
		if err != nil {
			return nil, err
		}

	case "local":
		computer, err = local.NewBackend(lifetimeCtx, conf, log.Sub("local"))
		if err != nil {
			return nil, err
		}

	case "noop":
		computer = noop.NewBackend()

	case "pbs":
		computer, err = pbs.NewBackend(lifetimeCtx, conf, reader, writer, log.Sub("pbs"))
		if err != nil {
			return nil, err
		}

	case "slurm":
		computer, err = slurm.NewBackend(lifetimeCtx, conf, reader, writer, log.Sub("slurm"))
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unknown compute backend: '%s'", conf.Compute)
	}

	writer = &events.ErrLogger{Writer: writer, Log: log}

	if c, ok := reader.(metrics.TaskStateCounter); ok {
		go metrics.WatchTaskStates(lifetimeCtx, c)
	}

	if nodes != nil {
		go metrics.WatchNodes(lifetimeCtx, nodes)
	}

	tasks := &taskservice.Service{
		Name:    conf.Server.ServiceName,
		Event:   writer,
		Compute: computer,
		Read:    reader,
		Log:     log,
		Config:  conf,
	}
	defer func() {
		if returnErr != nil {
			tasks.Close()
		}
	}()

	serverConf := &Server{
		Server: &server.Server{
			RPCAddress:       ":" + conf.Server.RPCPort,
			HTTPPort:         conf.Server.HTTPPort,
			BasicAuth:        conf.Server.BasicAuth,
			OidcAuth:         conf.Server.OidcAuth,
			DisableHTTPCache: conf.Server.DisableHTTPCache,
			MaxResponseBytes: appconfig.ResponseBytes(conf.Server),
			TaskAccess:       conf.Server.TaskAccess,
			Log:              log,
			Tasks:            tasks,
			Events:           &events.Service{Writer: writer, Claimer: executionClaimer},
			Nodes:            nodes,
			Plugins:          conf.Plugins,
		},
		Scheduler:      sched,
		cancelLifetime: cancelLifetime,
		writers:        writers,
		tasks:          tasks,
	}

	if conf.Plugins != nil {
		if conf.Plugins.Path == "" {
			return nil, fmt.Errorf("Plugin config is set but required plugin field 'Path' is not found")
		}
		tasks.PluginManager = &pluginmanager.Manager{}
		log.Info("getting plugin client", "path", conf.Plugins.Path)
		plugin, err := tasks.PluginManager.Client(conf.Plugins.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to get plugin client: %w", err)
		}
		tasks.Plugin = plugin
		serverConf.Server.AuthPlugin = plugin
	}

	return serverConf, nil
}

// Run runs a default Funnel server.
// This opens a database, and starts an API server, scheduler and task logger.
// This blocks indefinitely.
func (s *Server) Run(ctx context.Context) error {
	defer s.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start server
	errch := make(chan error, 2)
	componentCount := 1
	go func() {
		errch <- s.Server.Serve(ctx)
	}()

	// Start Scheduler
	if s.Scheduler != nil {
		componentCount++
		go func() {
			errch <- s.Scheduler.Run(ctx)
		}()
	}

	firstErr := <-errch
	cancel()
	for range componentCount - 1 {
		if err := <-errch; firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Close releases the resources acquired while constructing the server.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		if s.cancelLifetime != nil {
			s.cancelLifetime()
		}
		if s.tasks != nil {
			s.tasks.Close()
		}
		s.writers.Close()
	})
}

func dberr(err error) error {
	return fmt.Errorf("error occurred while connecting to or creating the database: %v", err)
}
