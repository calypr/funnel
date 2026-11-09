package dynamodb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsdb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/calypr/funnel/tes"
)

func TestListTasksByteBudgetResumes(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request struct{ ExclusiveStartKey map[string]struct{ S string } }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			return nil, err
		}
		start := request.ExclusiveStartKey["id"].S
		ids := []string{"c", "b", "a"}
		var items []string
		for _, id := range ids {
			if start == "" || id < start {
				items = append(items, fmt.Sprintf(`{"id":{"S":%q},"state":{"N":"5"},"name":{"S":%q}}`, id, strings.Repeat("x", 50)))
			}
		}
		body := `{"Items":[` + strings.Join(items, ",") + `],"Count":` + fmt.Sprint(len(items)) + `}`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/x-amz-json-1.0"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	cfg := aws.Config{BaseEndpoint: aws.String("http://dynamodb.test"), Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("test", "secret", ""), HTTPClient: client, RetryMaxAttempts: 1}
	db := &DynamoDB{client: awsdb.NewFromConfig(cfg), partitionKey: "hid", partitionValue: "0", taskTable: "tasks"}
	ctx := tes.WithTaskPageBytes(context.Background(), 90)
	token := ""
	for _, want := range []string{"c", "b", "a"} {
		result, err := db.ListTasks(ctx, &tes.ListTasksRequest{View: "BASIC", PageSize: 3, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Tasks) != 1 || result.Tasks[0].Id != want {
			t.Fatalf("expected %s, got %v", want, result)
		}
		token = result.NextPageToken
	}
	if token != "" {
		t.Fatalf("unexpected final token: %q", token)
	}
}
