package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// toggleService is a sibling service whose readiness a test flips, and
// which can crash after reporting ready without resetting Ready — the
// case only the supervisor's record catches.
type toggleService struct {
	name  string
	crash chan struct{}

	mu    sync.Mutex
	ready bool
}

func newToggle(name string) *toggleService {
	return &toggleService{name: name, crash: make(chan struct{})}
}

func (s *toggleService) Name() string { return s.name }

func (s *toggleService) Start(ctx context.Context, report func()) error {
	s.set(true)
	report()
	select {
	case <-ctx.Done():
		return nil
	case <-s.crash:
		return errors.New("crashed")
	}
}

func (s *toggleService) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

func (s *toggleService) set(v bool) {
	s.mu.Lock()
	s.ready = v
	s.mu.Unlock()
}

func (s *toggleService) Stop(context.Context) error { s.set(false); return nil }

// probe issues method against base+path and decodes the health body.
func probe(t *testing.T, base, path string, header ...string) (int, api.HealthStatus) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	require.NoError(t, err)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var st api.HealthStatus
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusServiceUnavailable {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	}
	return resp.StatusCode, st
}

// serveArgs runs `serve` with args and waits for the api service to
// report ready, returning its base URL and a stop func.
func serveArgs(t *testing.T, r *Root, args ...string) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r.SetArgs(append([]string{"serve"}, args...))
	errCh := make(chan error, 1)
	go func() { errCh <- r.Execute(ctx) }()

	a := apiSvc(t, r)
	deadline := time.Now().Add(10 * time.Second)
	for !a.Ready() || a.Addr() == "" {
		select {
		case err := <-errCh:
			cancel()
			t.Fatalf("serve returned before the api was ready: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("api never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return "http://" + a.Addr(), func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("serve did not return after cancellation")
		}
	}
}

func healthRoot(t *testing.T, cfg APIConfig, opts ...func(*Root)) *Root {
	t.Helper()
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	return authRoot(t, append([]func(*Root){WithAPI(cfg)}, opts...)...)
}

func TestAPIHealth_LiveAndReadyWhileServing(t *testing.T) {
	r := healthRoot(t, APIConfig{})
	base, stop := serveAPI(t, r)
	defer stop()

	code, st := probe(t, base, "/healthz")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, api.HealthStatus{Status: "ok"}, st)

	code, st = probe(t, base, "/readyz")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, api.HealthStatus{Status: "ok"}, st)
}

func TestAPIHealth_NotReadyBeforeStartOrAfterStop(t *testing.T) {
	r := healthRoot(t, APIConfig{})
	h := projectionHandler(t, r)

	get := func(path string) (int, api.HealthStatus) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		var st api.HealthStatus
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
		return rec.Code, st
	}

	code, st := get("/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, code, "not ready before Start reports ready")
	assert.Equal(t, []string{"api"}, st.Failing)
	code, _ = get("/healthz")
	assert.Equal(t, http.StatusOK, code, "liveness is not readiness")

	base, stop := serveAPI(t, r)
	code, _ = probe(t, base, "/readyz")
	assert.Equal(t, http.StatusOK, code)
	stop()

	code, _ = get("/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, code, "not ready once Stop begins draining")
}

func TestAPIHealth_DependencyNotReady(t *testing.T) {
	dep := newToggle("dep")
	r := healthRoot(t, APIConfig{DependsOn: []string{"dep"}}, WithService(dep))
	base, stop := serveArgs(t, r, "--enable", "dep")
	defer stop()

	code, _ := probe(t, base, "/readyz")
	require.Equal(t, http.StatusOK, code)

	dep.set(false)
	code, st := probe(t, base, "/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Equal(t, api.HealthStatus{Status: "unavailable", Failing: []string{"dep"}}, st)

	code, _ = probe(t, base, "/healthz")
	assert.Equal(t, http.StatusOK, code, "a dependency never fails liveness")

	dep.set(true)
	code, _ = probe(t, base, "/readyz")
	assert.Equal(t, http.StatusOK, code)
}

func TestAPIHealth_CrashedDependencyUnderIsolate(t *testing.T) {
	dep := newToggle("dep")
	r := healthRoot(t, APIConfig{DependsOn: []string{"dep"}}, WithService(dep))
	r.Viper.Set("services.failure_policy", "isolate")
	base, stop := serveArgs(t, r, "--enable", "dep")
	defer stop()

	close(dep.crash)
	require.Eventually(t, func() bool {
		code, _ := probe(t, base, "/readyz")
		return code == http.StatusServiceUnavailable
	}, 5*time.Second, 10*time.Millisecond,
		"the supervisor recorded the crash; the dependency's own Ready never noticed")
	assert.True(t, dep.Ready())
	_, st := probe(t, base, "/readyz")
	assert.Equal(t, []string{"dep"}, st.Failing)
}

func TestAPIHealth_DependencyOutsideTheRunIsNotChecked(t *testing.T) {
	dep := newToggle("dep")
	r := healthRoot(t, APIConfig{DependsOn: []string{"dep"}}, WithService(dep))
	base, stop := serveAPI(t, r) // selector form: dep is not started
	defer stop()

	code, _ := probe(t, base, "/readyz")
	assert.Equal(t, http.StatusOK, code,
		"a dependency the run did not start is the operator's business, as it is for start order")
}

func TestAPIHealth_DependsOnOrdersStart(t *testing.T) {
	r := healthRoot(t, APIConfig{DependsOn: []string{"dep"}}, WithService(newToggle("dep")))
	a := apiSvc(t, r)
	assert.Equal(t, []string{"dep"}, a.DependsOn())
}

func TestAPIHealth_SkipsAuth(t *testing.T) {
	deny := func(*http.Request) (any, error) { return nil, errors.New("no") }
	r := healthRoot(t, APIConfig{Auth: deny})
	base, stop := serveAPI(t, r)
	defer stop()

	code, _ := probe(t, base, "/healthz")
	assert.Equal(t, http.StatusOK, code, "liveness needs no Authorization")
	code, _ = probe(t, base, "/readyz")
	assert.Equal(t, http.StatusOK, code, "readiness needs no Authorization")

	code, _ = probe(t, base, "/v1/commands")
	assert.Equal(t, http.StatusUnauthorized, code, "everything else still does")
}

func TestAPIHealth_AbsentFromDiscoveryAndOpenAPI(t *testing.T) {
	h := projectionHandler(t, healthRoot(t, APIConfig{}))

	for name := range discoveryOf(t, h) {
		assert.NotContains(t, name, "healthz")
		assert.NotContains(t, name, "readyz")
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.OpenAPISpecPath, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "healthz")
	assert.NotContains(t, rec.Body.String(), "readyz")
}

func TestAPIHealth_Disabled(t *testing.T) {
	r := healthRoot(t, APIConfig{})
	r.Viper.Set("services.api.health.enabled", false)
	h := projectionHandler(t, r)

	for _, p := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		assert.Equal(t, http.StatusNotFound, rec.Code, p)
	}
}

func TestAPIHealth_PathPrefix(t *testing.T) {
	r := healthRoot(t, APIConfig{})
	r.Viper.Set("services.api.health.path_prefix", "/_kit")
	base, stop := serveAPI(t, r)
	defer stop()

	code, _ := probe(t, base, "/_kit/readyz")
	assert.Equal(t, http.StatusOK, code)
	code, _ = probe(t, base, "/_kit/healthz")
	assert.Equal(t, http.StatusOK, code)
	code, _ = probe(t, base, "/healthz")
	assert.Equal(t, http.StatusNotFound, code)
}

func TestAPIHealth_InvalidPathPrefixIsAConfigError(t *testing.T) {
	for _, bad := range []string{"_kit", "/_kit/", "/a/../b", "/a b", "/a?b"} {
		r := healthRoot(t, APIConfig{})
		r.Viper.Set("services.api.health.path_prefix", bad)
		err := apiSvc(t, r).Validate()
		require.Error(t, err, bad)
		assert.True(t, strings.Contains(err.Error(), "services.api.health.path_prefix"), err.Error())
	}
}

func TestAPIHealth_DetailFollowsBind(t *testing.T) {
	allow := func(*http.Request) (any, error) { return "caller", nil }
	failing := func(r *Root) []string {
		rec := httptest.NewRecorder()
		projectionHandler(t, r).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		var st api.HealthStatus
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
		return st.Failing
	}

	assert.Equal(t, []string{"api"}, failing(healthRoot(t, APIConfig{})),
		"loopback names failing checks")

	remote := healthRoot(t, APIConfig{Addr: "0.0.0.0:0", Auth: allow})
	assert.Empty(t, failing(remote), "a remote bind names nothing unless configured")

	remote.Viper.Set("services.api.health.detail", true)
	assert.Equal(t, []string{"api"}, failing(remote))

	local := healthRoot(t, APIConfig{})
	local.Viper.Set("services.api.health.detail", false)
	assert.Empty(t, failing(local))
}

func TestAPIHealth_SharedDefaultsFromServicesAll(t *testing.T) {
	r := healthRoot(t, APIConfig{})
	r.Viper.Set("services.all.health.enabled", false)
	h := projectionHandler(t, r)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code, "services.all.health.enabled applies to api")

	r = healthRoot(t, APIConfig{})
	r.Viper.Set("services.all.health.enabled", false)
	r.Viper.Set("services.api.health.enabled", true)
	r.Viper.Set("services.all.health.path_prefix", "/_kit")
	h = projectionHandler(t, r)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_kit/healthz", nil))
	assert.Equal(t, http.StatusOK, rec.Code,
		"the service's own key wins over services.all, key by key")
}

func TestAPIHealth_UnknownKeyIsAConfigError(t *testing.T) {
	for _, key := range []string{"services.api.health.enabeld", "services.all.health.prefix"} {
		r := healthRoot(t, APIConfig{})
		r.Viper.Set(key, true)
		err := apiSvc(t, r).Validate()
		require.Error(t, err, key)
		assert.Contains(t, err.Error(), key)
	}
}

func TestAPIHealth_AdopterRouteAtTheSamePathWins(t *testing.T) {
	r := healthRoot(t, APIConfig{Handlers: func(rt *api.Router) {
		rt.Handle(http.MethodGet, "/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		})
	}})
	h := projectionHandler(t, r)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusTeapot, rec.Code)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "kit still answers the probe the adopter left alone")
}

func TestAPIHealth_InsideTheRequestIDLayer(t *testing.T) {
	h := projectionHandler(t, healthRoot(t, APIConfig{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"),
		"request id, access log and recovery wrap the probes")
}
