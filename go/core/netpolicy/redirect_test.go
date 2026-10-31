package netpolicy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// loopServer redirects every request to a longer path on itself.
func loopServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A caller-supplied CheckRedirect composes: its verdict applies to
// hops the header check lets through, and cannot override a refusal.
func TestCheckRedirectComposes(t *testing.T) {
	b := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer b.Close()
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, b.URL, http.StatusFound)
	}))
	defer a.Close()

	called := 0
	c := &http.Client{CheckRedirect: CheckRedirect(func(*http.Request, []*http.Request) error {
		called++
		return http.ErrUseLastResponse
	})}

	// Safe headers: next decides, and ErrUseLastResponse returns the 3xx.
	req, _ := http.NewRequest(http.MethodGet, a.URL, nil)
	req.Header.Set("Accept", "*/*")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || called != 1 {
		t.Errorf("status=%d called=%d, want 302 and next consulted once", resp.StatusCode, called)
	}

	// Unsafe header: refused before next is consulted.
	req, _ = http.NewRequest(http.MethodGet, a.URL, nil)
	req.Header.Set("X-Api-Key", "k")
	if resp, err = c.Do(req); resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrCrossOriginHeaders) || called != 1 {
		t.Errorf("err=%v called=%d, want refusal without consulting next", err, called)
	}
}

// With no next, net/http's default limit of 10 hops still applies.
func TestCheckRedirectDefaultLimit(t *testing.T) {
	srv := loopServer(t)
	_, err := NewClient(nil).Get(srv.URL + "/loop")
	if err == nil || !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Errorf("err = %v, want hop limit", err)
	}
}

// Ports are part of the origin; default ports are implied.
func TestSameOrigin(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"http://h/x", "http://h:80/y", true},
		{"https://h/x", "https://H:443/y", true},
		{"http://h:1/x", "http://h:2/x", false},
		{"http://h/x", "https://h/x", false},
		{"http://a/x", "http://b/x", false},
	} {
		ra, _ := http.NewRequest("GET", c.a, nil)
		rb, _ := http.NewRequest("GET", c.b, nil)
		if got := sameOrigin(ra.URL, rb.URL); got != c.want {
			t.Errorf("sameOrigin(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
