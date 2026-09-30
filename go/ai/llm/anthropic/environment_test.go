package anthropic_test

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// recordingTransport answers every request with a canned Messages
// response and records what was sent. It replaces
// http.DefaultTransport so no request leaves the process, whichever
// base URL the SDK picks.
type recordingTransport struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.reqs = append(rt.reqs, req.Clone(req.Context()))
	rt.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(anthropicResponse("ok", "end_turn", 1, 1))),
		Request:    req,
	}, nil
}

func (rt *recordingTransport) only(t *testing.T) *http.Request {
	t.Helper()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	require.Len(t, rt.reqs, 1)
	return rt.reqs[0]
}

// isolateAnthropicEnv clears the SDK's ANTHROPIC_* environment inputs,
// points its profile directory at a temp dir, routes HTTP through a
// recordingTransport and captures the standard logger.
func isolateAnthropicEnv(t *testing.T) (configDir string, rt *recordingTransport, logs *bytes.Buffer) {
	t.Helper()
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"ANTHROPIC_PROFILE", "ANTHROPIC_WORKSPACE_ID", "ANTHROPIC_ORGANIZATION_ID",
		"ANTHROPIC_FEDERATION_RULE_ID", "ANTHROPIC_IDENTITY_TOKEN_FILE",
		"ANTHROPIC_CUSTOM_HEADERS",
	} {
		unsetenv(t, k)
	}
	configDir = t.TempDir()
	t.Setenv("ANTHROPIC_CONFIG_DIR", configDir)

	rt = &recordingTransport{}
	prevTransport := http.DefaultTransport
	http.DefaultTransport = rt
	t.Cleanup(func() { http.DefaultTransport = prevTransport })

	logs = &bytes.Buffer{}
	prevOut := log.Writer()
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(prevOut) })
	return configDir, rt, logs
}

// unsetenv removes k for the test and restores its prior value.
// t.Setenv(k, "") is not equivalent: the SDK treats a present-but-empty
// ANTHROPIC_BASE_URL as a base URL.
func unsetenv(t *testing.T, k string) {
	t.Helper()
	prev, had := os.LookupEnv(k)
	require.NoError(t, os.Unsetenv(k))
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(k, prev)
		} else {
			_ = os.Unsetenv(k)
		}
	})
}

// writeProfile writes an `ant auth login` style default profile.
func writeProfile(t *testing.T, dir, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "configs"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "configs", "default.json"), []byte(body), 0o600))
}

func completeOnce(t *testing.T, baseURL string) {
	t.Helper()
	p, err := anthropicFactory(llm.ResolvedConfig{Provider: llm.ProviderConfig{
		APIKey:  "kit-key",
		BaseURL: baseURL,
		Model:   "claude-sonnet-4-20250514",
	}})
	require.NoError(t, err)
	defer p.Close()
	_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	})
	require.NoError(t, err)
}

// The adapter resolves its own credential; an SDK profile on the host
// must not redirect the request, add headers, or log a shadow warning.
func TestNew_IgnoresSDKProfile(t *testing.T) {
	dir, rt, logs := isolateAnthropicEnv(t)
	writeProfile(t, dir, `{
		"authentication": {"type": "user_oauth"},
		"base_url": "https://profile.invalid",
		"workspace_id": "wrkspc_profile"
	}`)

	completeOnce(t, "")

	req := rt.only(t)
	assert.Equal(t, "api.anthropic.com", req.URL.Host)
	assert.Equal(t, "kit-key", req.Header.Get("X-Api-Key"))
	assert.Empty(t, req.Header.Get("Authorization"))
	assert.Empty(t, req.Header.Get("Anthropic-Workspace-Id"))
	assert.NotContains(t, logs.String(), "anthropic-sdk-go")
}

// ANTHROPIC_BASE_URL still applies when the kit config sets no base URL.
func TestNew_EnvBaseURLFallback(t *testing.T) {
	_, rt, _ := isolateAnthropicEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://gateway.example.test")

	completeOnce(t, "")

	req := rt.only(t)
	assert.Equal(t, "gateway.example.test", req.URL.Host)
	assert.Equal(t, "kit-key", req.Header.Get("X-Api-Key"))
}

// ANTHROPIC_WORKSPACE_ID is sent as the anthropic-workspace-id header,
// which keys not scoped to a workspace need.
func TestNew_EnvWorkspaceID(t *testing.T) {
	_, rt, _ := isolateAnthropicEnv(t)
	t.Setenv("ANTHROPIC_WORKSPACE_ID", "wrkspc_test")

	completeOnce(t, "")

	assert.Equal(t, "wrkspc_test", rt.only(t).Header.Get("Anthropic-Workspace-Id"))
}

// An empty ANTHROPIC_WORKSPACE_ID sends no header.
func TestNew_EmptyEnvWorkspaceID(t *testing.T) {
	_, rt, _ := isolateAnthropicEnv(t)
	t.Setenv("ANTHROPIC_WORKSPACE_ID", "")

	completeOnce(t, "")

	assert.Empty(t, rt.only(t).Header.Get("Anthropic-Workspace-Id"))
}

// A configured base URL wins over ANTHROPIC_BASE_URL.
func TestNew_ConfigBaseURLWinsOverEnv(t *testing.T) {
	_, rt, _ := isolateAnthropicEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://gateway.example.test")

	completeOnce(t, "https://configured.example.test")

	req := rt.only(t)
	assert.Equal(t, "configured.example.test", req.URL.Host)
}

// ANTHROPIC_AUTH_TOKEN is still sent as a bearer token alongside the key.
func TestNew_EnvAuthToken(t *testing.T) {
	_, rt, _ := isolateAnthropicEnv(t)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token")

	completeOnce(t, "")

	req := rt.only(t)
	assert.Equal(t, "kit-key", req.Header.Get("X-Api-Key"))
	assert.True(t, strings.HasSuffix(req.Header.Get("Authorization"), "env-token"))
}
