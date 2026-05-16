// Package config provides config-driven feature toggling for extensions.
package config

import (
	"io"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// ExtensionConfig holds per-extension configuration.
type ExtensionConfig struct {
	Enabled  bool           `yaml:"enabled"`
	Settings map[string]any `yaml:"settings,omitempty"`
}

// configFile mirrors the YAML structure on disk.
type configFile struct {
	Extensions map[string]ExtensionConfig `yaml:"extensions"`
}

// Store is a thread-safe container for extension configs.
type Store struct {
	mu      sync.RWMutex
	configs map[string]ExtensionConfig
}

// NewStore returns an empty Store ready for use.
func NewStore() *Store {
	return &Store{configs: make(map[string]ExtensionConfig)}
}

// Load parses YAML from r and merges into the store.
func (s *Store) Load(r io.Reader) error {
	var cf configFile
	dec := yaml.NewDecoder(r)
	if err := dec.Decode(&cf); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, ec := range cf.Extensions {
		s.configs[name] = ec
	}
	return nil
}

// LoadFile is a convenience wrapper that opens path and calls Load.
func (s *Store) LoadFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return s.Load(f)
}

// IsEnabled reports whether the named extension is enabled.
// Unknown extensions default to true (opt-out model).
func (s *Store) IsEnabled(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ec, ok := s.configs[name]
	if !ok {
		return true
	}
	return ec.Enabled
}

// Settings returns a copy of the settings map for the named extension.
// Returns nil when the extension has no settings.
func (s *Store) Settings(name string) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ec, ok := s.configs[name]
	if !ok {
		return nil
	}
	return copyMap(ec.Settings)
}

// SetEnabled toggles the enabled flag at runtime.
func (s *Store) SetEnabled(name string, enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ec := s.configs[name]
	ec.Enabled = enabled
	s.configs[name] = ec
}

// All returns a deep snapshot of every known extension config.
func (s *Store) All() map[string]ExtensionConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]ExtensionConfig, len(s.configs))
	for k, v := range s.configs {
		out[k] = ExtensionConfig{
			Enabled:  v.Enabled,
			Settings: copyMap(v.Settings),
		}
	}
	return out
}

func copyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
