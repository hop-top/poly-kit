package mcpsdk

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"hop.top/kit/go/transport/api"
)

// unsized hides the reader type so net/http sends the body chunked.
type unsized struct{ io.Reader }

// pingBody is a JSON-RPC ping of exactly n bytes.
func pingBody(t *testing.T, n int) string {
	t.Helper()
	const prefix, suffix = `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"_pad":"`, `"}}`
	if n < len(prefix)+len(suffix) {
		t.Fatalf("size %d too small", n)
	}
	return prefix + strings.Repeat("x", n-len(prefix)-len(suffix)) + suffix
}

func postMCP(t *testing.T, url string, body io.Reader) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestMaxBodyBytes(t *testing.T) {
	const limit = 512
	sized := func(s string) io.Reader { return strings.NewReader(s) }
	chunked := func(s string) io.Reader { return unsized{strings.NewReader(s)} }
	cases := []struct {
		name string
		size int
		wrap func(string) io.Reader
		over bool
	}{
		{"sized at limit", limit, sized, false},
		{"sized one over", limit + 1, sized, true},
		{"chunked at limit", limit, chunked, false},
		{"chunked one over", limit + 1, chunked, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := newHarness(t, defaultBridge, WithStateless(), WithJSONResponse(), WithMaxBodyBytes(limit))
			got := postMCP(t, srv.URL+"/mcp", c.wrap(pingBody(t, c.size)))
			if c.over && got != http.StatusRequestEntityTooLarge {
				t.Fatalf("status=%d want 413", got)
			}
			if !c.over && got == http.StatusRequestEntityTooLarge {
				t.Fatalf("status=413 for a body at the cap")
			}
		})
	}
}

func TestMaxBodyBytesDefaultIsKitDefault(t *testing.T) {
	// Kit's 1 MiB, not the SDK's 4 MiB.
	srv, _ := newHarness(t, defaultBridge, WithStateless(), WithJSONResponse())
	if got := postMCP(t, srv.URL+"/mcp", unsized{strings.NewReader(pingBody(t, int(api.DefaultMaxBodyBytes)+1))}); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413 just over api.DefaultMaxBodyBytes", got)
	}
}
