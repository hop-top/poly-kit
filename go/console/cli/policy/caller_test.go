package policy_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/policy"
)

// teamPolicy refuses writes and destructive commands to everyone,
// then widens per caller.
func teamPolicy() policy.Policy {
	return policy.Policy{
		Name: "team",
		Allow: map[policy.SideEffect][]string{
			policy.SideEffectWrite:       {},
			policy.SideEffectDestructive: {},
		},
		Callers: []policy.CallerRule{
			{
				Principal: "alice",
				Allow: map[policy.SideEffect][]string{
					policy.SideEffectDestructive: {"widget purge"},
				},
			},
			{
				Scope: "widgets:write",
				Allow: map[policy.SideEffect][]string{
					policy.SideEffectWrite: {"*"},
				},
				MaxOps: 2,
				Window: time.Hour,
			},
			{
				Tenant: "acme-*",
				Allow: map[policy.SideEffect][]string{
					policy.SideEffectWrite: {"widget add"},
				},
			},
		},
	}
}

func TestAuthorizeFor_BaseAppliesWithoutCaller(t *testing.T) {
	t.Parallel()
	e := policy.NewEngine(teamPolicy(), 0)
	add := leafAt("kit", policy.SideEffectWrite, "widget", "add")

	allowed, _, reason := e.AuthorizeFor(add, nil)
	assert.False(t, allowed)
	assert.Equal(t, "policy: write not allowed for widget add", reason)

	allowed, _, _ = e.Authorize(add)
	assert.False(t, allowed, "Authorize is AuthorizeFor with no caller")
}

func TestAuthorizeFor_FirstMatchingRuleWidensItsClasses(t *testing.T) {
	t.Parallel()
	e := policy.NewEngine(teamPolicy(), 0)
	purge := leafAt("kit", policy.SideEffectDestructive, "widget", "purge")
	add := leafAt("kit", policy.SideEffectWrite, "widget", "add")

	alice := &policy.Caller{Principal: "alice"}
	allowed, _, _ := e.AuthorizeFor(purge, alice)
	assert.True(t, allowed, "alice's rule allows widget purge")

	// alice's rule declares no write class, so the base answers it,
	// and later rules are not consulted: the first match wins.
	allowed, _, _ = e.AuthorizeFor(add, &policy.Caller{Principal: "alice", Tenant: "acme-eu"})
	assert.False(t, allowed)

	bob := &policy.Caller{Principal: "bob"}
	allowed, _, _ = e.AuthorizeFor(purge, bob)
	assert.False(t, allowed, "no rule matches bob")
}

func TestAuthorizeFor_ScopeAndTenantDimensions(t *testing.T) {
	t.Parallel()
	e := policy.NewEngine(teamPolicy(), 0)
	add := leafAt("kit", policy.SideEffectWrite, "widget", "add")
	rename := leafAt("kit", policy.SideEffectWrite, "widget", "rename")

	scoped := &policy.Caller{Principal: "ci", Scopes: []string{"widgets:read", "widgets:write"}}
	allowed, _, _ := e.AuthorizeFor(rename, scoped)
	assert.True(t, allowed, "a caller holding widgets:write matches the scope rule")

	tenant := &policy.Caller{Principal: "carol", Tenant: "acme-eu"}
	allowed, _, _ = e.AuthorizeFor(add, tenant)
	assert.True(t, allowed)
	allowed, _, _ = e.AuthorizeFor(rename, tenant)
	assert.False(t, allowed, "the tenant rule allows widget add only")

	// The owner holds the owner's authority: every scope counts as held.
	owner := &policy.Caller{Owner: true}
	allowed, _, _ = e.AuthorizeFor(rename, owner)
	assert.True(t, allowed)
}

func TestAuthorizeFor_ExpandedTiersMeetTheirClass(t *testing.T) {
	t.Parallel()
	p := policy.Policy{Allow: map[policy.SideEffect][]string{
		policy.SideEffectWrite:       {},
		policy.SideEffectDestructive: {},
	}}
	e := policy.NewEngine(p, 0)
	for _, se := range []policy.SideEffect{"write-local", "write-shared", "destructive-local", "destructive-shared"} {
		allowed, _, _ := e.Authorize(leafAt("kit", se, "x"))
		assert.False(t, allowed, "%s is refused by its legacy class", se)
	}

	// An exact tier key still answers for its own tier.
	p.Allow["destructive-local"] = []string{"x"}
	e = policy.NewEngine(p, 0)
	allowed, _, _ := e.Authorize(leafAt("kit", "destructive-local", "x"))
	assert.True(t, allowed)
	allowed, _, _ = e.Authorize(leafAt("kit", "destructive-shared", "x"))
	assert.False(t, allowed)
}

func TestRefusedForEveryone(t *testing.T) {
	t.Parallel()
	e := policy.NewEngine(teamPolicy(), 0)
	assert.False(t, e.RefusedForEveryone(leafAt("kit", policy.SideEffectDestructive, "widget", "purge")),
		"alice may purge")
	assert.True(t, e.RefusedForEveryone(leafAt("kit", policy.SideEffectDestructive, "widget", "drop")),
		"no rule allows widget drop")
	assert.False(t, e.RefusedForEveryone(leafAt("kit", policy.SideEffectRead, "widget", "list")))

	plain := policy.NewEngine(policy.Policy{Allow: map[policy.SideEffect][]string{policy.SideEffectWrite: {}}}, 0)
	assert.True(t, plain.RefusedForEveryone(leafAt("kit", policy.SideEffectWrite, "widget", "add")))
}

func TestBudgetFor(t *testing.T) {
	t.Parallel()
	e := policy.NewEngine(teamPolicy(), 0)

	b, ok := e.BudgetFor(&policy.Caller{Principal: "ci", Tenant: "t1", Scopes: []string{"widgets:write"}})
	require.True(t, ok)
	assert.Equal(t, 2, b.MaxOps)
	assert.Equal(t, time.Hour, b.Window)
	other, _ := e.BudgetFor(&policy.Caller{Principal: "ci", Tenant: "t2", Scopes: []string{"widgets:write"}})
	assert.NotEqual(t, b.Key, other.Key, "one principal acting for two tenants spends two budgets")

	_, ok = e.BudgetFor(&policy.Caller{Principal: "alice"})
	assert.False(t, ok, "alice's rule sets no max_ops")
	_, ok = e.BudgetFor(nil)
	assert.False(t, ok, "an unestablished caller has no served budget")
}

func TestBudgetFor_WindowDefaultsToAnHour(t *testing.T) {
	t.Parallel()
	e := policy.NewEngine(policy.Policy{Callers: []policy.CallerRule{{MaxOps: 5}}}, 0)
	b, ok := e.BudgetFor(&policy.Caller{Principal: "x"})
	require.True(t, ok)
	assert.Equal(t, policy.DefaultBudgetWindow, b.Window)
}

func TestLoad_CallersSection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "team.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
allow:
  write: []
callers:
  - principal: "svc-*"
    tenant: acme
    scope: widgets:write
    allow:
      write: ["*"]
    max_ops: 100
    window: 24h
`), 0o600))
	p, err := policy.Load(path)
	require.NoError(t, err)
	require.Len(t, p.Callers, 1)
	r := p.Callers[0]
	assert.Equal(t, "svc-*", r.Principal)
	assert.Equal(t, "acme", r.Tenant)
	assert.Equal(t, "widgets:write", r.Scope)
	assert.Equal(t, []string{"*"}, r.Allow[policy.SideEffectWrite])
	assert.Equal(t, 100, r.MaxOps)
	assert.Equal(t, 24*time.Hour, r.Window)
}

func TestLoad_RefusesABadCallersSection(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"negative max_ops": "callers:\n  - max_ops: -1\n",
		"negative window":  "callers:\n  - max_ops: 1\n    window: -1h\n",
		"bad glob":         "callers:\n  - principal: \"[a\"\n",
		"unknown class":    "callers:\n  - allow:\n      delete: [\"*\"]\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "p.yaml")
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			_, err := policy.Load(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "callers[0]")
		})
	}
}

func TestLoad_SinglePolicyFileStillLoads(t *testing.T) {
	t.Parallel()
	// Today's shape, unknown top-level keys included, keeps loading:
	// another section of the schema may sit beside it.
	path := filepath.Join(t.TempDir(), "ops.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: ops
allow:
  destructive: ["delete:*"]
max_ops: 3
require_confirm: ["delete:*"]
something_else:
  - x
`), 0o600))
	p, err := policy.Load(path)
	require.NoError(t, err)
	assert.Equal(t, 3, p.MaxOps)
	assert.Empty(t, p.Callers)
}

// leafAt builds root > path... with the side-effect tag on the last
// command, so CommandPath() is "<root> <path...>".
func leafAt(rootName string, se policy.SideEffect, path ...string) *cobra.Command {
	parent := &cobra.Command{Use: rootName}
	var c *cobra.Command
	for _, name := range path {
		c = &cobra.Command{Use: name, Run: func(*cobra.Command, []string) {}}
		parent.AddCommand(c)
		parent = c
	}
	if se != "" {
		c.Annotations = map[string]string{"kit/side-effect": string(se)}
	}
	return c
}
