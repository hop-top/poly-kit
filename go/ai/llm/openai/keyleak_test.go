package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	oai "github.com/openai/openai-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// baseURLSecret is a credential a caller embedded in the base URL
// (userinfo or a query param); any echo of it in an error is a leak.
const baseURLSecret = "fake-base-url-secret-must-not-leak"

// The SDK quotes the request URL in every API error, userinfo
// included; mapError must hand back a message without it.
func TestBaseURLCredentialsNotInAPIErrors(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"message":"nope","type":"invalid_request_error"}}`)
			})
			base := "http://user:" + baseURLSecret + "@" + strings.TrimPrefix(srv.URL, "http://")
			p, err := New(llm.ResolvedConfig{
				URI:      llm.URI{Scheme: "openai"},
				Provider: llm.ProviderConfig{BaseURL: base, Model: "gpt-4o", APIKey: "k"},
			})
			require.NoError(t, err)

			_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: "user", Content: "hi"}},
			})
			require.Error(t, err)
			assert.NotContains(t, err.Error(), baseURLSecret)
		})
	}
}

func TestMapError_RedactsURLs(t *testing.T) {
	const raw = "http://" + baseURLSecret + "@h/v1/chat/completions?key=" + baseURLSecret
	apiErr := func(status int) error {
		req, err := http.NewRequest(http.MethodPost, raw, nil)
		require.NoError(t, err)
		return &oai.Error{StatusCode: status, Request: req, Response: &http.Response{StatusCode: status}}
	}
	// Each case builds a fresh error: mapError masks it in place.
	cases := map[string]func() error{
		"400": func() error { return apiErr(http.StatusBadRequest) },
		"401": func() error { return apiErr(http.StatusUnauthorized) },
		"404": func() error { return apiErr(http.StatusNotFound) },
		"500": func() error { return apiErr(http.StatusInternalServerError) },
		"transport": func() error {
			return fmt.Errorf("send: %w", &url.Error{Op: "Post", URL: raw, Err: errors.New("connection refused")})
		},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			for _, got := range []error{
				mapError(in(), "openai", "gpt-4o"),
				mapImageError(in(), "openai", "gpt-image-1"),
			} {
				require.Error(t, got)
				assert.NotContains(t, got.Error(), baseURLSecret)
			}
		})
	}
}
