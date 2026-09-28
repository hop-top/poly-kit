package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/memory"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// callersPolicy refuses writes to everyone but alice, and gives alice
// a budget of two writes an hour.
func callersPolicy(maxOps int) policy.Policy {
	return policy.Policy{
		Name:  "team",
		Allow: map[policy.SideEffect][]string{policy.SideEffectWrite: {}},
		Callers: []policy.CallerRule{{
			Principal: "alice",
			Allow:     map[policy.SideEffect][]string{policy.SideEffectWrite: {"*"}},
			MaxOps:    maxOps,
			Window:    time.Hour,
		}},
	}
}

// callersRoot serves p on loopback, authenticating with bearer and
// counting budgets in store.
func callersRoot(t *testing.T, p policy.Policy, auth api.AuthFunc, store kv.Store) *Root {
	t.Helper()
	opts := []func(*Root){
		WithAPI(APIConfig{Auth: auth}),
		WithPolicy(func(string) (policy.Policy, error) { return p, nil }),
	}
	if store != nil {
		opts = append(opts, WithUsageStore(store))
	}
	return authRoot(t, opts...)
}

// listing returns the discovery entries by name.
func listing(t *testing.T, base string, hdr map[string]string) map[string]api.DiscoveryEntry {
	t.Helper()
	resp, body := get(t, base+"/v1/commands", hdr)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	var doc api.DiscoveryDocument
	require.NoError(t, json.Unmarshal(body, &doc))
	out := map[string]api.DiscoveryEntry{}
	for _, e := range doc.Commands {
		out[e.Name] = e
	}
	return out
}

func TestServedPolicy_CallerRuleWidensForItsCaller(t *testing.T) {
	r := callersRoot(t, callersPolicy(0), bearer(nil), nil)
	base, stop := serveAPI(t, r, "--policy", "team")
	defer stop()

	// The rule answers at the permission gate and again inside the
	// command, where the same --policy runs for the same caller.
	resp, body := postJSON(t, base+"/v1/commands/add", `{}`, map[string]string{"Authorization": "Bearer alice"})
	assert.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	assert.Contains(t, string(body), "added")

	resp, body = postJSON(t, base+"/v1/commands/add", `{}`, map[string]string{"Authorization": "Bearer bob"})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, string(body))
	assert.Contains(t, string(body), "permission_denied")
	assert.Contains(t, string(body), "policy: write not allowed for add")
}

func TestServedPolicy_DiscoveryReflectsTheCaller(t *testing.T) {
	scopes := map[string][]string{"alice": {"admin"}}
	r := callersRoot(t, callersPolicy(0), bearer(scopes), nil)
	base, stop := serveAPI(t, r, "--policy", "team")
	defer stop()

	alice := listing(t, base, map[string]string{"Authorization": "Bearer alice"})
	assert.True(t, alice["add"].Invocable, "alice's rule allows writes")
	assert.True(t, alice["admin reset"].Invocable, "alice holds admin")

	bob := listing(t, base, map[string]string{"Authorization": "Bearer bob"})
	assert.False(t, bob["add"].Invocable)
	assert.Equal(t, cmdsurface.ReasonPermissionDenied, bob["add"].Reason)
	assert.False(t, bob["admin reset"].Invocable)
	assert.Equal(t, cmdsurface.ReasonInsufficientScope, bob["admin reset"].Reason,
		"the scope check answers first, as it does per call")
	assert.True(t, bob["list"].Invocable)
}

func TestServedPolicy_SharedListingWithholdsOnlyWhatNobodyMayRun(t *testing.T) {
	// No verifier: every request is anonymous and gets the shared
	// listing, which cannot know who calls.
	p := callersPolicy(0)
	p.Allow[policy.SideEffectDestructive] = []string{}
	r := callersRoot(t, p, nil, nil)
	base, stop := serveAPI(t, r, "--policy", "team")
	defer stop()

	shared := listing(t, base, nil)
	assert.True(t, shared["add"].Invocable, "a caller rule allows add for someone")
	resp, body := postJSON(t, base+"/v1/commands/add", `{}`, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode,
		"an unestablished caller is answered by the base rules: %s", body)
}

func TestServedPolicy_BudgetIsPersistedPerCallerOverAWindow(t *testing.T) {
	store := memory.New()
	alice := map[string]string{"Authorization": "Bearer alice"}

	r := callersRoot(t, callersPolicy(2), bearer(nil), store)
	base, stop := serveAPI(t, r, "--policy", "team")
	// Discovery reads the budget without spending it.
	for range 3 {
		assert.True(t, listing(t, base, alice)["add"].Invocable)
	}
	for range 2 {
		resp, body := postJSON(t, base+"/v1/commands/add", `{}`, alice)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	}
	assert.Equal(t, cmdsurface.ReasonPermissionDenied, listing(t, base, alice)["add"].Reason,
		"a spent budget shows in the caller's listing")
	resp, body := postJSON(t, base+"/v1/commands/add", `{}`, alice)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, string(body))
	assert.Contains(t, string(body), "max_ops budget of 2 per 1h0m0s spent; resets at")
	stop()

	// A restart counts on from the same store.
	r = callersRoot(t, callersPolicy(2), bearer(nil), store)
	base, stop = serveAPI(t, r, "--policy", "team")
	defer stop()
	resp, body = postJSON(t, base+"/v1/commands/add", `{}`, alice)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, string(body))
}

func TestServedPolicy_DefaultUsageStoreIsAStateFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	r := callersRoot(t, callersPolicy(1), bearer(nil), nil)
	base, stop := serveAPI(t, r, "--policy", "team")
	defer stop()
	resp, body := postJSON(t, base+"/v1/commands/add", `{}`, map[string]string{"Authorization": "Bearer alice"})
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))

	ledger, err := usageLedgerOf(r)
	require.NoError(t, err)
	keys, err := ledger.Keys(context.Background(), "policy/")
	require.NoError(t, err)
	assert.Len(t, keys, 1)
}

func TestPolicyCaller(t *testing.T) {
	assert.Nil(t, policyCaller(cmdsurface.Meta{Caller: "mallory"}), "a claim establishes nobody")

	c := policyCaller(cmdsurface.Meta{
		Caller: "alice", Tenant: "acme", Established: cmdsurface.EstablishedVerified,
		Extra: map[string]string{"scopes": "a,b"},
	})
	require.NotNil(t, c)
	assert.Equal(t, policy.Caller{Principal: "alice", Tenant: "acme", Scopes: []string{"a", "b"}}, *c)

	owner := policyCaller(cmdsurface.Meta{Established: cmdsurface.EstablishedTransport})
	require.NotNil(t, owner)
	assert.True(t, owner.Owner)

	// The transport vouched for the connection, not a name: the owner
	// matches no rule, and spends no budget, of the name it claims.
	claim := policyCaller(cmdsurface.Meta{Caller: "alice", Tenant: "acme",
		Surface: cmdsurface.SurfaceSocket, Established: cmdsurface.EstablishedTransport})
	require.NotNil(t, claim)
	assert.Equal(t, policy.Caller{Owner: true}, *claim)

	// A verifier on the socket (peer credentials) establishes a caller
	// like any other: it holds its credential's scopes, not every scope.
	peer := policyCaller(cmdsurface.Meta{Caller: "uid:501",
		Surface: cmdsurface.SurfaceSocket, Established: cmdsurface.EstablishedVerified})
	require.NotNil(t, peer)
	assert.False(t, peer.Owner)
	assert.Empty(t, peer.Scopes)
}

// A caller rule that requires a scope admits a verified socket caller
// (peer credentials) only when its credential holds that scope: holding
// every scope is the transport-vouched owner's alone, whatever the
// surface.
func TestServedPolicy_ScopeRuleRefusesAVerifiedSocketCallerWithoutIt(t *testing.T) {
	p := policy.Policy{
		Name:  "team",
		Allow: map[policy.SideEffect][]string{policy.SideEffectWrite: {}},
		Callers: []policy.CallerRule{{
			Scope: "widgets:write",
			Allow: map[policy.SideEffect][]string{policy.SideEffectWrite: {"*"}},
		}},
	}
	r := authRoot(t)
	gate := permissionFromEngine(policy.NewEngine(p, 0), nil)
	add, _, err := r.Cmd.Find([]string{"add"})
	require.NoError(t, err)
	leaf := &cmdsurface.Leaf{Cmd: add, Path: []string{"add"}}
	ctx := context.Background()

	peer := cmdsurface.Meta{Surface: cmdsurface.SurfaceSocket, Caller: "uid:501",
		Established: cmdsurface.EstablishedVerified}
	assert.False(t, gate(ctx, peer, leaf).Allowed, "a peer caller with no scopes does not match the scope rule")

	peer.Extra = map[string]string{"scopes": "widgets:write"}
	assert.True(t, gate(ctx, peer, leaf).Allowed, "one holding the scope does")

	owner := cmdsurface.Meta{Surface: cmdsurface.SurfaceSocket, Established: cmdsurface.EstablishedTransport}
	assert.True(t, gate(ctx, owner, leaf).Allowed, "the owner-only socket's caller holds every scope")
}
