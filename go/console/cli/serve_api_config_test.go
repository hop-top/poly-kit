package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// configFixture builds a root with a destructive command and a couple
// of ordinary ones, over whichever APIConfig the caller supplies.
func configFixture(t *testing.T, cfg APIConfig) *Root {
	t.Helper()
	cfg.Addr = ":0"

	r := New(Config{Name: "fix", Version: "1.0.0", DisableValidate: true},
		WithAPI(cfg))

	r.Cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "list things",
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = cmd.OutOrStdout().Write([]byte("listed"))
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	})

	// Destructive AND confirmation-gated: permitting the tier must
	// not also waive the confirmation.
	r.Cmd.AddCommand(&cobra.Command{
		Use:   "purge",
		Short: "purge the store",
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = cmd.OutOrStdout().Write([]byte("purged"))
		},
		Annotations: map[string]string{
			"kit/side-effect":           "destructive-shared",
			"kit/requires-confirmation": "true",
		},
	})

	admin := &cobra.Command{Use: "admin", Short: "admin things"}
	admin.AddCommand(&cobra.Command{
		Use:         "reset",
		Short:       "reset state",
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{"kit/side-effect": "write-shared"},
	})
	r.Cmd.AddCommand(admin)

	return r
}

// discoveryOf fetches the discovery listing keyed by command name.
func discoveryOf(t *testing.T, h http.Handler) map[string]api.DiscoveryEntry {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var doc api.DiscoveryDocument
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))

	out := map[string]api.DiscoveryEntry{}
	for _, e := range doc.Commands {
		out[e.Name] = e
	}
	return out
}

func TestZeroConfigWithholdsDestructive(t *testing.T) {
	// The default: an adopter who sets nothing gets no destructive
	// commands over REST.
	h := projectionHandler(t, configFixture(t, APIConfig{}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/commands/purge", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"a destructive command must not be mounted by default")

	entry := discoveryOf(t, h)["purge"]
	assert.False(t, entry.Invocable)
	assert.Equal(t, "unauthorized-destructive", entry.Reason)
}

func TestPolicyPermitsDestructiveOverREST(t *testing.T) {
	h := projectionHandler(t, configFixture(t, APIConfig{
		Policy: cmdsurface.Policy{
			AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceREST},
		},
	}))

	// Now mounted and listed as invocable.
	entry := discoveryOf(t, h)["purge"]
	assert.True(t, entry.Invocable, "policy naming REST must mount it")
	assert.Empty(t, entry.Reason)
	assert.Equal(t, http.MethodPost, entry.Method)
	assert.True(t, entry.RequiresConfirmation,
		"discovery must still advertise the confirmation requirement")

	// Permitting the tier does NOT waive confirmation.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/commands/purge", nil))
	assert.Equal(t, http.StatusPreconditionRequired, rec.Code,
		"a confirmation-gated command still needs its token")

	// With the token, it runs.
	req := httptest.NewRequest(http.MethodPost, "/v1/commands/purge", nil)
	req.Header.Set(api.ConfirmTokenHeader, "tok-1")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var res api.CommandResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Contains(t, res.Stdout, "purged")
}

func TestHideKeepsCommandOffREST(t *testing.T) {
	h := projectionHandler(t, configFixture(t, APIConfig{
		Hide: []string{"list"},
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands/list", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"a withheld command must not be mounted")

	entry := discoveryOf(t, h)["list"]
	assert.False(t, entry.Invocable)
	assert.Equal(t, ReasonWithheldByConfig, entry.Reason,
		"a config withholding must be distinguishable from a policy refusal")
	assert.Empty(t, entry.Route)
}

func TestHideAcceptsWildcard(t *testing.T) {
	h := projectionHandler(t, configFixture(t, APIConfig{
		Hide: []string{"admin *"},
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/v1/commands/admin/reset", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)

	entries := discoveryOf(t, h)
	assert.Equal(t, ReasonWithheldByConfig, entries["admin reset"].Reason)
	// A sibling outside the pattern is untouched.
	assert.True(t, entries["list"].Invocable,
		"the pattern must not withhold unrelated commands")
}

func TestHideDoesNotAffectOtherSurfaces(t *testing.T) {
	// Withholding is REST-only. The bridge the projection builds is
	// internal, so assert on an equivalent bridge: the same Expose
	// then Hide sequence must leave CLI/Lib/MCP enabled.
	root := configFixture(t, APIConfig{}).Cmd
	b := cmdsurface.New(root)
	b.Expose("*", cmdsurface.SurfaceREST)
	b.Hide("list", cmdsurface.SurfaceREST)

	var found bool
	for _, leaf := range b.Leaves() {
		if leaf.PathKey() != "list" {
			continue
		}
		found = true
		assert.False(t, leaf.Enabled[cmdsurface.SurfaceREST],
			"REST must be withheld")
		assert.True(t, leaf.Enabled[cmdsurface.SurfaceCLI],
			"the CLI must be untouched")
		assert.True(t, leaf.Enabled[cmdsurface.SurfaceMCP],
			"MCP must be untouched")
	}
	require.True(t, found, "fixture must expose a list leaf")
}

func TestHideWinsOverPolicyPermission(t *testing.T) {
	// Both knobs at once: the withhold list is the later, narrower
	// statement and must win.
	h := projectionHandler(t, configFixture(t, APIConfig{
		Policy: cmdsurface.Policy{
			AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceREST},
		},
		Hide: []string{"purge"},
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/commands/purge", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)

	entry := discoveryOf(t, h)["purge"]
	assert.False(t, entry.Invocable)
	assert.Equal(t, ReasonWithheldByConfig, entry.Reason)
}

func TestExposeNarrowsTheProjection(t *testing.T) {
	// Empty Expose reaches the whole tree; a non-empty one is an
	// allow-list, and everything outside it is off REST.
	h := projectionHandler(t, configFixture(t, APIConfig{
		Expose: []string{"list"},
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands/list", nil))
	assert.Equal(t, http.StatusOK, rec.Code, "the named command stays mounted")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/v1/commands/admin/reset", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"a command outside Expose must not be mounted")

	entries := discoveryOf(t, h)
	assert.True(t, entries["list"].Invocable)
	assert.False(t, entries["admin reset"].Invocable)
	assert.Equal(t, ReasonWithheldByConfig, entries["admin reset"].Reason)
}
