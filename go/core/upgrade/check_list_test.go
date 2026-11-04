package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

type rel map[string]any

func r(tag string, extra ...any) rel {
	m := rel{"tag_name": tag}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i].(string)] = extra[i+1]
	}
	return m
}

// listServer serves pages[n-1] for ?page=n on /repos/acme/mono/releases and
// 404 for anything else (releases/latest included). reqs records paths.
type listServer struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []string
}

func newListServer(t *testing.T, status int, pages ...any) *listServer {
	t.Helper()
	ls := &listServer{}
	ls.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ls.mu.Lock()
		ls.reqs = append(ls.reqs, req.URL.RequestURI())
		ls.mu.Unlock()
		if req.URL.Path != "/repos/acme/mono/releases" {
			http.NotFound(w, req)
			return
		}
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		n, _ := strconv.Atoi(req.URL.Query().Get("page"))
		var body any = []any{}
		if n >= 1 && n <= len(pages) {
			body = pages[n-1]
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	old := githubAPI
	githubAPI = ls.URL
	t.Cleanup(func() { githubAPI = old; ls.Close() })
	return ls
}

func listCheck(t *testing.T, dir, current string, opts ...Option) *Result {
	t.Helper()
	base := []Option{
		WithBinary("xray", current), WithGitHub("acme/mono"),
		WithStateDir(dir), WithTimeout(5 * time.Second),
	}
	return New(append(base, opts...)...).Check(context.Background())
}

func filler(n, count int) []rel {
	out := make([]rel, count)
	for i := range out {
		out[i] = r(fmt.Sprintf("kit/v1.%d.%d", n, i))
	}
	return out
}

func TestListPrefixNotifies(t *testing.T) {
	newListServer(t, 200, []rel{r("xray/v0.2.0")})
	res := listCheck(t, t.TempDir(), "0.1.0", WithTagPrefix("xray/v"))
	if res.Err != nil || !res.UpdateAvail || res.Latest != "0.2.0" || res.Tag != "xray/v0.2.0" {
		t.Fatalf("got %+v", res)
	}
}

func TestListIgnoresOtherComponents(t *testing.T) {
	newListServer(t, 200, []rel{r("kit/v9.0.0"), r("xray/v0.2.0")})
	res := listCheck(t, t.TempDir(), "0.1.0", WithTagPrefix("xray/v"))
	if res.Latest != "0.2.0" {
		t.Fatalf("got %+v", res)
	}
	newListServer(t, 200, []rel{r("kit/v9.0.0"), r("xray-cli/v9.0.0")})
	res = listCheck(t, t.TempDir(), "0.1.0", WithTagPrefix("xray/v"))
	if res.UpdateAvail || res.Latest != "" {
		t.Fatalf("other component notified: %+v", res)
	}
}

func TestListHighestSemverSkipsDrafts(t *testing.T) {
	newListServer(t, 200, []rel{
		r("xray/v0.2.1"), r("xray/v0.10.0"), r("xray/v0.9.0"), r("xray/v0.99.0", "draft", true),
	})
	if res := listCheck(t, t.TempDir(), "0.1.0", WithTagPrefix("xray/v")); res.Latest != "0.10.0" {
		t.Fatalf("got %+v", res)
	}
}

func TestListPrereleaseSkippedWithoutOption(t *testing.T) {
	newListServer(t, 200, []rel{
		r("xray/v0.3.0-rc.1", "prerelease", true),
		r("xray/v0.3.0-rc.0"), // not flagged by GitHub; semver says prerelease
	})
	if res := listCheck(t, t.TempDir(), "0.2.0", WithTagPrefix("xray/v")); res.UpdateAvail {
		t.Fatalf("stable user nudged onto prerelease: %+v", res)
	}
}

func TestListPrereleaseUsers(t *testing.T) {
	newListServer(t, 200, []rel{r("xray/v0.3.0-rc.1", "prerelease", true)})
	o := []Option{WithTagPrefix("xray/v"), WithIncludePrereleases(true)}
	if res := listCheck(t, t.TempDir(), "0.2.0", o...); res.Latest != "0.3.0-rc.1" || !res.UpdateAvail {
		t.Fatalf("stable user, option on: %+v", res)
	}
	newListServer(t, 200, []rel{r("xray/v0.1.0-rc.1", "prerelease", true), r("xray/v0.1.0-rc.0", "prerelease", true)})
	if res := listCheck(t, t.TempDir(), "0.1.0-rc.0", o...); res.Latest != "0.1.0-rc.1" || !res.UpdateAvail {
		t.Fatalf("prerelease user: %+v", res)
	}
	if res := listCheck(t, t.TempDir(), "0.1.0-rc.1", o...); res.UpdateAvail {
		t.Fatalf("already newest: %+v", res)
	}
	newListServer(t, 200, []rel{r("xray/v0.1.0-rc.1", "prerelease", true), r("xray/v0.1.0")})
	if res := listCheck(t, t.TempDir(), "0.1.0-rc.0", o...); res.Latest != "0.1.0" {
		t.Fatalf("final stable, option on: %+v", res)
	}
	if res := listCheck(t, t.TempDir(), "0.1.0-rc.0", WithTagPrefix("xray/v")); res.Latest != "0.1.0" || !res.UpdateAvail {
		t.Fatalf("final stable, option off: %+v", res)
	}
}

func TestListIncludePrereleasesAlone(t *testing.T) {
	newListServer(t, 200, []rel{r("v1.0.0-rc.1", "prerelease", true), r("v0.9.0")})
	res := listCheck(t, t.TempDir(), "0.9.0", WithIncludePrereleases(true))
	if res.Latest != "1.0.0-rc.1" || res.Tag != "v1.0.0-rc.1" {
		t.Fatalf("got %+v", res)
	}
}

func TestListNeverCallsLatest(t *testing.T) {
	ls := newListServer(t, 200, []rel{r("xray/v0.2.0")})
	listCheck(t, t.TempDir(), "0.1.0", WithTagPrefix("xray/v"))
	for _, q := range ls.reqs {
		if q == "/repos/acme/mono/releases/latest" {
			t.Fatalf("list mode hit releases/latest")
		}
	}
}

func TestListPaging(t *testing.T) {
	ls := newListServer(t, 200, filler(0, 100), []rel{r("xray/v0.2.0")})
	if res := listCheck(t, t.TempDir(), "0.1.0", WithTagPrefix("xray/v")); res.Latest != "0.2.0" {
		t.Fatalf("got %+v", res)
	}
	if len(ls.reqs) != 2 {
		t.Fatalf("want 2 requests, got %v", ls.reqs)
	}
}

func TestListMaxThreePages(t *testing.T) {
	ls := newListServer(t, 200, filler(0, 100), filler(1, 100), filler(2, 100), []rel{r("xray/v0.2.0")})
	if res := listCheck(t, t.TempDir(), "0.1.0", WithTagPrefix("xray/v")); res.UpdateAvail {
		t.Fatalf("page 4 read: %+v", res)
	}
	if len(ls.reqs) != 3 {
		t.Fatalf("want 3 requests, got %v", ls.reqs)
	}
}

func TestListKeepsEarlierPagesOnLaterFailure(t *testing.T) {
	full := make([]rel, 100)
	for i := range full {
		full[i] = r(fmt.Sprintf("xray/v0.2.%d", i))
	}
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n++
		if n == 1 {
			_ = json.NewEncoder(w).Encode(full)
			return
		}
		w.WriteHeader(500)
	}))
	defer srv.Close()
	old := githubAPI
	githubAPI = srv.URL
	defer func() { githubAPI = old }()
	if res := listCheck(t, t.TempDir(), "0.1.0", WithTagPrefix("xray/v")); res.Latest != "0.2.99" {
		t.Fatalf("got %+v", res)
	}
}

func TestListErrorsStaySilent(t *testing.T) {
	newListServer(t, 403)
	if res := listCheck(t, t.TempDir(), "0.1.0", WithTagPrefix("xray/v")); res.Err == nil || res.UpdateAvail {
		t.Fatalf("want embedded error, got %+v", res)
	}
	newListServer(t, 200, map[string]string{"message": "nope"})
	if res := listCheck(t, t.TempDir(), "0.1.0", WithTagPrefix("xray/v")); res.Err == nil || res.UpdateAvail {
		t.Fatalf("object body: %+v", res)
	}
	newListServer(t, 200, []any{nil, rel{"tag_name": 7}, r("xray/vbogus"), "x"})
	if res := listCheck(t, t.TempDir(), "0.1.0", WithTagPrefix("xray/v")); res.UpdateAvail {
		t.Fatalf("junk items: %+v", res)
	}
}

func TestListCacheKeepsTag(t *testing.T) {
	ls := newListServer(t, 200, []rel{r("xray/v0.2.0")})
	dir := t.TempDir()
	listCheck(t, dir, "0.1.0", WithTagPrefix("xray/v"))
	res := listCheck(t, dir, "0.1.0", WithTagPrefix("xray/v"))
	if len(ls.reqs) != 1 || res.Tag != "xray/v0.2.0" || res.Latest != "0.2.0" {
		t.Fatalf("reqs=%v res=%+v", ls.reqs, res)
	}
}

func TestListCachedTagMustNameVersion(t *testing.T) {
	for name, tag := range map[string]string{
		"path traversal":    "xray/v../../../evil",
		"another version":   "xray/v1.0.0",
		"another component": "kit/v2.0.0",
	} {
		t.Run(name, func(t *testing.T) {
			ls := newListServer(t, 200, []rel{r("xray/v1.5.0")})
			dir := t.TempDir()
			cache := cachedResult{Current: "1.0.0", Latest: "2.0.0", CheckedAt: time.Now(), UpdateAvail: true, Tag: tag}
			data, _ := json.Marshal(cache)
			_ = os.MkdirAll(filepath.Join(dir), 0o700)
			if err := os.WriteFile(filepath.Join(dir, "cache.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			res := listCheck(t, dir, "1.0.0", WithTagPrefix("xray/v"))
			if len(ls.reqs) != 1 || res.Latest != "1.5.0" || res.Tag != "xray/v1.5.0" {
				t.Fatalf("poisoned cache served: reqs=%v res=%+v", ls.reqs, res)
			}
		})
	}
}

func TestDefaultModeUnchanged(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		paths = append(paths, req.URL.RequestURI())
		_ = json.NewEncoder(w).Encode(rel{"tag_name": "v2.0.0"})
	}))
	defer srv.Close()
	old := githubAPI
	githubAPI = srv.URL
	defer func() { githubAPI = old }()

	dir := t.TempDir()
	res := listCheck(t, dir, "1.0.0")
	if len(paths) != 1 || paths[0] != "/repos/acme/mono/releases/latest" {
		t.Fatalf("paths %v", paths)
	}
	if res.Latest != "2.0.0" || !res.UpdateAvail || res.Tag != "" {
		t.Fatalf("got %+v", res)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "cache.json"))
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(raw, &keys)
	for _, k := range []string{"current", "latest", "url", "notes", "checked_at", "update_avail"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("cache missing %q", k)
		}
	}
	if _, ok := keys["tag"]; ok || len(keys) != 6 {
		t.Errorf("default cache keys changed: %s", raw)
	}
}

func TestListTaglessCacheIsMiss(t *testing.T) {
	ls := newListServer(t, 200, []rel{r("xray/v1.5.0")})
	dir := t.TempDir()
	// A record as default mode writes it: no tag.
	cache := cachedResult{Current: "1.0.0", Latest: "2.0.0", CheckedAt: time.Now(), UpdateAvail: true}
	data, _ := json.Marshal(cache)
	if err := os.WriteFile(filepath.Join(dir, "cache.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	res := listCheck(t, dir, "1.0.0", WithTagPrefix("xray/v"))
	if len(ls.reqs) != 1 || res.Latest != "1.5.0" || res.Tag != "xray/v1.5.0" {
		t.Fatalf("tagless cache served in list mode: reqs=%v res=%+v", ls.reqs, res)
	}
}
