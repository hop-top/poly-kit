package cli_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/internal/testhome"
	"hop.top/kit/go/console/output"
)

// silentService starts and never reports ready.
type silentService struct{ name string }

func (n *silentService) Name() string { return n.name }
func (n *silentService) Start(ctx context.Context, _ func()) error {
	<-ctx.Done()
	return nil
}
func (n *silentService) Ready() bool                { return false }
func (n *silentService) Stop(context.Context) error { return nil }

// stuckStopService is ready at once and its Stop never returns until
// the test ends.
type stuckStopService struct {
	name    string
	release chan struct{}
}

func (s *stuckStopService) Name() string { return s.name }
func (s *stuckStopService) Start(ctx context.Context, report func()) error {
	report()
	<-ctx.Done()
	return nil
}
func (s *stuckStopService) Ready() bool { return true }
func (s *stuckStopService) Stop(context.Context) error {
	<-s.release
	return nil
}

// TestServe_SelectorReadyTimeoutFlagReachesAnUnconfiguredService pins
// that --ready-timeout applies to the service the selector names even
// when it has no services.<name> block.
func TestServe_SelectorReadyTimeoutFlagReachesAnUnconfiguredService(t *testing.T) {
	testhome.Isolate(t)
	r := newServeRoot(t, cli.WithService(&silentService{name: "slow"}))

	var oe *output.Error
	require.ErrorAs(t, runServeArgs(t, r, []string{"serve", "slow", "--ready-timeout", "50ms"}, 5*time.Second), &oe,
		"the flag's budget, not the 30s default, decides")
	assert.Equal(t, 1, oe.ExitCode)
	assert.Contains(t, oe.Message, "not ready within 50ms")
}

// TestServe_SelectorStopTimeoutFlagReachesAnUnconfiguredService pins
// the same for --stop-timeout.
func TestServe_SelectorStopTimeoutFlagReachesAnUnconfiguredService(t *testing.T) {
	testhome.Isolate(t)
	stuck := &stuckStopService{name: "hang", release: make(chan struct{})}
	t.Cleanup(func() { close(stuck.release) })
	r := newServeRoot(t, cli.WithService(stuck))

	var oe *output.Error
	require.ErrorAs(t, runServeArgs(t, r,
		[]string{"serve", "hang", "--stop-timeout", "50ms", "--shutdown-timeout", "2s"}, 300*time.Millisecond), &oe)
	assert.Equal(t, 1, oe.ExitCode)
	assert.Contains(t, oe.Message, "stop exceeded 50ms", "the flag's budget, not the 30s default, bounded the Stop")
}
