package rpcserve_test

import (
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/rpcserve"
	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/transport/cmdsurface"
)

// TestRPCServiceBearerJWTMode pins services.rpc.auth.mode: jwt on the
// rpc service over every protocol: a token the tool's identity signed
// is an established caller for kit/auth-required, attributed to its
// sub and tenant; no token and another signer's are Unauthenticated;
// and the mode replaces Config.Auth, which is never consulted.
func TestRPCServiceBearerJWTMode(t *testing.T) {
	rec := &recorder{}
	codeAuthCalls := 0
	cfg := rpcserve.Config{Auth: func(*http.Request) (any, error) {
		codeAuthCalls++
		return nil, errors.New("code auth refuses everything")
	}}
	run, base := startRPC(t, rpcserve.With(cfg), []string{"rpc", "--rpc-addr", "127.0.0.1:0"},
		cli.WithIdentity(cli.IdentityConfig{Dir: filepath.Join(t.TempDir(), "identity")}),
		cli.WithAuditSinks(rec.spec()),
		func(r *cli.Root) { r.Viper.Set("services.rpc.auth.mode", "jwt") })
	require.NotNil(t, run.root.Identity)

	now := time.Now()
	good, err := run.root.Identity.SignJWT(identity.Claims{Subject: "alice", Tenant: "acme",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()})
	require.NoError(t, err)
	stranger, err := identity.Generate()
	require.NoError(t, err)
	forged, err := stranger.SignJWT(identity.Claims{Subject: "mallory", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()})
	require.NoError(t, err)

	for _, proto := range protocols {
		t.Run(proto.name, func(t *testing.T) {
			c := client(base, proto)
			resp, err := c.Invoke(t.Context(), call("secret", http.Header{"Authorization": {"Bearer " + good}}))
			require.NoError(t, err)
			assert.Equal(t, "unlocked", resp.Msg.GetStdout())

			_, err = c.Invoke(t.Context(), call("ping", nil))
			assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "no token")
			_, err = c.Invoke(t.Context(), call("ping", http.Header{"Authorization": {"Bearer " + forged}}))
			assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "another signer")
		})
	}
	assert.Zero(t, codeAuthCalls, "a configured mode replaces Config.Auth")

	rec.mu.Lock()
	defer rec.mu.Unlock()
	var runs int
	for i, inv := range rec.invs {
		if rec.errs[i] == nil {
			runs++
			assert.Equal(t, "alice", inv.Meta.Caller)
			assert.Equal(t, "acme", inv.Meta.Tenant)
			assert.Equal(t, cmdsurface.EstablishedVerified, inv.Meta.Established)
		}
	}
	assert.Equal(t, len(protocols), runs)
}

// TestRPCServiceBearerModeAuthenticatesForExposure pins that a bearer
// mode satisfies the non-loopback authentication rule.
func TestRPCServiceBearerModeAuthenticatesForExposure(t *testing.T) {
	r := newServeRoot(t, rpcserve.With(rpcserve.Config{InsecureNoPolicy: true}),
		cli.WithIdentity(cli.IdentityConfig{Dir: filepath.Join(t.TempDir(), "identity")}),
		func(r *cli.Root) { r.Viper.Set("services.rpc.auth.mode", "jwt") })
	rpcCommands(r)
	err := runServeArgs(t, r, []string{"serve", "rpc", "--rpc-addr", "0.0.0.0:0"}, 2*time.Second)
	assert.NoError(t, err)
}
