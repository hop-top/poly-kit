// Package schema defines the Go types and YAML tags for kit
// stories (v1). It is the single source of truth for the closed-key
// shape; the parser package uses yaml.v3's KnownFields(true) against
// these types to reject any unknown top-level / step / reference key.
//
// The schema is intentionally minimal: stories are "what the user is
// trying to do" — plain-English intent plus a command sequence. They
// do NOT carry assertions, judges, or cassette guards (those keys are
// scenario-rubric territory; verify-no-leak gates them out of the
// public repo).
//
// Version policy:
//
//   - schema_version: "1" is the only accepted value in v1.
//   - Additive fields within v1 are allowed in minor bumps (no
//     rename of v1; future v1.x adds optional fields).
//   - Major bump (v2) is breaking; the v2 validator ships a
//     v1-compat mode.
package schema

// SchemaVersionV1 is the only schema_version this kit binary
// recognizes. A bump to "2" requires a new kit binary; the validator
// refuses unknown versions.
const SchemaVersionV1 = "1"

// CaptureExitCode / CaptureStdout / CaptureStderr / CaptureDurationMS
// are the recognized values of steps[].capture in v1. Adding a value
// requires updating both Capture.IsValid() and the JSON Schema in
// contracts/story-schema.json.
const (
	CaptureExitCode   = "exit_code"
	CaptureStdout     = "stdout"
	CaptureStderr     = "stderr"
	CaptureDurationMS = "duration_ms"
)

// AllowedCaptures is the closed enum for steps[].capture values.
var AllowedCaptures = map[string]struct{}{
	CaptureExitCode:   {},
	CaptureStdout:     {},
	CaptureStderr:     {},
	CaptureDurationMS: {},
}

// Story is the top-level closed-key shape. yaml.v3 with
// KnownFields(true) rejects any field not declared here.
type Story struct {
	SchemaVersion string         `yaml:"schema_version"`
	StoryID       string         `yaml:"story_id"`
	Title         string         `yaml:"title"`
	Intent        string         `yaml:"intent"`
	Binary        string         `yaml:"binary"`
	ToolspecRef   string         `yaml:"toolspec_ref,omitempty"`
	References    []Reference    `yaml:"references,omitempty"`
	Preconditions []string       `yaml:"preconditions,omitempty"`
	Steps         []Step         `yaml:"steps"`
	Metadata      map[string]any `yaml:"metadata,omitempty"`
}

// Step is the closed-key shape for one entry in steps[].
type Step struct {
	ID      string   `yaml:"id"`
	Intent  string   `yaml:"intent,omitempty"`
	Invoke  []string `yaml:"invoke"`
	Capture []string `yaml:"capture,omitempty"`
}

// Reference is the closed-key shape for one entry in references[].
type Reference struct {
	Title string `yaml:"title"`
	URL   string `yaml:"url"`
}
