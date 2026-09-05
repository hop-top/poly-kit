package transportsvc_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/transportsvc"
)

// stubRunner is a Runner whose Run records the invocation it saw.
type stubRunner struct {
	got cmdsurface.Invocation
}

func (s *stubRunner) Run(_ context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	s.got = inv
	return cmdsurface.Result{Stdout: "stubbed"}, nil
}

func (s *stubRunner) Stream(context.Context, cmdsurface.Invocation, chan<- cmdsurface.Event) error {
	return errors.New("not streamed")
}

// startAndWait starts svc and blocks until it reports ready.
func startAndWait(t *testing.T, svc *transportsvc.TransportService) chan error {
	t.Helper()
	ready, errCh := startService(t, svc)
	select {
	case <-ready:
	case err := <-errCh:
		t.Fatalf("service failed to start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("service never reported ready")
	}
	return errCh
}

func TestWithBridgeOptionsFuncResolvesAtStart(t *testing.T) {
	t.Parallel()
	// The function must not run at construction: the state it reads
	// — a parsed flag, an option registered after the service — does
	// not exist yet. It runs once, at Start, and its options apply.
	calls := 0
	runner := &stubRunner{}
	tr := newFakeTransport()
	svc := transportsvc.NewTransportService(
		"tcp", testRoot(), cmdsurface.SurfaceRPC, tr,
		transportsvc.Expose("*"),
		transportsvc.WithBridgeOptionsFunc(func() []cmdsurface.Option {
			calls++
			return []cmdsurface.Option{cmdsurface.WithRunner(runner)}
		}),
	)
	require.Zero(t, calls, "deferred options must not resolve at construction")

	errCh := startAndWait(t, svc)
	assert.Equal(t, 1, calls, "deferred options resolve exactly once per start")

	res, err := tr.invoke(t)(context.Background(), cmdsurface.Invocation{Path: []string{"ping"}})
	require.NoError(t, err)
	assert.Equal(t, "stubbed", res.Stdout,
		"the Runner installed through the deferred options must be the one used")

	require.NoError(t, svc.Stop(context.Background()))
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Stop")
	}
}

func TestWithBridgeOptionsFuncTakesPrecedenceOverStaticOptions(t *testing.T) {
	t.Parallel()
	// Deferred options are applied after WithBridgeOptions, so a
	// later, better-informed value wins; a nil func is tolerated.
	static := &stubRunner{}
	deferred := &stubRunner{}
	tr := newFakeTransport()
	svc := transportsvc.NewTransportService(
		"tcp", testRoot(), cmdsurface.SurfaceRPC, tr,
		transportsvc.Expose("*"),
		transportsvc.WithBridgeOptions(cmdsurface.WithRunner(static)),
		transportsvc.WithBridgeOptionsFunc(func() []cmdsurface.Option {
			return []cmdsurface.Option{cmdsurface.WithRunner(deferred)}
		}),
		transportsvc.WithBridgeOptionsFunc(nil),
	)
	errCh := startAndWait(t, svc)

	_, err := tr.invoke(t)(context.Background(), cmdsurface.Invocation{Path: []string{"ping"}})
	require.NoError(t, err)
	assert.Equal(t, "ping", strings.Join(deferred.got.Path, " "),
		"the deferred runner must be the active one")
	assert.Empty(t, static.got.Path, "the static runner must have been replaced")

	require.NoError(t, svc.Stop(context.Background()))
	<-errCh
}

func TestInvokerMapsPermissionDenied(t *testing.T) {
	t.Parallel()
	// A permission gate wired through the bridge is reached through
	// the seam's Invoker, and its refusal comes back as the bridge's
	// own sentinel for the transport to map.
	tr := newFakeTransport()
	svc := transportsvc.NewTransportService(
		"tcp", testRoot(), cmdsurface.SurfaceRPC, tr,
		transportsvc.Expose("*"),
		transportsvc.WithBridgeOptions(cmdsurface.WithPermission(
			func(_ context.Context, meta cmdsurface.Meta, _ *cmdsurface.Leaf) cmdsurface.PermissionDecision {
				if meta.Caller == "mallory" {
					return cmdsurface.PermissionDecision{Reason: "not entitled"}
				}
				return cmdsurface.PermissionDecision{Allowed: true}
			},
		)),
	)
	errCh := startAndWait(t, svc)
	inv := tr.invoke(t)

	_, err := inv(context.Background(), cmdsurface.Invocation{
		Path: []string{"ping"}, Meta: cmdsurface.Meta{Caller: "mallory"},
	})
	assert.ErrorIs(t, err, cmdsurface.ErrPermissionDenied)
	assert.Contains(t, err.Error(), "not entitled")

	_, err = inv(context.Background(), cmdsurface.Invocation{
		Path: []string{"ping"}, Meta: cmdsurface.Meta{Caller: "alice"},
	})
	assert.NoError(t, err)

	require.NoError(t, svc.Stop(context.Background()))
	<-errCh
}
