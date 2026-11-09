package testenv

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"path"
	"strconv"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/calypr/funnel/api/config"
	logconfig "github.com/calypr/funnel/api/logger"
	appconfig "github.com/calypr/funnel/internal/config"
	"github.com/calypr/funnel/internal/logger"
)

var configFile string

func ParseConfig() {
	if !flag.Parsed() {
		flag.Parse()
	}
}

// DefaultConfig returns a default configuration useful for testing,
// including temp storage dirs, random ports, S3 + minio config, etc.
func DefaultConfig(scope CleanupScope) *config.Config {
	conf := appconfig.DefaultConfig()
	conf.AmazonS3.Disabled = true
	conf.GoogleStorage.Disabled = true
	conf.Swift.Disabled = true
	conf.FTPStorage.Disabled = true

	// Get config from test command line flag, if present.
	if configFile != "" {
		err := appconfig.ParseFile(configFile, conf)
		if err != nil {
			panic(err)
		}
	}

	return TestifyConfig(scope, conf)
}

// TestifyConfig modifies a ports, directory paths, etc. to avoid
// conflicts between tests.
func TestifyConfig(scope CleanupScope, conf *config.Config) *config.Config {
	conf = TempDirConfig(scope, conf)
	conf = RandomPortConfig(conf)

	conf.Scheduler.ScheduleRate = durationpb.New(time.Millisecond * 500)
	conf.Node.UpdateRate = durationpb.New(time.Millisecond * 1300)
	conf.Worker.LogUpdateRate = durationpb.New(time.Millisecond * 500)
	conf.Worker.PollingRate = durationpb.New(time.Millisecond * 100)

	prefix := "funnel-e2e-tests-" + RandomString(6)
	conf.MongoDB.Database = prefix
	conf.DynamoDB.TableBasename = prefix
	conf.Datastore.Project = prefix

	reconcile := durationpb.New(time.Second * 5)
	conf.HTCondor.ReconcileRate = reconcile
	conf.Slurm.ReconcileRate = reconcile
	conf.PBS.ReconcileRate = reconcile
	conf.AWSBatch.ReconcileRate = reconcile

	storageDir := artifactDir(scope, "funnel-test-storage-")
	wd, err := os.Getwd()
	if err != nil {
		panic(fmt.Errorf("get test working directory: %w", err))
	}

	conf.LocalStorage = &config.LocalStorage{
		AllowedDirs: []string{storageDir, wd},
	}

	return conf
}

// RandomPortConfig returns a modified config with random HTTP and RPC ports.
func RandomPortConfig(conf *config.Config) *config.Config {
	ports := randomPorts(2)
	conf.Server.RPCPort = ports[0]
	conf.Server.HTTPPort = ports[1]
	conf.RPCClient.ServerAddress = appconfig.RPCAddress(conf.Server)
	return conf
}

func randomPorts(count int) []string {
	listeners := make([]net.Listener, 0, count)
	defer func() {
		for _, listener := range listeners {
			err := listener.Close()
			if err != nil {
				panic(fmt.Errorf("release allocated test port: %w", err))
			}
		}
	}()

	ports := make([]string, 0, count)
	for i := 0; i < count; i++ {
		listener, err := net.Listen("tcp", ":0")
		if err != nil {
			panic(fmt.Errorf("allocate test port: %w", err))
		}
		listeners = append(listeners, listener)
		ports = append(ports, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	}
	return ports
}

// TempDirConfig returns a modified config with workdir and db path set to a temp. directory.
func TempDirConfig(scope CleanupScope, conf *config.Config) *config.Config {
	dir := artifactDir(scope, "funnel-test-")
	conf.Worker.WorkDir = dir
	conf.BoltDB.Path = path.Join(dir, "funnel.db")
	conf.Badger.Path = path.Join(dir, "funnel.badger.db")
	return conf
}

// RandomString generates a random string of length n
func RandomString(n int) string {
	var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz0123456789")
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

// SetLogOutput provides a method for connecting funnel logso the the test logger
func SetLogOutput(l *logger.Logger, t *testing.T) {
	l.SetOutput(TestingWriter(t))
}

// TestingWriter returns an io.Writer that writes each line via t.Log
func TestingWriter(t *testing.T) io.Writer {
	reader, writer := io.Pipe()
	scanner := bufio.NewScanner(reader)
	go func() {
		for scanner.Scan() {
			// Carriage return removes testing's file:line number and indent.
			// In this case, the file and line will always be "utils.go:62".
			// Go 1.9 introduced t.Helper() to fix this, but something about
			// this function being in a goroutine seems to break that.
			// Carriage return is the hack for now.
			t.Log("\r" + scanner.Text())
		}
		err := scanner.Err()
		if err != nil {
			t.Error("testing writer scanner error", err)
		}
	}()
	return writer
}

// LogConfig returns logger configuration useful for tests, which has a text indent.
func LogConfig() *logconfig.LoggerConfig {
	conf := logger.DefaultConfig()
	conf.TextFormat.ForceColors = true
	conf.TextFormat.Indent = "        "
	return conf
}
