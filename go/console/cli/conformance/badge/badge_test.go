package badge_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	leaf "hop.top/kit/go/console/cli/conformance/badge"
)

// matrixFixture is a 12/12-pass matrix file the leaf can consume.
const matrixFixture = `{
  "schemaVersion": 1,
  "factors": [
    {"n":1,"name":"Capability Introspection","tier":"must","status":"pass"},
    {"n":2,"name":"Intent Clarity","tier":"must","status":"pass"},
    {"n":3,"name":"Structured I/O","tier":"must","status":"pass"},
    {"n":4,"name":"Corrective Error Model","tier":"should","status":"pass"},
    {"n":5,"name":"Explicit Contracts","tier":"must","status":"pass"},
    {"n":6,"name":"Previewability","tier":"should","status":"pass"},
    {"n":7,"name":"Idempotency","tier":"must","status":"pass"},
    {"n":8,"name":"State Transparency","tier":"must","status":"pass"},
    {"n":9,"name":"Contextual Guidance","tier":"should","status":"pass"},
    {"n":10,"name":"Delegation Safety","tier":"must","status":"pass"},
    {"n":11,"name":"Provenance","tier":"should","status":"pass"},
    {"n":12,"name":"Evolution Guarantees","tier":"must","status":"pass"}
  ]
}`

// runLeaf invokes the leaf with the given args, returns the output
// path's contents and the leaf's stdout.
func runLeaf(t *testing.T, dir string, args ...string) (badgeJSON []byte, stdout string) {
	t.Helper()
	cmd := leaf.Cmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("leaf execute: %v\nstdout: %s", err, out.String())
	}
	// Determine output path: --output flag, default .12fcc.json.
	outPath := filepath.Join(dir, ".12fcc.json")
	for i, a := range args {
		if (a == "-o" || a == "--output") && i+1 < len(args) {
			outPath = args[i+1]
			if !filepath.IsAbs(outPath) {
				outPath = filepath.Join(dir, outPath)
			}
		}
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read badge JSON %s: %v", outPath, err)
	}
	return data, out.String()
}

func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return dir
}

func TestLeaf_EmitSeed_WritesUngradable(t *testing.T) {
	dir := chdirTemp(t)
	data, stdout := runLeaf(t, dir, "--emit-seed")
	if !strings.Contains(stdout, ".12fcc.json") {
		t.Errorf("stdout did not mention output path: %q", stdout)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("badge JSON not parseable: %v", err)
	}
	if got["message"] != "ungradable" || got["color"] != "lightgrey" {
		t.Errorf("seed payload = %v; want ungradable/lightgrey", got)
	}
}

func TestLeaf_FromMatrix_AllPass_Brightgreen(t *testing.T) {
	dir := chdirTemp(t)
	matrixPath := filepath.Join(dir, "matrix.json")
	if err := os.WriteFile(matrixPath, []byte(matrixFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	data, _ := runLeaf(t, dir, "--matrix", matrixPath)
	var got map[string]any
	_ = json.Unmarshal(data, &got)
	if got["message"] != "12/12 pass" || got["color"] != "brightgreen" {
		t.Errorf("payload = %v; want 12/12 pass + brightgreen", got)
	}
}

func TestLeaf_RespectsOutputFlag(t *testing.T) {
	dir := chdirTemp(t)
	custom := filepath.Join(dir, "public", "badge.json")
	if err := os.MkdirAll(filepath.Dir(custom), 0o755); err != nil {
		t.Fatal(err)
	}
	runLeaf(t, dir, "--emit-seed", "-o", custom)
	if _, err := os.Stat(custom); err != nil {
		t.Fatalf("custom output not written: %v", err)
	}
	// Default path should not be created.
	if _, err := os.Stat(filepath.Join(dir, ".12fcc.json")); !os.IsNotExist(err) {
		t.Errorf("default .12fcc.json was also created; want only custom path")
	}
}

func TestLeaf_RejectsMutuallyExclusiveFlags(t *testing.T) {
	dir := chdirTemp(t)
	matrix := filepath.Join(dir, "m.json")
	_ = os.WriteFile(matrix, []byte(matrixFixture), 0o644)
	cmd := leaf.Cmd()
	cmd.SetArgs([]string{"--emit-seed", "--matrix", matrix})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.ExecuteContext(t.Context())
	if err == nil {
		t.Fatal("ran with --emit-seed + --matrix; want error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err = %v; want it to mention mutually exclusive", err)
	}
}

func TestLeaf_RequiresOneMode(t *testing.T) {
	chdirTemp(t)
	cmd := leaf.Cmd()
	cmd.SetArgs(nil)
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.ExecuteContext(t.Context())
	if err == nil {
		t.Fatal("ran with no mode flags; want error")
	}
	if !strings.Contains(err.Error(), "--matrix") || !strings.Contains(err.Error(), "--emit-seed") {
		t.Errorf("err = %v; want mention of both --matrix and --emit-seed", err)
	}
}

func TestLeaf_RejectsUnknownTier(t *testing.T) {
	dir := chdirTemp(t)
	bad := strings.Replace(matrixFixture, `"tier":"must"`, `"tier":"required"`, 1)
	matrix := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(matrix, []byte(bad), 0o644)
	cmd := leaf.Cmd()
	cmd.SetArgs([]string{"--matrix", matrix})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.ExecuteContext(t.Context())
	if err == nil {
		t.Fatal("ran with unknown tier; want error")
	}
	if !strings.Contains(err.Error(), "tier") {
		t.Errorf("err = %v; want it to mention tier", err)
	}
}

func TestLeaf_RejectsEmptyTier(t *testing.T) {
	dir := chdirTemp(t)
	// Drop the tier value on factor 1 — JSON parses missing/empty as
	// empty string. Matrix-author footgun: empty tier must NOT silently
	// default to May, or a real MUST factor's failure would never red
	// the badge.
	missing := strings.Replace(matrixFixture,
		`"tier":"must","status":"pass"`,
		`"tier":"","status":"pass"`, 1)
	matrix := filepath.Join(dir, "missing-tier.json")
	if err := os.WriteFile(matrix, []byte(missing), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := leaf.Cmd()
	cmd.SetArgs([]string{"--matrix", matrix})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.ExecuteContext(t.Context())
	if err == nil {
		t.Fatal("ran with empty tier; want error")
	}
	if !strings.Contains(err.Error(), "tier") {
		t.Errorf("err = %v; want it to mention tier", err)
	}
}

func TestLeaf_RejectsMissingMatrixFile(t *testing.T) {
	chdirTemp(t)
	cmd := leaf.Cmd()
	cmd.SetArgs([]string{"--matrix", "does-not-exist.json"})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.ExecuteContext(t.Context())
	if err == nil {
		t.Fatal("ran with missing matrix; want error")
	}
}
