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
	"hop.top/kit/go/conformance/scenario"
	"hop.top/kit/go/console/output"
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
// error whose AsCLIError envelope reports the GRADE_FAIL band code
// (68) rather than colliding with the shared usage slot.
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
	conv, ok := err.(interface{ AsCLIError() *output.Error })
	if !ok {
		t.Fatal("grade-fail error must implement AsCLIError")
	}
	if got := conv.AsCLIError().ExitCode; got != client.ExitGradeFail {
		t.Fatalf("ExitCode = %d, want %d (GRADE_FAIL band code)", got, client.ExitGradeFail)
	}
}

// TestGradeLeafFailShowsAssertionTraces asserts a failing grade's
// per-assertion detail — the WHY behind each red facet — reaches CLI
// consumers in both human and JSON output. The fixture reply mirrors
// svc's wire shape exactly: the grader's scenario.Result (with its
// assertions[] trace) serialized under the "result" key.
func TestGradeLeafFailShowsAssertionTraces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": &scenario.Result{
				ScenarioID: "t.leaf.trace",
				Verdict:    scenario.VerdictFail,
				Reason:     "1 of 1 assertions failed",
				Tier:       3,
				Facets:     []scenario.FactorFacet{{Factor: 11, Status: scenario.StatusFail}},
				Assertions: []scenario.AssertionResult{{
					ID: "exits-zero", Kind: "exit_code_equals", Factor: 11,
					Status: scenario.StatusFail, Observed: 3, Expected: 0,
					Message: "exit code 3 != 0",
				}},
			},
			"service": map[string]string{"version": "0.1.0"},
		})
	}))
	defer srv.Close()

	run := func(format string) string {
		t.Helper()
		dir := makeCassetteDir(t)
		cmd := Cmd()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetContext(context.Background())
		cmd.SetArgs([]string{dir, "--service", srv.URL, "--format", format})
		err := cmd.Execute()
		if !errors.Is(err, client.ErrGradeFail) {
			t.Fatalf("Execute err = %v, want errors.Is ErrGradeFail\nstderr=%s", err, stderr.String())
		}
		return stdout.String()
	}

	human := run("human")
	for _, want := range []string{
		"failing assertions:",
		"[exits-zero]",
		"exit_code_equals",
		"expected 0, observed 3",
		"exit code 3 != 0",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("human output missing %q:\n%s", want, human)
		}
	}

	jsonOut := run("json")
	for _, want := range []string{
		`"assertions"`,
		`"exits-zero"`,
		`"observed": 3`,
		`"exit code 3 != 0"`,
	} {
		if !strings.Contains(jsonOut, want) {
			t.Errorf("json output missing %q:\n%s", want, jsonOut)
		}
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
