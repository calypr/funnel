package config

import (
	"os"
	"path"
	"strings"
	"time"

	"github.com/alecthomas/units"
	intern "github.com/ohsu-comp-bio/funnel/config/internal"
	"github.com/ohsu-comp-bio/funnel/logger"
	durationpb "google.golang.org/protobuf/types/known/durationpb"
)

// DefaultConfig returns configuration with simple defaults.
func DefaultConfig() *Config {
	cwd, _ := os.Getwd()
	workDir := path.Join(cwd, "funnel-work-dir")

	allowedDirs := []string{cwd}
	if os.Getenv("HOME") != "" {
		allowedDirs = append(allowedDirs, os.Getenv("HOME"))
	}
	if os.Getenv("TMPDIR") != "" {
		allowedDirs = append(allowedDirs, os.Getenv("TMPDIR"))
	}

	server := &Server{
		HostName:         "localhost",
		HTTPPort:         "8000",
		RPCPort:          "9090",
		ServiceName:      "Funnel",
		DisableHTTPCache: true,
		TaskAccess:       "All",
	}

	c := &Config{
		Compute:      "local",
		Database:     "boltdb",
		EventWriters: []string{"log"},
		// funnel components
		Server: server,
		RPCClient: &RPCClient{
			ServerAddress: server.RPCAddress(),
			Timeout: &TimeoutConfig{
				TimeoutOption: &TimeoutConfig_Duration{
					Duration: durationpb.New(time.Second * 60),
				},
			},
			MaxRetries: 10,
			Credential: &BasicCredential{},
		},
		Scheduler: &Scheduler{
			ScheduleRate:  durationpb.New(time.Second),
			ScheduleChunk: 10,
			NodePingTimeout: &TimeoutConfig{
				TimeoutOption: &TimeoutConfig_Duration{
					Duration: durationpb.New(time.Minute),
				},
			},
			NodeInitTimeout: &TimeoutConfig{
				TimeoutOption: &TimeoutConfig_Duration{
					Duration: durationpb.New(time.Minute * 5),
				},
			},
			NodeDeadTimeout: &TimeoutConfig{
				TimeoutOption: &TimeoutConfig_Duration{
					Duration: durationpb.New(time.Minute * 5),
				},
			},
		},
		Node: &Node{
			Timeout: &TimeoutConfig{
				TimeoutOption: &TimeoutConfig_Disabled{
					Disabled: true,
				},
			},
			UpdateRate: durationpb.New(time.Second * 5),
			Metadata:   map[string]string{},
			Resources:  &Resources{},
		},
		Worker: &Worker{
			WorkDir:              workDir,
			PollingRate:          durationpb.New(time.Second * 5),
			LogUpdateRate:        durationpb.New(time.Second * 5),
			LogTailSize:          10000,
			MaxParallelTransfers: 10,
			// `docker run` command flags
			// https://docs.docker.com/reference/cli/docker/container/run/
			Container: &ContainerConfig{
				DriverCommand: "docker",
				RunCommand: "run -i --read-only " +
					// Remove container after it exits
					"{{if .RemoveContainer}}--rm{{end}} " +

					// Environment variables
					"{{.GetEnvArgs}} " +

					// Tags/Labels
					"{{range $k, $v := .Tags}}--label {{$k}}={{$v}} {{end}} " +

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
		Logger:  logger.DefaultConfig(),
		// databases / event handlers
		BoltDB: &BoltDB{
			Path: path.Join(workDir, "funnel.db"),
		},
		Badger: &Badger{
			Path: path.Join(workDir, "funnel.badger.db"),
		},
		DynamoDB: &DynamoDB{
			TableBasename: "funnel",
			AWSConfig:     &AWSConfig{},
		},
		Elastic: &Elastic{
			URL:         "http://localhost:9200",
			IndexPrefix: "funnel",
		},
		MongoDB: &MongoDB{
			Addrs: []string{"localhost"},
			Timeout: &TimeoutConfig{
				TimeoutOption: &TimeoutConfig_Duration{
					Duration: durationpb.New(time.Minute * 5),
				},
			},
			Database: "funnel",
		},
		Postgres: &Postgres{
			Host:          "localhost:5432",
			Database:      "funnel",
			User:          "funnel",
			Password:      "example",
			AdminUser:     "postgres",
			AdminPassword: "example",
			Timeout: &TimeoutConfig{
				TimeoutOption: &TimeoutConfig_Duration{
					Duration: durationpb.New(time.Second * 30),
				},
			},
		},
		// event writers
		Kafka: &Kafka{
			Topic: "funnel",
		},
		// storage
		LocalStorage: &LocalStorage{
			AllowedDirs: allowedDirs,
		},
		HTTPStorage: &HTTPStorage{
			Timeout: &TimeoutConfig{
				TimeoutOption: &TimeoutConfig_Duration{
					Duration: durationpb.New(time.Second * 60),
				},
			},
		},
		FTPStorage: &FTPStorage{
			Timeout: &TimeoutConfig{
				TimeoutOption: &TimeoutConfig_Duration{
					Duration: durationpb.New(time.Second * 10),
				},
			},
			User:     "anonymous",
			Password: "anonymous",
		},
		AmazonS3: &AmazonS3Storage{
			SSE: &SSE{},
			AWSConfig: &AWSConfig{
				MaxRetries: 10,
			},
		},
		Swift: &SwiftStorage{
			MaxRetries:     20,
			ChunkSizeBytes: int64(500 * units.MB),
		},
		HTCondor:      &HPCBackend{},
		Slurm:         &HPCBackend{},
		PBS:           &HPCBackend{},
		GridEngine:    &GridEngine{},
		AWSBatch:      &AWSBatch{AWSConfig: &AWSConfig{}},
		GCPBatch:      &GCPBatch{},
		Kubernetes:    &Kubernetes{},
		GoogleStorage: &GoogleCloudStorage{},
		PubSub:        &PubSub{},
		Datastore:     &Datastore{},
		GenericS3:     []*GenericS3Storage{},
	}

	// compute
	reconcile := durationpb.New(time.Minute * 10)

	htcondorTemplate := intern.MustAsset("config/htcondor-template.txt")
	c.HTCondor.Template = string(htcondorTemplate)
	c.HTCondor.ReconcileRate = reconcile
	c.HTCondor.DisableReconciler = true

	slurmTemplate := intern.MustAsset("config/slurm-template.txt")
	c.Slurm.Template = string(slurmTemplate)
	c.Slurm.ReconcileRate = reconcile
	c.Slurm.DisableReconciler = true

	pbsTemplate := intern.MustAsset("config/pbs-template.txt")
	c.PBS.Template = string(pbsTemplate)
	c.PBS.ReconcileRate = reconcile
	c.PBS.DisableReconciler = true

	geTemplate := intern.MustAsset("config/gridengine-template.txt")
	c.GridEngine.Template = string(geTemplate)

	c.AWSBatch.JobDefinition = "funnel-job-def"
	c.AWSBatch.JobQueue = "funnel-job-queue"
	c.AWSBatch.ReconcileRate = reconcile
	c.AWSBatch.DisableReconciler = true

	c.GCPBatch.Project = "example-gcp-project"
	c.GCPBatch.Location = "us-central1"
	c.GCPBatch.ReconcileRate = reconcile
	c.GCPBatch.DisableReconciler = true

	// Kubernetes Configs moved to Helm Charts:
	// https://github.com/ohsu-comp-bio/helm-charts/tree/main/charts/funnel

	return c
}

var examples = buildExamples()

func buildExamples() map[string]string {
	examples := map[string]string{}
	for _, n := range intern.AssetNames() {
		sn := path.Base(n)
		sn = strings.TrimSuffix(sn, path.Ext(sn))
		b := intern.MustAsset(n)
		examples[sn] = string(b)
	}
	return examples
}

// Examples returns a set of example configurations strings.
func Examples() map[string]string {
	return examples
}
