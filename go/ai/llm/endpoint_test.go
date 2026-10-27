package llm_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

const guardKey = "fake-guard-key-must-not-leak"

var registerKeyGuard sync.Once

// keyguard is a scheme with a declared default endpoint and one more
// declared endpoint; its key is KEYGUARD_API_KEY by convention.
func keyGuardScheme(t *testing.T) {
	t.Helper()
	registerKeyGuard.Do(func() {
		llm.Register("keyguard", func(llm.ResolvedConfig) (llm.Provider, error) {
			return &mockProvider{}, nil
		}, llm.Declaration{
			BaseURL:   "https://api.keyguard.test/v1",
			Endpoints: []string{"https://alt.keyguard.test"},
		})
	})
}

// A key resolved from llm.yaml, a store or the environment goes only to
// the provider's own endpoints, ones the user configured, or loopback;
// never to a host the URI alone names, and never over plain http to a
// remote host.
func TestApplyAPIKey_KeyGoesOnlyToTrustedEndpoints(t *testing.T) {
	keyGuardScheme(t)
	cases := []struct {
		name    string
		yaml    string
		envBase string
		uri     string
		refused bool
	}{
		{name: "no endpoint in the URI", uri: "keyguard://m"},
		{name: "declared default", uri: "keyguard://m?base_url=https://api.keyguard.test/v1"},
		{name: "declared default, other path, case and default port", uri: "keyguard://m?base_url=https://API.keyguard.test:443/other"},
		{name: "declared endpoint", uri: "keyguard://m?base_url=https://alt.keyguard.test/v2"},
		{name: "llm.yaml base_url", yaml: "providers:\n  keyguard:\n    base_url: https://proxy.corp.test/v1\n", uri: "keyguard://m?base_url=https://proxy.corp.test/v1"},
		{name: "LLM_BASE_URL", envBase: "https://gw.corp.test", uri: "keyguard://m?base_url=https://gw.corp.test/v1"},
		{name: "loopback http base_url", uri: "keyguard://m?base_url=http://127.0.0.1:1234/v1"},
		{name: "loopback host form", uri: "keyguard://localhost:1234/m"},
		{name: "hostile base_url", uri: "keyguard://m?base_url=https://evil.example/v1", refused: true},
		{name: "hostile host form", uri: "keyguard://evil.example:8443/m", refused: true},
		{name: "look-alike host", uri: "keyguard://m?base_url=https://api.keyguard.test.evil.example", refused: true},
		{name: "default host over plain http", uri: "keyguard://m?base_url=http://api.keyguard.test/v1", refused: true},
		{name: "configured endpoint over plain http", yaml: "providers:\n  keyguard:\n    base_url: http://proxy.corp.test\n", uri: "keyguard://m?base_url=http://proxy.corp.test", refused: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateKeys(t)
			keyGuardScheme(t)
			if tc.yaml != "" {
				writeLLMConfig(t, tc.yaml)
			}
			if tc.envBase != "" {
				t.Setenv("LLM_BASE_URL", tc.envBase)
			}
			t.Setenv("KEYGUARD_API_KEY", guardKey)

			got, err := llm.ApplyAPIKey(context.Background(), nil, tc.uri)
			if tc.refused {
				require.Error(t, err)
				assert.True(t, errors.Is(err, llm.ErrKeyEndpoint), "%v", err)
				var ke *llm.KeyEndpointError
				require.ErrorAs(t, err, &ke)
				assert.Equal(t, "keyguard", ke.Scheme)
				assert.NotContains(t, err.Error(), guardKey)
				assert.Empty(t, got)

				_, err = llm.LoadConfig(tc.uri)
				assert.ErrorIs(t, err, llm.ErrKeyEndpoint, "LoadConfig refuses alike")
				return
			}
			require.NoError(t, err)
			assert.Contains(t, got, "api_key="+guardKey)
			cfg, err := llm.LoadConfig(tc.uri)
			require.NoError(t, err)
			assert.Equal(t, guardKey, cfg.Provider.APIKey)
		})
	}
}

// A key in the URI itself is the caller's choice for any https host;
// plain http still needs a loopback host.
func TestInlineKey_CallersChoice(t *testing.T) {
	isolateKeys(t)
	keyGuardScheme(t)
	t.Setenv("KEYGUARD_API_KEY", guardKey)

	uri := "keyguard://m?api_key=fake-inline&base_url=https://elsewhere.example"
	got, err := llm.ApplyAPIKey(context.Background(), nil, uri)
	require.NoError(t, err)
	assert.Equal(t, uri, got)
	_, err = llm.Resolve(uri)
	require.NoError(t, err)

	for _, ok := range []string{
		"keyguard://m?api_key=fake-inline&base_url=http://127.0.0.1:9/v1",
		"keyguard://127.0.0.1:9/m?api_key=fake-inline",
		"keyguard://m?base_url=http://elsewhere.example", // no key: nothing to protect
	} {
		_, err := llm.Resolve(ok)
		require.NoError(t, err, ok)
	}

	_, err = llm.Resolve("keyguard://m?api_key=fake-inline&base_url=http://elsewhere.example")
	require.ErrorIs(t, err, llm.ErrKeyEndpoint)
	assert.NotContains(t, err.Error(), "fake-inline")
}
