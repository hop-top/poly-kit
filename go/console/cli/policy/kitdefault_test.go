package policy_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/policy"
)

func TestKitDefault(t *testing.T) {
	t.Parallel()
	e := policy.NewEngine(policy.KitDefault(), 0)
	assert.Equal(t, policy.KitDefaultName, e.Policy().Name)

	list := leafAt("kit", policy.SideEffectRead, "widget", "list")
	add := leafAt("kit", "write-shared", "widget", "add")
	bare := leafAt("kit", "", "widget", "touch")
	purge := leafAt("kit", "destructive-shared", "widget", "purge")
	purge.Annotations["kit/permissions"] = "widgets:admin"
	drop := leafAt("kit", policy.SideEffectDestructive, "widget", "drop")
	alice := &policy.Caller{Principal: "alice"}

	allowed := func(cmd *cobra.Command, c *policy.Caller) bool {
		ok, _, _ := e.AuthorizeFor(cmd, c)
		return ok
	}
	assert.True(t, allowed(list, nil), "anyone reads")
	assert.True(t, allowed(add, alice), "an established principal writes")
	assert.True(t, allowed(bare, alice), "unannotated is a write")
	assert.True(t, allowed(purge, alice), "a destructive command declaring its scope runs; the scope check asks for it")

	for name, tc := range map[string]struct {
		cmd  *cobra.Command
		c    *policy.Caller
		want string
	}{
		"write, unestablished":        {add, nil, "write-shared not allowed for widget add"},
		"unannotated, unestablished":  {bare, nil, "write not allowed for widget touch"},
		"destructive, unestablished":  {purge, nil, "destructive-shared not allowed for widget purge"},
		"unscoped destructive, alice": {drop, alice, "widget drop declares no kit/permissions scope"},
	} {
		ok, _, reason := e.AuthorizeFor(tc.cmd, tc.c)
		assert.False(t, ok, name)
		assert.Contains(t, reason, tc.want, name)
		assert.Contains(t, reason, "(policy kit-default:", "%s: the refusal names the policy and the remedy", name)
	}

	assert.False(t, e.RefusedForEveryone(add), "an established caller may write")
	assert.False(t, e.RefusedForEveryone(purge))
	assert.True(t, e.RefusedForEveryone(drop), "nobody runs an unscoped destructive command")
	assert.True(t, e.Mutating(bare), "unannotated counts as write")
	assert.False(t, e.Mutating(list))
}

func TestUnannotatedAndRequireDeclaredScopeLoad(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "p.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
unannotated: write
callers:
  - require_declared_scope: [destructive]
`), 0o600))
	p, err := policy.Load(path)
	require.NoError(t, err)
	assert.Equal(t, policy.SideEffectWrite, p.Unannotated)
	assert.Equal(t, []policy.SideEffect{policy.SideEffectDestructive}, p.Callers[0].RequireDeclaredScope)

	require.NoError(t, os.WriteFile(path, []byte("unannotated: sometimes\n"), 0o600))
	_, err = policy.Load(path)
	require.Error(t, err)
}

func TestLoadNamedResolvesKitDefaultWithoutAFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p, err := policy.LoadNamed("tool", policy.KitDefaultName)
	require.NoError(t, err)
	assert.Equal(t, policy.KitDefault(), p)
}
