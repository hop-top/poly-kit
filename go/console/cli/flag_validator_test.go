package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/console/output"
)

// buildFlagValidatorRoot returns a Root with one leaf subcommand
// (`do`) that records whether its RunE was invoked. The persistent
// flag `--my-flag` lives on the root so children inherit it (mirrors
// real adopter usage of cross-cutting flags like --api-version).
func buildFlagValidatorRoot(t *testing.T) (*cli.Root, *atomic.Int32, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	r := cli.New(cli.Config{
		Name:            "validatortool",
		Version:         "0.0.0",
		Short:           "Flag validator test tool",
		DisableValidate: true,
	})
	r.Cmd.PersistentFlags().String("my-flag", "default", "test flag")

	var called atomic.Int32
	leaf := &cobra.Command{
		Use:   "do",
		Short: "do thing",
		Annotations: map[string]string{
			"kit/side-effect": "false",
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			called.Add(1)
			return nil
		},
	}
	r.Cmd.AddCommand(leaf)

	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	return r, &called, &stdout, &stderr
}

func TestWithFlagValidator_RejectRendersJSONEnvelope(t *testing.T) {
	r, called, _, stderr := buildFlagValidatorRoot(t)

	r.WithFlagValidator("my-flag", func(v string) *output.Error {
		if v == "bad" {
			return &output.Error{
				Code:     "INVALID_FLAG",
				Message:  "my-flag rejected",
				ExitCode: 2,
			}
		}
		return nil
	})
	r.WrapRunE()

	r.Cmd.SetArgs([]string{"do", "--format", "json", "--my-flag", "bad"})
	err := r.Cmd.Execute()
	require.Error(t, err)

	var got output.Error
	require.NoError(t, json.Unmarshal(stderr.Bytes(), &got),
		"expected JSON envelope on stderr, got %q", stderr.String())
	assert.Equal(t, "INVALID_FLAG", got.Code)
	assert.Equal(t, "my-flag rejected", got.Message)
	assert.Equal(t, 2, got.ExitCode)
	assert.Equal(t, int32(0), called.Load(),
		"leaf RunE must NOT be called when validator rejects")
}

func TestWithFlagValidator_AcceptDispatchesNormally(t *testing.T) {
	r, called, _, stderr := buildFlagValidatorRoot(t)

	r.WithFlagValidator("my-flag", func(_ string) *output.Error {
		return nil // accept everything
	})
	r.WrapRunE()

	r.Cmd.SetArgs([]string{"do", "--my-flag", "good"})
	err := r.Cmd.Execute()
	require.NoError(t, err)
	assert.Equal(t, int32(1), called.Load(),
		"leaf RunE must be called when validator accepts")
	assert.Empty(t, stderr.String(),
		"accepting validator must not write to stderr")
}

func TestWithFlagValidator_NoValidatorPassthrough(t *testing.T) {
	r, called, _, stderr := buildFlagValidatorRoot(t)

	// No WithFlagValidator call.
	r.WrapRunE()

	r.Cmd.SetArgs([]string{"do", "--my-flag", "whatever"})
	err := r.Cmd.Execute()
	require.NoError(t, err)
	assert.Equal(t, int32(1), called.Load())
	assert.Empty(t, stderr.String())
}

func TestWithFlagValidator_OnlyValidatesWhenChanged(t *testing.T) {
	r, called, _, stderr := buildFlagValidatorRoot(t)

	r.WithFlagValidator("my-flag", func(_ string) *output.Error {
		return &output.Error{
			Code:     "ALWAYS_REJECT",
			Message:  "should not fire",
			ExitCode: 2,
		}
	})
	r.WrapRunE()

	// Do NOT pass --my-flag: cobra leaves flag.Changed=false and
	// uses the default. Validator must skip per the documented
	// "only validate when the user set it" semantics.
	r.Cmd.SetArgs([]string{"do"})
	err := r.Cmd.Execute()
	require.NoError(t, err,
		"validator must not fire when user didn't set the flag")
	assert.Equal(t, int32(1), called.Load())
	assert.Empty(t, stderr.String())
}

func TestWithFlagValidator_LastWins(t *testing.T) {
	r, called, _, _ := buildFlagValidatorRoot(t)

	// First registration rejects everything.
	r.WithFlagValidator("my-flag", func(_ string) *output.Error {
		return &output.Error{
			Code:     "FIRST_REJECT",
			Message:  "first wins",
			ExitCode: 2,
		}
	})
	// Second registration overwrites; accepts everything.
	r.WithFlagValidator("my-flag", func(_ string) *output.Error {
		return nil
	})
	r.WrapRunE()

	r.Cmd.SetArgs([]string{"do", "--my-flag", "anything"})
	err := r.Cmd.Execute()
	require.NoError(t, err)
	assert.Equal(t, int32(1), called.Load(),
		"second WithFlagValidator must overwrite the first")
}

func TestWithFlagValidator_UnknownFlagSilent(t *testing.T) {
	r, called, _, stderr := buildFlagValidatorRoot(t)

	// Register a validator for a flag that doesn't exist anywhere
	// on the tree. Must NOT panic; must silently never fire.
	r.WithFlagValidator("nonexistent-flag", func(_ string) *output.Error {
		return &output.Error{
			Code:     "SHOULD_NOT_FIRE",
			Message:  "validator hit for unknown flag",
			ExitCode: 2,
		}
	})
	r.WrapRunE()

	r.Cmd.SetArgs([]string{"do", "--my-flag", "anything"})
	err := r.Cmd.Execute()
	require.NoError(t, err)
	assert.Equal(t, int32(1), called.Load())
	assert.Empty(t, stderr.String())
}

func TestWithFlagValidator_WrapRunEIdempotent(t *testing.T) {
	r, called, _, stderr := buildFlagValidatorRoot(t)

	var validatorCalls atomic.Int32
	r.WithFlagValidator("my-flag", func(_ string) *output.Error {
		validatorCalls.Add(1)
		return &output.Error{
			Code:     "REJECT",
			Message:  "no",
			ExitCode: 2,
		}
	})

	// Two WrapRunE calls — the second must be a no-op for already-
	// wrapped leaves so the validator doesn't get stacked.
	r.WrapRunE()
	r.WrapRunE()

	r.Cmd.SetArgs([]string{"do", "--format", "json", "--my-flag", "bad"})
	err := r.Cmd.Execute()
	require.Error(t, err)
	assert.Equal(t, int32(0), called.Load(),
		"leaf RunE must not run when validator rejects")
	assert.Equal(t, int32(1), validatorCalls.Load(),
		"validator must fire exactly once per invocation, even after double WrapRunE")
	assert.True(t,
		strings.Contains(stderr.String(), "REJECT"),
		"expected envelope on stderr, got %q", stderr.String())
}

// A validator rejection on a conditional-idempotent leaf invoked
// with --idempotency-key must NOT record an entry in the store —
// otherwise a subsequent call with the same key + a valid flag value
// would replay the (empty) cached output instead of dispatching.
func TestWithFlagValidator_DoesNotPoisonIdemStore(t *testing.T) {
	store := idemstore.Memory()
	defer store.Close()

	r := cli.New(cli.Config{
		Name:            "validatortool",
		Version:         "0.0.0",
		Short:           "flag validator + idempotency interaction",
		DisableValidate: true,
	}, cli.WithIdempotencyStore(store))
	r.Cmd.PersistentFlags().String("my-flag", "default", "test flag")

	var leafCalls atomic.Int32
	leaf := &cobra.Command{
		Use:   "do",
		Short: "do thing",
		RunE: func(cmd *cobra.Command, _ []string) error {
			leafCalls.Add(1)
			_, _ = cmd.OutOrStdout().Write([]byte("ok"))
			return nil
		},
	}
	cli.SetSideEffect(leaf, cli.SideEffectWrite)
	cli.SetIdempotency(leaf, cli.IdempotencyConditional)
	r.Cmd.AddCommand(leaf)

	r.WithFlagValidator("my-flag", func(v string) *output.Error {
		if v == "bad" {
			return &output.Error{
				Code:     "INVALID_FLAG",
				Message:  "my-flag rejected",
				ExitCode: 2,
			}
		}
		return nil
	})
	r.WrapRunE()

	// First call: rejected by validator while carrying an idempotency
	// key. Must NOT write anything to the store.
	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	r.Cmd.SetArgs([]string{"do", "--idempotency-key", "k1", "--my-flag", "bad"})
	err := r.Cmd.ExecuteContext(context.Background())
	require.Error(t, err, "rejection must surface")
	assert.Equal(t, int32(0), leafCalls.Load(), "leaf must not run")

	_, hit, err := store.Lookup(context.Background(), "k1")
	require.NoError(t, err)
	assert.False(t, hit, "store must NOT have recorded the rejected call")

	// Second call: same key, valid flag value. Leaf must dispatch —
	// proving the cache wasn't poisoned by the first rejection.
	stdout.Reset()
	stderr.Reset()
	r.Cmd.SetArgs([]string{"do", "--idempotency-key", "k1", "--my-flag", "good"})
	err = r.Cmd.ExecuteContext(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), leafCalls.Load(), "leaf must dispatch on the retry")
	assert.Equal(t, "ok", stdout.String(), "leaf payload must reach stdout")
}
