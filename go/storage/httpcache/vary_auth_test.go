package httpcache_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/httpcache"
)

// echoServer replies with "<hdr>=<value of request header hdr>" and the
// given response headers. origin counts requests that reached it.
func echoServer(t *testing.T, hdr string, respHeaders map[string]string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var origin atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin.Add(1)
		for k, v := range respHeaders {
			w.Header().Set(k, v)
		}
		_, _ = io.WriteString(w, hdr+"="+r.Header.Get(hdr))
	}))
	t.Cleanup(srv.Close)
	return srv, &origin
}

func getWith(t *testing.T, c *http.Client, url string, hdr map[string]string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

func TestVary_SeparatesVariants(t *testing.T) {
	srv, origin := echoServer(t, "Accept-Language", map[string]string{"Vary": "Accept-Language"})
	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}

	fr := map[string]string{"Accept-Language": "fr"}
	en := map[string]string{"Accept-Language": "en"}

	assert.Equal(t, "Accept-Language=fr", getWith(t, client, srv.URL, fr))
	assert.Equal(t, "Accept-Language=en", getWith(t, client, srv.URL, en), "different Vary value must not get the fr response")
	assert.Equal(t, int64(2), origin.Load())

	// Both variants now cached.
	assert.Equal(t, "Accept-Language=fr", getWith(t, client, srv.URL, fr))
	assert.Equal(t, "Accept-Language=en", getWith(t, client, srv.URL, en))
	assert.Equal(t, int64(2), origin.Load(), "repeat requests served from their own variant")
}

func TestVary_MissingHeaderIsItsOwnVariant(t *testing.T) {
	srv, origin := echoServer(t, "Accept-Language", map[string]string{"Vary": "accept-language"})
	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}

	assert.Equal(t, "Accept-Language=", getWith(t, client, srv.URL, nil))
	assert.Equal(t, "Accept-Language=fr", getWith(t, client, srv.URL, map[string]string{"Accept-Language": "fr"}))
	assert.Equal(t, "Accept-Language=", getWith(t, client, srv.URL, nil))
	assert.Equal(t, int64(2), origin.Load())
}

func TestVary_Star_NeverStored(t *testing.T) {
	srv, origin := echoServer(t, "X", map[string]string{"Vary": "*"})
	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}
	getWith(t, client, srv.URL, nil)
	getWith(t, client, srv.URL, nil)
	assert.Equal(t, int64(2), origin.Load(), "Vary: * responses must not be cached")
}

func TestVary_MultipleFieldsAndHeaderLines(t *testing.T) {
	var origin atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin.Add(1)
		w.Header().Add("Vary", "Accept")
		w.Header().Add("Vary", "Accept-Language")
		_, _ = io.WriteString(w, r.Header.Get("Accept")+"|"+r.Header.Get("Accept-Language"))
	}))
	t.Cleanup(srv.Close)
	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}

	a := map[string]string{"Accept": "text/html", "Accept-Language": "en"}
	b := map[string]string{"Accept": "text/html", "Accept-Language": "fr"}
	assert.Equal(t, "text/html|en", getWith(t, client, srv.URL, a))
	assert.Equal(t, "text/html|fr", getWith(t, client, srv.URL, b))
	assert.Equal(t, "text/html|en", getWith(t, client, srv.URL, a))
	assert.Equal(t, int64(2), origin.Load())
}

func TestVary_NoVaryStillHitsAcrossHeaders(t *testing.T) {
	srv, origin := echoServer(t, "Accept-Language", nil)
	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}
	getWith(t, client, srv.URL, map[string]string{"Accept-Language": "fr"})
	getWith(t, client, srv.URL, map[string]string{"Accept-Language": "en"})
	assert.Equal(t, int64(1), origin.Load(), "no Vary: key stays method+URL")
}

func TestAuthorization_NotStoredByDefault(t *testing.T) {
	srv, origin := echoServer(t, "Authorization", nil)
	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}

	alice := map[string]string{"Authorization": "Bearer alice"}
	bob := map[string]string{"Authorization": "Bearer bob"}
	assert.Equal(t, "Authorization=Bearer alice", getWith(t, client, srv.URL, alice))
	assert.Equal(t, "Authorization=Bearer bob", getWith(t, client, srv.URL, bob), "bob must never see alice's response")
	assert.Equal(t, "Authorization=Bearer alice", getWith(t, client, srv.URL, alice))
	assert.Equal(t, int64(3), origin.Load(), "authenticated responses without a shared-cache directive are not stored")
}

func TestAuthorization_UnauthenticatedRequestNeverSeesAuthenticatedResponse(t *testing.T) {
	srv, origin := echoServer(t, "Authorization", nil)
	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}
	getWith(t, client, srv.URL, map[string]string{"Authorization": "Bearer alice"})
	assert.Equal(t, "Authorization=", getWith(t, client, srv.URL, nil))
	assert.Equal(t, int64(2), origin.Load())
}

func TestAuthorization_StoredWhenResponseAllowsSharedCaching(t *testing.T) {
	for _, cc := range []string{"public", "max-age=60, PUBLIC", "public, must-revalidate"} {
		t.Run(cc, func(t *testing.T) {
			srv, origin := echoServer(t, "Authorization", map[string]string{"Cache-Control": cc})
			client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}
			h := map[string]string{"Authorization": "Bearer alice"}
			getWith(t, client, srv.URL, h)
			getWith(t, client, srv.URL, h)
			assert.Equal(t, int64(1), origin.Load(), "%q opts the response into shared caching", cc)
		})
	}
}

func TestAuthorization_DirectiveTokensAreBounded(t *testing.T) {
	srv, origin := echoServer(t, "Authorization", map[string]string{"Cache-Control": "publicly-visible, s-maxage-ish=1"})
	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}
	h := map[string]string{"Authorization": "Bearer alice"}
	getWith(t, client, srv.URL, h)
	getWith(t, client, srv.URL, h)
	assert.Equal(t, int64(2), origin.Load())
}

func TestAuthorization_PublicButNoStoreStillNotStored(t *testing.T) {
	srv, origin := echoServer(t, "Authorization", map[string]string{"Cache-Control": "public, no-store"})
	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}
	h := map[string]string{"Authorization": "Bearer alice"}
	getWith(t, client, srv.URL, h)
	getWith(t, client, srv.URL, h)
	assert.Equal(t, int64(2), origin.Load())
}

func TestAuthorization_NotStoredWithoutPublic(t *testing.T) {
	// The store neither revalidates nor honors s-maxage, so neither directive
	// alone may expose one identity's response to another.
	for _, cc := range []string{"s-maxage=60", "must-revalidate", "max-age=60, must-revalidate"} {
		t.Run(cc, func(t *testing.T) {
			srv, origin := echoServer(t, "Authorization", map[string]string{"Cache-Control": cc})
			client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}
			assert.Equal(t, "Authorization=Bearer alice", getWith(t, client, srv.URL, map[string]string{"Authorization": "Bearer alice"}))
			assert.Equal(t, "Authorization=Bearer bob", getWith(t, client, srv.URL, map[string]string{"Authorization": "Bearer bob"}))
			assert.Equal(t, int64(2), origin.Load(), "%q must not opt an authenticated response into the shared store", cc)
		})
	}
}
