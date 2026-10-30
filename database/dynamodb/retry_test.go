package dynamodb

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/calypr/funnel/config"
)

func TestDefaultRetryAttemptsPreserveDynamoDBRetries(t *testing.T) {
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_MAX_ATTEMPTS", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) <= 3 {
			w.Header().Set("Content-Type", "application/x-amz-json-1.0")
			w.WriteHeader(http.StatusBadRequest)
			_, err := io.WriteString(w, `{"__type":"com.amazonaws.dynamodb.v20120810#ThrottlingException","message":"throttled"}`)
			if err != nil {
				t.Errorf("write throttling response: %v", err)
			}
			return
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		_, err := io.WriteString(w, `{"TableNames":[]}`)
		if err != nil {
			t.Errorf("write successful response: %v", err)
		}
	}))
	defer server.Close()

	db, err := NewDynamoDB(&config.DynamoDB{
		TableBasename: "fixture",
		AWSConfig: &config.AWSConfig{
			Endpoint: server.URL,
			Region:   "us-east-1",
			Key:      "fixture",
			Secret:   "secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := db.client.Options().RetryMaxAttempts; got != 11 {
		t.Fatalf("configured retry attempts = %d, want 11", got)
	}
	if _, err := db.client.ListTables(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 4 {
		t.Fatalf("request attempts = %d, want 4 after three throttling responses", got)
	}
}
