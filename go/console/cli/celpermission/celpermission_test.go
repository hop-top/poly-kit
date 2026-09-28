package celpermission_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/celpermission"
	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/transport/cmdsurface"
)

// tree is root with "item add" (write-local), "item list" (read) and
// "item export" (read, kit/permissions items:export).
func tree() *cobra.Command {
	root := &cobra.Command{Use: "tool"}
	item := &cobra.Command{Use: "item"}
	leaf := func(use string, ann map[string]string) *cobra.Command {
		c := &cobra.Command{
			Use:         use,
			RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("ran"); return nil },
			Annotations: ann,
		}
		c.Flags().Bool("force", false, "")
		c.Flags().Int("count", 0, "")
		return c
	}
	item.AddCommand(
		leaf("add", map[string]string{"kit/side-effect": "write-local"}),
		leaf("list", map[string]string{"kit/side-effect": "read"}),
		leaf("export", map[string]string{"kit/side-effect": "read", "kit/permissions": "items:export"}),
	)
	root.AddCommand(item)
	return root
}

// okRunner answers every call without running anything.
type okRunner struct{}

func (okRunner) Run(context.Context, cmdsurface.Invocation) (cmdsurface.Result, error) {
	return cmdsurface.Result{Stdout: "ran"}, nil
}

func (okRunner) Stream(_ context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	close(out)
	return nil
}

// recordSink keeps every audited error.
type recordSink struct {
	mu   sync.Mutex
	errs []string
}

func (s *recordSink) Emit(_ context.Context, _ cmdsurface.Invocation, _ cmdsurface.Result, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.errs = append(s.errs, err.Error())
	}
	return nil
}

func deny(name, when, msg string) policy.PermissionRule {
	return policy.PermissionRule{Name: name, When: when, Effect: policy.RuleDeny, Otherwise: policy.RuleAllow, Message: msg}
}

// bridge compiles rules and serves tree on REST behind them.
func bridge(t *testing.T, sink cmdsurface.Sink, rules ...policy.PermissionRule) *cmdsurface.Bridge {
	t.Helper()
	gate, err := celpermission.New(rules)
	require.NoError(t, err)
	opts := []cmdsurface.Option{cmdsurface.WithRunner(okRunner{}), cmdsurface.WithPermission(gate)}
	if sink != nil {
		opts = append(opts, cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnError: true}))
	}
	b := cmdsurface.New(tree(), opts...)
	b.Expose("*", cmdsurface.SurfaceREST, cmdsurface.SurfaceMCP)
	return b
}

func verified(caller, tenant, scopes string) cmdsurface.Meta {
	return cmdsurface.Meta{
		Surface:     cmdsurface.SurfaceREST,
		Caller:      caller,
		Tenant:      tenant,
		Established: cmdsurface.EstablishedVerified,
		Extra:       map[string]string{"scopes": scopes, "remote_addr": "10.1.2.3:51234"},
	}
}

// TestNew_Bindings pins every binding a rule reads, one rule per
// binding: each denies exactly the invocation that sets it.
func TestNew_Bindings(t *testing.T) {
	cases := []struct {
		binding string
		when    string
		hit     cmdsurface.Invocation
	}{
		{"caller", `principal.id == "mallory"`, cmdsurface.Invocation{Meta: verified("mallory", "acme", "")}},
		{"tenant", `principal.tenant == "globex"`, cmdsurface.Invocation{Meta: verified("alice", "globex", "")}},
		{"scopes", `"items:write" in principal.scopes`, cmdsurface.Invocation{Meta: verified("alice", "acme", "items:read,items:write")}},
		{"established", `!principal.established`, cmdsurface.Invocation{Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceREST, Caller: "alice"}}},
		{"source", `principal.source == "transport"`, cmdsurface.Invocation{Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceREST, Established: cmdsurface.EstablishedTransport}}},
		{"surface", `context.surface == "mcp"`, cmdsurface.Invocation{Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceMCP, Caller: "alice", Established: cmdsurface.EstablishedVerified}}},
		{"client address", `context.client_addr == "192.168.0.9"`, cmdsurface.Invocation{Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceREST, Extra: map[string]string{"remote_addr": "192.168.0.9:4000"}}}},
		{"leaf path", `resource.id == "item add" && resource.path[1] == "add"`, cmdsurface.Invocation{Path: []string{"item", "add"}, Meta: verified("alice", "acme", "")}},
		{"tier", `resource.tier == "write-local"`, cmdsurface.Invocation{Path: []string{"item", "add"}, Meta: verified("alice", "acme", "")}},
		{"args", `"secret" in payload.args`, cmdsurface.Invocation{Args: []string{"secret"}, Meta: verified("alice", "acme", "")}},
		{"flags", `has(payload.flags.force) && payload.flags.force == true`, cmdsurface.Invocation{Flags: map[string]any{"force": true}, Meta: verified("alice", "acme", "")}},
	}
	for _, tc := range cases {
		t.Run(tc.binding, func(t *testing.T) {
			b := bridge(t, nil, deny("r", tc.when, tc.binding))

			hit := tc.hit
			if hit.Path == nil {
				hit.Path = []string{"item", "list"}
			}
			_, err := b.Invoke(context.Background(), hit)
			require.ErrorIs(t, err, cmdsurface.ErrPermissionDenied, "the invocation setting %s is refused", tc.binding)

			_, err = b.Invoke(context.Background(), cmdsurface.Invocation{
				Path: []string{"item", "list"},
				Meta: verified("alice", "acme", "items:read"),
			})
			require.NoError(t, err, "an invocation not matching %s runs", tc.binding)
		})
	}
}

// TestNew_ClaimedScopesAreNotScopes pins that principal.scopes holds
// only what a verifier granted: a scopes entry on an unestablished
// call is a claim and reads as empty.
func TestNew_ClaimedScopesAreNotScopes(t *testing.T) {
	b := bridge(t, nil, policy.PermissionRule{
		Name: "needs-admin", When: `"items:admin" in principal.scopes`,
		Effect: policy.RuleAllow, Otherwise: policy.RuleDeny, Message: "admins only",
	})
	claimed := cmdsurface.Meta{Surface: cmdsurface.SurfaceREST, Caller: "alice", Extra: map[string]string{"scopes": "items:admin"}}
	_, err := b.Invoke(context.Background(), cmdsurface.Invocation{Path: []string{"item", "list"}, Meta: claimed})
	require.ErrorIs(t, err, cmdsurface.ErrPermissionDenied)

	_, err = b.Invoke(context.Background(), cmdsurface.Invocation{Path: []string{"item", "list"}, Meta: verified("alice", "acme", "items:admin")})
	require.NoError(t, err)
}

// TestNew_DenyOverrides pins the composition: one denying rule refuses
// however many allow, and the refusal names the rule that denied.
func TestNew_DenyOverrides(t *testing.T) {
	sink := &recordSink{}
	b := bridge(t, sink,
		policy.PermissionRule{Name: "everyone", When: "true", Effect: policy.RuleAllow, Otherwise: policy.RuleAllow},
		deny("no-globex", `principal.tenant == "globex"`, "globex is offboarded"),
		policy.PermissionRule{Name: "also-everyone", When: "true", Effect: policy.RuleAllow, Otherwise: policy.RuleAllow},
	)
	_, err := b.Invoke(context.Background(), cmdsurface.Invocation{Path: []string{"item", "list"}, Meta: verified("alice", "globex", "")})
	require.ErrorIs(t, err, cmdsurface.ErrPermissionDenied)
	assert.Equal(t,
		`cmdsurface: permission denied: item list on rest: permission rule "no-globex": globex is offboarded`,
		err.Error())

	sink.mu.Lock()
	defer sink.mu.Unlock()
	require.Len(t, sink.errs, 1)
	assert.Contains(t, sink.errs[0], `permission rule "no-globex"`, "the audit record names the rule")
}

// TestNew_EvaluationErrorDenies pins fail-closed evaluation: a rule
// reading a flag the caller did not set, without has(), cannot be
// evaluated, and the invocation is refused naming the rule.
func TestNew_EvaluationErrorDenies(t *testing.T) {
	b := bridge(t, nil, deny("force-needs-admin", `payload.flags.force && !("items:admin" in principal.scopes)`, "force needs admin"))
	_, err := b.Invoke(context.Background(), cmdsurface.Invocation{Path: []string{"item", "list"}, Meta: verified("alice", "acme", "")})
	require.ErrorIs(t, err, cmdsurface.ErrPermissionDenied)
	assert.Contains(t, err.Error(), `permission rule "force-needs-admin": evaluation error`)
}

// TestNew_OnlyNarrows pins that rules sit after the scope check: an
// allow rule cannot admit a caller the scope check refused, and the
// refusal stays insufficient_scope.
func TestNew_OnlyNarrows(t *testing.T) {
	b := bridge(t, nil, policy.PermissionRule{Name: "let-everyone-in", When: "true", Effect: policy.RuleAllow, Otherwise: policy.RuleAllow})
	_, err := b.Invoke(context.Background(), cmdsurface.Invocation{Path: []string{"item", "export"}, Meta: verified("alice", "acme", "items:read")})
	require.ErrorIs(t, err, cmdsurface.ErrInsufficientScope)
}

// TestNew_MountTimeIsNeverCallerIndependent pins that discovery keeps
// a command a rule might refuse: the verdict depends on the caller.
func TestNew_MountTimeIsNeverCallerIndependent(t *testing.T) {
	gate, err := celpermission.New([]policy.PermissionRule{deny("nobody", "true", "closed")})
	require.NoError(t, err)
	b := cmdsurface.New(tree(), cmdsurface.WithRunner(okRunner{}), cmdsurface.WithPermission(gate))
	b.Expose("*", cmdsurface.SurfaceREST)
	var leaf *cmdsurface.Leaf
	for _, l := range b.Leaves() {
		if l.PathKey() == "item list" {
			leaf = l
		}
	}
	require.NotNil(t, leaf)
	dec := b.Permission(context.Background(), cmdsurface.Meta{Surface: cmdsurface.SurfaceREST}, leaf)
	assert.False(t, dec.Allowed)
	assert.False(t, dec.CallerIndependent)
}

// TestNew_RefusesARuleThatDoesNotCompile pins load-time compilation:
// the error names the rule, and no gate is returned.
func TestNew_RefusesARuleThatDoesNotCompile(t *testing.T) {
	gate, err := celpermission.New([]policy.PermissionRule{
		deny("fine", "true", ""),
		deny("typo", "principal.id ==", ""),
	})
	require.Error(t, err)
	assert.Nil(t, gate)
	assert.True(t, strings.HasPrefix(err.Error(), `permission rule "typo" does not compile: `), err.Error())

	_, err = celpermission.New([]policy.PermissionRule{deny("unknown", "caller == 1", "")})
	require.Error(t, err, "an undeclared binding is a compile error, not a runtime deny")
	assert.Contains(t, err.Error(), `permission rule "unknown"`)
}

// TestNew_RefusesAMalformedRule pins that New validates what a custom
// loader handed it, as policy.Load does for a file.
func TestNew_RefusesAMalformedRule(t *testing.T) {
	_, err := celpermission.New([]policy.PermissionRule{{Name: "x", When: "true", Effect: "block", Otherwise: "allow"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `permission rule "x": effect "block" invalid`)
}

// TestNew_NoRulesPermitsAll pins the empty block's gate.
func TestNew_NoRulesPermitsAll(t *testing.T) {
	gate, err := celpermission.New(nil)
	require.NoError(t, err)
	assert.True(t, gate(context.Background(), cmdsurface.Meta{}, &cmdsurface.Leaf{}).Allowed)
}

// TestNew_ReasonWithoutMessage pins the reason of a rule with no
// message: the rule's name alone.
func TestNew_ReasonWithoutMessage(t *testing.T) {
	b := bridge(t, nil, deny("quiet", "true", ""))
	_, err := b.Invoke(context.Background(), cmdsurface.Invocation{Path: []string{"item", "list"}, Meta: verified("alice", "acme", "")})
	require.True(t, errors.Is(err, cmdsurface.ErrPermissionDenied))
	assert.True(t, strings.HasSuffix(err.Error(), `: permission rule "quiet"`), err.Error())
}
