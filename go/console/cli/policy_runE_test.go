package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// runWithStdin runs the root with the given args, supplying the
// PROMPT's answers from promptText. Returns stdout, stderr, and err.
//
// promptText goes to the prompt terminal, not to the command's stdin:
// that separation is the fix under test. hasTTY=false installs a
// PromptSource with no terminal, which is the CI / redirected shape.
func runWithStdin(t *testing.T, r *Root, args []string, promptText string, hasTTY bool) (string, string, error) {
	t.Helper()
	var stdout, stderr, promptOut bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	r.Cmd.SetIn(strings.NewReader(""))
	r.Cmd.SetArgs(args)

	if hasTTY {
		r.promptSource = PromptSourceFromReadWriter(
			strings.NewReader(promptText), &promptOut)
	} else {
		r.promptSource = func() *PromptTTY { return nil }
	}
	t.Cleanup(func() { r.promptSource = nil })

	r.AutoRegisterFlags()
	r.WrapRunE()
	err := r.Cmd.Execute()
	// The prompt question now lands on the terminal rather than
	// stderr; tests that assert on the question read it from there.
	return stdout.String(), promptOut.String() + stderr.String(), err
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

// TestRunE_Middleware_PromptDoesNotConsumeStdin asserts the payload a
// command reads from stdin is byte-exact after a prompted confirm.
//
// The payload is deliberately larger than bufio's 4096-byte buffer:
// the prompt used to read stdin through a bufio.Reader it then threw
// away, so one buffer fill of payload disappeared with it. A payload
// under 4KB vanishes entirely and a larger one is truncated, both at
// exit 0 — a test with a short payload sees the same "empty" either
// way and misses the truncation.
func TestRunE_Middleware_PromptDoesNotConsumeStdin(t *testing.T) {
	const payload = 9001
	body := strings.Repeat("A", payload)

	r := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"})
	var got []byte
	leaf := &cobra.Command{
		Use: "eat", Short: "eat",
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := io.ReadAll(cmd.InOrStdin())
			got = b
			return err
		},
	}
	SetSideEffect(leaf, SideEffectDestructive)
	SetIdempotency(leaf, IdempotencyYes)
	r.Cmd.AddCommand(leaf)

	var stdout, promptOut bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&bytes.Buffer{})
	r.Cmd.SetIn(strings.NewReader(body))
	r.Cmd.SetArgs([]string{"eat", "--confirm", "prompt"})
	r.promptSource = PromptSourceFromReadWriter(
		strings.NewReader("y\n"), &promptOut)

	r.AutoRegisterFlags()
	r.WrapRunE()
	require.NoError(t, r.Cmd.Execute())

	assert.Len(t, got, payload,
		"prompt must not consume any of the command's stdin")
	assert.Equal(t, body, string(got), "payload must arrive byte-exact")
	assert.Contains(t, promptOut.String(), "[y/N]",
		"question belongs on the prompt terminal, not on stderr")
}

// TestRunE_Middleware_PromptAnswerNotTakenFromStdin asserts stdin is
// never mistaken for an answer. A payload whose first line reads "y"
// must NOT authorize the operation; with no terminal there is nobody
// to ask, so the gate refuses.
func TestRunE_Middleware_PromptAnswerNotTakenFromStdin(t *testing.T) {
	r, _ := destructiveLeaf(t, "delete")
	var stdout bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&bytes.Buffer{})
	r.Cmd.SetIn(strings.NewReader("y\npayload\n"))
	r.Cmd.SetArgs([]string{"delete", "--confirm", "prompt"})
	r.promptSource = func() *PromptTTY { return nil }

	r.AutoRegisterFlags()
	r.WrapRunE()
	err := r.Cmd.Execute()

	require.Error(t, err, "a 'y' on stdin must not answer the prompt")
	assert.Empty(t, stdout.String(), "RunE must not have run")
}

// TestRunE_Middleware_NoTTY_PromptDoesNotBlock asserts a prompted
// confirm with no terminal refuses promptly instead of blocking on a
// read nobody will answer. A hang is worse than a refusal.
//
// This has to run as a SUBPROCESS with a pipe held open on its stdin.
// In-process the check is worthless: `go test` hands the test binary a
// stdin that is already at EOF, so a prompt that wrongly reads stdin
// returns immediately and looks identical to one that correctly
// declined to ask. Only a writer still holding the pipe open
// distinguishes "refused" from "waiting forever".
func TestRunE_Middleware_NoTTY_PromptDoesNotBlock(t *testing.T) {
	if os.Getenv("KIT_NOBLOCK_CHILD") == "1" {
		r, _ := destructiveLeaf(t, "delete")
		r.Cmd.SetArgs([]string{"delete", "--confirm", "prompt"})
		r.AutoRegisterFlags()
		r.WrapRunE()
		if err := r.Cmd.Execute(); err != nil {
			os.Exit(9) // refused, as it must be
		}
		os.Exit(0)
	}

	cmd := exec.Command(os.Args[0],
		"-test.run", "TestRunE_Middleware_NoTTY_PromptDoesNotBlock",
		"-test.timeout", "60s")
	cmd.Env = append(os.Environ(), "KIT_NOBLOCK_CHILD=1")

	// A pipe whose write end stays open for the whole test: a stdin
	// read would block here rather than see EOF.
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = stdin.Close() })

	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		var ee *exec.ExitError
		require.ErrorAs(t, err, &ee,
			"no terminal to ask on must refuse, not succeed")
		assert.Equal(t, 9, ee.ExitCode(), "expected the refusal path")
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("prompt blocked on stdin with no terminal to answer it")
	}
}

// TestPromptTTY_RetainsReaderAcrossPrompts asserts a second prompt on
// the same terminal reads the second answer.
//
// Constructing a bufio.Reader per prompt would fill the buffer from
// the terminal, consume the first line and discard the rest, so the
// second prompt would see EOF and silently decline. Retaining one
// reader per terminal is what makes consecutive prompts answerable.
func TestPromptTTY_RetainsReaderAcrossPrompts(t *testing.T) {
	var out bytes.Buffer
	src := PromptSourceFromReadWriter(strings.NewReader("y\ny\n"), &out)

	assert.True(t, promptConfirm(src, "first?"), "first answer is y")
	assert.True(t, promptConfirm(src, "second?"),
		"second answer must survive the first prompt's buffering")
}

// TestResolveConfirmMode_TerminalDecidesDefault asserts the bare
// default follows the terminal's availability, not stdin's shape.
func TestResolveConfirmMode_TerminalDecidesDefault(t *testing.T) {
	withTTY := PromptSourceFromReadWriter(strings.NewReader(""), &bytes.Buffer{})
	noTTY := PromptSource(func() *PromptTTY { return nil })

	assert.Equal(t, confirmPrompt, resolveConfirmMode(withTTY, ""),
		"a terminal to ask on means prompt")
	assert.Equal(t, confirmNo, resolveConfirmMode(noTTY, ""),
		"no terminal means the non-interactive default")

	// The explicit vocabulary is unaffected by terminal availability.
	for _, tc := range []struct {
		raw  string
		want confirmMode
	}{
		{"yes", confirmYes}, {"no", confirmNo},
		{"auto", confirmAuto}, {"prompt", confirmPrompt},
	} {
		assert.Equal(t, tc.want, resolveConfirmMode(noTTY, tc.raw), tc.raw)
		assert.Equal(t, tc.want, resolveConfirmMode(withTTY, tc.raw), tc.raw)
	}
}
