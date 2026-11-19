// Package transporttest checks that an adapter sends every request
// through the transport its caller supplied ([llm.WithTransport]),
// never through the process default, and refuses a redirect. Network
// free: the transports here answer from memory.
package transporttest

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// elsewhere is where the redirect points; a request to it means the
// adapter followed the 3xx.
const elsewhere = "redirect-target.invalid"

// RT is a counting [http.RoundTripper] with a canned answer.
type RT struct {
	Status int
	Header http.Header
	Body   string

	mu   sync.Mutex
	urls []string
}

// Serve answers every request 200 with body.
func Serve(contentType, body string) *RT {
	return &RT{Status: 200, Header: http.Header{"Content-Type": {contentType}}, Body: body}
}

// Redirect answers every request with a 302 to another host.
func Redirect() *RT {
	return &RT{Status: 302, Header: http.Header{"Location": {"https://" + elsewhere + "/x"}}}
}

// RoundTrip implements [http.RoundTripper].
func (r *RT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.urls = append(r.urls, req.URL.String())
	r.mu.Unlock()
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	return &http.Response{
		Status:     http.StatusText(r.Status),
		StatusCode: r.Status,
		Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:  r.Header.Clone(),
		Body:    io.NopCloser(strings.NewReader(r.Body)),
		Request: req,
	}, nil
}

// Hits is the number of requests seen.
func (r *RT) Hits() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.urls)
}

// Followed reports whether any request went to the redirect target.
func (r *RT) Followed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.urls {
		if strings.Contains(u, elsewhere) {
			return true
		}
	}
	return false
}

var errDefaultHit = errors.New("transporttest: http.DefaultTransport used")

// failing is the stand-in default transport.
type failing struct {
	mu sync.Mutex
	n  int
}

func (f *failing) RoundTrip(*http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.n++
	f.mu.Unlock()
	return nil, errDefaultHit
}

// BlockDefault swaps http.DefaultTransport for one that fails and
// counts, restored at cleanup. The test must not run in parallel.
func BlockDefault(t *testing.T) func() int {
	t.Helper()
	f := &failing{}
	old := http.DefaultTransport
	http.DefaultTransport = f
	t.Cleanup(func() { http.DefaultTransport = old })
	return func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.n
	}
}

// Spec describes one adapter under test.
type Spec struct {
	URI         string // resolves to the adapter, with a key if it needs one
	ContentType string
	Body        string                   // a success body for Call
	Call        func(llm.Provider) error // makes one request; nil on success
}

// Check runs the transport contract against s.
func Check(t *testing.T, s Spec) {
	t.Helper()

	resolve := func(t *testing.T, opts ...llm.ResolveOption) llm.Provider {
		t.Helper()
		p, err := llm.Resolve(s.URI, opts...)
		require.NoError(t, err)
		t.Cleanup(func() { _ = p.Close() })
		return p
	}

	t.Run("caller transport serves, default untouched", func(t *testing.T) {
		defHits := BlockDefault(t)
		rt := Serve(s.ContentType, s.Body)
		// Hermetic and transport together.
		p := resolve(t, llm.WithTransport(rt), llm.Hermetic())
		require.NoError(t, s.Call(p))
		assert.Positive(t, rt.Hits(), "caller transport was not used")
		assert.Zero(t, defHits(), "http.DefaultTransport was used")
	})

	t.Run("redirect refused", func(t *testing.T) {
		defHits := BlockDefault(t)
		rt := Redirect()
		p := resolve(t, llm.WithTransport(rt))
		require.Error(t, s.Call(p))
		assert.Positive(t, rt.Hits())
		assert.False(t, rt.Followed(), "redirect was followed")
		assert.Zero(t, defHits())
	})

	t.Run("no transport keeps the process default", func(t *testing.T) {
		BlockDefault(t)
		rt := Serve(s.ContentType, s.Body)
		old := http.DefaultClient.Transport
		http.DefaultClient.Transport = rt
		t.Cleanup(func() { http.DefaultClient.Transport = old })
		p := resolve(t)
		require.NoError(t, s.Call(p))
		assert.Positive(t, rt.Hits(), "http.DefaultClient's transport was bypassed")
	})
}
