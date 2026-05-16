package grade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/conformance/client"
)

// TestGradeLeafSuccess wires the leaf against an httptest fixture
// server and asserts that a verdict=pass response prints clean JSON
// and exits 0.
func TestGradeLeafSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"scenario_id":    "t.leaf.pass",
				"verdict":        client.VerdictPass,
				"exit_code":      0,
				"grader_version": "1.0.0",
			},
		})
	}))
	defer srv.Close()

	dir := makeCassetteDir(t)
	cmd := Cmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{
		dir,
		"--service", srv.URL,
		"--token", "tk",
		"--format", "json",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "\"verdict\": \"pass\"") {
		t.Fatalf("stdout missing pass verdict: %s", stdout.String())
	}
}

// TestGradeLeafFailMapsExitCode asserts that verdict=fail returns an
// error whose AsCLIError envelope reports ExitCode 2 (GRADE_FAIL).
func TestGradeLeafFailMapsExitCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"scenario_id": "t.leaf.fail",
				"verdict":     client.VerdictFail,
				"reason":      "3 assertions failed",
			},
		})
	}))
	defer srv.Close()

	dir := makeCassetteDir(t)
	cmd := Cmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{dir, "--service", srv.URL, "--format", "human"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil; want GradeFailError")
	}
	if !errors.Is(err, client.ErrGradeFail) {
		t.Fatalf("err = %v, want errors.Is ErrGradeFail", err)
	}
}

// TestGradeLeafMissingService asserts the no-default-URL contract at
// the leaf surface: missing --service => usage error.
func TestGradeLeafMissingService(t *testing.T) {
	dir := makeCassetteDir(t)
	cmd := Cmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{dir})

	// Ensure env is clean.
	t.Setenv("KIT_CONFORMANCE_SERVICE", "")

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil; want usage error")
	}
	if !strings.Contains(err.Error(), "--service URL is required") {
		t.Fatalf("err = %v, want usage error mentioning --service", err)
	}
}

// TestGradeLeafCIFlipsToJSON exercises the auto-format CI flip: when
// CI=<truthy> and --format is not explicitly passed, the leaf emits
// JSON.
func TestGradeLeafCIFlipsToJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"scenario_id": "t.leaf.ci",
				"verdict":     client.VerdictPass,
			},
		})
	}))
	defer srv.Close()

	t.Setenv("CI", "true")
	dir := makeCassetteDir(t)
	cmd := Cmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{dir, "--service", srv.URL})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.HasPrefix(stdout.String(), "{") {
		t.Fatalf("CI did not flip to JSON; stdout=%s", stdout.String())
	}
}

// TestGradeLeafBadFormat asserts validation rejects unknown format.
// After the --format consolidation the kit-wide registry accepts
// human|json|yaml|table|csv|text, so the rejection probe uses a
// genuinely unknown key (xml).
func TestGradeLeafBadFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{"scenario_id": "t.fmt", "verdict": client.VerdictPass},
		})
	}))
	defer srv.Close()

	dir := makeCassetteDir(t)
	cmd := Cmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{dir, "--service", srv.URL, "--format", "xml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil; want usage error")
	}
	if !strings.Contains(err.Error(), "format") {
		t.Fatalf("err = %v, want format error", err)
	}
}

// TestGradeLeafBadTier asserts --tier=4 is rejected.
func TestGradeLeafBadTier(t *testing.T) {
	dir := makeCassetteDir(t)
	cmd := Cmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{dir, "--service", "https://x", "--tier", "4"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "tier") {
		t.Fatalf("err = %v, want tier validation", err)
	}
}

func makeCassetteDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"),
		[]byte("schema_version: \"1\"\nscenario_id: t.leaf\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
