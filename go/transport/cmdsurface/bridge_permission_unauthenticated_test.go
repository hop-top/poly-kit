package cmdsurface

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// authFailure is a permission function whose refusal is an
// authentication failure for "ghost" and an ordinary one for "mallory".
func authFailure(_ context.Context, meta Meta, _ *Leaf) PermissionDecision {
	switch meta.Caller {
	case "ghost":
		return PermissionDecision{Reason: "unknown_principal", Unauthenticated: true}
	case "mallory":
		return PermissionDecision{Reason: "no_grant"}
	}
	return PermissionDecision{Allowed: true}
}

func TestInvoke_PermissionRefusalCanBeAnAuthenticationFailure(t *testing.T) {
	calls := 0
	rec := &sinkRecorder{}
	b := New(newBridgeTree(),
		WithRunner(countingRunner(&calls, nil)),
		WithPermission(authFailure),
		WithSinks(SinkSpec{Sink: rec, OnOK: true, OnError: true}))
	b.Expose("*", SurfaceREST)
	ctx := context.Background()
	inv := func(caller string) Invocation {
		return Invocation{Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceREST, Caller: caller}}
	}

	_, err := b.Invoke(ctx, inv("ghost"))
	if !errors.Is(err, ErrAuthRefused) {
		t.Fatalf("err = %v, want ErrAuthRefused", err)
	}
	if errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("err = %v must not also be ErrPermissionDenied: one class, one answer", err)
	}
	for _, want := range []string{"unknown_principal", "widget add on rest"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q missing from %q", want, err)
		}
	}

	_, err = b.Invoke(ctx, inv("mallory"))
	if !errors.Is(err, ErrPermissionDenied) || errors.Is(err, ErrAuthRefused) {
		t.Fatalf("err = %v, want only ErrPermissionDenied", err)
	}
	if calls != 0 {
		t.Fatalf("runner reached %d times while refusing", calls)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.calls) != 2 {
		t.Fatalf("records = %d, want one per refusal", len(rec.calls))
	}
	if !errors.Is(rec.calls[0].err, ErrAuthRefused) || !strings.Contains(rec.calls[0].err.Error(), "unknown_principal") {
		t.Errorf("record 0 = %v, want the authentication class and the host's reason", rec.calls[0].err)
	}
	if !errors.Is(rec.calls[1].err, ErrPermissionDenied) {
		t.Errorf("record 1 = %v, want the permission class", rec.calls[1].err)
	}
}

func TestInvoke_UnauthenticatedFlagOnAnAllowedDecisionIsIgnored(t *testing.T) {
	calls := 0
	b := New(newBridgeTree(),
		WithRunner(countingRunner(&calls, nil)),
		WithPermission(func(context.Context, Meta, *Leaf) PermissionDecision {
			return PermissionDecision{Allowed: true, Unauthenticated: true}
		}))
	b.Expose("*", SurfaceREST)
	if _, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceREST, Caller: "alice"},
	}); err != nil || calls != 1 {
		t.Fatalf("err = %v, calls = %d, want the call to run", err, calls)
	}
}

func TestVerdict_KeepsTheAuthenticationClass(t *testing.T) {
	b := New(newBridgeTree(), WithPermission(authFailure))
	b.Expose("*", SurfaceREST)
	leaf, err := b.resolveLeaf([]string{"widget", "add"})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Verdict(context.Background(), Meta{Surface: SurfaceREST, Caller: "ghost"}, leaf); !errors.Is(err, ErrAuthRefused) {
		t.Errorf("ghost: verdict = %v, want ErrAuthRefused", err)
	}
	if err := b.Verdict(context.Background(), Meta{Surface: SurfaceREST, Caller: "mallory"}, leaf); !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("mallory: verdict = %v, want ErrPermissionDenied", err)
	}
	if err := b.Verdict(context.Background(), Meta{Surface: SurfaceREST, Caller: "alice"}, leaf); err != nil {
		t.Errorf("alice: verdict = %v, want nil", err)
	}
}

// A refusal of either class stops before the rate limiter: like a
// failure of the authentication stage it spends no token and is never
// counted, so refused callers cannot exhaust a bucket and a flood of
// them is not rate-limited into a different answer.
func TestInvoke_UnauthenticatedRefusalSpendsNoRateToken(t *testing.T) {
	b := New(newBridgeTree(),
		WithRunner(countingRunner(new(int), nil)),
		WithPermission(authFailure),
		WithRateLimit(RateLimit{
			Read:        RateRule{PerMinute: 1, Burst: 1},
			Write:       RateRule{PerMinute: 1, Burst: 1},
			Destructive: RateRule{PerMinute: 1, Burst: 1},
		}))
	b.Expose("*", SurfaceREST)
	ctx := context.Background()
	for range 5 {
		_, err := b.Invoke(ctx, Invocation{
			Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceREST, Caller: "ghost"},
		})
		if !errors.Is(err, ErrAuthRefused) || errors.Is(err, ErrRateLimited) {
			t.Fatalf("err = %v, want ErrAuthRefused every time, never rate limited", err)
		}
	}
	if _, err := b.Invoke(ctx, Invocation{
		Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceREST, Caller: "ghost"},
	}); errors.Is(err, ErrRateLimited) {
		t.Fatal("refusals must not draw from the bucket")
	}
}

// The OAuth callback answers an authentication refusal as 401 with the
// challenge, and a permission refusal as 403.
func TestOAuthInvokeErrorSeparatesAuthenticationFromPermission(t *testing.T) {
	for _, tc := range []struct {
		err       error
		status    int
		challenge bool
	}{
		{fmt.Errorf("%w: cb on oauth: unknown_principal", ErrAuthRefused), http.StatusUnauthorized, true},
		{fmt.Errorf("%w: cb on oauth: no_grant", ErrPermissionDenied), http.StatusForbidden, false},
	} {
		w := httptest.NewRecorder()
		oauthWriteInvokeError(w, OAuthProvider{}, tc.err)
		if w.Code != tc.status || (w.Header().Get("WWW-Authenticate") != "") != tc.challenge {
			t.Errorf("%v: status=%d challenge=%q, want %d (challenge=%t)",
				tc.err, w.Code, w.Header().Get("WWW-Authenticate"), tc.status, tc.challenge)
		}
	}
}
