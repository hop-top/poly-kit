package ollama_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/ollama"
)

// authServer answers chat (plain and streamed) and generate requests
// and records each request's path and Authorization header values: nil
// when none was sent.
type authServer struct {
	mu    sync.Mutex
	paths []string
	auth  [][]string
}

func newAuthServer(t *testing.T) (*httptest.Server, *authServer) {
	t.Helper()
	as := &authServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		as.mu.Lock()
		as.paths = append(as.paths, r.URL.Path)
		as.auth = append(as.auth, r.Header.Values("Authorization"))
		as.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/generate" {
			_ = json.NewEncoder(w).Encode(map[string]any{"images": []string{"aGk="}, "done": true})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"role": "assistant", "content": "ok"},
			"done":    true,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, as
}

// exercise sends one request down every path the adapter has.
func exercise(t *testing.T, p llm.Provider) {
	t.Helper()
	ctx := context.Background()
	req := llm.Request{Messages: []llm.Message{{Role: "user", Content: "hi"}}}

	_, err := p.(llm.Completer).Complete(ctx, req)
	require.NoError(t, err)

	it, err := p.(llm.Streamer).Stream(ctx, req)
	require.NoError(t, err)
	require.NoError(t, it.Close())

	_, err = p.(llm.ImageGenerator).GenerateImage(ctx, llm.ImageRequest{Prompt: "a cat"})
	require.NoError(t, err)
}

// The key the URI carries goes out as a bearer token on chat, stream
// and image requests alike; a blank or missing key sends no
// Authorization header at all.
func TestAdapter_SendsKeyWhenSet(t *testing.T) {
	cases := map[string][]string{
		"":         nil,
		"  ":       nil,
		"\t":       nil,
		"fake-key": {"Bearer fake-key"},
	}
	for key, want := range cases {
		t.Run(key, func(t *testing.T) {
			srv, as := newAuthServer(t)
			p, err := ollama.New(llm.ResolvedConfig{Provider: llm.ProviderConfig{
				BaseURL: srv.URL, Model: "llama3", APIKey: key,
			}})
			require.NoError(t, err)
			exercise(t, p)
			assert.Equal(t, []string{"/api/chat", "/api/chat", "/api/generate"}, as.paths)
			for i, got := range as.auth {
				assert.Equal(t, want, got, as.paths[i])
			}
		})
	}
}

// End to end: OLLAMA_API_KEY reaches the server through ApplyAPIKey and
// Resolve when set, and nothing is sent when it is unset or blank.
func TestAdapter_OllamaAPIKeyEndToEnd(t *testing.T) {
	cases := []struct {
		name  string
		unset bool
		value string
		want  []string
	}{
		{name: "unset", unset: true},
		{name: "empty", value: ""},
		{name: "whitespace", value: "  "},
		{name: "set", value: "fake-ollama", want: []string{"Bearer fake-ollama"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			t.Setenv("OLLAMA_API_KEY", tc.value)
			if tc.unset {
				require.NoError(t, os.Unsetenv("OLLAMA_API_KEY"))
			}
			srv, as := newAuthServer(t)
			uri, err := llm.ApplyAPIKey(context.Background(), nil, "ollama://llama3?base_url="+srv.URL)
			require.NoError(t, err)
			p, err := llm.Resolve(uri)
			require.NoError(t, err)
			exercise(t, p)
			require.Len(t, as.auth, 3)
			for i, got := range as.auth {
				assert.Equal(t, tc.want, got, as.paths[i])
			}
		})
	}
}
