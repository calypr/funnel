package dynamodb

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/tes"
)

func fixtureDB(t *testing.T, request func(string, map[string]json.RawMessage) string) *DynamoDB {
	t.Helper()
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var input map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			return nil, err
		}
		body := request(strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "DynamoDB_20120810."), input)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/x-amz-json-1.0"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	return &DynamoDB{
		client:       dynamodb.NewFromConfig(aws.Config{Region: "local", BaseEndpoint: aws.String("http://fixture.invalid"), Credentials: credentials.NewStaticCredentialsProvider("fixture", "secret", ""), HTTPClient: client, RetryMaxAttempts: 1}),
		partitionKey: "hid", partitionValue: "0", taskTable: "tasks", contentTable: "content", stdoutTable: "stdout", stderrTable: "stderr", syslogsTable: "syslogs",
	}
}

func TestDynamoDBTaskWireFormat(t *testing.T) {
	var stored json.RawMessage
	var contentWrites int
	db := fixtureDB(t, func(operation string, request map[string]json.RawMessage) string {
		switch operation {
		case "PutItem":
			if string(request["TableName"]) == `"tasks"` {
				stored = request["Item"]
				var item map[string]json.RawMessage
				if err := json.Unmarshal(stored, &item); err != nil {
					t.Fatal(err)
				}
				if string(item["id"]) != `{"S":"task-1"}` || string(item["creation_time"]) != `{"S":"2026-10-15T00:00:00Z"}` {
					t.Fatalf("incompatible task record %s", stored)
				}
				if string(item["tags"]) != `{"M":{"empty":{"NULL":true}}}` {
					t.Fatalf("empty tag encoding changed %s", item["tags"])
				}
				if _, present := item["Id"]; present {
					t.Fatal("SDK wrote Go field names")
				}
			} else {
				contentWrites++
				if !strings.Contains(string(request["Item"]), `"content":{"S":"inline content"}`) {
					t.Fatalf("input content changed %s", request["Item"])
				}
			}
			return `{}`
		case "GetItem":
			return `{"Item":` + string(stored) + `}`
		default:
			t.Fatalf("unexpected operation %s", operation)
			return `{}`
		}
	})
	task := &tes.Task{Id: "task-1", State: tes.Queued, CreationTime: "2026-10-15T00:00:00Z", Tags: map[string]string{"empty": ""}, Resources: &tes.Resources{CpuCores: 2, RamGb: 4}, Inputs: []*tes.Input{{Path: "/input", Content: "inline content"}}}
	if err := db.WriteEvent(t.Context(), events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetTask(t.Context(), &tes.GetTaskRequest{Id: task.Id, View: tes.View_BASIC.String()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Id != task.Id || got.CreationTime != task.CreationTime || got.Resources.CpuCores != 2 || contentWrites != 1 {
		t.Fatalf("task round trip changed %v, content writes %d", got, contentWrites)
	}
}

func TestDynamoDBOutputEventWireFormat(t *testing.T) {
	var updates int
	db := fixtureDB(t, func(operation string, request map[string]json.RawMessage) string {
		if operation != "UpdateItem" {
			t.Fatalf("unexpected operation %s", operation)
		}
		updates++
		if updates == 2 {
			values := string(request["ExpressionAttributeValues"])
			for _, expected := range []string{`"url":{"S":"s3://bucket/file"}`, `"size_bytes":{"S":"42"}`, `"path":{"S":"/output"}`} {
				if !strings.Contains(values, expected) {
					t.Fatalf("output event lost JSON field names %s", values)
				}
			}
		}
		return `{}`
	})
	err := db.WriteEvent(t.Context(), events.NewOutputs("task-1", 0, []*tes.OutputFileLog{{Url: "s3://bucket/file", Path: "/output", SizeBytes: "42"}}))
	if err != nil {
		t.Fatal(err)
	}
	if updates != 2 {
		t.Fatalf("updates = %d, want initialize and append", updates)
	}
}

func TestDynamoDBFullViewPagination(t *testing.T) {
	var contentPages int
	var queries int
	db := fixtureDB(t, func(operation string, request map[string]json.RawMessage) string {
		if operation == "GetItem" {
			return `{"Item":{"id":{"S":"task-1"},"inputs":{"L":[{"M":{"path":{"S":"/one"}}},{"M":{"path":{"S":"/two"}}}]},"logs":{"L":[{"M":{"logs":{"L":[{"M":{}}]}}}]}}}`
		}
		if operation != "Query" {
			t.Fatalf("unexpected operation %s", operation)
		}
		queries++
		if string(request["ConsistentRead"]) != "true" {
			t.Fatal("full view query lost consistent reads")
		}
		switch string(request["TableName"]) {
		case `"content"`:
			contentPages++
			if contentPages == 1 {
				return `{"Items":[{"index":{"N":"0"},"content":{"S":"first"}}],"LastEvaluatedKey":{"id":{"S":"task-1"},"index":{"N":"0"}}}`
			}
			if len(request["ExclusiveStartKey"]) == 0 {
				t.Error("missing content pagination key")
			}
			return `{"Items":[{"index":{"N":"1"},"content":{"S":"second"}}]}`
		case `"stdout"`, `"stderr"`:
			field := strings.Trim(string(request["TableName"]), `"`)
			return fmt.Sprintf(`{"Items":[{"index":{"N":"0"},"attempt":{"N":"0"},"%s":{"S":"%s text"}}]}`, field, field)
		case `"syslogs"`:
			return `{"Items":[{"attempt":{"N":"0"},"system_logs":{"L":[{"S":"system text"}]}}]}`
		default:
			t.Fatalf("unexpected table %s", request["TableName"])
			return `{}`
		}
	})
	task, err := db.GetTask(t.Context(), &tes.GetTaskRequest{Id: "task-1", View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if queries != 5 || contentPages != 2 || task.Inputs[0].Content != "first" || task.Inputs[1].Content != "second" || task.Logs[0].Logs[0].Stdout != "stdout text" || task.Logs[0].Logs[0].Stderr != "stderr text" || task.Logs[0].SystemLogs[0] != "system text" {
		t.Fatalf("incomplete full view %v, queries %d", task, queries)
	}
}
