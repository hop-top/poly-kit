package cli_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// mcpCommands mounts one leaf per class the mcp service treats
// differently.
func mcpCommands(r *cli.Root) {
	ping := &cobra.Command{
		Use:   "ping",
		Short: "Answer pong",
		RunE:  func(cmd *cobra.Command, _ []string) error { cmd.Print("pong"); return nil },
	}
	cli.SetSideEffect(ping, cli.SideEffectRead)
	nuke := &cobra.Command{
		Use:   "nuke",
		Short: "Destroy everything",
		RunE:  func(cmd *cobra.Command, _ []string) error { cmd.Print("destroyed"); return nil },
	}
	cli.SetSideEffect(nuke, cli.SideEffectDestructiveShared)
	secret := &cobra.Command{
		Use:         "secret",
		Short:       "Needs the caller's credentials",
		Annotations: map[string]string{"kit/auth-required": "true"},
		RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("unlocked"); return nil },
	}
	cli.SetSideEffect(secret, cli.SideEffectRead)
	deploy := &cobra.Command{
		Use:         "deploy",
		Short:       "Deploy, after a person approves",
		Annotations: map[string]string{"kit/requires-confirmation": "true"},
		RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("deployed"); return nil },
	}
	cli.SetSideEffect(deploy, cli.SideEffectWriteLocal)
	shell := &cobra.Command{
		Use:   "shell",
		Short: "Interactive shell",
		RunE:  func(cmd *cobra.Command, _ []string) error { cmd.Print("shell"); return nil },
	}
	cli.SetSideEffect(shell, cli.SideEffectInteractive)
	r.Cmd.AddCommand(ping, nuke, secret, deploy, shell)
}

// mcpRun is one background `test serve mcp ...` invocation.
type mcpRun struct {
	root  *cli.Root
	ready chan serve.EventPayload
	errCh chan error
	stop  context.CancelFunc
	err   *strings.Builder
}

// startMCP runs `serve <args>` on a root carrying the mcp service and
// the commands above, and waits for the mcp service's readiness.
func startMCP(t *testing.T, cfg cli.MCPConfig, args []string, opts ...func(*cli.Root)) (*mcpRun, string) {
	t.Helper()
	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	run := &mcpRun{
		ready: make(chan serve.EventPayload, 4),
		errCh: make(chan error, 1),
		err:   &strings.Builder{},
	}
	b.Subscribe("kit.serve.service.ready_reported", func(_ context.Context, e bus.Event) error {
		if p, ok := e.Payload.(serve.EventPayload); ok && p.Service == cli.MCPServiceName {
			run.ready <- p
		}
		return nil
	})
	all := append([]func(*cli.Root){cli.WithMCP(cfg), cli.WithServiceBus(b)}, opts...)
	run.root = newServeRoot(t, all...)
	mcpCommands(run.root)
	run.root.Cmd.SetErr(&lockedWriter{w: run.err})

	ctx, cancel := context.WithCancel(context.Background())
	run.stop = cancel
	run.root.SetArgs(append([]string{"serve"}, args...))
	go func() { run.errCh <- run.root.Execute(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-run.errCh:
		case <-time.After(10 * time.Second):
			t.Error("serve did not return after cancellation")
		}
	})

	select {
	case p := <-run.ready:
		return run, p.Address
	case err := <-run.errCh:
		run.errCh <- err
		t.Fatalf("serve returned before mcp was ready: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("mcp never reported ready")
	}
	return nil, ""
}

// lockedWriter serializes writes from the supervisor's goroutines.
type lockedWriter struct {
	mu sync.Mutex
	w  *strings.Builder
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// dialMCP connects the official SDK client to endpoint.
func dialMCP(t *testing.T, endpoint string, hdr http.Header, opts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	hc := &http.Client{}
	if hdr != nil {
		hc.Transport = headerRoundTripper{hdr: hdr}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "cli-test", Version: "0"}, opts)
	sess, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: hc,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

type headerRoundTripper struct{ hdr http.Header }

func (h headerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	for k, vs := range h.hdr {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	return http.DefaultTransport.RoundTrip(r)
}

func toolNames(t *testing.T, sess *mcp.ClientSession) []string {
	t.Helper()
	res, err := sess.ListTools(t.Context(), nil)
	require.NoError(t, err)
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func callTool(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err, name)
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestMCPServiceHTTPListsAndCallsTools(t *testing.T) {
	_, endpoint := startMCP(t, cli.MCPConfig{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"})
	require.True(t, strings.HasPrefix(endpoint, "http://127.0.0.1:"), endpoint)
	require.True(t, strings.HasSuffix(endpoint, "/mcp"), "readiness carries the endpoint URL: %s", endpoint)

	sess := dialMCP(t, endpoint, nil, nil)
	assert.Equal(t, "test", sess.InitializeResult().ServerInfo.Name, "server identity is the tool's")

	names := toolNames(t, sess)
	assert.Subset(t, names, []string{"ping", "secret", "deploy"})
	assert.NotContains(t, names, "nuke", "a destructive leaf the policy refuses is withheld")
	assert.NotContains(t, names, "shell", "an interactive leaf is withheld")
	assert.NotContains(t, names, "serve", "self-hosting commands are never tools")

	text, isErr := callTool(t, sess, "ping", nil)
	assert.False(t, isErr)
	assert.Equal(t, "pong", text)
}

func TestMCPServiceHTTPRefusesDestructiveByDefault(t *testing.T) {
	_, endpoint := startMCP(t, cli.MCPConfig{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"})
	sess := dialMCP(t, endpoint, nil, nil)

	// Withheld from the catalog, and a call naming it anyway is
	// refused before anything runs.
	_, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "nuke", Arguments: map[string]any{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nuke")
}

func TestMCPServiceHTTPDestructiveOnceNamedStillNeedsItsConfirm(t *testing.T) {
	_, endpoint := startMCP(t, cli.MCPConfig{
		Policy: cmdsurface.Policy{AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceMCP}},
	}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"})
	sess := dialMCP(t, endpoint, nil, nil)
	assert.Contains(t, toolNames(t, sess), "nuke")

	// The command's own gate: no terminal, so no confirm is a refusal.
	text, isErr := callTool(t, sess, "nuke", nil)
	assert.True(t, isErr)
	assert.Contains(t, text, "--confirm")
	assert.NotContains(t, text, "destroyed")

	text, isErr = callTool(t, sess, "nuke", map[string]any{"confirm": "yes"})
	assert.False(t, isErr, text)
	assert.Contains(t, text, "destroyed")
}

func TestMCPServiceHTTPAuthRequiredNeedsVerifiedAuth(t *testing.T) {
	_, endpoint := startMCP(t, cli.MCPConfig{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"})

	// A bare Authorization header is presence, not authentication.
	sess := dialMCP(t, endpoint, http.Header{"Authorization": {"Bearer made-up"}}, nil)
	text, isErr := callTool(t, sess, "secret", nil)
	assert.True(t, isErr)
	assert.Equal(t, "authentication required", text)

	// Nor can a client claim an identity through the service's own
	// call header: the middleware overwrites it.
	forged := dialMCP(t, endpoint, http.Header{
		"X-Kit-Mcp-Call": {`{"verified":true,"principal":"root"}`},
	}, nil)
	text, isErr = callTool(t, forged, "secret", nil)
	assert.True(t, isErr)
	assert.Equal(t, "authentication required", text)
}

// sinkFunc adapts a function to cmdsurface.Sink.
type sinkFunc func(context.Context, cmdsurface.Invocation, cmdsurface.Result, error) error

func (f sinkFunc) Emit(ctx context.Context, inv cmdsurface.Invocation, res cmdsurface.Result, err error) error {
	return f(ctx, inv, res, err)
}

// bearerAuth accepts one token and attributes it to alice.
func bearerAuth(r *http.Request) (any, error) {
	if r.Header.Get("Authorization") != "Bearer good" {
		return nil, errors.New("bad token")
	}
	return api.Claims{Subject: "alice", Tenant: "acme", Scopes: []string{"read"}}, nil
}

func TestMCPServiceHTTPAuthAttributesAndAudits(t *testing.T) {
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
	_, endpoint := startMCP(t, cli.MCPConfig{Auth: bearerAuth},
		[]string{"mcp", "--mcp-addr", "127.0.0.1:0"},
		cli.WithAuditSinks(cmdsurface.SinkSpec{Sink: sink, OnError: true, OnOK: true}))

	// Refused at the HTTP layer: the SDK never sees the request.
	client := mcp.NewClient(&mcp.Implementation{Name: "cli-test", Version: "0"}, nil)
	_, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	require.Error(t, err, "an unauthenticated client must not connect")

	sess := dialMCP(t, endpoint, http.Header{"Authorization": {"Bearer good"}}, nil)
	text, isErr := callTool(t, sess, "secret", nil)
	require.False(t, isErr, text)
	assert.Equal(t, "unlocked", text)

	mu.Lock()
	defer mu.Unlock()
	var sawRefusal, sawCall bool
	for i, m := range metas {
		assert.Equal(t, cmdsurface.SurfaceMCP, m.Surface)
		if errors.Is(errs[i], cmdsurface.ErrAuthRefused) {
			sawRefusal = true
			continue
		}
		if errs[i] == nil {
			sawCall = true
			assert.Equal(t, "alice", m.Caller)
			assert.Equal(t, "acme", m.Tenant)
			assert.NotEmpty(t, m.RequestID)
			assert.Equal(t, "http", m.Extra["mcp_transport"])
			assert.Equal(t, "read", m.Extra["scopes"])
			assert.Equal(t, "cli-test", m.Extra["mcp_client"])
		}
	}
	assert.True(t, sawRefusal, "the HTTP auth refusal is audited")
	assert.True(t, sawCall, "the call is audited with its verified caller")
}

func TestMCPServiceHTTPConfirmationByElicitation(t *testing.T) {
	_, endpoint := startMCP(t, cli.MCPConfig{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"})

	var asked atomic.Int32
	accepting := dialMCP(t, endpoint, nil, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			asked.Add(1)
			assert.Contains(t, req.Params.Message, `"deploy"`)
			return &mcp.ElicitResult{Action: "accept"}, nil
		},
	})
	text, isErr := callTool(t, accepting, "deploy", nil)
	assert.False(t, isErr, text)
	assert.Equal(t, "deployed", text)
	assert.Equal(t, int32(1), asked.Load(), "one question per call")

	declining := dialMCP(t, endpoint, nil, &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: "decline"}, nil
		},
	})
	text, isErr = callTool(t, declining, "deploy", nil)
	assert.True(t, isErr)
	assert.Equal(t, "confirmation declined", text)

	// No elicitation and no header: a clear refusal, never a silent yes.
	plain := dialMCP(t, endpoint, nil, nil)
	text, isErr = callTool(t, plain, "deploy", nil)
	assert.True(t, isErr)
	assert.Contains(t, text, "confirmation required")
	assert.Contains(t, text, "X-Confirm-Token")

	// The HTTP header remains the other per-request act.
	headed := dialMCP(t, endpoint, http.Header{"X-Confirm-Token": {"yes"}}, nil)
	text, isErr = callTool(t, headed, "deploy", nil)
	assert.False(t, isErr, text)
	assert.Equal(t, "deployed", text)
}

func TestMCPServicePermissionGateWithholdsAndRefuses(t *testing.T) {
	deny := func(_ context.Context, meta cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		switch leaf.PathKey() {
		case "ping":
			return cmdsurface.PermissionDecision{Reason: "nobody pings", CallerIndependent: true}
		case "deploy":
			if meta.Caller == "" {
				return cmdsurface.PermissionDecision{Reason: "anonymous deploys refused"}
			}
		}
		return cmdsurface.PermissionDecision{Allowed: true}
	}
	_, endpoint := startMCP(t, cli.MCPConfig{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
		cli.WithPermission(deny))
	sess := dialMCP(t, endpoint, http.Header{"X-Confirm-Token": {"yes"}}, nil)

	names := toolNames(t, sess)
	assert.NotContains(t, names, "ping", "refused for every caller: withheld")
	assert.Contains(t, names, "deploy", "a caller-specific refusal cannot be known before the call")

	text, isErr := callTool(t, sess, "deploy", nil)
	assert.True(t, isErr)
	assert.Contains(t, text, "anonymous deploys refused")
}

func TestMCPServiceExposeNarrowsAndHideCarves(t *testing.T) {
	_, endpoint := startMCP(t, cli.MCPConfig{Expose: []string{"ping", "secret"}, Hide: []string{"secret"}},
		[]string{"mcp", "--mcp-addr", "127.0.0.1:0"})
	sess := dialMCP(t, endpoint, nil, nil)
	assert.Equal(t, []string{"ping"}, toolNames(t, sess))
}

func TestMCPServiceRunsOnTheRootFactory(t *testing.T) {
	var builds atomic.Int32
	var factory func() *cli.Root
	factory = func() *cli.Root {
		builds.Add(1)
		r := newServeRoot(t, cli.WithMCP(cli.MCPConfig{}), cli.WithRootFactory(factory))
		mcpCommands(r)
		return r
	}
	_, endpoint := startMCP(t, cli.MCPConfig{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
		cli.WithRootFactory(factory))
	sess := dialMCP(t, endpoint, nil, nil)

	before := builds.Load()
	text, isErr := callTool(t, sess, "ping", nil)
	require.False(t, isErr, text)
	assert.Greater(t, builds.Load(), before, "a served call runs on a tree the factory built")
}

// serveErr runs `serve <args>` on a root with the mcp service and
// returns the refusal it ends with.
func serveErr(t *testing.T, cfg cli.MCPConfig, args []string, opts ...func(*cli.Root)) *output.Error {
	t.Helper()
	r := newServeRoot(t, append([]func(*cli.Root){cli.WithMCP(cfg)}, opts...)...)
	mcpCommands(r)
	err := runServeArgs(t, r, append([]string{"serve"}, args...), 5*time.Second)
	require.Error(t, err)
	var oe *output.Error
	require.ErrorAs(t, err, &oe, err.Error())
	return oe
}

func TestMCPServiceHTTPExposureRefusals(t *testing.T) {
	t.Run("unauthenticated remote", func(t *testing.T) {
		oe := serveErr(t, cli.MCPConfig{}, []string{"mcp", "--mcp-addr", "0.0.0.0:0"})
		assert.Equal(t, 2, oe.ExitCode)
		for _, want := range []string{"MCPConfig.Auth", "127.0.0.1", "services.mcp.insecure_remote"} {
			assert.Contains(t, oe.Error(), want)
		}
	})
	t.Run("authenticated but unbounded", func(t *testing.T) {
		oe := serveErr(t, cli.MCPConfig{Auth: bearerAuth}, []string{"mcp", "--mcp-addr", "0.0.0.0:0"})
		assert.Equal(t, 2, oe.ExitCode)
		for _, want := range []string{"--policy", "127.0.0.1", "services.mcp.insecure_no_policy"} {
			assert.Contains(t, oe.Error(), want)
		}
	})
	t.Run("bad address", func(t *testing.T) {
		oe := serveErr(t, cli.MCPConfig{}, []string{"mcp", "--mcp-addr", "nope"})
		assert.Equal(t, 2, oe.ExitCode)
		assert.Contains(t, oe.Error(), "addr")
	})
	t.Run("bad path", func(t *testing.T) {
		oe := serveErr(t, cli.MCPConfig{Path: "mcp"}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"})
		assert.Equal(t, 2, oe.ExitCode)
		assert.Contains(t, oe.Error(), "path")
	})
	t.Run("unknown transport", func(t *testing.T) {
		oe := serveErr(t, cli.MCPConfig{Transport: "carrier-pigeon"}, []string{"mcp"})
		assert.Equal(t, 2, oe.ExitCode)
		assert.Contains(t, oe.Error(), "carrier-pigeon")
	})
}

func TestMCPServiceInsecureOptInsAreConfigKeys(t *testing.T) {
	r := newServeRoot(t, cli.WithMCP(cli.MCPConfig{}))
	mcpCommands(r)
	r.Viper.Set("services.mcp.insecure_remote", true)
	r.Viper.Set("services.mcp.insecure_no_policy", true)
	// Past validation, the run binds; cancellation is a clean stop.
	err := runServeArgs(t, r, []string{"serve", "mcp", "--mcp-addr", "0.0.0.0:0"}, 2*time.Second)
	assert.NoError(t, err)
}

func TestMCPServiceIsListedAndDisabledByDefault(t *testing.T) {
	r := newServeRoot(t, cli.WithMCP(cli.MCPConfig{}))
	var out strings.Builder
	r.Cmd.SetOut(&out)
	r.SetArgs([]string{"serve", "--list"})
	require.NoError(t, r.Execute(context.Background()))
	assert.Regexp(t, `(?m)^mcp\s+false\s+false`, out.String())

	// The supervisor form with nothing enabled is a usage error, not
	// a server nobody asked for.
	r2 := newServeRoot(t, cli.WithMCP(cli.MCPConfig{}))
	err := runServeArgs(t, r2, []string{"serve"}, 2*time.Second)
	var oe *output.Error
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, 2, oe.ExitCode)
}
