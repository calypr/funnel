package util

import (
	"strings"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/spf13/pflag"
)

// ServerFlags returns a new flag set for configuring a Funnel server
func ServerFlags(flagConf *config.Config, configFile *string) *pflag.FlagSet {
	f := pflag.NewFlagSet("", pflag.ContinueOnError)

	f.StringVarP(configFile, "config", "c", *configFile, "Config File")

	f.AddFlagSet(selectorFlags(flagConf))
	f.AddFlagSet(serverFlags(flagConf))
	f.AddFlagSet(rpcClientFlags(flagConf))
	f.AddFlagSet(workerFlags(flagConf))
	f.AddFlagSet(nodeFlags(flagConf))
	f.AddFlagSet(dbFlags(flagConf))
	f.AddFlagSet(storageFlags(flagConf))
	f.AddFlagSet(computeFlags(flagConf))
	f.AddFlagSet(loggerFlags(flagConf))

	return f
}

// WorkerFlags returns a new flag set for configuring a Funnel worker
func WorkerFlags(flagConf *config.Config, configFile *string) *pflag.FlagSet {
	f := pflag.NewFlagSet("", pflag.ContinueOnError)

	f.StringVarP(configFile, "config", "c", *configFile, "Config File")

	f.AddFlagSet(selectorFlags(flagConf))
	f.AddFlagSet(serverFlags(flagConf))
	f.AddFlagSet(rpcClientFlags(flagConf))
	f.AddFlagSet(workerFlags(flagConf))
	f.AddFlagSet(nodeFlags(flagConf))
	f.AddFlagSet(dbFlags(flagConf))
	f.AddFlagSet(storageFlags(flagConf))
	f.AddFlagSet(loggerFlags(flagConf))

	return f
}

// NodeFlags returns a new flag set for configuring a Funnel node
func NodeFlags(flagConf *config.Config, configFile *string) *pflag.FlagSet {
	f := pflag.NewFlagSet("", pflag.ContinueOnError)
	f.StringVarP(configFile, "config", "c", *configFile, "Config File")
	f.AddFlagSet(selectorFlags(flagConf))
	f.AddFlagSet(serverFlags(flagConf))
	f.AddFlagSet(rpcClientFlags(flagConf))
	f.AddFlagSet(workerFlags(flagConf))
	f.AddFlagSet(nodeFlags(flagConf))
	f.AddFlagSet(dbFlags(flagConf))
	f.AddFlagSet(storageFlags(flagConf))
	f.AddFlagSet(loggerFlags(flagConf))
	return f
}

func selectorFlags(flagConf *config.Config) *pflag.FlagSet {
	//fmt.Println("COMPUTE: ", flagConf.RPCClient.Timeout.GetDuration())

	f := pflag.NewFlagSet("", pflag.ContinueOnError)
	f.StringVar(&flagConf.Compute, "Compute", flagConf.Compute, "Name of compute backed to use")
	f.StringVar(&flagConf.Database, "Database", flagConf.Database, "Name of database backed to use")
	f.StringSliceVar(&flagConf.EventWriters, "EventWriters", flagConf.EventWriters, "Name of an event writer backend to use. This flag can be used multiple times")

	return f
}

func rpcClientFlags(flagConf *config.Config) *pflag.FlagSet {
	f := pflag.NewFlagSet("", pflag.ContinueOnError)

	f.StringVar(&flagConf.RPCClient.ServerAddress, "RPCClient.ServerAddress", flagConf.RPCClient.ServerAddress, "RPC server address")
	f.Var(&TimeoutConfigValue{&flagConf.RPCClient.Timeout}, "RPCClient.Timeout", "Request timeout for RPC client connections")
	f.Uint32Var(&flagConf.RPCClient.MaxRetries, "RPCClient.MaxRetries", flagConf.RPCClient.MaxRetries, "Maximum number of times that a request will be retried for failures")

	return f
}

func serverFlags(flagConf *config.Config) *pflag.FlagSet {
	f := pflag.NewFlagSet("", pflag.ContinueOnError)

	f.StringVar(&flagConf.Server.HostName, "Server.HostName", flagConf.Server.HostName, "Host name or IP")
	f.StringVar(&flagConf.Server.HTTPPort, "Server.HTTPPort", flagConf.Server.HTTPPort, "HTTP Port")
	f.StringVar(&flagConf.Server.RPCPort, "Server.RPCPort", flagConf.Server.RPCPort, "RPC Port")
	f.StringVar(&flagConf.Server.ServiceName, "Server.ServiceName", flagConf.Server.ServiceName, "Host name or IP")

	return f
}

func workerFlags(flagConf *config.Config) *pflag.FlagSet {
	f := pflag.NewFlagSet("", pflag.ContinueOnError)

	f.Int64Var(&flagConf.Worker.LogTailSize, "Worker.LogTailSize", flagConf.Worker.LogTailSize, "Max bytes to store for stdout/stderr")

	f.Var(&DurationValue{D: &flagConf.Worker.LogUpdateRate}, "Worker.LogUpdateRate", "How often to send stdout/stderr log updates")
	f.Var(&DurationValue{D: &flagConf.Worker.PollingRate}, "Worker.PollingRate", "How often to poll for cancel signals")
	f.StringVar(&flagConf.Worker.WorkDir, "Worker.WorkDir", flagConf.Worker.WorkDir, "Working directory")
	f.StringVar(&flagConf.Worker.ScratchPath, "Worker.ScratchPath", flagConf.Worker.ScratchPath, "Scratch directory")
	f.BoolVar(&flagConf.Worker.LeaveWorkDir, "Worker.LeaveWorkDir", flagConf.Worker.LeaveWorkDir, "Leave working directory after execution")
	f.StringVar(&flagConf.Worker.DriverCommand, "Worker.DriverCommand", flagConf.Worker.DriverCommand, "Overrides the default command used to run containers.")

	return f
}

func nodeFlags(flagConf *config.Config) *pflag.FlagSet {
	f := pflag.NewFlagSet("", pflag.ContinueOnError)

	f.Uint32Var(&flagConf.Node.Resources.Cpus, "Node.Resources.Cpus", flagConf.Node.Resources.Cpus, "Cpus available to Node")
	f.Float64Var(&flagConf.Node.Resources.RamGb, "Node.Resources.RamGb", flagConf.Node.Resources.RamGb, "Ram (GB) available to Node")
	f.Float64Var(&flagConf.Node.Resources.DiskGb, "Node.Resources.DiskGb", flagConf.Node.Resources.DiskGb, "Free disk (GB) available to Node")
	f.Var(&TimeoutConfigValue{&flagConf.Node.Timeout}, "Node.Timeout", "Node timeout in seconds")
	f.Var(&DurationValue{D: &flagConf.Node.UpdateRate}, "Node.UpdateRate", "Node update rate")
	// TODO Metadata

	return f
}

func loggerFlags(flagConf *config.Config) *pflag.FlagSet {
	f := pflag.NewFlagSet("", pflag.ContinueOnError)

	f.StringVar(&flagConf.Logger.Level, "Logger.Level", flagConf.Logger.Level, "Level of logging")
	f.StringVar(&flagConf.Logger.OutputFile, "Logger.OutputFile", flagConf.Logger.OutputFile, "File path to write logs to")
	f.StringVar(&flagConf.Logger.Formatter, "Logger.Formatter", flagConf.Logger.Formatter, "Logs formatter. One of ['text', 'json']")

	return f
}

func dbFlags(flagConf *config.Config) *pflag.FlagSet {
	f := pflag.NewFlagSet("", pflag.ContinueOnError)

	// boltdb
	f.StringVar(&flagConf.BoltDB.Path, "BoltDB.Path", flagConf.BoltDB.Path, "Path to BoltDB database")

	// dynamodb
	f.StringVar(&flagConf.DynamoDB.AWSConfig.Region, "DynamoDB.Region", flagConf.DynamoDB.AWSConfig.Region, "AWS region of DynamoDB tables")
	f.StringVar(&flagConf.DynamoDB.TableBasename, "DynamoDB.TableBasename", flagConf.DynamoDB.TableBasename, "Basename of DynamoDB tables")
	f.Int32Var(&flagConf.DynamoDB.AWSConfig.MaxRetries, "DynamoDB.MaxRetries", flagConf.DynamoDB.AWSConfig.MaxRetries, "Maximum number of times that a request will be retried for failures")

	// datastore
	f.StringVar(&flagConf.Datastore.Project, "Datastore.Project", flagConf.Datastore.Project, "Google project for Datastore")

	// elastic
	f.StringVar(&flagConf.Elastic.IndexPrefix, "Elastic.IndexPrefix", flagConf.Elastic.IndexPrefix, "Prefix to use for Elasticsearch indices")
	f.StringVar(&flagConf.Elastic.URL, "Elastic.URL", flagConf.Elastic.URL, "Elasticsearch URL")

	// kafka
	f.StringSliceVar(&flagConf.Kafka.Servers, "Kafka.Servers", flagConf.Kafka.Servers, "Address of a Kafka server. This flag can be used multiple times")
	f.StringVar(&flagConf.Kafka.Topic, "Kafka.Topic", flagConf.Kafka.Topic, "Kafka topic to write events to")

	// mongodb
	f.StringSliceVar(&flagConf.MongoDB.Addrs, "MongoDB.Addrs", flagConf.MongoDB.Addrs, "Address of a MongoDB seed server. This flag can be used multiple times")
	f.StringVar(&flagConf.MongoDB.Database, "MongoDB.Database", flagConf.MongoDB.Database, "Database name in MongoDB")
	f.Var(&TimeoutConfigValue{&flagConf.MongoDB.Timeout}, "MongoDB.Timeout", "Timeout in seconds for initial connection and follow up operations")

	// postgres
	f.StringVar(&flagConf.Postgres.Host, "Postgres.Host", flagConf.Postgres.Host, "Address of Postgres server")
	f.StringVar(&flagConf.Postgres.Database, "Postgres.Database", flagConf.Postgres.Database, "Database name in Postgres")
	f.StringVar(&flagConf.Postgres.User, "Postgres.User", flagConf.Postgres.User, "User name for Postgres authentication")
	f.StringVar(&flagConf.Postgres.Password, "Postgres.Password", flagConf.Postgres.Password, "Password for Postgres authentication")

	return f
}

func storageFlags(flagConf *config.Config) *pflag.FlagSet {
	f := pflag.NewFlagSet("", pflag.ContinueOnError)

	// local storage
	f.BoolVar(&flagConf.LocalStorage.Disabled, "LocalStorage.Disabled", flagConf.LocalStorage.Disabled, "Disable storage backend")
	f.StringSliceVar(&flagConf.LocalStorage.AllowedDirs, "LocalStorage.AllowedDirs", flagConf.LocalStorage.AllowedDirs, "Directories Funnel is allowed to access. This flag can be used multiple times")

	// amazon s3
	f.BoolVar(&flagConf.AmazonS3.Disabled, "AmazonS3.Disabled", flagConf.AmazonS3.Disabled, "Disable storage backend")
	f.Int32Var(&flagConf.AmazonS3.AWSConfig.MaxRetries, "AmazonS3.MaxRetries", flagConf.AmazonS3.AWSConfig.MaxRetries, "Maximum number of times that a request will be retried for failures")

	// google storage
	f.BoolVar(&flagConf.GoogleStorage.Disabled, "GoogleStorage.Disabled", flagConf.GoogleStorage.Disabled, "Disable storage backend")

	// swift
	f.BoolVar(&flagConf.Swift.Disabled, "Swift.Disabled", flagConf.Swift.Disabled, "Disable storage backend")
	f.Int64Var(&flagConf.Swift.ChunkSizeBytes, "Swift.ChunkSizeBytes", flagConf.Swift.ChunkSizeBytes, "Size of chunks to use for large object creation")
	f.Int32Var(&flagConf.Swift.MaxRetries, "Swift.MaxRetries", flagConf.Swift.MaxRetries, "Maximum number of times that a request will be retried for failures")

	// HTTP storage
	f.BoolVar(&flagConf.HTTPStorage.Disabled, "HTTPStorage.Disabled", flagConf.HTTPStorage.Disabled, "Disable storage backend")
	f.Var(&TimeoutConfigValue{&flagConf.HTTPStorage.Timeout}, "HTTPStorage.Timeout", "Timeout in seconds for request")

	// FTP storage
	f.BoolVar(&flagConf.FTPStorage.Disabled, "FTPStorage.Disabled", flagConf.FTPStorage.Disabled, "Disable storage backend")
	f.Var(&TimeoutConfigValue{&flagConf.FTPStorage.Timeout}, "FTPStorage.Timeout", "Timeout in seconds for request")

	return f
}

func computeFlags(flagConf *config.Config) *pflag.FlagSet {
	f := pflag.NewFlagSet("", pflag.ContinueOnError)

	// AWS Batch
	f.StringVar(&flagConf.AWSBatch.AWSConfig.Region, "AWSBatch.Region", flagConf.AWSBatch.AWSConfig.Region, "AWS region of Batch resources")
	f.StringVar(&flagConf.AWSBatch.JobDefinition, "AWSBatch.JobDefinition", flagConf.AWSBatch.JobDefinition, "AWS Batch job definition name or ARN")
	f.StringVar(&flagConf.AWSBatch.JobQueue, "AWSBatch.JobQueue", flagConf.AWSBatch.JobQueue, "AWS Batch job queue name or ARN")
	f.Int32Var(&flagConf.AWSBatch.AWSConfig.MaxRetries, "AWSBatch.MaxRetries", flagConf.AWSBatch.AWSConfig.MaxRetries, "Maximum number of times that a request will be retried for failures")
	f.BoolVar(&flagConf.AWSBatch.DisableReconciler, "AWSBatch.DisableReconciler", flagConf.AWSBatch.DisableReconciler, "Disable the state reconciler")
	f.Var(&DurationValue{&flagConf.AWSBatch.ReconcileRate}, "AWSBatch.ReconcileRate", "How often to run the reconciler")

	// GCP Batch
	f.StringVar(&flagConf.GCPBatch.Project, "GCPBatch.Project", flagConf.GCPBatch.Project, "GCP Project ID")
	f.StringVar(&flagConf.GCPBatch.Location, "GCPBatch.Location", flagConf.GCPBatch.Location, "GCP Location for Batch jobs")
	f.BoolVar(&flagConf.GCPBatch.DisableReconciler, "GCPBatch.DisableReconciler", flagConf.GCPBatch.DisableReconciler, "Disable the state reconciler")
	f.Var(&DurationValue{&flagConf.GCPBatch.ReconcileRate}, "GCPBatch.ReconcileRate", "How often to run the reconciler")

	// GridEngine
	f.StringVar(&flagConf.GridEngine.TemplateFile, "GridEngine.TemplateFile", flagConf.GridEngine.TemplateFile, "Path to template submit file")

	// HTCondor
	f.StringVar(&flagConf.HTCondor.TemplateFile, "HTCondor.TemplateFile", flagConf.HTCondor.TemplateFile, "Path to template submit file")
	f.BoolVar(&flagConf.HTCondor.DisableReconciler, "HTCondor.DisableReconciler", flagConf.HTCondor.DisableReconciler, "Disable the state reconciler")
	f.Var(&DurationValue{&flagConf.HTCondor.ReconcileRate}, "HTCondor.ReconcileRate", "How often to run the reconciler")

	// PBS/Torque
	f.StringVar(&flagConf.PBS.TemplateFile, "PBS.TemplateFile", flagConf.PBS.TemplateFile, "Path to template submit file")
	f.BoolVar(&flagConf.PBS.DisableReconciler, "PBS.DisableReconciler", flagConf.PBS.DisableReconciler, "Disable the state reconciler")
	f.Var(&DurationValue{&flagConf.PBS.ReconcileRate}, "PBS.ReconcileRate", "How often to run the reconciler")

	// Slurm
	f.StringVar(&flagConf.Slurm.TemplateFile, "Slurm.TemplateFile", flagConf.Slurm.TemplateFile, "Path to template submit file")
	f.BoolVar(&flagConf.Slurm.DisableReconciler, "Slurm.DisableReconciler", flagConf.Slurm.DisableReconciler, "Disable the state reconciler")
	f.Var(&DurationValue{&flagConf.Slurm.ReconcileRate}, "Slurm.ReconcileRate", "How often to run the reconciler")

	// Scheduler
	f.Var(&DurationValue{&flagConf.Scheduler.ScheduleRate}, "Scheduler.ScheduleRate", "How often to run a scheduler iteration")
	f.Int32Var(&flagConf.Scheduler.ScheduleChunk, "Scheduler.ScheduleChunk", flagConf.Scheduler.ScheduleChunk, "How many tasks to schedule in one iteration")
	f.Var(&TimeoutConfigValue{&flagConf.Scheduler.NodePingTimeout}, "Scheduler.NodePingTimeout", "How long to wait for a node ping before marking it as dead")
	f.Var(&TimeoutConfigValue{&flagConf.Scheduler.NodeInitTimeout}, "Scheduler.NodeInitTimeout", "How long to wait for node initialization before marking it dead")
	f.Var(&TimeoutConfigValue{&flagConf.Scheduler.NodeDeadTimeout}, "Scheduler.NodeDeadTimeout", "How long to wait before deleting a dead node from the DB")

	// Kubernetes
	f.StringVar(&flagConf.Kubernetes.Executor, "Kubernetes.Executor", flagConf.Kubernetes.Executor, "Executor to use for executing tasks (docker or kubernetes)")
	f.StringVar(&flagConf.Kubernetes.Namespace, "Kubernetes.Namespace", flagConf.Kubernetes.Namespace, "Namespace to spawn jobs within")
	f.BoolVar(&flagConf.Kubernetes.DisableReconciler, "Kubernetes.DisableReconciler", flagConf.Kubernetes.DisableReconciler, "Disable the state reconciler")
	f.Var(&DurationValue{&flagConf.Kubernetes.ReconcileRate}, "Kubernetes.ReconcileRate", "How often to run the reconciler")

	return f
}

func normalize(name string) string {
	from := []string{"-", "_"}
	to := "."
	for _, sep := range from {
		name = strings.Replace(name, sep, to, -1)
	}
	return strings.ToLower(name)
}

// NormalizeFlags allows for flags to be case and separator insensitive.
// Use it by passing it to cobra.Command.SetGlobalNormalizationFunc
func NormalizeFlags(f *pflag.FlagSet, name string) pflag.NormalizedName {
	lookup := map[string]string{"help": "help", normalize(name): name}

	f.VisitAll(func(f *pflag.Flag) {
		lookup[normalize(f.Name)] = f.Name
	})

	return pflag.NormalizedName(lookup[normalize(name)])
}
