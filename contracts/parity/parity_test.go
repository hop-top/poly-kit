package parity_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// TestParityNoUnloadedBlocks is the drift guard: it fails when parity.json
// declares a top-level block the Go loader does not know about, and when
// parity.Blocks names a block parity.json no longer declares.
//
// Without it, a new block can be added to parity.json and read as enforced
// while nothing ever loads it — the exact failure mode that left verbosity,
// streams and table decorative. Any block added to parity.json must also get
// a parity.Data field and a parity.Blocks entry, or this test goes red.
//
// Keys beginning with "$" are JSON Schema metadata and are exempt.
func TestParityNoUnloadedBlocks(t *testing.T) {
	var declared map[string]json.RawMessage
	if err := json.Unmarshal(parity.Raw(), &declared); err != nil {
		t.Fatalf("parse embedded parity.json: %v", err)
	}

	known := make(map[string]bool, len(parity.Blocks))
	for _, b := range parity.Blocks {
		known[b] = true
	}

	for key := range declared {
		if strings.HasPrefix(key, "$") {
			continue // JSON Schema metadata, not content
		}
		if !known[key] {
			t.Errorf("parity.json declares block %q that the loader does not know.\n"+
				"parity.json is a loaded contract, not documentation: an unloaded block "+
				"is invisible to every test and every consumer.\n"+
				"Fix by adding a %q field to parity.Data and %q to parity.Blocks in "+
				"contracts/parity/parity.go — or, if the block is not a cross-language "+
				"constant, move it to prose in contracts/parity/README.md.", key, key, key)
		}
	}

	for _, b := range parity.Blocks {
		if _, ok := declared[b]; !ok {
			t.Errorf("parity.Blocks lists %q but parity.json no longer declares it; "+
				"drop it from parity.Blocks in contracts/parity/parity.go", b)
		}
	}
}

// TestParityLoadedBlocksNonZero asserts that every content block the loader
// knows actually unmarshalled into something. A block wired into Data with a
// mismatched json tag parses without error and leaves a zero value behind —
// silent, and exactly what this contract exists to prevent.
func TestParityLoadedBlocksNonZero(t *testing.T) {
	v := parity.Values

	if len(v.Status.Symbols) == 0 {
		t.Error("status.symbols: empty after load")
	}
	if len(v.Spinner.Frames) == 0 {
		t.Error("spinner.frames: empty after load")
	}
	if v.Anim.Runes == "" {
		t.Error("anim.runes: empty after load")
	}
	if len(v.Help.SectionOrder) == 0 || len(v.Help.Sections) == 0 {
		t.Error("help: section_order or sections empty after load")
	}
	if v.Verbosity.Flag == "" || len(v.Verbosity.Levels) == 0 || v.Verbosity.QuietOverride == "" {
		t.Errorf("verbosity: incompletely loaded: %+v", v.Verbosity)
	}
	if v.Streams.Flag == "" || v.Streams.LabelFormat == "" || v.Streams.Output == "" {
		t.Errorf("streams: incompletely loaded: %+v", v.Streams)
	}
}

// TestParityVerbosityValues pins the verbosity contract. These values are
// currently hardcoded identically in the Go, TS and Python ports; pinning
// them here makes an accidental change fail rather than silently diverge.
func TestParityVerbosityValues(t *testing.T) {
	v := parity.Values.Verbosity
	if v.Flag != "-V" {
		t.Errorf("verbosity.flag = %q, want %q", v.Flag, "-V")
	}
	want := map[string]string{"0": "info", "1": "debug", "2": "trace"}
	if !reflect.DeepEqual(v.Levels, want) {
		t.Errorf("verbosity.levels = %v, want %v", v.Levels, want)
	}
	if v.QuietOverride != "warn" {
		t.Errorf("verbosity.quiet_override = %q, want %q", v.QuietOverride, "warn")
	}
}

// TestParityStreamsValues pins the streams contract. The label format is the
// value each port reproduces when prefixing stream lines on stderr.
func TestParityStreamsValues(t *testing.T) {
	s := parity.Values.Streams
	if s.Flag != "--stream" {
		t.Errorf("streams.flag = %q, want %q", s.Flag, "--stream")
	}
	if s.LabelFormat != "[{name}]" {
		t.Errorf("streams.label_format = %q, want %q", s.LabelFormat, "[{name}]")
	}
	if s.Output != "stderr" {
		t.Errorf("streams.output = %q, want %q", s.Output, "stderr")
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
