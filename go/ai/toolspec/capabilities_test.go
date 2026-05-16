package toolspec_test

import (
	"encoding/json"
	"testing"

	"hop.top/kit/go/ai/toolspec"
)

func TestCapabilitySet_Add(t *testing.T) {
	cs := toolspec.CapabilitySet{ServiceName: "test"}
	cs.Add(toolspec.Capability{Name: "list_items", Type: "endpoint", Path: "/items"})
	if len(cs.Capabilities) != 1 {
		t.Fatalf("expected 1, got %d", len(cs.Capabilities))
	}
	if cs.Capabilities[0].Name != "list_items" {
		t.Fatalf("unexpected name: %s", cs.Capabilities[0].Name)
	}
}

func TestCapabilitySet_JSON_Roundtrip(t *testing.T) {
	cs := toolspec.CapabilitySet{
		ServiceName: "widgets",
		Version:     "1.0.0",
	}
	cs.Add(toolspec.Capability{
		Name:        "create_widget",
		Type:        "endpoint",
		Path:        "/widgets",
		Methods:     []string{"POST"},
		Description: "Create a widget",
	})
	cs.Add(toolspec.Capability{
		Name:    "list_widgets",
		Type:    "endpoint",
		Path:    "/widgets",
		Methods: []string{"GET"},
	})

	data, err := cs.JSON()
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}

	var decoded toolspec.CapabilitySet
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if decoded.ServiceName != "widgets" {
		t.Errorf("service = %q, want %q", decoded.ServiceName, "widgets")
	}
	if decoded.Version != "1.0.0" {
		t.Errorf("version = %q, want %q", decoded.Version, "1.0.0")
	}
	if len(decoded.Capabilities) != 2 {
		t.Fatalf("capabilities len = %d, want 2", len(decoded.Capabilities))
	}
	if decoded.Capabilities[0].Description != "Create a widget" {
		t.Errorf("description = %q", decoded.Capabilities[0].Description)
	}
}

func TestCapabilitySet_Merge_Deduplicates(t *testing.T) {
	cs := toolspec.CapabilitySet{ServiceName: "svc"}
	cs.Add(toolspec.Capability{Name: "get_item", Type: "endpoint", Path: "/items/{id}"})
	cs.Add(toolspec.Capability{Name: "list_items", Type: "endpoint", Path: "/items"})

	other := toolspec.CapabilitySet{ServiceName: "other"}
	other.Add(toolspec.Capability{Name: "get_item", Type: "endpoint", Path: "/items/{id}"})
	other.Add(toolspec.Capability{Name: "delete_item", Type: "endpoint", Path: "/items/{id}"})

	cs.Merge(other)

	if len(cs.Capabilities) != 3 {
		t.Fatalf("expected 3 after merge, got %d", len(cs.Capabilities))
	}

	names := map[string]bool{}
	for _, c := range cs.Capabilities {
		names[c.Name] = true
	}
	for _, want := range []string{"get_item", "list_items", "delete_item"} {
		if !names[want] {
			t.Errorf("missing capability %q after merge", want)
		}
	}
}

func TestCapabilitySet_Merge_SameNameDifferentPath(t *testing.T) {
	cs := toolspec.CapabilitySet{ServiceName: "svc"}
	cs.Add(toolspec.Capability{Name: "get", Path: "/a"})

	other := toolspec.CapabilitySet{}
	other.Add(toolspec.Capability{Name: "get", Path: "/b"})

	cs.Merge(other)

	if len(cs.Capabilities) != 2 {
		t.Fatalf("expected 2 (same name, different path), got %d", len(cs.Capabilities))
	}
}
