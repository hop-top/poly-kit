package google_test

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
		name   string
		status int
		body   string
		denied bool
	}{
		{"api error 403", 403, `{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED"}}`, true},
		{"bare 403", 403, `nope`, true},
		{"api error 401", 401, `{"error":{"code":401,"message":"bad key","status":"UNAUTHENTICATED"}}`, false},
		{"bare 401", 401, `nope`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			p := mustNew(t, srv.URL, "gemini-2.0-flash")
			_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: "user", Content: "hi"}},
			})
			require.Error(t, err)
			assert.Equal(t, tc.denied, errors.As(err, new(*llmerrors.ErrPermissionDenied)))
			assert.ErrorAs(t, err, new(*llmerrors.ErrAuth))
		})
	}
}

func TestStream_Forbidden403EventIsPermissionDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"error\":{\"code\":403,\"message\":\"denied\"}}\n\n"))
	}))
	defer srv.Close()
	p := mustNew(t, srv.URL, "gemini-2.0-flash")
	it, err := p.(llm.Streamer).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	defer it.Close()
	_, err = it.Next()
	require.Error(t, err)
	assert.ErrorAs(t, err, new(*llmerrors.ErrPermissionDenied))
}
