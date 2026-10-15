package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/ohsu-comp-bio/funnel/server"
	"github.com/ohsu-comp-bio/funnel/tes"
)

func checkCreateErr(err error) error {
	var aerr smithy.APIError
	if errors.As(err, &aerr) {
		switch aerr.ErrorCode() {
		case "ResourceInUseException":
			return nil
		}
	}
	return err
}

func (db *DynamoDB) createTables() error {
	var table *dynamodb.CreateTableInput
	var err error

	table = &dynamodb.CreateTableInput{
		TableName: aws.String(db.taskTable),
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String(db.partitionKey),
				AttributeType: "S",
			},
			{
				AttributeName: aws.String("id"),
				AttributeType: "S",
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String(db.partitionKey),
				KeyType:       "HASH",
			},
			{
				AttributeName: aws.String("id"),
				KeyType:       "RANGE",
			},
		},
		ProvisionedThroughput: &types.ProvisionedThroughput{
			ReadCapacityUnits:  aws.Int64(1),
			WriteCapacityUnits: aws.Int64(1),
		},
	}
	_, err = db.client.CreateTable(context.Background(), table)
	if checkCreateErr(err) != nil {
		return err
	}

	table = &dynamodb.CreateTableInput{
		TableName: aws.String(db.contentTable),
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String("id"),
				AttributeType: "S",
			},
			{
				AttributeName: aws.String("index"),
				AttributeType: "N",
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String("id"),
				KeyType:       "HASH",
			},
			{
				AttributeName: aws.String("index"),
				KeyType:       "RANGE",
			},
		},
		ProvisionedThroughput: &types.ProvisionedThroughput{
			ReadCapacityUnits:  aws.Int64(1),
			WriteCapacityUnits: aws.Int64(1),
		},
	}
	_, err = db.client.CreateTable(context.Background(), table)
	if checkCreateErr(err) != nil {
		return err
	}

	table = &dynamodb.CreateTableInput{
		TableName: aws.String(db.stdoutTable),
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String("id"),
				AttributeType: "S",
			},
			{
				AttributeName: aws.String("attempt_index"),
				AttributeType: "S",
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String("id"),
				KeyType:       "HASH",
			},
			{
				AttributeName: aws.String("attempt_index"),
				KeyType:       "RANGE",
			},
		},
		ProvisionedThroughput: &types.ProvisionedThroughput{
			ReadCapacityUnits:  aws.Int64(1),
			WriteCapacityUnits: aws.Int64(1),
		},
	}
	_, err = db.client.CreateTable(context.Background(), table)
	if checkCreateErr(err) != nil {
		return err
	}

	table = &dynamodb.CreateTableInput{
		TableName: aws.String(db.stderrTable),
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String("id"),
				AttributeType: "S",
			},
			{
				AttributeName: aws.String("attempt_index"),
				AttributeType: "S",
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String("id"),
				KeyType:       "HASH",
			},
			{
				AttributeName: aws.String("attempt_index"),
				KeyType:       "RANGE",
			},
		},
		ProvisionedThroughput: &types.ProvisionedThroughput{
			ReadCapacityUnits:  aws.Int64(1),
			WriteCapacityUnits: aws.Int64(1),
		},
	}
	_, err = db.client.CreateTable(context.Background(), table)
	if checkCreateErr(err) != nil {
		return err
	}

	table = &dynamodb.CreateTableInput{
		TableName: aws.String(db.syslogsTable),
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String("id"),
				AttributeType: "S",
			},
			{
				AttributeName: aws.String("attempt"),
				AttributeType: "N",
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String("id"),
				KeyType:       "HASH",
			},
			{
				AttributeName: aws.String("attempt"),
				KeyType:       "RANGE",
			},
		},
		ProvisionedThroughput: &types.ProvisionedThroughput{
			ReadCapacityUnits:  aws.Int64(1),
			WriteCapacityUnits: aws.Int64(1),
		},
	}
	_, err = db.client.CreateTable(context.Background(), table)
	if checkCreateErr(err) != nil {
		return err
	}
	return db.waitForTables()
}

func (db *DynamoDB) tableIsAlive(ctx context.Context, name string) error {
	ticker := time.NewTicker(time.Millisecond * 500).C
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker:
			r, err := db.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
				TableName: aws.String(name),
			})
			if err != nil {
				var aerr smithy.APIError
				if errors.As(err, &aerr) {
					if aerr.ErrorCode() == "ResourceNotFoundException" {
						continue
					}
				}
				return err
			}
			if r.Table.TableStatus == "ACTIVE" {
				return nil
			}
		}
	}
}

func (db *DynamoDB) waitForTables() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
	defer cancel()

	if err := db.tableIsAlive(ctx, db.taskTable); err != nil {
		return err
	}
	if err := db.tableIsAlive(ctx, db.contentTable); err != nil {
		return err
	}
	if err := db.tableIsAlive(ctx, db.stdoutTable); err != nil {
		return err
	}
	if err := db.tableIsAlive(ctx, db.stderrTable); err != nil {
		return err
	}
	if err := db.tableIsAlive(ctx, db.syslogsTable); err != nil {
		return err
	}

	return nil
}

func (db *DynamoDB) createTask(ctx context.Context, task *tes.Task) error {
	taskBasic := task.GetBasicView()
	av, err := attributevalue.MarshalMapWithOptions(taskBasic, encodeJSONTags)
	if err != nil {
		return fmt.Errorf("failed to DynamoDB marshal Task, %v", err)
	}

	for key, value := range av {
		av[key] = normalizeStoredValue(value)
	}

	av[db.partitionKey] = &types.AttributeValueMemberS{
		Value: db.partitionValue,
	}

	av["version"] = &types.AttributeValueMemberS{
		Value: strconv.FormatInt(time.Now().UnixNano(), 10),
	}

	av["owner"] = &types.AttributeValueMemberS{
		Value: server.GetUsername(ctx),
	}

	// Add nil fields to make updates easier
	av["logs"] = &types.AttributeValueMemberL{
		Value: []types.AttributeValue{
			&types.AttributeValueMemberM{
				Value: map[string]types.AttributeValue{
					"logs": &types.AttributeValueMemberL{
						Value: []types.AttributeValue{},
					},
					"outputs": &types.AttributeValueMemberL{
						Value: []types.AttributeValue{},
					},
				},
			},
		},
	}

	item := &dynamodb.PutItemInput{
		TableName: aws.String(db.taskTable),
		Item:      av,
	}

	_, err = db.client.PutItem(ctx, item)
	if err != nil {
		return fmt.Errorf("failed to write task items to DynamoDB, %v", err)
	}

	err = db.createTaskInputContent(ctx, task)
	if err != nil {
		return fmt.Errorf("failed to write task items to DynamoDB, %v", err)
	}
	return nil
}

func (db *DynamoDB) createTaskInputContent(ctx context.Context, task *tes.Task) error {
	av := make(map[string]types.AttributeValue)

	for i, v := range task.Inputs {
		if v.Content != "" {
			av["id"] = &types.AttributeValueMemberS{
				Value: task.Id,
			}
			av["index"] = &types.AttributeValueMemberN{
				Value: strconv.Itoa(i),
			}
			av["content"] = &types.AttributeValueMemberS{
				Value: v.Content,
			}

			item := &dynamodb.PutItemInput{
				TableName: aws.String(db.contentTable),
				Item:      av,
			}

			_, err := db.client.PutItem(ctx, item)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (db *DynamoDB) taskKey(id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		db.partitionKey: &types.AttributeValueMemberS{
			Value: db.partitionValue,
		},
		"id": &types.AttributeValueMemberS{
			Value: id,
		},
	}
}

func (db *DynamoDB) getMinimalView(ctx context.Context, id string) (*dynamodb.GetItemOutput, error) {
	item := &dynamodb.GetItemInput{
		TableName:      aws.String(db.taskTable),
		Key:            db.taskKey(id),
		ConsistentRead: aws.Bool(true),
		ExpressionAttributeNames: map[string]string{
			"#owner": "owner",
			"#state": "state",
		},
		ProjectionExpression: aws.String("id, #owner, #state"),
	}
	return db.client.GetItem(ctx, item)
}

func (db *DynamoDB) getBasicView(ctx context.Context, id string) (*dynamodb.GetItemOutput, error) {
	item := &dynamodb.GetItemInput{
		TableName: aws.String(db.taskTable),
		Key:       db.taskKey(id),
	}
	return db.client.GetItem(ctx, item)
}

func (db *DynamoDB) getFullView(ctx context.Context, id string) (*dynamodb.GetItemOutput, error) {
	resp, err := db.getBasicView(ctx, id)
	if err != nil || resp.Item == nil {
		return resp, err
	}

	err = db.getContent(ctx, resp.Item)
	if err != nil {
		return resp, fmt.Errorf("failed to retrieve input content: %v", err)
	}

	err = db.getExecutorOutput(ctx, resp.Item, "stdout", db.stdoutTable)
	if err != nil {
		return resp, fmt.Errorf("failed to retrieve task executor log stdout: %v", err)
	}

	err = db.getExecutorOutput(ctx, resp.Item, "stderr", db.stderrTable)
	if err != nil {
		return resp, fmt.Errorf("failed to retrieve task executor log stderr: %v", err)
	}

	err = db.getSystemLogs(ctx, resp.Item)
	if err != nil {
		return resp, fmt.Errorf("failed to retrieve system logs: %v", err)
	}

	return resp, nil
}
func (db *DynamoDB) queryInput(table string, id types.AttributeValue, limit int64) *dynamodb.QueryInput {
	return &dynamodb.QueryInput{
		TableName:              aws.String(table),
		Limit:                  aws.Int32(int32(limit)),
		ScanIndexForward:       aws.Bool(false),
		ConsistentRead:         aws.Bool(true),
		KeyConditionExpression: aws.String("id = :v1"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":v1": id,
		},
	}
}

func (db *DynamoDB) getContent(ctx context.Context, in map[string]types.AttributeValue) error {
	pages := dynamodb.NewQueryPaginator(db.client, db.queryInput(db.contentTable, in["id"], 10))
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			i, _ := strconv.ParseInt(item["index"].(*types.AttributeValueMemberN).Value, 10, 64)
			in["inputs"].(*types.AttributeValueMemberL).Value[i].(*types.AttributeValueMemberM).Value["content"] = item["content"]
		}
	}
	return nil
}

func (db *DynamoDB) getExecutorOutput(ctx context.Context, in map[string]types.AttributeValue, val string, table string) error {
	pages := dynamodb.NewQueryPaginator(db.client, db.queryInput(table, in["id"], 10))
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			i, _ := strconv.ParseInt(item["index"].(*types.AttributeValueMemberN).Value, 10, 64)
			a, _ := strconv.ParseInt(item["attempt"].(*types.AttributeValueMemberN).Value, 10, 64)
			if out, ok := item[val]; ok {
				attempt := in["logs"].(*types.AttributeValueMemberL).Value[a].(*types.AttributeValueMemberM).Value
				attempt["logs"].(*types.AttributeValueMemberL).Value[i].(*types.AttributeValueMemberM).Value[val] = out
			}
		}
	}
	return nil
}

func (db *DynamoDB) getSystemLogs(ctx context.Context, in map[string]types.AttributeValue) error {
	pages := dynamodb.NewQueryPaginator(db.client, db.queryInput(db.syslogsTable, in["id"], 50))
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			i, _ := strconv.ParseInt(item["attempt"].(*types.AttributeValueMemberN).Value, 10, 64)
			in["logs"].(*types.AttributeValueMemberL).Value[i].(*types.AttributeValueMemberM).Value["system_logs"] = item["system_logs"]
		}
	}
	return nil
}

func encodeJSONTags(options *attributevalue.EncoderOptions) { options.TagKey = "json" }
func decodeJSONTags(options *attributevalue.DecoderOptions) { options.TagKey = "json" }

func expressionValue(value any) expression.ValueBuilder {
	return expression.Value(storedValue{value})
}

type storedValue struct{ value any }

func (value storedValue) MarshalDynamoDBAttributeValue() (types.AttributeValue, error) {
	encoded, err := attributevalue.MarshalWithOptions(value.value, encodeJSONTags)
	if err != nil {
		return nil, err
	}
	return normalizeStoredValue(encoded), nil
}

// Existing task records encode empty strings and collections as DynamoDB NULL.
func normalizeStoredValue(value types.AttributeValue) types.AttributeValue {
	switch value := value.(type) {
	case *types.AttributeValueMemberS:
		if value.Value == "" {
			return &types.AttributeValueMemberNULL{Value: true}
		}
	case *types.AttributeValueMemberB:
		if len(value.Value) == 0 {
			return &types.AttributeValueMemberNULL{Value: true}
		}
	case *types.AttributeValueMemberL:
		if len(value.Value) == 0 {
			return &types.AttributeValueMemberNULL{Value: true}
		}
		for i, item := range value.Value {
			value.Value[i] = normalizeStoredValue(item)
		}
	case *types.AttributeValueMemberM:
		if len(value.Value) == 0 {
			return &types.AttributeValueMemberNULL{Value: true}
		}
		for key, item := range value.Value {
			value.Value[key] = normalizeStoredValue(item)
		}
	}
	return value
}

func isAccessible(ctx context.Context, response *dynamodb.GetItemOutput) bool {
	taskOwner := ""
	if attrValue, ok := response.Item["owner"]; ok {
		taskOwner = attrValue.(*types.AttributeValueMemberS).Value
	}
	return server.GetUser(ctx).IsAccessible(taskOwner)
}
