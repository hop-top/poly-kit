package ollama_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xrr "hop.top/xrr"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/internal/transporttest"
)

// TestTransport_XRRReplay replays a cassette recorded against an
// httptest fake. Re-record with OLLAMA_XRR_RECORD=1.
func TestTransport_XRRReplay(t *testing.T) {
	dir := filepath.Join("testdata", "xrr")
	uri := "ollama://llama3?base_url=http://ollama.transport.test"
	call := func(t *testing.T, opts ...llm.ResolveOption) llm.Response {
		t.Helper()
		p, err := llm.Resolve(uri, opts...)
		require.NoError(t, err)
		defer p.Close()
		resp, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
			Messages: []llm.Message{{Role: "user", Content: "Hi"}},
		})
		require.NoError(t, err)
		return resp
	}

	if os.Getenv("OLLAMA_XRR_RECORD") != "" {
		fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"recorded hello"},`+
				`"done":true,"eval_count":3,"prompt_eval_count":2}`)
		}))
		defer fake.Close()
		require.NoError(t, os.MkdirAll(dir, 0o750))
		inner := &http.Transport{}
		rec := transporttest.XRRTransport{Session: xrr.NewSession(xrr.ModeRecord, xrr.NewFileCassette(dir)), Inner: inner}
		p, err := llm.Resolve("ollama://llama3?base_url="+fake.URL, llm.WithTransport(rec))
		require.NoError(t, err)
		defer p.Close()
		_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
			Messages: []llm.Message{{Role: "user", Content: "Hi"}},
		})
		require.NoError(t, err)
		return
	}

	defHits := transporttest.BlockDefault(t)
	rep := transporttest.XRRTransport{Session: xrr.NewSession(xrr.ModeReplay, xrr.NewFileCassette(dir))}
	resp := call(t, llm.WithTransport(rep), llm.Hermetic())
	assert.Equal(t, "recorded hello", resp.Content)
	assert.Equal(t, 5, resp.Usage.TotalTokens)
	assert.Zero(t, defHits())
}
