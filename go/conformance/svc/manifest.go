package svc

import (
	"fmt"
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Manifest is the parsed manifest.yaml shape per design §2.
type Manifest struct {
	SchemaVersion   string         `yaml:"schema_version"`
	Binary          string         `yaml:"binary"`
	BinaryVersion   string         `yaml:"binary_version,omitempty"`
	Recorder        string         `yaml:"recorder"`
	RecorderVersion string         `yaml:"recorder_version"`
	RecordedAt      time.Time      `yaml:"recorded_at"`
	ScenarioID      string         `yaml:"scenario_id,omitempty"`
	ScenarioVersion string         `yaml:"scenario_version,omitempty"`
	StoryRef        ManifestStory  `yaml:"story_ref"`
	Steps           []ManifestStep `yaml:"steps"`
}

// ManifestStory pins the story bytes inside the tar.
type ManifestStory struct {
	StoryID     string `yaml:"story_id"`
	StoryPath   string `yaml:"story_path,omitempty"`
	ContentHash string `yaml:"content_hash"`
}

// ManifestStep is a per-step pointer into the tar.
type ManifestStep struct {
	ID          string `yaml:"id"`
	CassetteDir string `yaml:"cassette_dir"`
	Captures    string `yaml:"captures"`
}

// LoadManifest parses YAML bytes from r into a *Manifest. No I/O
// validation against disk; that's the caller's responsibility.
func LoadManifest(r io.Reader) (*Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(r)
	dec.KnownFields(false) // forward-compat: ignore unknown keys
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest yaml: %w", err)
	}
	return &m, nil
}

// ValidateManifest checks the parsed manifest against the v1 schema.
// Returns the first violation; aggregate validation can be layered later.
func ValidateManifest(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("nil manifest")
	}
	if m.SchemaVersion != "1" {
		return fmt.Errorf("schema_version: want %q, got %q", "1", m.SchemaVersion)
	}
	if m.Binary == "" {
		return fmt.Errorf("binary: required")
	}
	if m.Recorder != "xrr" {
		return fmt.Errorf("recorder: want %q, got %q", "xrr", m.Recorder)
	}
	if m.RecorderVersion == "" {
		return fmt.Errorf("recorder_version: required")
	}
	if m.RecordedAt.IsZero() {
		return fmt.Errorf("recorded_at: required")
	}
	if m.StoryRef.StoryID == "" {
		return fmt.Errorf("story_ref.story_id: required")
	}
	if !strings.HasPrefix(m.StoryRef.ContentHash, "sha256:") {
		return fmt.Errorf("story_ref.content_hash: must start with %q", "sha256:")
	}
	if len(m.Steps) == 0 {
		return fmt.Errorf("steps: must be non-empty")
	}
	seen := make(map[string]struct{}, len(m.Steps))
	for i, s := range m.Steps {
		if s.ID == "" {
			return fmt.Errorf("steps[%d].id: required", i)
		}
		if _, dup := seen[s.ID]; dup {
			return fmt.Errorf("steps[%d].id: duplicate %q", i, s.ID)
		}
		seen[s.ID] = struct{}{}
		if s.CassetteDir == "" {
			return fmt.Errorf("steps[%d].cassette_dir: required", i)
		}
		if s.Captures == "" {
			return fmt.Errorf("steps[%d].captures: required", i)
		}
	}
	return nil
}
