package cmdsurface_test

import (
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/storage/kv/memory"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// cachedProjection serves a tree whose read and write both declare a
// one-minute kit/cache-ttl, through a bridge with the result cache on,
// and counts how often each command ran.
func cachedProjection(t *testing.T, auth api.AuthFunc) (url string, reads, writes *atomic.Int64) {
	t.Helper()
	reads, writes = &atomic.Int64{}, &atomic.Int64{}
	root := &cobra.Command{Use: "tool"}
	widget := &cobra.Command{Use: "widget"}
	widget.AddCommand(
		&cobra.Command{
			Use: "list",
			Annotations: map[string]string{
				"kit/side-effect":             "read",
				cmdsurface.AnnotationCacheTTL: "1m",
			},
			RunE: func(cmd *cobra.Command, _ []string) error {
				reads.Add(1)
				cmd.Println("a b")
				return nil
			},
		},
		&cobra.Command{
			Use: "add",
			Annotations: map[string]string{
				"kit/side-effect":             "write",
				cmdsurface.AnnotationCacheTTL: "1m",
			},
			RunE: func(cmd *cobra.Command, _ []string) error {
				writes.Add(1)
				cmd.Println("added")
				return nil
			},
		},
	)
	root.AddCommand(widget)

	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	b := cmdsurface.New(root, cmdsurface.WithResultCache(store))
	b.Expose("*", cmdsurface.SurfaceREST)
	r := api.NewRouter()
	var opts []cmdsurface.ProjectionOption
	if auth != nil {
		opts = append(opts, cmdsurface.WithProjectionAuth(auth))
	}
	if err := cmdsurface.MountProjection(b, r, opts...); err != nil {
		t.Fatal(err)
	}
	return serve(t, r), reads, writes
}

func request(t *testing.T, method, url string, header map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestProjectionCache_ETagAnd304(t *testing.T) {
	url, reads, _ := cachedProjection(t, nil)
	route := url + "/v1/commands/widget/list"

	first := request(t, http.MethodGet, route, nil)
	firstBody := readBody(t, first)
	etag := first.Header.Get("ETag")
	if first.StatusCode != http.StatusOK || etag == "" {
		t.Fatalf("first GET: %d, ETag %q", first.StatusCode, etag)
	}
	if cc := first.Header.Get("Cache-Control"); cc != "public, max-age=60" {
		t.Fatalf("Cache-Control = %q", cc)
	}

	second := request(t, http.MethodGet, route, nil)
	if body := readBody(t, second); body != firstBody || second.Header.Get("ETag") != etag {
		t.Fatalf("hit answered %q (ETag %q), miss %q (ETag %q)",
			body, second.Header.Get("ETag"), firstBody, etag)
	}

	notModified := request(t, http.MethodGet, route, map[string]string{"If-None-Match": etag})
	if body := readBody(t, notModified); notModified.StatusCode != http.StatusNotModified || body != "" {
		t.Fatalf("If-None-Match: %d %q, want 304 with no body", notModified.StatusCode, body)
	}
	stale := request(t, http.MethodGet, route, map[string]string{"If-None-Match": `W/"other"`})
	if readBody(t, stale); stale.StatusCode != http.StatusOK {
		t.Fatalf("stale If-None-Match: %d, want 200", stale.StatusCode)
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("widget list ran %d times, want 1", n)
	}
}

func TestProjectionCache_WriteNeverCached(t *testing.T) {
	url, _, writes := cachedProjection(t, nil)
	for range 2 {
		resp := request(t, http.MethodPost, url+"/v1/commands/widget/add",
			map[string]string{"If-None-Match": "*"})
		if readBody(t, resp); resp.StatusCode != http.StatusOK || resp.Header.Get("ETag") != "" {
			t.Fatalf("POST widget/add: %d, ETag %q", resp.StatusCode, resp.Header.Get("ETag"))
		}
	}
	if n := writes.Load(); n != 2 {
		t.Fatalf("widget add ran %d times, want 2", n)
	}
}

func TestProjectionCache_PrivatePerPrincipal(t *testing.T) {
	auth := func(r *http.Request) (any, error) {
		return api.Claims{Subject: r.Header.Get("X-User")}, nil
	}
	url, reads, _ := cachedProjection(t, auth)
	route := url + "/v1/commands/widget/list"

	alice := request(t, http.MethodGet, route, map[string]string{"X-User": "alice"})
	readBody(t, alice)
	if cc := alice.Header.Get("Cache-Control"); cc != "private, max-age=60" {
		t.Fatalf("Cache-Control = %q, want private", cc)
	}
	// Bob presenting alice's validator gets his own result, not a 304.
	bob := request(t, http.MethodGet, route, map[string]string{
		"X-User": "bob", "If-None-Match": alice.Header.Get("ETag"),
	})
	if readBody(t, bob); bob.StatusCode != http.StatusOK {
		t.Fatalf("bob with alice's ETag: %d, want 200", bob.StatusCode)
	}
	if n := reads.Load(); n != 2 {
		t.Fatalf("widget list ran %d times for two principals, want 2", n)
	}
}
