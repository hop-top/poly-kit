package ollama_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
	}{{403, true}, {401, false}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
		}))
		p := mustNew(t, srv.URL, "llama3")
		_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
			Messages: []llm.Message{{Role: "user", Content: "hi"}},
		})
		srv.Close()
		require.Error(t, err)
		assert.Equal(t, tc.denied, errors.As(err, new(*llmerrors.ErrPermissionDenied)), "status %d", tc.status)
		assert.ErrorAs(t, err, new(*llmerrors.ErrAuth))
	}
}
