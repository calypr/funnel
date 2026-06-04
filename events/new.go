package events

import (
	"time"

	"github.com/ohsu-comp-bio/funnel/tes"
)

// NewTaskCreated creates a state change event.
func NewTaskCreated(task *tes.Task) *Event {
	return &Event{
		Id:        task.Id,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Type:      Type_TASK_CREATED,
		Data: &Event_Task{
			Task: task,
		},
	}
}

// NewState creates a state change event.
func NewState(taskID string, s tes.State) *Event {
	return &Event{
		Id:        taskID,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Type:      Type_TASK_STATE,
		Data: &Event_State{
			State: s,
		},
	}
}

// NewStartTime creates a task start time event.
func NewStartTime(taskID string, attempt uint32, t time.Time) *Event {
	return &Event{
		Id:        taskID,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Type:      Type_TASK_START_TIME,
		Attempt:   attempt,
		Data: &Event_StartTime{
			StartTime: t.Format(time.RFC3339Nano),
		},
	}
}

// NewEndTime creates a task end time event.
func NewEndTime(taskID string, attempt uint32, t time.Time) *Event {
	return &Event{
		Id:        taskID,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Type:      Type_TASK_END_TIME,
		Attempt:   attempt,
		Data: &Event_EndTime{
			EndTime: t.Format(time.RFC3339Nano),
		},
	}
}

// NewOutputs creates a task output file log event.
func NewOutputs(taskID string, attempt uint32, f []*tes.OutputFileLog) *Event {
	return &Event{
		Id:        taskID,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Type:      Type_TASK_OUTPUTS,
		Attempt:   attempt,
		Data: &Event_Outputs{
			Outputs: &Outputs{
				Value: f,
			},
		},
	}
}

// NewMetadata creates a task metadata log event.
func NewMetadata(taskID string, attempt uint32, m map[string]string) *Event {
	return &Event{
		Id:        taskID,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Type:      Type_TASK_METADATA,
		Attempt:   attempt,
		Data: &Event_Metadata{
			Metadata: &Metadata{
				Value: m,
			},
		},
	}
}

// NewExecutorStartTime creates an executor start time event
// for the executor at the given index.
func NewExecutorStartTime(taskID string, attempt uint32, index uint32, t time.Time) *Event {
	return &Event{
		Id:        taskID,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Type:      Type_EXECUTOR_START_TIME,
		Attempt:   attempt,
		Index:     index,
		Data: &Event_StartTime{
			StartTime: t.Format(time.RFC3339Nano),
		},
	}
}

// NewExecutorEndTime creates an executor end time event.
// for the executor at the given index.
func NewExecutorEndTime(taskID string, attempt uint32, index uint32, t time.Time) *Event {
	return &Event{
		Id:        taskID,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Type:      Type_EXECUTOR_END_TIME,
		Attempt:   attempt,
		Index:     index,
		Data: &Event_EndTime{
			EndTime: t.Format(time.RFC3339Nano),
		},
	}
}

// NewExitCode creates an executor exit code event
// for the executor at the given index.
func NewExitCode(taskID string, attempt uint32, index uint32, x int32) *Event {
	return &Event{
		Id:        taskID,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Type:      Type_EXECUTOR_EXIT_CODE,
		Attempt:   attempt,
		Index:     index,
		Data: &Event_ExitCode{
			ExitCode: x,
		},
	}
}

// NewStdout creates an executor stdout chunk event
// for the executor at the given index.
func NewStdout(taskID string, attempt uint32, index uint32, s string) *Event {
	return &Event{
		Id:        taskID,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Type:      Type_EXECUTOR_STDOUT,
		Attempt:   attempt,
		Index:     index,
		Data: &Event_Stdout{
			Stdout: s,
		},
	}
}

// NewStderr creates an executor stderr chunk event
// for the executor at the given index.
func NewStderr(taskID string, attempt uint32, index uint32, s string) *Event {
	return &Event{
		Id:        taskID,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Type:      Type_EXECUTOR_STDERR,
		Attempt:   attempt,
		Index:     index,
		Data: &Event_Stderr{
			Stderr: s,
		},
	}
}

// NewSystemLog creates an system log event.
func NewSystemLog(taskID string, attempt uint32, index uint32, lvl string, msg string, fields map[string]string) *Event {
	return &Event{
		Id:        taskID,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Type:      Type_SYSTEM_LOG,
		Attempt:   attempt,
		Index:     index,
		Data: &Event_SystemLog{
			SystemLog: &SystemLog{
				Msg:    msg,
				Level:  lvl,
				Fields: fields,
			},
		},
	}
}

// NewResources creates a resources update event.
func NewResources(taskID string, r *tes.Resources) *Event {
	if r == nil {
		r = &tes.Resources{}
	}

	return &Event{
		Id:        taskID,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Type:      Type_TASK_RESOURCES,
		Data: &Event_Resources{
			Resources: &Resources{
				CpuCores:          r.CpuCores,
				RamGb:             r.RamGb,
				DiskGb:            r.DiskGb,
				Preemptible:       r.Preemptible,
				BackendParameters: r.BackendParameters,
				Zones:             r.Zones,
			},
		},
	}
}
