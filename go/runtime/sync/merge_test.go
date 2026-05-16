package sync

import (
	"encoding/json"
	"testing"
)

func TestLastWriteWins_PicksLater(t *testing.T) {
	local := Diff{
		EntityID:  "1",
		Timestamp: Timestamp{Physical: 10, Logical: 0, NodeID: "a"},
		NodeID:    "a",
	}
	remote := Diff{
		EntityID:  "1",
		Timestamp: Timestamp{Physical: 20, Logical: 0, NodeID: "b"},
		NodeID:    "b",
	}

	winner := LastWriteWins(local, remote)
	if winner.NodeID != "b" {
		t.Fatalf("expected remote to win, got NodeID=%s", winner.NodeID)
	}
}

func TestLastWriteWins_EqualTimestamp_TiebreakByNodeID(t *testing.T) {
	local := Diff{
		EntityID:  "1",
		Timestamp: Timestamp{Physical: 10, Logical: 0, NodeID: "a"},
		NodeID:    "a",
	}
	remote := Diff{
		EntityID:  "1",
		Timestamp: Timestamp{Physical: 10, Logical: 0, NodeID: "b"},
		NodeID:    "b",
	}

	winner := LastWriteWins(local, remote)
	if winner.NodeID != "b" {
		t.Fatalf("expected higher NodeID 'b' to win, got %s", winner.NodeID)
	}

	// Swap: local has higher NodeID
	local.Timestamp.NodeID = "z"
	local.NodeID = "z"
	winner = LastWriteWins(local, remote)
	if winner.NodeID != "z" {
		t.Fatalf("expected higher NodeID 'z' to win, got %s", winner.NodeID)
	}
}

func TestResolveDiff_CustomMerge(t *testing.T) {
	type item struct {
		Count int `json:"count"`
	}

	localAfter, _ := json.Marshal(item{Count: 5})
	remoteAfter, _ := json.Marshal(item{Count: 3})

	local := Diff{
		EntityID:  "1",
		Operation: OpUpdate,
		After:     localAfter,
		Timestamp: Timestamp{Physical: 10, NodeID: "a"},
		NodeID:    "a",
	}
	remote := Diff{
		EntityID:  "1",
		Operation: OpUpdate,
		After:     remoteAfter,
		Timestamp: Timestamp{Physical: 20, NodeID: "b"},
		NodeID:    "b",
	}

	// Custom merge: sum counts
	merged, err := ResolveDiff(local, remote, func(l, r item) (item, error) {
		return item{Count: l.Count + r.Count}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var result item
	if err := json.Unmarshal(merged.After, &result); err != nil {
		t.Fatal(err)
	}
	if result.Count != 8 {
		t.Fatalf("expected merged count 8, got %d", result.Count)
	}
}
