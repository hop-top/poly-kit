package rpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"

	"hop.top/kit/go/transport/api"
)

type requestIDKey struct{}

// RequestIDFromContext extracts the request ID stored by
// RequestIDInterceptor.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

// RequestIDInterceptor injects a unique request ID into the context
// of every RPC call, mirroring api.RequestID for HTTP handlers.
func RequestIDInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			id := req.Header().Get("X-Request-ID")
			if id == "" {
				id = newID()
			}
			ctx = context.WithValue(ctx, requestIDKey{}, id)
			req.Header().Set("X-Request-ID", id)
			return next(ctx, req)
		}
	}
}

type claimsKeyType struct{}

// ClaimsFromContext extracts auth claims stored by AuthInterceptor.
func ClaimsFromContext(ctx context.Context) any {
	return ctx.Value(claimsKeyType{})
}

// AuthInterceptor adapts api.AuthFunc for RPC use. Note: the
// AuthFunc receives a synthetic *http.Request containing only
// headers from the RPC metadata. AuthFunc implementations must
// not rely on URL, Method, Body, or other http.Request fields.
func AuthInterceptor(fn api.AuthFunc) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			hr := &http.Request{Header: http.Header(req.Header().Clone())}
			hr = hr.WithContext(ctx)

			claims, err := fn(hr)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, err)
			}
			ctx = context.WithValue(ctx, claimsKeyType{}, claims)
			return next(ctx, req)
		}
	}
}

// LogInterceptor logs every unary RPC call with procedure name,
// duration, and any error.
func LogInterceptor(logFn func(string, ...any)) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			resp, err := next(ctx, req)
			logFn("rpc request",
				"procedure", req.Spec().Procedure,
				"duration", fmt.Sprintf("%dms", time.Since(start).Milliseconds()),
				"error", err,
			)
			return resp, err
		}
	}
}

// RecoveryInterceptor catches panics in unary handlers and converts
// them to connect Internal errors.
func RecoveryInterceptor(onPanic func(any)) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (resp connect.AnyResponse, err error) {
			defer func() {
				if v := recover(); v != nil {
					if onPanic != nil {
						onPanic(v)
					}
					err = connect.NewError(connect.CodeInternal,
						fmt.Errorf("panic: %v", v))
				}
			}()
			return next(ctx, req)
		}
	}
}

var idCounter atomic.Uint64

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x-%x", time.Now().UnixNano(), idCounter.Add(1))
	}
	return hex.EncodeToString(b)
}
