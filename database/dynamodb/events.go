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
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/util"
)

// WriteEvent creates an event for the server to handle.
func (db *DynamoDB) WriteEvent(ctx context.Context, e *events.Event) error {
	item := &dynamodb.UpdateItemInput{
		TableName: aws.String(db.taskTable),
		Key: map[string]types.AttributeValue{
			db.partitionKey: &types.AttributeValueMemberS{
				Value: db.partitionValue,
			},
			"id": &types.AttributeValueMemberS{
				Value: e.Id,
			},
		},
	}

	var updateExpr expression.UpdateBuilder

	switch e.Type {
	case events.Type_TASK_CREATED:
		return db.createTask(ctx, e.GetTask())

	case events.Type_TASK_STATE:
		retrier := util.NewRetrier()
		retrier.ShouldRetry = func(err error) bool {
			var aerr smithy.APIError
			if errors.As(err, &aerr) {
				if aerr.ErrorCode() == "ConditionalCheckFailedException" ||
					aerr.ErrorCode() == "ProvisionedThroughputExceededException" {
					return true
				}
			}
			return false
		}

		return retrier.Retry(ctx, func() error {
			// get current state & version
			current := make(map[string]interface{})
			response, err := db.getBasicView(ctx, e.Id)
			if err != nil {
				return err
			}
			if response.Item == nil {
				return tes.ErrNotFound
			}
			if !isAccessible(ctx, response) {
				return tes.ErrNotPermitted
			}

			err = attributevalue.UnmarshalMapWithOptions(response.Item, &current, decodeJSONTags)
			if err != nil {
				return fmt.Errorf("failed to DynamoDB unmarshal Task, %v", err)
			}

			// validate state transition
			from := tes.State(current["state"].(float64))
			to := e.GetState()
			if err := tes.ValidateTransition(from, to); err != nil {
				return err
			}

			// apply version restriction and set update
			condExpr := expression.Name("version").Equal(expressionValue(fmt.Sprintf("%v", current["version"])))
			updateExpr = expression.Set(expression.Name("state"), expressionValue(to))
			updateExpr = updateExpr.Set(expression.Name("version"), expressionValue(strconv.FormatInt(time.Now().UnixNano(), 10)))

			// build update item
			expr, err := expression.NewBuilder().WithUpdate(updateExpr).WithCondition(condExpr).Build()
			if err != nil {
				return err
			}

			item.ExpressionAttributeNames = expr.Names()
			item.ExpressionAttributeValues = expr.Values()
			item.UpdateExpression = expr.Update()
			item.ConditionExpression = expr.Condition()

			// apply update with retries upon version collisions
			_, err = db.client.UpdateItem(ctx, item)
			return err
		})

	case events.Type_TASK_START_TIME:
		if err := db.ensureTaskLog(ctx, e.Id, e.Attempt); err != nil {
			return err
		}

		updateExpr = expression.Set(
			expression.Name(fmt.Sprintf("logs[%v].start_time", e.Attempt)),
			expressionValue(e.GetStartTime()),
		)

	case events.Type_TASK_END_TIME:
		if err := db.ensureTaskLog(ctx, e.Id, e.Attempt); err != nil {
			return err
		}

		updateExpr = expression.Set(
			expression.Name(fmt.Sprintf("logs[%v].end_time", e.Attempt)),
			expressionValue(e.GetEndTime()),
		)

	case events.Type_TASK_OUTPUTS:
		if err := db.ensureTaskLog(ctx, e.Id, e.Attempt); err != nil {
			return err
		}

		updateExpr = expression.Set(
			expression.Name(fmt.Sprintf("logs[%v].outputs", e.Attempt)),
			expressionValue(e.GetOutputs().Value),
		)

	case events.Type_TASK_METADATA:
		if err := db.ensureTaskLog(ctx, e.Id, e.Attempt); err != nil {
			return err
		}

		if err := db.ensureTaskLogMetadata(ctx, e.Id, e.Attempt); err != nil {
			return err
		}

		for k, v := range e.GetMetadata().Value {
			updateExpr = updateExpr.Set(
				expression.Name(fmt.Sprintf("logs[%v].metadata.%s", e.Attempt, k)),
				expressionValue(v),
			)
		}

	case events.Type_EXECUTOR_START_TIME:
		if err := db.ensureExecLog(ctx, e.Id, e.Attempt, e.Index); err != nil {
			return err
		}
		updateExpr = expression.Set(
			expression.Name(fmt.Sprintf("logs[%v].logs[%v].start_time", e.Attempt, e.Index)),
			expressionValue(e.GetStartTime()),
		)

	case events.Type_EXECUTOR_END_TIME:
		if err := db.ensureExecLog(ctx, e.Id, e.Attempt, e.Index); err != nil {
			return err
		}
		updateExpr = expression.Set(
			expression.Name(fmt.Sprintf("logs[%v].logs[%v].end_time", e.Attempt, e.Index)),
			expressionValue(e.GetEndTime()),
		)

	case events.Type_EXECUTOR_EXIT_CODE:
		if err := db.ensureExecLog(ctx, e.Id, e.Attempt, e.Index); err != nil {
			return err
		}
		updateExpr = expression.Set(
			expression.Name(fmt.Sprintf("logs[%v].logs[%v].exit_code", e.Attempt, e.Index)),
			expressionValue(e.GetExitCode()),
		)

	case events.Type_EXECUTOR_STDOUT:
		item = &dynamodb.UpdateItemInput{
			TableName: aws.String(db.stdoutTable),
			Key: map[string]types.AttributeValue{
				"id": &types.AttributeValueMemberS{
					Value: e.Id,
				},
				"attempt_index": &types.AttributeValueMemberS{
					Value: fmt.Sprintf("%v-%v", e.Attempt, e.Index),
				},
			},
		}

		updateExpr = expression.Set(
			expression.Name("stdout"),
			expressionValue(e.GetStdout()),
		).Set(
			expression.Name("attempt"),
			expressionValue(e.Attempt),
		).Set(
			expression.Name("index"),
			expressionValue(e.Index),
		)

	case events.Type_EXECUTOR_STDERR:
		item = &dynamodb.UpdateItemInput{
			TableName: aws.String(db.stderrTable),
			Key: map[string]types.AttributeValue{
				"id": &types.AttributeValueMemberS{
					Value: e.Id,
				},
				"attempt_index": &types.AttributeValueMemberS{
					Value: fmt.Sprintf("%v-%v", e.Attempt, e.Index),
				},
			},
		}

		updateExpr = expression.Set(
			expression.Name("stderr"),
			expressionValue(e.GetStderr()),
		).Set(
			expression.Name("attempt"),
			expressionValue(e.Attempt),
		).Set(
			expression.Name("index"),
			expressionValue(e.Index),
		)

	case events.Type_SYSTEM_LOG:
		if err := db.ensureSysLog(ctx, e.Id, e.Attempt); err != nil {
			return err
		}

		item = &dynamodb.UpdateItemInput{
			TableName: aws.String(db.syslogsTable),
			Key: map[string]types.AttributeValue{
				"id": &types.AttributeValueMemberS{
					Value: e.Id,
				},
				"attempt": &types.AttributeValueMemberN{
					Value: strconv.Itoa(int(e.Attempt)),
				},
			},
		}

		updateExpr = expression.Set(
			expression.Name("system_logs"),
			expression.ListAppend(expression.Name("system_logs"), expressionValue([]string{e.SysLogString()})),
		)
	}

	expr, err := expression.NewBuilder().WithUpdate(updateExpr).Build()
	if err != nil {
		return err
	}

	item.ExpressionAttributeNames = expr.Names()
	item.ExpressionAttributeValues = expr.Values()
	item.UpdateExpression = expr.Update()
	item.ConditionExpression = expr.Condition()

	_, err = db.client.UpdateItem(ctx, item)
	return checkErrNotFound(err)
}

func (db *DynamoDB) ensureTaskLog(ctx context.Context, id string, attempt uint32) error {
	// create the log structure for the attempt if it doesnt already exist
	attemptItem := &dynamodb.UpdateItemInput{
		TableName: aws.String(db.taskTable),
		Key: map[string]types.AttributeValue{
			db.partitionKey: &types.AttributeValueMemberS{
				Value: db.partitionValue,
			},
			"id": &types.AttributeValueMemberS{
				Value: id,
			},
		},
		UpdateExpression: aws.String(fmt.Sprintf("SET logs[%v] = if_not_exists(logs[%v], :v)", attempt, attempt)),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":v": &types.AttributeValueMemberM{
				Value: map[string]types.AttributeValue{},
			},
		},
	}

	_, err := db.client.UpdateItem(ctx, attemptItem)
	return checkErrNotFound(err)
}

func (db *DynamoDB) ensureTaskLogMetadata(ctx context.Context, id string, attempt uint32) error {
	// create the log structure for the attempt if it doesnt already exist
	attemptItem := &dynamodb.UpdateItemInput{
		TableName: aws.String(db.taskTable),
		Key: map[string]types.AttributeValue{
			db.partitionKey: &types.AttributeValueMemberS{
				Value: db.partitionValue,
			},
			"id": &types.AttributeValueMemberS{
				Value: id,
			},
		},
		UpdateExpression: aws.String(fmt.Sprintf("SET logs[%v].metadata = if_not_exists(logs[%v].metadata, :v)", attempt, attempt)),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":v": &types.AttributeValueMemberM{
				Value: map[string]types.AttributeValue{},
			},
		},
	}

	_, err := db.client.UpdateItem(ctx, attemptItem)
	return checkErrNotFound(err)
}

func (db *DynamoDB) ensureExecLog(ctx context.Context, id string, attempt, index uint32) error {
	// create the log structure for the executor if it doesnt already exist
	indexItem := &dynamodb.UpdateItemInput{
		TableName: aws.String(db.taskTable),
		Key: map[string]types.AttributeValue{
			db.partitionKey: &types.AttributeValueMemberS{
				Value: db.partitionValue,
			},
			"id": &types.AttributeValueMemberS{
				Value: id,
			},
		},
		UpdateExpression: aws.String(fmt.Sprintf("SET logs[%v].logs[%v] = if_not_exists(logs[%v].logs[%v], :v)", attempt, index, attempt, index)),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":v": &types.AttributeValueMemberM{
				Value: map[string]types.AttributeValue{},
			},
		},
	}
	_, err := db.client.UpdateItem(ctx, indexItem)
	return checkErrNotFound(err)
}

func (db *DynamoDB) ensureSysLog(ctx context.Context, id string, attempt uint32) error {
	item := &dynamodb.UpdateItemInput{
		TableName: aws.String(db.syslogsTable),
		Key: map[string]types.AttributeValue{
			"id": &types.AttributeValueMemberS{
				Value: id,
			},
			"attempt": &types.AttributeValueMemberN{
				Value: strconv.Itoa(int(attempt)),
			},
		},
		UpdateExpression: aws.String("SET system_logs = if_not_exists(system_logs, :v)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":v": &types.AttributeValueMemberL{
				Value: []types.AttributeValue{},
			},
		},
	}

	_, err := db.client.UpdateItem(ctx, item)
	return checkErrNotFound(err)
}

func checkErrNotFound(err error) error {
	var aerr smithy.APIError
	if errors.As(err, &aerr) {
		if aerr.ErrorCode() == "ResourceNotFoundException" {
			return tes.ErrNotFound
		}
	}
	return err
}
