package boltdb

import (
	"fmt"
	"log"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/util/fsutil"
	bolt "go.etcd.io/bbolt"
)

// TODO these should probably be unexported names

// TaskBucket defines the name of a bucket which maps
// task ID -> tes.Task struct
var TaskBucket = []byte("tasks")

// TasksQueued defines the name of a bucket which maps
// task ID -> nil
var TasksQueued = []byte("tasks-queued")

// TaskOwner maps: task ID -> owner string
var TaskOwner = []byte("tasks-owner")

// TaskState maps: task ID -> state string
var TaskState = []byte("tasks-state")

// ExecutionClaims maps task IDs to durable execution-admission markers.
var ExecutionClaims = []byte("execution-claims")

// TaskClaims maps task ID -> scheduler-assigned node ID.
var TaskClaims = []byte("tasks-claims")

// NodeClaims indexes active task claims by node ID. Its creation marks the
// transactional migration from legacy node task-ID arrays.
var NodeClaims = []byte("node-claims")

// TasksLog defines the name of a bucket which maps
// task ID -> tes.TaskLog struct
var TasksLog = []byte("tasks-log")

// ExecutorLogs maps (task ID + executor index) -> tes.ExecutorLog struct
var ExecutorLogs = []byte("executor-logs")

// ExecutorStdout maps (task ID + executor index) -> tes.ExecutorLog.Stdout string
var ExecutorStdout = []byte("executor-stdout")

// ExecutorStderr maps (task ID + executor index) -> tes.ExecutorLog.Stderr string
var ExecutorStderr = []byte("executor-stderr")

// Nodes maps:
// node ID -> pbs.Node struct
var Nodes = []byte("nodes")

// SysLogs defeines the name of a bucket with maps
// task ID -> tes.TaskLog.SystemLogs
var SysLogs = []byte("system-logs")

// BoltDB provides handlers for gRPC endpoints.
// Data is stored/retrieved from the BoltDB key-value database.
type BoltDB struct {
	scheduler.UnimplementedSchedulerServiceServer
	db  *bolt.DB
	log *logger.Logger
}

// NewBoltDB returns a new instance of BoltDB, accessing the database at
// the given path, and including the given ServerConfig.
func NewBoltDB(conf *config.BoltDB) (*BoltDB, error) {
	if err := fsutil.EnsurePath(conf.Path); err != nil {
		return nil, fmt.Errorf("creating boltdb directory: %w", err)
	}
	db, err := bolt.Open(conf.Path, 0600, &bolt.Options{
		Timeout: time.Second * 5,
	})
	if err != nil {
		return nil, err
	}
	return &BoltDB{db: db, log: logger.NewLogger("BoltDB", logger.DefaultConfig())}, nil
}

// Init creates the required BoltDB buckets
func (taskBolt *BoltDB) Init() error {
	// Check to make sure all the required buckets have been created
	return taskBolt.db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket(TaskBucket) == nil {
			if _, err := tx.CreateBucket(TaskBucket); err != nil {
				return err
			}
		}
		if tx.Bucket(TasksQueued) == nil {
			if _, err := tx.CreateBucket(TasksQueued); err != nil {
				return err
			}
		}
		if tx.Bucket(TaskOwner) == nil {
			if _, err := tx.CreateBucket(TaskOwner); err != nil {
				return err
			}
		}
		if tx.Bucket(TaskState) == nil {
			if _, err := tx.CreateBucket(TaskState); err != nil {
				return err
			}
		}
		if tx.Bucket(ExecutionClaims) == nil {
			claims, err := tx.CreateBucket(ExecutionClaims)
			if err != nil {
				return err
			}
			states := tx.Bucket(TaskState)
			if err := states.ForEach(func(taskID, state []byte) error {
				value, ok := tes.State_value[string(state)]
				if !ok {
					return nil
				}
				current := tes.State(value)
				if current != tes.State_RUNNING && !tes.TerminalState(current) {
					return nil
				}
				return claims.Put(taskID, []byte{1})
			}); err != nil {
				return err
			}
		}
		if tx.Bucket(TaskClaims) == nil {
			if _, err := tx.CreateBucket(TaskClaims); err != nil {
				return err
			}
		}
		if tx.Bucket(TasksLog) == nil {
			if _, err := tx.CreateBucket(TasksLog); err != nil {
				return err
			}
		}
		if tx.Bucket(ExecutorLogs) == nil {
			if _, err := tx.CreateBucket(ExecutorLogs); err != nil {
				return err
			}
		}
		if tx.Bucket(ExecutorStdout) == nil {
			if _, err := tx.CreateBucket(ExecutorStdout); err != nil {
				return err
			}
		}
		if tx.Bucket(ExecutorStderr) == nil {
			if _, err := tx.CreateBucket(ExecutorStderr); err != nil {
				return err
			}
		}
		if tx.Bucket(Nodes) == nil {
			if _, err := tx.CreateBucket(Nodes); err != nil {
				return err
			}
		}
		if tx.Bucket(SysLogs) == nil {
			if _, err := tx.CreateBucket(SysLogs); err != nil {
				return err
			}
		}
		if err := migrateTaskClaims(tx); err != nil {
			return err
		}
		if err := migrateNodeHeartbeats(tx); err != nil {
			return err
		}
		if err := migrateResourceReservations(tx); err != nil {
			return err
		}
		return migrateNodeTaskIDProjection(tx)
	})
}

func (taskBolt *BoltDB) Close() {
	if err := taskBolt.db.Close(); err != nil {
		if taskBolt.log != nil {
			taskBolt.log.Error("closing BoltDB database", "error", err)
		} else {
			log.Printf("closing BoltDB database: %v", err)
		}
	}
}
