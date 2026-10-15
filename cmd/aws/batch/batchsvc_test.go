package batch

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

func TestCreateResourcesReturnsDescribeErrors(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation string
		create    func(*batchsvc) error
	}{
		{
			name:      "compute environment",
			operation: "DescribeComputeEnvironments",
			create: func(svc *batchsvc) error {
				_, err := svc.CreateComputeEnvironment()
				return err
			},
		},
		{
			name:      "job queue",
			operation: "DescribeJobQueues",
			create: func(svc *batchsvc) error {
				_, err := svc.CreateJobQueue()
				return err
			},
		},
		{
			name:      "job definition",
			operation: "DescribeJobDefinitions",
			create: func(svc *batchsvc) error {
				_, err := svc.CreateJobDefinition(false)
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if operation := strings.TrimPrefix(r.URL.Path, "/v1/"); !strings.EqualFold(operation, test.operation) {
					t.Errorf("operation path = %q, want %q", r.URL.Path, test.operation)
				}
				w.Header().Set("Content-Type", "application/x-amz-json-1.1")
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"__type":"AccessDeniedException","message":"not authorized"}`)
			}))
			defer server.Close()

			conf := DefaultConfig()
			conf.ComputeEnv.Name = "fixture-compute"
			conf.JobQueue.Name = "fixture-queue"
			conf.JobDef.Name = "fixture-definition"
			svc := &batchsvc{
				sess: aws.Config{
					Region:       "us-east-1",
					BaseEndpoint: aws.String(server.URL),
					Credentials:  credentials.NewStaticCredentialsProvider("fixture", "secret", ""),
				},
				conf: conf,
			}

			err := test.create(svc)
			if err == nil || !strings.Contains(err.Error(), "not authorized") {
				t.Fatalf("create error = %v, want describe authorization error", err)
			}
			if got := requests.Load(); got != 1 {
				t.Fatalf("AWS requests = %d, want only the failed describe", got)
			}
		})
	}
}

func TestNewBatchSvcPreservesRetryAttempts(t *testing.T) {
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_MAX_ATTEMPTS", "")
	for _, test := range []struct {
		name       string
		maxRetries int32
		want       int
	}{
		{name: "legacy default", want: 4},
		{name: "configured retries", maxRetries: 7, want: 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			conf := DefaultConfig()
			conf.AWSConfig.Region = "us-east-1"
			conf.AWSConfig.DisableAutoCredentialLoad = true
			conf.AWSConfig.MaxRetries = test.maxRetries
			svc, err := newBatchSvc(conf)
			if err != nil {
				t.Fatal(err)
			}
			if svc.sess.RetryMaxAttempts != test.want {
				t.Fatalf("retry attempts = %d, want %d", svc.sess.RetryMaxAttempts, test.want)
			}
		})
	}
}
