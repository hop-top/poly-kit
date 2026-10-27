package routellm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

func TestHermetic_IgnoresRouteLLMEnv(t *testing.T) {
	var cfgHits, envHits atomic.Int32
	reply := func(n *atomic.Int32) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":0,"model":"m",` +
				`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	fromCfg, fromEnv := reply(&cfgHits), reply(&envHits)
	t.Setenv("ROUTELLM_BASE_URL", fromEnv.URL)

	for _, hermetic := range []bool{false, true} {
		p, err := New(llm.ResolvedConfig{
			URI: llm.URI{Scheme: "routellm", Model: "mf:0.5"},
			Provider: llm.ProviderConfig{
				Model: "mf:0.5", APIKey: "k",
				Extras: map[string]any{"routellm": map[string]any{"base_url": fromCfg.URL}},
			},
			Hermetic: hermetic,
		})
		require.NoError(t, err)
		_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
			Messages: []llm.Message{{Role: "user", Content: "Hi"}},
		})
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), envHits.Load(), "ambient: ROUTELLM_BASE_URL wins")
	assert.Equal(t, int32(1), cfgHits.Load(), "hermetic: the config alone")
}
