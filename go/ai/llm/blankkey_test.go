package llm_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/memory"
)

// A blank key is no key. Every source kit reads a key from (the URI's
// api_key param, llm.yaml api_key, the variable api_key_env names, the
// secret store, the provider variable, LLM_API_KEY) treats a value that
// is empty or only whitespace as unset: no surface reports it, none
// sends it, and resolution moves on to the next source.

// blankKeyStates are the states a key source can be in; present marks
// the one a key resolves from.
var blankKeyStates = []struct {
	name    string
	unset   bool
	value   string
	present bool
}{
	{name: "unset", unset: true},
	{name: "empty", value: ""},
	{name: "whitespace", value: " \t "},
	{name: "set", value: "fake-key", present: true},
}

// blankKeySource places a key state for openai://m on one source. It
// returns the URI to resolve and the store to resolve it with.
type blankKeySource struct {
	name string
	// place applies value; unset leaves the source empty.
	place func(t *testing.T, store secret.Store, unset bool, value string) (uri string)
	// source is where a present key reports it came from.
	source llm.KeySource
	// viaURI: the key travels on the URI already; ApplyAPIKey returns
	// it unchanged and SecretFor, which ignores the URI, cannot see it.
	viaURI bool
	// viaStore: LoadConfig, which reads no store, cannot see it.
	viaStore bool
}

const blankKeyBase = "openai://m"

var blankKeySources = []blankKeySource{
	{
		name: "URI api_key param",
		place: func(_ *testing.T, _ secret.Store, unset bool, v string) string {
			if unset {
				return blankKeyBase
			}
			return blankKeyBase + "?api_key=" + v
		},
		source: llm.KeySource{Kind: llm.KeySourceURI},
		viaURI: true,
	},
	{
		name: "llm.yaml api_key",
		place: func(t *testing.T, _ secret.Store, unset bool, v string) string {
			if !unset {
				writeLLMConfig(t, "providers:\n  openai:\n    api_key: \""+v+"\"\n")
			}
			return blankKeyBase
		},
		source: llm.KeySource{Kind: llm.KeySourceConfig, Name: "providers.openai.api_key"},
	},
	{
		name: "llm.yaml api_key_env",
		place: func(t *testing.T, _ secret.Store, unset bool, v string) string {
			writeLLMConfig(t, "providers:\n  openai:\n    api_key_env: MY_BLANK_TEST_KEY\n")
			setOrUnset(t, "MY_BLANK_TEST_KEY", unset, v)
			return blankKeyBase
		},
		source: llm.KeySource{Kind: llm.KeySourceEnv, Name: "MY_BLANK_TEST_KEY"},
	},
	{
		name: "secret store",
		place: func(t *testing.T, store secret.Store, unset bool, v string) string {
			if !unset {
				require.NoError(t, store.(*memory.Store).Set(context.Background(), "OPENAI_API_KEY", []byte(v)))
			}
			return blankKeyBase
		},
		source:   llm.KeySource{Kind: llm.KeySourceStore, Name: "OPENAI_API_KEY"},
		viaStore: true,
	},
	{
		name: "provider variable",
		place: func(t *testing.T, _ secret.Store, unset bool, v string) string {
			setOrUnset(t, "OPENAI_API_KEY", unset, v)
			return blankKeyBase
		},
		source: llm.KeySource{Kind: llm.KeySourceEnv, Name: "OPENAI_API_KEY"},
	},
	{
		name: "LLM_API_KEY",
		place: func(t *testing.T, _ secret.Store, unset bool, v string) string {
			setOrUnset(t, llm.FallbackEnvKey, unset, v)
			return blankKeyBase
		},
		source: llm.KeySource{Kind: llm.KeySourceFallback, Name: llm.FallbackEnvKey},
	},
}

func setOrUnset(t *testing.T, name string, unset bool, v string) {
	t.Helper()
	t.Setenv(name, v)
	if unset {
		require.NoError(t, os.Unsetenv(name))
	}
}

// ApplyAPIKey, ResolveAPIKey, LoadConfig and SecretFor agree, for every
// source in every state, on whether a key is present.
func TestBlankKey_SurfacesAgree(t *testing.T) {
	ctx := context.Background()
	for _, src := range blankKeySources {
		for _, st := range blankKeyStates {
			t.Run(src.name+"/"+st.name, func(t *testing.T) {
				isolateKeys(t)
				store := memory.New()
				uri := src.place(t, store, st.unset, st.value)

				// ResolveAPIKey
				res, err := llm.ResolveAPIKey(ctx, store, uri)
				if st.present {
					require.NoError(t, err)
					assert.Equal(t, st.value, res.Value)
					assert.Equal(t, src.source, res.Source)
					assert.True(t, res.Found())
				} else {
					var missing *llm.MissingKeyError
					require.ErrorAs(t, err, &missing)
					assert.Empty(t, res.Value)
					assert.Equal(t, llm.KeySource{}, res.Source, "a blank key has no source")
					assert.False(t, res.Found())
				}

				// ApplyAPIKey
				applied, err := llm.ApplyAPIKey(ctx, store, uri)
				switch {
				case !st.present:
					assert.ErrorIs(t, err, llm.ErrMissingKey)
					assert.Empty(t, applied)
				case src.viaURI:
					require.NoError(t, err)
					assert.Equal(t, uri, applied)
				default:
					require.NoError(t, err)
					assert.Equal(t, blankKeyBase+"?api_key="+st.value, applied)
				}

				// LoadConfig: no store; its params never hold a blank key.
				cfg, err := llm.LoadConfig(uri)
				require.NoError(t, err)
				if st.present && !src.viaStore {
					assert.Equal(t, st.value, cfg.Provider.APIKey)
				} else {
					assert.Empty(t, cfg.Provider.APIKey)
				}
				if !st.present {
					assert.NotContains(t, cfg.Provider.Params, "api_key")
					assert.NotContains(t, cfg.URI.Params, "api_key")
				}

				// SecretFor ignores the URI's own param.
				got, err := llm.SecretFor(ctx, store, uri)
				if st.present && !src.viaURI {
					require.NoError(t, err)
					assert.Equal(t, st.value, got)
				} else {
					assert.ErrorIs(t, err, secret.ErrNotFound)
					assert.Empty(t, got)
				}
			})
		}
	}
}

// A blank key at a higher source does not shadow a key further down:
// resolution goes on as if the blank source were unset.
func TestBlankKey_FallsThrough(t *testing.T) {
	ctx := context.Background()
	for _, src := range blankKeySources {
		if src.source.Kind == llm.KeySourceFallback {
			continue // the last source: nothing below it
		}
		for _, st := range blankKeyStates[1:3] { // empty, whitespace
			t.Run(src.name+"/"+st.name, func(t *testing.T) {
				isolateKeys(t)
				store := memory.New()
				uri := src.place(t, store, false, st.value)
				t.Setenv(llm.FallbackEnvKey, "fake-lower")

				res, err := llm.ResolveAPIKey(ctx, store, uri)
				require.NoError(t, err)
				assert.Equal(t, "fake-lower", res.Value)
				assert.Equal(t, llm.KeySource{Kind: llm.KeySourceFallback, Name: llm.FallbackEnvKey}, res.Source)

				applied, err := llm.ApplyAPIKey(ctx, store, uri)
				require.NoError(t, err)
				assert.Equal(t, blankKeyBase+"?api_key=fake-lower", applied)

				cfg, err := llm.LoadConfig(uri)
				require.NoError(t, err)
				assert.Equal(t, "fake-lower", cfg.Provider.APIKey)

				got, err := llm.SecretFor(ctx, store, uri)
				require.NoError(t, err)
				assert.Equal(t, "fake-lower", got)
			})
		}
	}
}

// ApplyAPIKey drops a blank api_key param, in every spelling, before it
// puts the resolved key on the URI: the URI handed on carries one
// api_key, the real one, and its other params as given.
func TestApplyAPIKey_DropsBlankURIParam(t *testing.T) {
	cases := map[string]string{
		"openai://m?api_key=":                   "openai://m?api_key=fake-env",
		"openai://m?api_key":                    "openai://m?api_key=fake-env",
		"openai://m?api_key=  ":                 "openai://m?api_key=fake-env",
		"openai://m?api_key=&api_key=":          "openai://m?api_key=fake-env",
		"openai://m?temperature=1&api_key=&x=2": "openai://m?temperature=1&x=2&api_key=fake-env",
		"openai://m?api_key=&base_url=http://h": "openai://m?base_url=http://h&api_key=fake-env",
		"openai://localhost:8080/m?api_key=":    "openai://localhost:8080/m?api_key=fake-env",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			isolateKeys(t)
			t.Setenv("OPENAI_API_KEY", "fake-env")
			got, err := llm.ApplyAPIKey(context.Background(), nil, in)
			require.NoError(t, err)
			assert.Equal(t, want, got)
			assert.Equal(t, 1, strings.Count(got, "api_key="))
		})
	}

	// A local runtime with no key of its own gets the URI back without
	// the blank param rather than with it.
	t.Run("local runtime, no key", func(t *testing.T) {
		isolateKeys(t)
		got, err := llm.ApplyAPIKey(context.Background(), nil, "ollama://llama3?api_key=&x=1")
		require.NoError(t, err)
		assert.Equal(t, "ollama://llama3?x=1", got)
	})

	// The request reaches the provider with the resolved key only.
	t.Run("request carries the resolved key", func(t *testing.T) {
		isolateKeys(t)
		t.Setenv("OPENAI_API_KEY", "fake-env")
		uri, err := llm.ApplyAPIKey(context.Background(), nil, "openai://m?api_key=&base_url=http://blank.invalid/v1")
		require.NoError(t, err)
		rr := recordRequests(t)
		complete(t, uri)
		require.Len(t, rr.auths, 1)
		assert.Equal(t, "Bearer fake-env", rr.auths[0])
	})

	// A missing key is reported before any request is attempted.
	t.Run("missing key, no request", func(t *testing.T) {
		isolateKeys(t)
		rr := recordRequests(t)
		_, err := llm.ApplyAPIKey(context.Background(), nil, "openai://m?api_key=")
		var missing *llm.MissingKeyError
		require.ErrorAs(t, err, &missing)
		assert.Equal(t, []string{"OPENAI_API_KEY", llm.FallbackEnvKey}, missing.EnvVars)
		assert.Empty(t, rr.urls)
	})
}

// Resolve, which reads the URI alone, never sends a blank key: no
// Authorization header at all rather than "Bearer " with nothing after
// it, and no provider-specific variable picked up in its place.
func TestResolve_BlankURIKeySendsNoCredential(t *testing.T) {
	for _, uri := range []string{
		"openai://m?base_url=http://blank.invalid/v1&api_key=",
		"openai://m?base_url=http://blank.invalid/v1&api_key=  ",
		"openai://m?base_url=http://blank.invalid/v1",
	} {
		t.Run(uri, func(t *testing.T) {
			isolateKeys(t)
			t.Setenv("OPENAI_API_KEY", "fake-sdk-env") // the SDK's own default
			rr := &headerRecorder{}
			withTransport(t, rr)
			complete(t, uri)
			require.Len(t, rr.auth, 1)
			assert.Nil(t, rr.auth[0], "no Authorization header")
		})
	}
}

// Resolve hands an adapter no blank api_key param.
func TestResolve_BlankURIKeyNotInParams(t *testing.T) {
	reg := llm.NewRegistry()
	var got llm.ResolvedConfig
	reg.Register("blankfake", func(cfg llm.ResolvedConfig) (llm.Provider, error) {
		got = cfg
		return &mockProvider{}, nil
	}, llm.Declaration{BaseURL: "http://blank.invalid"})

	for _, uri := range []string{"blankfake://m?api_key=&x=1", "blankfake://m?api_key= &x=1", "blankfake://m?api_key&x=1"} {
		_, err := reg.Resolve(uri)
		require.NoError(t, err, uri)
		assert.Empty(t, got.Provider.APIKey, uri)
		assert.NotContains(t, got.Provider.Params, "api_key", uri)
		assert.NotContains(t, got.URI.Params, "api_key", uri)
		assert.Equal(t, "1", got.Provider.Params["x"], uri)
	}

	_, err := reg.Resolve("blankfake://m?api_key=fake-uri")
	require.NoError(t, err)
	assert.Equal(t, "fake-uri", got.Provider.APIKey)
}

// A blank api_key_env names no variable.
func TestBlankKey_APIKeyEnvName(t *testing.T) {
	isolateKeys(t)
	writeLLMConfig(t, "providers:\n  openai:\n    api_key_env: \"  \"\n")
	assert.Equal(t, "OPENAI_API_KEY", llm.EnvKeyFor("openai"))
	key, ok := llm.ProviderKeyFor("openai")
	require.True(t, ok)
	assert.Equal(t, []string{"OPENAI_API_KEY"}, key.EnvVars)
}

// headerRecorder is a requestRecorder that also tells an absent
// Authorization header (nil) from an empty one.
type headerRecorder struct {
	requestRecorder
	auth [][]string
}

func (hr *headerRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	hr.mu.Lock()
	hr.auth = append(hr.auth, r.Header.Values("Authorization"))
	hr.mu.Unlock()
	return hr.requestRecorder.RoundTrip(r)
}

// withTransport routes http.DefaultClient through rt for the test.
func withTransport(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	orig := http.DefaultClient.Transport
	http.DefaultClient.Transport = rt
	t.Cleanup(func() { http.DefaultClient.Transport = orig })
}
