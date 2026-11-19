package llm_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/aim"
	"hop.top/kit/go/ai/llm"
)

// noDial fails the test on any HTTP round trip through the default
// transport, the path an aim fetch takes.
type noDial struct {
	t     *testing.T
	dials atomic.Int32
}

func (d *noDial) RoundTrip(r *http.Request) (*http.Response, error) {
	d.dials.Add(1)
	d.t.Errorf("network dial to %s", r.URL)
	return nil, assert.AnError
}

func guardNetwork(t *testing.T) *noDial {
	t.Helper()
	d := &noDial{t: t}
	old := http.DefaultTransport
	http.DefaultTransport = d
	t.Cleanup(func() { http.DefaultTransport = old })
	return d
}

func priceProviders() []aim.Provider {
	return []aim.Provider{
		{ID: "anthropic", Models: map[string]*aim.Model{
			"claude-x": {ID: "claude-x", Cost: &aim.Cost{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}},
			"nocost":   {ID: "nocost"},
		}},
		{ID: "fireworks-ai", Models: map[string]*aim.Model{
			"llama": {ID: "llama", Cost: &aim.Cost{Input: 0.9, Output: 0.9}},
		}},
		{ID: "lmstudio", API: "http://127.0.0.1:1234/v1", Models: map[string]*aim.Model{
			"m": {ID: "m", Cost: &aim.Cost{Input: 5}},
		}},
	}
}

func TestPriceOfLocalSchemesAreFree(t *testing.T) {
	d := guardNetwork(t)
	useColdCatalog(t) // no cache needed: local is free by scheme
	for _, s := range []string{"ollama", "lmstudio", "routellm", "triton"} {
		p, ok := llm.PriceOf(context.Background(), s, "anything")
		assert.True(t, ok, s)
		assert.Equal(t, llm.Price{}, p, s)
	}
	assert.Zero(t, d.dials.Load())
}

func TestPriceOfLocalBeatsCatalogCost(t *testing.T) {
	useCatalog(t, priceProviders()...)
	p, ok := llm.PriceOf(context.Background(), "lmstudio", "m")
	assert.True(t, ok)
	assert.Equal(t, llm.Price{}, p)
}

func TestPriceOfKnownCloudModel(t *testing.T) {
	d := guardNetwork(t)
	fetches := useCatalog(t, priceProviders()...)
	p, ok := llm.PriceOf(context.Background(), "anthropic", "claude-x")
	require.True(t, ok)
	assert.Equal(t, llm.Price{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, p)

	u := llm.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000}
	assert.InDelta(t, 18.0, u.Cost(p), 1e-9)

	p, ok = llm.PriceOf(context.Background(), "fireworks", "llama") // alias
	require.True(t, ok)
	assert.Equal(t, 0.9, p.Input)

	assert.Zero(t, fetches.Load())
	assert.Zero(t, d.dials.Load())
}

func TestPriceOfUnknown(t *testing.T) {
	useCatalog(t, priceProviders()...)
	for _, c := range [][2]string{
		{"anthropic", "nope"},
		{"anthropic", "nocost"},
		{"nosuchscheme", "claude-x"},
		{"", ""},
	} {
		p, ok := llm.PriceOf(context.Background(), c[0], c[1])
		assert.False(t, ok, "%v", c)
		assert.Equal(t, llm.Price{}, p, "%v", c)
	}
}

func TestPriceOfMissingCacheNeverFetches(t *testing.T) {
	d := guardNetwork(t)
	fetches := useColdCatalog(t)
	p, ok := llm.PriceOf(context.Background(), "anthropic", "claude-x")
	assert.False(t, ok)
	assert.Equal(t, llm.Price{}, p)
	assert.Zero(t, fetches.Load(), "cold cache must not trigger a fetch")
	assert.Zero(t, d.dials.Load())
}
