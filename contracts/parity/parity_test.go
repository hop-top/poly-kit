package parity_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hop.top/kit/contracts/parity"
)

func TestParityStatusSymbols(t *testing.T) {
	kinds := []string{"info", "success", "error", "warn"}
	for _, k := range kinds {
		sym, ok := parity.Values.Status.Symbols[k]
		if !ok || sym == "" {
			t.Errorf("status.symbols[%q]: missing or empty", k)
		}
	}
}

func TestParityStatusSymbolValues(t *testing.T) {
	// Pin the exact values so any accidental change fails the test.
	want := map[string]string{
		"info":    "ℹ",
		"success": "✓",
		"error":   "●",
		"warn":    "▲",
	}
	for k, v := range want {
		if got := parity.Values.Status.Symbols[k]; got != v {
			t.Errorf("status.symbols[%q] = %q, want %q", k, got, v)
		}
	}
}

func TestParitySpinnerFrames(t *testing.T) {
	frames := parity.Values.Spinner.Frames
	if len(frames) == 0 {
		t.Fatal("spinner.frames: empty")
	}
	if parity.Values.Spinner.IntervalMs <= 0 {
		t.Errorf("spinner.interval_ms = %d, want > 0", parity.Values.Spinner.IntervalMs)
	}
}

func TestParityAnimRunes(t *testing.T) {
	if parity.Values.Anim.Runes == "" {
		t.Fatal("anim.runes: empty")
	}
	if parity.Values.Anim.IntervalMs <= 0 {
		t.Errorf("anim.interval_ms = %d, want > 0", parity.Values.Anim.IntervalMs)
	}
	if parity.Values.Anim.DefaultWidth <= 0 {
		t.Errorf("anim.default_width = %d, want > 0", parity.Values.Anim.DefaultWidth)
	}
}

// TestScopeDefaultsContractSync asserts that every per-language local copy of
// scope-defaults.json is byte-equal (after JSON parse) to the canonical
// contracts/parity/scope-defaults.json. Drift means a port has gone out of
// sync — fix by re-copying from contracts/parity/.
func TestScopeDefaultsContractSync(t *testing.T) {
	root := repoRoot(t)
	canonical := readJSON(t, filepath.Join(root, "contracts", "parity", "scope-defaults.json"))

	copies := map[string]string{
		"go/core/scope/scope-defaults.json":      filepath.Join(root, "go", "core", "scope", "scope-defaults.json"),
		"sdk/ts/src/scope-defaults.json":         filepath.Join(root, "sdk", "ts", "src", "scope-defaults.json"),
		"sdk/py/hop_top_kit/scope-defaults.json": filepath.Join(root, "sdk", "py", "hop_top_kit", "scope-defaults.json"),
	}

	for label, path := range copies {
		got := readJSON(t, path)
		if !reflect.DeepEqual(got, canonical) {
			t.Errorf("%s drifted from canonical contracts/parity/scope-defaults.json", label)
		}
	}
}

// TestScopeDefaultsRegistered asserts that scope-defaults.json appears in
// parity.json's "extends" list — the registry of contracts the parity suite
// covers.
func TestScopeDefaultsRegistered(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "parity", "parity.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Extends []string `json:"extends"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, name := range v.Extends {
		if name == "scope-defaults.json" {
			return
		}
	}
	t.Fatal("contracts/parity/parity.json: extends[] missing scope-defaults.json")
}

func readJSON(t *testing.T, path string) any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return v
}

// repoRoot returns the repo root by walking up from the test file location
// until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repoRoot: walked to /, no go.mod found")
		}
		dir = parent
	}
}
