package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestProgressReporter_EmitsJSON(t *testing.T) {
	var buf bytes.Buffer
	pr := NewProgressReporter(&buf, false)

	pr.Emit(ProgressEvent{
		Phase:   "preflight",
		Step:    "fuel-check",
		Current: 1,
		Total:   3,
		Percent: 33.3,
		Message: "Checking fuel levels",
	})

	var got ProgressEvent
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON unmarshal: %v\nbody: %s", err, buf.String())
	}
	if got.Phase != "preflight" {
		t.Errorf("Phase = %q, want preflight", got.Phase)
	}
	if got.Current != 1 {
		t.Errorf("Current = %d, want 1", got.Current)
	}
}

func TestProgressReporter_EmitsHuman(t *testing.T) {
	var buf bytes.Buffer
	pr := NewProgressReporter(&buf, true)

	pr.Emit(ProgressEvent{
		Phase:   "launch",
		Step:    "ignition",
		Current: 2,
		Total:   5,
		Percent: 40.0,
	})

	out := buf.String()
	if !strings.Contains(out, "launch") {
		t.Errorf("human output missing phase: %q", out)
	}
	if !strings.Contains(out, "40") {
		t.Errorf("human output missing percent: %q", out)
	}
	// human mode must NOT be valid JSON
	var discard map[string]any
	if json.Unmarshal(buf.Bytes(), &discard) == nil {
		t.Error("human output should not be valid JSON")
	}
}

func TestProgressReporter_Done(t *testing.T) {
	var buf bytes.Buffer
	pr := NewProgressReporter(&buf, false)
	pr.Done("Launch complete")

	var got ProgressEvent
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	if got.Phase != "done" {
		t.Errorf("Phase = %q, want done", got.Phase)
	}
	if got.Percent != 100 {
		t.Errorf("Percent = %f, want 100", got.Percent)
	}
	if got.Message != "Launch complete" {
		t.Errorf("Message = %q, want 'Launch complete'", got.Message)
	}
}
