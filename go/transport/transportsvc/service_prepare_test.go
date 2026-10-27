package transportsvc_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/transportsvc"
)

// preparingTransport adds the optional Prepare step to fakeTransport
// and records the order of the seam's calls into it.
type preparingTransport struct {
	*fakeTransport
	prepareErr error

	mu       sync.Mutex
	calls    []string
	prepared *cmdsurface.Bridge
}

func (p *preparingTransport) Prepare(_ context.Context, b *cmdsurface.Bridge) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, "prepare")
	p.prepared = b
	return p.prepareErr
}

func (p *preparingTransport) Bind(ctx context.Context) (string, error) {
	p.mu.Lock()
	p.calls = append(p.calls, "bind")
	p.mu.Unlock()
	return p.fakeTransport.Bind(ctx)
}

func (p *preparingTransport) seen() ([]string, *cmdsurface.Bridge) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...), p.prepared
}

func awaitReady(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("service never reported ready")
	}
}

// Prepare builds the bridge — the tree reflected, the bridge options
// resolved — and hands it to the transport's Prepare; Start serves
// that bridge without reflecting again or resolving the options again.
func TestServiceStartServesWhatPrepareBuilt(t *testing.T) {
	t.Parallel()
	tr := &preparingTransport{fakeTransport: newFakeTransport()}
	var optCalls int
	var mu sync.Mutex
	svc := transportsvc.NewTransportService("fake", testRoot(), cmdsurface.SurfaceRPC, tr,
		transportsvc.Expose("*"),
		transportsvc.WithBridgeOptionsFunc(func() []cmdsurface.Option {
			mu.Lock()
			optCalls++
			mu.Unlock()
			return nil
		}),
	)

	require.NoError(t, svc.Prepare(context.Background()))
	calls, prepared := tr.seen()
	require.Equal(t, []string{"prepare"}, calls, "nothing binds while preparing")
	require.NotNil(t, prepared)
	assert.Nil(t, svc.Bridge(), "a prepared service is not started")

	ready, errCh := startService(t, svc)
	awaitReady(t, ready)

	calls, _ = tr.seen()
	assert.Equal(t, []string{"prepare", "bind"}, calls, "Start does not prepare again")
	assert.Same(t, prepared, svc.Bridge())
	mu.Lock()
	assert.Equal(t, 1, optCalls, "the bridge options are resolved once, while preparing")
	mu.Unlock()

	require.NoError(t, svc.Stop(context.Background()))
	require.NoError(t, <-errCh)
}

// Started outside a supervisor, with no Prepare before it, a service
// prepares itself before it binds.
func TestServiceStartPreparesWhenNotPrepared(t *testing.T) {
	t.Parallel()
	tr := &preparingTransport{fakeTransport: newFakeTransport()}
	svc := transportsvc.NewTransportService("fake", testRoot(), cmdsurface.SurfaceRPC, tr,
		transportsvc.Expose("*"))

	ready, errCh := startService(t, svc)
	awaitReady(t, ready)

	calls, prepared := tr.seen()
	assert.Equal(t, []string{"prepare", "bind"}, calls)
	assert.Same(t, prepared, svc.Bridge())

	require.NoError(t, svc.Stop(context.Background()))
	require.NoError(t, <-errCh)
}

// The transport's Prepare failing is the service's: Prepare returns
// it, and a Start that prepares itself fails with it before binding.
func TestServicePrepareFailureIsReturnedAndNothingBinds(t *testing.T) {
	t.Parallel()
	errCatalog := errors.New("catalog: refused")
	tr := &preparingTransport{fakeTransport: newFakeTransport(), prepareErr: errCatalog}
	svc := transportsvc.NewTransportService("fake", testRoot(), cmdsurface.SurfaceRPC, tr)

	assert.ErrorIs(t, svc.Prepare(context.Background()), errCatalog)

	ready, errCh := startService(t, svc)
	assert.ErrorIs(t, <-errCh, errCatalog)
	assert.Equal(t, 0, tr.binds())
	select {
	case <-ready:
		t.Fatal("ready must not be reported when Prepare fails")
	default:
	}
}
