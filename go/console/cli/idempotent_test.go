package cli_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/idemstore"
)

// runnableLeaf builds a runnable cobra command with no annotations.
// Used by idempotency tests that exercise auto-apply.
func runnableLeaf(name string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: name + " command",
		Run:   func(*cobra.Command, []string) {},
	}
}

func TestIdempotency_GetSet(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}

	// Missing returns false.
	if _, ok := cli.GetIdempotency(cmd); ok {
		t.Fatalf("expected GetIdempotency to return false on a fresh command")
	}

	// Round-trip via SetIdempotency.
	cli.SetIdempotency(cmd, cli.IdempotencyConditional)
	got, ok := cli.GetIdempotency(cmd)
	require.True(t, ok, "GetIdempotency must report present after Set")
	assert.Equal(t, cli.IdempotencyConditional, got)

	// Verify the underlying annotation key is what the spec locks.
	assert.Equal(t, "conditional", cmd.Annotations["kit/idempotent"])
}

func TestIdempotency_AutoApply_DefaultsByVerb(t *testing.T) {
	r := cli.New(cli.Config{
		Name:            "vtool",
		Version:         "0.0.0",
		Short:           "validation test tool",
		DisableValidate: true,
	})

	// Stamp these without idempotency tags. side-effect tags must
	// be set so the side-effect arm of Validate doesn't trigger.
	mustSE := func(c *cobra.Command, s cli.SideEffect) *cobra.Command {
		cli.SetSideEffect(c, s)
		return c
	}
	listCmd := mustSE(runnableLeaf("list"), cli.SideEffectRead)
	createCmd := mustSE(runnableLeaf("create"), cli.SideEffectWrite)
	deleteCmd := mustSE(runnableLeaf("delete"), cli.SideEffectDestructive)
	syncCmd := mustSE(runnableLeaf("sync"), cli.SideEffectWrite)
	addCmd := mustSE(runnableLeaf("add"), cli.SideEffectWrite)

	r.Cmd.AddCommand(listCmd, createCmd, deleteCmd, syncCmd, addCmd)

	// Validate must succeed (auto-apply fills in defaults from verb).
	require.NoError(t, r.Validate(),
		"verb-default auto-apply must satisfy Validate without explicit tags")

	// Each leaf now carries the expected default tag.
	got, ok := cli.GetIdempotency(listCmd)
	require.True(t, ok)
	assert.Equal(t, cli.IdempotencyYes, got, "list defaults to yes")

	got, ok = cli.GetIdempotency(createCmd)
	require.True(t, ok)
	assert.Equal(t, cli.IdempotencyNo, got, "create defaults to no")

	got, ok = cli.GetIdempotency(deleteCmd)
	require.True(t, ok)
	assert.Equal(t, cli.IdempotencyYes, got, "delete defaults to yes (delete-by-id)")

	got, ok = cli.GetIdempotency(syncCmd)
	require.True(t, ok)
	assert.Equal(t, cli.IdempotencyYes, got, "sync defaults to yes")

	got, ok = cli.GetIdempotency(addCmd)
	require.True(t, ok)
	assert.Equal(t, cli.IdempotencyNo, got, "add defaults to no")
}

func TestIdempotency_AutoApply_DoesNotOverwrite(t *testing.T) {
	r := cli.New(cli.Config{
		Name:            "vtool",
		Version:         "0.0.0",
		Short:           "validation test tool",
		DisableValidate: true,
	})

	c := runnableLeaf("create")
	cli.SetSideEffect(c, cli.SideEffectWrite)
	cli.SetIdempotency(c, cli.IdempotencyConditional)
	r.Cmd.AddCommand(c)

	require.NoError(t, r.Validate(), "valid tags Validate")

	// Auto-apply must not overwrite the explicit conditional tag.
	got, ok := cli.GetIdempotency(c)
	require.True(t, ok)
	assert.Equal(t, cli.IdempotencyConditional, got,
		"adopter-supplied tag must survive auto-apply")
}

func TestRoot_Validate_MissingIdempotent_Fails(t *testing.T) {
	r := cli.New(cli.Config{
		Name:            "vtool",
		Version:         "0.0.0",
		Short:           "validation test tool",
		DisableValidate: true,
	})

	// Verb "rotate" is NOT in defaultIdempotency. side-effect set
	// so we isolate the idempotency failure.
	c := runnableLeaf("rotate")
	cli.SetSideEffect(c, cli.SideEffectDestructive)
	r.Cmd.AddCommand(c)

	err := r.Validate()
	require.Error(t, err, "leaf with no kit/idempotent and no default must fail")

	var ve *cli.ValidationError
	require.True(t, errors.As(err, &ve), "must be a *ValidationError")
	require.Empty(t, ve.Missing, "side-effect arm must not flag rotate")
	require.Empty(t, ve.Invalid)
	require.Len(t, ve.MissingIdempotency, 1)
	assert.Contains(t, ve.MissingIdempotency[0], "rotate",
		"command path of the missing idempotency leaf must surface")
	// Error message includes the missing path.
	assert.Contains(t, err.Error(), "rotate")
	assert.Contains(t, err.Error(), "kit/idempotent")
}

func TestRoot_Validate_InvalidIdempotent_Fails(t *testing.T) {
	r := cli.New(cli.Config{
		Name:            "vtool",
		Version:         "0.0.0",
		Short:           "validation test tool",
		DisableValidate: true,
	})

	c := runnableLeaf("list")
	cli.SetSideEffect(c, cli.SideEffectRead)
	c.Annotations["kit/idempotent"] = "maybe"
	r.Cmd.AddCommand(c)

	err := r.Validate()
	require.Error(t, err)

	var ve *cli.ValidationError
	require.True(t, errors.As(err, &ve))
	require.Empty(t, ve.MissingIdempotency)
	require.Len(t, ve.InvalidIdempotency, 1)
	assert.Contains(t, ve.InvalidIdempotency[0], "list")
	assert.Contains(t, ve.InvalidIdempotency[0], "maybe")
}

// runIdempCmd builds a one-leaf root tagged conditional+write, runs
// it with the given args and returns stdout, stderr, and the cobra
// error. The leaf's RunE writes payload to stdout and returns runErr.
func runIdempCmd(t *testing.T, store idemstore.Store, args []string,
	payload string, runErr error,
) (string, string, error) {
	t.Helper()
	r := cli.New(cli.Config{
		Name:            "idemtool",
		Version:         "0.0.0",
		Short:           "idempotency test tool",
		DisableValidate: true,
	}, cli.WithIdempotencyStore(store))

	leaf := &cobra.Command{
		Use:   "do",
		Short: "do thing",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = cmd.OutOrStdout().Write([]byte(payload))
			return runErr
		},
	}
	cli.SetSideEffect(leaf, cli.SideEffectWrite)
	cli.SetIdempotency(leaf, cli.IdempotencyConditional)
	r.Cmd.AddCommand(leaf)

	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	r.Cmd.SetArgs(args)

	r.WrapRunE()
	err := r.Cmd.ExecuteContext(context.Background())
	return stdout.String(), stderr.String(), err
}

func TestRunE_IdempotencyKey_Records_OnMiss(t *testing.T) {
	store := idemstore.Memory()
	defer store.Close()

	stdout, _, err := runIdempCmd(t, store,
		[]string{"do", "--idempotency-key", "k1"}, "hello-output", nil)
	require.NoError(t, err)
	assert.Equal(t, "hello-output", stdout, "first call writes payload to stdout")

	// Lookup via the store must find the recorded result.
	r, hit, err := store.Lookup(context.Background(), "k1")
	require.NoError(t, err)
	require.True(t, hit, "miss should have recorded the result")
	assert.Equal(t, []byte("hello-output"), r.Output)
	assert.Equal(t, 0, r.ExitCode)
	assert.Equal(t, "k1", r.Key)
}

func TestRunE_IdempotencyKey_Replays_OnHit(t *testing.T) {
	store := idemstore.Memory()
	defer store.Close()

	// Pre-seed: pretend a prior invocation recorded "first-output".
	require.NoError(t, store.Record(context.Background(), "k2",
		idemstore.Result{
			Key:      "k2",
			ExitCode: 0,
			Output:   []byte("first-output"),
		}))

	// Second invocation: RunE would write "second-output" but
	// replay should short-circuit it back to "first-output".
	stdout, _, err := runIdempCmd(t, store,
		[]string{"do", "--idempotency-key", "k2"}, "second-output", nil)
	require.NoError(t, err)
	assert.Equal(t, "first-output", stdout,
		"hit must replay recorded output, not orig RunE's payload")
}

func TestRunE_IdempotencyKey_Empty_Bypasses(t *testing.T) {
	store := idemstore.Memory()
	defer store.Close()

	// No --idempotency-key: orig RunE runs, nothing recorded.
	stdout, _, err := runIdempCmd(t, store,
		[]string{"do"}, "fresh", nil)
	require.NoError(t, err)
	assert.Equal(t, "fresh", stdout)

	// Store remains empty.
	_, hit, err := store.Lookup(context.Background(), "")
	require.NoError(t, err)
	assert.False(t, hit, "empty key must not be recorded")
}

func TestRunE_IdempotencyKey_FlagAutoRegistered(t *testing.T) {
	r := cli.New(cli.Config{
		Name:            "idemtool",
		Version:         "0.0.0",
		Short:           "idempotency test tool",
		DisableValidate: true,
	})

	// Conditional + write -> flag should auto-register.
	condWrite := &cobra.Command{
		Use:   "create",
		Short: "create thing",
		Run:   func(*cobra.Command, []string) {},
	}
	cli.SetSideEffect(condWrite, cli.SideEffectWrite)
	cli.SetIdempotency(condWrite, cli.IdempotencyConditional)
	r.Cmd.AddCommand(condWrite)

	// Conditional + read -> flag should NOT auto-register.
	condRead := &cobra.Command{
		Use:   "show",
		Short: "show thing",
		Run:   func(*cobra.Command, []string) {},
	}
	cli.SetSideEffect(condRead, cli.SideEffectRead)
	cli.SetIdempotency(condRead, cli.IdempotencyConditional)
	r.Cmd.AddCommand(condRead)

	// Yes + write -> flag should NOT auto-register.
	yesWrite := &cobra.Command{
		Use:   "edit",
		Short: "edit thing",
		Run:   func(*cobra.Command, []string) {},
	}
	cli.SetSideEffect(yesWrite, cli.SideEffectWrite)
	cli.SetIdempotency(yesWrite, cli.IdempotencyYes)
	r.Cmd.AddCommand(yesWrite)

	r.WrapRunE()

	assert.NotNil(t, condWrite.Flags().Lookup("idempotency-key"),
		"conditional + write must auto-register --idempotency-key")
	assert.Nil(t, condRead.Flags().Lookup("idempotency-key"),
		"conditional + read must NOT auto-register --idempotency-key")
	assert.Nil(t, yesWrite.Flags().Lookup("idempotency-key"),
		"yes + write must NOT auto-register --idempotency-key")
}
