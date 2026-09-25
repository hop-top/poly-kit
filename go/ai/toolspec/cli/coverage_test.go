package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	speccli "hop.top/kit/go/ai/toolspec/cli"
	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// coverageRoot builds a kit Root with the named leaves. Each entry
// maps a command name to the kit/side-effect value it declares; the
// empty string means the leaf declares nothing.
func coverageRoot(t *testing.T, name string, leaves map[string]string) *kitcli.Root {
	t.Helper()
	r := kitcli.New(kitcli.Config{Name: name, Version: "0.0.1", Short: name})
	for leaf, se := range leaves {
		c := &cobra.Command{
			Use:   leaf,
			Short: leaf,
			RunE:  func(*cobra.Command, []string) error { return nil },
		}
		if se != "" {
			c.Annotations = map[string]string{"kit/side-effect": se}
		}
		r.Cmd.AddCommand(c)
	}
	return r
}

// --- Counting -------------------------------------------------------

func TestCoverageOf_CountsAnnotatedAndUnannotated(t *testing.T) {
	r := coverageRoot(t, "demo", map[string]string{
		"show":    "read",
		"push":    "write-shared",
		"silent":  "",
		"quieter": "",
	})

	rep := speccli.CoverageOf(r)

	assert.True(t, rep.Measurable)
	assert.Equal(t, "demo", rep.Tool)
	assert.Equal(t, 4, rep.Total)
	assert.Equal(t, 2, rep.Annotated)
	assert.Equal(t, 2, rep.Unannotated)
	assert.InDelta(t, 50.0, rep.Percent(), 0.001)
	assert.Equal(t,
		[]string{"demo quieter", "demo silent"},
		rep.UnannotatedPaths,
		"the report names which commands to fix, sorted")
}

// TestCoverageOf_TotalsAlwaysReconcile is the invariant that makes
// the percentage trustworthy. Annotated + Unannotated must equal
// Total for every mix, including the two categories that are easy
// to double-count: heuristic-inferred leaves and malformed values.
func TestCoverageOf_TotalsAlwaysReconcile(t *testing.T) {
	r := coverageRoot(t, "demo", map[string]string{
		"show":   "read",
		"push":   "write",
		"silent": "",
		"delete": "",           // destructive-name heuristic fires
		"typo":   "destrutive", // declared, unresolvable
	})

	rep := speccli.CoverageOf(r)

	assert.Equal(t, 5, rep.Total)
	assert.Equal(t, rep.Total, rep.Annotated+rep.Unannotated,
		"annotated + unannotated must equal total")
	assert.Equal(t, 2, rep.Annotated)
	assert.Equal(t, 1, rep.Inferred, "delete trips the heuristic")
	assert.Equal(t, 1, rep.Malformed, "a typo is not a declaration")
}

// TestCoverageOf_InferredCountsAgainstCoverage pins the choice that
// kit's destructive-name guess does NOT count as an annotation. The
// heuristic is the inference the report exists to replace; counting
// it as coverage would let a tool reach 100% having declared nothing.
func TestCoverageOf_InferredCountsAgainstCoverage(t *testing.T) {
	r := coverageRoot(t, "demo", map[string]string{"delete": ""})

	rep := speccli.CoverageOf(r)

	assert.Equal(t, 1, rep.Total)
	assert.Equal(t, 0, rep.Annotated)
	assert.Equal(t, 1, rep.Inferred)
	assert.InDelta(t, 0.0, rep.Percent(), 0.001)
	assert.Contains(t, rep.UnannotatedPaths, "demo delete")
}

// TestCoverageOf_MalformedIsNotAnnotated pins that an unresolvable
// value is reported as a typo AND counted against coverage, with the
// offending text echoed so the adopter can see what they wrote.
func TestCoverageOf_MalformedIsNotAnnotated(t *testing.T) {
	r := coverageRoot(t, "demo", map[string]string{"typo": "destrutive"})

	rep := speccli.CoverageOf(r)

	assert.Equal(t, 0, rep.Annotated)
	assert.Equal(t, 1, rep.Malformed)
	require.Len(t, rep.MalformedPaths, 1)
	assert.Contains(t, rep.MalformedPaths[0], "destrutive",
		"the report echoes the typo so it can be found")
}

// TestCoverageOf_SkipsReservedAndGroups keeps the denominator at
// commands an adopter can actually annotate. Kit's own reserved
// verbs and pure command groups are excluded; counting them would
// put a ceiling on coverage no adopter work could lift.
func TestCoverageOf_SkipsReservedAndGroups(t *testing.T) {
	r := coverageRoot(t, "demo", map[string]string{"show": "read"})
	require.NoError(t, speccli.RegisterSpecCommand(r, "1.1"))

	group := &cobra.Command{Use: "task", Short: "Task commands"}
	group.AddCommand(&cobra.Command{
		Use:         "add",
		Short:       "Add",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "write"},
	})
	r.Cmd.AddCommand(group)

	rep := speccli.CoverageOf(r)

	assert.Equal(t, 2, rep.Total, "show + task add; not the group, not spec")
	assert.Equal(t, 2, rep.Annotated)
	for _, p := range rep.UnannotatedPaths {
		assert.NotContains(t, p, "spec",
			"kit's own reserved verbs are not the adopter's to annotate")
	}
}

// --- Measurability --------------------------------------------------

// TestCoverageOf_EmptyTreeIsUnmeasurableNotZero is the trap the
// brief calls out: a tree a reporter could not read must report
// UNMEASURABLE, never 0%. Silently reporting 0% for a CLI you failed
// to parse is the same class of bug as defaulting unknown to read.
func TestCoverageOf_EmptyTreeIsUnmeasurableNotZero(t *testing.T) {
	r := kitcli.New(kitcli.Config{Name: "bare", Version: "0.0.1", Short: "bare"})

	rep := speccli.CoverageOf(r)

	assert.False(t, rep.Measurable, "no reflectable commands = unmeasurable")
	assert.Equal(t, 0, rep.Total)
}

func TestCoverageOf_NilRootIsUnmeasurable(t *testing.T) {
	rep := speccli.CoverageOf(nil)
	assert.False(t, rep.Measurable)
	assert.Equal(t, 0, rep.Total)
}

// --- Threshold ------------------------------------------------------

func TestCoverageReport_Meets(t *testing.T) {
	r := coverageRoot(t, "demo", map[string]string{
		"a": "read", "b": "read", "c": "read", "d": "",
	})
	rep := speccli.CoverageOf(r) // 3/4 = 75%

	assert.True(t, rep.Meets(0), "a zero threshold disables the gate")
	assert.True(t, rep.Meets(75), "the threshold is inclusive")
	assert.True(t, rep.Meets(50))
	assert.False(t, rep.Meets(80))
}

// TestCoverageReport_UnmeasurableFailsAnyRealThreshold pins the
// fail-closed posture: a gate must not go green because there was
// nothing to measure.
func TestCoverageReport_UnmeasurableFailsAnyRealThreshold(t *testing.T) {
	rep := speccli.CoverageOf(nil)

	assert.True(t, rep.Meets(0), "an explicitly disabled gate still passes")
	assert.False(t, rep.Meets(1), "unmeasurable never satisfies a real threshold")
	assert.False(t, rep.Meets(100))
}

// --- The subcommand -------------------------------------------------

func TestSpecCoverage_RendersJSONAndPassesUnderThreshold(t *testing.T) {
	r := coverageRoot(t, "demo", map[string]string{"show": "read", "silent": ""})
	require.NoError(t, speccli.RegisterSpecCommand(r, "1.1"))

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	r.Cmd.SetArgs([]string{"spec", "coverage", "--min", "50"})
	require.NoError(t, r.Cmd.Execute())

	var rep speccli.CoverageReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rep))
	assert.Equal(t, 2, rep.Total)
	assert.Equal(t, 1, rep.Annotated)
	assert.True(t, rep.Measurable)
}

// TestSpecCoverage_BelowThresholdExitsConflict pins the CI contract:
// a failing gate exits CONFLICT (4), and the error names the commands
// to annotate so the build log is actionable.
func TestSpecCoverage_BelowThresholdExitsConflict(t *testing.T) {
	r := coverageRoot(t, "demo", map[string]string{"show": "read", "silent": ""})
	require.NoError(t, speccli.RegisterSpecCommand(r, "1.1"))

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	r.Cmd.SetArgs([]string{"spec", "coverage", "--min", "90"})

	err := r.Cmd.Execute()
	require.Error(t, err)

	var oerr *output.Error
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, output.ExitConflict, oerr.ExitCode)
	assert.Contains(t, oerr.Message, "demo silent",
		"a CI failure must say which command to annotate")

	// The report is still printed: an adopter needs the detail even
	// on the failing run.
	assert.Contains(t, buf.String(), "unannotated")
}

// TestSpecCoverage_ReportPrintedEvenWhenGatePasses guards against a
// future refactor that only renders on failure.
func TestSpecCoverage_ReportPrintedEvenWhenGatePasses(t *testing.T) {
	r := coverageRoot(t, "demo", map[string]string{"show": "read"})
	require.NoError(t, speccli.RegisterSpecCommand(r, "1.1"))

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	r.Cmd.SetArgs([]string{"spec", "coverage"})
	require.NoError(t, r.Cmd.Execute())

	assert.True(t, strings.Contains(buf.String(), "\"annotated\""),
		"the report renders on the passing path too")
}
