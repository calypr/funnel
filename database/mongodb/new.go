package mongodb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoDB provides an MongoDB database server backend.
type MongoDB struct {
	scheduler.UnimplementedSchedulerServiceServer
	client   *mongo.Client
	database *mongo.Database
	conf     *config.MongoDB
	active   bool
}

func NewMongoDB(conf *config.MongoDB) (*MongoDB, error) {
	opts := options.Client().
		SetHosts(conf.Addrs).
		SetAppName("funnel")

	if len(conf.Addrs) == 1 && (strings.HasPrefix(conf.Addrs[0], "mongodb://") || strings.HasPrefix(conf.Addrs[0], "mongodb+srv://")) {
		opts = options.Client().ApplyURI(conf.Addrs[0]).SetAppName("funnel")
	}

	if len(conf.Username) > 0 && len(conf.Password) > 0 {
		opts = opts.SetAuth(options.Credential{
			Username: conf.Username,
			Password: conf.Password,
		})
	}

	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, err
	}

	db := &MongoDB{
		client:   client,
		database: client.Database(conf.Database),
		conf:     conf,
		active:   true,
	}
	return db, nil
}

func (db *MongoDB) context() (context.Context, context.CancelFunc) {
	return db.wrap(context.Background())
}

func (db *MongoDB) wrap(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, db.conf.Timeout.GetDuration().AsDuration())
}

func (db *MongoDB) collection(name string) *mongo.Collection {
	return db.database.Collection(name)
}

func (db *MongoDB) nodes() *mongo.Collection {
	return db.collection("nodes")
}

func (db *MongoDB) tasks() *mongo.Collection {
	return db.collection("tasks")
}

// Init creates tables in MongoDB.
func (db *MongoDB) Init() error {
	ctx, cancel := db.context()
	defer cancel()
	if err := db.requireTransactions(ctx); err != nil {
		return err
	}
	for _, name := range []string{"tasks", "nodes", "node_heartbeats", "scheduler_migrations"} {
		if err := db.database.CreateCollection(ctx, name); err != nil {
			var commandError mongo.CommandError
			if !errors.As(err, &commandError) || commandError.Code != 48 {
				return err
			}
		}
	}
	if err := db.ensureNodeIDIndex(); err != nil {
		return err
	}
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "state", Value: 1}, {Key: "creationtime", Value: 1}, {Key: "id", Value: 1}}},
		{Keys: bson.D{{Key: "claim_owner", Value: 1}, {Key: "id", Value: 1}}, Options: options.Index().SetPartialFilterExpression(bson.M{"claim_active": true})},
	}
	if _, err := db.tasks().Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensuring task identity and scheduler indexes: %w", err)
	}
	if err := db.migrateScheduler(ctx); err != nil {
		return err
	}
	return db.migrateExecutionClaims(ctx)
}

func (db *MongoDB) ensureNodeIDIndex() error {
	ctx, cancel := db.context()
	defer cancel()

	index := mongo.IndexModel{
		Keys: bson.D{{Key: "id", Value: 1}},
		Options: options.Index().
			SetName("id_1").
			SetUnique(true).
			SetCollation(&options.Collation{Locale: "simple"}),
	}
	if _, err := db.nodes().Indexes().CreateOne(ctx, index); err != nil {
		return fmt.Errorf("ensuring unique simple-collation index on nodes.id (duplicate legacy node IDs must be resolved before startup): %w", err)
	}
	return nil
}

// Close closes the database session.
func (db *MongoDB) Close() {
	if db.active {
		ctx, cancel := db.context()
		defer cancel()
		_ = db.client.Disconnect(ctx)
	}
	db.active = false
}
