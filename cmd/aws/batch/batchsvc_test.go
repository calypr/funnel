package batch

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

func TestCreateJobDefinitionResourceRequirements(t *testing.T) {
	var properties map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch strings.ToLower(r.URL.Path) {
		case "/v1/registerjobdefinition":
			var request struct {
				ContainerProperties map[string]json.RawMessage `json:"containerProperties"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			properties = request.ContainerProperties
			_, _ = io.WriteString(w, `{"jobDefinitionName":"test","jobDefinitionArn":"arn:example","revision":1}`)
		case "/v1/describejobdefinitions":
			_, _ = io.WriteString(w, `{"jobDefinitions":[{"jobDefinitionName":"test","jobDefinitionArn":"arn:example","revision":1,"type":"container"}]}`)
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	conf := DefaultConfig()
	conf.JobDef.JobRoleArn = "arn:example"
	conf.JobDef.MemoryMiB = 8192
	conf.JobDef.VCPUs = 4
	svc := &batchsvc{sess: aws.Config{Region: "us-east-1", BaseEndpoint: aws.String(server.URL), Credentials: credentials.NewStaticCredentialsProvider("fixture", "secret", "")}, conf: conf}
	if _, err := svc.CreateJobDefinition(true); err != nil {
		t.Fatal(err)
	}
	var command []string
	if err := json.Unmarshal(properties["command"], &command); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(command, " "), "--Compute aws-batch") {
		t.Fatalf("worker command omits compute identity: %v", command)
	}
	if _, ok := properties["memory"]; ok {
		t.Fatal("deprecated memory field was sent")
	}
	if _, ok := properties["vcpus"]; ok {
		t.Fatal("deprecated vcpus field was sent")
	}
	var requirements []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(properties["resourceRequirements"], &requirements); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, requirement := range requirements {
		values[requirement.Type] = requirement.Value
	}
	if !reflect.DeepEqual(values, map[string]string{"MEMORY": "8192", "VCPU": "4"}) {
		t.Fatalf("unexpected resource requirements: %v", values)
	}
}

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
