package postgres

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ohsu-comp-bio/funnel/compute/scheduler"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/util"
)

// Postgres provides a PostgreSQL database server backend.
type Postgres struct {
	scheduler.UnimplementedSchedulerServiceServer
	client *pgxpool.Pool
	conf   config.Postgres
	active bool
}

func NewPostgres(conf *config.Postgres) (*Postgres, error) {
	ctx := context.Background()

	// Initialize the connection pool
	pool, err := pgxpool.New(ctx, getConnStr(*conf))
	if err != nil {
		return nil, err
	}

	return &Postgres{
		conf:   *conf,
		active: true,
		client: pool,
	}, nil
}

func (db *Postgres) Init() error {
	ctx := context.Background()

	retrier := util.NewRetrier()
	retrier.MaxElapsedTime = time.Second * 300

	return retrier.Retry(ctx, func() error {
		// Check/create resources (Roles/DBs)
		if err := ensureDatabaseExists(ctx, db.conf); err != nil {
			return err
		}

		// Create Tables and Indices
		return db.createTables(ctx)
	})
}

func (db *Postgres) context() (context.Context, context.CancelFunc) {
	// Use a timeout from the configuration
	return context.WithTimeout(context.Background(), db.conf.Timeout.GetDuration().AsDuration())
}

func getConnStr(conf config.Postgres) string {
	u := url.UserPassword(conf.User, conf.Password)

	connURL := url.URL{
		Scheme: "postgres",
		User:   u,
		Host:   conf.Host,
		Path:   conf.Database,
	}

	return connURL.String()
}

func getAdminConnStr(conf config.Postgres) string {
	conf.User = conf.AdminUser
	conf.Password = conf.AdminPassword
	return getConnStr(conf)
}

func ensureDatabaseExists(ctx context.Context, conf config.Postgres) error {
	// First check that we even need to connect as an "admin" in order to create the Funnel Role + DB
	connStr := getConnStr(conf)
	probeConn, err := pgx.Connect(ctx, connStr)
	if err == nil {
		defer probeConn.Close(ctx)
		var hasFullAccess bool
		checkSQL := `
			SELECT EXISTS (
				SELECT 1 FROM pg_database d
				WHERE d.datname = $1 
				AND has_database_privilege($2, $1, 'CONNECT')
				AND has_schema_privilege($2, 'public', 'CREATE')
			)`

		err = probeConn.QueryRow(ctx, checkSQL, conf.Database, conf.User).Scan(&hasFullAccess)

		// Role + DB already exist, no need to try connecting as admin
		if err == nil && hasFullAccess {
			return nil
		}
	}

	return createResources(ctx, conf)
}

func createResources(ctx context.Context, conf config.Postgres) error {
	connStr := getAdminConnStr(conf)
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect as admin for bootstrap: %w", err)
	}
	defer conn.Close(ctx)

	// Role
	createUser := fmt.Sprintf(
		"DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = '%s') "+
			"THEN CREATE ROLE %s WITH LOGIN PASSWORD '%s'; END IF; END $$",
		conf.User, conf.User, conf.Password,
	)
	if _, err := conn.Exec(ctx, createUser); err != nil {
		return fmt.Errorf("failed to ensure role exists: %w", err)
	}

	// Database
	createDB := fmt.Sprintf("CREATE DATABASE %s OWNER %s", conf.Database, conf.User)
	var dbExists int
	conn.QueryRow(ctx, "SELECT 1 FROM pg_database WHERE datname=$1", conf.Database).Scan(&dbExists)
	if dbExists != 1 {
		if _, err := conn.Exec(ctx, createDB); err != nil {
			return fmt.Errorf("failed to create app database: %w", err)
		}
	}

	return nil
}

func (db *Postgres) createTables(ctx context.Context) error {
	// Tasks
	tasks := `
    CREATE TABLE IF NOT EXISTS tasks (
		id VARCHAR(255) PRIMARY KEY,
		state VARCHAR(50) NOT NULL,
		owner VARCHAR(255),
		creation_time TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
		version BIGINT DEFAULT 0 NOT NULL,
		data JSONB
    );
    `

	if _, err := db.client.Exec(ctx, tasks); err != nil {
		return fmt.Errorf("failed to create 'tasks' table: %w", err)
	}

	// Nodes
	nodes := `
    CREATE TABLE IF NOT EXISTS nodes (
		id VARCHAR(255) PRIMARY KEY,
		state VARCHAR(50) NOT NULL,
		owner VARCHAR(255),
		version BIGINT DEFAULT 0 NOT NULL,
		last_heartbeat TIMESTAMP WITH TIME ZONE,
		data JSONB
    );
    `

	if _, err := db.client.Exec(ctx, nodes); err != nil {
		return fmt.Errorf("failed to create 'nodes' table: %w", err)
	}

	indices := []string{
		"CREATE INDEX IF NOT EXISTS idx_tasks_state ON tasks (state);",
		"CREATE INDEX IF NOT EXISTS idx_tasks_owner ON tasks (owner);",
		"CREATE INDEX IF NOT EXISTS idx_tasks_creation_time ON tasks (creation_time DESC);",
		"CREATE INDEX IF NOT EXISTS idx_nodes_state ON nodes (state);",
		"CREATE INDEX IF NOT EXISTS idx_nodes_owner ON nodes (owner);",
	}

	for _, query := range indices {
		if _, err := db.client.Exec(ctx, query); err != nil {
			return fmt.Errorf("failed to create index with query '%s': %w", query, err)
		}
	}

	return nil
}

// Close closes the database session.
func (db *Postgres) Close() {
	if db.active {
		db.client.Close()
		db.active = false
	}
}
