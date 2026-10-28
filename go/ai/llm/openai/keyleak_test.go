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

// The SDK flattens a base URL parse error, URL included, into a plain
// string no caller can mask; New rejects it first with the URL masked.
func TestNew_UnparseableBaseURLRedacted(t *testing.T) {
	for name, base := range map[string]string{
		"userinfo token": "http://" + baseURLSecret + "@h\x7f/v1",
		"userinfo pass":  "http://user:" + baseURLSecret + "@h\x7f/v1",
		"query key":      "http://h\x7f/v1?key=" + baseURLSecret,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(llm.ResolvedConfig{
				URI:      llm.URI{Scheme: "openai"},
				Provider: llm.ProviderConfig{BaseURL: base, Model: "gpt-4o", APIKey: "k"},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "openai: invalid base URL")
			assert.Contains(t, err.Error(), "REDACTED")
			assert.NotContains(t, err.Error(), baseURLSecret)
		})
	}
}

// A key a response echoes never surfaces in an error: a non-streamed
// error body or a streamed error chunk.
func TestEchoedKeyScrubbed(t *testing.T) {
	const key = "sk-echoed-key-must-not-leak"
	cases := map[string]struct {
		stream bool
		status int
		body   string
	}{
		"error body":         {status: http.StatusBadRequest, body: `{"error":{"message":"bad key ` + key + `","type":"invalid_request_error"}}`},
		"stream error chunk": {stream: true, status: http.StatusOK, body: "data: {\"error\":{\"message\":\"bad key " + key + "\",\"type\":\"server_error\"}}\n\n"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
				ct := "application/json"
				if c.stream {
					ct = "text/event-stream"
				}
				w.Header().Set("Content-Type", ct)
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.body)
			})
			p, err := New(llm.ResolvedConfig{
				URI:      llm.URI{Scheme: "openai"},
				Provider: llm.ProviderConfig{BaseURL: srv.URL, Model: "gpt-4o", APIKey: key},
			})
			require.NoError(t, err)
			req := llm.Request{Messages: []llm.Message{{Role: "user", Content: "hi"}}}
			if c.stream {
				var it llm.TokenIterator
				if it, err = p.(llm.Streamer).Stream(context.Background(), req); err == nil {
					for err == nil {
						_, err = it.Next()
					}
				}
			} else {
				_, err = p.(llm.Completer).Complete(context.Background(), req)
			}
			require.Error(t, err)
			assert.NotErrorIs(t, err, io.EOF)
			assert.NotContains(t, err.Error(), key)
			assert.NotContains(t, fmt.Sprintf("%+v", err), key)
		})
	}
}
