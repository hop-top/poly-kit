package main

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadPin(t *testing.T) {
	for name, tc := range map[string]struct {
		gomod, want, err string
	}{
		"plain": {
			gomod: "module x\n\nrequire (\n\thop.top/kit v0.5.0-alpha.15\n)\n",
			want:  "v0.5.0-alpha.15",
		},
		"release annotation as a template comment": {
			gomod: "require (\n\thop.top/kit v0.5.0-alpha.15 {{- /* x-release-please-version */}}\n)\n",
			want:  "v0.5.0-alpha.15",
		},
		"single-line require with a comment": {
			gomod: "require hop.top/kit v1.2.3 // indirect\n",
			want:  "v1.2.3",
		},
		"a module sharing the prefix is not the pin": {
			gomod: "require (\n\thop.top/kitchen v9.0.0\n\thop.top/kit v0.1.0\n)\n",
			want:  "v0.1.0",
		},
		"absent":     {gomod: "module x\n", err: "no hop.top/kit requirement"},
		"twice":      {gomod: "\thop.top/kit v0.1.0\n\thop.top/kit v0.2.0\n", err: "2 hop.top/kit requirements"},
		"not semver": {gomod: "\thop.top/kit latest\n", err: "not a semantic version"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := readPin(tc.gomod)
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestReadPin_TheTemplate keeps the checker and the template in step:
// the pin line the release PR rewrites is the one the checker reads.
func TestReadPin_TheTemplate(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "templates", "cli-go", "go.mod.tmpl"))
	require.NoError(t, err)
	_, err = readPin(string(body))
	require.NoError(t, err)
}

func TestCheck(t *testing.T) {
	published := []string{
		"v0.5.0-alpha.9", "v0.5.0-alpha.10", "v0.5.0-alpha.13",
		"v0.5.0-alpha.14", "v0.5.0-alpha.15",
	}
	for name, tc := range map[string]struct {
		pin       string
		published []string
		newer     []string
		err       string
	}{
		"current":                          {pin: "v0.5.0-alpha.15", published: published},
		"one prerelease behind is allowed": {pin: "v0.5.0-alpha.14", published: published, newer: []string{"v0.5.0-alpha.15"}},
		"two behind fails": {
			pin: "v0.5.0-alpha.13", published: published,
			newer: []string{"v0.5.0-alpha.14", "v0.5.0-alpha.15"},
			err:   "2 published versions are newer",
		},
		"prerelease numbers compare numerically, not as text": {
			pin: "v0.5.0-alpha.9", published: published,
			newer: []string{"v0.5.0-alpha.10", "v0.5.0-alpha.13", "v0.5.0-alpha.14", "v0.5.0-alpha.15"},
			err:   "4 published versions are newer",
		},
		"a stable release outranks its prereleases": {
			pin: "v0.5.0-alpha.15", published: append(slicesClone(published), "v0.5.0"),
			newer: []string{"v0.5.0"},
		},
		"a release in flight is ahead of the proxy": {pin: "v0.5.0-alpha.16", published: published},
		"an unpublished pin behind the latest names nothing": {
			pin: "v0.5.0-alpha.11", published: published,
			newer: []string{"v0.5.0-alpha.13", "v0.5.0-alpha.14", "v0.5.0-alpha.15"},
			err:   "not a published version",
		},
	} {
		t.Run(name, func(t *testing.T) {
			newer, err := check(tc.pin, tc.published, 1)
			assert.Equal(t, tc.newer, newer)
			if tc.err == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.err)
		})
	}

	t.Run("max-lag 0 wants the latest", func(t *testing.T) {
		_, err := check("v0.5.0-alpha.14", published, 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "to v0.5.0-alpha.15", "the error names the fix")
	})

	t.Run("unpublished is its own error", func(t *testing.T) {
		_, err := check("v0.5.0-alpha.11", published, 5)
		assert.True(t, errors.Is(err, errUnpublished))
	})
}

func slicesClone(s []string) []string { return append([]string(nil), s...) }

// loopbackServer serves h on 127.0.0.1 only.
func loopbackServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchVersions(t *testing.T) {
	var asked string
	srv := loopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		_, _ = w.Write([]byte("v0.5.0-alpha.14\n\nnot-a-version\nv0.5.0-alpha.15\n"))
	}))

	got, err := fetchVersions(t.Context(), srv.Client(), srv.URL+"/", "hop.top/kit")
	require.NoError(t, err)
	assert.Equal(t, "/hop.top/kit/@v/list", asked, "the proxy protocol's list endpoint")
	assert.Equal(t, []string{"v0.5.0-alpha.14", "v0.5.0-alpha.15"}, got)
}

func TestFetchVersions_Failures(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		srv := loopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not found: unknown module", http.StatusNotFound)
		}))
		_, err := fetchVersions(t.Context(), srv.Client(), srv.URL, "hop.top/kit")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "404")
	})
	t.Run("empty list", func(t *testing.T) {
		srv := loopbackServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		_, err := fetchVersions(t.Context(), srv.Client(), srv.URL, "hop.top/kit")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no versions listed")
	})
}
