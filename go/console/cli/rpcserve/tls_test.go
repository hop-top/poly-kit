package rpcserve_test

import (
	"crypto/tls"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/rpcserve"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1/cmdsurfacev1connect"
	"hop.top/kit/internal/testpki"
)

const clientPrincipal = "spiffe://example.org/tenant/acme/svc/alice"

// pki is a CA with a loopback server certificate and a client
// certificate it signed, written where the service's keys point.
type pki struct {
	ca     *testpki.CA
	keys   map[string]any
	client tls.Certificate
}

func newPKI(t *testing.T, mtls bool) *pki {
	t.Helper()
	ca := testpki.NewCA(t, "test CA")
	dir := t.TempDir()
	cert, key := ca.Localhost(t).Write(t, dir, "server")
	p := &pki{
		ca: ca,
		keys: map[string]any{
			"services.rpc.tls.cert_file": cert,
			"services.rpc.tls.key_file":  key,
		},
		client: ca.Issue(t, testpki.Leaf{CN: "alice", URIs: []string{clientPrincipal}, Client: true}).TLS,
	}
	if mtls {
		p.keys["services.rpc.auth.mode"] = "mtls"
		p.keys["services.rpc.auth.mtls.ca_file"] = ca.WriteCA(t, dir, "ca")
		p.keys["services.rpc.auth.mtls.tenant_san_pattern"] = `^spiffe://example\.org/tenant/([^/]+)/`
	}
	return p
}

// with sets the keys on the root.
func (p *pki) with(extra map[string]any) func(*cli.Root) {
	return func(r *cli.Root) {
		for k, v := range p.keys {
			r.Viper.Set(k, v)
		}
		for k, v := range extra {
			r.Viper.Set(k, v)
		}
	}
}

// httpsClient speaks HTTP/1.1 and HTTP/2 over TLS, trusting the CA
// and presenting certs.
func (p *pki) httpsClient(certs ...tls.Certificate) *http.Client {
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig:   p.ca.ClientTLS(certs...),
		ForceAttemptHTTP2: true,
	}}
}

func (p *pki) commands(base string, proto protocol, certs ...tls.Certificate) cmdsurfacev1connect.CommandsClient {
	return cmdsurfacev1connect.NewCommandsClient(p.httpsClient(certs...), base, proto.opts...)
}

func TestRPCServiceServesTLS(t *testing.T) {
	p := newPKI(t, false)
	base := startDefault(t, rpcserve.Config{}, p.with(nil))
	require.True(t, strings.HasPrefix(base, "https://127.0.0.1:"), "readiness reports the https URL: %s", base)

	for _, proto := range protocols {
		t.Run(proto.name, func(t *testing.T) {
			resp, err := p.commands(base, proto).Invoke(t.Context(), call("ping", nil))
			require.NoError(t, err, "native gRPC included: HTTP/2 is negotiated by ALPN")
			assert.Equal(t, "pong", resp.Msg.GetStdout())
		})
	}

	// HTTP/2 is negotiated by ALPN, as h2c was without TLS.
	resp, err := p.httpsClient().Get(base + cmdsurface.RPCInvokeProcedure)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 2, resp.ProtoMajor)

	// Plain TLS authenticates nobody.
	_, err = p.commands(base, protocols[0]).Invoke(t.Context(), call("secret", nil))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	// h2c is off once TLS is on: a plaintext client reaches nothing.
	_, err = client(strings.Replace(base, "https://", "http://", 1), protocols[1]).
		Invoke(t.Context(), call("ping", nil))
	require.Error(t, err)
}

func TestRPCServiceMutualTLS(t *testing.T) {
	p := newPKI(t, true)
	rec := &recorder{}
	base := startDefault(t, rpcserve.Config{}, p.with(nil), cli.WithAuditSinks(rec.spec()))

	for _, proto := range protocols {
		t.Run(proto.name, func(t *testing.T) {
			c := p.commands(base, proto, p.client)
			resp, err := c.Invoke(t.Context(), call("secret", nil))
			require.NoError(t, err, "the certificate is an established identity: kit/auth-required admits it")
			assert.Equal(t, "unlocked", resp.Msg.GetStdout())

			lines, _, err := streamAll(t.Context(), c, call("tick", nil))
			require.NoError(t, err)
			assert.Len(t, lines, 3)

			anon := p.commands(base, proto)
			_, err = anon.Invoke(t.Context(), call("ping", nil))
			assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "no certificate, no call")
		})
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	var refusals, runs int
	for i, inv := range rec.invs {
		switch {
		case errors.Is(rec.errs[i], cmdsurface.ErrAuthRefused):
			refusals++
		case rec.errs[i] == nil:
			runs++
			assert.Equal(t, clientPrincipal, inv.Meta.Caller, "principal from the certificate's SAN")
			assert.Equal(t, "acme", inv.Meta.Tenant, "tenant from the SAN pattern")
		}
	}
	assert.Equal(t, len(protocols), refusals, "every refusal is audited")
	assert.Equal(t, 2*len(protocols), runs)
}

func TestRPCServiceExposureWithTLS(t *testing.T) {
	t.Run("plain TLS does not authenticate", func(t *testing.T) {
		p := newPKI(t, false)
		oe := serveErr(t, rpcserve.Config{InsecureNoPolicy: true},
			[]string{"rpc", "--rpc-addr", "0.0.0.0:0"}, p.with(nil))
		assert.Equal(t, 2, oe.ExitCode)
		assert.Contains(t, oe.Error(), "services.rpc.insecure_remote")
	})
	t.Run("mtls authenticates", func(t *testing.T) {
		p := newPKI(t, true)
		r := newServeRoot(t, rpcserve.With(rpcserve.Config{InsecureNoPolicy: true}), p.with(nil))
		rpcCommands(r)
		err := runServeArgs(t, r, []string{"serve", "rpc", "--rpc-addr", "0.0.0.0:0"}, 2*time.Second)
		assert.NoError(t, err)
	})
	t.Run("a bad certificate is a usage error", func(t *testing.T) {
		p := newPKI(t, false)
		oe := serveErr(t, rpcserve.Config{}, []string{"rpc", "--rpc-addr", "127.0.0.1:0"},
			p.with(map[string]any{"services.rpc.tls.key_file": "/nonexistent/key.pem"}))
		assert.Equal(t, 2, oe.ExitCode)
		assert.Contains(t, oe.Error(), "services.rpc.tls.cert_file, services.rpc.tls.key_file")
	})
}
