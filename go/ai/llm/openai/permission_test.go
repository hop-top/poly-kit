package openai

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

func TestError_Forbidden403IsPermissionDenied(t *testing.T) {
	for _, tc := range []struct {
		status int
		denied bool
	}{{http.StatusForbidden, true}, {http.StatusUnauthorized, false}} {
		srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"error":{"message":"no","type":"x"}}`))
		})
		p := newAdapter(t, "openai", srv.URL, "gpt-4o")
		_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
			Messages: []llm.Message{{Role: "user", Content: "Hi"}},
		})
		require.Error(t, err)
		assert.Equal(t, tc.denied, errors.As(err, new(*llmerrors.ErrPermissionDenied)), "status %d", tc.status)
		assert.ErrorAs(t, err, new(*llmerrors.ErrAuth), "status %d is an auth error", tc.status)
		assert.False(t, llmerrors.IsFallbackable(err))
	}
}
