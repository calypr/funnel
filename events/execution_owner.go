package events

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/calypr/funnel/tes"
)

const (
	ExecutionOwnerMetadataKey = "funnel_worker_owner"
	OwnerNamespaceEnv         = "FUNNEL_WORKER_POD_NAMESPACE"
	OwnerNameEnv              = "FUNNEL_WORKER_POD_NAME"
	OwnerUIDEnv               = "FUNNEL_WORKER_POD_UID"
)

// ExecutionOwnerMetadata encodes the worker owner in task metadata.
func ExecutionOwnerMetadata(owner *ExecutionOwner) (map[string]string, error) {
	if owner == nil {
		return nil, nil
	}
	if err := validateExecutionOwner(owner); err != nil {
		return nil, err
	}

	value, err := json.Marshal(owner)
	if err != nil {
		return nil, fmt.Errorf("encoding execution owner: %w", err)
	}
	return map[string]string{ExecutionOwnerMetadataKey: string(value)}, nil
}

// TaskExecutionOwner reads the owner recorded in the first task log.
func TaskExecutionOwner(task *tes.Task) (*ExecutionOwner, error) {
	if task == nil || len(task.GetLogs()) == 0 || task.GetLogs()[0] == nil {
		return nil, nil
	}
	value, ok := task.GetLogs()[0].GetMetadata()[ExecutionOwnerMetadataKey]
	if !ok {
		return nil, nil
	}

	decoder := json.NewDecoder(bytes.NewBufferString(value))
	decoder.DisallowUnknownFields()
	var owner *ExecutionOwner
	if err := decoder.Decode(&owner); err != nil {
		return nil, fmt.Errorf("decoding task execution owner: %w", err)
	}
	if owner == nil {
		return nil, fmt.Errorf("decoding task execution owner: value must be an object")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decoding task execution owner: trailing value")
		}
		return nil, fmt.Errorf("decoding task execution owner: %w", err)
	}
	if err := validateExecutionOwner(owner); err != nil {
		return nil, fmt.Errorf("decoding task execution owner: %w", err)
	}
	return owner, nil
}

func validateExecutionOwner(owner *ExecutionOwner) error {
	if owner.GetNamespace() == "" {
		return fmt.Errorf("execution owner namespace is required")
	}
	if owner.GetPodName() == "" {
		return fmt.Errorf("execution owner pod name is required")
	}
	if owner.GetPodUid() == "" {
		return fmt.Errorf("execution owner pod UID is required")
	}
	return nil
}
