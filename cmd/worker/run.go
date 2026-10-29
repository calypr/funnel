package worker

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/calypr/funnel/compute/kubernetes/storageplan"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/database/datastore"
	"github.com/calypr/funnel/database/dynamodb"
	"github.com/calypr/funnel/database/mongodb"
	"github.com/calypr/funnel/database/postgres"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/storage"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/util"
	"github.com/calypr/funnel/worker"
	googleproto "google.golang.org/protobuf/proto"
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
	log.Debug("NewWorker", "config", conf.Safe())

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
	var executionClaimer worker.ExecutionClaimer
	if opts.TaskFile == "" && opts.TaskBase64 == "" {
		var ok bool
		executionClaimer, ok = reader.(worker.ExecutionClaimer)
		if !ok {
			reader.Close()
			writer.Close()
			return nil, fmt.Errorf("task reader %T does not support execution claims", reader)
		}
	}

	// The executor always defaults to docker, unless explicitly set to kubernetes.
	var readMounts []storage.S3ReadMount
	var writeMounts []storage.S3WriteMount
	var executor = worker.Executor{
		Backend:        "docker",
		ComputeBackend: strings.ToLower(strings.TrimSpace(conf.Compute)),
	}

	if compute := strings.ToLower(strings.TrimSpace(conf.Compute)); compute == "kubernetes" || compute == "k8s" {
		executor.Backend = "kubernetes"
		owner := &events.ExecutionOwner{
			Namespace: os.Getenv(events.OwnerNamespaceEnv),
			PodName:   os.Getenv(events.OwnerNameEnv),
			PodUid:    os.Getenv(events.OwnerUIDEnv),
		}
		if owner.Namespace != "" || owner.PodName != "" || owner.PodUid != "" {
			if _, err := events.ExecutionOwnerMetadata(owner); err != nil {
				return nil, fmt.Errorf("reading Kubernetes worker identity: %w", err)
			}
			executor.ExecutionOwner = owner
		}
		executor.Template = conf.Kubernetes.ExecutorTemplate
		executor.Namespace = conf.Kubernetes.Namespace
		executor.JobsNamespace = conf.Kubernetes.JobsNamespace
		executor.ServiceAccount = conf.Kubernetes.ServiceAccount
		executor.Resources = conf.Kubernetes.Resources
		executor.NodeSelector = conf.Kubernetes.NodeSelector
		executor.Tolerations = convertK8sTolerations(conf.Kubernetes.Tolerations)
		if snapshot := os.Getenv(storageplan.Env); snapshot != "" {
			plan, err := storageplan.Decode(snapshot)
			if err != nil {
				return nil, fmt.Errorf("reading submitted storage plan: %w", err)
			}
			conf = googleproto.Clone(conf).(*config.Config)
			conf.Worker.WorkDir = plan.WorkDir
			readMounts = plan.S3ReadMounts
			writeMounts = plan.S3WriteMounts
			executor.StoragePlan = plan
			executor.JobsNamespace = plan.Namespace
		} else if conf.Kubernetes.Storage != nil {
			return nil, fmt.Errorf("explicit Kubernetes storage requires a submitted storage plan")
		}

	}

	if executor.StoragePlan == nil {
		for _, backend := range conf.GenericS3 {
			if backend != nil && (backend.MountedRead != nil || backend.MountedWrite != nil) {
				return nil, fmt.Errorf("GenericS3 mounted access requires a submitted Kubernetes storage plan")
			}
		}
	}
	mux, err := storage.NewWorkerMux(conf, readMounts, writeMounts...)
	if err != nil {
		return nil, fmt.Errorf("creating worker storage backend: %w", err)
	}
	mux.AttachLogger(log)

	return &worker.DefaultWorker{
		Executor:         executor,
		Conf:             conf.Worker,
		Store:            mux,
		TaskReader:       reader,
		ExecutionClaimer: executionClaimer,
		EventWriter:      writer,
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
		return newDatabaseTaskReader(opts.TaskID, "datastore", db, err)

	case "dynamodb":
		db, err := dynamodb.NewDynamoDB(conf.DynamoDB)
		return newDatabaseTaskReader(opts.TaskID, "dynamodb", db, err)

	case "mongodb":
		db, err := mongodb.NewMongoDB(conf.MongoDB)
		return newDatabaseTaskReader(opts.TaskID, "mongodb", db, err)

	case "postgres":
		db, err := postgres.NewPostgres(conf.Postgres)
		return newDatabaseTaskReader(opts.TaskID, "postgres", db, err)

	// These databases are embedded in the server, so workers use RPC.
	case "", "boltdb", "badger", "grpc", "rpc":
		return worker.NewRPCTaskReader(ctx, conf.RPCClient, opts.TaskID)

	default:
		return nil, fmt.Errorf("unknown database: '%s'", conf.Database)
	}
}

// newDatabaseTaskReader helps create a generic task reader wrapper
// for the given database backend.
func newDatabaseTaskReader(taskID, database string, db tes.ReadOnlyServer, err error) (worker.TaskReader, error) {
	if err != nil {
		return nil, fmt.Errorf("creating database task reader: %v", err)
	}
	reader := worker.NewGenericTaskReader(db.GetTask, taskID, db.Close)
	claimer, _ := db.(worker.ExecutionClaimer)
	return &databaseTaskReader{GenericTaskReader: reader, database: database, claimer: claimer}, nil
}

type databaseTaskReader struct {
	*worker.GenericTaskReader
	database string
	claimer  worker.ExecutionClaimer
}

func (r *databaseTaskReader) ClaimExecution(ctx context.Context, taskID string, owner *events.ExecutionOwner) (bool, error) {
	if r.claimer == nil {
		return false, fmt.Errorf("database %q does not support execution claims", r.database)
	}
	return r.claimer.ClaimExecution(ctx, taskID, owner)
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

	switch strings.ToLower(name) {
	case "log":
		writer = &events.Logger{Log: log}
	case "boltdb", "badger", "grpc", "rpc":
		writer, err = events.NewRPCWriter(ctx, conf.RPCClient)
	case "dynamodb":
		writer, err = dynamodb.NewDynamoDB(conf.DynamoDB)
	case "datastore":
		writer, err = datastore.NewDatastore(conf.Datastore)
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
			if strings.ToLower(e) != "log" && strings.ToLower(e) != "kafka" && strings.ToLower(e) != "pubsub" {
				return fmt.Errorf("event writer %q is not supported with a task file/string reader", e)
			}
		}
	}
	return nil
}

func convertK8sTolerations(in []*config.Toleration) []map[string]interface{} {
	if len(in) == 0 {
		return nil
	}

	out := make([]map[string]interface{}, 0, len(in))
	for _, t := range in {
		if t == nil {
			continue
		}

		m := map[string]interface{}{
			"Key":      t.Key,
			"Operator": t.Operator,
			"Effect":   t.Effect,
		}
		if t.Value != "" {
			m["Value"] = t.Value
		}
		if t.TolerationSeconds != nil {
			m["TolerationSeconds"] = *t.TolerationSeconds
		}
		out = append(out, m)
	}

	if len(out) == 0 {
		return nil
	}
	return out
}
