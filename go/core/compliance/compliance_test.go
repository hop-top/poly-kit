package compliance_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/compliance"
)

func testdataPath() string {
	// spaced.toolspec.yaml lives two levels up in examples/spaced/
	return filepath.Join("..", "examples", "spaced", "spaced.toolspec.yaml")
}

func TestRunStatic_SpacedToolspec(t *testing.T) {
	path := testdataPath()
	if _, err := os.Stat(path); err != nil {
		t.Skipf("toolspec not found: %s", path)
	}

	results, err := compliance.RunStatic(path)
	require.NoError(t, err)
	require.NotEmpty(t, results)

	byFactor := make(map[compliance.Factor]compliance.CheckResult)
	for _, r := range results {
		byFactor[r.Factor] = r
	}

	// Factor 1: Self-describing — commands non-empty
	assert.Equal(t, "pass", byFactor[compliance.FactorSelfDescribing].Status)

	// Factor 2: Structured I/O — at least one output_schema
	assert.Equal(t, "pass", byFactor[compliance.FactorStructuredIO].Status)

	// Factor 4: Contracts — mutating commands have contract fields
	assert.Equal(t, "pass", byFactor[compliance.FactorContractsErrors].Status)

	// Factor 5: Preview — mutating commands have preview_modes
	assert.Equal(t, "pass", byFactor[compliance.FactorPreview].Status)

	// Factor 6: Idempotency — contract.idempotent declared
	assert.Equal(t, "pass", byFactor[compliance.FactorIdempotency].Status)

	// Factor 7: State transparency — config_commands present
	assert.Equal(t, "pass", byFactor[compliance.FactorStateTransparency].Status)

	// Factor 8: Safe delegation — dangerous cmds have safety
	assert.Equal(t, "pass", byFactor[compliance.FactorSafeDelegation].Status)

	// Factor 11: Evolution — schema_version set
	assert.Equal(t, "pass", byFactor[compliance.FactorEvolution].Status)

	// Factor 12: Auth lifecycle — auth_commands present
	assert.Contains(t, []string{"pass", "skip"},
		byFactor[compliance.FactorAuthLifecycle].Status)

	// Skipped factors (runtime only)
	assert.Equal(t, "skip", byFactor[compliance.FactorProvenance].Status)
	assert.Equal(t, "skip", byFactor[compliance.FactorStreamDiscipline].Status)
	assert.Equal(t, "skip", byFactor[compliance.FactorObservableOps].Status)
}

func TestRunStatic_EmptySpec(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "empty.yaml")
	require.NoError(t, os.WriteFile(p, []byte("name: empty\n"), 0644))

	results, err := compliance.RunStatic(p)
	require.NoError(t, err)

	failing := 0
	for _, r := range results {
		if r.Status == "fail" {
			failing++
		}
	}
	assert.Greater(t, failing, 0, "empty spec should have failures")
}

func TestRun_ReturnsReport(t *testing.T) {
	path := testdataPath()
	if _, err := os.Stat(path); err != nil {
		t.Skipf("toolspec not found: %s", path)
	}

	// Static-only (no binary)
	report, err := compliance.Run("", path)
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Equal(t, path, report.Toolspec)
	assert.Equal(t, 12, report.Total)
	assert.GreaterOrEqual(t, report.Score, 1)
}

func TestFormatReport_Text(t *testing.T) {
	report := &compliance.Report{
		Binary:   "test-bin",
		Toolspec: "test.yaml",
		Total:    12,
		Score:    8,
		Results: []compliance.CheckResult{
			{
				Factor: compliance.FactorSelfDescribing,
				Name:   "Self-Describing",
				Status: "pass",
			},
			{
				Factor:     compliance.FactorStructuredIO,
				Name:       "Structured I/O",
				Status:     "fail",
				Suggestion: "Add output_schema",
			},
		},
	}

	out := compliance.FormatReport(report, "text")
	assert.Contains(t, out, "Self-Describing")
	assert.Contains(t, out, "PASS")
	assert.Contains(t, out, "FAIL")
	assert.Contains(t, out, "8/12")
}

func TestFormatReport_JSON(t *testing.T) {
	report := &compliance.Report{
		Binary:   "test-bin",
		Toolspec: "test.yaml",
		Total:    12,
		Score:    8,
		Results: []compliance.CheckResult{
			{
				Factor: compliance.FactorSelfDescribing,
				Name:   "Self-Describing",
				Status: "pass",
			},
		},
	}

	out := compliance.FormatReport(report, "json")
	assert.Contains(t, out, `"score"`)
	assert.Contains(t, out, `"total"`)
	assert.Contains(t, out, `"results"`)
}

func TestFactorNames(t *testing.T) {
	// Every factor should have a name
	for f := compliance.FactorSelfDescribing; f <= compliance.FactorAuthLifecycle; f++ {
		assert.NotEmpty(t, f.String(), "factor %d should have name", f)
	}
}
