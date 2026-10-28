// Package scheduler contains Funnel's builtin compute scheduler and node.
package scheduler

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"context"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TaskQueue returns queued tasks in a stable order, starting strictly after the cursor.
type TaskQueue interface {
	ReadQueue(count int, after *tes.Task) []*tes.Task
}

// AtomicTaskAssigner claims a queued task and reserves it on a node in one
// database transaction. The candidate comes from the scheduler's current list;
// implementations must check that node's current eligibility before
// reserving it. It returns the owner, or an empty string when no assignment is
// made. A nil candidate only looks up an existing assignment. An error must
// not leave a new claim or node reservation committed.
type AtomicTaskAssigner interface {
	TryAssign(ctx context.Context, task *tes.Task, candidate *Node) (string, error)
}

// NodeHealthStore fences a health transition against the observed heartbeat.
type NodeHealthStore interface {
	SetNodeState(context.Context, *Node) (*Node, error)
}

// Scheduler handles scheduling tasks to nodes and support many backends.
type Scheduler struct {
	Conf           *config.Scheduler
	Log            *logger.Logger
	Nodes          SchedulerServiceServer
	Queue          TaskQueue
	AtomicAssigner AtomicTaskAssigner
	Event          events.Writer
	queueCursor    *tes.Task
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
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("schedule error: %s", err)
			}
		}
	}
}

// CheckNodes is used by the scheduler to check for dead/gone nodes.
// This is not an RPC endpoint
func (s *Scheduler) CheckNodes() error {
	ctx := context.Background()
	nodes, err := ListAllNodes(ctx, s.Nodes)

	if err != nil {
		return err
	}

	return s.checkNodes(ctx, nodes)
}

func (s *Scheduler) checkNodes(ctx context.Context, nodes []*Node) error {
	updated := UpdateNodeState(nodes, s.Conf)

	for _, node := range updated {
		var err error
		health, ok := s.Nodes.(NodeHealthStore)
		if !ok {
			return fmt.Errorf("scheduler requires heartbeat-fenced node state updates")
		}
		node, err = health.SetNodeState(ctx, node)
		if errors.Is(err, ErrNodeChanged) {
			continue
		}
		if err != nil {
			return err
		}

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
		}

		if errors.Is(err, ErrNodeChanged) {
			continue
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
	if s.AtomicAssigner == nil {
		return fmt.Errorf("scheduler requires atomic task assignment")
	}
	nodes, err := s.readNodeCandidates(ctx)
	if err != nil {
		s.Log.Error("Error reading scheduling candidates", err)
		return nil
	}
	if err := s.checkNodes(ctx, nodes); err != nil {
		s.Log.Error("Error checking nodes", err)
	}
	candidates := newNodeCandidates(nodes)

	count := int(s.Conf.ScheduleChunk)
	if count <= 0 {
		return nil
	}
	// Continue through the queue in order and restart after reaching its end.
	tasks := s.Queue.ReadQueue(count, s.queueCursor)
	if len(tasks) < count {
		s.queueCursor = nil
	} else {
		s.queueCursor = tasks[len(tasks)-1]
	}
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.scheduleTask(ctx, task, candidates)
	}
	return nil
}

// scheduleTask uses a backend transaction to reserve task and node
// ownership together. This path never performs a separate node write.
func (s *Scheduler) scheduleTask(ctx context.Context, task *tes.Task, candidates *nodeCandidates) {
	// The durable claim is authoritative, including when its owner disappeared.
	ownerID, err := s.AtomicAssigner.TryAssign(ctx, task, nil)
	if err != nil {
		s.logAssignTaskFailure(ctx, task.Id, "", err)
		return
	}
	alreadyAssigned := ownerID != ""
	if !alreadyAssigned {

		offer := DefaultScheduleAlgorithm(task, candidates.nodes, nil)
		if offer == nil {
			return
		}
		ownerID, err = s.AtomicAssigner.TryAssign(ctx, task, offer.Node)
		if err != nil {
			s.logAssignTaskFailure(ctx, task.Id, offer.Node.Id, err)
			s.refreshCandidates(ctx, candidates, offer.Node.Id)
			return
		}
		if ownerID == "" {
			return
		}
	}

	node, err := s.Nodes.GetNode(ctx, &GetNodeRequest{Id: ownerID})
	if err != nil {
		candidates.replace(ownerID, nil)
		if status.Code(err) == codes.NotFound {
			s.failClaimedTask(ctx, task.Id, ownerID, "claimed node is missing")
		} else {
			s.Log.Error("Error retrieving claimed task node", "taskID", task.Id, "nodeID", ownerID, "error", err)
		}
		return
	}
	candidates.replace(ownerID, node)
	if node == nil {
		s.Log.Error("Claimed task node lookup returned no node", "taskID", task.Id, "nodeID", ownerID)
		return
	}
	if node.State == NodeState_GONE {
		s.failClaimedTask(ctx, task.Id, ownerID, "claimed node is gone")
		return
	}
	if !slices.Contains(node.TaskIds, task.Id) {
		// The transaction should either have found an existing assignment or
		// reserved the task on the returned owner. Do not repair this with a
		// separate node write, which would break atomic ownership.
		s.logAssignmentConflict(ctx, task.Id, "atomic assignment returned an owner without a node reservation", map[string]string{
			"claimedNodeID": ownerID,
		})
		return
	}

	if !alreadyAssigned {
		s.logTaskAssignment(ctx, task, node)
	}
	s.writeInitializing(ctx, task.Id, node.Id)
}

func (s *Scheduler) logTaskAssignment(ctx context.Context, task *tes.Task, node *Node) {
	s.Log.Info("Assigning task to node", "taskID", task.Id, "nodeID", node.Id, "node", node)
	if err := s.Event.WriteEvent(ctx, events.NewSystemLog(task.Id, 0, 0, "info",
		"Assigning task to node", map[string]string{"nodeID": node.Id})); err != nil {
		s.Log.Error("writing assignment event", "taskID", task.Id, "error", err)
	}
}

func (s *Scheduler) logAssignTaskFailure(ctx context.Context, taskID, nodeID string, cause error) {
	s.Log.Error("Error in AssignTask", "error", cause, "taskID", taskID, "nodeID", nodeID)
	if err := s.Event.WriteEvent(ctx, events.NewSystemLog(taskID, 0, 0, "error",
		"Error in AssignTask", map[string]string{
			"error":  cause.Error(),
			"nodeID": nodeID,
		})); err != nil {
		s.Log.Error("writing AssignTask error event", "taskID", taskID, "error", err)
	}
}

func (s *Scheduler) writeInitializing(ctx context.Context, taskID, nodeID string) {
	if err := s.Event.WriteEvent(ctx, events.NewState(taskID, tes.State_INITIALIZING)); err != nil {
		s.Log.Error("Error marking task as initializing", "error", err, "taskID", taskID, "nodeID", nodeID)
	}
}

// failClaimedTask marks a task failed when its immutable owner can no longer
// receive it. A missing or gone owner cannot safely be replaced with another
// node because the durable claim may already have been observed elsewhere.
func (s *Scheduler) failClaimedTask(ctx context.Context, taskID, nodeID, reason string) {
	if err := s.Event.WriteEvent(ctx, events.NewSystemLog(taskID, 0, 0, "error",
		"Task cannot be assigned to its claimed node", map[string]string{
			"nodeID": nodeID,
			"error":  reason,
		})); err != nil {
		s.Log.Error("writing claimed-node error event", "taskID", taskID, "nodeID", nodeID, "error", err)
	}
	if err := s.Event.WriteEvent(ctx, events.NewState(taskID, tes.State_SYSTEM_ERROR)); err != nil {
		s.Log.Error("marking task with unavailable claimed node as system error", "taskID", taskID, "nodeID", nodeID, "error", err)
	}
}

func (s *Scheduler) logAssignmentConflict(ctx context.Context, taskID, message string, fields map[string]string) {
	s.Log.Error(message, "taskID", taskID, "details", fields)
	if err := s.Event.WriteEvent(ctx, events.NewSystemLog(taskID, 0, 0, "error", message, fields)); err != nil {
		s.Log.Error("writing assignment conflict event", "taskID", taskID, "error", err)
	}
}

// GetOffer returns an offer based on available funnel nodes.
func (s *Scheduler) GetOffer(j *tes.Task) *Offer {
	// Get the nodes from the funnel server
	nodes := []*Node{}
	allNodes, err := ListAllNodes(context.Background(), s.Nodes)
	if err == nil {
		nodes = allNodes
	}
	return DefaultScheduleAlgorithm(j, nodes, nil)
}
