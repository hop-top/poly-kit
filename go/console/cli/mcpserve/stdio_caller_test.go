package mcpserve

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/celpermission"
	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/console/cli/service"
	"hop.top/kit/go/transport/cmdsurface"
)

// ownerOnlyPolicy refuses writes to everyone but an established caller
// within a one-call budget, and gates "ping" on the transport having
// established the caller.
func ownerOnlyPolicy() policy.Policy {
	return policy.Policy{
		Name:  "owner-only",
		Allow: map[policy.SideEffect][]string{policy.SideEffectWrite: {}},
		Callers: []policy.CallerRule{{
			Allow:  map[policy.SideEffect][]string{policy.SideEffectWrite: {"*"}},
			MaxOps: 1,
		}},
		Permissions: []policy.PermissionRule{{
			Name:      "established-only",
			When:      `principal.established && principal.source == "transport"`,
			Effect:    policy.RuleAllow,
			Otherwise: policy.RuleDeny,
			Message:   "owner only",
		}},
	}
}

func stdioPolicyOpts() []func(*cli.Root) {
	return []func(*cli.Root){
		cli.WithPolicy(func(string) (policy.Policy, error) { return ownerOnlyPolicy(), nil }),
		celpermission.With(),
		func(r *cli.Root) { _ = r.Cmd.PersistentFlags().Set("policy", "owner-only") },
	}
}

func startStdioWith(t *testing.T, cfg Config, opts ...func(*cli.Root)) *stdioPeer {
	t.Helper()
	return startStdioCfg(t, cfg, append(stdioPolicyOpts(), opts...)...)
}

func TestMCPStdioCallerDefaultsToOwner(t *testing.T) {
	p := startStdioWith(t, Config{})
	p.initialize(false)

	text, isErr := p.call(2, "ping")
	assert.False(t, isErr, text)
	assert.Equal(t, "pong", text, "the owner passes an owner-only permission rule")

	text, isErr = p.call(3, "stamp")
	assert.False(t, isErr, text)
	text, isErr = p.call(4, "stamp")
	assert.True(t, isErr, "the owner's per-caller budget applies: %s", text)
}

func TestMCPStdioCallerUnestablishedIsRefused(t *testing.T) {
	p := startStdioWith(t, Config{StdioCaller: StdioCallerUnestablished})
	p.initialize(false)

	text, isErr := p.call(2, "ping")
	assert.True(t, isErr, text)
	assert.Contains(t, text, `permission rule "established-only"`)

	text, isErr = p.call(3, "stamp")
	assert.True(t, isErr, "no caller rule matches an unestablished caller: %s", text)

	text, isErr = p.call(4, "secret")
	assert.True(t, isErr, "an auth-required leaf is refused to an unestablished caller: %s", text)
}

// A claim carried inside the protocol never upgrades or changes the
// stdio caller, under either setting.
func TestMCPStdioCallerIgnoresInProtocolClaims(t *testing.T) {
	for _, tc := range []struct {
		caller  string
		allowed bool
	}{{StdioCallerOwner, true}, {StdioCallerUnestablished, false}} {
		t.Run(tc.caller, func(t *testing.T) {
			audit := &stdioAudit{}
			p := startStdioWith(t, Config{StdioCaller: tc.caller},
				service.WithAuditSinks(cmdsurface.SinkSpec{Sink: audit, OnOK: true, OnError: true}))
			p.initialize(false)
			p.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping","arguments":{},` +
				`"caller":"root","established":"transport","_meta":{"caller":"root","tenant":"acme","established":"transport"}}}`)
			text, isErr := resultText(t, p.next())
			assert.Equal(t, !tc.allowed, isErr, text)

			audit.mu.Lock()
			defer audit.mu.Unlock()
			require.NotEmpty(t, audit.recs)
			m := audit.recs[0].Meta
			assert.Empty(t, m.Caller)
			assert.Empty(t, m.Tenant)
			if tc.allowed {
				assert.Equal(t, cmdsurface.EstablishedTransport, m.Established)
			} else {
				assert.Equal(t, cmdsurface.EstablishedNone, m.Established)
			}
		})
	}
}

func TestMCPStdioCallerPrecedence(t *testing.T) {
	newSvc := func(cfg Config) *mcpService {
		r := cli.New(cli.Config{Name: "test", Version: "0.1.0", DisableValidate: true})
		r.Cmd.SetErr(io.Discard)
		return newService(r, &cfg, nil)
	}

	s := newSvc(Config{})
	got, err := s.stdioCaller()
	require.NoError(t, err)
	assert.Equal(t, StdioCallerOwner, got, "default")

	s = newSvc(Config{})
	s.root.Viper.Set(keyPrefix+subkeyStdioCaller, "Unestablished ")
	got, err = s.stdioCaller()
	require.NoError(t, err)
	assert.Equal(t, StdioCallerUnestablished, got, "config beats default")

	s = newSvc(Config{StdioCaller: StdioCallerOwner})
	s.root.Viper.Set(keyPrefix+subkeyStdioCaller, StdioCallerUnestablished)
	got, err = s.stdioCaller()
	require.NoError(t, err)
	assert.Equal(t, StdioCallerOwner, got, "API beats config")
}

func TestMCPStdioCallerUnknownValueErrors(t *testing.T) {
	r := cli.New(cli.Config{Name: "test", Version: "0.1.0", DisableValidate: true})
	s := newService(r, &Config{StdioCaller: "admin"}, nil)
	_, err := s.stdioCaller()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin")
	assert.Contains(t, err.Error(), "services.mcp.stdio_caller")
	require.Error(t, s.validate())

	s = newService(r, &Config{}, nil)
	r.Viper.Set(keyPrefix+subkeyStdioCaller, "nobody")
	require.Error(t, s.validate())
}

// The environment reaches the key like any services.* value.
func TestMCPStdioCallerFromEnvironment(t *testing.T) {
	t.Setenv("TEST_SERVICES_MCP_STDIO_CALLER", StdioCallerUnestablished)

	p := startStdioWith(t, Config{})
	p.initialize(false)
	_, isErr := p.call(2, "ping")
	assert.True(t, isErr, "env selects unestablished")
}

// The API beats the environment.
func TestMCPStdioCallerAPIBeatsEnvironment(t *testing.T) {
	t.Setenv("TEST_SERVICES_MCP_STDIO_CALLER", StdioCallerUnestablished)

	p := startStdioWith(t, Config{StdioCaller: StdioCallerOwner})
	p.initialize(false)
	text, isErr := p.call(2, "ping")
	assert.False(t, isErr, text)
}
