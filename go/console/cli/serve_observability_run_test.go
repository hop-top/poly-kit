package cli_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/transport/cmdsurface"
)

// lifecycleObservability records when serve starts and flushes it.
type lifecycleObservability struct {
	mu       sync.Mutex
	events   []string
	services []string
}

func (o *lifecycleObservability) record(e string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, e)
}

func (o *lifecycleObservability) Start(_ context.Context, _ *viper.Viper, _, _ string, services []string) (func(context.Context) error, error) {
	o.mu.Lock()
	o.services = services
	o.mu.Unlock()
	o.record("observability started")
	return func(context.Context) error { o.record("observability flushed"); return nil }, nil
}

func (o *lifecycleObservability) HTTPMiddleware(string) func(http.Handler) http.Handler { return nil }

func (o *lifecycleObservability) BridgeOptions(string) []cmdsurface.Option { return nil }

// orderedService records its start and stop into the same trail.
type orderedService struct {
	stubService
	o *lifecycleObservability
}

func (s *orderedService) Start(ctx context.Context, report func()) error {
	s.o.record("service started")
	return s.stubService.Start(ctx, report)
}

func (s *orderedService) Stop(ctx context.Context) error {
	s.o.record("service stopped")
	return s.stubService.Stop(ctx)
}

func TestServe_ObservabilityStartsBeforeServicesAndFlushesAfter(t *testing.T) {
	o := &lifecycleObservability{}
	svc := &orderedService{stubService: stubService{name: "worker"}, o: o}
	r := newServeRoot(t, cli.WithService(svc), cli.WithObservability(o))
	r.Viper.Set("services.worker.enabled", true)

	require.NoError(t, runServeArgs(t, r, []string{"serve"}, 300*time.Millisecond))

	o.mu.Lock()
	defer o.mu.Unlock()
	assert.Equal(t, []string{"worker"}, o.services, "the provider resolves every registered service")
	assert.Equal(t, []string{"observability started", "service started", "service stopped", "observability flushed"}, o.events)
}
