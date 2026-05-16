package scenario_test

import (
	"os"
	"path/filepath"
	"testing"

	"hop.top/kit/go/conformance/scenario"
)

func TestParseFile_OKMinimal(t *testing.T) {
	s, err := scenario.ParseFile(filepath.Join("testdata", "ok-minimal.yaml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.ScenarioID != "ok-minimal" {
		t.Errorf("ScenarioID = %q, want ok-minimal", s.ScenarioID)
	}
	if s.SchemaVersion != "1" {
		t.Errorf("SchemaVersion = %q, want \"1\"", s.SchemaVersion)
	}
	if len(s.Steps) != 1 {
		t.Errorf("len(Steps) = %d, want 1", len(s.Steps))
	}
	if len(s.Assertions) != 2 {
		t.Errorf("len(Assertions) = %d, want 2", len(s.Assertions))
	}
	// Check the inline-args path: exit_code_equals carries value: 0.
	if v, ok := s.Assertions[0].Args["value"]; !ok || v != 0 {
		t.Errorf("Assertions[0].Args[value] = %v, want 0", v)
	}
}

func TestParseBytes_MissingSchemaVersion(t *testing.T) {
	data := []byte(`scenario_id: foo
binary: spaced
factor_coverage: [1]
tier: 1
story_ref:
  story_id: x
  story_path: x
  content_hash: "sha256:0000000000000000000000000000000000000000000000000000000000000000"
steps:
  - id: s
    invoke: ["a"]
assertions:
  - id: a
    kind: exit_code_equals
    on: s
    factor: 1
    value: 0
`)
	_, err := scenario.ParseBytes(data, "<test>")
	if err == nil {
		t.Fatalf("expected error for missing schema_version")
	}
	if !scenario.IsParseError(err) {
		t.Errorf("expected ParseError, got %T: %v", err, err)
	}
}

func TestParseBytes_UnsupportedSchemaVersion(t *testing.T) {
	data := []byte(`schema_version: "999"
scenario_id: foo
binary: spaced
factor_coverage: [1]
tier: 1
story_ref:
  story_id: x
  story_path: x
  content_hash: "sha256:0000000000000000000000000000000000000000000000000000000000000000"
steps:
  - id: s
    invoke: ["a"]
assertions: [{id: a, kind: exit_code_equals, on: s, factor: 1, value: 0}]
`)
	_, err := scenario.ParseBytes(data, "<test>")
	if err == nil {
		t.Fatalf("expected error for unsupported schema_version")
	}
	if !scenario.IsSchemaUnsupported(err) {
		t.Errorf("expected SchemaUnsupportedError, got %T", err)
	}
}

func TestParseBytes_UnknownTopLevelKey(t *testing.T) {
	data := []byte(`schema_version: "1"
scenario_id: foo
binary: spaced
factor_coverage: [1]
tier: 1
story_ref:
  story_id: x
  story_path: x
  content_hash: "sha256:0000000000000000000000000000000000000000000000000000000000000000"
steps: [{id: s, invoke: ["a"]}]
assertions: [{id: a, kind: exit_code_equals, on: s, factor: 1, value: 0}]
some_unknown_key: "boom"
`)
	_, err := scenario.ParseBytes(data, "<test>")
	if err == nil {
		t.Fatalf("expected error for unknown top-level key")
	}
	if !scenario.IsParseError(err) {
		t.Errorf("expected ParseError, got %T", err)
	}
}

func TestParseFile_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := scenario.ParseFile(filepath.Join(dir, "nope.yaml"))
	if err == nil {
		t.Fatalf("expected error for missing file")
	}
	if _, perr := os.Stat(filepath.Join(dir, "nope.yaml")); !os.IsNotExist(perr) {
		t.Errorf("unexpected stat: %v", perr)
	}
}
