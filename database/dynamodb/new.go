package dynamodb

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/ohsu-comp-bio/funnel/config"
	util "github.com/ohsu-comp-bio/funnel/util/aws"
)

const defaultDynamoDBRetryMaxAttempts = 11

// DynamoDB provides handlers for gRPC endpoints
// Data is stored/retrieved from the Amazon DynamoDB NoSQL database.
type DynamoDB struct {
	client         *dynamodb.Client
	partitionKey   string
	partitionValue string
	taskTable      string
	contentTable   string
	stdoutTable    string
	stderrTable    string
	syslogsTable   string
}

// NewDynamoDB returns a new instance of DynamoDB, accessing the database at
// the given url, and including the given ServerConfig.
func NewDynamoDB(conf *config.DynamoDB) (*DynamoDB, error) {
	sess, err := util.NewAWSConfig(context.Background(), conf.AWSConfig)
	if err != nil {
		return nil, fmt.Errorf("error occurred creating dynamodb client: %v", err)
	}
	if conf.AWSConfig.MaxRetries <= 0 && sess.RetryMaxAttempts == 0 {
		sess.RetryMaxAttempts = defaultDynamoDBRetryMaxAttempts
	}

	db := &DynamoDB{
		client:         dynamodb.NewFromConfig(sess),
		partitionKey:   "hid",
		partitionValue: "0",
		taskTable:      conf.TableBasename + "-task",
		contentTable:   conf.TableBasename + "-content",
		stdoutTable:    conf.TableBasename + "-stdout",
		stderrTable:    conf.TableBasename + "-stderr",
		syslogsTable:   conf.TableBasename + "-syslogs",
	}

	return db, nil
}

// Init creates tables in DynamoDB.
func (db *DynamoDB) Init() error {
	return db.createTables()
}

func (db *DynamoDB) Close() {}
