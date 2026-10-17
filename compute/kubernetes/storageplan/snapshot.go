// Package storageplan resolves native Kubernetes storage objects for one task.
package storageplan

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (p *Plan) Encode() (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(p)
	return string(data), err
}

func Decode(snapshot string) (*Plan, error) {
	p, err := decodeSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return p, nil
}

// DecodeOwnership validates the storage identity used by cleanup while excluding
// S3 routes, which describe execution behavior and do not affect resource ownership.
func DecodeOwnership(snapshot string) (*Ownership, error) {
	p, err := decodeSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	ownershipPlan := *p
	ownershipPlan.S3ReadMounts = nil
	ownershipPlan.S3WriteMounts = nil
	if err := ownershipPlan.validate(); err != nil {
		return nil, err
	}
	return &Ownership{
		TaskID:          p.TaskID,
		Namespace:       p.Namespace,
		WorkerContainer: p.WorkerContainer,
		OwnedVolumes:    append([]string(nil), p.OwnedVolumes...),
	}, nil
}

func decodeSnapshot(snapshot string) (*Plan, error) {
	var header struct{ Version int }
	if err := json.Unmarshal([]byte(snapshot), &header); err != nil {
		return nil, fmt.Errorf("decode storage plan version: %w", err)
	}
	if header.Version != planVersion {
		return nil, fmt.Errorf("unsupported storage plan version %d; version %d is required", header.Version, planVersion)
	}
	var p Plan
	decoder := json.NewDecoder(strings.NewReader(snapshot))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode storage plan: %w", err)
	}
	return &p, nil
}
