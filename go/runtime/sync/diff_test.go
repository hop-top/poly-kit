package sync

import (
	"encoding/json"
	"testing"
)

type testEntity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestComputeDiff_Create(t *testing.T) {
	ts := Timestamp{Physical: 1, NodeID: "n1"}
	e := testEntity{ID: "1", Name: "foo"}
	d, err := ComputeDiff("1", "test", nil, e, ts)
	if err != nil {
		t.Fatal(err)
	}
	if d.Operation != OpCreate {
		t.Fatalf("expected OpCreate, got %d", d.Operation)
	}
	if d.Before != nil {
		t.Fatal("expected nil Before")
	}
	if d.After == nil {
		t.Fatal("expected non-nil After")
	}
}

func TestComputeDiff_Update(t *testing.T) {
	ts := Timestamp{Physical: 2, NodeID: "n1"}
	old := testEntity{ID: "1", Name: "foo"}
	new := testEntity{ID: "1", Name: "bar"}
	d, err := ComputeDiff("1", "test", old, new, ts)
	if err != nil {
		t.Fatal(err)
	}
	if d.Operation != OpUpdate {
		t.Fatalf("expected OpUpdate, got %d", d.Operation)
	}
	if d.Before == nil || d.After == nil {
		t.Fatal("expected both Before and After")
	}
}

func TestComputeDiff_Delete(t *testing.T) {
	ts := Timestamp{Physical: 3, NodeID: "n1"}
	e := testEntity{ID: "1", Name: "foo"}
	d, err := ComputeDiff("1", "test", e, nil, ts)
	if err != nil {
		t.Fatal(err)
	}
	if d.Operation != OpDelete {
		t.Fatalf("expected OpDelete, got %d", d.Operation)
	}
	if d.After != nil {
		t.Fatal("expected nil After")
	}
}

func TestComputeDiff_BothNil(t *testing.T) {
	ts := Timestamp{Physical: 4, NodeID: "n1"}
	_, err := ComputeDiff("1", "test", nil, nil, ts)
	if err == nil {
		t.Fatal("expected error for both nil")
	}
}

func TestComputeDiff_JSONRoundtrip(t *testing.T) {
	ts := Timestamp{Physical: 5, NodeID: "n1"}
	e := testEntity{ID: "1", Name: "roundtrip"}
	d, err := ComputeDiff("1", "test", nil, e, ts)
	if err != nil {
		t.Fatal(err)
	}

	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}

	var d2 Diff
	if err := json.Unmarshal(b, &d2); err != nil {
		t.Fatal(err)
	}
	if d2.EntityID != d.EntityID || d2.Operation != d.Operation {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", d, d2)
	}
}
