package mcpserve_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/mcpserve"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/internal/testpki"
)

const clientPrincipal = "spiffe://example.org/tenant/acme/svc/alice"

// pki is a CA with a loopback server certificate and a client
// certificate it signed, as services.mcp.* keys.
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
			"services.mcp.tls.cert_file": cert,
			"services.mcp.tls.key_file":  key,
		},
		client: ca.Issue(t, testpki.Leaf{CN: "alice", URIs: []string{clientPrincipal}, Client: true}).TLS,
	}
	if mtls {
		p.keys["services.mcp.auth.mode"] = "mtls"
		p.keys["services.mcp.auth.mtls.ca_file"] = ca.WriteCA(t, dir, "ca")
		p.keys["services.mcp.auth.mtls.tenant_san_pattern"] = `^spiffe://example\.org/tenant/([^/]+)/`
	}
	return p
}

func (p *pki) with() func(*cli.Root) {
	return func(r *cli.Root) {
		for k, v := range p.keys {
			r.Viper.Set(k, v)
		}
	}
}

func (p *pki) connect(ctx context.Context, endpoint string, certs ...tls.Certificate) (*mcp.ClientSession, error) {
	hc := &http.Client{Transport: &http.Transport{
		TLSClientConfig:   p.ca.ClientTLS(certs...),
		ForceAttemptHTTP2: true,
	}}
	c := mcp.NewClient(&mcp.Implementation{Name: "cli-test", Version: "0"}, nil)
	return c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: hc}, nil)
}

func TestMCPServiceHTTPServesTLS(t *testing.T) {
	p := newPKI(t, false)
	_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"}, p.with())
	require.True(t, strings.HasPrefix(endpoint, "https://127.0.0.1:"), endpoint)

	sess, err := p.connect(t.Context(), endpoint)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	text, isErr := callTool(t, sess, "ping", nil)
	assert.False(t, isErr)
	assert.Equal(t, "pong", text)

	// Plain TLS authenticates nobody.
	text, isErr = callTool(t, sess, "secret", nil)
	assert.True(t, isErr)
	assert.Equal(t, "authentication required", text)
}

func TestMCPServiceHTTPMutualTLS(t *testing.T) {
	p := newPKI(t, true)
	var (
		mu    sync.Mutex
		metas []cmdsurface.Meta
		errs  []error
	)
	sink := sinkFunc(func(_ context.Context, inv cmdsurface.Invocation, _ cmdsurface.Result, err error) error {
		mu.Lock()
		defer mu.Unlock()
		metas = append(metas, inv.Meta)
		errs = append(errs, err)
		return nil
	})
	_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
		p.with(), cli.WithAuditSinks(cmdsurface.SinkSpec{Sink: sink, OnError: true, OnOK: true}))

	_, err := p.connect(t.Context(), endpoint)
	require.Error(t, err, "a client without a certificate must not connect")

	sess, err := p.connect(t.Context(), endpoint, p.client)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	text, isErr := callTool(t, sess, "secret", nil)
	require.False(t, isErr, "the certificate is an established identity: %s", text)
	assert.Equal(t, "unlocked", text)

	mu.Lock()
	defer mu.Unlock()
	var sawRefusal, sawCall bool
	for i, m := range metas {
		if errors.Is(errs[i], cmdsurface.ErrAuthRefused) {
			sawRefusal = true
			continue
		}
		if errs[i] == nil {
			sawCall = true
			assert.Equal(t, clientPrincipal, m.Caller)
			assert.Equal(t, "acme", m.Tenant)
		}
	}
	assert.True(t, sawRefusal, "the refusal is audited")
	assert.True(t, sawCall, "the call is attributed to the certificate")
}

func TestMCPServiceHTTPExposureWithTLS(t *testing.T) {
	t.Run("plain TLS does not authenticate", func(t *testing.T) {
		p := newPKI(t, false)
		oe := serveErr(t, mcpserve.Config{InsecureNoPolicy: true},
			[]string{"mcp", "--mcp-addr", "0.0.0.0:0"}, p.with())
		assert.Equal(t, 2, oe.ExitCode)
		assert.Contains(t, oe.Error(), "services.mcp.insecure_remote")
	})
	t.Run("mtls authenticates", func(t *testing.T) {
		p := newPKI(t, true)
		r := newServeRoot(t, mcpserve.With(mcpserve.Config{InsecureNoPolicy: true}), p.with())
		mcpCommands(r)
		err := runServeArgs(t, r, []string{"serve", "mcp", "--mcp-addr", "0.0.0.0:0"}, 2*time.Second)
		assert.NoError(t, err)
	})
}
