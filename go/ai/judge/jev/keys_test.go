package jev_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/judge/jev"
	"hop.top/kit/go/ai/llm"
)

const (
	otherKey = "sk-or-v1-other-1111111111111111"
	proxyURL = "https://proxy.example.test/api/v1/systemone"
)

// TestNoProviderSDKInDeps pins the package's dependency set: importing
// jev must not link a provider SDK, so a hermetic tool that passes its
// own key can depend on it.
func TestNoProviderSDKInDeps(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go binary not on PATH")
	}
	out, err := exec.Command(goBin, "list", "-deps", ".").CombinedOutput()
	require.NoError(t, err, string(out))
	for _, pkg := range strings.Fields(string(out)) {
		assert.False(t, strings.HasPrefix(pkg, "github.com/openai/"),
			"jev links provider SDK package %s", pkg)
	}
}

// isolate points llm.yaml lookup at an empty dir and clears every env var
// that can supply a key or an endpoint.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	for _, k := range []string{"OPENROUTER_API_KEY", "LLM_API_KEY", "LLM_BASE_URL"} {
		t.Setenv(k, "")
	}
	return dir
}

// writeLLMYAML writes llm.yaml where llm reads it.
func writeLLMYAML(t *testing.T, dir, body string) {
	t.Helper()
	hop := filepath.Join(dir, "hop")
	require.NoError(t, os.MkdirAll(hop, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hop, "llm.yaml"), []byte(body), 0o600))
}

// authFor builds a judge and returns the Authorization header it sends.
func authFor(t *testing.T, opts ...jev.Option) string {
	t.Helper()
	f := &fakeTransport{body: allKindsResponse}
	j, err := jev.New(append([]jev.Option{jev.WithTransport(f)}, opts...)...)
	require.NoError(t, err)
	_, err = j.Judge(context.Background(), allKindsCase())
	require.NoError(t, err)
	return f.last(t).headers.Get("Authorization")
}

func TestKeyFromLLMAPIKeyFallback(t *testing.T) {
	isolate(t)
	t.Setenv("LLM_API_KEY", testKey)
	assert.Equal(t, "Bearer "+testKey, authFor(t))
}

func TestOpenRouterEnvBeatsFallback(t *testing.T) {
	isolate(t)
	t.Setenv("OPENROUTER_API_KEY", testKey)
	t.Setenv("LLM_API_KEY", otherKey)
	assert.Equal(t, "Bearer "+testKey, authFor(t))
}

func TestKeyFromLLMYAMLLiteral(t *testing.T) {
	dir := isolate(t)
	writeLLMYAML(t, dir, "providers:\n  openrouter:\n    api_key: "+testKey+"\n")
	assert.Equal(t, "Bearer "+testKey, authFor(t))
}

func TestKeyFromLLMYAMLEnvName(t *testing.T) {
	dir := isolate(t)
	writeLLMYAML(t, dir, "providers:\n  openrouter:\n    api_key_env: MY_JEV_KEY\n")
	t.Setenv("MY_JEV_KEY", testKey)
	assert.Equal(t, "Bearer "+testKey, authFor(t))
}

func TestWithAPIKeyBeatsEnvironment(t *testing.T) {
	isolate(t)
	t.Setenv("OPENROUTER_API_KEY", otherKey)
	assert.Equal(t, "Bearer "+testKey, authFor(t, jev.WithAPIKey(testKey)))
}

func TestHermeticRequiresAPIKey(t *testing.T) {
	dir := isolate(t)
	t.Setenv("OPENROUTER_API_KEY", otherKey)
	t.Setenv("LLM_API_KEY", otherKey)
	writeLLMYAML(t, dir, "providers:\n  openrouter:\n    api_key: "+otherKey+"\n")
	_, err := jev.New(jev.Hermetic(), jev.WithTransport(&fakeTransport{}))
	require.ErrorIs(t, err, llm.ErrMissingKey)
	assert.NotContains(t, err.Error(), otherKey)

	_, err = jev.New(jev.Hermetic(), jev.WithAPIKey("  "), jev.WithTransport(&fakeTransport{}))
	require.ErrorIs(t, err, llm.ErrMissingKey, "a blank key is no key")
}

func TestHermeticIgnoresEnvironmentAndConfig(t *testing.T) {
	dir := isolate(t)
	t.Setenv("OPENROUTER_API_KEY", otherKey)
	t.Setenv("LLM_API_KEY", otherKey)
	writeLLMYAML(t, dir, "providers:\n  openrouter:\n    api_key: "+otherKey+"\n")
	assert.Equal(t, "Bearer "+testKey, authFor(t, jev.Hermetic(), jev.WithAPIKey(testKey)))
}

func TestConfiguredBaseURLTrusted(t *testing.T) {
	t.Run("llm.yaml base_url", func(t *testing.T) {
		dir := isolate(t)
		writeLLMYAML(t, dir, "providers:\n  openrouter:\n    base_url: "+proxyURL+"\n")
		_, err := jev.New(jev.WithAPIKey(testKey), jev.WithURL(proxyURL),
			jev.WithTransport(&fakeTransport{}))
		require.NoError(t, err)
	})
	t.Run("LLM_BASE_URL", func(t *testing.T) {
		isolate(t)
		t.Setenv("LLM_BASE_URL", proxyURL)
		_, err := jev.New(jev.WithAPIKey(testKey), jev.WithURL(proxyURL),
			jev.WithTransport(&fakeTransport{}))
		require.NoError(t, err)
	})
}

func TestHermeticIgnoresConfiguredBaseURL(t *testing.T) {
	dir := isolate(t)
	t.Setenv("LLM_BASE_URL", proxyURL)
	writeLLMYAML(t, dir, "providers:\n  openrouter:\n    base_url: "+proxyURL+"\n")
	_, err := jev.New(jev.Hermetic(), jev.WithAPIKey(testKey), jev.WithURL(proxyURL),
		jev.WithTransport(&fakeTransport{}))
	require.ErrorIs(t, err, llm.ErrKeyEndpoint)
	assert.NotContains(t, err.Error(), testKey)
}

func TestHermeticEndpointTrust(t *testing.T) {
	isolate(t)
	tests := []struct {
		name string
		url  string
		ok   bool
	}{
		{"default", jev.DefaultURL, true},
		{"default other path", "https://openrouter.ai/other", true},
		{"loopback ip", "http://127.0.0.1:9/api/v1/systemone", true},
		{"loopback v6", "http://[::1]:9/api/v1/systemone", true},
		{"localhost", "http://localhost:9/x", true},
		{"remote https", proxyURL, false},
		{"remote http", "http://openrouter.ai/api/v1/systemone", false},
		{"lookalike host", "https://openrouter.ai.example.test/x", false},
		{"userinfo trick", "https://openrouter.ai@proxy.example.test/x", false},
		{"malformed", "://", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := jev.New(jev.Hermetic(), jev.WithAPIKey(testKey), jev.WithURL(tt.url),
				jev.WithTransport(&fakeTransport{}))
			if tt.ok {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, llm.ErrKeyEndpoint)
		})
	}
}
