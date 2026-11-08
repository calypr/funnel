package config

import (
	"os"
	"path"
	"strings"
	"time"

	durationpb "google.golang.org/protobuf/types/known/durationpb"

	schema "github.com/calypr/funnel/config"
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
			ServerAddress: server.RPCAddress(),
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

	htcondorTemplate := MustAsset("config/htcondor-template.txt")
	c.HTCondor.Template = string(htcondorTemplate)
	c.HTCondor.ReconcileRate = reconcile
	c.HTCondor.DisableReconciler = true

	slurmTemplate := MustAsset("config/slurm-template.txt")
	c.Slurm.Template = string(slurmTemplate)
	c.Slurm.ReconcileRate = reconcile
	c.Slurm.DisableReconciler = true

	pbsTemplate := MustAsset("config/pbs-template.txt")
	c.PBS.Template = string(pbsTemplate)
	c.PBS.ReconcileRate = reconcile
	c.PBS.DisableReconciler = true

	geTemplate := MustAsset("config/gridengine-template.txt")
	c.GridEngine.Template = string(geTemplate)

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

var examples = buildExamples()

func buildExamples() map[string]string {
	examples := map[string]string{}
	for _, n := range AssetNames() {
		sn := path.Base(n)
		sn = strings.TrimSuffix(sn, path.Ext(sn))
		b := MustAsset(n)
		examples[sn] = string(b)
	}
	return examples
}

// Examples returns a set of example configurations strings.
func Examples() map[string]string {
	return examples
}
