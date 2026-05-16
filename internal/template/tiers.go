// Package template — per-file tier filtering.
//
// Tiers are per-file metadata stored in tiers.yaml at the template
// root, mapping path -> []int of applicable tiers. Used by augment
// mode to render only files whose tier matches the requested tier.
// Bootstrap mode (tier=0) bypasses the filter; absent paths default
// to tier [4].
//
// See docs/superpowers/specs/2026-04-26-kit-init-design.md §13.
package template

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"text/template"

	"gopkg.in/yaml.v3"
)

// tiersFile is the on-disk schema for tiers.yaml.
type tiersFile struct {
	Files map[string][]int `yaml:"files"`
}

// LoadTiers reads "tiers.yaml" from src root if present. Returns an
// empty map when the file is absent (every file then defaults to
// tier [4]). Returns an error only on parse failure.
func LoadTiers(src fs.FS) (map[string][]int, error) {
	data, err := fs.ReadFile(src, "tiers.yaml")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string][]int{}, nil
		}
		return nil, fmt.Errorf("template: read tiers.yaml: %w", err)
	}
	var tf tiersFile
	if err := yaml.Unmarshal(data, &tf); err != nil {
		return nil, fmt.Errorf("template: parse tiers.yaml: %w", err)
	}
	if tf.Files == nil {
		return map[string][]int{}, nil
	}
	return tf.Files, nil
}

// renderTierKeys runs each key in tierMap through text/template with
// vars as data, producing a new map keyed by post-substitution paths.
// This lets tiers.yaml use template variables (e.g.
// "cmd/{{.name}}/main.go") that resolve to the same output paths the
// engine writes. A malformed key template returns an error wrapped
// with the offending key so callers can pinpoint the source.
// Empty rendered keys are dropped (defensive: avoids matching every
// file under the empty path).
func renderTierKeys(tierMap map[string][]int, vars map[string]any) (map[string][]int, error) {
	if len(tierMap) == 0 {
		return tierMap, nil
	}
	out := make(map[string][]int, len(tierMap))
	for key, tiers := range tierMap {
		t, err := template.New("tierkey").Option("missingkey=error").Parse(key)
		if err != nil {
			return nil, fmt.Errorf("template: tier key %q: %w", key, err)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, vars); err != nil {
			return nil, fmt.Errorf("template: tier key %q: %w", key, err)
		}
		rendered := buf.String()
		if rendered == "" {
			continue
		}
		out[rendered] = tiers
	}
	return out, nil
}

// AppliesAtTier reports whether the file at path should be rendered
// at the given tier. Files not present in tierMap default to [4].
// tier=0 (bootstrap mode) returns true unconditionally.
func AppliesAtTier(path string, tierMap map[string][]int, tier int) bool {
	if tier == 0 {
		return true
	}
	tiers, ok := tierMap[path]
	if !ok {
		tiers = []int{4}
	}
	for _, t := range tiers {
		if t == tier {
			return true
		}
	}
	return false
}
