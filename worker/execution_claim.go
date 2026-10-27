package worker

import "github.com/calypr/funnel/events"

// ExecutionClaimer atomically admits one worker to execute a task.
type ExecutionClaimer = events.ExecutionClaimer
