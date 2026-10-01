package llm_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	_ "hop.top/kit/go/ai/llm/anthropic"
	_ "hop.top/kit/go/ai/llm/google"
	_ "hop.top/kit/go/ai/llm/ollama"
	_ "hop.top/kit/go/ai/llm/openai"
	_ "hop.top/kit/go/ai/llm/routellm"
	_ "hop.top/kit/go/ai/llm/triton"
	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/memory"
)

// keyedSchemes is every hosted scheme with the variable its provider
// documents, highest precedence first.
var keyedSchemes = map[string][]string{
	"openai":     {"OPENAI_API_KEY"},
	"anthropic":  {"ANTHROPIC_API_KEY"},
	"google":     {"GOOGLE_API_KEY", "GEMINI_API_KEY"},
	"gemini":     {"GOOGLE_API_KEY", "GEMINI_API_KEY"},
	"openrouter": {"OPENROUTER_API_KEY"},
	"groq":       {"GROQ_API_KEY"},
	"xai":        {"XAI_API_KEY"},
	"together":   {"TOGETHER_API_KEY"},
	"fireworks":  {"FIREWORKS_API_KEY"},
	"deepseek":   {"DEEPSEEK_API_KEY"},
	"mistral":    {"MISTRAL_API_KEY"},
}

// localSchemes take a key when one is set and never require one.
var localSchemes = map[string][]string{
	"ollama":   {"OLLAMA_API_KEY"},
	"lmstudio": nil,
	"routellm": {"ROUTELLM_API_KEY"},
	"triton":   {"TRITON_API_KEY"},
}

// isolateKeys gives the test a throwaway HOME/XDG and removes every
// provider key variable, so a developer's real environment can neither
// satisfy nor poison an assertion.
func isolateKeys(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // no aim catalog unless a test installs one
	llm.ResetDefaultRegistry()
	t.Cleanup(llm.ResetDefaultRegistry)
	vars := []string{llm.FallbackEnvKey, "LLM_BASE_URL", "OPENAI_BASE_URL"}
	vars = append(vars, fixtureEnvVars()...)
	for _, names := range keyedSchemes {
		vars = append(vars, names...)
	}
	for _, names := range localSchemes {
		vars = append(vars, names...)
	}
	for _, v := range vars {
		t.Setenv(v, "")
		require.NoError(t, os.Unsetenv(v))
	}
}

func TestEnvKeyFor_HostedGatewaysUseOwnVariable(t *testing.T) {
	for scheme, names := range keyedSchemes {
		if scheme == "google" || scheme == "gemini" {
			continue // compat name, see TestEnvKeyFor_GoogleKeepsGeminiName
		}
		assert.Equal(t, names[0], llm.EnvKeyFor(scheme+"://m"), scheme)
	}
}

// EnvKeyFor predates the multi-name google entry; its single answer
// stays GEMINI_API_KEY, which still works.
func TestEnvKeyFor_GoogleKeepsGeminiName(t *testing.T) {
	assert.Equal(t, "GEMINI_API_KEY", llm.EnvKeyFor("google://gemini-2.5-flash"))
	assert.Equal(t, "GEMINI_API_KEY", llm.EnvKeyFor("gemini"))
	assert.Equal(t, llm.FallbackEnvKey, llm.EnvKeyFor("lmstudio://qwen"))
}

func TestProviderKeyFor_Table(t *testing.T) {
	isolateKeys(t)
	for scheme, names := range keyedSchemes {
		got, ok := llm.ProviderKeyFor(scheme + "://vendor/model?x=1")
		require.True(t, ok, scheme)
		assert.Equal(t, names, got.EnvVars, scheme)
		assert.False(t, got.Optional, scheme)
	}
	for scheme, names := range localSchemes {
		got, ok := llm.ProviderKeyFor(scheme)
		require.True(t, ok, scheme)
		assert.Equal(t, names, got.EnvVars, scheme)
		assert.True(t, got.Optional, scheme)
	}
	_, ok := llm.ProviderKeyFor("no-such-scheme://x")
	assert.False(t, ok)
}

// Every scheme an adapter registers gets a deliberate entry; a new
// adapter without one would reach its provider unauthenticated.
func TestProviderKeyFor_CoversEveryRegisteredScheme(t *testing.T) {
	isolateKeys(t)
	for _, scheme := range llm.Schemes() {
		_, ok := llm.ProviderKeyFor(scheme)
		assert.True(t, ok, "registered scheme %q has no provider key entry", scheme)
	}
}

func TestProviderKeyFor_ReturnsCopy(t *testing.T) {
	got, _ := llm.ProviderKeyFor("google")
	got.EnvVars[0] = "MUTATED"
	again, _ := llm.ProviderKeyFor("google")
	assert.Equal(t, "GOOGLE_API_KEY", again.EnvVars[0])
}

func TestSecretFor_GooglePrecedence(t *testing.T) {
	ctx := context.Background()

	t.Run("GOOGLE_API_KEY alone", func(t *testing.T) {
		isolateKeys(t)
		t.Setenv("GOOGLE_API_KEY", "fake-google")
		got, err := llm.SecretFor(ctx, nil, "google://gemini-2.5-flash")
		require.NoError(t, err)
		assert.Equal(t, "fake-google", got)
	})
	t.Run("GEMINI_API_KEY alone", func(t *testing.T) {
		isolateKeys(t)
		t.Setenv("GEMINI_API_KEY", "fake-gemini")
		got, err := llm.SecretFor(ctx, nil, "gemini://gemini-2.5-flash")
		require.NoError(t, err)
		assert.Equal(t, "fake-gemini", got)
	})
	t.Run("both set: GOOGLE_API_KEY wins", func(t *testing.T) {
		isolateKeys(t)
		t.Setenv("GOOGLE_API_KEY", "fake-google")
		t.Setenv("GEMINI_API_KEY", "fake-gemini")
		got, err := llm.SecretFor(ctx, nil, "google://gemini-2.5-flash")
		require.NoError(t, err)
		assert.Equal(t, "fake-google", got)
	})
	t.Run("store under either name beats env", func(t *testing.T) {
		isolateKeys(t)
		t.Setenv("GOOGLE_API_KEY", "fake-env")
		store := memory.New()
		require.NoError(t, store.Set(ctx, "GEMINI_API_KEY", []byte("fake-store")))
		got, err := llm.SecretFor(ctx, store, "google://gemini-2.5-flash")
		require.NoError(t, err)
		assert.Equal(t, "fake-store", got)
	})
}

func TestApplyAPIKey_InjectsSchemeKey(t *testing.T) {
	for scheme, names := range keyedSchemes {
		t.Run(scheme, func(t *testing.T) {
			isolateKeys(t)
			t.Setenv(names[0], "fake-"+scheme)
			if names[0] != "OPENAI_API_KEY" {
				t.Setenv("OPENAI_API_KEY", "fake-openai") // present, wrong host
			}
			got, err := llm.ApplyAPIKey(context.Background(), nil, scheme+"://vendor/some-model")
			require.NoError(t, err)
			parsed, err := llm.ParseURI(got)
			require.NoError(t, err)
			assert.Equal(t, scheme, parsed.Scheme)
			assert.Equal(t, "vendor/some-model", parsed.Model)
			assert.Equal(t, "fake-"+scheme, parsed.Params["api_key"])
		})
	}
}

func TestApplyAPIKey_AppendsToExistingQuery(t *testing.T) {
	isolateKeys(t)
	t.Setenv("OPENROUTER_API_KEY", "fake-or")
	const base = "http://127.0.0.1:1/v1"
	got, err := llm.ApplyAPIKey(context.Background(), nil, "openrouter://openai/gpt-4.1-nano?base_url="+base)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(got, "?"), got)
	parsed, err := llm.ParseURI(got)
	require.NoError(t, err)
	assert.Equal(t, base, parsed.Params["base_url"])
	assert.Equal(t, "fake-or", parsed.Params["api_key"])
}

func TestApplyAPIKey_ExplicitKeyUntouched(t *testing.T) {
	isolateKeys(t)
	t.Setenv("OPENROUTER_API_KEY", "fake-env")
	const uri = "openrouter://openai/gpt-4.1-nano?api_key=fake-explicit&base_url=http://127.0.0.1:1/v1"
	got, err := llm.ApplyAPIKey(context.Background(), nil, uri)
	require.NoError(t, err)
	assert.Equal(t, uri, got)
}

func TestApplyAPIKey_StoreAndFallback(t *testing.T) {
	ctx := context.Background()

	t.Run("store wins over env", func(t *testing.T) {
		isolateKeys(t)
		t.Setenv("GROQ_API_KEY", "fake-env")
		store := memory.New()
		require.NoError(t, store.Set(ctx, "GROQ_API_KEY", []byte("fake-store")))
		got, err := llm.ApplyAPIKey(ctx, store, "groq://llama-3.3-70b")
		require.NoError(t, err)
		assert.Equal(t, "groq://llama-3.3-70b?api_key=fake-store", got)
	})
	t.Run("universal LLM_API_KEY for a keyed scheme", func(t *testing.T) {
		isolateKeys(t)
		t.Setenv(llm.FallbackEnvKey, "fake-universal")
		got, err := llm.ApplyAPIKey(ctx, nil, "mistral://mistral-small")
		require.NoError(t, err)
		assert.Equal(t, "mistral://mistral-small?api_key=fake-universal", got)
	})
}

func TestApplyAPIKey_LocalSchemes(t *testing.T) {
	ctx := context.Background()
	uris := []string{
		"ollama://llama3.2",
		"lmstudio://qwen2.5-7b?base_url=http://127.0.0.1:1234/v1",
		"routellm://mf:0.5",
		"triton://127.0.0.1:8000/model",
	}

	t.Run("no key: untouched, no error", func(t *testing.T) {
		isolateKeys(t)
		t.Setenv(llm.FallbackEnvKey, "fake-universal") // never lent to a local runtime
		for _, uri := range uris {
			got, err := llm.ApplyAPIKey(ctx, nil, uri)
			require.NoError(t, err, uri)
			assert.Equal(t, uri, got)
		}
	})
	t.Run("own key set: applied", func(t *testing.T) {
		isolateKeys(t)
		t.Setenv("ROUTELLM_API_KEY", "fake-routellm")
		got, err := llm.ApplyAPIKey(ctx, nil, "routellm://mf:0.5")
		require.NoError(t, err)
		assert.Equal(t, "routellm://mf:0.5?api_key=fake-routellm", got)
	})
}

// A scheme kit has no entry for keeps its URI: kit cannot know which
// credential that host takes, so it lends none.
func TestApplyAPIKey_UnknownSchemeUntouched(t *testing.T) {
	isolateKeys(t)
	t.Setenv(llm.FallbackEnvKey, "fake-universal")
	t.Setenv("OPENAI_API_KEY", "fake-openai")
	got, err := llm.ApplyAPIKey(context.Background(), nil, "no-such-scheme://m")
	require.NoError(t, err)
	assert.Equal(t, "no-such-scheme://m", got)
}

func TestApplyAPIKey_MissingKey(t *testing.T) {
	isolateKeys(t)
	t.Setenv("OPENAI_API_KEY", "fake-openai") // must not satisfy openrouter

	_, err := llm.ApplyAPIKey(context.Background(), nil, "openrouter://openai/gpt-4.1-nano?base_url=http://127.0.0.1:1/v1")
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrMissingKey)
	assert.ErrorIs(t, err, secret.ErrNotFound)

	var missing *llm.MissingKeyError
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, "openrouter", missing.Scheme)
	assert.Equal(t, "openai/gpt-4.1-nano", missing.Model)
	assert.Equal(t, []string{"OPENROUTER_API_KEY", llm.FallbackEnvKey}, missing.EnvVars)
	assert.Contains(t, err.Error(), "OPENROUTER_API_KEY")
	assert.NotContains(t, err.Error(), "OPENAI_API_KEY")
	assert.NotContains(t, err.Error(), "fake-openai")
}

func TestApplyAPIKey_GoogleMissingNamesBothVariables(t *testing.T) {
	isolateKeys(t)
	_, err := llm.ApplyAPIKey(context.Background(), nil, "gemini://gemini-2.5-flash")
	var missing *llm.MissingKeyError
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, []string{"GOOGLE_API_KEY", "GEMINI_API_KEY", llm.FallbackEnvKey}, missing.EnvVars)
}

type failingStore struct{}

var errBackend = errors.New("backend unreachable")

func (failingStore) Get(context.Context, string) (*secret.Secret, error) { return nil, errBackend }
func (failingStore) List(context.Context, string) ([]string, error)      { return nil, errBackend }
func (failingStore) Exists(context.Context, string) (bool, error)        { return false, errBackend }

// partlyFailingStore fails for the names in fail and serves the rest
// from inner.
type partlyFailingStore struct {
	inner secret.Store
	fail  map[string]bool
}

func (s partlyFailingStore) Get(ctx context.Context, k string) (*secret.Secret, error) {
	if s.fail[k] {
		return nil, errBackend
	}
	return s.inner.Get(ctx, k)
}

func (s partlyFailingStore) List(ctx context.Context, p string) ([]string, error) {
	return s.inner.List(ctx, p)
}

func (s partlyFailingStore) Exists(ctx context.Context, k string) (bool, error) {
	if s.fail[k] {
		return false, errBackend
	}
	return s.inner.Exists(ctx, k)
}

// captureLogs routes slog.Default to a buffer for the test's duration.
func captureLogs(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(orig) })
	return &buf
}

// A secret-store backend failure is not a missing key and does not end
// the search: the next name, the environment and LLM_API_KEY still
// answer. The failure is surfaced, never silent: logged as a warning
// when a later source supplies the key, carried by the error when none
// does.
func TestApplyAPIKey_StoreErrorFallsThrough(t *testing.T) {
	ctx := context.Background()

	t.Run("provider env var answers", func(t *testing.T) {
		isolateKeys(t)
		logs := captureLogs(t)
		t.Setenv("OPENAI_API_KEY", "fake-env")
		got, err := llm.ApplyAPIKey(ctx, failingStore{}, "openai://gpt-4.1-nano")
		require.NoError(t, err)
		assert.Equal(t, "openai://gpt-4.1-nano?api_key=fake-env", got)
		assert.Contains(t, logs.String(), "level=WARN")
		assert.Contains(t, logs.String(), "scheme=openai")
		assert.Contains(t, logs.String(), "OPENAI_API_KEY: backend unreachable")
		assert.NotContains(t, logs.String(), "fake-env")
	})
	t.Run("LLM_API_KEY answers", func(t *testing.T) {
		isolateKeys(t)
		captureLogs(t)
		t.Setenv(llm.FallbackEnvKey, "fake-universal")
		got, err := llm.ApplyAPIKey(ctx, failingStore{}, "openai://m")
		require.NoError(t, err)
		assert.Equal(t, "openai://m?api_key=fake-universal", got)
	})
	t.Run("next store name answers", func(t *testing.T) {
		isolateKeys(t)
		captureLogs(t)
		mem := memory.New()
		require.NoError(t, mem.Set(ctx, "GEMINI_API_KEY", []byte("fake-gemini")))
		store := partlyFailingStore{inner: mem, fail: map[string]bool{"GOOGLE_API_KEY": true}}
		got, err := llm.ApplyAPIKey(ctx, store, "google://m")
		require.NoError(t, err)
		assert.Equal(t, "google://m?api_key=fake-gemini", got)
	})
	t.Run("nothing answers: missing key carrying the store error", func(t *testing.T) {
		isolateKeys(t)
		logs := captureLogs(t)
		_, err := llm.ApplyAPIKey(ctx, failingStore{}, "openai://gpt-4.1-nano")
		require.ErrorIs(t, err, llm.ErrMissingKey)
		assert.ErrorIs(t, err, errBackend)
		var missing *llm.MissingKeyError
		require.ErrorAs(t, err, &missing)
		require.ErrorIs(t, missing.StoreErr, errBackend)
		assert.Equal(t, []string{"OPENAI_API_KEY", llm.FallbackEnvKey}, missing.EnvVars)
		assert.Contains(t, err.Error(), "set OPENAI_API_KEY or LLM_API_KEY")
		assert.Contains(t, err.Error(), "OPENAI_API_KEY: backend unreachable")
		assert.Empty(t, logs.String(), "returned, not also logged")
	})
	t.Run("no store error: message unchanged", func(t *testing.T) {
		isolateKeys(t)
		_, err := llm.ApplyAPIKey(ctx, memory.New(), "openai://m")
		var missing *llm.MissingKeyError
		require.ErrorAs(t, err, &missing)
		assert.NoError(t, missing.StoreErr)
		assert.Equal(t, `llm: no API key for provider "openai" (model "m"): set OPENAI_API_KEY or LLM_API_KEY`, err.Error())
	})
	t.Run("local runtime: logged, uri untouched", func(t *testing.T) {
		isolateKeys(t)
		logs := captureLogs(t)
		got, err := llm.ApplyAPIKey(ctx, failingStore{}, "ollama://llama3")
		require.NoError(t, err)
		assert.Equal(t, "ollama://llama3", got)
		assert.Contains(t, logs.String(), "OLLAMA_API_KEY: backend unreachable")
	})
}

func TestSecretFor_StoreErrorFallsThrough(t *testing.T) {
	ctx := context.Background()

	t.Run("env answers", func(t *testing.T) {
		isolateKeys(t)
		logs := captureLogs(t)
		t.Setenv("OPENAI_API_KEY", "fake-env")
		got, err := llm.SecretFor(ctx, failingStore{}, "openai")
		require.NoError(t, err)
		assert.Equal(t, "fake-env", got)
		assert.Contains(t, logs.String(), "OPENAI_API_KEY: backend unreachable")
	})
	t.Run("nothing answers", func(t *testing.T) {
		isolateKeys(t)
		_, err := llm.SecretFor(ctx, failingStore{}, "openai")
		require.ErrorIs(t, err, secret.ErrNotFound)
		assert.ErrorIs(t, err, errBackend)
	})
}

func TestApplyAPIKey_ErrorsNeverCarryKeys(t *testing.T) {
	isolateKeys(t)
	ctx := context.Background()

	_, err := llm.ApplyAPIKey(ctx, nil, "gpt-4.1-nano?api_key=fake-secret-in-uri")
	require.Error(t, err, "a URI without a scheme is rejected")
	assert.NotErrorIs(t, err, llm.ErrMissingKey)
	assert.NotContains(t, err.Error(), "fake-secret-in-uri")

	t.Setenv("OPENAI_API_KEY", "fake&secret")
	_, err = llm.ApplyAPIKey(ctx, nil, "openai://gpt-4.1-nano")
	require.Error(t, err, "a key that would split the query is rejected")
	assert.NotContains(t, err.Error(), "fake&secret")
}

// End to end: a URI-form model reaches the provider with the scheme's
// key once ApplyAPIKey has run.
func TestApplyAPIKey_ReachesProviderAuthenticated(t *testing.T) {
	isolateKeys(t)
	t.Setenv("OPENROUTER_API_KEY", "fake-or")
	t.Setenv("OPENAI_API_KEY", "fake-openai")

	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		http.Error(w, "stop", http.StatusTeapot)
	}))
	defer srv.Close()

	uri, err := llm.ApplyAPIKey(context.Background(), nil, "openrouter://openai/gpt-4.1-nano?base_url="+srv.URL)
	require.NoError(t, err)
	p, err := llm.Resolve(uri)
	require.NoError(t, err)
	_, _ = p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	assert.Equal(t, "Bearer fake-or", auth)
}

func ExampleApplyAPIKey() {
	ctx := context.Background()
	store := memory.New()
	_ = store.Set(ctx, "OPENROUTER_API_KEY", []byte("sk-or-example"))

	uri, err := llm.ApplyAPIKey(ctx, store, "openrouter://openai/gpt-4.1-nano")
	var missing *llm.MissingKeyError
	if errors.As(err, &missing) {
		fmt.Println("set", missing.EnvVars[0]) // the app words its own message
		return
	}
	fmt.Println(uri)
	// Output: openrouter://openai/gpt-4.1-nano?api_key=sk-or-example
}
