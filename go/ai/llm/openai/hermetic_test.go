package openai

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

func TestHermetic_NoSDKEnvHeaders(t *testing.T) {
	t.Setenv("OPENAI_ORG_ID", "org-env")
	t.Setenv("OPENAI_PROJECT_ID", "proj-env")
	for _, hermetic := range []bool{false, true} {
		var got http.Header
		srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Clone()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(completionJSON("ok", "assistant", "stop")))
		})
		cfg := resolvedCfg("openai", srv.URL, "gpt-4o")
		cfg.Hermetic = hermetic
		p, err := New(cfg)
		require.NoError(t, err)
		_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
			Messages: []llm.Message{{Role: "user", Content: "Hi"}},
		})
		require.NoError(t, err)
		if !hermetic {
			// Control: the SDK reads these by default.
			assert.Equal(t, "org-env", got.Get("OpenAI-Organization"))
			continue
		}
		assert.Empty(t, got.Values("OpenAI-Organization"))
		assert.Empty(t, got.Values("OpenAI-Project"))
		assert.Equal(t, "Bearer test-key", got.Get("Authorization"))
	}
}
