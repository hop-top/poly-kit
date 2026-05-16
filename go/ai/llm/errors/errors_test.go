package errors_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmerr "hop.top/kit/go/ai/llm/errors"
)

// --- Error() messages ---

func TestErrProviderNotFound_Error(t *testing.T) {
	err := llmerr.NewProviderNotFound("openai")
	assert.Equal(t, `provider not found: scheme "openai"`, err.Error())
}

func TestErrCapabilityNotSupported_Error(t *testing.T) {
	err := llmerr.NewCapabilityNotSupported("streaming", "ollama")
	assert.Equal(t, `capability "streaming" not supported by provider "ollama"`, err.Error())
}

func TestErrAuth_Error(t *testing.T) {
	cause := fmt.Errorf("401 unauthorized")
	err := llmerr.NewAuth("openai", cause)
	assert.Equal(t, `auth error (provider "openai"): 401 unauthorized`, err.Error())
}

func TestErrRateLimit_Error(t *testing.T) {
	err := llmerr.NewRateLimit("anthropic", 30*time.Second)
	assert.Equal(t, `rate limited (provider "anthropic", retry after 30s)`, err.Error())
}

func TestErrContext_Error(t *testing.T) {
	cause := context.DeadlineExceeded
	err := llmerr.NewContext(cause)
	assert.Equal(t, "context error: context deadline exceeded", err.Error())
}

func TestErrModel_Error(t *testing.T) {
	err := llmerr.NewModel("gpt-5", "openai")
	assert.Equal(t, `model "gpt-5" not available (provider "openai")`, err.Error())
}

func TestErrFallbackExhausted_Error(t *testing.T) {
	errs := []error{
		fmt.Errorf("provider A failed"),
		fmt.Errorf("provider B failed"),
	}
	err := llmerr.NewFallbackExhausted(errs)
	assert.Contains(t, err.Error(), "all providers failed")
	assert.Contains(t, err.Error(), "2 errors")
}

// --- Unwrap / errors.Is / errors.As ---

func TestErrAuth_Unwrap(t *testing.T) {
	cause := fmt.Errorf("token expired")
	err := llmerr.NewAuth("openai", cause)

	assert.True(t, errors.Is(err, cause))

	var authErr *llmerr.ErrAuth
	require.True(t, errors.As(err, &authErr))
	assert.Equal(t, "openai", authErr.Provider)
}

func TestErrRateLimit_Unwrap(t *testing.T) {
	err := llmerr.NewRateLimit("anthropic", 10*time.Second)

	var rlErr *llmerr.ErrRateLimit
	require.True(t, errors.As(err, &rlErr))
	assert.Equal(t, "anthropic", rlErr.Provider)
	assert.Equal(t, 10*time.Second, rlErr.RetryAfter)
}

func TestErrContext_Unwrap(t *testing.T) {
	cause := context.Canceled
	err := llmerr.NewContext(cause)

	assert.True(t, errors.Is(err, context.Canceled))
}

func TestErrProviderNotFound_Unwrap(t *testing.T) {
	err := llmerr.NewProviderNotFound("foo")

	var pnf *llmerr.ErrProviderNotFound
	require.True(t, errors.As(err, &pnf))
	assert.Equal(t, "foo", pnf.Scheme)
	// No underlying error to unwrap.
	assert.Nil(t, errors.Unwrap(err))
}

func TestErrModel_Unwrap(t *testing.T) {
	err := llmerr.NewModel("claude-4", "anthropic")

	var mErr *llmerr.ErrModel
	require.True(t, errors.As(err, &mErr))
	assert.Equal(t, "claude-4", mErr.Model)
	assert.Equal(t, "anthropic", mErr.Provider)
}

func TestErrFallbackExhausted_Unwrap_ReturnsNil(t *testing.T) {
	errs := []error{fmt.Errorf("a"), fmt.Errorf("b")}
	err := llmerr.NewFallbackExhausted(errs)

	// Unwrap returns nil (no single underlying error).
	assert.Nil(t, errors.Unwrap(err))

	// But Errors field is accessible via errors.As.
	var fe *llmerr.ErrFallbackExhausted
	require.True(t, errors.As(err, &fe))
	assert.Len(t, fe.Errors, 2)
}

// --- IsFallbackable ---

func TestIsFallbackable_ErrRateLimit(t *testing.T) {
	err := llmerr.NewRateLimit("openai", 5*time.Second)
	assert.True(t, llmerr.IsFallbackable(err))
}

func TestIsFallbackable_NetOpError(t *testing.T) {
	err := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: fmt.Errorf("connection refused"),
	}
	assert.True(t, llmerr.IsFallbackable(err))
}

func TestIsFallbackable_WrappedNetError(t *testing.T) {
	inner := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: fmt.Errorf("connection refused"),
	}
	err := fmt.Errorf("request failed: %w", inner)
	assert.True(t, llmerr.IsFallbackable(err))
}

func TestIsFallbackable_HTTPStatus5xx(t *testing.T) {
	err := llmerr.NewHTTPStatusError(502, "bad gateway")
	assert.True(t, llmerr.IsFallbackable(err))
}

func TestIsFallbackable_False_ErrAuth(t *testing.T) {
	err := llmerr.NewAuth("openai", fmt.Errorf("bad key"))
	assert.False(t, llmerr.IsFallbackable(err))
}

func TestIsFallbackable_False_ErrModel(t *testing.T) {
	err := llmerr.NewModel("gpt-9", "openai")
	assert.False(t, llmerr.IsFallbackable(err))
}

func TestIsFallbackable_False_ErrCapabilityNotSupported(t *testing.T) {
	err := llmerr.NewCapabilityNotSupported("tools", "ollama")
	assert.False(t, llmerr.IsFallbackable(err))
}

func TestIsFallbackable_False_ErrContext(t *testing.T) {
	err := llmerr.NewContext(context.Canceled)
	assert.False(t, llmerr.IsFallbackable(err))
}

func TestIsFallbackable_Nil(t *testing.T) {
	assert.False(t, llmerr.IsFallbackable(nil))
}
