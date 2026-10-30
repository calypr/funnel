package dynamodb

import (
	"errors"
	"fmt"

	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
)

// GetTask gets a task, which describes a running task
func (db *DynamoDB) GetTask(ctx context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	var task *tes.Task
	var response *dynamodb.GetItemOutput
	var err error

	switch req.View {
	case tes.View_MINIMAL.String():
		response, err = db.getMinimalView(ctx, req.Id)
	case tes.View_BASIC.String():
		response, err = db.getBasicView(ctx, req.Id)
	case tes.View_FULL.String():
		response, err = db.getFullView(ctx, req.Id)
	}
	if err != nil {
		return nil, err
	}
	if response.Item == nil {
		return nil, tes.ErrNotFound
	}
	if !isAccessible(ctx, response) {
		return nil, tes.ErrNotPermitted
	}

	err = attributevalue.UnmarshalMapWithOptions(response.Item, &task, decodeJSONTags)
	if err != nil {
		return nil, fmt.Errorf("failed to DynamoDB unmarshal Task, %v", err)
	}

	return task, nil
}

// ListTasks returns a list of taskIDs
func (db *DynamoDB) ListTasks(ctx context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {

	filters := []expression.ConditionBuilder{}

	if userInfo := server.GetUser(ctx); !userInfo.CanSeeAllTasks() {
		filters = append(filters, expression.Name("owner").Equal(expressionValue(userInfo.Username)))
	}

	if req.State != tes.Unknown {
		num := int(req.State)
		filters = append(filters, expression.Name("state").Equal(expressionValue(num)))
	}

	for k, v := range req.GetTags() {
		var fieldValue expression.ValueBuilder
		if v == "" {
			fieldValue = expressionValue(expression.Null)
		} else {
			fieldValue = expressionValue(v)
		}
		filters = append(filters, expression.Name("tags."+k).Equal(fieldValue))
	}

	exp := expression.NewBuilder().
		WithKeyCondition(expression.Key(db.partitionKey).Equal(expressionValue(db.partitionValue)))

	if req.View == tes.View_MINIMAL.String() {
		exp = exp.WithProjection(expression.NamesList(expression.Name("id"), expression.Name("state")))
	}

	if len(filters) == 1 {
		exp.WithFilter(filters[0])
	} else if len(filters) > 1 {
		exp.WithFilter(expression.And(filters[0], filters[1], filters[2:]...))
	}

	eb, err := exp.Build()
	if err != nil {
		return nil, err
	}

	pageSize := tes.GetPageSize(req.GetPageSize())
	query := &dynamodb.QueryInput{
		TableName:                 aws.String(db.taskTable),
		Limit:                     aws.Int32(int32(pageSize)),
		ScanIndexForward:          aws.Bool(false),
		ConsistentRead:            aws.Bool(true),
		KeyConditionExpression:    eb.KeyCondition(),
		ExpressionAttributeNames:  eb.Names(),
		ExpressionAttributeValues: eb.Values(),
		FilterExpression:          eb.Filter(),
		ProjectionExpression:      eb.Projection(),
	}

	if req.PageToken != "" {
		query.ExclusiveStartKey = map[string]types.AttributeValue{
			db.partitionKey: &types.AttributeValueMemberS{
				Value: db.partitionValue,
			},
			"id": &types.AttributeValueMemberS{
				Value: req.PageToken,
			},
		}
	}

	page := tes.NewTaskPage(ctx)
	accepted := 0
	var more bool
	for {
		query.Limit = aws.Int32(int32(pageSize - accepted))
		response, err := db.client.Query(ctx, query)
		if err != nil {
			return nil, err
		}
		for _, item := range response.Items {
			if req.View == tes.View_FULL.String() {
				var errs []error
				if err := db.getContent(ctx, item); err != nil {
					errs = append(errs, err)
				}
				if err := db.getExecutorOutput(ctx, item, "stdout", db.stdoutTable); err != nil {
					errs = append(errs, err)
				}
				if err := db.getExecutorOutput(ctx, item, "stderr", db.stderrTable); err != nil {
					errs = append(errs, err)
				}
				if err := db.getSystemLogs(ctx, item); err != nil {
					errs = append(errs, err)
				}
				if err := errors.Join(errs...); err != nil {
					return nil, fmt.Errorf("dynamodb loading task detail: %w", err)
				}
			}
			var task tes.Task
			if err := attributevalue.UnmarshalMapWithOptions(item, &task, decodeJSONTags); err != nil {
				return nil, fmt.Errorf("failed to DynamoDB unmarshal Task: %w", err)
			}
			added, err := page.Add(&task, task.Id)
			if err != nil {
				return nil, err
			}
			if !added {
				more = true
				break
			}
			accepted++
		}
		if more || len(response.LastEvaluatedKey) == 0 {
			break
		}
		if accepted == pageSize {
			more = true
			break
		}
		query.ExclusiveStartKey = response.LastEvaluatedKey
	}
	return page.Finish(more), nil
}
