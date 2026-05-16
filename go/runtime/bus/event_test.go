package bus

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTopic_Match_Exact(t *testing.T) {
	topic := Topic("llm.request")
	if !topic.Match("llm.request") {
		t.Error("exact match should succeed")
	}
	if topic.Match("llm.response") {
		t.Error("different topic should not match")
	}
}

func TestTopic_Match_SingleWildcard(t *testing.T) {
	topic := Topic("llm.request")
	if !topic.Match("llm.*") {
		t.Error("llm.* should match llm.request")
	}
	if !topic.Match("*.request") {
		t.Error("*.request should match llm.request")
	}

	deep := Topic("llm.request.start")
	if deep.Match("llm.*") {
		t.Error("llm.* should NOT match llm.request.start (too deep)")
	}
}

func TestTopic_Match_MultiWildcard(t *testing.T) {
	cases := []struct {
		topic   Topic
		pattern string
		want    bool
	}{
		{"llm.request", "llm.#", true},
		{"llm.request.start", "llm.#", true},
		{"llm", "llm.#", true},
		{"tool.exec", "llm.#", false},
		{"llm.request.start", "#", true},
		{"anything", "#", true},
	}
	for _, tc := range cases {
		got := tc.topic.Match(tc.pattern)
		if got != tc.want {
			t.Errorf("Topic(%q).Match(%q) = %v, want %v",
				tc.topic, tc.pattern, got, tc.want)
		}
	}
}

func TestEvent_JSONRoundTrip(t *testing.T) {
	type payload struct {
		Model string `json:"model"`
	}
	e := Event{
		Topic:     "llm.request",
		Source:    "test",
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Payload:   payload{Model: "claude-4"},
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded struct {
		Topic     string          `json:"topic"`
		Source    string          `json:"source"`
		Timestamp time.Time       `json:"timestamp"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Topic != "llm.request" {
		t.Errorf("topic = %q, want llm.request", decoded.Topic)
	}
	if decoded.Source != "test" {
		t.Errorf("source = %q, want test", decoded.Source)
	}

	var p payload
	if err := json.Unmarshal(decoded.Payload, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if p.Model != "claude-4" {
		t.Errorf("model = %q, want claude-4", p.Model)
	}
}

func TestNewEvent_SetsTimestamp(t *testing.T) {
	before := time.Now()
	e := NewEvent("test.topic", "src", nil)
	after := time.Now()

	if e.Timestamp.Before(before) || e.Timestamp.After(after) {
		t.Error("timestamp should be between before and after")
	}
}

// TestEvent_JSON_LowercaseFieldNames verifies bus.Event marshals with
// lowercase JSON keys per tlc/docs/bus-topics-spec-0.1.md §4 (T-0196).
// External cross-process subscribers parse lowercase; capitalized keys
// are a v0.1 leak that breaks them.
func TestEvent_JSON_LowercaseFieldNames(t *testing.T) {
	e := Event{
		Topic:     "x.y",
		Source:    "src",
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Payload:   map[string]string{"k": "v"},
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)

	for _, key := range []string{`"topic"`, `"source"`, `"timestamp"`, `"payload"`} {
		if !strings.Contains(js, key) {
			t.Errorf("expected JSON to contain %s, got: %s", key, js)
		}
	}
	for _, bad := range []string{`"Topic"`, `"Source"`, `"Timestamp"`, `"Payload"`} {
		if strings.Contains(js, bad) {
			t.Errorf("expected JSON to NOT contain capitalized %s, got: %s", bad, js)
		}
	}
}

// TestEvent_WorkspaceID_RoundTrip verifies the v0.2 envelope addition
// (T-0192 spec): WorkspaceID survives JSON round-trip with snake_case
// key and is omitted when blank for backward-compat with v0.1 publishers.
func TestEvent_WorkspaceID_RoundTrip(t *testing.T) {
	// Set: workspace_id present.
	e := Event{
		Topic:       "tlc.task.created",
		Source:      "tlc",
		Timestamp:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		WorkspaceID: "01J7ZXY8Q2K9V0M3N4P5R6S7T8",
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"workspace_id":"01J7ZXY8Q2K9V0M3N4P5R6S7T8"`) {
		t.Errorf("expected snake_case workspace_id in JSON, got: %s", data)
	}

	var decoded struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.WorkspaceID != "01J7ZXY8Q2K9V0M3N4P5R6S7T8" {
		t.Errorf("workspace_id = %q, want round-trip preserved", decoded.WorkspaceID)
	}

	// Blank: omitempty drops the key (v0.1 backward-compat).
	blank := Event{
		Topic:     "tlc.task.created",
		Source:    "tlc",
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	blankData, err := json.Marshal(blank)
	if err != nil {
		t.Fatalf("marshal blank: %v", err)
	}
	if strings.Contains(string(blankData), "workspace_id") {
		t.Errorf("blank WorkspaceID should be omitted, got: %s", blankData)
	}
}
