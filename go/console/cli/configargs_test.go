package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/console/cli"
)

// T-0433: Root.ConfigArgs returns the parse error directly so adopters
// can surface it. Pre-fix the call swallowed errors and returned (nil,
// nil), which dropped -c flags silently.
func TestRoot_ConfigArgs_SurfacesParseError(t *testing.T) {
	r := cli.New(cli.Config{Name: "tool", Version: "1.0.0", Short: "x", DisableValidate: true})
	r.Viper.Set("config", []string{"no-equals-and-no-such-file"})

	paths, overrides, err := r.ConfigArgs()
	if err == nil {
		t.Fatalf("ConfigArgs should surface parse error; got nil")
	}
	if paths != nil || overrides != nil {
		t.Errorf("ConfigArgs on parse error must return nil paths + overrides; got paths=%v overrides=%v",
			paths, overrides)
	}
}

// T-0433: when ProjectMarker is configured, a bare-directory token
// resolves to <dir>/<marker>.
func TestRoot_ConfigArgs_ProjectMarkerResolvesDirectory(t *testing.T) {
	dir := t.TempDir()
	markerRel := filepath.Join(".rlz", "config.yaml")
	markerAbs := filepath.Join(dir, markerRel)
	if err := os.MkdirAll(filepath.Dir(markerAbs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(markerAbs, []byte("model: x\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := cli.New(cli.Config{
		Name:            "tool",
		Version:         "1.0.0",
		Short:           "x",
		ProjectMarker:   markerRel,
		DisableValidate: true,
	})
	r.Viper.Set("config", []string{dir})

	paths, _, err := r.ConfigArgs()
	if err != nil {
		t.Fatalf("ConfigArgs: %v", err)
	}
	if len(paths) != 1 || paths[0] != markerAbs {
		t.Errorf("paths: got %v want %v", paths, []string{markerAbs})
	}
}

// T-0433: missing project marker produces a clear error mentioning
// the resolved path.
func TestRoot_ConfigArgs_MissingProjectMarkerErrors(t *testing.T) {
	dir := t.TempDir()
	markerRel := filepath.Join(".rlz", "config.yaml")

	r := cli.New(cli.Config{
		Name:            "tool",
		Version:         "1.0.0",
		Short:           "x",
		ProjectMarker:   markerRel,
		DisableValidate: true,
	})
	r.Viper.Set("config", []string{dir})

	_, _, err := r.ConfigArgs()
	if err == nil {
		t.Fatalf("expected error for missing marker")
	}
	wantPath := filepath.Join(dir, markerRel)
	if !strings.Contains(err.Error(), wantPath) {
		t.Errorf("error should mention resolved path %q; got: %v", wantPath, err)
	}
}

// T-0433: Root.Validate() surfaces -c parse errors. Pre-fix the
// comment said it did, but the implementation swallowed the error.
func TestRoot_Validate_SurfacesConfigArgsError(t *testing.T) {
	r := cli.New(cli.Config{Name: "tool", Version: "1.0.0", Short: "x", DisableValidate: true})
	r.Viper.Set("config", []string{"no-equals-and-no-such-file"})

	err := r.Validate()
	if err == nil {
		t.Fatalf("Validate should surface -c parse failure; got nil")
	}
	if !strings.Contains(err.Error(), "config") {
		t.Errorf("Validate error should mention config; got: %v", err)
	}
}

// T-0433: Validate clears past errors on a subsequent successful
// ConfigArgs call (e.g. after the user fixes their flag and calls
// Validate again from a long-running test harness).
func TestRoot_Validate_ClearsErrorOnSuccessfulRecheck(t *testing.T) {
	r := cli.New(cli.Config{Name: "tool", Version: "1.0.0", Short: "x", DisableValidate: true})
	r.Viper.Set("config", []string{"no-equals-and-no-such-file"})
	if err := r.Validate(); err == nil {
		t.Fatalf("first Validate should fail")
	}

	// Fix the flag and re-validate.
	r.Viper.Set("config", []string{"model=o3"})
	if _, _, err := r.ConfigArgs(); err != nil {
		t.Fatalf("ConfigArgs: %v", err)
	}
	if err := r.Validate(); err != nil {
		// Validate may still fail for unrelated reasons (no leaves),
		// but it must NOT return the stale config-args error.
		if strings.Contains(err.Error(), "no-equals-and-no-such-file") ||
			strings.Contains(err.Error(), "invalid -c/--config flag") {
			t.Fatalf("Validate returned stale config-args error: %v", err)
		}
	}
}
