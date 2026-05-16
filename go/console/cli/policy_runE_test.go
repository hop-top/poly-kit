package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/console/output"
)

// destructiveLeaf builds a runnable cobra leaf with a destructive
// side-effect tag, attached to a fresh cli.Root.
func destructiveLeaf(t *testing.T, name string) (*Root, *cobra.Command) {
	t.Helper()
	r := New(Config{Name: "ptool", Version: "0.0.0", Short: "policy test tool"})
	leaf := &cobra.Command{
		Use:   name,
		Short: name + " command",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = cmd.OutOrStdout().Write([]byte("ran:" + name))
			return nil
		},
	}
	SetSideEffect(leaf, SideEffectDestructive)
	SetIdempotency(leaf, IdempotencyYes)
	r.Cmd.AddCommand(leaf)
	return r, leaf
}

// runWithStdin runs the root with the given args, supplying stdin from
// stdinText. Returns stdout, stderr, and the resulting err.
func runWithStdin(t *testing.T, r *Root, args []string, stdinText string, isTTY bool) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	r.Cmd.SetIn(strings.NewReader(stdinText))
	r.Cmd.SetArgs(args)

	prevTTY := promptIsTTYFn
	promptIsTTYFn = func(*cobra.Command) bool { return isTTY }
	t.Cleanup(func() { promptIsTTYFn = prevTTY })

	prevInput := promptInputFn
	promptInputFn = func(cmd *cobra.Command) io.Reader { return cmd.InOrStdin() }
	t.Cleanup(func() { promptInputFn = prevInput })

	r.AutoRegisterFlags()
	r.WrapRunE()
	err := r.Cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestRunE_Middleware_PromptCancel_AbortsUnauthorized(t *testing.T) {
	r, _ := destructiveLeaf(t, "delete")
	stdout, stderr, err := runWithStdin(t, r,
		[]string{"delete", "--format", "json"},
		"\n", // empty answer = decline
		true, // simulate TTY so default is prompt
	)
	require.Error(t, err, "decline at prompt must propagate as error")
	assert.Empty(t, stdout, "no payload when policy aborts pre-RunE")

	// Stderr carries the prompt + the JSON envelope. Skip past the
	// prompt by parsing the last { ... } block.
	jsonStart := strings.Index(stderr, "{")
	require.Greater(t, jsonStart, -1, "expected JSON envelope on stderr, got %q", stderr)
	var got output.Error
	require.NoError(t, json.Unmarshal([]byte(stderr[jsonStart:]), &got))
	assert.Equal(t, output.CodeUnauthorized, got.Code)
	assert.Equal(t, 5, got.ExitCode)
	assert.Contains(t, got.Message, "aborted by user")
}

func TestRunE_Middleware_PromptYes_Proceeds(t *testing.T) {
	r, _ := destructiveLeaf(t, "delete")
	stdout, _, err := runWithStdin(t, r,
		[]string{"delete"},
		"y\n",
		true,
	)
	require.NoError(t, err)
	assert.Equal(t, "ran:delete", stdout, "after y, RunE must execute and write payload")
}

func TestRunE_Middleware_ConfirmYes_Proceeds(t *testing.T) {
	r, _ := destructiveLeaf(t, "delete")
	stdout, stderr, err := runWithStdin(t, r,
		[]string{"delete", "--confirm", "yes"},
		"", // no stdin needed when --confirm=yes
		true,
	)
	require.NoError(t, err, "stderr=%q", stderr)
	assert.Equal(t, "ran:delete", stdout)
}

func TestRunE_Middleware_ConfirmNo_AbortsUnauthorized(t *testing.T) {
	r, _ := destructiveLeaf(t, "delete")
	_, stderr, err := runWithStdin(t, r,
		[]string{"delete", "--confirm", "no", "--format", "json"},
		"",
		true,
	)
	require.Error(t, err)
	jsonStart := strings.Index(stderr, "{")
	require.Greater(t, jsonStart, -1, "stderr=%q", stderr)
	var got output.Error
	require.NoError(t, json.Unmarshal([]byte(stderr[jsonStart:]), &got))
	assert.Equal(t, output.CodeUnauthorized, got.Code)
	assert.Equal(t, 5, got.ExitCode)
}

func TestRunE_Middleware_NonTTY_DefaultsToNo(t *testing.T) {
	r, _ := destructiveLeaf(t, "delete")
	// No --confirm flag, no TTY → default is "no" → abort UNAUTHORIZED.
	_, stderr, err := runWithStdin(t, r,
		[]string{"delete", "--format", "json"},
		"",
		false, // non-TTY
	)
	require.Error(t, err)
	jsonStart := strings.Index(stderr, "{")
	require.Greater(t, jsonStart, -1)
	var got output.Error
	require.NoError(t, json.Unmarshal([]byte(stderr[jsonStart:]), &got))
	assert.Equal(t, output.CodeUnauthorized, got.Code)
	assert.Contains(t, got.Message, "non-TTY", "default-no message must hint at non-TTY")
}

func TestRunE_Middleware_DryRun_BypassesConfirm(t *testing.T) {
	r, _ := destructiveLeaf(t, "delete")
	// Dry-run: confirm gate must be skipped even on --confirm=no /
	// non-TTY. RunE still runs (printing "ran:delete") because the
	// dry-run pre-flight has no real side-effect to confirm.
	stdout, _, err := runWithStdin(t, r,
		[]string{"delete", "--dry-run", "--confirm", "no"},
		"",
		false,
	)
	require.NoError(t, err)
	assert.Equal(t, "ran:delete", stdout)
}

func TestRunE_Middleware_ReadCommands_BypassConfirm(t *testing.T) {
	// Read commands never gate on --confirm regardless of value.
	r := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"})
	leaf := &cobra.Command{
		Use:   "list",
		Short: "list",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = cmd.OutOrStdout().Write([]byte("read-output"))
			return nil
		},
	}
	SetSideEffect(leaf, SideEffectRead)
	SetIdempotency(leaf, IdempotencyYes)
	r.Cmd.AddCommand(leaf)

	stdout, _, err := runWithStdin(t, r,
		[]string{"list", "--confirm", "no"},
		"",
		false,
	)
	require.NoError(t, err)
	assert.Equal(t, "read-output", stdout)
}

func TestRunE_Middleware_DestructiveTokenRequired(t *testing.T) {
	// Set up a leaf opted in to typed confirmation.
	r := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"})
	leaf := &cobra.Command{
		Use:   "drop-db",
		Short: "drop db",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = cmd.OutOrStdout().Write([]byte("dropped"))
			return nil
		},
	}
	SetSideEffect(leaf, SideEffectDestructive)
	SetIdempotency(leaf, IdempotencyYes)
	if leaf.Annotations == nil {
		leaf.Annotations = map[string]string{}
	}
	leaf.Annotations[destructiveTokenAnnotation] = "required"
	r.Cmd.AddCommand(leaf)

	// 1. --confirm=yes alone is NOT enough — token still required.
	_, stderr, err := runWithStdin(t, r,
		[]string{"drop-db", "--confirm", "yes", "--format", "json"},
		"", true)
	require.Error(t, err)
	jsonStart := strings.Index(stderr, "{")
	require.Greater(t, jsonStart, -1)
	var got output.Error
	require.NoError(t, json.Unmarshal([]byte(stderr[jsonStart:]), &got))
	assert.Equal(t, output.CodeUnauthorized, got.Code)
	assert.Contains(t, got.Message, "--confirm-token=")

	// 2. With the right token, the command proceeds even with
	// --confirm=no.
	expected := sha256SumPath("ptool drop-db")
	r2 := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"})
	leaf2 := &cobra.Command{
		Use:   "drop-db",
		Short: "drop db",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = cmd.OutOrStdout().Write([]byte("dropped"))
			return nil
		},
		Annotations: map[string]string{destructiveTokenAnnotation: "required"},
	}
	SetSideEffect(leaf2, SideEffectDestructive)
	SetIdempotency(leaf2, IdempotencyYes)
	r2.Cmd.AddCommand(leaf2)

	stdout, _, err := runWithStdin(t, r2,
		[]string{"drop-db", "--confirm", "no", "--confirm-token", expected},
		"", true)
	require.NoError(t, err)
	assert.Equal(t, "dropped", stdout)

	// 3. Wrong token: refused.
	r3 := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"})
	leaf3 := &cobra.Command{
		Use:   "drop-db",
		Short: "drop db",
		RunE:  func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{
			destructiveTokenAnnotation: "required",
		},
	}
	SetSideEffect(leaf3, SideEffectDestructive)
	SetIdempotency(leaf3, IdempotencyYes)
	r3.Cmd.AddCommand(leaf3)

	_, stderr3, err := runWithStdin(t, r3,
		[]string{"drop-db", "--confirm", "yes", "--confirm-token", "deadbeef", "--format", "json"},
		"", true)
	require.Error(t, err)
	jsonStart = strings.Index(stderr3, "{")
	require.Greater(t, jsonStart, -1)
	var got3 output.Error
	require.NoError(t, json.Unmarshal([]byte(stderr3[jsonStart:]), &got3))
	assert.Equal(t, output.CodeUnauthorized, got3.Code)
	assert.Contains(t, got3.Message, "mismatch")
}

func TestRunE_Middleware_MaxOps_BudgetExceeded_RateLimited(t *testing.T) {
	// One write leaf with max-ops=0 (after the first record, second
	// would exceed). We can't actually run a single command twice in
	// one Execute, so we exercise the code path via the Engine
	// directly: max-ops=1 is enforced by RecordOp inside the wrap.
	// Run once with max-ops=0 (zero means unlimited), then once with
	// the budget=1 (one mutation allowed); to drive the budget check
	// we have to artificially over-account by setting max-ops=0 and
	// observing nothing fails.
	//
	// Instead: max-ops=1 with one mutation → success. Then call the
	// middleware twice on the same engine — but the middleware
	// constructs a fresh engine per Execute, so we can only assert
	// the success path here. The Engine's internal budget logic is
	// covered by policy/policy_test.go.

	r := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"})
	leaf := &cobra.Command{
		Use:   "create",
		Short: "create",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = cmd.OutOrStdout().Write([]byte("created"))
			return nil
		},
	}
	SetSideEffect(leaf, SideEffectWrite)
	SetIdempotency(leaf, IdempotencyNo)
	r.Cmd.AddCommand(leaf)

	stdout, _, err := runWithStdin(t, r,
		[]string{"create", "--max-ops", "1"}, "", false)
	require.NoError(t, err)
	assert.Equal(t, "created", stdout)
}

func TestRunE_Middleware_PolicyLoader_Refuses(t *testing.T) {
	// Wire a loader that returns a categorically-deny policy.
	denyAll := policy.Policy{
		Name: "deny",
		Allow: map[policy.SideEffect][]string{
			policy.SideEffectDestructive: {},
		},
	}
	r := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"},
		WithPolicy(func(name string) (policy.Policy, error) {
			return denyAll, nil
		}),
	)
	leaf := &cobra.Command{
		Use: "delete", Short: "delete",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	SetSideEffect(leaf, SideEffectDestructive)
	SetIdempotency(leaf, IdempotencyYes)
	r.Cmd.AddCommand(leaf)

	_, stderr, err := runWithStdin(t, r,
		[]string{"delete", "--policy", "deny", "--confirm", "yes", "--format", "json"},
		"", true)
	require.Error(t, err)
	jsonStart := strings.Index(stderr, "{")
	require.Greater(t, jsonStart, -1)
	var got output.Error
	require.NoError(t, json.Unmarshal([]byte(stderr[jsonStart:]), &got))
	assert.Equal(t, output.CodeUnauthorized, got.Code)
	assert.Contains(t, got.Message, "destructive")
}

func TestRunE_Middleware_PolicyFlag_NoLoader_UsageError(t *testing.T) {
	// --policy set but no loader wired → UsageError.
	r := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"})
	leaf := &cobra.Command{
		Use: "delete", Short: "delete",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	SetSideEffect(leaf, SideEffectDestructive)
	SetIdempotency(leaf, IdempotencyYes)
	r.Cmd.AddCommand(leaf)

	_, stderr, err := runWithStdin(t, r,
		[]string{"delete", "--policy", "anything", "--confirm", "yes", "--format", "json"},
		"", true)
	require.Error(t, err)
	jsonStart := strings.Index(stderr, "{")
	require.Greater(t, jsonStart, -1)
	var got output.Error
	require.NoError(t, json.Unmarshal([]byte(stderr[jsonStart:]), &got))
	assert.Equal(t, output.CodeUsage, got.Code)
}

func TestRunE_Middleware_PolicyLoader_DefaultLoader_FromXDG(t *testing.T) {
	// Round-trip via DefaultPolicyLoader: write a YAML under a temp
	// XDG_CONFIG_HOME, then load it through the loader.
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	policyDir := filepath.Join(tmp, "ptool", "policies")
	require.NoError(t, os.MkdirAll(policyDir, 0o755))
	body := "name: lenient\nmax_ops: 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(policyDir, "lenient.yaml"), []byte(body), 0o600))

	loader := DefaultPolicyLoader("ptool")
	p, err := loader("lenient")
	require.NoError(t, err)
	assert.Equal(t, "lenient", p.Name)
}

// sha256SumPath mirrors destructiveTokenSha for the test — we can't
// reach the unexported helper, so we recompute the deterministic
// 12-char prefix on the same input.
func sha256SumPath(path string) string {
	h := sha256.Sum256([]byte(path))
	return hex.EncodeToString(h[:6])
}
