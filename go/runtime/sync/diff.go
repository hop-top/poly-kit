package sync

import (
	"encoding/json"
	"errors"
)

// Operation describes the type of entity change.
type Operation int

const (
	OpCreate Operation = iota
	OpUpdate
	OpDelete
)

// Diff captures a single entity-level change as JSON snapshots.
type Diff struct {
	EntityID   string    `json:"entity_id"`
	EntityType string    `json:"entity_type"`
	Operation  Operation `json:"operation"`
	Before     []byte    `json:"before,omitempty"`
	After      []byte    `json:"after,omitempty"`
	Timestamp  Timestamp `json:"timestamp"`
	NodeID     string    `json:"node_id"`
}

// ComputeDiff builds a Diff by marshaling before/after into JSON snapshots.
// A nil before implies OpCreate; a nil after implies OpDelete; both non-nil
// implies OpUpdate.
func ComputeDiff(entityID, entityType string, before, after any, ts Timestamp) (Diff, error) {
	if before == nil && after == nil {
		return Diff{}, errors.New("sync: both before and after are nil")
	}

	d := Diff{
		EntityID:   entityID,
		EntityType: entityType,
		Timestamp:  ts,
		NodeID:     ts.NodeID,
	}

	switch {
	case before == nil:
		d.Operation = OpCreate
	case after == nil:
		d.Operation = OpDelete
	default:
		d.Operation = OpUpdate
	}

	if before != nil {
		b, err := json.Marshal(before)
		if err != nil {
			return Diff{}, err
		}
		d.Before = b
	}
	if after != nil {
		b, err := json.Marshal(after)
		if err != nil {
			return Diff{}, err
		}
		d.After = b
	}

	return d, nil
}
