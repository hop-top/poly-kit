package cli_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/identity"
)

// bareIdentityRoot is a Root with an identity keypair and no serve
// plane, plus whatever opts add.
func bareIdentityRoot(t *testing.T, opts ...func(*cli.Root)) *cli.Root {
	t.Helper()
	dir := t.TempDir()
	all := append([]func(*cli.Root){cli.WithIdentity(cli.IdentityConfig{Dir: filepath.Join(dir, "id")})}, opts...)
	return cli.New(cli.Config{Name: "tool", Version: "1.0.0", DisableValidate: true}, all...)
}

// tokenLeaves lists the leaves of r's token commands, and how many
// token commands r has.
func tokenLeaves(r *cli.Root) (leaves []string, commands int) {
	for _, c := range r.Cmd.Commands() {
		if c.Name() != "token" {
			continue
		}
		commands++
		for _, l := range c.Commands() {
			leaves = append(leaves, l.Name())
		}
	}
	sort.Strings(leaves)
	return leaves, commands
}

func runRoot(t *testing.T, r *cli.Root, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetErr(io.Discard)
	r.SetArgs(args)
	err := r.Execute(context.Background())
	return out.String(), err
}

// TestBareIdentityMountsTheSigningTokenLeaves pins the self-signed
// client model: an identity keypair alone, with no serve plane, mounts
// a kit-reserved token command with exactly create, decode and claims.
// verify and key need the serve plane and are not there.
func TestBareIdentityMountsTheSigningTokenLeaves(t *testing.T) {
	r := bareIdentityRoot(t)
	leaves, n := tokenLeaves(r)
	assert.Equal(t, 1, n)
	assert.Equal(t, []string{"claims", "create", "decode"}, leaves)
	assert.True(t, r.IsReserved("token"), "kit's token command is reserved")

	none := cli.New(cli.Config{Name: "tool", Version: "1.0.0", DisableValidate: true})
	leaves, n = tokenLeaves(none)
	assert.Zero(t, n, "no identity, no token command: %v", leaves)
	assert.False(t, none.IsReserved("token"))
}

// TestBareIdentityGraftsOntoTheAdoptersTokenCommand pins that an
// adopter's own token command, mounted through an option, receives the
// kit's leaves in place of a second token command, keeps its own, and
// is reserved.
func TestBareIdentityGraftsOntoTheAdoptersTokenCommand(t *testing.T) {
	own := func(r *cli.Root) {
		c := &cobra.Command{Use: "token", Short: "the adopter's own"}
		c.AddCommand(&cobra.Command{Use: "hello", Short: "hi", RunE: func(*cobra.Command, []string) error { return nil }})
		r.Cmd.AddCommand(c)
	}
	r := bareIdentityRoot(t, own)
	leaves, n := tokenLeaves(r)
	assert.Equal(t, 1, n, "one token command")
	assert.Equal(t, []string{"claims", "create", "decode", "hello"}, leaves)
	assert.True(t, r.IsReserved("token"))
}

// TestBareIdentityTokenCreateSigns pins what the cli-side create signs:
// EdDSA, the keypair's kid in the header, sub, iat, exp, and the
// optional claims, scopes both as scope (space-delimited) and scopes.
func TestBareIdentityTokenCreateSigns(t *testing.T) {
	r := bareIdentityRoot(t)
	out, err := runRoot(t, r, "token", "create", "--sub", "alice", "--scopes", "items:read,items:write",
		"--tenant", "acme", "--audience", "kit-api", "--issuer", "tool", "--expires", "1h")
	require.NoError(t, err)
	raw := strings.TrimSpace(out)
	require.NotNil(t, r.Identity)

	parts := strings.Split(raw, ".")
	require.Len(t, parts, 3)
	hdrJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	var hdr map[string]string
	require.NoError(t, json.Unmarshal(hdrJSON, &hdr))
	assert.Equal(t, map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": r.Identity.PublicKeyID()}, hdr)

	claims, err := identity.VerifyJWT(raw, r.Identity.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, "alice", claims.Subject)
	assert.Equal(t, "acme", claims.Tenant)
	assert.Equal(t, "tool", claims.Issuer)
	assert.Equal(t, identity.Audience{"kit-api"}, claims.Audience)
	assert.Equal(t, []string{"items:read", "items:write"}, claims.Scopes)
	assert.Equal(t, "items:read items:write", claims.Scope)
	assert.InDelta(t, time.Now().Add(time.Hour).Unix(), claims.ExpiresAt, 5)

	for name, args := range map[string][]string{
		"no subject":         {"token", "create"},
		"no lifetime":        {"token", "create", "--sub", "a", "--expires", "0s"},
		"a scope with space": {"token", "create", "--sub", "a", "--scopes", "items read"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runRoot(t, bareIdentityRoot(t), args...)
			require.Error(t, err)
		})
	}
}

// TestBareIdentityTokenDecodeAndClaims pins the two inspection leaves on
// a Root with no serve plane.
func TestBareIdentityTokenDecodeAndClaims(t *testing.T) {
	out, err := runRoot(t, bareIdentityRoot(t), "token", "create", "--sub", "bob")
	require.NoError(t, err)

	dec, err := runRoot(t, bareIdentityRoot(t), "token", "decode", strings.TrimSpace(out))
	require.NoError(t, err)
	var p map[string]any
	require.NoError(t, json.Unmarshal([]byte(dec), &p), dec)
	assert.Equal(t, "bob", p["sub"])

	tmpl, err := runRoot(t, bareIdentityRoot(t), "token", "claims", "--sub", "carol")
	require.NoError(t, err)
	var c map[string]any
	require.NoError(t, json.Unmarshal([]byte(tmpl), &c), tmpl)
	assert.Equal(t, "carol", c["sub"])
}
