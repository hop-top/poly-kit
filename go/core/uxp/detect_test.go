package uxp

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// stubExecRunner returns canned output for known binaries.
type stubExecRunner struct {
	binaries map[string]string // binary -> version output
}

func (s *stubExecRunner) Run(name string, args ...string) ([]byte, error) {
	out, ok := s.binaries[name]
	if !ok {
		return nil, fmt.Errorf("exec: %q: not found", name)
	}
	return []byte(out), nil
}

// stubLookPather returns canned paths for known binaries.
type stubLookPather struct {
	paths map[string]string
}

func (s *stubLookPather) LookPath(file string) (string, error) {
	p, ok := s.paths[file]
	if !ok {
		return "", fmt.Errorf("lookpath: %q: not found", file)
	}
	return p, nil
}

func TestParseVersion_ClaudeFormat(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Claude Code 1.0.12\n", "1.0.12"},
		{"claude code 2.3.4", "2.3.4"},
		{"v1.2.3", "1.2.3"},
		{"1.2.3", "1.2.3"},
		{"something 0.10.0-beta.1\n", "0.10.0-beta.1"},
		{"no version here", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseVersion(tt.input)
			if got != tt.want {
				t.Errorf("parseVersion(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestDetect_FoundWithVersion(t *testing.T) {
	runner := &stubExecRunner{
		binaries: map[string]string{
			"claude": "Claude Code 1.0.12\n",
		},
	}
	lp := &stubLookPather{
		paths: map[string]string{
			"claude": "/usr/local/bin/claude",
		},
	}

	reg := &CLIRegistry{
		m: map[CLIName]CLIInfo{
			CLIClaude: {
				Name:        CLIClaude,
				BinaryNames: []string{"claude"},
				StoreRootPaths: StorePaths{
					Data: t.TempDir(),
				},
			},
		},
	}

	result, err := Detect(CLIClaude, reg, &DetectOpts{
		Runner: runner, LookPath: lp,
	})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !result.Installed {
		t.Error("expected Installed=true")
	}
	if result.Version != "1.0.12" {
		t.Errorf("Version = %q, want %q", result.Version, "1.0.12")
	}
	if result.BinaryPath != "/usr/local/bin/claude" {
		t.Errorf("BinaryPath = %q, want /usr/local/bin/claude", result.BinaryPath)
	}
}

func TestDetect_NotInstalled(t *testing.T) {
	runner := &stubExecRunner{
		binaries: map[string]string{},
	}

	reg := &CLIRegistry{
		m: map[CLIName]CLIInfo{
			CLIClaude: {
				Name:        CLIClaude,
				BinaryNames: []string{"claude"},
			},
		},
	}

	result, err := Detect(CLIClaude, reg, &DetectOpts{Runner: runner})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if result.Installed {
		t.Error("expected Installed=false")
	}
	if len(result.Errors) == 0 {
		t.Error("expected at least one error")
	}
}

func TestDetect_UnknownCLI(t *testing.T) {
	reg := &CLIRegistry{m: map[CLIName]CLIInfo{}}

	_, err := Detect("nonexistent", reg, nil)
	if err == nil {
		t.Fatal("expected error for unknown CLI")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention CLI name: %v", err)
	}
}

func TestDetect_NilOpts(t *testing.T) {
	reg := &CLIRegistry{
		m: map[CLIName]CLIInfo{
			"echo": {
				Name:        "echo",
				BinaryNames: []string{"echo"},
			},
		},
	}

	// nil opts should use defaults without panic.
	result, err := Detect("echo", reg, nil)
	if err != nil {
		t.Fatalf("Detect with nil opts: %v", err)
	}
	// echo --version may or may not work; just verify no panic.
	_ = result
}

func TestDetect_FallbackBinary(t *testing.T) {
	runner := &stubExecRunner{
		binaries: map[string]string{
			"agy": "Antigravity 0.5.0\n",
		},
	}
	lp := &stubLookPather{
		paths: map[string]string{
			"agy": "/usr/local/bin/agy",
		},
	}

	reg := &CLIRegistry{
		m: map[CLIName]CLIInfo{
			CLIAntigravity: {
				Name:        CLIAntigravity,
				BinaryNames: []string{"antigravity", "agy"},
				StoreRootPaths: StorePaths{
					Data: t.TempDir(),
				},
			},
		},
	}

	result, err := Detect(CLIAntigravity, reg, &DetectOpts{
		Runner: runner, LookPath: lp,
	})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !result.Installed {
		t.Error("expected Installed=true via fallback binary")
	}
	if result.Version != "0.5.0" {
		t.Errorf("Version = %q, want %q", result.Version, "0.5.0")
	}
}

func TestDetect_StorePathMissing(t *testing.T) {
	runner := &stubExecRunner{
		binaries: map[string]string{
			"claude": "Claude Code 1.0.0\n",
		},
	}
	lp := &stubLookPather{
		paths: map[string]string{"claude": "/usr/local/bin/claude"},
	}

	reg := &CLIRegistry{
		m: map[CLIName]CLIInfo{
			CLIClaude: {
				Name:           CLIClaude,
				BinaryNames:    []string{"claude"},
				StoreRootPaths: StorePaths{Data: "/nonexistent/path/xyz"},
			},
		},
	}

	result, err := Detect(CLIClaude, reg, &DetectOpts{
		Runner: runner, LookPath: lp,
	})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !result.Installed {
		t.Error("expected Installed=true (binary found)")
	}
}

// Regression: LookPath must be injected so tests don't depend on host PATH.
func TestDetect_LookPathInjected(t *testing.T) {
	runner := &stubExecRunner{
		binaries: map[string]string{"claude": "Claude Code 1.0.0\n"},
	}
	lp := &stubLookPather{
		paths: map[string]string{"claude": "/custom/path/claude"},
	}

	reg := &CLIRegistry{
		m: map[CLIName]CLIInfo{
			CLIClaude: {
				Name:           CLIClaude,
				BinaryNames:    []string{"claude"},
				StoreRootPaths: StorePaths{Data: t.TempDir()},
			},
		},
	}

	result, err := Detect(CLIClaude, reg, &DetectOpts{
		Runner: runner, LookPath: lp,
	})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if result.BinaryPath != "/custom/path/claude" {
		t.Errorf("BinaryPath = %q, want /custom/path/claude (LookPath not injected?)", result.BinaryPath)
	}
}

// Regression: store path check must use ResolveStorePath, not inline expansion.
func TestDetect_StoreCheckUsesResolveStorePath(t *testing.T) {
	runner := &stubExecRunner{
		binaries: map[string]string{"testcli": "v1.0.0\n"},
	}
	lp := &stubLookPather{
		paths: map[string]string{"testcli": "/bin/testcli"},
	}

	// Use tilde path — only ResolveStorePath handles this correctly.
	reg := &CLIRegistry{
		m: map[CLIName]CLIInfo{
			"testcli": {
				Name:           "testcli",
				BinaryNames:    []string{"testcli"},
				StoreRootPaths: StorePaths{Data: "~/.nonexistent-test-path-xyz"},
			},
		},
	}

	result, err := Detect("testcli", reg, &DetectOpts{
		Runner: runner, LookPath: lp,
	})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	// The error message should contain an expanded path (with home dir),
	// not the raw "~/" — proving ResolveStorePath was used.
	for _, e := range result.Errors {
		if strings.Contains(e, "~/") {
			t.Errorf("store error contains unexpanded tilde: %s", e)
		}
	}
}

func TestDefaultExecRunner_Run(t *testing.T) {
	runner := &DefaultExecRunner{}

	out, err := runner.Run("echo", "hello")
	if err != nil {
		if _, lookErr := exec.LookPath("echo"); lookErr != nil {
			t.Skip("echo not in PATH")
		}
		t.Fatalf("Run(echo hello): %v", err)
	}
	if !strings.Contains(string(out), "hello") {
		t.Errorf("output = %q, want contains 'hello'", out)
	}
}
