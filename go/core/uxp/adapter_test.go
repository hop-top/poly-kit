package uxp

import "testing"

// Support enum values.
func TestSupportValues(t *testing.T) {
	if Native != 0 {
		t.Errorf("Native = %d, want 0", Native)
	}
	if Workaround != 1 {
		t.Errorf("Workaround = %d, want 1", Workaround)
	}
	if Missing != 2 {
		t.Errorf("Missing = %d, want 2", Missing)
	}
}

func TestSupportString(t *testing.T) {
	tests := []struct {
		s    Support
		want string
	}{
		{Native, "native"},
		{Workaround, "workaround"},
		{Missing, "missing"},
		{Support(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("Support(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

// mockCapabilityMap implements CapabilityMap for testing.
type mockCapabilityMap struct {
	caps map[string]Support
}

func (m *mockCapabilityMap) Supports(dim string) bool {
	s, ok := m.caps[dim]
	return ok && s != Missing
}

func (m *mockCapabilityMap) Coverage() map[string]Support {
	return m.caps
}

func TestCapabilityMap_Supports(t *testing.T) {
	cm := &mockCapabilityMap{caps: map[string]Support{
		"mcp":       Native,
		"streaming": Workaround,
		"plugins":   Missing,
	}}

	if !cm.Supports("mcp") {
		t.Error("expected mcp supported")
	}
	if !cm.Supports("streaming") {
		t.Error("expected streaming supported (workaround)")
	}
	if cm.Supports("plugins") {
		t.Error("expected plugins not supported")
	}
	if cm.Supports("unknown-dim") {
		t.Error("expected unknown dimension not supported")
	}
}

func TestCapabilityMap_Coverage(t *testing.T) {
	caps := map[string]Support{
		"mcp":       Native,
		"streaming": Workaround,
	}
	cm := &mockCapabilityMap{caps: caps}

	cov := cm.Coverage()
	if len(cov) != 2 {
		t.Fatalf("Coverage() len = %d, want 2", len(cov))
	}
	if cov["mcp"] != Native {
		t.Errorf("mcp = %v, want Native", cov["mcp"])
	}
	if cov["streaming"] != Workaround {
		t.Errorf("streaming = %v, want Workaround", cov["streaming"])
	}
}

// mockAdapter satisfies the Adapter interface.
type mockAdapter struct {
	cli  CLIName
	det  *DetectResult
	caps CapabilityMap
}

func (m *mockAdapter) CLI() CLIName                   { return m.cli }
func (m *mockAdapter) Detect() (*DetectResult, error) { return m.det, nil }
func (m *mockAdapter) Capabilities() CapabilityMap    { return m.caps }

func TestMockAdapterSatisfiesInterface(t *testing.T) {
	det := &DetectResult{
		Installed:   true,
		Version:     "1.0.0",
		BinaryPath:  "/usr/local/bin/claude",
		ConfigPaths: []string{"~/.config/claude"},
	}
	caps := &mockCapabilityMap{caps: map[string]Support{"mcp": Native}}

	var a Adapter = &mockAdapter{
		cli:  CLIClaude,
		det:  det,
		caps: caps,
	}

	if a.CLI() != CLIClaude {
		t.Errorf("CLI() = %q, want %q", a.CLI(), CLIClaude)
	}

	result, err := a.Detect()
	if err != nil {
		t.Fatalf("Detect() error: %v", err)
	}
	if !result.Installed {
		t.Error("expected Installed=true")
	}
	if result.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", result.Version, "1.0.0")
	}

	if !a.Capabilities().Supports("mcp") {
		t.Error("expected mcp supported")
	}
}

func TestDetectResult_Errors(t *testing.T) {
	dr := &DetectResult{
		Installed: false,
		Errors:    []string{"binary not found", "config missing"},
	}

	if dr.Installed {
		t.Error("expected not installed")
	}
	if len(dr.Errors) != 2 {
		t.Fatalf("Errors len = %d, want 2", len(dr.Errors))
	}
}
