package scheduler

import "time"

// idleTimeout tracks whether a node has remained idle long enough to shut down.
type idleTimeout interface {
	Done() <-chan time.Time
	Start()
	Stop()
}

// noTimeout never times out.
// It has no fields and doesn't need to do anything except provide the idleTimeout interface.
type noTimeout struct{}

func (*noTimeout) Done() <-chan time.Time {
	return nil
}
func (*noTimeout) Start() {
	// return
}
func (*noTimeout) Stop() {
	// return
}

// noIdleTimeout creates an idleTimeout that never times out.
func noIdleTimeout() idleTimeout {
	return new(noTimeout)
}

type timerTimeout struct {
	timeout time.Duration
	timer   *time.Timer
	started bool
}

// Done returns a channel which can be used to wait for the timeout.
func (t *timerTimeout) Done() <-chan time.Time {
	if t.timer != nil {
		return t.timer.C
	}
	return nil
}

// Start resets and starts the timer.
func (t *timerTimeout) Start() {
	if !t.started {
		t.timer = time.NewTimer(t.timeout)
		t.started = true
	}
}

// Stop stops the idle timer and cleans up resources.
func (t *timerTimeout) Stop() {
	if !t.started {
		return
	}
	t.timer.Stop()
	t.started = false
}

// newIdleTimeout returns an idleTimeout instance for the given duration.
func newIdleTimeout(d time.Duration) idleTimeout {
	if d < 0 {
		return noIdleTimeout()
	}
	return &timerTimeout{d, nil, false}
}
