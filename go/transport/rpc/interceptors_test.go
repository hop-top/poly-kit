package rpc_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/rpc"
)

// --- helpers ---

func unaryHandler(resp connect.AnyResponse, err error) connect.UnaryFunc {
	return func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return resp, err
	}
}

func panicHandler() connect.UnaryFunc {
	return func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		panic("boom")
	}
}

func fakeRequest() connect.AnyRequest {
	return connect.NewRequest[any](nil)
}

func fakeSpec() connect.Spec {
	return connect.Spec{Procedure: "/test.v1.Svc/Method"}
}

// specRequest wraps a request with a fixed Spec.
type specRequest struct {
	connect.AnyRequest
	spec connect.Spec
}

func (s *specRequest) Spec() connect.Spec { return s.spec }

func newSpecRequest(spec connect.Spec) connect.AnyRequest {
	base := connect.NewRequest[any](nil)
	return &specRequest{AnyRequest: base, spec: spec}
}

// --- tests ---

func TestAuthInterceptorRejectsUnauthenticated(t *testing.T) {
	authFn := func(r *http.Request) (any, error) {
		return nil, errors.New("bad token")
	}

	interceptor := rpc.AuthInterceptor(authFn)
	wrapped := interceptor(unaryHandler(nil, nil))

	_, err := wrapped(context.Background(), fakeRequest())
	require.Error(t, err)

	var connectErr *connect.Error
	require.True(t, errors.As(err, &connectErr))
	assert.Equal(t, connect.CodeUnauthenticated, connectErr.Code())
}

func TestAuthInterceptorPassesClaims(t *testing.T) {
	type userClaims struct{ UserID string }
	want := userClaims{UserID: "u-42"}

	authFn := func(r *http.Request) (any, error) {
		return want, nil
	}

	var got any
	handler := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		got = rpc.ClaimsFromContext(ctx)
		return nil, nil
	}

	interceptor := rpc.AuthInterceptor(authFn)
	wrapped := interceptor(handler)

	_, err := wrapped(context.Background(), fakeRequest())
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestLogInterceptorLogsMethodAndDuration(t *testing.T) {
	var mu sync.Mutex
	var logged []any

	logFn := func(msg string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, msg)
		logged = append(logged, args...)
	}

	interceptor := rpc.LogInterceptor(logFn)
	handler := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, nil
	}
	wrapped := interceptor(handler)

	req := newSpecRequest(connect.Spec{Procedure: "/test.v1.Svc/Ping"})
	_, err := wrapped(context.Background(), req)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()

	// Check msg.
	require.NotEmpty(t, logged)
	assert.Equal(t, "rpc request", logged[0])

	// Find procedure and duration in key-value pairs.
	full := fmt.Sprint(logged)
	assert.True(t, strings.Contains(full, "/test.v1.Svc/Ping"),
		"expected procedure in log output: %s", full)
	assert.True(t, strings.Contains(full, "duration"),
		"expected duration in log output: %s", full)
}

func TestRecoveryInterceptorCatchesPanics(t *testing.T) {
	var recovered any

	interceptor := rpc.RecoveryInterceptor(func(v any) {
		recovered = v
	})
	wrapped := interceptor(panicHandler())

	_, err := wrapped(context.Background(), fakeRequest())
	require.Error(t, err)

	var connectErr *connect.Error
	require.True(t, errors.As(err, &connectErr))
	assert.Equal(t, connect.CodeInternal, connectErr.Code())
	assert.Equal(t, "boom", recovered)
}

func TestRequestIDInterceptorAddsID(t *testing.T) {
	var gotID string
	handler := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		gotID = rpc.RequestIDFromContext(ctx)
		return nil, nil
	}

	interceptor := rpc.RequestIDInterceptor()
	wrapped := interceptor(handler)

	_, err := wrapped(context.Background(), fakeRequest())
	require.NoError(t, err)
	assert.NotEmpty(t, gotID, "expected request ID in context")
}

func TestRequestIDInterceptorPreservesExisting(t *testing.T) {
	var gotID string
	handler := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		gotID = rpc.RequestIDFromContext(ctx)
		return nil, nil
	}

	interceptor := rpc.RequestIDInterceptor()
	wrapped := interceptor(handler)

	req := connect.NewRequest[any](nil)
	req.Header().Set("X-Request-ID", "existing-id-123")

	_, err := wrapped(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "existing-id-123", gotID)
}
