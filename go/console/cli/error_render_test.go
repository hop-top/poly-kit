package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// typedNotFound is an adopter-side typed error implementing
// AsCLIError(). Used to verify the middleware honors typed errors.
type typedNotFound struct{ msg string }

func (e *typedNotFound) Error() string { return e.msg }
func (e *typedNotFound) AsCLIError() *output.Error {
	return output.NotFoundError(e.msg)
}

// runWithErr builds a kit Root with one leaf subcommand whose RunE
// returns runErr, runs the root with --format=fmt, and returns the
// stderr output and the cobra error.
func runWithErr(t *testing.T, format string, runErr error) (string, error) {
	t.Helper()
	r := cli.New(cli.Config{
		Name:            "errtool",
		Version:         "0.0.0",
		Short:           "Error rendering test tool",
		DisableValidate: true,
	})

	leaf := &cobra.Command{
		Use:   "do",
		Short: "do thing",
		Annotations: map[string]string{
			"kit/side-effect": "false",
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return runErr
		},
	}
	r.Cmd.AddCommand(leaf)

	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)

	args := []string{"do"}
	if format != "" {
		args = append(args, "--format", format)
	}
	r.Cmd.SetArgs(args)

	// Bypass fang so we can observe the wrapped RunE behavior with a
	// stable stderr writer. Manually trigger the WrapRunE step that
	// Execute would run.
	r.WrapRunE()

	err := r.Cmd.Execute()
	return stderr.String(), err
}

func TestRunE_Middleware_Wraps_GenericError(t *testing.T) {
	bareErr := errors.New("kaboom")
	stderr, err := runWithErr(t, "json", bareErr)
	require.Error(t, err)

	// Verify the rendered envelope is structured JSON with CodeGeneric.
	var got output.Error
	require.NoError(t, json.Unmarshal([]byte(stderr), &got),
		"expected JSON envelope on stderr, got %q", stderr)
	assert.Equal(t, output.CodeGeneric, got.Code)
	assert.Equal(t, "kaboom", got.Message)
	assert.Equal(t, 1, got.ExitCode)
}

func TestRunE_Middleware_PreservesExitCode(t *testing.T) {
	stderr, err := runWithErr(t, "json", &typedNotFound{msg: "no such thing"})
	require.Error(t, err)

	var got output.Error
	require.NoError(t, json.Unmarshal([]byte(stderr), &got),
		"expected JSON envelope on stderr, got %q", stderr)
	assert.Equal(t, output.CodeNotFound, got.Code)
	assert.Equal(t, 3, got.ExitCode)
	assert.Equal(t, "no such thing", got.Message)
}

func TestRunE_Middleware_AsCLIError_Interface(t *testing.T) {
	// Typed error returning a custom envelope must control Code +
	// ExitCode end-to-end.
	custom := &output.Error{
		Code:     "CUSTOM_CODE",
		Message:  "custom message",
		ExitCode: 42,
	}
	stderr, err := runWithErr(t, "json", custom)
	require.Error(t, err)

	var got output.Error
	require.NoError(t, json.Unmarshal([]byte(stderr), &got),
		"expected JSON envelope on stderr, got %q", stderr)
	assert.Equal(t, "CUSTOM_CODE", got.Code)
	assert.Equal(t, 42, got.ExitCode)
}

func TestRunE_Middleware_DoesNotPrintOnSuccess(t *testing.T) {
	stderr, err := runWithErr(t, "json", nil)
	require.NoError(t, err)
	assert.Empty(t, stderr,
		"successful RunE must not write anything to stderr")
}

func TestRunE_Middleware_TableModeUsesPlaintext(t *testing.T) {
	stderr, err := runWithErr(t, "table", &typedNotFound{msg: "missing"})
	require.Error(t, err)

	// Plaintext mode: "NOT_FOUND: missing\n" — no JSON braces.
	assert.Contains(t, stderr, "NOT_FOUND: missing")
	assert.False(t, strings.Contains(stderr, "{"),
		"table-mode error must not be JSON, got %q", stderr)
}

func TestRunE_Middleware_DefaultFormatUsesPlaintext(t *testing.T) {
	// No --format flag: plaintext fallback.
	stderr, err := runWithErr(t, "", errors.New("plain failure"))
	require.Error(t, err)
	assert.Contains(t, stderr, "GENERIC: plain failure")
}
