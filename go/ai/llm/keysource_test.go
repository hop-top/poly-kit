package llm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/memory"
)

// ResolveAPIKey reports where the key ApplyAPIKey would attach came
// from, for every source in the chain.
func TestResolveAPIKey_Sources(t *testing.T) {
	cases := []struct {
		name   string
		yaml   string
		env    map[string]string
		stored map[string]string
		uri    string
		value  string
		source llm.KeySource
	}{
		{
			name:   "URI param",
			yaml:   "providers:\n  openrouter:\n    api_key: fake-file\n",
			env:    map[string]string{"OPENROUTER_API_KEY": "fake-env"},
			uri:    "openrouter://m?api_key=fake-uri",
			value:  "fake-uri",
			source: llm.KeySource{Kind: llm.KeySourceURI},
		},
		{
			name:   "llm.yaml api_key",
			yaml:   "providers:\n  openrouter:\n    api_key: fake-file\n",
			env:    map[string]string{"OPENROUTER_API_KEY": "fake-env"},
			uri:    "openrouter://m",
			value:  "fake-file",
			source: llm.KeySource{Kind: llm.KeySourceConfig, Name: "providers.openrouter.api_key"},
		},
		{
			name:   "llm.yaml api_key from an alias's block",
			yaml:   "providers:\n  fireworks:\n    api_key: fake-file\n",
			uri:    "fireworks-ai://m",
			value:  "fake-file",
			source: llm.KeySource{Kind: llm.KeySourceConfig, Name: "providers.fireworks.api_key"},
		},
		{
			name:   "api_key_env variable",
			yaml:   "providers:\n  openrouter:\n    api_key_env: MY_OR_KEY\n",
			env:    map[string]string{"MY_OR_KEY": "fake-mine", "OPENROUTER_API_KEY": "fake-env"},
			uri:    "openrouter://m",
			value:  "fake-mine",
			source: llm.KeySource{Kind: llm.KeySourceEnv, Name: "MY_OR_KEY"},
		},
		{
			name:   "secret store",
			env:    map[string]string{"OPENROUTER_API_KEY": "fake-env"},
			stored: map[string]string{"OPENROUTER_API_KEY": "fake-store"},
			uri:    "openrouter://m",
			value:  "fake-store",
			source: llm.KeySource{Kind: llm.KeySourceStore, Name: "OPENROUTER_API_KEY"},
		},
		{
			name:   "provider variable, second name",
			env:    map[string]string{"GEMINI_API_KEY": "fake-gemini"},
			uri:    "google://m",
			value:  "fake-gemini",
			source: llm.KeySource{Kind: llm.KeySourceEnv, Name: "GEMINI_API_KEY"},
		},
		{
			name:   "LLM_API_KEY",
			env:    map[string]string{llm.FallbackEnvKey: "fake-universal"},
			uri:    "openrouter://m",
			value:  "fake-universal",
			source: llm.KeySource{Kind: llm.KeySourceFallback, Name: llm.FallbackEnvKey},
		},
		{
			name: "local runtime without its key",
			env:  map[string]string{llm.FallbackEnvKey: "fake-universal"},
			uri:  "ollama://llama3",
		},
		{
			name: "scheme taking no key",
			env:  map[string]string{llm.FallbackEnvKey: "fake-universal"},
			uri:  "lmstudio://m",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateKeys(t)
			ctx := context.Background()
			if tc.yaml != "" {
				writeLLMConfig(t, tc.yaml)
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			store := memory.New()
			for k, v := range tc.stored {
				require.NoError(t, store.Set(ctx, k, []byte(v)))
			}

			res, err := llm.ResolveAPIKey(ctx, store, tc.uri)
			require.NoError(t, err)
			assert.Equal(t, tc.value, res.Value)
			assert.Equal(t, tc.source, res.Source)
			assert.Equal(t, tc.value != "", res.Found())
			assert.True(t, res.Known)

			// ApplyAPIKey attaches exactly that key.
			applied, err := llm.ApplyAPIKey(ctx, store, tc.uri)
			require.NoError(t, err)
			if tc.source.Kind == llm.KeySourceNone || tc.source.Kind == llm.KeySourceURI {
				assert.Equal(t, tc.uri, applied)
			} else {
				assert.Equal(t, tc.uri+"?api_key="+tc.value, applied)
			}
		})
	}
}

func TestResolveAPIKey_DescribesTheScheme(t *testing.T) {
	isolateKeys(t)
	res, err := llm.ResolveAPIKey(context.Background(), nil, "google://m")
	var missing *llm.MissingKeyError
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, "google", res.Scheme)
	assert.True(t, res.Known)
	assert.Equal(t, []string{"GOOGLE_API_KEY", "GEMINI_API_KEY"}, res.Key.EnvVars)
	assert.False(t, res.Found())
	assert.Equal(t, []string{"GOOGLE_API_KEY", "GEMINI_API_KEY", llm.FallbackEnvKey}, missing.EnvVars)

	res, err = llm.ResolveAPIKey(context.Background(), nil, "ollama://m")
	require.NoError(t, err)
	assert.True(t, res.Key.Optional)
}

func TestResolveAPIKey_UnknownScheme(t *testing.T) {
	isolateKeys(t)
	t.Setenv(llm.FallbackEnvKey, "fake-universal")
	res, err := llm.ResolveAPIKey(context.Background(), nil, "no-such-scheme://m")
	require.NoError(t, err)
	assert.False(t, res.Known)
	assert.False(t, res.Found())
	assert.Empty(t, res.Value)
}

// Store failures come back in StoreErr, found or not, and are not
// logged: the caller decides how to surface them.
func TestResolveAPIKey_StoreErr(t *testing.T) {
	isolateKeys(t)
	logs := captureLogs(t)
	t.Setenv("OPENAI_API_KEY", "fake-env")
	res, err := llm.ResolveAPIKey(context.Background(), failingStore{}, "openai://m")
	require.NoError(t, err)
	assert.Equal(t, llm.KeySource{Kind: llm.KeySourceEnv, Name: "OPENAI_API_KEY"}, res.Source)
	require.ErrorIs(t, res.StoreErr, errBackend)
	assert.Contains(t, res.StoreErr.Error(), "OPENAI_API_KEY")
	assert.Empty(t, logs.String())
}

func TestResolveAPIKey_NeverPrintsTheKey(t *testing.T) {
	isolateKeys(t)
	t.Setenv("OPENAI_API_KEY", "fake-secret-value")
	res, err := llm.ResolveAPIKey(context.Background(), nil, "openai://m")
	require.NoError(t, err)
	require.Equal(t, "fake-secret-value", res.Value)

	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		out := fmt.Sprintf(verb, res)
		assert.NotContains(t, out, "fake-secret-value", verb)
		assert.Contains(t, out, "env:OPENAI_API_KEY", verb)
	}
	b, err := json.Marshal(res)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "fake-secret-value")
}

func TestResolveAPIKey_BadURINeverEchoesIt(t *testing.T) {
	_, err := llm.ResolveAPIKey(context.Background(), nil, "gpt-4.1?api_key=fake-secret")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "fake-secret")
	assert.NotErrorIs(t, err, secret.ErrNotFound)
}

func ExampleResolveAPIKey() {
	ctx := context.Background()
	store := memory.New()
	_ = store.Set(ctx, "OPENROUTER_API_KEY", []byte("sk-or-example"))

	res, err := llm.ResolveAPIKey(ctx, store, "openrouter://openai/gpt-4.1-nano")
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(res.Source) // never res.Value
	// Output: store:OPENROUTER_API_KEY
}
