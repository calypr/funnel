package schedulerprocess

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/database/mongodb"
	"github.com/calypr/funnel/database/postgres"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests/mongotest"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/protobuf/types/known/durationpb"
)

type dbSettings struct {
	backend string
	host    string
	user    string
	pass    string
	uri     string
	name    string
}

type schedulerStore interface {
	tes.ReadOnlyServer
	scheduler.SchedulerServiceServer
	scheduler.TaskQueue
	scheduler.AtomicTaskAssigner
	scheduler.NodeHealthStore
	events.Writer
	events.ExecutionClaimer
	Init() error
	Close()
}

type databaseFixture struct {
	backend string
	host    string
	uri     string
}

func startDatabaseFixture(t *testing.T, backend string) *databaseFixture {
	t.Helper()
	if backend == "mongodb" {
		return &databaseFixture{backend: backend, uri: mongotest.Start(t)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	container, err := postgrescontainer.Run(ctx, "postgres:16-alpine",
		postgrescontainer.WithDatabase("postgres"),
		postgrescontainer.WithUsername("postgres"),
		postgrescontainer.WithPassword("example"),
		postgrescontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Errorf("terminate PostgreSQL container: %v", err)
		}
	})
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("get PostgreSQL host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("get PostgreSQL port: %v", err)
	}
	return &databaseFixture{backend: backend, host: net.JoinHostPort(host, port.Port())}
}

func (f *databaseFixture) newDatabase(t *testing.T) dbSettings {
	t.Helper()
	name := fmt.Sprintf("funnel_scheduler_process_%d", time.Now().UnixNano())
	settings := dbSettings{backend: f.backend, host: f.host, uri: f.uri, user: "postgres", pass: "example", name: name}
	db := openStore(t, settings, true)
	t.Cleanup(db.Close)
	return settings
}

func openStore(t *testing.T, settings dbSettings, init bool) schedulerStore {
	t.Helper()
	var db schedulerStore
	switch settings.backend {
	case "postgres":
		conf := &config.Postgres{
			Host: settings.host, Database: settings.name, User: settings.user, Password: settings.pass,
			AdminUser: settings.user, AdminPassword: settings.pass,
			Timeout: &config.TimeoutConfig{TimeoutOption: &config.TimeoutConfig_Duration{Duration: durationpb.New(10 * time.Second)}},
		}
		value, err := postgres.NewPostgres(conf)
		if err != nil {
			t.Fatalf("open PostgreSQL store: %v", err)
		}
		db = value
	case "mongodb":
		conf := &config.MongoDB{
			Addrs: []string{settings.uri}, Database: settings.name,
			Timeout: &config.TimeoutConfig{TimeoutOption: &config.TimeoutConfig_Duration{Duration: durationpb.New(10 * time.Second)}},
		}
		value, err := mongodb.NewMongoDB(conf)
		if err != nil {
			t.Fatalf("open MongoDB store: %v", err)
		}
		db = value
	default:
		t.Fatalf("unsupported database backend %q", settings.backend)
	}
	if init {
		if err := db.Init(); err != nil {
			db.Close()
			t.Fatalf("initialize %s store: %v", settings.backend, err)
		}
	}
	return db
}

func dbSettingsFromEnv(t *testing.T) dbSettings {
	t.Helper()
	return dbSettings{
		backend: requiredEnv(t, "FUNNEL_PROCESS_DB_BACKEND"),
		host:    os.Getenv("FUNNEL_PROCESS_DB_HOST"),
		user:    os.Getenv("FUNNEL_PROCESS_DB_USER"),
		pass:    os.Getenv("FUNNEL_PROCESS_DB_PASSWORD"),
		uri:     os.Getenv("FUNNEL_PROCESS_MONGO_URI"),
		name:    requiredEnv(t, "FUNNEL_PROCESS_DB_NAME"),
	}
}

func (s dbSettings) env() []string {
	return []string{
		"FUNNEL_PROCESS_DB_BACKEND=" + s.backend,
		"FUNNEL_PROCESS_DB_HOST=" + s.host,
		"FUNNEL_PROCESS_DB_USER=" + s.user,
		"FUNNEL_PROCESS_DB_PASSWORD=" + s.pass,
		"FUNNEL_PROCESS_MONGO_URI=" + s.uri,
		"FUNNEL_PROCESS_DB_NAME=" + s.name,
	}
}
