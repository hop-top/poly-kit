package llm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ParseURI tests ---

func TestParseURI_Basic(t *testing.T) {
	uri, err := ParseURI("openai://gpt-4o")
	require.NoError(t, err)
	assert.Equal(t, "openai", uri.Scheme)
	assert.Equal(t, "gpt-4o", uri.Model)
	assert.Empty(t, uri.Host)
	assert.Empty(t, uri.Params)
}

func TestParseURI_WithParams(t *testing.T) {
	uri, err := ParseURI("ollama://llama3?temperature=0.7&top_p=0.9")
	require.NoError(t, err)
	assert.Equal(t, "ollama", uri.Scheme)
	assert.Equal(t, "llama3", uri.Model)
	assert.Equal(t, "0.7", uri.Params["temperature"])
	assert.Equal(t, "0.9", uri.Params["top_p"])
}

func TestParseURI_WithHostPort(t *testing.T) {
	uri, err := ParseURI("lmstudio://localhost:1234/my-model")
	require.NoError(t, err)
	assert.Equal(t, "lmstudio", uri.Scheme)
	assert.Equal(t, "localhost:1234", uri.Host)
	assert.Equal(t, "my-model", uri.Model)
}

func TestParseURI_SlashesInModel(t *testing.T) {
	uri, err := ParseURI("openrouter://meta-llama/llama-3-70b")
	require.NoError(t, err)
	assert.Equal(t, "openrouter", uri.Scheme)
	assert.Equal(t, "meta-llama/llama-3-70b", uri.Model)
}

func TestParseURI_EmptyModel(t *testing.T) {
	uri, err := ParseURI("ollama://")
	require.NoError(t, err)
	assert.Equal(t, "ollama", uri.Scheme)
	assert.Empty(t, uri.Model)
}

func TestParseURI_MissingScheme(t *testing.T) {
	_, err := ParseURI("gpt-4o")
	assert.Error(t, err)
}

func TestParseURI_EmptyString(t *testing.T) {
	_, err := ParseURI("")
	assert.Error(t, err)
}

func TestParseURI_InvalidScheme(t *testing.T) {
	_, err := ParseURI("://gpt-4o")
	assert.Error(t, err)
}

func TestParseURI_Anthropic(t *testing.T) {
	uri, err := ParseURI("anthropic://claude-sonnet-4-20250514")
	require.NoError(t, err)
	assert.Equal(t, "anthropic", uri.Scheme)
	assert.Equal(t, "claude-sonnet-4-20250514", uri.Model)
}

// --- LoadConfig tests ---

func TestLoadConfig_NoConfigFile_EnvOnly(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	// no config file exists

	cfg, err := LoadConfig("openai://gpt-4o")
	require.NoError(t, err)
	assert.Equal(t, "openai", cfg.URI.Scheme)
	assert.Equal(t, "gpt-4o", cfg.URI.Model)
}

func TestLoadConfig_WithConfigFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	hopDir := filepath.Join(tmp, "hop")
	require.NoError(t, os.MkdirAll(hopDir, 0o750))

	configYAML := `default: openai://gpt-4o
providers:
  openai:
    api_key: sk-test-key
    base_url: https://api.openai.com/v1
  ollama:
    base_url: http://localhost:11434
fallback:
  - ollama://llama3
`
	require.NoError(t, os.WriteFile(
		filepath.Join(hopDir, "llm.yaml"), []byte(configYAML), 0o644,
	))

	cfg, err := LoadConfig("openai://gpt-4o")
	require.NoError(t, err)
	assert.Equal(t, "openai", cfg.URI.Scheme)
	assert.Equal(t, "gpt-4o", cfg.URI.Model)
	assert.Equal(t, "sk-test-key", cfg.Provider.APIKey)
	assert.Equal(t, "https://api.openai.com/v1", cfg.Provider.BaseURL)
	assert.Equal(t, []string{"ollama://llama3"}, cfg.Fallbacks)
}

func TestLoadConfig_EnvOverridesConfigFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	hopDir := filepath.Join(tmp, "hop")
	require.NoError(t, os.MkdirAll(hopDir, 0o750))

	configYAML := `providers:
  openai:
    api_key: sk-from-config
    base_url: https://api.openai.com/v1
`
	require.NoError(t, os.WriteFile(
		filepath.Join(hopDir, "llm.yaml"), []byte(configYAML), 0o644,
	))

	t.Setenv("LLM_API_KEY", "sk-from-env")
	t.Setenv("LLM_BASE_URL", "https://custom.api.com/v1")

	cfg, err := LoadConfig("openai://gpt-4o")
	require.NoError(t, err)
	// env overrides config file
	assert.Equal(t, "sk-from-env", cfg.Provider.APIKey)
	assert.Equal(t, "https://custom.api.com/v1", cfg.Provider.BaseURL)
}

func TestLoadConfig_URIOverridesConfigFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	hopDir := filepath.Join(tmp, "hop")
	require.NoError(t, os.MkdirAll(hopDir, 0o750))

	configYAML := `providers:
  openai:
    api_key: sk-config
    base_url: https://api.openai.com/v1
    model: gpt-3.5-turbo
`
	require.NoError(t, os.WriteFile(
		filepath.Join(hopDir, "llm.yaml"), []byte(configYAML), 0o644,
	))

	cfg, err := LoadConfig("openai://gpt-4o")
	require.NoError(t, err)
	// URI model overrides config model
	assert.Equal(t, "gpt-4o", cfg.Provider.Model)
	// config values still used
	assert.Equal(t, "sk-config", cfg.Provider.APIKey)
}

func TestLoadConfig_MergeOrder(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	hopDir := filepath.Join(tmp, "hop")
	require.NoError(t, os.MkdirAll(hopDir, 0o750))

	// config file has base_url
	configYAML := `providers:
  openai:
    api_key: sk-config
    base_url: https://config.api.com
`
	require.NoError(t, os.WriteFile(
		filepath.Join(hopDir, "llm.yaml"), []byte(configYAML), 0o644,
	))

	// env overrides api_key
	t.Setenv("LLM_API_KEY", "sk-env")

	cfg, err := LoadConfig("openai://gpt-4o?base_url=https://uri.api.com")
	require.NoError(t, err)
	// env > URI > config: api_key from env
	assert.Equal(t, "sk-env", cfg.Provider.APIKey)
	// URI param overrides config base_url
	assert.Equal(t, "https://uri.api.com", cfg.Provider.BaseURL)
}

func TestLoadConfig_LLMProviderDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("LLM_PROVIDER", "anthropic://claude-sonnet-4-20250514")

	// empty URI uses LLM_PROVIDER
	cfg, err := LoadConfig("")
	require.NoError(t, err)
	assert.Equal(t, "anthropic", cfg.URI.Scheme)
	assert.Equal(t, "claude-sonnet-4-20250514", cfg.URI.Model)
}

func TestLoadConfig_LLMFallback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("LLM_FALLBACK", "ollama://llama3,openai://gpt-3.5-turbo")

	cfg, err := LoadConfig("openai://gpt-4o")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"ollama://llama3",
		"openai://gpt-3.5-turbo",
	}, cfg.Fallbacks)
}

func TestLoadConfig_LLMFallbackOverridesConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	hopDir := filepath.Join(tmp, "hop")
	require.NoError(t, os.MkdirAll(hopDir, 0o750))

	configYAML := `fallback:
  - ollama://llama3
`
	require.NoError(t, os.WriteFile(
		filepath.Join(hopDir, "llm.yaml"), []byte(configYAML), 0o644,
	))

	t.Setenv("LLM_FALLBACK", "openai://gpt-3.5-turbo")

	cfg, err := LoadConfig("openai://gpt-4o")
	require.NoError(t, err)
	// env overrides config fallbacks
	assert.Equal(t, []string{"openai://gpt-3.5-turbo"}, cfg.Fallbacks)
}

func TestLoadConfig_DefaultFromConfigFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	hopDir := filepath.Join(tmp, "hop")
	require.NoError(t, os.MkdirAll(hopDir, 0o750))

	configYAML := `default: anthropic://claude-sonnet-4-20250514
providers:
  anthropic:
    api_key: sk-ant-test
`
	require.NoError(t, os.WriteFile(
		filepath.Join(hopDir, "llm.yaml"), []byte(configYAML), 0o644,
	))

	cfg, err := LoadConfig("")
	require.NoError(t, err)
	assert.Equal(t, "anthropic", cfg.URI.Scheme)
	assert.Equal(t, "sk-ant-test", cfg.Provider.APIKey)
}

func TestLoadConfig_NoURINoDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	_, err := LoadConfig("")
	assert.Error(t, err)
}

func TestLoadConfig_ProviderExtras(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	hopDir := filepath.Join(tmp, "hop")
	require.NoError(t, os.MkdirAll(hopDir, 0o750))

	configYAML := `providers:
  openai:
    api_key: sk-test
    organization: org-123
    project: proj-456
`
	require.NoError(t, os.WriteFile(
		filepath.Join(hopDir, "llm.yaml"), []byte(configYAML), 0o644,
	))

	cfg, err := LoadConfig("openai://gpt-4o")
	require.NoError(t, err)
	assert.Equal(t, "org-123", cfg.Provider.Extras["organization"])
	assert.Equal(t, "proj-456", cfg.Provider.Extras["project"])
}

func TestParseURI_HostPortWithSlashModel(t *testing.T) {
	uri, err := ParseURI("lmstudio://localhost:1234/org/model-name")
	require.NoError(t, err)
	assert.Equal(t, "lmstudio", uri.Scheme)
	assert.Equal(t, "localhost:1234", uri.Host)
	assert.Equal(t, "org/model-name", uri.Model)
}
