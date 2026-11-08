package scheduler

import (
	"errors"
	"sync"
	"time"
)

var errRunSetWaitTimedOut = errors.New("runSet.Wait() timed out")

func newRunSet() *runSet {
	return &runSet{
		runners: make(map[string]struct{}),
	}
}

// runSet tracks a set of concurrent goroutines by ID.
// Used by the node service to track a set of running tasks by task ID.
type runSet struct {
	wg      sync.WaitGroup
	mtx     sync.Mutex
	runners map[string]struct{}
}

// Add tries to add an ID to the set and returns true if it was added,
// false if it already existed.
func (r *runSet) Add(id string) bool {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	// Only add the ID if it doesn't already exist.
	if _, ok := r.runners[id]; !ok {
		r.runners[id] = struct{}{}
		r.wg.Add(1)
		return true
	}
	return false
}

// Remove removes the given ID from the set.
func (r *runSet) Remove(id string) {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	// Only remove if the ID exists in the set.
	if _, ok := r.runners[id]; ok {
		r.wg.Done()
		delete(r.runners, id)
	}
}

// Wait for all runners to exit.
func (r *runSet) Wait(timeout time.Duration) error {
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return errRunSetWaitTimedOut
	}
}

// Count returns the number of runners currently running.
func (r *runSet) Count() int {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	return len(r.runners)
}
