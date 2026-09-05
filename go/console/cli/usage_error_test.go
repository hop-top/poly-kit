package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/cmdsurface"
)

// usageRoot builds a root with one leaf, `do`, taking exactly one
// positional argument and one integer flag, so cobra's own argument
// and flag validation can be provoked without any adopter code
// running. The leaf's RunE records whether it was reached.
func usageRoot(t *testing.T, reached *bool) *cli.Root {
	t.Helper()
	r := cli.New(cli.Config{
		Name: "usagetool", Version: "0.0.0", Short: "usage test tool",
		DisableValidate: true,
	})
	do := &cobra.Command{
		Use:   "do <thing>",
		Short: "do a thing",
		Args:  cobra.ExactArgs(1),
		RunE: func(*cobra.Command, []string) error {
			if reached != nil {
				*reached = true
			}
			return nil
		},
	}
	cli.SetSideEffect(do, cli.SideEffectRead)
	do.Flags().Int("count", 0, "how many")
	do.Flags().String("name", "", "a name")
	do.Flags().String("tag", "", "a tag")
	do.MarkFlagsRequiredTogether("name", "tag")
	r.Cmd.AddCommand(do)
	return r
}

// executeUsage runs the root through Execute, the path a built binary
// takes, with captured streams. It returns the envelope the error
// unwraps to (nil when it does not), stderr, and the raw error.
func executeUsage(t *testing.T, r *cli.Root, args ...string) (*output.Error, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	r.SetArgs(args)
	err := r.Execute(context.Background())
	var ce *output.Error
	errors.As(err, &ce)
	return ce, stderr.String(), err
}

func TestUsage_CobraValidationErrorsExitTwo(t *testing.T) {
	var (
		notExist      *pflag.NotExistError
		valueRequired *pflag.ValueRequiredError
		invalidValue  *pflag.InvalidValueError
	)
	cases := []struct {
		name    string
		args    []string
		message string
		// retained is a pointer to a typed pflag error the envelope
		// must still unwrap to; nil when cobra raises a bare error.
		retained any
	}{
		{"missing positional", []string{"do"}, "accepts 1 arg(s), received 0", nil},
		{"extra positional", []string{"do", "a", "b"}, "accepts 1 arg(s), received 2", nil},
		{"unknown flag", []string{"do", "--nosuch", "a"}, "unknown flag: --nosuch", &notExist},
		{"unknown shorthand", []string{"do", "-Z", "a"}, "unknown shorthand flag: 'Z' in -Z", &notExist},
		{"flag needs an argument", []string{"do", "--count"}, "flag needs an argument: --count", &valueRequired},
		{"invalid flag value", []string{"do", "--count=x", "a"},
			`invalid argument "x" for "--count" flag: strconv.ParseInt: parsing "x": invalid syntax`, &invalidValue},
		{"flag group incomplete", []string{"do", "--name=n", "a"},
			"if any flags in the group [name tag] are set they must all be set; missing [tag]", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reached bool
			ce, stderr, err := executeUsage(t, usageRoot(t, &reached), tc.args...)
			require.Error(t, err)
			require.NotNil(t, ce, "cobra's validation error must leave Execute as a kit envelope, got %T: %v", err, err)
			assert.Equal(t, output.CodeUsage, ce.Code)
			assert.Equal(t, 2, ce.ExitCode)
			assert.Equal(t, tc.message, ce.Message)
			assert.Equal(t, output.TransiencePermanent, ce.Transience)
			assert.Contains(t, stderr, "USAGE: "+tc.message,
				"the envelope is rendered to stderr like any other kit error")
			assert.Contains(t, stderr, "--help", "the fix names the help flag")
			assert.False(t, reached, "the leaf must not run on a usage error")
			if tc.retained != nil {
				assert.True(t, errors.As(err, tc.retained),
					"the pflag error must survive the conversion so errors.As still reaches it")
			}
		})
	}
}

func TestUsage_RequiredFlagMissingExitsTwo(t *testing.T) {
	r := usageRoot(t, nil)
	leaf, _, err := r.Cmd.Find([]string{"do"})
	require.NoError(t, err)
	require.NoError(t, leaf.MarkFlagRequired("count"))

	ce, stderr, _ := executeUsage(t, r, "do", "a")
	require.NotNil(t, ce)
	assert.Equal(t, output.CodeUsage, ce.Code)
	assert.Equal(t, 2, ce.ExitCode)
	assert.Equal(t, `required flag(s) "count" not set`, ce.Message)
	assert.Contains(t, stderr, "USAGE: required flag(s)")
}

func TestUsage_ValidInvocationStillRuns(t *testing.T) {
	var reached bool
	ce, stderr, err := executeUsage(t, usageRoot(t, &reached), "do", "--count=2", "--name=n", "--tag=t", "a")
	require.NoError(t, err)
	assert.Nil(t, ce)
	assert.True(t, reached)
	assert.Empty(t, stderr)
}

func TestUsage_HelpStillHelps(t *testing.T) {
	r := usageRoot(t, nil)
	var stdout bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stdout)
	r.SetArgs([]string{"do", "--help"})
	require.NoError(t, r.Execute(context.Background()),
		"--help is a help request, not a flag error")
	assert.Contains(t, stdout.String(), "do a thing")
}

func TestUsage_EnvelopeHonorsFormat(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"positional", []string{"do", "--format=json"}},
		{"flag", []string{"do", "--format=json", "--nosuch", "a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, _ := executeUsage(t, usageRoot(t, nil), tc.args...)
			var got output.Error
			// fang appends its own plain line after the envelope, so
			// decode the first JSON value rather than the whole stream.
			require.NoError(t, json.NewDecoder(strings.NewReader(stderr)).Decode(&got),
				"expected a JSON envelope on stderr, got %q", stderr)
			assert.Equal(t, output.CodeUsage, got.Code)
			assert.Equal(t, 2, got.ExitCode)
		})
	}
}

func TestUsage_UnknownSubcommandOnRunnableRootExitsTwo(t *testing.T) {
	// cli.New gives the root cobra.NoArgs; once the root is runnable
	// cobra validates it, and an unrecognized word is a usage error,
	// not a resource lookup that failed.
	r := usageRoot(t, nil)
	r.Cmd.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }

	ce, _, _ := executeUsage(t, r, "nosuch")
	require.NotNil(t, ce)
	assert.Equal(t, output.CodeUsage, ce.Code)
	assert.Equal(t, 2, ce.ExitCode)
	assert.Equal(t, `unknown command "nosuch" for "usagetool"`, ce.Message)
}

func TestUsage_AdopterClassificationWins(t *testing.T) {
	// An adopter whose own validator already chose a code keeps it:
	// kit classifies what cobra left bare, it does not reclassify.
	r := usageRoot(t, nil)
	leaf, _, err := r.Cmd.Find([]string{"do"})
	require.NoError(t, err)
	leaf.Args = func(*cobra.Command, []string) error {
		return output.NotFoundError("no such thing")
	}

	ce, _, _ := executeUsage(t, r, "do", "a")
	require.NotNil(t, ce)
	assert.Equal(t, output.CodeNotFound, ce.Code)
	assert.Equal(t, 3, ce.ExitCode)
}

// TestUsage_BridgeInvocationReportsExitTwo drives the same root
// through the bridge and the in-process runner, the way the api and
// socket services do, and checks the Result carries the taxonomy code
// with the usage text in stderr.
func TestUsage_BridgeInvocationReportsExitTwo(t *testing.T) {
	r := usageRoot(t, nil)
	// Execute prepares the tree before serving it; the bridge test
	// takes the same preparation step without running a command.
	r.WrapRunE()

	b := cmdsurface.New(r.Cmd, cmdsurface.WithRunner(cmdsurface.InProcessRunner(r.Cmd)))
	b.Expose("*", cmdsurface.SurfaceREST)
	meta := cmdsurface.Meta{Surface: cmdsurface.SurfaceREST}

	for _, tc := range []struct {
		name    string
		inv     cmdsurface.Invocation
		message string
	}{
		{"missing positional", cmdsurface.Invocation{Path: []string{"do"}, Meta: meta},
			"accepts 1 arg(s), received 0"},
		{"unknown flag", cmdsurface.Invocation{
			Path: []string{"do"}, Args: []string{"a"}, Flags: map[string]any{"nosuch": "x"}, Meta: meta,
		}, "unknown flag: --nosuch"},
		{"invalid flag value", cmdsurface.Invocation{
			Path: []string{"do"}, Args: []string{"a"}, Flags: map[string]any{"count": "x"}, Meta: meta,
		}, `invalid argument "x" for "--count" flag`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := b.Invoke(context.Background(), tc.inv)
			require.NoError(t, err, "a usage error is a Result, not a transport failure")
			assert.Equal(t, 2, res.ExitCode)
			assert.Contains(t, res.Stderr, "USAGE: "+tc.message)
			assert.Empty(t, res.Stdout)
		})
	}

	res, err := b.Invoke(context.Background(), cmdsurface.Invocation{
		Path: []string{"do"}, Args: []string{"a"}, Meta: meta,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode, "the tree is clean between invocations")
}
