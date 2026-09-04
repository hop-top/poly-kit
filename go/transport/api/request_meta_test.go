package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// adopterClaims is a claims type the api package has never seen; it
// implements api.Identity.
type adopterClaims struct{ user, org string }

func (c adopterClaims) Principal() string { return c.user }
func (c adopterClaims) TenantID() string  { return c.org }

// mapClaims mirrors a JWT library's named map type.
type mapClaims map[string]any

func TestIdentityOfUnderstandsThreeShapes(t *testing.T) {
	cases := []struct {
		name           string
		claims         any
		wantP, wantT   string
		wantScopes     []string
		explainAbsence string
	}{
		{name: "nil", claims: nil},
		{
			name: "Claims value", claims: api.Claims{Subject: "alice", Tenant: "acme", Scopes: []string{"a", "b"}},
			wantP: "alice", wantT: "acme", wantScopes: []string{"a", "b"},
		},
		{
			name: "Claims pointer", claims: &api.Claims{Subject: "bob", Scopes: []string{"x"}},
			wantP: "bob", wantScopes: []string{"x"},
		},
		{
			name: "Identity implementation", claims: adopterClaims{"carol", "corp"},
			wantP: "carol", wantT: "corp",
		},
		{
			name: "map[string]any", claims: map[string]any{"sub": "dan", "tenant": "dept", "scopes": []any{"r", "w"}},
			wantP: "dan", wantT: "dept", wantScopes: []string{"r", "w"},
		},
		{
			name: "named map type", claims: mapClaims{"sub": "erin", "scopes": []string{"s"}},
			wantP: "erin", wantScopes: []string{"s"},
		},
		{
			name: "map[string]string", claims: map[string]string{"sub": "fay", "tenant": "t"},
			wantP: "fay", wantT: "t",
		},
		{name: "unrecognized", claims: 42, explainAbsence: "an int carries no identity"},
		{name: "map with non-string sub", claims: map[string]any{"sub": 7}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, tenant := api.IdentityOf(c.claims)
			assert.Equal(t, c.wantP, p, c.explainAbsence)
			assert.Equal(t, c.wantT, tenant)
			assert.Equal(t, c.wantScopes, api.ScopesOf(c.claims))
		})
	}
}

func TestTraceIDFromRequest(t *testing.T) {
	cases := []struct {
		name string
		hdr  map[string]string
		want string
	}{
		{"none", nil, ""},
		{
			"traceparent",
			map[string]string{"Traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
			"4bf92f3577b34da6a3ce929d0e0e4736",
		},
		{
			"traceparent uppercase is normalized",
			map[string]string{"Traceparent": "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01"},
			"4bf92f3577b34da6a3ce929d0e0e4736",
		},
		{
			"traceparent all-zero trace id is invalid",
			map[string]string{"Traceparent": "00-00000000000000000000000000000000-00f067aa0ba902b7-01", "X-Trace-ID": "fallback"},
			"fallback",
		},
		{
			"traceparent malformed falls back",
			map[string]string{"Traceparent": "garbage", "X-Trace-ID": "fallback"},
			"fallback",
		},
		{
			"traceparent non-hex",
			map[string]string{"Traceparent": "00-zzzz2f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
			"",
		},
		{"x-trace-id alone", map[string]string{"X-Trace-ID": "abc"}, "abc"},
		{
			"traceparent wins over x-trace-id",
			map[string]string{"Traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "X-Trace-ID": "abc"},
			"4bf92f3577b34da6a3ce929d0e0e4736",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range c.hdr {
				r.Header.Set(k, v)
			}
			assert.Equal(t, c.want, api.TraceIDFromRequest(r))
		})
	}
}

func TestAuthRefusalHookObservesEveryRefusal(t *testing.T) {
	var got []error
	var seen []string
	mw := api.Auth(func(r *http.Request) (any, error) {
		if r.Header.Get("Authorization") == "" {
			return nil, errors.New("missing token")
		}
		return api.Claims{Subject: "alice"}, nil
	}, api.OnAuthRefused(func(r *http.Request, err error) {
		got = append(got, err)
		seen = append(seen, api.GetRequestID(r))
	}))

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := api.IdentityOf(api.ClaimsFromContext(r.Context()))
		_, _ = w.Write([]byte(p))
	})
	h := api.Chain(api.RequestID(), mw)(next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "req-1")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Len(t, got, 1, "the hook sees the refusal")
	assert.EqualError(t, got[0], "missing token")
	assert.Equal(t, []string{"req-1"}, seen, "the hook sees the request id the middleware attached")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer x")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "alice", rec.Body.String())
	assert.Len(t, got, 1, "a permitted request does not reach the hook")
}

func TestRequestIDFromContext(t *testing.T) {
	assert.Empty(t, api.RequestIDFromContext(context.Background()),
		"no middleware, no id")
	var inCtx string
	h := api.RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		inCtx = api.RequestIDFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "echoed")
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, "echoed", inCtx)
}

// metaExecutor records the RequestMeta the projection handed it.
type metaExecutor struct {
	got api.CommandRequest
	ctx context.Context
}

func (m *metaExecutor) Execute(ctx context.Context, req api.CommandRequest) (api.CommandResult, error) {
	m.got = req
	m.ctx = ctx
	return api.CommandResult{}, nil
}

func TestProjectionHandsProvenanceToExecutor(t *testing.T) {
	ex := &metaExecutor{}
	r := api.NewRouter(api.WithMiddleware(
		api.RequestID(),
		api.Auth(func(*http.Request) (any, error) {
			return api.Claims{Subject: "alice", Tenant: "acme", Scopes: []string{"widgets:read"}}, nil
		}),
	))
	api.MountCommandProjection(r, api.ProjectionConfig{
		Descriptors: fixtureDescriptors(), Executor: ex,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/commands/list?filter=x", nil)
	req.Header.Set("X-Request-ID", "req-9")
	req.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	req.Header.Set("Idempotency-Key", "idem-9")
	req.RemoteAddr = "10.0.0.7:5555"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	m := ex.got.Meta
	assert.Equal(t, "alice", m.Principal)
	assert.Equal(t, "acme", m.Tenant)
	assert.Equal(t, []string{"widgets:read"}, m.Scopes)
	assert.Equal(t, "req-9", m.RequestID)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", m.TraceID)
	assert.Equal(t, "idem-9", m.IdempotencyKey)
	assert.Equal(t, "10.0.0.7:5555", m.RemoteAddr)
	assert.False(t, m.ReceivedAt.IsZero())
	assert.Equal(t, "req-9", rec.Header().Get("X-Request-ID"), "the id is echoed to the caller")
}

func TestPermissionDeniedIs403WithStableCode(t *testing.T) {
	ex := &stubExecutor{err: errors.Join(api.ErrPermissionDenied, errors.New("caller not entitled"))}
	r := newProjectedRouter(t, ex)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands/list?filter=x", nil))
	assert.Equal(t, http.StatusForbidden, rec.Code, "authenticated but not permitted is 403, not 401")

	var ae api.APIError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ae))
	assert.Equal(t, api.CodePermissionDenied, ae.Code)
	assert.Contains(t, ae.Message, "caller not entitled", "the body carries the gate's reason")
}
