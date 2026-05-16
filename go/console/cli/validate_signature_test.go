package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// sigFixtureRoot returns a Root with EnforceValidate=false (so the
// signature checks fire in isolation, without Layer-A noise), no
// status subcommand requirement, and a single well-formed top-level
// noun-group. Tests mutate the tree before calling
// r.ValidateSignature() directly.
func sigFixtureRoot(t *testing.T, modify ...func(*cli.Config)) *cli.Root {
	t.Helper()
	cfg := cli.Config{
		Name:            "vtool",
		Version:         "0.1.0",
		Short:           "signature validator fixture",
		DisableValidate: true,
	}
	for _, m := range modify {
		m(&cfg)
	}
	return cli.New(cfg)
}

// sigLeaf returns a runnable leaf with the minimum annotations so
// the signature validator can focus on shape signals. Side-effect
// + idempotency are set to keep the leaf parsable but the
// signature pass doesn't read them.
func sigLeaf(name string) *cobra.Command {
	c := &cobra.Command{
		Use:   name,
		Short: name + " summary",
		Long:  name + " long",
		Run:   func(*cobra.Command, []string) {},
	}
	cli.SetSideEffect(c, cli.SideEffectRead)
	cli.SetIdempotency(c, cli.IdempotencyYes)
	return c
}

// findViolation returns the first violation matching the given
// check, or nil. Used so tests can assert the right CHECK fired
// without depending on slice order.
func findViolation(report *cli.SignatureReport, check string) *cli.SignatureViolation {
	if report == nil {
		return nil
	}
	for i := range report.Violations {
		if report.Violations[i].Check == check {
			return &report.Violations[i]
		}
	}
	return nil
}

// ----------------------------------------------------------------------
// Check A: local-globals
// ----------------------------------------------------------------------

func TestValidateSignature_LocalGlobals_FiresOnFormatShadow(t *testing.T) {
	r := sigFixtureRoot(t)
	bad := sigLeaf("ping")
	bad.Flags().String("format", "json", "redefined global")
	r.Cmd.AddCommand(bad)

	report := r.ValidateSignature()
	require.True(t, report.HasViolations(),
		"redefining --format on a leaf should surface a violation")

	v := findViolation(report, cli.SignatureCheckLocalGlobals)
	require.NotNil(t, v)
	assert.Equal(t, "vtool ping", v.Path)
	assert.Contains(t, v.Detail, "format")
}

func TestValidateSignature_LocalGlobals_FiresOnDryRunShadow(t *testing.T) {
	r := sigFixtureRoot(t)
	bad := sigLeaf("ping")
	bad.Flags().Bool("dry-run", false, "redefined global")
	r.Cmd.AddCommand(bad)

	report := r.ValidateSignature()
	v := findViolation(report, cli.SignatureCheckLocalGlobals)
	require.NotNil(t, v)
	assert.Contains(t, v.Detail, "dry-run")
}

func TestValidateSignature_LocalGlobals_QuietOnNonGlobalFlag(t *testing.T) {
	r := sigFixtureRoot(t)
	clean := sigLeaf("ping")
	clean.Flags().String("custom", "", "leaf-local flag, not a global")
	r.Cmd.AddCommand(clean)

	report := r.ValidateSignature()
	assert.False(t, report.HasViolations(),
		"non-shadowing local flag must not trip the validator")
}

// ----------------------------------------------------------------------
// Check B: reserved-name
// ----------------------------------------------------------------------

func TestValidateSignature_ReservedName_FiresOnChildCollision(t *testing.T) {
	r := sigFixtureRoot(t)
	parent := &cobra.Command{Use: "foo", Short: "foo group"}
	cli.SetReservesChildren(parent, []string{"static", "harness"})
	parent.AddCommand(sigLeaf("static"))
	parent.AddCommand(sigLeaf("ok"))
	r.Cmd.AddCommand(parent)

	report := r.ValidateSignature()
	v := findViolation(report, cli.SignatureCheckReservedName)
	require.NotNil(t, v, "child named 'static' must trip the reservation")
	assert.Equal(t, "vtool foo static", v.Path)
}

func TestValidateSignature_ReservedName_QuietWithoutAnnotation(t *testing.T) {
	r := sigFixtureRoot(t)
	parent := &cobra.Command{Use: "foo", Short: "foo group"}
	parent.AddCommand(sigLeaf("static"))
	r.Cmd.AddCommand(parent)

	report := r.ValidateSignature()
	assert.Nil(t, findViolation(report, cli.SignatureCheckReservedName),
		"no kit/reserves-children annotation, no violation")
}

// ----------------------------------------------------------------------
// Check C: depth-hierarchical
// ----------------------------------------------------------------------

func TestValidateSignature_DepthHierarchical_FiresWithoutAnnotation(t *testing.T) {
	r := sigFixtureRoot(t)
	outer := &cobra.Command{Use: "svc", Short: "svc group"}
	inner := &cobra.Command{Use: "token", Short: "token group"}
	inner.AddCommand(sigLeaf("mint"))
	outer.AddCommand(inner)
	r.Cmd.AddCommand(outer)

	report := r.ValidateSignature()
	v := findViolation(report, cli.SignatureCheckDepthHierarchical)
	require.NotNil(t, v,
		"depth-3 leaf without kit/hierarchical on intermediates must fire")
	assert.Equal(t, "vtool svc token mint", v.Path)
	assert.Contains(t, v.Detail, "vtool svc")
	assert.Contains(t, v.Detail, "vtool svc token")
}

func TestValidateSignature_DepthHierarchical_QuietWhenAnnotated(t *testing.T) {
	r := sigFixtureRoot(t)
	outer := &cobra.Command{Use: "svc", Short: "svc group"}
	cli.SetHierarchical(outer)
	inner := &cobra.Command{Use: "token", Short: "token group"}
	cli.SetHierarchical(inner)
	inner.AddCommand(sigLeaf("mint"))
	outer.AddCommand(inner)
	r.Cmd.AddCommand(outer)

	report := r.ValidateSignature()
	assert.Nil(t, findViolation(report, cli.SignatureCheckDepthHierarchical),
		"fully-annotated chain must pass")
}

// ----------------------------------------------------------------------
// Check D: passthrough
// ----------------------------------------------------------------------

func TestValidateSignature_Passthrough_FiresOnArbitraryArgs(t *testing.T) {
	r := sigFixtureRoot(t)
	bad := sigLeaf("ping")
	bad.Args = cobra.ArbitraryArgs
	r.Cmd.AddCommand(bad)

	report := r.ValidateSignature()
	v := findViolation(report, cli.SignatureCheckPassthrough)
	require.NotNil(t, v,
		"cobra.ArbitraryArgs without kit/passthrough must warn")
	assert.Equal(t, "warning", v.Severity)
}

func TestValidateSignature_Passthrough_QuietWhenAnnotated(t *testing.T) {
	r := sigFixtureRoot(t)
	clean := sigLeaf("ping")
	clean.Args = cobra.ArbitraryArgs
	cli.SetPassthrough(clean)
	r.Cmd.AddCommand(clean)

	report := r.ValidateSignature()
	assert.Nil(t, findViolation(report, cli.SignatureCheckPassthrough),
		"kit/passthrough=true clears the warning")
}

func TestValidateSignature_Passthrough_QuietForNoArgs(t *testing.T) {
	r := sigFixtureRoot(t)
	clean := sigLeaf("ping")
	clean.Args = cobra.NoArgs
	r.Cmd.AddCommand(clean)

	report := r.ValidateSignature()
	assert.Nil(t, findViolation(report, cli.SignatureCheckPassthrough))
}

// ----------------------------------------------------------------------
// Strictness modes
// ----------------------------------------------------------------------

// TestSignatureStrictness_Silent_NoDiagnostics confirms that with
// strictness=silent, Execute proceeds even when violations exist —
// no log, no error, no exit.
func TestSignatureStrictness_Silent_NoDiagnostics(t *testing.T) {
	r := cli.New(cli.Config{
		Name:                  "vtool",
		Version:               "0.1.0",
		Short:                 "vt",
		DisableValidate:       true,
		SignatureStrictness:   cli.SignatureStrictnessSilent,
		ValidationFailureMode: cli.ValidationFailureError,
	})
	// Add a depth-3 leaf without kit/hierarchical to guarantee a
	// signature violation. The validator will see it, but silent
	// mode must drop the result.
	outer := &cobra.Command{Use: "svc", Short: "svc group"}
	inner := &cobra.Command{Use: "token", Short: "token group"}
	leaf := sigLeaf("mint")
	leaf.RunE = func(*cobra.Command, []string) error { return nil }
	inner.AddCommand(leaf)
	outer.AddCommand(inner)
	r.Cmd.AddCommand(outer)

	r.SetArgs([]string{"svc", "token", "mint"})
	err := r.Execute(context.Background())
	require.NoError(t, err,
		"silent mode must not surface signature violations through Execute")
}

func TestSignatureStrictness_Warn_LogsButProceeds(t *testing.T) {
	r := cli.New(cli.Config{
		Name:                  "vtool",
		Version:               "0.1.0",
		Short:                 "vt",
		DisableValidate:       true,
		SignatureStrictness:   cli.SignatureStrictnessWarn,
		ValidationFailureMode: cli.ValidationFailureError,
	})
	outer := &cobra.Command{Use: "svc", Short: "svc group"}
	inner := &cobra.Command{Use: "token", Short: "token group"}
	leaf := sigLeaf("mint")
	leaf.RunE = func(*cobra.Command, []string) error { return nil }
	inner.AddCommand(leaf)
	outer.AddCommand(inner)
	r.Cmd.AddCommand(outer)

	r.SetArgs([]string{"svc", "token", "mint"})
	// Warn mode logs via slog and continues; Execute must succeed.
	err := r.Execute(context.Background())
	require.NoError(t, err, "warn mode must not abort Execute")
}

func TestSignatureStrictness_Reject_AbortsViaFailureMode(t *testing.T) {
	r := cli.New(cli.Config{
		Name:                  "vtool",
		Version:               "0.1.0",
		Short:                 "vt",
		DisableValidate:       true,
		SignatureStrictness:   cli.SignatureStrictnessReject,
		ValidationFailureMode: cli.ValidationFailureError,
	})
	outer := &cobra.Command{Use: "svc", Short: "svc group"}
	inner := &cobra.Command{Use: "token", Short: "token group"}
	leaf := sigLeaf("mint")
	leaf.RunE = func(*cobra.Command, []string) error { return nil }
	inner.AddCommand(leaf)
	outer.AddCommand(inner)
	r.Cmd.AddCommand(outer)

	r.SetArgs([]string{"svc", "token", "mint"})
	err := r.Execute(context.Background())
	require.Error(t, err, "reject mode must surface the failure")

	var sigErr *cli.SignatureReportError
	require.True(t, errors.As(err, &sigErr),
		"reject failure must wrap a *SignatureReportError, got %T", err)
	require.NotNil(t, sigErr.Report)
	assert.True(t, sigErr.Report.HasViolations())
}

func TestSignatureStrictness_Reject_QuietWhenTreeIsClean(t *testing.T) {
	r := cli.New(cli.Config{
		Name:                  "vtool",
		Version:               "0.1.0",
		Short:                 "vt",
		DisableValidate:       true,
		SignatureStrictness:   cli.SignatureStrictnessReject,
		ValidationFailureMode: cli.ValidationFailureError,
	})
	leaf := sigLeaf("ping")
	leaf.RunE = func(*cobra.Command, []string) error { return nil }
	r.Cmd.AddCommand(leaf)

	r.SetArgs([]string{"ping"})
	require.NoError(t, r.Execute(context.Background()))
}

// ----------------------------------------------------------------------
// SignatureReport rendering
// ----------------------------------------------------------------------

func TestSignatureReport_RenderJSON_RoundTrips(t *testing.T) {
	r := sigFixtureRoot(t)
	// Inject one of each check so the report has variety.
	parent := &cobra.Command{Use: "foo", Short: "foo group"}
	cli.SetReservesChildren(parent, []string{"static"})
	parent.AddCommand(sigLeaf("static"))
	r.Cmd.AddCommand(parent)

	other := sigLeaf("ping")
	other.Flags().String("format", "json", "shadow")
	r.Cmd.AddCommand(other)

	report := r.ValidateSignature()
	require.True(t, report.HasViolations())

	var buf bytes.Buffer
	require.NoError(t, report.RenderJSON(&buf))

	var round cli.SignatureReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &round))
	assert.Equal(t, len(report.Violations), len(round.Violations))
	for i, v := range report.Violations {
		assert.Equal(t, v.Path, round.Violations[i].Path)
		assert.Equal(t, v.Check, round.Violations[i].Check)
		assert.Equal(t, v.Detail, round.Violations[i].Detail)
		assert.Equal(t, v.Severity, round.Violations[i].Severity)
	}
}

func TestSignatureReport_RenderJSON_EmptyReport(t *testing.T) {
	report := &cli.SignatureReport{}
	var buf bytes.Buffer
	require.NoError(t, report.RenderJSON(&buf))

	var round cli.SignatureReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &round))
	assert.Empty(t, round.Violations)
}

func TestSignatureReport_RenderText_EmptyVsPopulated(t *testing.T) {
	empty := &cli.SignatureReport{}
	var buf bytes.Buffer
	require.NoError(t, empty.RenderText(&buf))
	assert.Contains(t, buf.String(), "no signature violations")

	buf.Reset()
	pop := &cli.SignatureReport{
		Violations: []cli.SignatureViolation{
			{Path: "vtool x", Check: "passthrough", Detail: "d", Severity: "warning"},
		},
	}
	require.NoError(t, pop.RenderText(&buf))
	assert.Contains(t, buf.String(), "vtool x")
	assert.Contains(t, buf.String(), "passthrough")
}

// ----------------------------------------------------------------------
// kit/reserves-children round-trip
// ----------------------------------------------------------------------

func TestReservesChildren_RoundTrip(t *testing.T) {
	cmd := &cobra.Command{Use: "foo"}
	cli.SetReservesChildren(cmd, []string{"static", "harness", "static"})
	got := cli.GetReservesChildren(cmd)
	assert.Equal(t, []string{"static", "harness"}, got,
		"duplicates collapse, order preserved")

	cli.SetReservesChildren(cmd, nil)
	assert.Nil(t, cli.GetReservesChildren(cmd),
		"nil/empty input clears the annotation")
}
