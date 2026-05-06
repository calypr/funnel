package mongodb

import (
	"context"
	"fmt"
	"time"

	"github.com/ohsu-comp-bio/funnel/compute/scheduler"
	"github.com/ohsu-comp-bio/funnel/config"
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

func (db *MongoDB) createCollection(name string, indexKeys *bson.D) error {
	ctx, cancel := db.context()
	defer cancel()

	err := db.database.CreateCollection(ctx, name, nil)
	if err != nil {
		return fmt.Errorf(
			"error creating collection [%s] in database [%s]: %v",
			name, db.conf.Database, err)
	}

	indexModel := mongo.IndexModel{
		Keys:    indexKeys,
		Options: options.Index().SetUnique(true).SetSparse(true),
	}

	ctx, cancel = db.context()
	defer cancel()

	_, err = db.collection(name).Indexes().CreateOne(ctx, indexModel)
	return err
}

func (db *MongoDB) findCollections(names ...string) (map[string]bool, error) {
	ctx, cancel := db.context()
	defer cancel()

	filter := bson.M{"name": bson.M{"$in": names}}
	names, err := db.database.ListCollectionNames(ctx, filter, nil)
	if err != nil {
		return nil, err
	}

	result := make(map[string]bool)
	for _, name := range names {
		result[name] = true
	}
	return result, nil
}

// Init creates tables in MongoDB.
func (db *MongoDB) Init() error {
	found, err := db.findCollections("tasks", "nodes")
	if err != nil {
		return err
	}

	if !found["tasks"] {
		indexKeys := &bson.D{
			{Key: "-id", Value: -1},
			{Key: "-creationtime", Value: -1},
		}
		if err := db.createCollection("tasks", indexKeys); err != nil {
			return err
		}
	}

	if !found["nodes"] {
		indexKeys := &bson.D{
			{Key: "-id", Value: -1},
		}
		if err := db.createCollection("nodes", indexKeys); err != nil {
			return err
		}
	}

	return nil
}

// Close closes the database session.
func (db *MongoDB) Close() {
	if db.active {
		ctx, cancel := db.context()
		defer cancel()
		db.client.Disconnect(ctx)
	}
	db.active = false
}
