package ollama_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/ollama"
)

// baseURLSecret is a credential a caller embedded in the base URL
// (userinfo or a query param); any echo of it in an error is a leak.
const baseURLSecret = "fake-base-url-secret-must-not-leak"

// Transport and request-build failures quote the request URL, which
// starts with the base URL; credentials in it come back masked.
func TestBaseURLCredentialsNotInErrors(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	host := strings.TrimPrefix(srv.URL, "http://")
	srv.Close() // nothing listens on host any more: connection refused

	bases := map[string]string{
		"userinfo token": "http://" + baseURLSecret + "@" + host,
		"userinfo pass":  "http://user:" + baseURLSecret + "@" + host,
		"query key":      "http://" + host + "?key=" + baseURLSecret,
		"unparseable":    "http://h\x7f?key=" + baseURLSecret,
	}
	callers := map[string]func(context.Context, llm.Provider) error{
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
		"GenerateImage": func(ctx context.Context, p llm.Provider) error {
			_, err := p.(llm.ImageGenerator).GenerateImage(ctx, llm.ImageRequest{Prompt: "a cat"})
			return err
		},
	}
	for baseName, base := range bases {
		p, err := ollama.New(llm.ResolvedConfig{
			Provider: llm.ProviderConfig{BaseURL: base, Model: "llama3"},
		})
		require.NoError(t, err)
		for name, call := range callers {
			t.Run(baseName+"/"+name, func(t *testing.T) {
				err := call(context.Background(), p)
				require.Error(t, err)
				assert.NotContains(t, err.Error(), baseURLSecret)
			})
		}
	}
}
