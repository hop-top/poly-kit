package llm_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/aim"
	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// fixtureProviders mirrors the models.dev entries kit's behavior turns
// on: the hosted schemes kit registers, a few catalog-only providers per
// protocol, the templated, local and base-URL-less shapes.
func fixtureProviders() []aim.Provider {
	oc := "@ai-sdk/openai-compatible"
	return []aim.Provider{
		{ID: "anthropic", NPM: "@ai-sdk/anthropic", Env: []string{"ANTHROPIC_API_KEY"}},
		{ID: "openai", NPM: "@ai-sdk/openai", Env: []string{"OPENAI_API_KEY"}},
		{ID: "google", NPM: "@ai-sdk/google", Env: []string{"GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY", "GEMINI_API_KEY"}},
		{ID: "openrouter", NPM: "@openrouter/ai-sdk-provider", API: "https://openrouter.ai/api/v1", Env: []string{"OPENROUTER_API_KEY"}},
		{ID: "groq", NPM: "@ai-sdk/groq", Env: []string{"GROQ_API_KEY"}},
		{ID: "xai", NPM: "@ai-sdk/xai", Env: []string{"XAI_API_KEY"}},
		{ID: "togetherai", NPM: "@ai-sdk/togetherai", Env: []string{"TOGETHER_API_KEY"}},
		{ID: "fireworks-ai", NPM: oc, API: "https://api.fireworks.ai/inference/v1/", Env: []string{"FIREWORKS_API_KEY"}},
		{ID: "deepseek", NPM: oc, API: "https://api.deepseek.com", Env: []string{"DEEPSEEK_API_KEY"}},
		{ID: "mistral", NPM: "@ai-sdk/mistral", Env: []string{"MISTRAL_API_KEY"}},
		{ID: "lmstudio", NPM: oc, API: "http://127.0.0.1:1234/v1", Env: []string{"LMSTUDIO_API_KEY"}},
		{ID: "digitalocean", NPM: oc, API: "https://inference.do-ai.run/v1", Env: []string{"DIGITALOCEAN_ACCESS_TOKEN"}},
		{ID: "meta", NPM: "@ai-sdk/openai", API: "https://api.meta.ai/v1", Env: []string{"META_MODEL_API_KEY"}},
		{ID: "standardcompute", NPM: "@openrouter/ai-sdk-provider", API: "https://api.stdcmpt.com/v1", Env: []string{"STANDARDCOMPUTE_API_KEY"}},
		{ID: "databricks", NPM: oc, API: "https://${DATABRICKS_HOST}/ai-gateway/mlflow/v1", Env: []string{"DATABRICKS_HOST", "DATABRICKS_TOKEN"}},
		{ID: "neon", NPM: oc, API: "${NEON_AI_GATEWAY_BASE_URL}/v1", Env: []string{"NEON_AI_GATEWAY_BASE_URL", "NEON_AI_GATEWAY_TOKEN"}},
		{ID: "atomic-chat", NPM: oc, API: "http://127.0.0.1:1337/v1", Env: []string{"ATOMIC_CHAT_API_KEY"}},
		{ID: "amazon-bedrock", NPM: "@ai-sdk/amazon-bedrock", Env: []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_REGION"}},
		// Hostile or broken shapes: a placeholder naming a key var, one
		// naming a var the provider does not list, and no base URL.
		{ID: "leaky", NPM: oc, API: "https://leak.example/${LEAKY_API_KEY}/v1", Env: []string{"LEAKY_API_KEY"}},
		{ID: "unlisted", NPM: oc, API: "https://${SOME_HOST}/v1", Env: []string{"UNLISTED_API_KEY"}},
		{ID: "noapi", NPM: oc, Env: []string{"NOAPI_API_KEY"}},
		{ID: "keyless", NPM: oc, API: "https://keyless.example/v1"},
	}
}

// fixtureEnvVars is every variable the fixture catalog names, cleared by
// isolateKeys alongside the hosted schemes' keys.
func fixtureEnvVars() []string {
	var out []string
	for _, p := range fixtureProviders() {
		out = append(out, p.Env...)
	}
	return append(out, "SOME_HOST", "KEYLESS_API_KEY", "MY_OR_KEY",
		"FIREWORKS_AI_API_KEY", "TOGETHERAI_API_KEY", "ACME_CLOUD_API_KEY")
}

// catalogSource serves a fixed provider map and counts fetches, so a
// test can prove key resolution and Resolve never reach the source.
type catalogSource struct {
	providers []aim.Provider
	fetches   *atomic.Int32
}

func (s catalogSource) Fetch(context.Context) (map[string]*aim.Provider, error) {
	s.fetches.Add(1)
	out := make(map[string]*aim.Provider, len(s.providers))
	for i := range s.providers {
		p := s.providers[i]
		if p.Name == "" {
			p.Name = p.ID
		}
		if p.Models == nil {
			p.Models = map[string]*aim.Model{}
		}
		out[p.ID] = &p
	}
	return out, nil
}

// useCatalog installs a default aim registry whose on-disk cache holds
// providers, the way a prior aim fetch leaves it. It returns the fetch
// counter as it stands after seeding; any later fetch is a network call
// key resolution must not make.
func useCatalog(t *testing.T, providers ...aim.Provider) *atomic.Int32 {
	t.Helper()
	fetches := &atomic.Int32{}
	reg := aim.NewRegistry(
		aim.WithSource(catalogSource{providers: providers, fetches: fetches}),
		aim.WithCacheOpts(aim.WithCacheDir(t.TempDir())),
	)
	require.NoError(t, reg.Refresh(context.Background()))
	fetches.Store(0)
	llm.SetDefaultRegistry(func(context.Context) (*aim.Registry, error) { return reg, nil })
	t.Cleanup(llm.ResetDefaultRegistry)
	return fetches
}

// useColdCatalog installs a default registry with an empty cache whose
// source counts fetches: the offline / never-fetched case.
func useColdCatalog(t *testing.T) *atomic.Int32 {
	t.Helper()
	fetches := &atomic.Int32{}
	reg := aim.NewRegistry(
		aim.WithSource(catalogSource{providers: fixtureProviders(), fetches: fetches}),
		aim.WithCacheOpts(aim.WithCacheDir(t.TempDir())),
	)
	llm.SetDefaultRegistry(func(context.Context) (*aim.Registry, error) { return reg, nil })
	t.Cleanup(llm.ResetDefaultRegistry)
	return fetches
}

// requestRecorder answers every request with a canned completion and
// records URL and Authorization, so a base URL is checked without any
// host listening there and no request leaves the process.
type requestRecorder struct {
	mu    sync.Mutex
	urls  []string
	auths []string
}

func (rr *requestRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	rr.mu.Lock()
	rr.urls = append(rr.urls, r.URL.String())
	rr.auths = append(rr.auths, r.Header.Get("Authorization"))
	rr.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"x","object":"chat.completion","created":0,"model":"m",` +
				`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`,
		)),
		Request: r,
	}, nil
}

// recordRequests routes http.DefaultClient, which the openai adapter
// uses, through a recorder for the test's duration.
func recordRequests(t *testing.T) *requestRecorder {
	t.Helper()
	rr := &requestRecorder{}
	orig := http.DefaultClient.Transport
	http.DefaultClient.Transport = rr
	t.Cleanup(func() { http.DefaultClient.Transport = orig })
	return rr
}

// complete resolves uri and sends one completion through the recorder.
func complete(t *testing.T, uri string) {
	t.Helper()
	p, err := llm.Resolve(uri)
	require.NoError(t, err)
	_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Scheme aliases
// ---------------------------------------------------------------------------

func TestResolve_AliasSchemesReachTheSameAdapter(t *testing.T) {
	cases := map[string]string{
		"fireworks-ai://accounts/fireworks/models/llama?api_key=fake": "https://api.fireworks.ai/inference/v1/chat/completions",
		"togetherai://meta-llama/Llama-3?api_key=fake":                "https://api.together.xyz/v1/chat/completions",
	}
	for _, catalog := range []bool{false, true} {
		for uri, want := range cases {
			t.Run(uri, func(t *testing.T) {
				isolateKeys(t)
				if catalog {
					useCatalog(t, fixtureProviders()...)
				}
				rr := recordRequests(t)
				complete(t, uri)
				require.Len(t, rr.urls, 1)
				assert.Equal(t, want, rr.urls[0])
			})
		}
	}
}

func TestApplyAPIKey_AliasSchemesUseTheProviderKey(t *testing.T) {
	cases := map[string]string{
		"fireworks-ai": "FIREWORKS_API_KEY",
		"togetherai":   "TOGETHER_API_KEY",
	}
	for _, catalog := range []bool{false, true} {
		for scheme, name := range cases {
			t.Run(scheme, func(t *testing.T) {
				isolateKeys(t)
				if catalog {
					useCatalog(t, fixtureProviders()...)
				}
				t.Setenv(name, "fake-"+scheme)
				got, err := llm.ApplyAPIKey(context.Background(), nil, scheme+"://m")
				require.NoError(t, err)
				assert.Equal(t, scheme+"://m?api_key=fake-"+scheme, got)

				key, ok := llm.ProviderKeyFor(scheme)
				require.True(t, ok)
				assert.Equal(t, []string{name}, key.EnvVars)
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Protocol routing: catalog-only providers
// ---------------------------------------------------------------------------

func TestResolve_CatalogProviderRoutesByProtocol(t *testing.T) {
	cases := map[string]string{
		"digitalocean":    "https://inference.do-ai.run/v1/chat/completions", // openai-compatible
		"meta":            "https://api.meta.ai/v1/chat/completions",         // @ai-sdk/openai
		"standardcompute": "https://api.stdcmpt.com/v1/chat/completions",     // openrouter
	}
	for scheme, want := range cases {
		t.Run(scheme, func(t *testing.T) {
			isolateKeys(t)
			fetches := useCatalog(t, fixtureProviders()...)
			rr := recordRequests(t)
			complete(t, scheme+"://some-model?api_key=fake-key")
			require.Len(t, rr.urls, 1)
			assert.Equal(t, want, rr.urls[0])
			assert.Equal(t, "Bearer fake-key", rr.auths[0])
			assert.Zero(t, fetches.Load(), "Resolve must not fetch the catalog")
		})
	}
}

func TestResolve_CatalogBaseURLParamWins(t *testing.T) {
	isolateKeys(t)
	useCatalog(t, fixtureProviders()...)
	rr := recordRequests(t)
	complete(t, "digitalocean://m?api_key=k&base_url=http://127.0.0.1:9/v1")
	require.Len(t, rr.urls, 1)
	assert.Equal(t, "http://127.0.0.1:9/v1/chat/completions", rr.urls[0])
}

func TestApplyAPIKey_CatalogProviderKeyVars(t *testing.T) {
	ctx := context.Background()

	t.Run("key var from facts", func(t *testing.T) {
		isolateKeys(t)
		useCatalog(t, fixtureProviders()...)
		t.Setenv("DIGITALOCEAN_ACCESS_TOKEN", "fake-do")
		t.Setenv("OPENAI_API_KEY", "fake-openai") // same adapter, wrong host
		got, err := llm.ApplyAPIKey(ctx, nil, "digitalocean://m")
		require.NoError(t, err)
		assert.Equal(t, "digitalocean://m?api_key=fake-do", got)
	})
	t.Run("missing key names the catalog var", func(t *testing.T) {
		isolateKeys(t)
		useCatalog(t, fixtureProviders()...)
		t.Setenv("OPENAI_API_KEY", "fake-openai")
		_, err := llm.ApplyAPIKey(ctx, nil, "digitalocean://m")
		var missing *llm.MissingKeyError
		require.ErrorAs(t, err, &missing)
		assert.Equal(t, []string{"DIGITALOCEAN_ACCESS_TOKEN", llm.FallbackEnvKey}, missing.EnvVars)
		assert.NotContains(t, err.Error(), "fake-openai")
	})
	t.Run("settings are not keys", func(t *testing.T) {
		isolateKeys(t)
		useCatalog(t, fixtureProviders()...)
		key, ok := llm.ProviderKeyFor("databricks")
		require.True(t, ok)
		assert.Equal(t, []string{"DATABRICKS_TOKEN"}, key.EnvVars)
	})
	t.Run("no key vars listed: convention", func(t *testing.T) {
		isolateKeys(t)
		useCatalog(t, fixtureProviders()...)
		key, ok := llm.ProviderKeyFor("keyless")
		require.True(t, ok)
		assert.Equal(t, []string{"KEYLESS_API_KEY"}, key.EnvVars)
		assert.False(t, key.Optional)
	})
}

func TestApplyAPIKey_LocalCatalogProviderKeyOptional(t *testing.T) {
	ctx := context.Background()

	t.Run("no key: untouched", func(t *testing.T) {
		isolateKeys(t)
		useCatalog(t, fixtureProviders()...)
		t.Setenv(llm.FallbackEnvKey, "fake-universal") // never lent to a local runtime
		got, err := llm.ApplyAPIKey(ctx, nil, "atomic-chat://m")
		require.NoError(t, err)
		assert.Equal(t, "atomic-chat://m", got)
		key, ok := llm.ProviderKeyFor("atomic-chat")
		require.True(t, ok)
		assert.True(t, key.Optional)
	})
	t.Run("own key: applied", func(t *testing.T) {
		isolateKeys(t)
		useCatalog(t, fixtureProviders()...)
		t.Setenv("ATOMIC_CHAT_API_KEY", "fake-atomic")
		got, err := llm.ApplyAPIKey(ctx, nil, "atomic-chat://m")
		require.NoError(t, err)
		assert.Equal(t, "atomic-chat://m?api_key=fake-atomic", got)
	})
}

// A catalog provider whose protocol no adapter serves stays unknown:
// kit cannot reach it, so it lends it no credential either.
func TestCatalogProviderWithoutAdapterStaysUnknown(t *testing.T) {
	isolateKeys(t)
	useCatalog(t, fixtureProviders()...)
	t.Setenv("AWS_SECRET_ACCESS_KEY", "fake-aws")
	t.Setenv(llm.FallbackEnvKey, "fake-universal")

	_, err := llm.Resolve("amazon-bedrock://anthropic.claude")
	var pnf *llmerrors.ErrProviderNotFound
	require.ErrorAs(t, err, &pnf)

	got, err := llm.ApplyAPIKey(context.Background(), nil, "amazon-bedrock://anthropic.claude")
	require.NoError(t, err)
	assert.Equal(t, "amazon-bedrock://anthropic.claude", got)

	_, ok := llm.ProviderKeyFor("amazon-bedrock")
	assert.False(t, ok)
}

// Without a catalog cache a catalog-only provider cannot be routed, and
// nothing is fetched to find out.
func TestCatalogProviderOffline(t *testing.T) {
	isolateKeys(t)
	fetches := useColdCatalog(t)
	t.Setenv("DIGITALOCEAN_ACCESS_TOKEN", "fake-do")

	_, err := llm.Resolve("digitalocean://m?api_key=k")
	var pnf *llmerrors.ErrProviderNotFound
	require.ErrorAs(t, err, &pnf)

	got, err := llm.ApplyAPIKey(context.Background(), nil, "digitalocean://m")
	require.NoError(t, err)
	assert.Equal(t, "digitalocean://m", got)

	// Registered schemes still resolve their keys: declarations and the
	// <SCHEME>_API_KEY convention need no catalog.
	t.Setenv("OPENROUTER_API_KEY", "fake-or")
	got, err = llm.ApplyAPIKey(context.Background(), nil, "openrouter://m")
	require.NoError(t, err)
	assert.Equal(t, "openrouter://m?api_key=fake-or", got)

	assert.Zero(t, fetches.Load(), "key resolution must not fetch the catalog")
}

// SetDefaultRegistry drops the memoised catalog, even when handed the
// same registry: its cache may have been refreshed since.
func TestSetDefaultRegistry_RereadsCatalog(t *testing.T) {
	isolateKeys(t)
	providers := fixtureProviders()
	src := &mutableSource{providers: providers}
	reg := aim.NewRegistry(aim.WithSource(src), aim.WithCacheOpts(aim.WithCacheDir(t.TempDir())))
	require.NoError(t, reg.Refresh(context.Background()))
	install := func() {
		llm.SetDefaultRegistry(func(context.Context) (*aim.Registry, error) { return reg, nil })
	}
	install()
	t.Cleanup(llm.ResetDefaultRegistry)

	key, ok := llm.ProviderKeyFor("digitalocean")
	require.True(t, ok)
	assert.Equal(t, []string{"DIGITALOCEAN_ACCESS_TOKEN"}, key.EnvVars)

	for i := range providers {
		if providers[i].ID == "digitalocean" {
			providers[i].Env = []string{"DO_TOKEN"}
		}
	}
	require.NoError(t, reg.Refresh(context.Background()))
	install()
	key, ok = llm.ProviderKeyFor("digitalocean")
	require.True(t, ok)
	assert.Equal(t, []string{"DO_TOKEN"}, key.EnvVars)
}

// mutableSource serves whatever providers holds at fetch time.
type mutableSource struct{ providers []aim.Provider }

func (s *mutableSource) Fetch(ctx context.Context) (map[string]*aim.Provider, error) {
	return catalogSource{providers: s.providers, fetches: &atomic.Int32{}}.Fetch(ctx)
}

// ---------------------------------------------------------------------------
// Templated and missing base URLs
// ---------------------------------------------------------------------------

func TestResolve_TemplatedBaseURL(t *testing.T) {
	t.Run("expanded from its setting", func(t *testing.T) {
		isolateKeys(t)
		useCatalog(t, fixtureProviders()...)
		t.Setenv("DATABRICKS_HOST", "dbc-123.cloud.databricks.com")
		rr := recordRequests(t)
		complete(t, "databricks://m?api_key=k")
		require.Len(t, rr.urls, 1)
		assert.Equal(t, "https://dbc-123.cloud.databricks.com/ai-gateway/mlflow/v1/chat/completions", rr.urls[0])
	})
	t.Run("whole base URL from a setting", func(t *testing.T) {
		isolateKeys(t)
		useCatalog(t, fixtureProviders()...)
		t.Setenv("NEON_AI_GATEWAY_BASE_URL", "https://gw.neon.example")
		rr := recordRequests(t)
		complete(t, "neon://m?api_key=k")
		require.Len(t, rr.urls, 1)
		assert.Equal(t, "https://gw.neon.example/v1/chat/completions", rr.urls[0])
	})
	t.Run("setting unset: error names it, nothing sent", func(t *testing.T) {
		isolateKeys(t)
		useCatalog(t, fixtureProviders()...)
		rr := recordRequests(t)
		_, err := llm.Resolve("databricks://m?api_key=k")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "needs DATABRICKS_HOST set")
		assert.Contains(t, err.Error(), "base_url")
		assert.Empty(t, rr.urls)
	})
	t.Run("base_url param skips the template", func(t *testing.T) {
		isolateKeys(t)
		useCatalog(t, fixtureProviders()...)
		rr := recordRequests(t)
		complete(t, "databricks://m?api_key=k&base_url=http://127.0.0.1:9/v1")
		require.Len(t, rr.urls, 1)
		assert.Equal(t, "http://127.0.0.1:9/v1/chat/completions", rr.urls[0])
	})
	t.Run("placeholder naming a key var is refused", func(t *testing.T) {
		isolateKeys(t)
		useCatalog(t, fixtureProviders()...)
		t.Setenv("LEAKY_API_KEY", "fake-leaked-secret")
		rr := recordRequests(t)
		_, err := llm.Resolve("leaky://m?api_key=k")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "fake-leaked-secret")
		assert.Empty(t, rr.urls)
	})
	t.Run("placeholder naming an unlisted var is refused", func(t *testing.T) {
		isolateKeys(t)
		useCatalog(t, fixtureProviders()...)
		t.Setenv("SOME_HOST", "attacker.example")
		rr := recordRequests(t)
		_, err := llm.Resolve("unlisted://m?api_key=k")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SOME_HOST")
		assert.Empty(t, rr.urls)
	})
}

// A catalog provider with no base URL must not fall back to OpenAI's:
// that would send its key to a host that is not the provider.
func TestResolve_CatalogProviderWithoutBaseURL(t *testing.T) {
	isolateKeys(t)
	useCatalog(t, fixtureProviders()...)
	rr := recordRequests(t)
	_, err := llm.Resolve("noapi://m?api_key=k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base_url")
	assert.Empty(t, rr.urls)
}

// ---------------------------------------------------------------------------
// Layering: declaration > facts > convention
// ---------------------------------------------------------------------------

// Every hosted scheme keeps its current key whether the catalog is
// cached or not: facts and convention agree on today's table.
func TestProviderKeyFor_TableWithCatalog(t *testing.T) {
	isolateKeys(t)
	useCatalog(t, fixtureProviders()...)
	for scheme, names := range keyedSchemes {
		got, ok := llm.ProviderKeyFor(scheme)
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
}

// Facts outrank the convention for a scheme with no declaration.
func TestProviderKeyFor_FactsOutrankConvention(t *testing.T) {
	isolateKeys(t)
	providers := fixtureProviders()
	for i := range providers {
		if providers[i].ID == "mistral" {
			providers[i].Env = []string{"MISTRAL_API_KEY", "CODESTRAL_API_KEY"}
		}
	}
	useCatalog(t, providers...)
	got, ok := llm.ProviderKeyFor("mistral")
	require.True(t, ok)
	assert.Equal(t, []string{"MISTRAL_API_KEY", "CODESTRAL_API_KEY"}, got.EnvVars)
}

// A registered scheme with neither declaration nor facts takes the
// <SCHEME>_API_KEY convention, dashes and dots folded to underscores.
func TestProviderKeyFor_Convention(t *testing.T) {
	isolateKeys(t)
	registerAcme()
	got, ok := llm.ProviderKeyFor("acme-cloud://m")
	require.True(t, ok)
	assert.Equal(t, []string{"ACME_CLOUD_API_KEY"}, got.EnvVars)
	assert.False(t, got.Optional)
}

var acmeOnce sync.Once

func registerAcme() {
	acmeOnce.Do(func() {
		llm.Register("acme-cloud", func(llm.ResolvedConfig) (llm.Provider, error) {
			return &mockCompleter{}, nil
		})
	})
}

// ---------------------------------------------------------------------------
// llm.yaml: providers.<scheme>.api_key / api_key_env
// ---------------------------------------------------------------------------

func writeLLMConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	require.NoError(t, os.MkdirAll(dir+"/hop", 0o750))
	require.NoError(t, os.WriteFile(dir+"/hop/llm.yaml", []byte(body), 0o600))
}

func TestApplyAPIKey_ConfigFile(t *testing.T) {
	ctx := context.Background()

	t.Run("api_key outranks the env var", func(t *testing.T) {
		isolateKeys(t)
		writeLLMConfig(t, "providers:\n  openrouter:\n    api_key: fake-file\n")
		t.Setenv("OPENROUTER_API_KEY", "fake-env")
		got, err := llm.ApplyAPIKey(ctx, nil, "openrouter://m")
		require.NoError(t, err)
		assert.Equal(t, "openrouter://m?api_key=fake-file", got)

		secretGot, err := llm.SecretFor(ctx, nil, "openrouter")
		require.NoError(t, err)
		assert.Equal(t, "fake-file", secretGot)
	})
	t.Run("api_key belongs to its own scheme", func(t *testing.T) {
		isolateKeys(t)
		writeLLMConfig(t, "providers:\n  openrouter:\n    api_key: fake-file\n")
		_, err := llm.ApplyAPIKey(ctx, nil, "groq://m")
		assert.ErrorIs(t, err, llm.ErrMissingKey)
	})
	t.Run("api_key_env names the variable", func(t *testing.T) {
		isolateKeys(t)
		writeLLMConfig(t, "providers:\n  openrouter:\n    api_key_env: MY_OR_KEY\n")
		t.Setenv("MY_OR_KEY", "fake-mine")
		t.Setenv("OPENROUTER_API_KEY", "fake-env")
		got, err := llm.ApplyAPIKey(ctx, nil, "openrouter://m")
		require.NoError(t, err)
		assert.Equal(t, "openrouter://m?api_key=fake-mine", got)
		assert.Equal(t, "MY_OR_KEY", llm.EnvKeyFor("openrouter"))
	})
	t.Run("api_key_env unset: provider vars still apply", func(t *testing.T) {
		isolateKeys(t)
		writeLLMConfig(t, "providers:\n  openrouter:\n    api_key_env: MY_OR_KEY\n")
		t.Setenv("OPENROUTER_API_KEY", "fake-env")
		got, err := llm.ApplyAPIKey(ctx, nil, "openrouter://m")
		require.NoError(t, err)
		assert.Equal(t, "openrouter://m?api_key=fake-env", got)
	})
	t.Run("missing key lists api_key_env first", func(t *testing.T) {
		isolateKeys(t)
		writeLLMConfig(t, "providers:\n  openrouter:\n    api_key_env: MY_OR_KEY\n")
		_, err := llm.ApplyAPIKey(ctx, nil, "openrouter://m")
		var missing *llm.MissingKeyError
		require.ErrorAs(t, err, &missing)
		assert.Equal(t, []string{"MY_OR_KEY", "OPENROUTER_API_KEY", llm.FallbackEnvKey}, missing.EnvVars)
		key, ok := llm.ProviderKeyFor("openrouter")
		require.True(t, ok)
		assert.Equal(t, []string{"MY_OR_KEY", "OPENROUTER_API_KEY"}, key.EnvVars)
	})
	t.Run("URI api_key still outranks the file", func(t *testing.T) {
		isolateKeys(t)
		writeLLMConfig(t, "providers:\n  openrouter:\n    api_key: fake-file\n")
		got, err := llm.ApplyAPIKey(ctx, nil, "openrouter://m?api_key=fake-uri")
		require.NoError(t, err)
		assert.Equal(t, "openrouter://m?api_key=fake-uri", got)
	})
}

func TestLoadConfig_APIKeyEnv(t *testing.T) {
	isolateKeys(t)
	writeLLMConfig(t, "providers:\n  openrouter:\n    api_key_env: MY_OR_KEY\n")
	t.Setenv("MY_OR_KEY", "fake-mine")
	cfg, err := llm.LoadConfig("openrouter://m")
	require.NoError(t, err)
	assert.Equal(t, "fake-mine", cfg.Provider.APIKey)
	assert.NotContains(t, cfg.Provider.Extras, "api_key_env")
}
