package taskservice

import (
	"testing"
	"time"
)

func TestCloseWaitsForSubmissionsAndCanBeRepeated(t *testing.T) {
	service := &Service{}
	service.submitWG.Add(1)
	done := make(chan struct{})
	go func() { service.Close(); close(done) }()
	select {
	case <-done:
		t.Fatal("Close returned before the submission finished")
	case <-time.After(20 * time.Millisecond):
	}
	service.submitWG.Done()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after the submission")
	}
	service.Close()
}
