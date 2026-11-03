package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	"github.com/stretchr/testify/mock"
)

type nodeRecoveryWriter struct {
	fail  bool
	state tes.State
	err   error
}

func (w *nodeRecoveryWriter) WriteEvent(_ context.Context, event *events.Event) error {
	if event.Type == events.Type_TASK_STATE {
		if w.err != nil {
			return w.err
		}
		if w.fail {
			return errors.New("database unavailable")
		}
		w.state = event.GetState()
	}
	return nil
}

func TestGoneNodeRemovedForTerminalOrDeletedTask(t *testing.T) {
	for _, err := range []error{&tes.TransitionError{From: tes.Complete, To: tes.SystemError}, &tes.TransitionError{From: tes.Canceled, To: tes.SystemError}, tes.ErrNotFound} {
		t.Run(err.Error(), func(t *testing.T) {
			node := &Node{Id: "dead-node", State: NodeState_GONE, TaskIds: []string{"task-1"}}
			s := newTestScheduler([]*Node{node}, &queueStub{}, &nodeRecoveryWriter{err: err})
			nodes := s.Nodes.(*MockSchedulerServiceServer)
			nodes.On("DeleteNode", mock.Anything, mock.Anything).Return(nil, nil)
			if err := s.CheckNodes(); err != nil {
				t.Fatal(err)
			}
			nodes.AssertNumberOfCalls(t, "DeleteNode", 1)
		})
	}
}
func (*nodeRecoveryWriter) Close() {}

func TestGoneNodeRetainedUntilTaskFailureIsRecorded(t *testing.T) {
	node := &Node{Id: "dead-node", State: NodeState_GONE, TaskIds: []string{"task-1"}}
	writer := &nodeRecoveryWriter{fail: true}
	s := newTestScheduler([]*Node{node}, &queueStub{}, writer)
	nodes := s.Nodes.(*MockSchedulerServiceServer)
	nodes.On("DeleteNode", mock.Anything, mock.Anything).Return(nil, nil)
	err := s.CheckNodes()
	if err != nil {
		t.Fatal(err)
	}
	nodes.AssertNotCalled(t, "DeleteNode", mock.Anything, mock.Anything)
	writer.fail = false
	err = s.CheckNodes()
	if err != nil {
		t.Fatal(err)
	}
	if writer.state != tes.State_SYSTEM_ERROR {
		t.Fatalf("state = %s, want SYSTEM_ERROR", writer.state)
	}
	nodes.AssertNumberOfCalls(t, "DeleteNode", 1)
}
