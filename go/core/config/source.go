package config

import (
	"fmt"
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
// Sources may carry tokens, so the file is private: parent directories
// are created 0o700 and the file is written 0o600 to a temp file in the
// same directory, then renamed into place, so readers never see a
// partial or world-readable file.
func SaveRegistry(path string, r *Registry) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	wrap := struct {
		Registry *Registry `yaml:"registry"`
	}{Registry: r}
	data, err := yaml.Marshal(wrap)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".registry-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck // gone after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close() //nolint:errcheck // already failing
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close() //nolint:errcheck // already failing
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// redacted stands in for a non-empty token in formatted output.
const redacted = "[REDACTED]"

// String renders the source with its token redacted, so logging a
// Source (or a Registry) never leaks the credential.
func (s Source) String() string {
	tok := ""
	if s.Token != "" {
		tok = redacted
	}
	return fmt.Sprintf("{Name:%s Type:%s Endpoint:%s Token:%s Dirs:%v}",
		s.Name, s.Type, s.Endpoint, tok, s.Dirs)
}

// GoString is String for %#v.
func (s Source) GoString() string { return "config.Source" + s.String() }
