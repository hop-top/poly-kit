package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/cmdsurface"
)

// fakeObservability records what the serve path asks of a provider.
type fakeObservability struct {
	mu       sync.Mutex
	started  []string
	tool     string
	stopped  bool
	startErr error
	invoked  []string
}

func (f *fakeObservability) Start(_ context.Context, _ *viper.Viper, name, _ string, services []string) (func(context.Context) error, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started, f.tool = services, name
	if f.startErr != nil {
		return nil, f.startErr
	}
	return func(context.Context) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.stopped = true
		return nil
	}, nil
}

func (f *fakeObservability) HTTPMiddleware(service string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Observed", service)
			next.ServeHTTP(w, r)
		})
	}
}

func (f *fakeObservability) BridgeOptions(service string) []cmdsurface.Option {
	return []cmdsurface.Option{cmdsurface.WithRunnerMiddleware(func(next cmdsurface.Runner) cmdsurface.Runner {
		return observedFake{next: next, f: f, service: service}
	})}
}

type observedFake struct {
	next    cmdsurface.Runner
	f       *fakeObservability
	service string
}

func (o observedFake) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	o.f.mu.Lock()
	o.f.invoked = append(o.f.invoked, o.service+":"+inv.Meta.Traceparent)
	o.f.mu.Unlock()
	return o.next.Run(ctx, inv)
}

func (o observedFake) Stream(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	return o.next.Stream(ctx, inv, out)
}

func TestObservabilityConfigWithoutAProviderIsRefused(t *testing.T) {
	for _, key := range []string{"services.all.tracing.enabled", "services.api.metrics.enabled"} {
		t.Run(key, func(t *testing.T) {
			r := New(Config{Name: "tool", DisableValidate: true}, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
			r.Viper.Set(key, true)

			_, err := r.startObservability(t.Context(), []string{APIServiceName})
			var kerr *output.Error
			require.True(t, errors.As(err, &kerr), "want a usage error, got %v", err)
			assert.Equal(t, 2, kerr.ExitCode)
			assert.Contains(t, kerr.Message, key)
		})
	}

	// Nothing requested, nothing linked: nothing to do.
	r := New(Config{Name: "tool", DisableValidate: true}, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	r.Viper.Set("services.api.tracing.enabled", false)
	flush, err := r.startObservability(t.Context(), []string{APIServiceName})
	require.NoError(t, err)
	flush()
}

func TestObservabilityStartsWithTheServicesAndFlushesAfter(t *testing.T) {
	f := &fakeObservability{}
	r := New(Config{Name: "tool", DisableValidate: true}, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), WithObservability(f))

	flush, err := r.startObservability(t.Context(), []string{"api", "socket"})
	require.NoError(t, err)
	assert.Equal(t, []string{"api", "socket"}, f.started)
	assert.Equal(t, "tool", f.tool)
	assert.False(t, f.stopped)
	flush()
	assert.True(t, f.stopped)

	f.startErr = errors.New("services.api.tracing.exporter: unknown exporter")
	_, err = r.startObservability(t.Context(), []string{"api"})
	var kerr *output.Error
	require.True(t, errors.As(err, &kerr))
	assert.Equal(t, 2, kerr.ExitCode, "a provider's configuration error is a usage error")
}

func TestAPIServiceInstallsTheLinkedObservability(t *testing.T) {
	const tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	f := &fakeObservability{}
	r := projectionFixture(t)
	WithObservability(f)(r)

	h := projectionHandler(t, r)
	req := httptest.NewRequest(http.MethodGet, "/v1/commands/list", nil)
	req.Header.Set("traceparent", tp)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.Equal(t, "api", rec.Header().Get("X-Observed"), "the middleware is in the api chain")
	assert.Equal(t, []string{"api:" + tp}, f.invoked,
		"the bridge carries the observer, and the caller's traceparent reaches it in Meta")
}

func TestAPIServiceWithoutObservabilityIsUnchanged(t *testing.T) {
	h := projectionHandler(t, projectionFixture(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands/list", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("X-Observed"))
}

// A service built outside this package (mcp, rpc) gets the linked
// provider's invocation instrumentation for its own name through
// ServeBridgeOptions, as the socket service does in-package.
func TestServeBridgeOptionsCarryTheLinkedObservability(t *testing.T) {
	f := &fakeObservability{}
	r := projectionFixture(t)
	WithObservability(f)(r)

	opts, err := ServeBridgeOptions(r, "mcp")
	require.NoError(t, err)
	b := cmdsurface.New(r.Cmd, opts...)
	b.Expose("*", cmdsurface.SurfaceMCP)
	_, err = b.Invoke(t.Context(), cmdsurface.Invocation{
		Path: []string{"list"},
		Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceMCP, Traceparent: "tp"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"mcp:tp"}, f.invoked)
}
