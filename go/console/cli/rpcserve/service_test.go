package rpcserve_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/rpcserve"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1/cmdsurfacev1connect"
	"hop.top/kit/go/transport/rpc"
)

// rpcCommands mounts one leaf per class the rpc service treats
// differently, plus two streaming ones.
func rpcCommands(r *cli.Root) {
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
	tick := &cobra.Command{
		Use:   "tick",
		Short: "Print a line every interval",
		RunE: func(cmd *cobra.Command, _ []string) error {
			n, _ := cmd.Flags().GetInt("count")
			every, _ := cmd.Flags().GetDuration("every")
			for i := range n {
				if i > 0 {
					select {
					case <-cmd.Context().Done():
						return cmd.Context().Err()
					case <-time.After(every):
					}
				}
				cmd.Printf("tick %d\n", i)
			}
			return nil
		},
	}
	tick.Flags().Int("count", 3, "lines to print")
	tick.Flags().Duration("every", 10*time.Millisecond, "interval between lines")
	cli.SetSideEffect(tick, cli.SideEffectRead)
	forever := &cobra.Command{
		Use:   "forever",
		Short: "Print one line, then wait to be canceled",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("started")
			<-cmd.Context().Done()
			return cmd.Context().Err()
		},
	}
	cli.SetSideEffect(forever, cli.SideEffectRead)
	r.Cmd.AddCommand(ping, nuke, secret, deploy, shell, tick, forever)
}

// rpcRun is one background `test serve rpc ...` invocation.
type rpcRun struct {
	root  *cli.Root
	errCh chan error
	stop  context.CancelFunc
}

// startRPC runs `serve <args>` on a root carrying the rpc service
// (added by with) and the commands above, and waits for the rpc
// service's readiness. It returns the base URL readiness reported.
func startRPC(t *testing.T, with func(*cli.Root), args []string, opts ...func(*cli.Root)) (*rpcRun, string) {
	t.Helper()
	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	ready := make(chan serve.EventPayload, 4)
	b.Subscribe("kit.serve.service.ready_reported", func(_ context.Context, e bus.Event) error {
		if p, ok := e.Payload.(serve.EventPayload); ok && p.Service == rpcserve.ServiceName {
			ready <- p
		}
		return nil
	})
	run := &rpcRun{errCh: make(chan error, 1)}
	all := append([]func(*cli.Root){with, cli.WithServiceBus(b)}, opts...)
	run.root = newServeRoot(t, all...)
	rpcCommands(run.root)

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
	case p := <-ready:
		return run, p.Address
	case err := <-run.errCh:
		run.errCh <- err
		t.Fatalf("serve returned before rpc was ready: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("rpc never reported ready")
	}
	return nil, ""
}

// startDefault starts the service with cfg on an ephemeral loopback port.
func startDefault(t *testing.T, cfg rpcserve.Config, opts ...func(*cli.Root)) string {
	t.Helper()
	_, base := startRPC(t, rpcserve.With(cfg), []string{"rpc", "--rpc-addr", "127.0.0.1:0"}, opts...)
	return base
}

// protocol is one wire protocol a client may speak.
type protocol struct {
	name string
	opts []connect.ClientOption
}

var protocols = []protocol{
	{name: "connect", opts: nil},
	{name: "grpc", opts: []connect.ClientOption{connect.WithGRPC()}},
	{name: "grpc-web", opts: []connect.ClientOption{connect.WithGRPCWeb()}},
}

// h2cClient speaks HTTP/1.1 and unencrypted HTTP/2 with prior
// knowledge, so every protocol — native gRPC included — reaches a
// plaintext listener.
func h2cClient() *http.Client {
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	return &http.Client{Transport: &http.Transport{Protocols: p}}
}

func client(base string, p protocol) cmdsurfacev1connect.CommandsClient {
	return cmdsurfacev1connect.NewCommandsClient(h2cClient(), base, p.opts...)
}

func call(path string, hdr http.Header) *connect.Request[cmdsurfacev1.Invocation] {
	req := connect.NewRequest(&cmdsurfacev1.Invocation{Path: strings.Fields(path)})
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header().Add(k, v)
		}
	}
	return req
}

// streamAll runs an InvokeStream call and returns the lines it carried
// and the terminal result.
func streamAll(ctx context.Context, c cmdsurfacev1connect.CommandsClient, req *connect.Request[cmdsurfacev1.Invocation]) ([]string, *cmdsurfacev1.Result, error) {
	stream, err := c.InvokeStream(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = stream.Close() }()
	var (
		lines []string
		res   *cmdsurfacev1.Result
	)
	for stream.Receive() {
		ev := stream.Msg()
		switch ev.GetKind() {
		case "stdout":
			lines = append(lines, ev.GetData().GetStringValue())
		case "done":
			res = ev.GetResult()
		}
	}
	return lines, res, stream.Err()
}

func TestRPCServiceServesEveryProtocol(t *testing.T) {
	base := startDefault(t, rpcserve.Config{})
	require.True(t, strings.HasPrefix(base, "http://127.0.0.1:"), "readiness carries the base URL: %s", base)

	for _, p := range protocols {
		t.Run(p.name, func(t *testing.T) {
			c := client(base, p)
			resp, err := c.Invoke(t.Context(), call("ping", nil))
			require.NoError(t, err)
			assert.Equal(t, "pong", resp.Msg.GetStdout())
			assert.Equal(t, int32(0), resp.Msg.GetExitCode())

			lines, res, err := streamAll(t.Context(), c, call("tick", nil))
			require.NoError(t, err)
			assert.Equal(t, []string{"tick 0", "tick 1", "tick 2"}, lines)
			require.NotNil(t, res)
			assert.Equal(t, int32(0), res.GetExitCode())
		})
	}
}

func TestRPCServiceWithholdsWhatNeverRunsRemotely(t *testing.T) {
	base := startDefault(t, rpcserve.Config{}, cli.WithStatus(cli.StatusConfig{}))
	c := client(base, protocols[0])
	for _, path := range []string{"shell", "status", "serve", "nope"} {
		_, err := c.Invoke(t.Context(), call(path, nil))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), path)
	}
}

func TestRPCServiceRefusesDestructiveUntilThePolicyNamesRPC(t *testing.T) {
	base := startDefault(t, rpcserve.Config{})
	_, err := client(base, protocols[0]).Invoke(t.Context(), call("nuke", nil))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	assert.ErrorContains(t, err, "destructive")

	// Naming another surface lifts nothing here.
	base = startDefault(t, rpcserve.Config{
		Policy: cmdsurface.Policy{AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceREST}},
	})
	_, err = client(base, protocols[0]).Invoke(t.Context(), call("nuke", nil))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	base = startDefault(t, rpcserve.Config{
		Policy: cmdsurface.Policy{AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceRPC}},
	})
	c := client(base, protocols[0])
	// Permitted, the command's own confirmation still applies.
	resp, err := c.Invoke(t.Context(), call("nuke", nil))
	require.NoError(t, err)
	assert.NotEqual(t, int32(0), resp.Msg.GetExitCode())
	assert.NotContains(t, resp.Msg.GetStdout(), "destroyed")

	flags, err := structpb.NewStruct(map[string]any{"confirm": "yes"})
	require.NoError(t, err)
	req := connect.NewRequest(&cmdsurfacev1.Invocation{Path: []string{"nuke"}, Flags: flags})
	resp, err = c.Invoke(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, "destroyed", resp.Msg.GetStdout(), resp.Msg.GetStderr())
}

func TestRPCServiceConfirmationNeedsTheHeader(t *testing.T) {
	base := startDefault(t, rpcserve.Config{})
	c := client(base, protocols[0])
	_, err := c.Invoke(t.Context(), call("deploy", nil))
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

	resp, err := c.Invoke(t.Context(), call("deploy", http.Header{"X-Confirm-Token": {"yes"}}))
	require.NoError(t, err)
	assert.Equal(t, "deployed", resp.Msg.GetStdout())
}

func TestRPCServiceAuthRequiredNeedsVerifiedAuth(t *testing.T) {
	base := startDefault(t, rpcserve.Config{})
	c := client(base, protocols[0])

	// A bare Authorization header is presence, not authentication.
	_, err := c.Invoke(t.Context(), call("secret", http.Header{"Authorization": {"Bearer made-up"}}))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	// Nor is a caller named in the body.
	req := call("secret", nil)
	req.Msg.Meta = &cmdsurfacev1.Meta{Caller: "root"}
	_, err = c.Invoke(t.Context(), req)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

// sinkFunc adapts a function to cmdsurface.Sink.
type sinkFunc func(context.Context, cmdsurface.Invocation, cmdsurface.Result, error) error

func (f sinkFunc) Emit(ctx context.Context, inv cmdsurface.Invocation, res cmdsurface.Result, err error) error {
	return f(ctx, inv, res, err)
}

// recorder collects what the audit sinks receive.
type recorder struct {
	mu   sync.Mutex
	invs []cmdsurface.Invocation
	errs []error
}

func (r *recorder) spec() cmdsurface.SinkSpec {
	return cmdsurface.SinkSpec{OnError: true, OnOK: true, Sink: sinkFunc(
		func(_ context.Context, inv cmdsurface.Invocation, _ cmdsurface.Result, err error) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.invs = append(r.invs, inv)
			r.errs = append(r.errs, err)
			return nil
		})}
}

// bearerAuth accepts one token and attributes it to alice.
func bearerAuth(r *http.Request) (any, error) {
	if r.Header.Get("Authorization") != "Bearer good" {
		return nil, errors.New("bad token")
	}
	return api.Claims{Subject: "alice", Tenant: "acme", Scopes: []string{"read"}}, nil
}

func TestRPCServiceAuthGatesUnaryAndStreamingCalls(t *testing.T) {
	rec := &recorder{}
	base := startDefault(t, rpcserve.Config{Auth: bearerAuth}, cli.WithAuditSinks(rec.spec()))

	for _, p := range protocols {
		t.Run(p.name, func(t *testing.T) {
			c := client(base, p)
			_, err := c.Invoke(t.Context(), call("ping", nil))
			assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "unary without a credential")

			_, _, err = streamAll(t.Context(), c, call("tick", nil))
			assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "stream without a credential")

			good := http.Header{"Authorization": {"Bearer good"}}
			resp, err := c.Invoke(t.Context(), call("secret", good))
			require.NoError(t, err)
			assert.Equal(t, "unlocked", resp.Msg.GetStdout())

			lines, _, err := streamAll(t.Context(), c, call("tick", good))
			require.NoError(t, err)
			assert.Len(t, lines, 3)
		})
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	var refusals, runs int
	for i, inv := range rec.invs {
		assert.Equal(t, cmdsurface.SurfaceRPC, inv.Meta.Surface)
		if errors.Is(rec.errs[i], cmdsurface.ErrAuthRefused) {
			refusals++
			assert.NotEmpty(t, inv.Meta.Extra["rpc_procedure"])
			continue
		}
		if rec.errs[i] == nil {
			runs++
			assert.Equal(t, "alice", inv.Meta.Caller)
			assert.Equal(t, "acme", inv.Meta.Tenant)
			assert.Equal(t, "read", inv.Meta.Extra["scopes"])
			assert.NotEmpty(t, inv.Meta.RequestID)
			assert.NotEmpty(t, inv.Meta.Extra["rpc_protocol"])
			assert.NotEmpty(t, inv.Meta.Extra["remote_addr"])
		}
	}
	assert.Equal(t, 2*len(protocols), refusals, "every refused call is audited, streams included")
	assert.Equal(t, 2*len(protocols), runs, "every run is audited with its verified caller")
}

// TestRPCServiceIgnoresClaimedIdentity pins that the body's caller,
// tenant and extra map never reach the gates: identity is what Auth
// verified.
func TestRPCServiceIgnoresClaimedIdentity(t *testing.T) {
	var seen atomic.Value
	perm := func(_ context.Context, meta cmdsurface.Meta, _ *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		seen.Store(meta)
		return cmdsurface.PermissionDecision{Allowed: true}
	}
	for _, withAuth := range []bool{false, true} {
		cfg := rpcserve.Config{}
		hdr := http.Header{}
		if withAuth {
			cfg.Auth = bearerAuth
			hdr.Set("Authorization", "Bearer good")
		}
		base := startDefault(t, cfg, cli.WithPermission(perm))
		req := call("ping", hdr)
		req.Msg.Meta = &cmdsurfacev1.Meta{
			Caller: "root", Tenant: "evil", RequestId: "body-id",
			Extra: map[string]string{"scopes": "admin"},
		}
		_, err := client(base, protocols[0]).Invoke(t.Context(), req)
		require.NoError(t, err)

		meta := seen.Load().(cmdsurface.Meta)
		if withAuth {
			assert.Equal(t, "alice", meta.Caller)
			assert.Equal(t, "acme", meta.Tenant)
			assert.Equal(t, "read", meta.Extra["scopes"])
		} else {
			assert.Empty(t, meta.Caller)
			assert.Empty(t, meta.Tenant)
			assert.NotContains(t, meta.Extra, "scopes")
		}
		assert.Equal(t, "body-id", meta.RequestID, "a body request id is kept when no header names one")
	}
}

func TestRPCServicePermissionGateRefuses(t *testing.T) {
	deny := func(_ context.Context, meta cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		if leaf.PathKey() == "ping" && meta.Caller == "" {
			return cmdsurface.PermissionDecision{Reason: "anonymous pings refused"}
		}
		return cmdsurface.PermissionDecision{Allowed: true}
	}
	base := startDefault(t, rpcserve.Config{}, cli.WithPermission(deny))
	c := client(base, protocols[0])
	_, err := c.Invoke(t.Context(), call("ping", nil))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	assert.ErrorContains(t, err, "anonymous pings refused")

	_, _, err = streamAll(t.Context(), c, call("ping", nil))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "a stream is admitted by the same gate")
}

func TestRPCServiceExposeNarrowsAndHideCarves(t *testing.T) {
	base := startDefault(t, rpcserve.Config{Expose: []string{"ping", "tick"}, Hide: []string{"tick"}})
	c := client(base, protocols[0])
	_, err := c.Invoke(t.Context(), call("ping", nil))
	require.NoError(t, err)
	for _, path := range []string{"tick", "deploy"} {
		_, err = c.Invoke(t.Context(), call(path, nil))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), path)
	}
}

func TestRPCServiceBoundsTheMessage(t *testing.T) {
	base := startDefault(t, rpcserve.Config{MaxBodyBytes: 1024})
	req := call("ping", nil)
	req.Msg.Args = []string{strings.Repeat("x", 4096)}
	_, err := client(base, protocols[0]).Invoke(t.Context(), req)
	assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
}

func TestRPCServiceRunsOnTheRootFactory(t *testing.T) {
	var builds atomic.Int32
	var factory func() *cli.Root
	factory = func() *cli.Root {
		builds.Add(1)
		r := newServeRoot(t, rpcserve.With(rpcserve.Config{}), cli.WithRootFactory(factory))
		rpcCommands(r)
		return r
	}
	base := startDefault(t, rpcserve.Config{}, cli.WithRootFactory(factory))
	before := builds.Load()
	_, err := client(base, protocols[0]).Invoke(t.Context(), call("ping", nil))
	require.NoError(t, err)
	assert.Greater(t, builds.Load(), before, "a served call runs on a tree the factory built")
}

// TestRPCServiceStreamOutlivesTheWriteTimeout pins that InvokeStream is
// exempt from the server's write timeout, which is sized for
// request/reply: a stream running past it keeps delivering and ends
// with its result, over HTTP/1.1 and h2c alike. A unary call keeps the
// timeout.
func TestRPCServiceStreamOutlivesTheWriteTimeout(t *testing.T) {
	const timeout = 300 * time.Millisecond
	_, base := startRPC(t,
		rpcserve.WithServerOptions(rpcserve.Config{}, rpc.WithWriteTimeout(timeout)),
		[]string{"rpc", "--rpc-addr", "127.0.0.1:0"})

	flags, err := structpb.NewStruct(map[string]any{"count": 6, "every": "150ms"})
	require.NoError(t, err)
	for _, p := range protocols {
		t.Run(p.name, func(t *testing.T) {
			req := connect.NewRequest(&cmdsurfacev1.Invocation{Path: []string{"tick"}, Flags: flags})
			start := time.Now()
			lines, res, err := streamAll(t.Context(), client(base, p), req)
			require.NoError(t, err)
			assert.Greater(t, time.Since(start), 2*timeout, "the stream ran past the write timeout")
			assert.Len(t, lines, 6)
			require.NotNil(t, res, "the terminal result arrived")
			assert.Equal(t, int32(0), res.GetExitCode())
		})
	}
}

// TestRPCServiceStopEndsOpenStreams pins that stopping the service
// ends a stream with no end of its own rather than waiting out the
// stop budget.
func TestRPCServiceStopEndsOpenStreams(t *testing.T) {
	run, base := startRPC(t, rpcserve.With(rpcserve.Config{}), []string{"rpc", "--rpc-addr", "127.0.0.1:0"})

	stream, err := client(base, protocols[1]).InvokeStream(t.Context(), call("forever", nil))
	require.NoError(t, err)
	defer func() { _ = stream.Close() }()
	require.True(t, stream.Receive(), "the stream started: %v", stream.Err())
	assert.Equal(t, "started", stream.Msg().GetData().GetStringValue())

	start := time.Now()
	run.stop()
	select {
	case err := <-run.errCh:
		run.errCh <- err
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return with a stream open")
	}
	assert.Less(t, time.Since(start), 5*time.Second)
	for stream.Receive() {
	}
	assert.Error(t, stream.Err(), "the open stream ended")
}

// serveErr runs `serve <args>` on a root with the rpc service and
// returns the refusal it ends with.
func serveErr(t *testing.T, cfg rpcserve.Config, args []string, opts ...func(*cli.Root)) *output.Error {
	t.Helper()
	r := newServeRoot(t, append([]func(*cli.Root){rpcserve.With(cfg)}, opts...)...)
	rpcCommands(r)
	err := runServeArgs(t, r, append([]string{"serve"}, args...), 5*time.Second)
	require.Error(t, err)
	var oe *output.Error
	require.ErrorAs(t, err, &oe, err.Error())
	return oe
}

func TestRPCServiceExposureRefusals(t *testing.T) {
	t.Run("unauthenticated remote", func(t *testing.T) {
		oe := serveErr(t, rpcserve.Config{}, []string{"rpc", "--rpc-addr", "0.0.0.0:0"})
		assert.Equal(t, 2, oe.ExitCode)
		for _, want := range []string{"rpcserve.Config.Auth", "127.0.0.1", "services.rpc.insecure_remote"} {
			assert.Contains(t, oe.Error(), want)
		}
	})
	t.Run("authenticated but unbounded", func(t *testing.T) {
		oe := serveErr(t, rpcserve.Config{Auth: bearerAuth}, []string{"rpc", "--rpc-addr", "0.0.0.0:0"})
		assert.Equal(t, 2, oe.ExitCode)
		for _, want := range []string{"--policy", "127.0.0.1", "services.rpc.insecure_no_policy"} {
			assert.Contains(t, oe.Error(), want)
		}
	})
	t.Run("bad address", func(t *testing.T) {
		oe := serveErr(t, rpcserve.Config{}, []string{"rpc", "--rpc-addr", "nope"})
		assert.Equal(t, 2, oe.ExitCode)
		assert.Contains(t, oe.Error(), "addr")
	})
	t.Run("configured address", func(t *testing.T) {
		r := newServeRoot(t, rpcserve.With(rpcserve.Config{}))
		rpcCommands(r)
		r.Viper.Set("services.rpc.addr", "0.0.0.0:0")
		err := runServeArgs(t, r, []string{"serve", "rpc"}, 5*time.Second)
		var oe *output.Error
		require.ErrorAs(t, err, &oe)
		assert.Equal(t, 2, oe.ExitCode)
		assert.Contains(t, oe.Error(), "services.rpc.insecure_remote")
	})
}

func TestRPCServiceInsecureOptInsAreConfigKeys(t *testing.T) {
	r := newServeRoot(t, rpcserve.With(rpcserve.Config{}))
	rpcCommands(r)
	r.Viper.Set("services.rpc.insecure_remote", true)
	r.Viper.Set("services.rpc.insecure_no_policy", true)
	// Past validation, the run binds; cancellation is a clean stop.
	err := runServeArgs(t, r, []string{"serve", "rpc", "--rpc-addr", "0.0.0.0:0"}, 2*time.Second)
	assert.NoError(t, err)
}

func TestRPCServiceIsListedAndDisabledByDefault(t *testing.T) {
	r := newServeRoot(t, rpcserve.With(rpcserve.Config{}))
	var out strings.Builder
	r.Cmd.SetOut(&out)
	r.SetArgs([]string{"serve", "--list"})
	require.NoError(t, r.Execute(context.Background()))
	assert.Regexp(t, `(?m)^rpc\s+false\s+false`, out.String())

	// The supervisor form with nothing enabled is a usage error, not
	// a server nobody asked for.
	r2 := newServeRoot(t, rpcserve.With(rpcserve.Config{}))
	err := runServeArgs(t, r2, []string{"serve"}, 2*time.Second)
	var oe *output.Error
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, 2, oe.ExitCode)
}

// newServeRoot builds a root carrying opts, with the validator off so
// test trees need no annotations beyond what they exercise.
func newServeRoot(t *testing.T, opts ...func(*cli.Root)) *cli.Root {
	t.Helper()
	return cli.New(cli.Config{Name: "test", Version: "0.1.0", DisableValidate: true}, opts...)
}

// runServeArgs executes the root with args and returns the error,
// canceling after settle so a run that started comes back.
func runServeArgs(t *testing.T, r *cli.Root, args []string, settle time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.SetArgs(args)
	errCh := make(chan error, 1)
	go func() { errCh <- r.Execute(ctx) }()
	select {
	case err := <-errCh:
		return err
	case <-time.After(settle):
		cancel()
	}
	select {
	case err := <-errCh:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after cancellation")
		return nil
	}
}
