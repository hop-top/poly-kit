package transportsvc_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/transportsvc"
)

// fakeTransport records the seam's calls into it and lets a test
// drive Bind/Serve/Close independently.
type fakeTransport struct {
	addr     string
	bindErr  error
	serveErr error

	mu      sync.Mutex
	bound   int
	closed  int
	served  bool
	invoker transportsvc.Invoker
	release chan struct{}
	// closeUnblocksServe mirrors the real contract: Close makes
	// Serve return. A transport that leaves it false is the mutation
	// the seam's contract test pins.
	closeUnblocksServe bool
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		addr:               "/fake/addr",
		release:            make(chan struct{}),
		closeUnblocksServe: true,
	}
}

func (f *fakeTransport) Bind(context.Context) (string, error) {
	f.mu.Lock()
	f.bound++
	f.mu.Unlock()
	if f.bindErr != nil {
		return "", f.bindErr
	}
	return f.addr, nil
}

func (f *fakeTransport) Serve(ctx context.Context, inv transportsvc.Invoker) error {
	f.mu.Lock()
	f.served = true
	f.invoker = inv
	f.mu.Unlock()
	if f.serveErr != nil {
		return f.serveErr
	}
	select {
	case <-f.release:
	case <-ctx.Done():
	}
	return nil
}

func (f *fakeTransport) Close(context.Context) error {
	f.mu.Lock()
	f.closed++
	unblock := f.closeUnblocksServe
	f.mu.Unlock()
	if unblock {
		select {
		case <-f.release:
		default:
			close(f.release)
		}
	}
	return nil
}

func (f *fakeTransport) closes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeTransport) binds() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bound
}

func (f *fakeTransport) invoke(t *testing.T) transportsvc.Invoker {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotNil(t, f.invoker, "Serve was never handed an Invoker")
	return f.invoker
}

// testRoot builds a small cobra tree: one plain leaf, one destructive
// leaf, and one hidden leaf the reflector excludes.
func testRoot() *cobra.Command {
	root := &cobra.Command{Use: "tool"}

	ok := &cobra.Command{
		Use:  "ping",
		RunE: func(cmd *cobra.Command, _ []string) error { cmd.Print("pong"); return nil },
	}
	danger := &cobra.Command{
		Use:         "nuke",
		Annotations: map[string]string{"kit/side-effect": "destructive"},
		RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("boom"); return nil },
	}
	hidden := &cobra.Command{
		Use:    "secret",
		Hidden: true,
		RunE:   func(cmd *cobra.Command, _ []string) error { return nil },
	}
	root.AddCommand(ok, danger, hidden)
	return root
}

// startService runs svc and returns a stop func plus the readiness
// signal, so a test can assert on ready ordering.
func startService(t *testing.T, svc *transportsvc.TransportService) (chan struct{}, chan error) {
	t.Helper()
	ready := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { errCh <- svc.Start(ctx, func() { ready <- struct{}{} }) }()
	return ready, errCh
}

func TestServiceReportsReadyAfterBindWithAddress(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	svc := transportsvc.NewTransportService(
		"fake", testRoot(), cmdsurface.SurfaceRPC, tr,
		transportsvc.Expose("*"),
	)

	assert.False(t, svc.Ready(), "not ready before start")
	ready, errCh := startService(t, svc)

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("service never reported ready")
	}

	assert.True(t, svc.Ready())
	// The bound address reaches the supervisor through Addressed,
	// which is how an operator learns where it is actually listening.
	assert.Equal(t, "/fake/addr", svc.Addr())
	assert.Equal(t, 1, tr.binds())

	require.NoError(t, svc.Stop(context.Background()))
	require.NoError(t, <-errCh)
}

func TestServiceDoesNotReportReadyWhenBindFails(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	tr.bindErr = errors.New("address in use")
	svc := transportsvc.NewTransportService("fake", testRoot(), cmdsurface.SurfaceRPC, tr)

	ready, errCh := startService(t, svc)

	err := <-errCh
	require.Error(t, err)
	assert.Contains(t, err.Error(), "address in use")
	assert.False(t, svc.Ready())
	assert.Empty(t, svc.Addr())

	select {
	case <-ready:
		t.Fatal("ready must not be reported when Bind fails")
	default:
	}
}

func TestServiceReflectsCommandTreeAtStart(t *testing.T) {
	t.Parallel()
	root := testRoot()
	tr := newFakeTransport()
	svc := transportsvc.NewTransportService(
		"fake", root, cmdsurface.SurfaceRPC, tr, transportsvc.Expose("*"),
	)

	// A command mounted AFTER construction but BEFORE start must be
	// visible: reflecting at construction would miss it, which is the
	// whole reason reflection is deferred to Start.
	root.AddCommand(&cobra.Command{
		Use:  "late",
		RunE: func(cmd *cobra.Command, _ []string) error { cmd.Print("late"); return nil },
	})

	assert.Nil(t, svc.Bridge(), "no bridge before start")
	ready, errCh := startService(t, svc)
	<-ready

	bridge := svc.Bridge()
	require.NotNil(t, bridge)

	var paths []string
	for _, leaf := range bridge.Leaves() {
		paths = append(paths, leaf.PathKey())
	}
	assert.Contains(t, paths, "ping")
	assert.Contains(t, paths, "late", "a command mounted after construction must be reflected")

	// The excluded ones stay answerable rather than silently absent.
	assert.NotEmpty(t, bridge.NonInvocable())

	require.NoError(t, svc.Stop(context.Background()))
	require.NoError(t, <-errCh)
}

func TestInvokerPinsSurfaceAndRunsCommand(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	svc := transportsvc.NewTransportService(
		"fake", testRoot(), cmdsurface.SurfaceRPC, tr, transportsvc.Expose("*"),
	)
	ready, errCh := startService(t, svc)
	<-ready

	inv := tr.invoke(t)
	// Surface deliberately set to something else: the seam must
	// overwrite it, so a transport cannot invoke as another surface.
	res, err := inv(context.Background(), cmdsurface.Invocation{
		Path: []string{"ping"},
		Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceCLI},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Contains(t, res.Stdout, "pong")

	require.NoError(t, svc.Stop(context.Background()))
	require.NoError(t, <-errCh)
}

func TestInvokerAppliesPolicyGate(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	svc := transportsvc.NewTransportService(
		"fake", testRoot(), cmdsurface.SurfaceRPC, tr, transportsvc.Expose("*"),
	)
	ready, errCh := startService(t, svc)
	<-ready
	inv := tr.invoke(t)

	// Destructive leaf on a non-local surface: refused by the
	// destructive ceiling the seam routes every invocation through.
	_, err := inv(context.Background(), cmdsurface.Invocation{Path: []string{"nuke"}})
	require.Error(t, err)
	assert.ErrorIs(t, err, cmdsurface.ErrDestructiveBlocked)

	// Unknown path is distinguishable from a refused one.
	_, err = inv(context.Background(), cmdsurface.Invocation{Path: []string{"nope"}})
	assert.ErrorIs(t, err, cmdsurface.ErrUnknownCommand)

	require.NoError(t, svc.Stop(context.Background()))
	require.NoError(t, <-errCh)
}

func TestInvokerRefusesLeafNotExposedOnSurface(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	// No Expose at all: the bridge's defaults do not include RPC, so
	// the leaf exists but is not reachable here.
	svc := transportsvc.NewTransportService("fake", testRoot(), cmdsurface.SurfaceRPC, tr)
	ready, errCh := startService(t, svc)
	<-ready

	_, err := tr.invoke(t)(context.Background(), cmdsurface.Invocation{Path: []string{"ping"}})
	require.Error(t, err)
	assert.ErrorIs(t, err, cmdsurface.ErrSurfaceNotEnabled)

	require.NoError(t, svc.Stop(context.Background()))
	require.NoError(t, <-errCh)
}

func TestHideCarvesExceptionOutOfExpose(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	svc := transportsvc.NewTransportService(
		"fake", testRoot(), cmdsurface.SurfaceRPC, tr,
		transportsvc.Expose("*"), transportsvc.Hide("ping"),
	)
	ready, errCh := startService(t, svc)
	<-ready

	_, err := tr.invoke(t)(context.Background(), cmdsurface.Invocation{Path: []string{"ping"}})
	assert.ErrorIs(t, err, cmdsurface.ErrSurfaceNotEnabled)

	require.NoError(t, svc.Stop(context.Background()))
	require.NoError(t, <-errCh)
}

func TestStopClosesTransportAndIsIdempotent(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	svc := transportsvc.NewTransportService("fake", testRoot(), cmdsurface.SurfaceRPC, tr)
	ready, errCh := startService(t, svc)
	<-ready

	require.NoError(t, svc.Stop(context.Background()))
	require.NoError(t, <-errCh, "Close must make Serve return")
	assert.False(t, svc.Ready(), "not ready after stop")
	assert.Equal(t, 1, tr.closes())

	// A second Stop must not close the transport twice: the
	// supervisor stops once, but a transport that also closes itself
	// must not be double-closed.
	require.NoError(t, svc.Stop(context.Background()))
	assert.Equal(t, 1, tr.closes(), "second Stop must be a no-op")
}

func TestCancellationStopsServe(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	svc := transportsvc.NewTransportService("fake", testRoot(), cmdsurface.SurfaceRPC, tr)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	ready := make(chan struct{}, 1)
	go func() { errCh <- svc.Start(ctx, func() { ready <- struct{}{} }) }()
	<-ready

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err, "cancellation is a clean stop")
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return on cancellation")
	}
}

func TestServeFailurePropagates(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	tr.serveErr = errors.New("accept exploded")
	svc := transportsvc.NewTransportService("fake", testRoot(), cmdsurface.SurfaceRPC, tr)

	_, errCh := startService(t, svc)
	err := <-errCh
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accept exploded")
}

func TestOptionalDeclarationsReachTheSupervisor(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("path: not absolute")
	tr := newFakeTransport()
	svc := transportsvc.NewTransportService(
		"fake", testRoot(), cmdsurface.SurfaceRPC, tr,
		transportsvc.WithValidate(func() error { return wantErr }),
		transportsvc.WithClass("write-shared", "listen"),
		transportsvc.WithDependsOn("api"),
	)

	// Validator: the second gate, run before anything binds.
	assert.ErrorIs(t, svc.Validate(), wantErr)
	assert.Zero(t, tr.binds(), "Validate must not bind")

	// Classified: the third gate's input.
	se, network := svc.Class()
	assert.Equal(t, "write-shared", se)
	assert.Equal(t, "listen", network)

	// Dependent: start ordering.
	assert.Equal(t, []string{"api"}, svc.DependsOn())
}

func TestUndeclaredOptionalsAreInert(t *testing.T) {
	t.Parallel()
	svc := transportsvc.NewTransportService(
		"fake", testRoot(), cmdsurface.SurfaceRPC, newFakeTransport(),
	)
	assert.NoError(t, svc.Validate(), "no hook means the gate passes")

	se, network := svc.Class()
	assert.Empty(t, se)
	assert.Empty(t, network, "unclassified passes the policy gate")
	assert.Empty(t, svc.DependsOn())
}

func TestServiceRegistersUnderItsName(t *testing.T) {
	t.Parallel()
	svc := transportsvc.NewTransportService(
		"fake", testRoot(), cmdsurface.SurfaceRPC, newFakeTransport(),
	)
	assert.Equal(t, "fake", svc.Name())

	reg := serve.NewRegistry()
	reg.Register(svc)

	got, ok := reg.Lookup("fake")
	require.True(t, ok)
	assert.Same(t, svc, got)
	assert.Equal(t, []string{"fake"}, reg.Names())
}

func TestInvalidNamePanics(t *testing.T) {
	t.Parallel()
	// The name is a CLI word, a config key segment, and a bus payload
	// value at once, so an unusable one is a construction-time bug.
	assert.Panics(t, func() {
		transportsvc.NewTransportService("Bad Name", testRoot(), cmdsurface.SurfaceRPC, newFakeTransport())
	})
	assert.Panics(t, func() {
		// "list" is reserved selector vocabulary.
		transportsvc.NewTransportService("list", testRoot(), cmdsurface.SurfaceRPC, newFakeTransport())
	})
	assert.Panics(t, func() {
		transportsvc.NewTransportService("fake", testRoot(), cmdsurface.SurfaceRPC, nil)
	})
}

func TestStartWithoutRootFails(t *testing.T) {
	t.Parallel()
	svc := transportsvc.NewTransportService("fake", nil, cmdsurface.SurfaceRPC, newFakeTransport())
	err := svc.Start(context.Background(), func() { t.Fatal("must not report ready") })
	require.Error(t, err)
}
