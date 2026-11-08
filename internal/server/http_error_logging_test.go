package server

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/funnel/internal/logger"
	logconfig "github.com/calypr/funnel/logger"
)

func TestHTTPErrorLogOmitsRequestAndErrorPayloads(t *testing.T) {
	const secret = "HTTP_ERROR_PAYLOAD_SENTINEL"
	var output bytes.Buffer
	log := logger.NewLogger("http-error-test", logconfig.DefaultConfig())
	log.SetOutput(&output)
	server := &Server{Log: log}
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/v1/tasks?token="+secret, nil)
	server.handleError(response, request, secret, 422)
	if response.Code != 422 || !strings.Contains(response.Body.String(), secret) {
		t.Fatal("HTTP error handling changed the client response")
	}
	if strings.Contains(output.String(), secret) {
		t.Fatal("request or error payload reached application logs")
	}
	if !strings.Contains(output.String(), "GET") || !strings.Contains(output.String(), "422") {
		t.Fatal("HTTP error log omitted method or status")
	}
}
