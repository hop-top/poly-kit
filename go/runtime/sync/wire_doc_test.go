package sync

import (
	"encoding/json"
	"testing"
)

// TestSyncModeWireValues pins the SyncMode integer values documented in
// docs/adopters/reference/engine-sync.md. SyncMode has no custom
// marshaller, so declaration order is the cross-language wire contract.
// Bidirectional must stay the zero value: a Remote with no explicit mode
// syncs both directions.
func TestSyncModeWireValues(t *testing.T) {
	for _, tc := range []struct {
		mode SyncMode
		want int
		name string
	}{
		{Bidirectional, 0, "Bidirectional"},
		{PushOnly, 1, "PushOnly"},
		{PullOnly, 2, "PullOnly"},
	} {
		if int(tc.mode) != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, int(tc.mode), tc.want)
		}
	}

	var zero SyncMode
	if zero != Bidirectional {
		t.Errorf("zero SyncMode = %d, want Bidirectional", int(zero))
	}
}

// TestOperationWireValues pins the Diff.Operation enum documented in
// engine-sync.md.
func TestOperationWireValues(t *testing.T) {
	for _, tc := range []struct {
		op   Operation
		want int
		name string
	}{
		{OpCreate, 0, "OpCreate"},
		{OpUpdate, 1, "OpUpdate"},
		{OpDelete, 2, "OpDelete"},
	} {
		if int(tc.op) != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, int(tc.op), tc.want)
		}
	}
}

// TestTimestampJSONKeys pins the HLC wire keys. Timestamp is the type
// other language ports must agree with byte-for-byte to order events.
func TestTimestampJSONKeys(t *testing.T) {
	raw, err := json.Marshal(Timestamp{Physical: 1713520000000000000, Logical: 1, NodeID: "abc123"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{"physical", "logical", "node_id"} {
		if _, ok := got[key]; !ok {
			t.Errorf("Timestamp JSON missing key %q, got %v", key, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("Timestamp JSON has %d keys, want exactly 3: %v", len(got), got)
	}
}

// TestDiffJSONKeys pins the Diff envelope keys.
func TestDiffJSONKeys(t *testing.T) {
	raw, err := json.Marshal(Diff{
		EntityID:   "e1",
		EntityType: "notes",
		Operation:  OpUpdate,
		Before:     []byte(`{"a":1}`),
		After:      []byte(`{"a":2}`),
		Timestamp:  Timestamp{Physical: 1, Logical: 0, NodeID: "n1"},
		NodeID:     "n1",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{
		"entity_id", "entity_type", "operation", "before", "after",
		"timestamp", "node_id",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("Diff JSON missing key %q, got %v", key, got)
		}
	}
}

// TestLastWriteWinsTiebreakIsInclusive pins the >= NodeID comparison
// documented in engine-sync.md. A port using strict > diverges when a
// diff is compared against an identical one, so both sides must agree.
func TestLastWriteWinsTiebreakIsInclusive(t *testing.T) {
	ts := Timestamp{Physical: 100, Logical: 1, NodeID: "n"}

	local := Diff{EntityID: "local", Timestamp: ts, NodeID: "same"}
	remote := Diff{EntityID: "remote", Timestamp: ts, NodeID: "same"}

	// Fully equal timestamps and node IDs: local must win.
	if got := LastWriteWins(local, remote); got.EntityID != "local" {
		t.Errorf("equal NodeID: winner = %q, want local", got.EntityID)
	}

	// Lexicographically greater node ID wins regardless of side.
	hi := Diff{EntityID: "hi", Timestamp: ts, NodeID: "b"}
	lo := Diff{EntityID: "lo", Timestamp: ts, NodeID: "a"}
	if got := LastWriteWins(lo, hi); got.EntityID != "hi" {
		t.Errorf("remote has greater NodeID: winner = %q, want hi", got.EntityID)
	}
	if got := LastWriteWins(hi, lo); got.EntityID != "hi" {
		t.Errorf("local has greater NodeID: winner = %q, want hi", got.EntityID)
	}
}
