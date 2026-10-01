package llm_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/storage/secret"
)

// LoadConfig's key is ApplyAPIKey's: one precedence, highest first —
// URI api_key, llm.yaml api_key / api_key_env, the provider's
// variables, LLM_API_KEY for a required key only.
func TestLoadConfig_KeyPrecedenceMatchesApplyAPIKey(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		env  map[string]string
		uri  string
		want string
	}{
		{
			name: "llm.yaml api_key outranks LLM_API_KEY",
			yaml: "providers:\n  openrouter:\n    api_key: fake-file\n",
			env:  map[string]string{llm.FallbackEnvKey: "fake-universal"},
			uri:  "openrouter://m",
			want: "fake-file",
		},
		{
			name: "llm.yaml api_key outranks the provider variable",
			yaml: "providers:\n  openrouter:\n    api_key: fake-file\n",
			env:  map[string]string{"OPENROUTER_API_KEY": "fake-env"},
			uri:  "openrouter://m",
			want: "fake-file",
		},
		{
			name: "URI api_key outranks LLM_API_KEY",
			env:  map[string]string{llm.FallbackEnvKey: "fake-universal"},
			uri:  "openrouter://m?api_key=fake-uri",
			want: "fake-uri",
		},
		{
			name: "provider variable outranks LLM_API_KEY",
			env:  map[string]string{"OPENROUTER_API_KEY": "fake-env", llm.FallbackEnvKey: "fake-universal"},
			uri:  "openrouter://m",
			want: "fake-env",
		},
		{
			name: "api_key_env outranks the provider variable",
			yaml: "providers:\n  openrouter:\n    api_key_env: MY_OR_KEY\n",
			env:  map[string]string{"MY_OR_KEY": "fake-mine", "OPENROUTER_API_KEY": "fake-env"},
			uri:  "openrouter://m",
			want: "fake-mine",
		},
		{
			name: "declared order (google)",
			env:  map[string]string{"GOOGLE_API_KEY": "fake-google", "GEMINI_API_KEY": "fake-gemini"},
			uri:  "gemini://m",
			want: "fake-google",
		},
		{
			name: "LLM_API_KEY for a required key",
			env:  map[string]string{llm.FallbackEnvKey: "fake-universal"},
			uri:  "openrouter://m",
			want: "fake-universal",
		},
		{
			name: "LLM_API_KEY never lent to a local runtime",
			env:  map[string]string{llm.FallbackEnvKey: "fake-universal"},
			uri:  "ollama://llama3",
		},
		{
			name: "local runtime's own variable",
			env:  map[string]string{"OLLAMA_API_KEY": "fake-ollama", llm.FallbackEnvKey: "fake-universal"},
			uri:  "ollama://llama3",
			want: "fake-ollama",
		},
		{
			name: "LLM_API_KEY never lent to an unknown scheme",
			env:  map[string]string{llm.FallbackEnvKey: "fake-universal"},
			uri:  "no-such-scheme://m",
		},
		{
			name: "an unknown scheme's own block",
			yaml: "providers:\n  no-such-scheme:\n    api_key: fake-file\n",
			env:  map[string]string{llm.FallbackEnvKey: "fake-universal"},
			uri:  "no-such-scheme://m",
			want: "fake-file",
		},
		{
			name: "an unknown scheme's own api_key_env",
			yaml: "providers:\n  no-such-scheme:\n    api_key_env: MY_OR_KEY\n",
			env:  map[string]string{"MY_OR_KEY": "fake-mine"},
			uri:  "no-such-scheme://m",
			want: "fake-mine",
		},
		{
			name: "an alias's block",
			yaml: "providers:\n  fireworks:\n    api_key: fake-file\n",
			env:  map[string]string{llm.FallbackEnvKey: "fake-universal"},
			uri:  "fireworks-ai://m",
			want: "fake-file",
		},
		{
			name: "nothing set",
			uri:  "openrouter://m",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateKeys(t)
			if tc.yaml != "" {
				writeLLMConfig(t, tc.yaml)
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			cfg, err := llm.LoadConfig(tc.uri)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.Provider.APIKey, "LoadConfig")

			res, _ := llm.ResolveAPIKey(context.Background(), nil, tc.uri)
			assert.Equal(t, tc.want, res.Value, "ResolveAPIKey")

			// SecretFor ignores the URI's own api_key.
			if !strings.Contains(tc.uri, "api_key=") {
				sec, err := llm.SecretFor(context.Background(), nil, tc.uri)
				assert.Equal(t, tc.want, sec, "SecretFor")
				if tc.want == "" {
					assert.ErrorIs(t, err, secret.ErrNotFound)
				}
			}

			applied, err := llm.ApplyAPIKey(context.Background(), nil, tc.uri)
			if tc.want == "" {
				if err == nil {
					assert.Equal(t, tc.uri, applied, "ApplyAPIKey")
				} else {
					assert.ErrorIs(t, err, llm.ErrMissingKey)
				}
				return
			}
			require.NoError(t, err)
			p, err := llm.ParseURI(applied)
			require.NoError(t, err)
			assert.Equal(t, tc.want, p.Params["api_key"], "ApplyAPIKey")
		})
	}
}

// configProbe is a registered scheme whose factory records the config
// Resolve hands it.
var (
	configProbeOnce sync.Once
	configProbeMu   sync.Mutex
	configProbeLast llm.ResolvedConfig
)

func registerConfigProbe() {
	configProbeOnce.Do(func() {
		llm.Register("cfgprobe", func(cfg llm.ResolvedConfig) (llm.Provider, error) {
			configProbeMu.Lock()
			configProbeLast = cfg
			configProbeMu.Unlock()
			return &mockCompleter{}, nil
		})
	})
}

// Base URL, highest first: the URI's base_url param, the URI's host,
// LLM_BASE_URL, llm.yaml. Where the URI names one, LoadConfig and
// Resolve agree on it, whatever the environment and file say.
func TestLoadConfig_BaseURLPrecedence(t *testing.T) {
	registerConfigProbe()
	const file = "providers:\n  cfgprobe:\n    base_url: http://127.0.0.1:9/file\n"
	cases := []struct {
		name    string
		yaml    string
		envBase string
		uri     string
		want    string
		resolve bool // Resolve gives the same base URL
	}{
		{"base_url param beats LLM_BASE_URL and file", file, "http://127.0.0.1:9/env",
			"cfgprobe://m?base_url=http://127.0.0.1:9/param", "http://127.0.0.1:9/param", true},
		{"base_url param beats host", file, "",
			"cfgprobe://127.0.0.1:7/m?base_url=http://127.0.0.1:9/param", "http://127.0.0.1:9/param", true},
		{"host beats LLM_BASE_URL and file", file, "http://127.0.0.1:9/env",
			"cfgprobe://127.0.0.1:7/m", "http://127.0.0.1:7", true},
		{"host beats file", file, "",
			"cfgprobe://127.0.0.1:7/m", "http://127.0.0.1:7", true},
		{"LLM_BASE_URL beats file", file, "http://127.0.0.1:9/env",
			"cfgprobe://m", "http://127.0.0.1:9/env", false},
		{"file alone", file, "", "cfgprobe://m", "http://127.0.0.1:9/file", false},
		{"nothing", "", "", "cfgprobe://m", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateKeys(t)
			if tc.yaml != "" {
				writeLLMConfig(t, tc.yaml)
			}
			if tc.envBase != "" {
				t.Setenv("LLM_BASE_URL", tc.envBase)
			}
			cfg, err := llm.LoadConfig(tc.uri)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.Provider.BaseURL, "LoadConfig")

			if tc.resolve {
				_, err := llm.Resolve(tc.uri)
				require.NoError(t, err)
				configProbeMu.Lock()
				got := configProbeLast.Provider.BaseURL
				configProbeMu.Unlock()
				assert.Equal(t, tc.want, got, "Resolve")
			}
		})
	}
}
