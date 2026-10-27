package errors_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmerr "hop.top/kit/go/ai/llm/errors"
)

func TestErrPermissionDenied(t *testing.T) {
	cause := fmt.Errorf("403 forbidden")
	err := llmerr.NewPermissionDenied("anthropic", cause)

	assert.Equal(t, `permission denied (provider "anthropic"): 403 forbidden`, err.Error())

	var pd *llmerr.ErrPermissionDenied
	require.True(t, errors.As(err, &pd))
	assert.Equal(t, "anthropic", pd.Provider)

	// Callers matching the auth class keep matching a 403.
	var auth *llmerr.ErrAuth
	require.True(t, errors.As(err, &auth), "a permission denial is still an auth error")
	assert.Equal(t, "anthropic", auth.Provider)
	assert.ErrorIs(t, err, cause)

	// A 401 is not a permission denial.
	assert.False(t, errors.As(llmerr.NewAuth("anthropic", cause), &pd))

	assert.False(t, llmerr.IsFallbackable(err))
	assert.False(t, llmerr.IsFallbackable(fmt.Errorf("wrapped: %w", err)))
}
