package anthropic_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/anthropic"
)

// The key never travels over plain http to a remote host, wherever the
// base URL came from; loopback (a local proxy) stays reachable.
func TestNew_RefusesKeyOverPlainHTTP(t *testing.T) {
	newWith := func(base string) error {
		_, err := anthropic.New(llm.ResolvedConfig{Provider: llm.ProviderConfig{
			APIKey: "k", BaseURL: base, Model: "m",
		}})
		return err
	}
	t.Run("config", func(t *testing.T) {
		clearAnthropicEnv(t)
		assert.ErrorIs(t, newWith("http://gateway.example.test"), llm.ErrKeyEndpoint)
		assert.NoError(t, newWith("http://127.0.0.1:9"))
		assert.NoError(t, newWith("https://gateway.example.test"))
	})
	t.Run("ANTHROPIC_BASE_URL", func(t *testing.T) {
		clearAnthropicEnv(t)
		t.Setenv("ANTHROPIC_BASE_URL", "http://gateway.example.test")
		assert.ErrorIs(t, newWith(""), llm.ErrKeyEndpoint)
		t.Setenv("ANTHROPIC_BASE_URL", "http://localhost:9")
		assert.NoError(t, newWith(""))
	})
}

// The default endpoint is declared, so naming it in the URI keeps the key.
func TestApplyAPIKey_DefaultEndpointGetsTheKey(t *testing.T) {
	clearAnthropicEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "fake-env")
	got, err := llm.ApplyAPIKey(context.Background(), nil, "anthropic://m?base_url=https://api.anthropic.com")
	require.NoError(t, err)
	assert.Equal(t, "anthropic://m?base_url=https://api.anthropic.com&api_key=fake-env", got)

	_, err = llm.ApplyAPIKey(context.Background(), nil, "anthropic://m?base_url=https://evil.example")
	assert.ErrorIs(t, err, llm.ErrKeyEndpoint)
}

// A server's retry wait is honored only within (0, 60s]; anything else
// (zero, negative, NaN, unparsable, huge) falls back to backoff, so a
// hostile or broken server cannot stall the client.
func TestRetryAfter_OutOfRangeFallsBackToBackoff(t *testing.T) {
	cases := []struct {
		name, header, value string
		minWait             time.Duration
	}{
		{"in range seconds", "retry-after", "0.3", 300 * time.Millisecond},
		{"in range ms", "retry-after-ms", "300", 300 * time.Millisecond},
		{"zero", "retry-after", "0", 300 * time.Millisecond},
		{"negative", "retry-after", "-5", 300 * time.Millisecond},
		{"nan", "retry-after", "NaN", 300 * time.Millisecond},
		{"unparsable", "retry-after", "soon", 300 * time.Millisecond},
		{"huge seconds", "retry-after", "999999", 300 * time.Millisecond},
		{"huge ms", "retry-after-ms", "60001", 300 * time.Millisecond},
		{"just over the cap", "retry-after", "61", 300 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAnthropicEnv(t)
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if n.Add(1) == 1 {
					w.Header().Set(tc.header, tc.value)
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"x"}}`)
					return
				}
				okMessage(w)
			}))
			t.Cleanup(srv.Close)
			p := newConfAdapter(t, srv.URL, "m", "k")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			start := time.Now()
			_, err := p.(llm.Completer).Complete(ctx, llm.Request{
				Messages: []llm.Message{{Role: "user", Content: "Hi"}},
			})
			elapsed := time.Since(start)
			require.NoError(t, err)
			assert.Equal(t, int32(2), n.Load())
			assert.GreaterOrEqual(t, elapsed, tc.minWait-50*time.Millisecond)
			assert.Less(t, elapsed, 3*time.Second)
		})
	}
}
