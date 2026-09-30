package google_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/google"
	"hop.top/kit/go/core/netpolicy"
)

// leakKey is a fake key distinctive enough that any echo of it in an
// error string is unambiguous.
const leakKey = "fake-gemini-key-must-not-leak"

// callers runs each request path the adapter exposes against p and
// returns the error it yields.
var callers = map[string]func(context.Context, llm.Provider) error{
	"Complete": func(ctx context.Context, p llm.Provider) error {
		_, err := p.(llm.Completer).Complete(ctx, llm.Request{
			Messages: []llm.Message{{Role: "user", Content: "hi"}},
		})
		return err
	},
	"Stream": func(ctx context.Context, p llm.Provider) error {
		it, err := p.(llm.Streamer).Stream(ctx, llm.Request{
			Messages: []llm.Message{{Role: "user", Content: "hi"}},
		})
		if it != nil {
			_ = it.Close()
		}
		return err
	},
	"CallWithTools": func(ctx context.Context, p llm.Provider) error {
		_, err := p.(llm.ToolCaller).CallWithTools(ctx, llm.Request{
			Messages: []llm.Message{{Role: "user", Content: "hi"}},
		}, nil)
		return err
	},
}

func newLeakAdapter(t *testing.T, baseURL string) llm.Provider {
	t.Helper()
	p, err := google.New(llm.ResolvedConfig{
		Provider: llm.ProviderConfig{
			BaseURL: baseURL,
			Model:   "gemini-2.0-flash",
			APIKey:  leakKey,
		},
	})
	require.NoError(t, err)
	return p
}

// A transport failure surfaces net/http's *url.Error, which quotes the
// request URL; the key must not be part of that URL.
func TestKeyLeak_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close() // nothing listens on base any more: connection refused

	p := newLeakAdapter(t, base)
	for name, call := range callers {
		t.Run(name, func(t *testing.T) {
			err := call(context.Background(), p)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), leakKey)
		})
	}
}

// The --offline guard refuses the request inside the transport and
// names its URL; neither the guard's message nor net/http's wrapper may
// carry the key.
func TestKeyLeak_OfflineRefusal(t *testing.T) {
	netpolicy.Install()
	ctx := netpolicy.WithOffline(context.Background(), true)

	// Non-loopback so the guard bites; refused before any DNS lookup.
	p := newLeakAdapter(t, "https://gemini.invalid/v1beta")
	for name, call := range callers {
		t.Run(name, func(t *testing.T) {
			err := call(ctx, p)
			require.Error(t, err)
			assert.True(t, errors.Is(err, netpolicy.ErrOffline), "got %v", err)
			assert.NotContains(t, err.Error(), leakKey)
		})
	}
}

// The key travels in the x-goog-api-key header, as Google's genai SDK
// sends it, and never in the URL.
func TestKeyLeak_KeyInHeaderNotURL(t *testing.T) {
	type seen struct{ header, rawQuery string }
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			got = append(got, seen{r.Header.Get("x-goog-api-key"), r.URL.RawQuery})
			if strings.Contains(r.URL.Path, ":streamGenerateContent") {
				_, _ = w.Write([]byte(sseChunk("ok", "STOP")))
				return
			}
			writeJSON(w, geminiResponse("ok", "STOP", 1, 1))
		},
	))
	defer srv.Close()

	p := newLeakAdapter(t, srv.URL)
	for name, call := range callers {
		got = nil
		require.NoError(t, call(context.Background(), p), name)
		require.Len(t, got, 1, name)
		assert.Equal(t, leakKey, got[0].header, name)
		assert.NotContains(t, got[0].rawQuery, leakKey, name)
		assert.NotContains(t, got[0].rawQuery, "key=", name)
	}
}
