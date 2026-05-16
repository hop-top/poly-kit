package cli

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCorrectedError_ImplementsError(t *testing.T) {
	var err error = &CorrectedError{
		Code:    "NOT_FOUND",
		Message: "mission not found",
	}
	if err.Error() != "mission not found" {
		t.Fatalf("got %q, want %q", err.Error(), "mission not found")
	}
}

func TestCorrectedError_JSON(t *testing.T) {
	e := &CorrectedError{
		Code:         "NOT_FOUND",
		Message:      "mission not found: bogus",
		Cause:        `no mission matches "bogus"`,
		Fix:          "spaced mission list",
		Alternatives: []string{"spaced mission search bogus"},
		Retryable:    false,
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"code", "message", "cause", "fix", "alternatives", "retryable"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing key %q in JSON output", key)
		}
	}
	if m["code"] != "NOT_FOUND" {
		t.Errorf("code = %v, want NOT_FOUND", m["code"])
	}
	if m["retryable"] != false {
		t.Errorf("retryable = %v, want false", m["retryable"])
	}
	alts, ok := m["alternatives"].([]any)
	if !ok || len(alts) != 1 {
		t.Fatalf("alternatives = %v, want 1-element slice", m["alternatives"])
	}
	if alts[0] != "spaced mission search bogus" {
		t.Errorf("alternatives[0] = %v", alts[0])
	}
}

func TestFormatError_Terminal(t *testing.T) {
	e := &CorrectedError{
		Code:         "NOT_FOUND",
		Message:      "mission not found: bogus",
		Cause:        `no mission matches "bogus"`,
		Fix:          "spaced mission list",
		Alternatives: []string{"spaced mission search bogus"},
	}
	var buf bytes.Buffer
	FormatError(e, &buf, true)
	out := buf.String()

	for _, want := range []string{
		"ERROR",
		"mission not found: bogus",
		"Cause:",
		`no mission matches "bogus"`,
		"Fix:",
		"spaced mission list",
		"Try:",
		"spaced mission search bogus",
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("output missing %q\n\ngot:\n%s", want, out)
		}
	}
}

func TestFormatError_NonCorrectedError(t *testing.T) {
	var buf bytes.Buffer
	FormatError(nil, &buf, true)
	if buf.Len() != 0 {
		t.Errorf("nil error should produce no output, got %q", buf.String())
	}
}
