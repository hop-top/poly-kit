package anthropic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
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
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"nope"}}`)
			}))
			t.Cleanup(srv.Close)
			base := "http://user:" + baseURLSecret + "@" + strings.TrimPrefix(srv.URL, "http://")
			p, err := New(llm.ResolvedConfig{
				Provider: llm.ProviderConfig{BaseURL: base, Model: "claude-sonnet-4-20250514", APIKey: "k"},
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
	const raw = "http://" + baseURLSecret + "@h/v1/messages?key=" + baseURLSecret
	apiErr := func(status int) error {
		req, err := http.NewRequest(http.MethodPost, raw, nil)
		require.NoError(t, err)
		return &anthropic.Error{StatusCode: status, Request: req, Response: &http.Response{StatusCode: status}}
	}
	cases := map[string]error{
		"400":       apiErr(http.StatusBadRequest),
		"401":       apiErr(http.StatusUnauthorized),
		"404":       apiErr(http.StatusNotFound),
		"500":       apiErr(http.StatusInternalServerError),
		"transport": fmt.Errorf("send: %w", &url.Error{Op: "Post", URL: raw, Err: errors.New("connection refused")}),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got := mapError(in, "claude-sonnet-4-20250514")
			require.Error(t, got)
			assert.NotContains(t, got.Error(), baseURLSecret)
		})
	}
}
