package scenario

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ParseFile reads a scenario YAML file from disk and decodes it into
// a *Scenario via yaml.v3 KnownFields(true). Schema-version gating
// runs after the strict decode so an old binary surfaces a clear
// SCENARIO_SCHEMA_UNSUPPORTED rather than a generic parse error.
func ParseFile(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scenario %s: %w", path, err)
	}
	return ParseBytes(data, path)
}

// ParseBytes parses an in-memory scenario YAML blob. label is the
// human-readable source identifier used in error messages (file
// path, "<bytes>", etc.).
//
// Two-phase decode:
//  1. Read schema_version from a permissive map decode and gate
//     before strict decoding — so a scenario authored against a
//     newer schema fails with SCENARIO_SCHEMA_UNSUPPORTED instead of
//     a noisy "unknown field" error from KnownFields(true).
//  2. Strict decode into *Scenario with KnownFields(true) for
//     closed-key enforcement.
func ParseBytes(data []byte, label string) (*Scenario, error) {
	if label == "" {
		label = "<bytes>"
	}

	// Phase 1: permissive schema_version probe.
	var probe struct {
		SchemaVersion string `yaml:"schema_version"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parse scenario %s: %w", label, err)
	}
	if probe.SchemaVersion == "" {
		return nil, &ParseError{
			Label:   label,
			Message: "missing required key schema_version",
		}
	}
	if !IsSupportedSchemaVersion(probe.SchemaVersion) {
		return nil, &SchemaUnsupportedError{
			Label:     label,
			Declared:  probe.SchemaVersion,
			Supported: append([]string(nil), SupportedSchemaVersions...),
		}
	}

	// Phase 2: strict decode.
	var s Scenario
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, &ParseError{
			Label:   label,
			Message: err.Error(),
			Cause:   err,
		}
	}
	return &s, nil
}

// ParseError is returned for malformed scenario YAML or unknown
// closed-key fields. Maps to SCENARIO_PARSE_ERROR (exit 2) at the
// CLI boundary.
type ParseError struct {
	Label   string
	Message string
	Cause   error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("scenario parse error (%s): %s", e.Label, e.Message)
}

func (e *ParseError) Unwrap() error { return e.Cause }

// SchemaUnsupportedError is returned when the scenario's declared
// schema_version is not in SupportedSchemaVersions. Maps to
// SCENARIO_SCHEMA_UNSUPPORTED (exit 1) at the CLI boundary.
type SchemaUnsupportedError struct {
	Label     string
	Declared  string
	Supported []string
}

func (e *SchemaUnsupportedError) Error() string {
	return fmt.Sprintf("scenario %s declares schema_version %q; this binary supports %v (upgrade kit or downgrade the scenario)",
		e.Label, e.Declared, e.Supported)
}

// IsParseError reports whether err is or wraps a *ParseError.
func IsParseError(err error) bool {
	var pe *ParseError
	return errors.As(err, &pe)
}

// IsSchemaUnsupported reports whether err is or wraps a
// *SchemaUnsupportedError.
func IsSchemaUnsupported(err error) bool {
	var se *SchemaUnsupportedError
	return errors.As(err, &se)
}
