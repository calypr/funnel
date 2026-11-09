package config

import (
	"os"
	"path"
	"time"

	"github.com/calypr/funnel/api/logger"

	durationpb "google.golang.org/protobuf/types/known/durationpb"

	schema "github.com/calypr/funnel/api/config"
	logging "github.com/calypr/funnel/internal/logger"
)

// DefaultConfig returns configuration with simple defaults.
func DefaultConfig() *schema.Config {
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	workDir := path.Join(cwd, "funnel-work-dir")

	allowedDirs := []string{cwd}
	if os.Getenv("HOME") != "" {
		allowedDirs = append(allowedDirs, os.Getenv("HOME"))
	}
	if os.Getenv("TMPDIR") != "" {
		allowedDirs = append(allowedDirs, os.Getenv("TMPDIR"))
	}

	server := &schema.Server{
		HostName:         "localhost",
		HTTPPort:         "8000",
		RPCPort:          "9090",
		ServiceName:      "Funnel",
		DisableHTTPCache: true,
		TaskAccess:       "All",
	}

	c := &schema.Config{
		Compute:      "local",
		Database:     "boltdb",
		EventWriters: []string{"log"},
		// funnel components
		Server: server,
		RPCClient: &schema.RPCClient{
			ServerAddress: RPCAddress(server),
			Timeout: &schema.TimeoutConfig{
				TimeoutOption: &schema.TimeoutConfig_Duration{
					Duration: durationpb.New(time.Second * 60),
				},
			},
			MaxRetries: 10,
			Credential: &schema.BasicCredential{},
		},
		Scheduler: &schema.Scheduler{
			ScheduleRate:  durationpb.New(time.Second),
			ScheduleChunk: 10,
			NodePingTimeout: &schema.TimeoutConfig{
				TimeoutOption: &schema.TimeoutConfig_Duration{
					Duration: durationpb.New(time.Minute),
				},
			},
			NodeInitTimeout: &schema.TimeoutConfig{
				TimeoutOption: &schema.TimeoutConfig_Duration{
					Duration: durationpb.New(time.Minute * 5),
				},
			},
			NodeDeadTimeout: &schema.TimeoutConfig{
				TimeoutOption: &schema.TimeoutConfig_Duration{
					Duration: durationpb.New(time.Minute * 5),
				},
			},
		},
		Node: &schema.Node{
			Timeout: &schema.TimeoutConfig{
				TimeoutOption: &schema.TimeoutConfig_Disabled{
					Disabled: true,
				},
			},
			UpdateRate: durationpb.New(time.Second * 5),
			Metadata:   map[string]string{},
			Resources:  &schema.Resources{},
		},
		Worker: &schema.Worker{
			WorkDir:              workDir,
			PollingRate:          durationpb.New(time.Second * 5),
			LogUpdateRate:        durationpb.New(time.Second * 5),
			LogTailSize:          10000,
			MaxParallelTransfers: 10,
			// `docker run` command flags
			// https://docs.docker.com/reference/cli/docker/container/run/
			Container: &schema.ContainerConfig{
				DriverCommand: "docker",
				RunCommand: "run -i --read-only " +
					"{{.GPUArgs}} " +
					"{{if and .EnforceResources .Resources}}{{if gt .Resources.CpuCores 0}}--cpus {{.Resources.CpuCores}}{{end}} {{if .MemoryMB}}--memory {{.MemoryMB}}m --memory-swap {{.MemoryMB}}m{{end}}{{end}} " +
					// Writable scratch space scoped to this executor container.
					"{{if .NeedsTmpfs}}--tmpfs /tmp{{end}} " +
					// Remove container after it exits
					"{{if .RemoveContainer}}--rm{{end}} " +

					// Environment variables
					"{{.GetEnvArgs}} " +

					// Tags/Labels
					"{{.GetTagArgs}} " +

					// Container Name
					"{{if .Name}}--name {{.Name}}{{end}} " +

					// Workdir
					"{{if .Workdir}}--workdir {{.Workdir}}{{end}} " +

					// Volumes
					"{{range .Volumes}}--volume {{.HostPath}}:{{.ContainerPath}}:{{if .Readonly}}ro{{else}}rw{{end}} {{end}} " +

					// Image and Command
					"{{.Image}} {{.Command}}",
				PullCommand: "pull {{.Image}}",
				StopCommand: "rm -f {{.Name}}",
			},
		},
		Plugins: nil,
		Logger:  logging.DefaultConfig(),
		// databases / event handlers
		BoltDB: &schema.BoltDB{
			Path: path.Join(workDir, "funnel.db"),
		},
		Badger: &schema.Badger{
			Path: path.Join(workDir, "funnel.badger.db"),
		},
		DynamoDB: &schema.DynamoDB{
			TableBasename: "funnel",
			AWSConfig:     &schema.AWSConfig{},
		},
		MongoDB: &schema.MongoDB{
			Addrs: []string{"localhost"},
			Timeout: &schema.TimeoutConfig{
				TimeoutOption: &schema.TimeoutConfig_Duration{
					Duration: durationpb.New(time.Minute * 5),
				},
			},
			Database: "funnel",
		},
		Postgres: &schema.Postgres{
			Host:          "localhost:5432",
			Database:      "funnel",
			User:          "funnel",
			Password:      "example",
			AdminUser:     "postgres",
			AdminPassword: "example",
			Timeout: &schema.TimeoutConfig{
				TimeoutOption: &schema.TimeoutConfig_Duration{
					Duration: durationpb.New(time.Second * 30),
				},
			},
		},
		// event writers
		Kafka: &schema.Kafka{
			Topic: "funnel",
		},
		// storage
		LocalStorage: &schema.LocalStorage{
			AllowedDirs: allowedDirs,
		},
		HTTPStorage: &schema.HTTPStorage{
			Timeout: &schema.TimeoutConfig{
				TimeoutOption: &schema.TimeoutConfig_Disabled{Disabled: true},
			},
		},
		FTPStorage: &schema.FTPStorage{
			Timeout: &schema.TimeoutConfig{
				TimeoutOption: &schema.TimeoutConfig_Duration{
					Duration: durationpb.New(time.Second * 10),
				},
			},
			User:     "anonymous",
			Password: "anonymous",
		},
		AmazonS3: &schema.AmazonS3Storage{
			SSE: &schema.SSE{},
			AWSConfig: &schema.AWSConfig{
				MaxRetries: 10,
			},
		},
		Swift: &schema.SwiftStorage{
			MaxRetries:     20,
			ChunkSizeBytes: int64(500 * 1000 * 1000),
		},
		HTCondor:   &schema.HPCBackend{},
		Slurm:      &schema.HPCBackend{},
		PBS:        &schema.HPCBackend{},
		GridEngine: &schema.GridEngine{},
		AWSBatch:   &schema.AWSBatch{AWSConfig: &schema.AWSConfig{}},
		GCPBatch:   &schema.GCPBatch{},
		Kubernetes: &schema.Kubernetes{
			ForbiddenPathPrefixes: []string{"/dev", "/proc", "/sys", "/run", "/var/run"},
		},
		GoogleStorage: &schema.GoogleCloudStorage{},
		PubSub:        &schema.PubSub{},
		Datastore:     &schema.Datastore{},
		GenericS3:     []*schema.GenericS3Storage{},
	}

	// compute
	reconcile := durationpb.New(time.Minute * 10)

	htcondorTemplate := embeddedText("templates/hpc/htcondor-template.txt")
	c.HTCondor.Template = htcondorTemplate
	c.HTCondor.ReconcileRate = reconcile
	c.HTCondor.DisableReconciler = true

	slurmTemplate := embeddedText("templates/hpc/slurm-template.txt")
	c.Slurm.Template = slurmTemplate
	c.Slurm.ReconcileRate = reconcile
	c.Slurm.DisableReconciler = true

	pbsTemplate := embeddedText("templates/hpc/pbs-template.txt")
	c.PBS.Template = pbsTemplate
	c.PBS.ReconcileRate = reconcile
	c.PBS.DisableReconciler = true

	geTemplate := embeddedText("templates/hpc/gridengine-template.txt")
	c.GridEngine.Template = geTemplate

	c.AWSBatch.JobDefinition = "funnel-job-def"
	c.AWSBatch.JobQueue = "funnel-job-queue"
	c.AWSBatch.ReconcileRate = reconcile
	c.AWSBatch.DisableReconciler = true

	c.GCPBatch.Project = "example-gcp-project"
	c.GCPBatch.Location = "us-central1"
	c.GCPBatch.ReconcileRate = reconcile
	c.GCPBatch.DisableReconciler = false

	// Kubernetes Configs moved to Helm Charts:
	// https://github.com/ohsu-comp-bio/helm-charts/tree/main/charts/funnel

	return c
}

// EmptyConfig allocates configuration sections without application defaults.
func EmptyConfig() *schema.Config {
	return &schema.Config{
		RPCClient:     &schema.RPCClient{Credential: &schema.BasicCredential{}, Timeout: &schema.TimeoutConfig{}},
		Scheduler:     &schema.Scheduler{ScheduleRate: &durationpb.Duration{}, NodePingTimeout: &schema.TimeoutConfig{}, NodeInitTimeout: &schema.TimeoutConfig{}, NodeDeadTimeout: &schema.TimeoutConfig{}},
		Node:          &schema.Node{Resources: &schema.Resources{}, Timeout: &schema.TimeoutConfig{}, Metadata: map[string]string{}},
		Worker:        &schema.Worker{Container: &schema.ContainerConfig{}, PollingRate: &durationpb.Duration{}, LogUpdateRate: &durationpb.Duration{}},
		Logger:        &logger.LoggerConfig{JsonFormat: &logger.JSONFormatConfig{}, TextFormat: &logger.TextFormatConfig{}},
		BoltDB:        &schema.BoltDB{},
		Badger:        &schema.Badger{},
		DynamoDB:      &schema.DynamoDB{AWSConfig: &schema.AWSConfig{}},
		MongoDB:       &schema.MongoDB{Timeout: &schema.TimeoutConfig{}},
		Postgres:      &schema.Postgres{Timeout: &schema.TimeoutConfig{}},
		Kafka:         &schema.Kafka{},
		LocalStorage:  &schema.LocalStorage{},
		HTTPStorage:   &schema.HTTPStorage{Timeout: &schema.TimeoutConfig{}},
		FTPStorage:    &schema.FTPStorage{Timeout: &schema.TimeoutConfig{}},
		AmazonS3:      &schema.AmazonS3Storage{SSE: &schema.SSE{}, AWSConfig: &schema.AWSConfig{}},
		Swift:         &schema.SwiftStorage{},
		HTCondor:      &schema.HPCBackend{ReconcileRate: &durationpb.Duration{}},
		Slurm:         &schema.HPCBackend{ReconcileRate: &durationpb.Duration{}},
		PBS:           &schema.HPCBackend{ReconcileRate: &durationpb.Duration{}},
		GridEngine:    &schema.GridEngine{},
		AWSBatch:      &schema.AWSBatch{AWSConfig: &schema.AWSConfig{}, ReconcileRate: &durationpb.Duration{}},
		GCPBatch:      &schema.GCPBatch{Project: "", Location: "", ReconcileRate: &durationpb.Duration{}},
		Kubernetes:    &schema.Kubernetes{ReconcileRate: &durationpb.Duration{}},
		GoogleStorage: &schema.GoogleCloudStorage{},
		PubSub:        &schema.PubSub{},
		Datastore:     &schema.Datastore{},
		GenericS3:     []*schema.GenericS3Storage{},
		Server:        &schema.Server{BasicAuth: []*schema.BasicCredential{}, OidcAuth: &schema.OidcAuth{}},
		EventWriters:  []string{},
	}
}
