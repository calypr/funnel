package dynamodb

import (
	"fmt"

	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/ohsu-comp-bio/funnel/config"
	util "github.com/ohsu-comp-bio/funnel/util/aws"
)

// DynamoDB provides handlers for gRPC endpoints
// Data is stored/retrieved from the Amazon DynamoDB NoSQL database.
type DynamoDB struct {
	client         *dynamodb.DynamoDB
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
	sess, err := util.NewAWSSession(conf.AWSConfig)
	if err != nil {
		return nil, fmt.Errorf("error occurred creating dynamodb client: %v", err)
	}

	db := &DynamoDB{
		client:         dynamodb.New(sess),
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
