package llm_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/aim"
	"hop.top/kit/go/ai/llm"
)

func TestEndpoint(t *testing.T) {
	useCatalog(t, fixtureProviders()...)
	cases := []struct {
		name, uri, want string
	}{
		{"ollama default", "ollama://llama3", "http://localhost:11434"},
		{"lmstudio default", "lmstudio://m", "http://localhost:1234/v1"},
		{"anthropic declared", "anthropic://claude", "https://api.anthropic.com"},
		{"openrouter declared", "openrouter://x/y", "https://openrouter.ai/api/v1"},
		{"host form", "ollama://gpu-box:11434/llama3", "http://gpu-box:11434"},
		{"base_url", "ollama://llama3?base_url=http://box:9", "http://box:9"},
		{"base_url outranks host", "openai://localhost:8000/m?base_url=https://api.openai.com/v1", "https://api.openai.com/v1"},
		{"host outranks declared", "lmstudio://box:1/m", "http://box:1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := llm.Endpoint(tc.uri)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			// Same answer hermetically: nothing ambient decides it.
			h, err := llm.Endpoint(tc.uri, llm.Hermetic())
			require.NoError(t, err)
			assert.Equal(t, tc.want, h)
		})
	}

	t.Run("adapter picks its own default", func(t *testing.T) {
		_, err := llm.Endpoint("openai://gpt-4o")
		assert.ErrorIs(t, err, llm.ErrNoEndpoint)
	})
	t.Run("unknown scheme", func(t *testing.T) {
		_, err := llm.Endpoint("nope://m")
		assert.Error(t, err)
	})
	t.Run("bad URI is redacted", func(t *testing.T) {
		_, err := llm.Endpoint("no-scheme?api_key=sk-secret")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "sk-secret")
	})
	t.Run("catalog base URL", func(t *testing.T) {
		got, err := llm.Endpoint("digitalocean://m")
		require.NoError(t, err)
		assert.Equal(t, "https://inference.do-ai.run/v1", got)
	})
}

func TestEndpointMatchesResolve(t *testing.T) {
	var got []llm.ResolvedConfig
	r := hermeticRegistry(&got)
	for _, uri := range []string{"fake://box:7/m", "fake://m?base_url=http://x:1/v1"} {
		ep, err := r.Endpoint(uri, llm.Hermetic())
		require.NoError(t, err)
		got = nil
		_, err = r.Resolve(uri, llm.Hermetic())
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, got[0].Provider.BaseURL, ep, uri)
	}
}

func TestEndpointLocality(t *testing.T) {
	cases := []struct {
		url  string
		want llm.Locality
	}{
		{"http://127.0.0.1:11434", llm.LocalityLoopback},
		{"http://127.0.0.2", llm.LocalityLoopback},
		{"http://[::1]:8000/v1", llm.LocalityLoopback},
		{"http://localhost:1234/v1", llm.LocalityLoopback},
		{"http://LOCALHOST", llm.LocalityLoopback},
		{"http://localhost.:80", llm.LocalityLoopback},
		{"http://10.1.2.3:8000", llm.LocalityPrivate},
		{"http://172.16.0.1", llm.LocalityPrivate},
		{"http://172.32.0.1", llm.LocalityRemote},
		{"http://192.168.1.9", llm.LocalityPrivate},
		{"http://[fd12::1]", llm.LocalityPrivate},
		{"http://box.local:1", llm.LocalityPrivate},
		{"http://100.64.0.1", llm.LocalityRemote}, // CGNAT / tailnet
		{"http://169.254.169.254", llm.LocalityRemote},
		{"http://0.0.0.0:8000", llm.LocalityRemote},
		{"http://8.8.8.8", llm.LocalityRemote},
		{"https://api.openai.com/v1", llm.LocalityRemote},
		{"http://gpu-box:1234", llm.LocalityRemote}, // a name is never resolved
		{"http://127.0.0.1.evil.example", llm.LocalityRemote},
		{"http://localhost.evil.example", llm.LocalityRemote},
		{"http://evil.example/.local", llm.LocalityRemote},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			got, err := llm.EndpointLocality(tc.url)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
	for _, bad := range []string{"", "localhost:8000", "ftp://localhost", "http://u:p@localhost", "http://", `http://localhost\@evil.example`, "http://local%68ost"} {
		_, err := llm.EndpointLocality(bad)
		assert.Error(t, err, bad)
	}
}

func TestLocalOnlyResolve(t *testing.T) {
	useCatalog(t, fixtureProviders()...)
	const leak = "openai://localhost:8000/m?base_url=https://api.openai.com/v1"
	cases := []struct {
		name string
		uri  string
		opts []llm.LocalOption
		ok   bool
	}{
		{"ollama default", "ollama://llama3", nil, true},
		{"lmstudio default", "lmstudio://m", nil, true},
		{"openai self-hosted loopback", "openai://localhost:8000/m", nil, true},
		{"ollama base_url loopback", "ollama://m?base_url=http://127.0.0.1:5", nil, true},
		{"private IP by default", "openai://10.0.0.5:8000/m", nil, true},
		{"private IP refused when disallowed", "openai://10.0.0.5:8000/m", []llm.LocalOption{llm.AllowPrivate(false)}, false},
		{"loopback still ok when private disallowed", "ollama://m", []llm.LocalOption{llm.AllowPrivate(false)}, true},
		{"mdns name", "ollama://box.local:11434/m", nil, true},
		{"tailnet IP remote", "ollama://100.100.1.1:11434/m", nil, false},
		{"tailnet IP allowed by CIDR", "ollama://100.100.1.1:11434/m", []llm.LocalOption{llm.AllowHosts("100.64.0.0/10")}, true},
		{"tailnet IP allowed literally", "ollama://100.100.1.1:11434/m", []llm.LocalOption{llm.AllowHosts("100.100.1.1")}, true},
		{"tailnet name remote", "ollama://gpu.tail1.ts.net:11434/m", nil, false},
		{"tailnet name allowed", "ollama://gpu.tail1.ts.net:11434/m", []llm.LocalOption{llm.AllowHosts("GPU.tail1.ts.net")}, true},
		{"private name refused", "ollama://gpu-box:11434/m", nil, false},
		{"anthropic declared", "anthropic://claude", nil, false},
		{"openrouter declared", "openrouter://x/y", nil, false},
		{"no known endpoint", "openai://gpt-4o", nil, false},
		{"base_url leak", leak, nil, false},
		{"base_url leak, key in URI", leak + "&api_key=sk-secret", nil, false},
		{"base_url to cloud on a local scheme", "ollama://m?base_url=https://ollama.com", nil, false},
		{"base_url userinfo trick", "ollama://m?base_url=http://localhost@evil.example", nil, false},
		{"base_url not http", "ollama://m?base_url=file:///x", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := llm.Resolve(tc.uri, llm.LocalOnly(tc.opts...), llm.Hermetic())
			if tc.ok {
				require.NoError(t, err)
				defer p.Close()
				assert.True(t, llm.IsLocal(p))
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, llm.ErrNotLocal)
			var nl *llm.NotLocalError
			require.True(t, errors.As(err, &nl))
			assert.NotContains(t, err.Error(), "sk-secret")
			assert.NotContains(t, nl.URI, "sk-secret")
		})
	}

	t.Run("error carries the endpoint and its locality", func(t *testing.T) {
		_, err := llm.Resolve(leak, llm.LocalOnly(), llm.Hermetic())
		var nl *llm.NotLocalError
		require.ErrorAs(t, err, &nl)
		assert.Equal(t, "https://api.openai.com/v1", nl.Endpoint)
		assert.Equal(t, llm.LocalityRemote, nl.Locality)
	})
	t.Run("catalog provider", func(t *testing.T) {
		_, err := llm.Resolve("digitalocean://m", llm.LocalOnly())
		assert.ErrorIs(t, err, llm.ErrNotLocal)
	})
	t.Run("without LocalOnly the leak resolves", func(t *testing.T) {
		p, err := llm.Resolve(leak, llm.Hermetic())
		require.NoError(t, err)
		assert.False(t, llm.IsLocal(p))
	})
	t.Run("bad AllowHosts entry", func(t *testing.T) {
		_, err := llm.Resolve("ollama://m", llm.LocalOnly(llm.AllowHosts("not a host")), llm.Hermetic())
		assert.Error(t, err)
		_, err = llm.Resolve("ollama://m", llm.LocalOnly(llm.AllowHosts("10.0.0.0/99")), llm.Hermetic())
		assert.Error(t, err)
	})
	t.Run("provider keeps its capabilities", func(t *testing.T) {
		p, err := llm.Resolve("ollama://m", llm.LocalOnly(), llm.Hermetic())
		require.NoError(t, err)
		_, ok := p.(llm.Completer)
		assert.True(t, ok)
		u, ok := p.(interface{ Unwrap() llm.Provider })
		require.True(t, ok)
		assert.False(t, llm.IsLocal(u.Unwrap()), "the adapter itself is unmarked")
		c, err := llm.LocalOnlyClient(p)
		require.NoError(t, err)
		assert.Equal(t, llm.NewClient(u.Unwrap()).Capabilities(), c.Capabilities(), "exact, as the adapter's")
	})
}

type tape struct{ hits []string }

func (tp *tape) RoundTrip(r *http.Request) (*http.Response, error) {
	tp.hits = append(tp.hits, r.URL.Host)
	return &http.Response{StatusCode: 204, Body: http.NoBody, Request: r}, nil
}

// localFake resolves "fake://" with a declared local endpoint and
// records the config its factory got.
func localFake(got *[]llm.ResolvedConfig) *llm.Registry {
	r := llm.NewRegistry()
	r.Register("fake", func(cfg llm.ResolvedConfig) (llm.Provider, error) {
		*got = append(*got, cfg)
		return nopProvider{}, nil
	}, llm.Declaration{BaseURL: "http://localhost:9"})
	return r
}

func get(t *testing.T, cfg llm.ResolvedConfig, url string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := cfg.HTTPClient().Do(req)
	if err == nil {
		resp.Body.Close()
	}
	return err
}

func TestLocalOnlyChecksEveryRequestOnACallerTransport(t *testing.T) {
	for name, order := range map[string]func(llm.ResolveOption, llm.ResolveOption) []llm.ResolveOption{
		"LocalOnly first": func(l, w llm.ResolveOption) []llm.ResolveOption { return []llm.ResolveOption{l, w} },
		"WithTransport first": func(l, w llm.ResolveOption) []llm.ResolveOption {
			return []llm.ResolveOption{w, l}
		},
	} {
		t.Run(name, func(t *testing.T) {
			var got []llm.ResolvedConfig
			r := localFake(&got)
			tp := &tape{}
			_, err := r.Resolve("fake://m", order(llm.LocalOnly(), llm.WithTransport(tp))...)
			require.NoError(t, err)
			require.Len(t, got, 1)

			require.NoError(t, get(t, got[0], "http://localhost:9/v1/chat"))
			require.NoError(t, get(t, got[0], "http://192.168.1.5:9/x"))
			assert.Equal(t, []string{"localhost:9", "192.168.1.5:9"}, tp.hits)

			err = get(t, got[0], "https://api.openai.com/v1/chat?api_key=sk-secret")
			require.ErrorIs(t, err, llm.ErrNotLocal)
			var nl *llm.NotLocalError
			require.ErrorAs(t, err, &nl)
			assert.NotContains(t, nl.URI, "sk-secret")
			assert.Len(t, tp.hits, 2, "the tape never saw the remote request")
		})
	}

	t.Run("an allowed host passes, others still do not", func(t *testing.T) {
		var got []llm.ResolvedConfig
		tp := &tape{}
		_, err := localFake(&got).Resolve("fake://m", llm.WithTransport(tp), llm.LocalOnly(llm.AllowHosts("gpu.ts.net")))
		require.NoError(t, err)
		require.NoError(t, get(t, got[0], "http://gpu.ts.net/x"))
		assert.ErrorIs(t, get(t, got[0], "http://other.ts.net/x"), llm.ErrNotLocal)
	})
}

func TestLocalOnlyGuardsTheDial(t *testing.T) {
	resolvingTo := func(ips ...string) func(context.Context, string) ([]net.IP, error) {
		return func(context.Context, string) ([]net.IP, error) {
			var out []net.IP
			for _, s := range ips {
				out = append(out, net.ParseIP(s))
			}
			return out, nil
		}
	}
	run := func(t *testing.T, host string, ips []string, extra ...llm.LocalOption) (dialed []string, err error) {
		t.Helper()
		var got []llm.ResolvedConfig
		opts := append([]llm.LocalOption{
			llm.LocalWithLookup(resolvingTo(ips...)),
			llm.LocalWithDial(func(_ context.Context, _, addr string) (net.Conn, error) {
				dialed = append(dialed, addr)
				return nil, errors.New("dialed")
			}),
		}, extra...)
		_, rerr := localFake(&got).Resolve("fake://"+host+":8000/m", llm.LocalOnly(opts...))
		require.NoError(t, rerr)
		require.Len(t, got, 1)
		return dialed, get(t, got[0], "http://"+host+":8000/x")
	}

	t.Run("a local name resolving to a public IP is refused before dialing", func(t *testing.T) {
		var dialed []string
		dialed, err := run(t, "h.local", []string{"8.8.8.8"})
		require.ErrorIs(t, err, llm.ErrNotLocal)
		assert.Empty(t, dialed)
	})
	t.Run("one public address among private ones refuses", func(t *testing.T) {
		dialed, err := run(t, "h.local", []string{"192.168.0.9", "1.1.1.1"})
		require.ErrorIs(t, err, llm.ErrNotLocal)
		assert.Empty(t, dialed)
	})
	t.Run("a private address is dialed, as checked", func(t *testing.T) {
		dialed, err := run(t, "h.local", []string{"192.168.0.9"})
		assert.ErrorContains(t, err, "dialed")
		assert.NotErrorIs(t, err, llm.ErrNotLocal)
		assert.Equal(t, []string{"192.168.0.9:8000"}, dialed)
	})
	t.Run("a name resolving private is refused when AllowPrivate is off", func(t *testing.T) {
		dialed, err := run(t, "localhost", []string{"192.168.0.9"}, llm.AllowPrivate(false))
		require.ErrorIs(t, err, llm.ErrNotLocal)
		assert.Empty(t, dialed)
	})
	t.Run("loopback is dialed", func(t *testing.T) {
		dialed, _ := run(t, "localhost", []string{"127.0.0.1"}, llm.AllowPrivate(false))
		assert.Equal(t, []string{"127.0.0.1:8000"}, dialed)
	})
	t.Run("an allowed name's addresses are accepted", func(t *testing.T) {
		dialed, _ := run(t, "h.local", []string{"100.101.102.103"}, llm.AllowHosts("h.local"))
		assert.Equal(t, []string{"100.101.102.103:8000"}, dialed)
	})
	t.Run("kit's own transport ignores proxy variables", func(t *testing.T) {
		assert.True(t, llm.LocalOwnTransportNoProxy())
	})
}

func TestLocalOnlyClient(t *testing.T) {
	useCatalog(t, fixtureProviders()...)
	local := func(uri string) llm.Provider {
		t.Helper()
		p, err := llm.Resolve(uri, llm.LocalOnly(), llm.Hermetic())
		require.NoError(t, err)
		return p
	}

	t.Run("local chain", func(t *testing.T) {
		c, err := llm.LocalOnlyClient(local("ollama://a"), llm.WithFallback(local("lmstudio://b")))
		require.NoError(t, err)
		assert.Contains(t, c.Capabilities(), "complete")
	})
	t.Run("a cloud fallback is refused at construction", func(t *testing.T) {
		cloud, err := llm.Resolve("anthropic://claude?api_key=k", llm.Hermetic())
		require.NoError(t, err)
		_, err = llm.LocalOnlyClient(local("ollama://a"), llm.WithFallback(local("lmstudio://b")), llm.WithFallback(cloud))
		require.ErrorIs(t, err, llm.ErrNotLocal)
		var nl *llm.NotLocalError
		require.ErrorAs(t, err, &nl)
		assert.Contains(t, nl.Provider, "anthropic")
	})
	t.Run("a cloud primary is refused", func(t *testing.T) {
		cloud, err := llm.Resolve("anthropic://claude?api_key=k", llm.Hermetic())
		require.NoError(t, err)
		_, err = llm.LocalOnlyClient(cloud)
		assert.ErrorIs(t, err, llm.ErrNotLocal)
		_, err = llm.LocalOnlyClient(nil)
		assert.ErrorIs(t, err, llm.ErrNotLocal)
	})
	t.Run("a nil fallback is refused", func(t *testing.T) {
		_, err := llm.LocalOnlyClient(local("ollama://a"), llm.WithFallback(nil))
		assert.ErrorIs(t, err, llm.ErrNotLocal)
	})
	t.Run("a provider that vouches for itself is accepted", func(t *testing.T) {
		_, err := llm.LocalOnlyClient(vouched{}, llm.WithFallback(vouched{}))
		assert.NoError(t, err)
		_, err = llm.LocalOnlyClient(vouched{no: true})
		assert.ErrorIs(t, err, llm.ErrNotLocal)
	})
}

type vouched struct{ no bool }

func (vouched) Close() error  { return nil }
func (v vouched) Local() bool { return !v.no }

func TestFilterLocal(t *testing.T) {
	useCatalog(t, fixtureProviders()...)
	pool := []llm.PoolEntry{
		{Alias: "a", Scheme: "ollama", Model: "llama3", Enabled: true, Weight: 1},
		{Scheme: "lmstudio", Model: "qwen", Enabled: true, Weight: 2},
		{Scheme: "anthropic", Model: "claude", Enabled: true, Weight: 1},
		{Scheme: "openrouter", Model: "x/y", Enabled: true},
		{Scheme: "openai", Model: "gpt-4o", Enabled: true}, // no known endpoint
		{Scheme: "nope", Model: "m", Enabled: true},
		{Scheme: "ollama", Model: "off", Enabled: false},
	}
	orig := append([]llm.PoolEntry(nil), pool...)
	got := llm.FilterLocal(pool)
	assert.Equal(t, orig, pool, "input untouched")
	require.Len(t, got, len(pool), "never shrinks: an empty pool would mean accept everything")
	var on []string
	for _, e := range got {
		if e.Enabled {
			on = append(on, e.Scheme)
		}
	}
	assert.Equal(t, []string{"ollama", "lmstudio"}, on)
	assert.Equal(t, orig[0], got[0], "a local entry is unchanged")

	assert.Empty(t, llm.FilterLocal(nil))

	// With nothing local, the picker's pool filter still eliminates all.
	cloud := []llm.PoolEntry{{Scheme: "anthropic", Model: "claude", Enabled: true}}
	survivors, eliminated := llm.FilterByPool([]aim.Model{{Provider: "anthropic", ID: "claude"}}, llm.FilterLocal(cloud))
	assert.Empty(t, survivors)
	assert.Len(t, eliminated, 1)
}
