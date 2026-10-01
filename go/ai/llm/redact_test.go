package llm_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// fakeSecret marks every credential in these tests; any echo of it in a
// redacted string or error message is a leak.
const fakeSecret = "fake-secret-must-not-leak"

func TestRedactURI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"api_key", "openrouter://openai/gpt-4?api_key=" + fakeSecret,
			"openrouter://openai/gpt-4?api_key=REDACTED"},
		{"key", "google://gemini-2.0-flash?key=" + fakeSecret,
			"google://gemini-2.0-flash?key=REDACTED"},
		{"name case and hyphen", "openai://gpt-4?X-API-Key=" + fakeSecret,
			"openai://gpt-4?X-API-Key=REDACTED"},
		{"token", "openai://gpt-4?access_token=" + fakeSecret,
			"openai://gpt-4?access_token=REDACTED"},
		{"secret", "openai://gpt-4?client_secret=" + fakeSecret,
			"openai://gpt-4?client_secret=REDACTED"},
		{"password", "openai://gpt-4?password=" + fakeSecret,
			"openai://gpt-4?password=REDACTED"},
		{"signature", "https://cdn.example/a.png?X-Amz-Signature=" + fakeSecret,
			"https://cdn.example/a.png?X-Amz-Signature=REDACTED"},
		{"escaped name", "openai://gpt-4?api%5Fkey=" + fakeSecret,
			"openai://gpt-4?api%5Fkey=REDACTED"},
		{"multiple params",
			"openai://gpt-4?temperature=0.2&api_key=" + fakeSecret + "&key=" + fakeSecret + "&base_url=http://h:1/v1",
			"openai://gpt-4?temperature=0.2&api_key=REDACTED&key=REDACTED&base_url=http://h:1/v1"},
		{"fragment after key", "openai://gpt-4?api_key=" + fakeSecret + "#frag",
			"openai://gpt-4?api_key=REDACTED"},
		{"userinfo with password", "openai://user:" + fakeSecret + "@proxy:8080/gpt-4",
			"openai://REDACTED@proxy:8080/gpt-4"},
		{"userinfo token before path", "openai://" + fakeSecret + "@proxy/gpt-4",
			"openai://REDACTED@proxy/gpt-4"},
		{"http userinfo without path", "https://" + fakeSecret + "@proxy",
			"https://REDACTED@proxy"},
		{"net/http masked password", "http://user:***@h/x",
			"http://REDACTED@h/x"},
		{"base_url userinfo", "openai://gpt-4?base_url=https://" + fakeSecret + "@proxy",
			"openai://gpt-4?base_url=https://REDACTED@proxy"},
		{"base_url nested query", "openai://gpt-4?base_url=https://h/v1?key=" + fakeSecret,
			"openai://gpt-4?base_url=https://h/v1?key=REDACTED"},
		{"malformed: missing scheme", "openai/gpt-4?api_key=" + fakeSecret,
			"openai/gpt-4?api_key=REDACTED"},
		{"malformed: empty scheme", "://gpt-4?api_key=" + fakeSecret,
			"://gpt-4?api_key=REDACTED"},
		{"malformed: & without ?", "openai://gpt-4&api_key=" + fakeSecret,
			"openai://gpt-4&api_key=REDACTED"},
		{"malformed: doubled ?", "openai://gpt-4??api_key=" + fakeSecret,
			"openai://gpt-4??api_key=REDACTED"},
		{"malformed: bare param", "api_key=" + fakeSecret,
			"api_key=REDACTED"},
		{"malformed: no scheme userinfo", "user:" + fakeSecret + "@host/model",
			"REDACTED@host/model"},

		// Kept as given: nothing in them is a credential.
		{"empty", "", ""},
		{"plain", "openai://gpt-4o", "openai://gpt-4o"},
		{"host form", "ollama://localhost:11434/llama3?temperature=0.5",
			"ollama://localhost:11434/llama3?temperature=0.5"},
		{"look-alike names", "openai://gpt-4?max_tokens=10&keyword=a&top_k=3&monkey=1",
			"openai://gpt-4?max_tokens=10&keyword=a&top_k=3&monkey=1"},
		{"empty key value", "openai://gpt-4?api_key=", "openai://gpt-4?api_key="},
		{"model@version", "vertex://claude-3-5-sonnet@20240620",
			"vertex://claude-3-5-sonnet@20240620"},
		{"@ in model path", "openrouter://openai/gpt-4@latest",
			"openrouter://openai/gpt-4@latest"},
		{"already redacted", "openai://REDACTED@h/m?api_key=REDACTED",
			"openai://REDACTED@h/m?api_key=REDACTED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := llm.RedactURI(tt.in)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, fakeSecret)
		})
	}
}

func TestRedactURLError(t *testing.T) {
	assert.NoError(t, llm.RedactURLError(nil))

	inner := &url.Error{
		Op:  "Post",
		URL: "http://" + fakeSecret + "@127.0.0.1:1/v1?key=" + fakeSecret,
		Err: errors.New("connection refused"),
	}
	other := &url.Error{Op: "Get", URL: "https://h/x?token=" + fakeSecret, Err: io.EOF}
	err := fmt.Errorf("adapter: %w", errors.Join(inner, fmt.Errorf("again: %w", other)))

	got := llm.RedactURLError(err)
	assert.ErrorIs(t, got, err, "the result unwraps to the error it was given")
	assert.NotContains(t, got.Error(), fakeSecret)
	assert.Contains(t, got.Error(), "http://REDACTED@127.0.0.1:1/v1?key=REDACTED")
	assert.Contains(t, got.Error(), "https://h/x?token=REDACTED")

	var ue *url.Error
	require.ErrorAs(t, got, &ue, "the *url.Error stays in the chain")
	assert.ErrorIs(t, got, io.EOF)

	// A *url.Error formats its message on demand: masked in place, it
	// comes back as itself.
	bare := &url.Error{Op: "Get", URL: "https://h/x?api_key=" + fakeSecret, Err: io.EOF}
	got = llm.RedactURLError(bare)
	assert.Same(t, bare, got)
	assert.NotContains(t, got.Error(), fakeSecret)

	// Nothing to mask: the error comes back untouched.
	clean := fmt.Errorf("w: %w", &url.Error{Op: "Get", URL: "https://h/x", Err: io.EOF})
	assert.Same(t, clean, llm.RedactURLError(clean))
}

// ParseURI quotes its input in its errors; a malformed URI carrying a
// key must come back masked, with scheme and model still readable.
func TestParseURI_ErrorRedactsCredentials(t *testing.T) {
	for name, raw := range map[string]string{
		"missing scheme": "openai/gpt-4?api_key=" + fakeSecret,
		"empty scheme":   "://gpt-4?api_key=" + fakeSecret,
		"userinfo":       "user:" + fakeSecret + "@proxy/gpt-4?key=" + fakeSecret,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := llm.ParseURI(raw)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), fakeSecret)
			assert.Contains(t, err.Error(), "gpt-4")
			assert.Contains(t, err.Error(), "REDACTED")
		})
	}
}

func TestLoadConfig_ErrorRedactsCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, err := llm.LoadConfig("openai/gpt-4?api_key=" + fakeSecret)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), fakeSecret)
	assert.Contains(t, err.Error(), "openai/gpt-4?api_key=REDACTED")
}

func TestResolve_InvalidURIRedactsCredentials(t *testing.T) {
	r := llm.NewRegistry()
	for name, raw := range map[string]string{
		"missing scheme": "openai/gpt-4?api_key=" + fakeSecret,
		"empty scheme":   "://gpt-4?key=" + fakeSecret,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := r.Resolve(raw)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), fakeSecret)
			assert.Contains(t, err.Error(), "llm: invalid URI")
			assert.Contains(t, err.Error(), "gpt-4")
		})
	}
}

// URLSource fetches a caller's URL, which may be presigned; neither a
// bad status, a transport failure nor an unparseable URL may echo its
// credentials.
func TestURLSource_ErrorRedactsCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close() // nothing listens any more: connection refused

	for name, raw := range map[string]string{
		"bad status":  srv.URL + "/a.png?X-Amz-Signature=" + fakeSecret,
		"transport":   "http://" + fakeSecret + "@" + closedURL[len("http://"):] + "/a.png?sig=" + fakeSecret,
		"unparseable": "http://h\x7f/a.png?token=" + fakeSecret,
	} {
		t.Run(name, func(t *testing.T) {
			rc, err := llm.URLSource(raw).Reader(context.Background())
			if rc != nil {
				_ = rc.Close()
			}
			require.Error(t, err)
			assert.NotContains(t, err.Error(), fakeSecret)
		})
	}
}
