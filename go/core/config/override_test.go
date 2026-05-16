package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseOverrides_Empty(t *testing.T) {
	got, err := ParseOverrides(nil)
	if err != nil {
		t.Fatalf("nil pairs: unexpected err: %v", err)
	}
	if got != nil {
		t.Fatalf("nil pairs: want nil map, got %v", got)
	}
}

func TestParseOverrides_Scalars(t *testing.T) {
	got, err := ParseOverrides([]string{
		`s=hello`,
		`n=3`,
		`b=true`,
		`f=1.5`,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := map[string]any{
		"s": "hello",
		"n": 3,
		"b": true,
		"f": 1.5,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scalars: got %#v want %#v", got, want)
	}
}

func TestParseOverrides_NestedDottedKeys(t *testing.T) {
	got, err := ParseOverrides([]string{
		`a.b.c=1`,
		`a.b.d=2`,
		`a.e=hello`,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := map[string]any{
		"a": map[string]any{
			"b": map[string]any{"c": 1, "d": 2},
			"e": "hello",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nested: got %#v want %#v", got, want)
	}
}

func TestParseOverrides_StructuredValues(t *testing.T) {
	got, err := ParseOverrides([]string{
		`tags=["a","b","c"]`,
		`m={k1: v1, k2: v2}`,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	tags, ok := got["tags"].([]any)
	if !ok || len(tags) != 3 || tags[0] != "a" {
		t.Fatalf("tags: got %#v", got["tags"])
	}
	m, ok := got["m"].(map[string]any)
	if !ok || m["k1"] != "v1" || m["k2"] != "v2" {
		t.Fatalf("m: got %#v", got["m"])
	}
}

func TestParseOverrides_LiteralFallback(t *testing.T) {
	// A leading '*' makes YAML attempt to resolve an alias and fail; the parser
	// should fall back to the literal string rather than erroring.
	got, err := ParseOverrides([]string{`x=*not-an-alias`})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got["x"] != "*not-an-alias" {
		t.Fatalf("literal fallback: got %#v", got["x"])
	}
}

func TestParseOverrides_LaterWins(t *testing.T) {
	got, err := ParseOverrides([]string{`k=first`, `k=second`})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got["k"] != "second" {
		t.Fatalf("later-wins: got %#v", got["k"])
	}
}

func TestParseOverrides_BadInput(t *testing.T) {
	cases := []string{
		"no-equals",
		"=value-only",
	}
	for _, p := range cases {
		if _, err := ParseOverrides([]string{p}); err == nil {
			t.Errorf("ParseOverrides(%q) = nil err; want err", p)
		}
	}
}

func TestParseOverrides_ConflictSegment(t *testing.T) {
	// Set a scalar at a key, then try to set a child of it.
	_, err := ParseOverrides([]string{`a=1`, `a.b=2`})
	if err == nil {
		t.Fatalf("expected conflict error, got nil")
	}
}

type sample struct {
	Model   string   `yaml:"model"`
	Retries int      `yaml:"retries"`
	Tags    []string `yaml:"tags"`
	Nested  struct {
		Inherit string `yaml:"inherit"`
	} `yaml:"nested"`
}

func TestApplyOverrides_OverwritesScalarsAndSlices(t *testing.T) {
	cfg := sample{
		Model:   "default",
		Retries: 1,
		Tags:    []string{"old"},
	}
	cfg.Nested.Inherit = "none"

	overrides, err := ParseOverrides([]string{
		`model=o3`,
		`retries=5`,
		`tags=["a","b"]`,
		`nested.inherit=all`,
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	if err := ApplyOverrides(&cfg, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}

	if cfg.Model != "o3" {
		t.Errorf("Model: want o3, got %q", cfg.Model)
	}
	if cfg.Retries != 5 {
		t.Errorf("Retries: want 5, got %d", cfg.Retries)
	}
	if !reflect.DeepEqual(cfg.Tags, []string{"a", "b"}) {
		t.Errorf("Tags: want [a b], got %v", cfg.Tags)
	}
	if cfg.Nested.Inherit != "all" {
		t.Errorf("Nested.Inherit: want all, got %q", cfg.Nested.Inherit)
	}
}

func TestApplyOverrides_NilIsNoOp(t *testing.T) {
	cfg := sample{Model: "keep"}
	if err := ApplyOverrides(&cfg, nil); err != nil {
		t.Fatalf("ApplyOverrides(nil): %v", err)
	}
	if cfg.Model != "keep" {
		t.Errorf("Model: want keep, got %q", cfg.Model)
	}
}

func TestParseConfigArgs_SplitsPathsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "extra.yaml")
	if err := os.WriteFile(fp, []byte("model: from-file\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	paths, overrides, err := ParseConfigArgs([]string{
		`model=o3`,
		fp,
		`retries=5`,
	})
	if err != nil {
		t.Fatalf("ParseConfigArgs: %v", err)
	}
	if got, want := paths, []string{fp}; !reflect.DeepEqual(got, want) {
		t.Errorf("paths: got %v want %v", got, want)
	}
	if overrides["model"] != "o3" || overrides["retries"] != 5 {
		t.Errorf("overrides: got %#v", overrides)
	}
}

func TestParseConfigArgs_MissingPathErrors(t *testing.T) {
	_, _, err := ParseConfigArgs([]string{"/nonexistent/file/x.yaml"})
	if err == nil {
		t.Fatalf("missing path: expected error, got nil")
	}
}

func TestParseConfigArgs_DirectoryRejected(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := ParseConfigArgs([]string{dir}); err == nil {
		t.Fatalf("directory: expected error, got nil")
	}
}

// T-0433: bare-directory token resolves to <dir>/<projectMarker>
// when WithProjectMarker is set and the marker file exists.
func TestParseConfigArgs_DirectoryResolvedViaProjectMarker(t *testing.T) {
	dir := t.TempDir()
	markerRel := filepath.Join(".rlz", "config.yaml")
	markerAbs := filepath.Join(dir, markerRel)
	if err := os.MkdirAll(filepath.Dir(markerAbs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(markerAbs, []byte("model: from-marker\n"), 0o600); err != nil {
		t.Fatalf("seed marker: %v", err)
	}

	paths, _, err := ParseConfigArgs([]string{dir}, WithProjectMarker(markerRel))
	if err != nil {
		t.Fatalf("ParseConfigArgs(dir + marker): %v", err)
	}
	if got, want := paths, []string{markerAbs}; !reflect.DeepEqual(got, want) {
		t.Errorf("paths: got %v want %v", got, want)
	}
}

// T-0433: bare-directory token errors clearly when WithProjectMarker
// is set but the marker file is missing under the supplied directory.
// The error mentions the resolved path so the user can fix their
// inputs.
func TestParseConfigArgs_DirectoryWithMissingMarkerErrors(t *testing.T) {
	dir := t.TempDir()
	markerRel := filepath.Join(".rlz", "config.yaml")
	_, _, err := ParseConfigArgs([]string{dir}, WithProjectMarker(markerRel))
	if err == nil {
		t.Fatalf("missing marker: expected error, got nil")
	}
	want := filepath.Join(dir, markerRel)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error should mention resolved path %q; got: %v", want, err)
	}
	if !strings.Contains(err.Error(), markerRel) {
		t.Errorf("error should mention project marker %q; got: %v", markerRel, err)
	}
}

// T-0433: file-path tokens still work when WithProjectMarker is set
// — the option only affects the bare-directory resolution branch.
func TestParseConfigArgs_FilePathStillWorksWithProjectMarker(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "extra.yaml")
	if err := os.WriteFile(fp, []byte("model: from-file\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	paths, _, err := ParseConfigArgs(
		[]string{fp},
		WithProjectMarker(filepath.Join(".rlz", "config.yaml")),
	)
	if err != nil {
		t.Fatalf("ParseConfigArgs(file + marker): %v", err)
	}
	if got, want := paths, []string{fp}; !reflect.DeepEqual(got, want) {
		t.Errorf("paths: got %v want %v", got, want)
	}
}

// T-0433: bare-directory token still errors (preserves legacy
// behavior) when no project marker is configured.
func TestParseConfigArgs_DirectoryWithoutMarkerStillErrors(t *testing.T) {
	dir := t.TempDir()
	_, _, err := ParseConfigArgs([]string{dir})
	if err == nil {
		t.Fatalf("directory without marker: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error should mention directory; got: %v", err)
	}
}

// T-0433: an absolute project marker is rejected as a programmer
// error so adopters don't accidentally configure a global file.
func TestParseConfigArgs_AbsoluteProjectMarkerRejected(t *testing.T) {
	dir := t.TempDir()
	_, _, err := ParseConfigArgs(
		[]string{dir},
		WithProjectMarker("/etc/global/config.yaml"),
	)
	if err == nil {
		t.Fatalf("absolute marker: expected error, got nil")
	}
}

// T-0433: = override tokens are unaffected by ProjectMarker.
func TestParseConfigArgs_OverrideTokensUnaffectedByProjectMarker(t *testing.T) {
	_, overrides, err := ParseConfigArgs(
		[]string{"model=o3"},
		WithProjectMarker(filepath.Join(".rlz", "config.yaml")),
	)
	if err != nil {
		t.Fatalf("ParseConfigArgs: %v", err)
	}
	if overrides["model"] != "o3" {
		t.Errorf("overrides: got %#v", overrides)
	}
}

func TestParseConfigArgs_EmptyKey(t *testing.T) {
	if _, _, err := ParseConfigArgs([]string{"=value"}); err == nil {
		t.Fatalf("empty key: expected error, got nil")
	}
}

func TestLoad_ExtraConfigPathsLayerAfterProject(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "project.yaml")
	if err := os.WriteFile(project, []byte("model: project\nretries: 1\n"), 0o600); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	extra := filepath.Join(dir, "extra.yaml")
	if err := os.WriteFile(extra, []byte("model: extra\n"), 0o600); err != nil {
		t.Fatalf("seed extra: %v", err)
	}

	var cfg sample
	if err := Load(&cfg, Options{
		ProjectConfigPath: project,
		ExtraConfigPaths:  []string{extra},
	}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model != "extra" {
		t.Errorf("Model: extra config must win over project, got %q", cfg.Model)
	}
	if cfg.Retries != 1 {
		t.Errorf("Retries: project value should survive, got %d", cfg.Retries)
	}
}

func TestLoad_OverridesWinOverExtraPaths(t *testing.T) {
	dir := t.TempDir()
	extra := filepath.Join(dir, "extra.yaml")
	if err := os.WriteFile(extra, []byte("model: from-file\n"), 0o600); err != nil {
		t.Fatalf("seed extra: %v", err)
	}
	overrides, err := ParseOverrides([]string{`model=from-cli`})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}

	var cfg sample
	if err := Load(&cfg, Options{
		ExtraConfigPaths: []string{extra},
		Overrides:        overrides,
	}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model != "from-cli" {
		t.Errorf("Model: -c override must win over -c file, got %q", cfg.Model)
	}
}

func TestLoad_ExtraConfigMissingFileIsHardError(t *testing.T) {
	var cfg sample
	err := Load(&cfg, Options{ExtraConfigPaths: []string{"/nonexistent/file.yaml"}})
	if err == nil {
		t.Fatalf("missing extra config: expected error, got nil")
	}
}
