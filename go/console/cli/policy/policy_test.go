package policy_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli/policy"
)

// leaf builds a cobra leaf with the given side-effect tag, attached to
// a minimal root so CommandPath() returns "<root> <leaf>".
func leaf(rootName, name string, se policy.SideEffect) *cobra.Command {
	root := &cobra.Command{Use: rootName}
	c := &cobra.Command{
		Use: name,
		Run: func(*cobra.Command, []string) {},
	}
	if se != "" {
		c.Annotations = map[string]string{"kit/side-effect": string(se)}
	}
	root.AddCommand(c)
	return c
}

func TestEngine_AuthorizeRead_AlwaysOK(t *testing.T) {
	t.Parallel()
	// Even with a categorically-deny policy, read passes.
	p := policy.Policy{
		Name: "strict",
		Allow: map[policy.SideEffect][]string{
			policy.SideEffectRead:        {},
			policy.SideEffectWrite:       {},
			policy.SideEffectDestructive: {},
		},
	}
	e := policy.NewEngine(p, 0)
	c := leaf("kit", "list", policy.SideEffectRead)

	allowed, confirm, reason := e.Authorize(c)
	assert.True(t, allowed, "read commands always pass policy")
	assert.False(t, confirm, "read commands never require confirm")
	assert.Empty(t, reason)
}

func TestEngine_AuthorizeDestructive_RequiresConfirm(t *testing.T) {
	t.Parallel()
	p := policy.Policy{
		Name: "ops",
		Allow: map[policy.SideEffect][]string{
			policy.SideEffectDestructive: {"delete:*", "rotate:*"},
		},
		RequireConfirm: []string{"delete:*", "rotate:*"},
	}
	e := policy.NewEngine(p, 0)

	allowed, confirm, _ := e.Authorize(leaf("kit", "delete", policy.SideEffectDestructive))
	assert.True(t, allowed, "delete is in allow list")
	assert.True(t, confirm, "delete is in require_confirm")

	// drop is NOT in allow — must be refused.
	allowed, _, reason := e.Authorize(leaf("kit", "drop", policy.SideEffectDestructive))
	assert.False(t, allowed, "drop is not allowed")
	assert.Contains(t, reason, "destructive")
	assert.Contains(t, reason, "drop")
}

func TestEngine_AuthorizeWrite_DefaultPermitWhenNoAllowMap(t *testing.T) {
	t.Parallel()
	// No Allow map at all = default-permit (engine acts like no policy
	// is loaded for the allow check).
	e := policy.NewEngine(policy.Policy{Name: "open"}, 0)
	allowed, confirm, _ := e.Authorize(leaf("kit", "create", policy.SideEffectWrite))
	assert.True(t, allowed)
	assert.False(t, confirm)
}

func TestEngine_AuthorizeWrite_EmptyAllowClassRefuses(t *testing.T) {
	t.Parallel()
	// Allow map present but the class is the empty list:
	// categorically refuse that class (per §8.6 example with
	// `destructive: []`).
	p := policy.Policy{
		Allow: map[policy.SideEffect][]string{
			policy.SideEffectDestructive: {},
		},
	}
	e := policy.NewEngine(p, 0)

	allowed, _, reason := e.Authorize(leaf("kit", "delete", policy.SideEffectDestructive))
	assert.False(t, allowed)
	assert.Contains(t, reason, "destructive")
}

func TestEngine_NilEngine_DefaultsAllow(t *testing.T) {
	t.Parallel()
	var e *policy.Engine
	allowed, confirm, _ := e.Authorize(leaf("kit", "delete", policy.SideEffectDestructive))
	assert.True(t, allowed, "nil Engine.Authorize must default-permit")
	assert.False(t, confirm)
}

func TestEngine_MaxOps_BudgetExceeded_Refuses(t *testing.T) {
	t.Parallel()
	e := policy.NewEngine(policy.Policy{}, 2) // budget 2
	c := leaf("kit", "create", policy.SideEffectWrite)

	require.NoError(t, e.RecordOp(c), "first op fits the budget")
	require.NoError(t, e.RecordOp(c), "second op fits the budget")
	err := e.RecordOp(c)
	require.Error(t, err, "third op exceeds the budget")
	assert.True(t, errors.Is(err, policy.ErrMaxOpsExceeded))
	assert.Equal(t, 3, e.OpsCount(), "OpsCount tracks attempts even past the budget")
}

func TestEngine_MaxOps_PolicyValueRespected(t *testing.T) {
	t.Parallel()
	// override == 0 means "use Policy.MaxOps".
	e := policy.NewEngine(policy.Policy{MaxOps: 1}, 0)
	c := leaf("kit", "create", policy.SideEffectWrite)
	require.NoError(t, e.RecordOp(c))
	require.ErrorIs(t, e.RecordOp(c), policy.ErrMaxOpsExceeded)
}

func TestEngine_MaxOps_OverrideTakesPrecedence(t *testing.T) {
	t.Parallel()
	// Policy budget 100, --max-ops=1 should win.
	e := policy.NewEngine(policy.Policy{MaxOps: 100}, 1)
	c := leaf("kit", "create", policy.SideEffectWrite)
	require.NoError(t, e.RecordOp(c))
	require.ErrorIs(t, e.RecordOp(c), policy.ErrMaxOpsExceeded)
}

func TestEngine_MaxOps_Zero_Unlimited(t *testing.T) {
	t.Parallel()
	e := policy.NewEngine(policy.Policy{}, 0)
	c := leaf("kit", "create", policy.SideEffectWrite)
	for i := 0; i < 1000; i++ {
		require.NoError(t, e.RecordOp(c))
	}
}

func TestPolicy_Load_FromYAML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "strict.yaml")
	body := `name: strict
allow:
  read: ["*"]
  write: ["edit:*", "update:*"]
  destructive: []
max_ops: 50
require_confirm: ["delete:*", "rotate:*"]
`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	p, err := policy.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "strict", p.Name)
	assert.Equal(t, 50, p.MaxOps)
	assert.Equal(t, []string{"*"}, p.Allow[policy.SideEffectRead])
	assert.Equal(t, []string{"edit:*", "update:*"}, p.Allow[policy.SideEffectWrite])
	assert.Equal(t, []string{}, p.Allow[policy.SideEffectDestructive])
	assert.Equal(t, []string{"delete:*", "rotate:*"}, p.RequireConfirm)
}

func TestPolicy_Load_NameDefaultsToFileStem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "lenient.yaml")
	require.NoError(t, os.WriteFile(path, []byte("max_ops: 5\n"), 0o600))

	p, err := policy.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "lenient", p.Name, "missing name: defaults to file stem")
	assert.Equal(t, 5, p.MaxOps)
}

func TestPolicy_Load_MissingFile_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := policy.Load(filepath.Join(t.TempDir(), "no-such.yaml"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, fs.ErrNotExist),
		"load of missing file must wrap fs.ErrNotExist")
}

func TestPolicy_Load_InvalidYAML_ReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("max_ops: : :\n"), 0o600))
	_, err := policy.Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestPolicy_Resolve_PathShape(t *testing.T) {
	t.Parallel()
	// We don't depend on $XDG_CONFIG_HOME being set; just verify the
	// shape includes "policies/<name>.yaml".
	p, err := policy.Resolve("mytool", "strict")
	require.NoError(t, err)
	assert.Contains(t, p, filepath.Join("mytool", "policies", "strict.yaml"))
}

func TestPolicy_Resolve_MissingTool_Errors(t *testing.T) {
	t.Parallel()
	_, err := policy.Resolve("", "strict")
	require.Error(t, err)
	_, err = policy.Resolve("tool", "")
	require.Error(t, err)
}

func TestEngine_Accessors(t *testing.T) {
	t.Parallel()
	p := policy.Policy{Name: "x", MaxOps: 5}
	e := policy.NewEngine(p, 0)
	assert.Equal(t, p, e.Policy())
	assert.Equal(t, 5, e.MaxOps())
	assert.Equal(t, 0, e.OpsCount())
}

func TestEngine_Authorize_AllowGlobsMatch(t *testing.T) {
	t.Parallel()
	p := policy.Policy{
		Allow: map[policy.SideEffect][]string{
			policy.SideEffectWrite: {"edit:*", "update:*"},
		},
	}
	e := policy.NewEngine(p, 0)
	allowed, _, _ := e.Authorize(leaf("kit", "edit", policy.SideEffectWrite))
	assert.True(t, allowed, "edit matches edit:*")

	allowed, _, _ = e.Authorize(leaf("kit", "update", policy.SideEffectWrite))
	assert.True(t, allowed, "update matches update:*")

	// star alone in patterns matches anything.
	p2 := policy.Policy{
		Allow: map[policy.SideEffect][]string{
			policy.SideEffectWrite: {"*"},
		},
	}
	e2 := policy.NewEngine(p2, 0)
	allowed, _, _ = e2.Authorize(leaf("kit", "anything", policy.SideEffectWrite))
	assert.True(t, allowed, "* matches anything")
}

func TestPolicy_LoadNamed_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	dir := filepath.Join(tmp, "mytool", "policies")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "ci.yaml"),
		[]byte("max_ops: 7\n"), 0o600))

	p, err := policy.LoadNamed("mytool", "ci")
	require.NoError(t, err)
	assert.Equal(t, "ci", p.Name) // defaulted from file stem
	assert.Equal(t, 7, p.MaxOps)
}

func TestPolicy_Load_TrimYmlExtension(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sloppy.yml")
	require.NoError(t, os.WriteFile(path, []byte("max_ops: 1\n"), 0o600))

	p, err := policy.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "sloppy", p.Name, ".yml extension is trimmed too")
}
