package projects

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the on-disk schema version this build understands.
// Reads of files with schema > SchemaVersion fail with ErrSchemaUnsupported
// (forward-incompatibility guard).
//
// History:
//
//	1 — initial layout (path, startup_cmd, source).
//	2 — added optional stage:{...} block (kit/core/stage). Old files
//	    read transparently; missing stage = StageActive at the consumer.
const SchemaVersion = 2

// Source identifies who registered a project entry. wsm-written entries
// are rewritten on workspace re-registration; manual entries (added via
// a future `rux project add`) are preserved across rewrites.
type Source string

const (
	// SourceWSM marks entries written by `wsm space add` / `wsm space sync`.
	SourceWSM Source = "wsm"
	// SourceManual marks entries added by hand or via `rux project add`.
	SourceManual Source = "manual"
)

// String implements fmt.Stringer.
func (s Source) String() string { return string(s) }

// UnmarshalYAML accepts the empty scalar as SourceWSM so older or hand-
// edited files without a `source:` field still load cleanly.
func (s *Source) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("projects: decode source: %w", err)
	}
	if raw == "" {
		*s = SourceWSM
		return nil
	}
	*s = Source(raw)
	return nil
}

// Entry is a single project registration.
//
// Stage is the optional operating-mode block (active / public_feedback
// / feature_freeze / maintenance / sunset / archived). When nil or
// absent on disk, consumers treat the scope as StageActive. The
// concrete shape is defined here (rather than re-using kit/core/stage
// directly) so the projects package stays import-cycle-free; the stage
// package re-exports StageState as stage.State via a type alias.
type Entry struct {
	Path       string      `yaml:"path"`
	StartupCmd string      `yaml:"startup_cmd,omitempty"`
	Source     Source      `yaml:"source,omitempty"`
	Stage      *StageState `yaml:"stage,omitempty"`
}

// StageState is the on-disk shape of an Entry's stage block. The
// kit/core/stage package re-exports this struct as stage.State.
//
// Field semantics:
//
//   - Stage  — the operating mode value (active / public_feedback / …)
//   - Since  — when the stage was entered; UTC RFC3339 on disk
//   - Until  — optional auto-expiry; nil means "no expiry"
//   - Reason — free-form note explaining why the stage is set
//   - Allow  — advisory CEL predicate hints (informational only)
//   - Deny   — advisory CEL predicate hints (informational only)
//   - Actor  — who set the stage (matches kit principal IDs)
type StageState struct {
	Stage  string     `yaml:"stage"`
	Since  time.Time  `yaml:"since"`
	Until  *time.Time `yaml:"until,omitempty"`
	Reason string     `yaml:"reason,omitempty"`
	Allow  []string   `yaml:"allow,omitempty"`
	Deny   []string   `yaml:"deny,omitempty"`
	Actor  string     `yaml:"actor,omitempty"`
}

// File is the on-disk shape of projects.yaml.
type File struct {
	Schema   int              `yaml:"schema"`
	Projects map[string]Entry `yaml:"projects"`
}
