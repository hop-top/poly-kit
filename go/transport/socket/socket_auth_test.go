package socket_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
	"hop.top/kit/go/transport/transportsvc"
)

// recordingRunner captures the invocation and the context it ran
// with, and can be told to block until that context is canceled.
type recordingRunner struct {
	mu     sync.Mutex
	got    cmdsurface.Invocation
	ctxErr error
	block  bool
	seen   chan struct{}
	done   chan struct{}
}

func newRecordingRunner(block bool) *recordingRunner {
	return &recordingRunner{block: block, seen: make(chan struct{}, 1), done: make(chan struct{}, 1)}
}

func (r *recordingRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	r.mu.Lock()
	r.got = inv
	r.mu.Unlock()
	select {
	case r.seen <- struct{}{}:
	default:
	}
	if r.block {
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
		r.mu.Lock()
		r.ctxErr = ctx.Err()
		r.mu.Unlock()
		select {
		case r.done <- struct{}{}:
		default:
		}
	}
	return cmdsurface.Result{Stdout: "ran"}, nil
}

func (r *recordingRunner) Stream(context.Context, cmdsurface.Invocation, chan<- cmdsurface.Event) error {
	return errors.New("not streamed")
}

func (r *recordingRunner) invocation() cmdsurface.Invocation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.got
}

// sinkRecorder captures audit records.
type sinkRecorder struct {
	mu   sync.Mutex
	invs []cmdsurface.Invocation
	errs []error
}

func (s *sinkRecorder) Emit(_ context.Context, inv cmdsurface.Invocation, _ cmdsurface.Result, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invs = append(s.invs, inv)
	s.errs = append(s.errs, err)
	return nil
}

func TestRequestProvenanceReachesMeta(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	runner := newRecordingRunner(false)
	startSocket(t, path, transportsvc.WithBridgeOptions(cmdsurface.WithRunner(runner)))

	resp := call(t, path, socket.Request{
		Path:           []string{"ping"},
		Caller:         "daemon",
		Tenant:         "acme",
		RequestID:      "req-7",
		TraceID:        "trace-7",
		IdempotencyKey: "key-7",
	})
	require.True(t, resp.Ok, "%+v", resp.Error)

	got := runner.invocation().Meta
	assert.Equal(t, "daemon", got.Caller, "without an authenticator the claim is recorded as provenance")
	assert.Equal(t, "acme", got.Tenant)
	assert.Equal(t, "req-7", got.RequestID)
	assert.Equal(t, "trace-7", got.TraceID)
	assert.Equal(t, "key-7", got.IdempotencyKey)
	assert.Equal(t, cmdsurface.SurfaceRPC, got.Surface, "the seam pins the surface")
	assert.False(t, got.RequestedAt.IsZero(), "the transport stamps the audit timestamp")
}

func TestRequestIDIsIssuedWhenAbsent(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	runner := newRecordingRunner(false)
	startSocket(t, path, transportsvc.WithBridgeOptions(cmdsurface.WithRunner(runner)))

	resp := call(t, path, socket.Request{Path: []string{"ping"}})
	require.True(t, resp.Ok)
	assert.NotEmpty(t, runner.invocation().Meta.RequestID,
		"every request must have an id in the audit trail, even from a one-line pipe")
}

func TestAuthenticatorReplacesClaimedIdentity(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	runner := newRecordingRunner(false)
	rec := &sinkRecorder{}

	var authConn net.Conn
	auth := func(_ context.Context, conn net.Conn, req socket.Request) (socket.Identity, error) {
		authConn = conn
		if req.Flags["token"] != "s3cret" {
			return socket.Identity{}, errors.New("bad token")
		}
		return socket.Identity{Principal: "alice", Tenant: "acme"}, nil
	}
	svc := startSocketWith(t, path, auth,
		transportsvc.WithBridgeOptions(
			cmdsurface.WithRunner(runner),
			cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: rec, OnError: true, OnOK: true}),
		))
	_ = svc

	// A claimed caller is replaced by the verified one.
	resp := call(t, path, socket.Request{
		Path: []string{"ping"}, Caller: "mallory", Tenant: "evil",
		Flags: map[string]any{"token": "s3cret"},
	})
	require.True(t, resp.Ok, "%+v", resp.Error)
	assert.NotNil(t, authConn, "the authenticator sees the connection, so it can read peer credentials")
	got := runner.invocation().Meta
	assert.Equal(t, "alice", got.Caller)
	assert.Equal(t, "acme", got.Tenant)

	// A refused caller never reaches the bridge, and the refusal is
	// audited with the request's provenance.
	resp = call(t, path, socket.Request{Path: []string{"ping"}, RequestID: "req-x", Flags: map[string]any{"token": "nope"}})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeUnauthenticated, resp.Error.Code)
	assert.Equal(t, "bad token", resp.Error.Message)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.invs, 2, "one execution record and one refusal record")
	assert.ErrorIs(t, rec.errs[1], cmdsurface.ErrAuthRefused)
	assert.Equal(t, "req-x", rec.invs[1].Meta.RequestID)
	assert.Equal(t, cmdsurface.SurfaceRPC, rec.invs[1].Meta.Surface)
	assert.Equal(t, []string{"ping"}, rec.invs[1].Path)
}

func TestPermissionDeniedIsDeniedOnTheWire(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	startSocket(t, path, transportsvc.WithBridgeOptions(cmdsurface.WithPermission(
		func(_ context.Context, meta cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
			if meta.Caller == "mallory" {
				return cmdsurface.PermissionDecision{Reason: "mallory may not " + leaf.PathKey()}
			}
			return cmdsurface.PermissionDecision{Allowed: true}
		},
	)))

	resp := call(t, path, socket.Request{Path: []string{"ping"}, Caller: "mallory"})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeDenied, resp.Error.Code)
	assert.Equal(t,
		"cmdsurface: permission denied: ping on rpc: mallory may not ping",
		resp.Error.Message)

	resp = call(t, path, socket.Request{Path: []string{"ping"}, Caller: "alice"})
	require.True(t, resp.Ok)
}

func TestClientDisconnectCancelsInvocation(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	runner := newRecordingRunner(true)
	startSocket(t, path, transportsvc.WithBridgeOptions(cmdsurface.WithRunner(runner)))

	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(conn).Encode(socket.Request{Path: []string{"ping"}}))

	select {
	case <-runner.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the runner never received the invocation")
	}
	// The command is still running; the client goes away.
	require.NoError(t, conn.Close())

	select {
	case <-runner.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the invocation context was not canceled after the client disconnected")
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	assert.ErrorIs(t, runner.ctxErr, context.Canceled,
		"the ctx handed to the Runner must be canceled by a client disconnect")
}

func TestPipelinedRequestsStillAnswerInOrder(t *testing.T) {
	t.Parallel()
	// Reading ahead for disconnect detection must not reorder
	// responses.
	path := socketPath(t)
	startSocket(t, path)

	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	enc := json.NewEncoder(conn)
	require.NoError(t, enc.Encode(socket.Request{Path: []string{"ping"}}))
	require.NoError(t, enc.Encode(socket.Request{Path: []string{"nosuch"}}))
	require.NoError(t, enc.Encode(socket.Request{Path: []string{"ping"}}))

	rd := bufio.NewReader(conn)
	var codes []string
	for range 3 {
		line, err := rd.ReadBytes('\n')
		require.NoError(t, err)
		var resp socket.Response
		require.NoError(t, json.Unmarshal(line, &resp))
		if resp.Ok {
			codes = append(codes, "ok")
		} else {
			codes = append(codes, resp.Error.Code)
		}
	}
	assert.Equal(t, []string{"ok", socket.CodeNotFound, "ok"}, codes)
}

// startSocketWith is startSocket with an authenticator installed on
// the transport.
func startSocketWith(t *testing.T, path string, auth socket.Authenticator, opts ...transportsvc.TransportOption) *transportsvc.TransportService {
	t.Helper()
	tr := socket.New(path)
	tr.Auth = auth
	opts = append([]transportsvc.TransportOption{transportsvc.Expose("*")}, opts...)
	svc := transportsvc.NewTransportService("socket", testRoot(), cmdsurface.SurfaceRPC, tr, opts...)
	// Route the transport's own refusals into the bridge's sinks, as
	// the cli wiring does.
	tr.OnRefused = func(ctx context.Context, inv cmdsurface.Invocation, err error) {
		if b := svc.Bridge(); b != nil {
			inv.Meta.Surface = cmdsurface.SurfaceRPC
			b.Audit(ctx, inv, cmdsurface.Result{}, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Start(ctx, func() { ready <- struct{}{} }) }()
	select {
	case <-ready:
	case err := <-errCh:
		cancel()
		t.Fatalf("socket service failed to start: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("socket service never reported ready")
	}
	t.Cleanup(func() {
		_ = svc.Stop(context.Background())
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("socket service did not return after stop")
		}
	})
	return svc
}
