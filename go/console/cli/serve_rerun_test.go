package cli_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// rerunService is a Service for re-execution tests: it comes up at
// once, stays up until its context is canceled, and can be crashed on
// demand so a run ends while the caller's context is still alive.
type rerunService struct {
	name string

	mu     sync.Mutex
	ready  bool
	starts int
	fail   chan struct{}
}

func newRerunService(name string) *rerunService {
	return &rerunService{name: name, fail: make(chan struct{})}
}

func (s *rerunService) Name() string { return s.name }

func (s *rerunService) Start(ctx context.Context, report func()) error {
	s.mu.Lock()
	s.starts++
	s.ready = true
	fail := s.fail
	s.mu.Unlock()
	report()
	select {
	case <-ctx.Done():
		return nil
	case <-fail:
		return errors.New("worker crashed")
	}
}

func (s *rerunService) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

func (s *rerunService) Stop(context.Context) error {
	s.mu.Lock()
	s.ready = false
	s.mu.Unlock()
	return nil
}

// crash fails the running Start and arms a fresh trigger for the next.
func (s *rerunService) crash() {
	s.mu.Lock()
	close(s.fail)
	s.fail = make(chan struct{})
	s.mu.Unlock()
}

func (s *rerunService) startCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starts
}

// startServe runs `serve` on r under ctx in the background and returns
// the channel Execute's result arrives on.
func startServe(r *cli.Root, ctx context.Context) <-chan error {
	r.SetArgs([]string{"serve"})
	errCh := make(chan error, 1)
	go func() { errCh <- r.Execute(ctx) }()
	return errCh
}

// awaitServe waits for a run to return, failing the test rather than
// hanging it when it does not.
func awaitServe(t *testing.T, errCh <-chan error, within time.Duration) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(within):
		t.Fatalf("serve did not return within %s", within)
		return nil
	}
}

// A Root may execute `serve` more than once in one process. Cobra
// copies the root's context onto a subcommand only when the subcommand
// has none, so without a reset the second run inherits the first run's
// context: canceled already, the second run stops at once without
// serving; still alive, the second run ignores its own cancellation
// and never returns.
func TestServe_SecondRunOnSameRootServesUntilItsOwnCancel(t *testing.T) {
	svc := newRerunService("worker")
	r := newServeRoot(t, cli.WithService(svc))
	r.Viper.Set("services.worker.enabled", true)

	ctx1, cancel1 := context.WithCancel(t.Context())
	errCh := startServe(r, ctx1)
	require.Eventually(t, svc.Ready, 2*time.Second, 10*time.Millisecond)
	cancel1()
	require.NoError(t, awaitServe(t, errCh, 5*time.Second), "a canceled run is a clean stop")

	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()
	errCh = startServe(r, ctx2)
	require.Eventually(t, svc.Ready, 2*time.Second, 10*time.Millisecond)
	select {
	case err := <-errCh:
		t.Fatalf("second run returned before its own context was canceled: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	assert.True(t, svc.Ready(), "the service keeps serving under the second run")

	cancel2()
	require.NoError(t, awaitServe(t, errCh, 5*time.Second))
	assert.False(t, svc.Ready(), "the second run stopped the service")
	assert.Equal(t, 2, svc.startCount())
}

func TestServe_SecondRunHonorsItsOwnCancelAfterCrashedFirstRun(t *testing.T) {
	svc := newRerunService("worker")
	r := newServeRoot(t, cli.WithService(svc))
	r.Viper.Set("services.worker.enabled", true)

	// The first run ends because its service crashes, so its context
	// is still alive when the second run starts.
	ctx1, cancel1 := context.WithCancel(t.Context())
	defer cancel1()
	errCh := startServe(r, ctx1)
	require.Eventually(t, svc.Ready, 2*time.Second, 10*time.Millisecond)
	svc.crash()
	err := awaitServe(t, errCh, 5*time.Second)
	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, output.CodeGeneric, kitErr.Code)
	assert.Equal(t, 1, kitErr.ExitCode, "a runtime crash is the generic failure")

	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()
	errCh = startServe(r, ctx2)
	require.Eventually(t, svc.Ready, 2*time.Second, 10*time.Millisecond)
	cancel2()
	require.NoError(t, awaitServe(t, errCh, 5*time.Second),
		"the second run must observe its own cancellation, not the first run's context")
}
