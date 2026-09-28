package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/transport/cmdsurface"
)

// exposedBridge builds the bridge a service with exposure exp applies.
func exposedBridge(t *testing.T, r *Root, svc string, exp ServeExposure) *cmdsurface.Bridge {
	t.Helper()
	opts, err := ServeBridgeOptionsExposed(r, svc, exp)
	require.NoError(t, err)
	b := cmdsurface.New(r.Cmd, opts...)
	b.Expose("*", cmdsurface.SurfaceMCP)
	return b
}

func invokeAdd(b *cmdsurface.Bridge, meta cmdsurface.Meta) error {
	meta.Surface = cmdsurface.SurfaceMCP
	_, err := b.Invoke(context.Background(), cmdsurface.Invocation{Path: []string{"add"}, Meta: meta})
	return err
}

func TestKitDefaultIsTheRemoteDefaultOnEveryService(t *testing.T) {
	anon := cmdsurface.Meta{}
	alice := cmdsurface.Meta{Caller: "alice", Established: cmdsurface.EstablishedVerified}

	for _, svc := range []string{"api", "rpc", "mcp"} {
		t.Run(svc, func(t *testing.T) {
			r := authRoot(t)
			remote := exposedBridge(t, r, svc, ServeExposure{})
			err := invokeAdd(remote, anon)
			require.ErrorIs(t, err, cmdsurface.ErrPermissionDenied)
			assert.Contains(t, err.Error(), "policy kit-default:")
			assert.NoError(t, invokeAdd(remote, alice), "an established principal writes")

			assert.NoError(t, invokeAdd(exposedBridge(t, r, svc, ServeExposure{Loopback: true}), anon),
				"loopback keeps allow-by-default")
			assert.NoError(t, invokeAdd(exposedBridge(t, r, svc, ServeExposure{InsecureNoPolicy: true}), anon),
				"insecure_no_policy opts out")
		})
	}
}

func TestServeBridgeOptionsForReadsTheOptInKey(t *testing.T) {
	r := authRoot(t)
	opts, err := ServeBridgeOptionsFor(r, "mcp", false)
	require.NoError(t, err)
	b := cmdsurface.New(r.Cmd, opts...)
	b.Expose("*", cmdsurface.SurfaceMCP)
	require.ErrorIs(t, invokeAdd(b, cmdsurface.Meta{}), cmdsurface.ErrPermissionDenied)

	r.Viper.Set("services.mcp.insecure_no_policy", true)
	opts, err = ServeBridgeOptionsFor(r, "mcp", false)
	require.NoError(t, err)
	b = cmdsurface.New(r.Cmd, opts...)
	b.Expose("*", cmdsurface.SurfaceMCP)
	assert.NoError(t, invokeAdd(b, cmdsurface.Meta{}))
}

func TestNamedPolicyWinsOverKitDefault(t *testing.T) {
	open := policy.Policy{Name: "open"}
	r := authRoot(t, WithPolicy(func(string) (policy.Policy, error) { return open, nil }))
	require.NoError(t, r.Cmd.PersistentFlags().Set(policyFlag, "open"))
	assert.NoError(t, invokeAdd(exposedBridge(t, r, "api", ServeExposure{}), cmdsurface.Meta{}))
}

func TestKitDefaultNameResolvesWithoutALoader(t *testing.T) {
	r := authRoot(t)
	require.NoError(t, r.Cmd.PersistentFlags().Set(policyFlag, policy.KitDefaultName))
	engine, err := r.newPolicyEngine(r.Cmd)
	require.NoError(t, err)
	assert.Equal(t, policy.KitDefaultName, engine.Policy().Name)
}
