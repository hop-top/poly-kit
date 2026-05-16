package uxp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveStorePathTildeExpansion(t *testing.T) {
	reg := DefaultRegistry()
	got, err := ResolveStorePath(CLIClaude, reg)
	if err != nil {
		t.Fatalf("ResolveStorePath(claude): %v", err)
	}

	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".claude", "projects")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveStorePathAntigravity(t *testing.T) {
	reg := DefaultRegistry()
	got, err := ResolveStorePath(CLIAntigravity, reg)
	if err != nil {
		t.Fatalf("ResolveStorePath(antigravity): %v", err)
	}

	home, _ := os.UserHomeDir()
	want := filepath.Join(home, "Library", "Application Support", "Antigravity")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveStorePathEnvVar(t *testing.T) {
	reg := &CLIRegistry{
		m: map[CLIName]CLIInfo{
			"testcli": {
				Name:           "testcli",
				StoreRootPaths: StorePaths{Data: "$HOME/.testcli/data/"},
			},
		},
	}

	got, err := ResolveStorePath("testcli", reg)
	if err != nil {
		t.Fatalf("ResolveStorePath(testcli): %v", err)
	}

	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".testcli", "data")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveStorePathUnknownCLI(t *testing.T) {
	reg := DefaultRegistry()
	_, err := ResolveStorePath("nonexistent", reg)
	if err == nil {
		t.Fatal("expected error for unknown CLI")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention CLI name, got: %v", err)
	}
}

func TestResolveStorePathAbsolute(t *testing.T) {
	reg := DefaultRegistry()
	got, err := ResolveStorePath(CLIClaude, reg)
	if err != nil {
		t.Fatalf("ResolveStorePath(claude): %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("result should be absolute, got %q", got)
	}
}

// Regression: tilde expansion must land under home dir, not at root.
// Bug was: p[1:] on "~/.foo" yields "/.foo" which is absolute and
// filepath.Join(home, "/.foo") discards home entirely.
func TestResolveStorePath_TildeNotRoot(t *testing.T) {
	reg := &CLIRegistry{
		m: map[CLIName]CLIInfo{
			"tildetest": {
				Name:           "tildetest",
				StoreRootPaths: StorePaths{Data: "~/.tildetest/data/"},
			},
		},
	}

	got, err := ResolveStorePath("tildetest", reg)
	if err != nil {
		t.Fatalf("ResolveStorePath: %v", err)
	}

	home, _ := os.UserHomeDir()
	if !strings.HasPrefix(got, home) {
		t.Errorf("tilde path should start with home %q, got %q", home, got)
	}
	if got == "/.tildetest/data" {
		t.Error("tilde expansion produced root-relative path (regression)")
	}
}

// Regression: bare "~" must resolve to home dir without panic.
func TestResolveStorePath_BareTilde(t *testing.T) {
	reg := &CLIRegistry{
		m: map[CLIName]CLIInfo{
			"baretilde": {
				Name:           "baretilde",
				StoreRootPaths: StorePaths{Data: "~"},
			},
		},
	}

	got, err := ResolveStorePath("baretilde", reg)
	if err != nil {
		t.Fatalf("ResolveStorePath: %v", err)
	}

	home, _ := os.UserHomeDir()
	if got != home {
		t.Errorf("bare ~ should resolve to %q, got %q", home, got)
	}
}

// Regression: relative paths must still produce absolute results.
func TestResolveStorePath_RelativeBecomesAbsolute(t *testing.T) {
	reg := &CLIRegistry{
		m: map[CLIName]CLIInfo{
			"reltest": {
				Name:           "reltest",
				StoreRootPaths: StorePaths{Data: "relative/path"},
			},
		},
	}

	got, err := ResolveStorePath("reltest", reg)
	if err != nil {
		t.Fatalf("ResolveStorePath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("relative input must produce absolute output, got %q", got)
	}
}
