package worker

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/database/datastore"
	"github.com/ohsu-comp-bio/funnel/database/dynamodb"
	"github.com/ohsu-comp-bio/funnel/database/elastic"
	"github.com/ohsu-comp-bio/funnel/database/mongodb"
	"github.com/ohsu-comp-bio/funnel/database/postgres"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/storage"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/util"
	"github.com/ohsu-comp-bio/funnel/worker"
)

// Run runs the "worker run" command.
func Run(ctx context.Context, conf *config.Config, log *logger.Logger, opts *Options) error {
	w, err := NewWorker(ctx, conf, log, opts)
	if err != nil {
		return err
	}
	return w.Run(ctx)
}

// NewWorker returns a new Funnel worker based on the given config.
func NewWorker(ctx context.Context, conf *config.Config, log *logger.Logger, opts *Options) (*worker.DefaultWorker, error) {
	err := validateConfig(conf, opts)
	if err != nil {
		return nil, fmt.Errorf("validating config: %v", err)
	}

	// Construct a set of event writers based on the config.
	builder := eventWriterBuilder{}
	// If the task comes from a file or string,
	// don't assume we should write to the database.
	if opts.TaskFile == "" && opts.TaskBase64 == "" {
		builder.Add(ctx, conf.Database, conf, log)
	}
	// Add configured event writers.
	for _, e := range conf.EventWriters {
		builder.Add(ctx, e, conf, log)
	}
	// Get the built writer.
	writer, err := builder.Writer()
	if err != nil {
		return nil, fmt.Errorf("creating event writers: %v", err)
	}

	// Wrap the event writers in a couple filters.
	writer = &events.SystemLogFilter{Writer: writer, Level: conf.Logger.Level}
	writer = &events.ErrLogger{Writer: writer, Log: log}

	// Get the task source reader: database, file, etc.
	reader, err := newTaskReader(ctx, conf, opts)
	if err != nil {
		return nil, fmt.Errorf("creating task reader: %v", err)
	}

	// Initialize task storage client.
	store, err := storage.NewMux(conf)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate Storage backend: %v", err)
	}
	store.AttachLogger(log)

	// The executor always defaults to docker, unless explicitly set to kubernetes.
	var executor = worker.Executor{
		Backend: "docker",
	}

	if conf.Compute == "kubernetes" {
		executor.Backend = "kubernetes"
		executor.Template = conf.Kubernetes.ExecutorTemplate
		executor.Namespace = conf.Kubernetes.Namespace
		executor.JobsNamespace = conf.Kubernetes.JobsNamespace
		executor.ServiceAccount = conf.Kubernetes.ServiceAccount
		executor.Resources = conf.Kubernetes.Resources
	}

	return &worker.DefaultWorker{
		Executor:    executor,
		Conf:        conf.Worker,
		Store:       store,
		TaskReader:  reader,
		EventWriter: writer,
	}, nil
}

// newTaskReader finds a TaskReader implementation that matches the config
// and commandline options.
func newTaskReader(ctx context.Context, conf *config.Config, opts *Options) (worker.TaskReader, error) {

	switch {
	// These readers are used to read a local task from a file, cli arg, etc.
	case opts.TaskFile != "":
		return worker.NewFileTaskReader(opts.TaskFile)

	case opts.TaskBase64 != "":
		return worker.NewBase64TaskReader(opts.TaskBase64)
	}

	switch strings.ToLower(conf.Database) {
	// These readers will connect to the configured task database.
	case "datastore":
		db, err := datastore.NewDatastore(conf.Datastore)
		return newDatabaseTaskReader(opts.TaskID, db, err)

	case "dynamodb":
		db, err := dynamodb.NewDynamoDB(conf.DynamoDB)
		return newDatabaseTaskReader(opts.TaskID, db, err)

	case "elastic":
		db, err := elastic.NewElastic(conf.Elastic)
		return newDatabaseTaskReader(opts.TaskID, db, err)

	case "mongodb":
		db, err := mongodb.NewMongoDB(conf.MongoDB)
		return newDatabaseTaskReader(opts.TaskID, db, err)

	case "postgres":
		db, err := postgres.NewPostgres(conf.Postgres)
		return newDatabaseTaskReader(opts.TaskID, db, err)

	// These readers connect via RPC (because the database is embedded in the server).
	// case "boltdb", "badger":
	// Default to asking the server for the task.

	default:
		return worker.NewRPCTaskReader(ctx, conf.RPCClient, opts.TaskID)
	}
}

// newDatabaseTaskReader helps create a generic task reader wrapper
// for the given database backend.
func newDatabaseTaskReader(taskID string, db tes.ReadOnlyServer, err error) (worker.TaskReader, error) {
	if err != nil {
		return nil, fmt.Errorf("creating database task reader: %v", err)
	}
	return worker.NewGenericTaskReader(db.GetTask, taskID, db.Close), nil
}

// eventWriterBuilder is a helper for building a set of event writers,
// collecting errors, de-duplicating config, etc.
type eventWriterBuilder struct {
	errors  util.MultiError
	writers events.MultiWriter
	// seen tracks which event writers have already been built,
	// so we don't build the same one twice.
	seen map[string]bool
}

// Writers gets all the event writers and errors collected by multiple calls to Add().
func (e *eventWriterBuilder) Writer() (events.Writer, error) {
	return &e.writers, e.errors.ToError()
}

// Add creates a new event writer by name and adds it to the builder.
func (e *eventWriterBuilder) Add(ctx context.Context, name string, conf *config.Config, log *logger.Logger) {
	if name == "" {
		return
	}

	if e.seen == nil {
		e.seen = map[string]bool{}
	}

	// If we've already created this event writer "name", skip it.
	if _, ok := e.seen[name]; ok {
		return
	}
	e.seen[name] = true

	var err error
	var writer events.Writer

	switch name {
	case "log":
		writer = &events.Logger{Log: log}
	case "boltdb", "badger", "grpc", "rpc":
		writer, err = events.NewRPCWriter(ctx, conf.RPCClient)
	case "dynamodb":
		writer, err = dynamodb.NewDynamoDB(conf.DynamoDB)
	case "datastore":
		writer, err = datastore.NewDatastore(conf.Datastore)
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
		err = fmt.Errorf("unknown event writer: %s", name)
	}

	if err != nil {
		e.errors = append(e.errors, err)
	} else {
		e.writers = append(e.writers, writer)
	}
}

func validateConfig(conf *config.Config, opts *Options) error {
	// If the task reader is a file or string,
	// only a subset of event writers are supported.
	if opts.TaskFile != "" || opts.TaskBase64 != "" {
		for _, e := range conf.EventWriters {
			if e != "log" && e != "kafka" && e != "pubsub" {
				return fmt.Errorf("event writer %q is not supported with a task file/string reader", e)
			}
		}
	}
	return nil
}
