package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Source describes a named registry source. Type/Endpoint/Dirs are kept as
// loose strings/slices so the config layer stays agnostic of the concrete
// source kinds that consumers (rlz, others) implement.
type Source struct {
	Name     string   `yaml:"name"`
	Type     string   `yaml:"type"`
	Endpoint string   `yaml:"endpoint,omitempty"`
	Token    string   `yaml:"token,omitempty"`
	Dirs     []string `yaml:"dirs,omitempty"`
}

// Registry is an ordered set of Sources. Insertion order is preserved so YAML
// round-trips don't shuffle entries; lookup is by name.
type Registry struct {
	Sources []Source `yaml:"sources,omitempty"`
}

// Get returns the named source and a found-flag.
func (r *Registry) Get(name string) (*Source, bool) {
	for i := range r.Sources {
		if r.Sources[i].Name == name {
			return &r.Sources[i], true
		}
	}
	return nil, false
}

// Add appends s. Duplicate names overwrite in place (upsert).
func (r *Registry) Add(s Source) {
	for i := range r.Sources {
		if r.Sources[i].Name == s.Name {
			r.Sources[i] = s
			return
		}
	}
	r.Sources = append(r.Sources, s)
}

// Remove drops the named source. Returns false if not present.
func (r *Registry) Remove(name string) bool {
	for i := range r.Sources {
		if r.Sources[i].Name == name {
			r.Sources = append(r.Sources[:i], r.Sources[i+1:]...)
			return true
		}
	}
	return false
}

// Names returns source names in insertion order.
func (r *Registry) Names() []string {
	out := make([]string, len(r.Sources))
	for i, s := range r.Sources {
		out[i] = s.Name
	}
	return out
}

// Merge folds other into r: new names append, existing names overwrite.
// Caller-controlled precedence: pass higher-priority registry as other.
func (r *Registry) Merge(other Registry) {
	for _, s := range other.Sources {
		r.Add(s)
	}
}

// LoadRegistry reads a YAML file containing a top-level `registry:` block
// (matching rlz's layout) and returns the parsed Registry. Missing files
// return an empty registry without error so callers can treat absence as
// "no sources configured".
func LoadRegistry(path string) (*Registry, error) {
	if path == "" {
		return &Registry{}, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Registry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var wrap struct {
		Registry Registry `yaml:"registry"`
	}
	if err := yaml.Unmarshal(data, &wrap); err != nil {
		return nil, err
	}
	return &wrap.Registry, nil
}

// SaveRegistry writes r to path inside a top-level `registry:` block.
// Creates parent directories as needed.
func SaveRegistry(path string, r *Registry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	wrap := struct {
		Registry *Registry `yaml:"registry"`
	}{Registry: r}
	data, err := yaml.Marshal(wrap)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
