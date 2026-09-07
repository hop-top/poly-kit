package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// surfaceRoot is the fixture for tests that must go through Root.Execute,
// which is where the fang error handler and the flag-error func are
// installed. cli.New alone installs neither.
func surfaceRoot(t *testing.T, runErr error) (*cli.Root, *bytes.Buffer) {
	t.Helper()
	r := cli.New(cli.Config{
		Name:            "surftool",
		Version:         "0.0.0",
		Short:           "Flag error surface tool",
		DisableValidate: true,
	})
	leaf := &cobra.Command{
		Use:         "list",
		Short:       "list things",
		Long:        "list things",
		Annotations: map[string]string{"kit/side-effect": "read", "kit/idempotent": "true"},
		RunE:        func(*cobra.Command, []string) error { return runErr },
	}
	leaf.Flags().String("status", "", "Status filter")
	leaf.Flags().Int("counters", 0, "Counter mode")
	leaf.Flags().Int("limit", 0, "Max rows")
	r.Cmd.AddCommand(leaf)
	r.WithFlagEnum("status", "TODO", "IN_PROGRESS", "DONE", "SKIPPED")

	var stderr bytes.Buffer
	r.Cmd.SetOut(&bytes.Buffer{})
	r.Cmd.SetErr(&stderr)
	return r, &stderr
}

func execSurface(t *testing.T, runErr error, args ...string) (string, error) {
	t.Helper()
	r, stderr := surfaceRoot(t, runErr)
	r.Cmd.SetArgs(args)
	err := r.Execute(context.Background())
	return stderr.String(), err
}

// --- double render -------------------------------------------------------

// ansi strips styling so the assertion counts renderings, not escapes.
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestExecute_StructuredErrorRendersOnce(t *testing.T) {
	runErr := &output.Error{
		Code:     output.CodeGeneric,
		Message:  `unknown status "BOGUS"; valid values: TODO, DONE`,
		ExitCode: 1,
	}
	stderr, err := execSurface(t, runErr, "list")
	require.Error(t, err)

	clean := ansi.ReplaceAllString(stderr, "")
	// The bug: the RunE middleware wrote the envelope, then fang's
	// default handler printed the same failure again in its own styling.
	// One failure must produce one rendering.
	assert.Equal(t, 1, strings.Count(clean, `unknown status "BOGUS"`),
		"structured error rendered more than once:\n%s", clean)
	assert.Equal(t, 1, strings.Count(clean, "GENERIC:"),
		"envelope prefix rendered more than once:\n%s", clean)
	// fang's reformatted spelling must not appear alongside the envelope.
	assert.NotContains(t, clean, "Generic:",
		"fang's restyled duplicate is still present:\n%s", clean)
}

func TestExecute_StructuredErrorRendersOnceInJSON(t *testing.T) {
	runErr := &output.Error{Code: output.CodeNotFound, Message: "no such thing", ExitCode: 3}
	stderr, err := execSurface(t, runErr, "list", "--format", "json")
	require.Error(t, err)

	clean := strings.TrimSpace(ansi.ReplaceAllString(stderr, ""))
	// A second, prose rendering appended to the JSON would make stderr
	// unparseable for the --format json caller who asked for structure.
	var env map[string]any
	require.NoError(t, json.Unmarshal([]byte(clean), &env),
		"stderr is not a single JSON document:\n%s", clean)
	assert.Equal(t, "NOT_FOUND", env["code"])
}

func TestExecute_UnstructuredErrorStillRenders(t *testing.T) {
	// A failure that never reached kit's middleware must still be
	// reported — suppressing the duplicate must not suppress the only
	// rendering of an error kit does not own. A Run (not RunE) leaf whose
	// Args validator rejects fails outside the wrapped chain entirely.
	r := cli.New(cli.Config{
		Name: "surftool", Version: "0.0.0", Short: "s", DisableValidate: true,
	})
	leaf := &cobra.Command{
		Use:   "list",
		Short: "list things",
		Long:  "list things",
		Args:  cobra.NoArgs,
		Run:   func(*cobra.Command, []string) {},
	}
	r.Cmd.AddCommand(leaf)
	var stderr bytes.Buffer
	r.Cmd.SetErr(&stderr)
	r.Cmd.SetOut(&bytes.Buffer{})
	r.Cmd.SetArgs([]string{"list", "unexpected-positional"})

	err := r.Execute(context.Background())
	require.Error(t, err)
	assert.Contains(t, ansi.ReplaceAllString(stderr.String(), ""), "unexpected-positional",
		"cobra's own error was swallowed by the already-rendered guard")
}

// --- parse-time errors through the full surface --------------------------

func TestExecute_UnknownFlagRendersCorrection(t *testing.T) {
	stderr, err := execSurface(t, nil, "list", "--count")
	require.Error(t, err)
	clean := ansi.ReplaceAllString(stderr, "")
	assert.Contains(t, clean, "USAGE:")
	assert.Contains(t, clean, "Fix: --counters",
		"the only prefix match should arrive as the fix:\n%s", clean)
	// fang's untreated wording must not survive.
	assert.NotContains(t, clean, "Try --help for usage")
}

func TestExecute_MissingValueRendersEnum(t *testing.T) {
	stderr, err := execSurface(t, nil, "list", "--status")
	require.Error(t, err)
	clean := ansi.ReplaceAllString(stderr, "")
	assert.Contains(t, clean, "Cause: --status requires a value")
	for _, v := range []string{"TODO", "IN_PROGRESS", "DONE", "SKIPPED"} {
		assert.Contains(t, clean, "--status "+v,
			"missing-value error omitted %q:\n%s", v, clean)
	}
}

func TestExecute_ParseErrorHonorsFormatJSON(t *testing.T) {
	stderr, err := execSurface(t, nil, "--format", "json", "list", "--status")
	require.Error(t, err)
	clean := strings.TrimSpace(ansi.ReplaceAllString(stderr, ""))

	var env struct {
		Code         string   `json:"code"`
		Cause        string   `json:"cause"`
		SuggestedFix string   `json:"suggested_fix"`
		Alternatives []string `json:"alternatives"`
		ExitCode     int      `json:"exit_code"`
		Transience   string   `json:"transience"`
	}
	require.NoError(t, json.Unmarshal([]byte(clean), &env),
		"parse error did not render as the structured envelope:\n%s", clean)
	assert.Equal(t, "USAGE", env.Code)
	assert.Equal(t, 2, env.ExitCode)
	assert.Equal(t, "permanent", env.Transience)
	assert.Equal(t, "--status TODO", env.SuggestedFix)
	assert.Len(t, env.Alternatives, 4)
}

func TestExecute_EnumReachesHelpText(t *testing.T) {
	r, _ := surfaceRoot(t, nil)
	var stdout bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetArgs([]string{"list", "--help"})
	require.NoError(t, r.Execute(context.Background()))
	clean := ansi.ReplaceAllString(stdout.String(), "")
	assert.Contains(t, clean, "one of: TODO, IN_PROGRESS, DONE, SKIPPED",
		"help text lost the enum:\n%s", clean)
}

func TestExecute_EnumReachesCompletion(t *testing.T) {
	r, _ := surfaceRoot(t, nil)
	var stdout bytes.Buffer
	r.Cmd.SetOut(&stdout)
	// cobra's hidden __complete command is what a shell actually calls.
	r.Cmd.SetArgs([]string{"__complete", "list", "--status", ""})
	require.NoError(t, r.Execute(context.Background()))
	out := stdout.String()
	for _, v := range []string{"TODO", "IN_PROGRESS", "DONE", "SKIPPED"} {
		assert.Contains(t, out, v, "completion omitted %q:\n%s", v, out)
	}
}

// --- signature validation ----------------------------------------------

func TestFlagEnum_KeepsSignatureValid(t *testing.T) {
	// The help addendum rewrites flag Usage strings; the signature
	// validator has opinions about flag shape, so assert it directly
	// rather than trusting the surface harness (which does not run it).
	r, _ := surfaceRoot(t, nil)
	r.Cmd.SetArgs([]string{"list", "--help"})
	require.NoError(t, r.Execute(context.Background()))
	report := r.ValidateSignature()
	assert.False(t, report.HasViolations(),
		"enum help addendum tripped the signature validator: %+v", report)
}
