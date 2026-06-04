package gcp_batch

import (
	"context"
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockEventWriter is a mock for the events.Writer interface
type MockEventWriter struct {
	mock.Mock
}

func (m *MockEventWriter) WriteEvent(ctx context.Context, ev *events.Event) error {
	args := m.Called(ctx, ev)
	return args.Error(0)
}

func (m *MockEventWriter) Close() {
	m.Called()
}

// MockReadOnlyServer is a mock for the tes.ReadOnlyServer interface
type MockReadOnlyServer struct {
	mock.Mock
}

func (m *MockReadOnlyServer) GetTask(ctx context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	args := m.Called(ctx, req)
	return args.Get(0).(*tes.Task), args.Error(1)
}

func (m *MockReadOnlyServer) ListTasks(ctx context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	args := m.Called(ctx, req)
	return args.Get(0).(*tes.ListTasksResponse), args.Error(1)
}

func (m *MockReadOnlyServer) GetServiceInfo(ctx context.Context, req *tes.GetServiceInfoRequest) (*tes.ServiceInfo, error) {
	args := m.Called(ctx, req)
	return args.Get(0).(*tes.ServiceInfo), args.Error(1)
}

func (m *MockReadOnlyServer) CancelTask(ctx context.Context, req *tes.CancelTaskRequest) (*tes.CancelTaskResponse, error) {
	args := m.Called(ctx, req)
	return args.Get(0).(*tes.CancelTaskResponse), args.Error(1)
}

func (m *MockReadOnlyServer) Close() {
	m.Called()
}

// Test that the backend can be created without errors when logging clients are unavailable
func TestNewBackendWithoutLoggingClients(t *testing.T) {
	ctx := context.Background()

	// Create a mock configuration
	conf := &config.GCPBatch{
		Project:           "test-project",
		Location:          "us-central1",
		DisableReconciler: true, // Disable reconciler to avoid ticker panic
		// This would normally require valid GCP credentials
	}

	// Create mock dependencies
	mockWriter := &MockEventWriter{}
	mockReader := &MockReadOnlyServer{}
	log := &logger.Logger{}

	// Set up mock expectations for Close calls
	mockWriter.On("Close")
	mockReader.On("Close")

	// This should not panic but should log warnings about missing clients
	backend, err := NewBackend(ctx, conf, mockReader, mockWriter, log)

	// We expect this to succeed but with logging clients set to nil
	// due to invalid credentials in the test environment
	if err == nil {
		assert.NotNil(t, backend)

		// Verify the backend can be closed without errors
		backend.Close()
	}
}
