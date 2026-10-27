package mongodb

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestMongoNodePagination(t *testing.T) {
	addr := startMongoNodePaginationContainer(t)

	t.Run("pages nodes in sorted bounded batches", func(t *testing.T) {
		db := newMongoNodePaginationDB(t, addr, "node_pages")
		if err := db.Init(); err != nil {
			t.Fatalf("initialize MongoDB: %v", err)
		}

		insertedIDs := []string{"node-c", "node-a", "node-e", "node-b", "node-d"}
		documents := make([]interface{}, 0, len(insertedIDs))
		for _, id := range insertedIDs {
			documents = append(documents, bson.M{"id": id})
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := db.nodes().InsertMany(ctx, documents); err != nil {
			t.Fatalf("seed nodes: %v", err)
		}

		var gotIDs []string
		request := &scheduler.ListNodesRequest{PageSize: 2}
		for pageNumber := 0; pageNumber < 4; pageNumber++ {
			response, err := db.ListNodes(context.Background(), request)
			if err != nil {
				t.Fatalf("list page %d: %v", pageNumber+1, err)
			}
			if len(response.Nodes) > int(request.PageSize) {
				t.Fatalf("page %d returned %d nodes, limit is %d", pageNumber+1, len(response.Nodes), request.PageSize)
			}
			for _, node := range response.Nodes {
				gotIDs = append(gotIDs, node.GetId())
			}
			if response.NextPageToken == "" {
				break
			}
			if pageNumber == 3 {
				t.Fatal("pagination did not terminate after four pages")
			}
			request = &scheduler.ListNodesRequest{PageSize: 2, PageToken: response.NextPageToken}
		}

		wantIDs := []string{"node-a", "node-b", "node-c", "node-d", "node-e"}
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Fatalf("listed node IDs = %v, want %v", gotIDs, wantIDs)
		}
	})

	t.Run("adds simple unique ID index to existing collection", func(t *testing.T) {
		db := newMongoNodePaginationDB(t, addr, "node_index_upgrade")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := db.database.CreateCollection(ctx, "nodes", options.CreateCollection().SetCollation(&options.Collation{
			Locale:   "en",
			Strength: 2,
		})); err != nil {
			t.Fatalf("create legacy nodes collection with default collation: %v", err)
		}
		if _, err := db.nodes().InsertMany(ctx, []interface{}{
			bson.M{"id": "legacy-node"},
			bson.M{"id": "LEGACY-NODE"},
		}); err != nil {
			t.Fatalf("seed existing nodes collection: %v", err)
		}

		if err := db.Init(); err != nil {
			t.Fatalf("initialize existing nodes collection: %v", err)
		}
		assertUniqueNodeIDIndex(t, db)

		count, err := db.nodes().CountDocuments(ctx, bson.M{})
		if err != nil {
			t.Fatalf("count legacy node after index upgrade: %v", err)
		}
		if count != 2 {
			t.Fatalf("legacy node count after index upgrade = %d, want 2", count)
		}
	})

	t.Run("duplicate IDs fail initialization and keep records", func(t *testing.T) {
		db := newMongoNodePaginationDB(t, addr, "node_duplicate_ids")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := db.nodes().InsertMany(ctx, []interface{}{
			bson.M{"id": "duplicate-node", "marker": "first"},
			bson.M{"id": "duplicate-node", "marker": "second"},
		}); err != nil {
			t.Fatalf("seed duplicate node IDs: %v", err)
		}

		err := db.Init()
		if err == nil {
			t.Fatal("Init succeeded with duplicate node IDs, want unique-index failure")
		}
		if !strings.Contains(err.Error(), "nodes.id") {
			t.Fatalf("Init error = %v, want nodes.id index error", err)
		}

		count, err := db.nodes().CountDocuments(ctx, bson.M{"id": "duplicate-node"})
		if err != nil {
			t.Fatalf("count duplicate node records after failed Init: %v", err)
		}
		if count != 2 {
			t.Fatalf("duplicate node record count after failed Init = %d, want 2", count)
		}
	})
}

func startMongoNodePaginationContainer(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("Mongo integration test requires Docker")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "mongo:7",
			ExposedPorts: []string{"27017/tcp"},
			WaitingFor:   wait.ForLog("Waiting for connections").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start MongoDB container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Errorf("terminate MongoDB container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("get MongoDB container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "27017/tcp")
	if err != nil {
		t.Fatalf("get mapped MongoDB port: %v", err)
	}
	return fmt.Sprintf("%s:%s", host, port.Port())
}

func newMongoNodePaginationDB(t *testing.T, addr, database string) *MongoDB {
	t.Helper()
	db, err := NewMongoDB(&config.MongoDB{
		Addrs:    []string{addr},
		Database: database,
		Timeout: &config.TimeoutConfig{
			TimeoutOption: &config.TimeoutConfig_Duration{Duration: durationpb.New(10 * time.Second)},
		},
	})
	if err != nil {
		t.Fatalf("connect MongoDB: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func assertUniqueNodeIDIndex(t *testing.T, db *MongoDB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cursor, err := db.nodes().Indexes().List(ctx)
	if err != nil {
		t.Fatalf("list node indexes: %v", err)
	}
	defer cursor.Close(ctx)

	var indexes []bson.M
	if err := cursor.All(ctx, &indexes); err != nil {
		t.Fatalf("decode node indexes: %v", err)
	}
	for _, index := range indexes {
		if index["name"] != "id_1" {
			continue
		}
		if index["unique"] != true {
			t.Fatalf("id_1 index = %#v, want unique index", index)
		}
		return
	}
	t.Fatal("nodes collection has no id_1 index")
}
