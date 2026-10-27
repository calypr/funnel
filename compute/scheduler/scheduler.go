// Package scheduler contains Funnel's builtin compute scheduler and node.
package scheduler

import (
	"errors"
	"fmt"
	"time"

	"context"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
)

// TaskQueue describes the interface the scheduler uses to find tasks that need scheduling.
type TaskQueue interface {
	ReadQueue(count int) []*tes.Task
}

// Scheduler handles scheduling tasks to nodes and support many backends.
type Scheduler struct {
	Conf  *config.Scheduler
	Log   *logger.Logger
	Nodes SchedulerServiceServer
	Queue TaskQueue
	Event events.Writer
}

// Run starts the scheduling loop. This blocks.
//
// The scheduler will take a chunk of tasks from the queue,
// request the the configured backend schedule them, and
// act on offers made by the backend.
func (s *Scheduler) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Duration(s.Conf.ScheduleRate.AsDuration()))
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			err := s.Schedule(ctx)
			if err != nil {
				return fmt.Errorf("schedule error: %s", err)
			}
		}
	}
}

// CheckNodes is used by the scheduler to check for dead/gone nodes.
// This is not an RPC endpoint
func (s *Scheduler) CheckNodes() error {
	ctx := context.Background()
	resp, err := s.Nodes.ListNodes(ctx, &ListNodesRequest{})

	if err != nil {
		return err
	}

	updated := UpdateNodeState(resp.Nodes, s.Conf)

	for _, node := range updated {
		var err error

		if node.State == NodeState_GONE {
			stateWriteFailed := false
			for _, tid := range node.TaskIds {
				if err := s.Event.WriteEvent(ctx, events.NewState(tid, tes.State_SYSTEM_ERROR)); err != nil {
					var transitionErr *tes.TransitionError
					if !errors.Is(err, tes.ErrNotFound) && !(errors.As(err, &transitionErr) && tes.TerminalState(transitionErr.From)) {
						stateWriteFailed = true
						s.Log.Error("writing dead-node task state", "taskID", tid, "error", err)
					}
				}
				if werr := s.Event.WriteEvent(ctx, events.NewSystemLog(tid, 0, 0, "info",
					"Cleaning up Task assigned to dead/gone node", map[string]string{
						"nodeID": node.Id,
					})); werr != nil {
					s.Log.Error("writing dead-node cleanup event", "taskID", tid, "error", werr)
				}
			}
			if stateWriteFailed {
				continue
			}
			_, err = s.Nodes.DeleteNode(ctx, node)
		} else {
			_, err = s.Nodes.PutNode(ctx, node)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

// Schedule does a scheduling iteration. It checks the health of nodes
// in the database, gets a chunk of tasks from the queue (configurable by config.ScheduleChunk),
// and calls the given scheduler backend. If the backend returns a valid offer, the
// task is assigned to the offered node.
func (s *Scheduler) Schedule(ctx context.Context) error {
	err := s.CheckNodes()
	if err != nil {
		s.Log.Error("Error checking nodes", err)
	}

	for _, task := range s.Queue.ReadQueue(int(s.Conf.ScheduleChunk)) {
		offer := s.GetOffer(task)
		if offer != nil {
			s.Log.Info("Assigning task to node",
				"taskID", task.Id,
				"nodeID", offer.Node.Id,
				"node", offer.Node,
			)
			if err := s.Event.WriteEvent(ctx, events.NewSystemLog(task.Id, 0, 0, "info",
				"Assigning task to node", map[string]string{
					"nodeID": offer.Node.Id,
				})); err != nil {
				s.Log.Error("writing assignment event", "taskID", task.Id, "error", err)
			}

			// The task's state is written INITIALIZING below, immediately upon
			// assignment — before the node has received it. Covered by
			// TestScheduleAssignsTaskAndMarksInitializing.
			offer.Node.TaskIds = append(offer.Node.TaskIds, task.Id)
			_, err = s.Nodes.PutNode(ctx, offer.Node)
			if err != nil {
				s.Log.Error("Error in AssignTask",
					"error", err,
					"taskID", task.Id,
					"nodeID", offer.Node.Id,
				)
				if werr := s.Event.WriteEvent(ctx, events.NewSystemLog(task.Id, 0, 0, "error",
					"Error in AssignTask", map[string]string{
						"error":  err.Error(),
						"nodeID": offer.Node.Id,
					})); werr != nil {
					s.Log.Error("writing AssignTask error event", "taskID", task.Id, "error", werr)
				}
				continue
			}

			err = s.Event.WriteEvent(ctx, events.NewState(task.Id, tes.State_INITIALIZING))
			if err != nil {
				s.Log.Error("Error marking task as initializing",
					"error", err,
					"taskID", task.Id,
					"nodeID", offer.Node.Id,
				)
			}
		} else {
			s.Log.Debug("Scheduling failed for task", "taskID", task.Id)
		}
	}
	return nil
}

// GetOffer returns an offer based on available funnel nodes.
func (s *Scheduler) GetOffer(j *tes.Task) *Offer {
	// Get the nodes from the funnel server
	nodes := []*Node{}
	resp, err := s.Nodes.ListNodes(context.Background(), &ListNodesRequest{})
	if err == nil {
		nodes = resp.Nodes
	}
	return DefaultScheduleAlgorithm(j, nodes, nil)
}
