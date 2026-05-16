package config

import (
	"strings"
	"testing"
)

const validYAML = `
extensions:
  my-ext:
    enabled: true
    settings:
      key: value
      count: 42
  other-ext:
    enabled: false
`

func TestLoadValidYAML(t *testing.T) {
	s := NewStore()
	if err := s.Load(strings.NewReader(validYAML)); err != nil {
		t.Fatalf("Load: %v", err)
	}
	all := s.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(all))
	}
	if !all["my-ext"].Enabled {
		t.Error("my-ext should be enabled")
	}
	if all["other-ext"].Enabled {
		t.Error("other-ext should be disabled")
	}
}

func TestIsEnabledUnknownExtension(t *testing.T) {
	s := NewStore()
	if !s.IsEnabled("never-seen") {
		t.Error("unknown extension should default to enabled")
	}
}

func TestIsEnabledExplicitlyDisabled(t *testing.T) {
	s := NewStore()
	if err := s.Load(strings.NewReader(validYAML)); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.IsEnabled("other-ext") {
		t.Error("other-ext should be disabled")
	}
}

func TestSettingsReturnsCorrectMap(t *testing.T) {
	s := NewStore()
	if err := s.Load(strings.NewReader(validYAML)); err != nil {
		t.Fatalf("Load: %v", err)
	}
	m := s.Settings("my-ext")
	if m == nil {
		t.Fatal("expected non-nil settings")
	}
	if v, ok := m["key"]; !ok || v != "value" {
		t.Errorf("key: got %v, want %q", v, "value")
	}
	if v, ok := m["count"]; !ok || v != 42 {
		t.Errorf("count: got %v, want 42", v)
	}
}

func TestSettingsNilForUnknown(t *testing.T) {
	s := NewStore()
	if m := s.Settings("unknown"); m != nil {
		t.Errorf("expected nil settings for unknown ext, got %v", m)
	}
}

func TestSetEnabledToggle(t *testing.T) {
	s := NewStore()
	if err := s.Load(strings.NewReader(validYAML)); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// disable a previously enabled ext
	s.SetEnabled("my-ext", false)
	if s.IsEnabled("my-ext") {
		t.Error("my-ext should be disabled after toggle")
	}
	// enable a previously disabled ext
	s.SetEnabled("other-ext", true)
	if !s.IsEnabled("other-ext") {
		t.Error("other-ext should be enabled after toggle")
	}
}

func TestSetEnabledCreatesEntry(t *testing.T) {
	s := NewStore()
	s.SetEnabled("brand-new", false)
	if s.IsEnabled("brand-new") {
		t.Error("brand-new should be disabled after SetEnabled(false)")
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	bad := `extensions: [not a map`
	s := NewStore()
	if err := s.Load(strings.NewReader(bad)); err == nil {
		t.Error("expected error for malformed YAML")
	}
}

func TestAllReturnsSnapshot(t *testing.T) {
	s := NewStore()
	if err := s.Load(strings.NewReader(validYAML)); err != nil {
		t.Fatalf("Load: %v", err)
	}
	snap := s.All()
	// mutating snapshot must not affect store
	snap["my-ext"] = ExtensionConfig{Enabled: false}
	if !s.IsEnabled("my-ext") {
		t.Error("mutating All() snapshot should not affect store")
	}
}
