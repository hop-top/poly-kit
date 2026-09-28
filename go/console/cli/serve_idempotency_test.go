package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/transport/api"
)

func TestServeIdempotencyConfig(t *testing.T) {
	cases := []struct {
		name    string
		set     map[string]any
		enabled bool
		ttl     time.Duration
		err     string
	}{
		{"default", nil, true, idemstore.DefaultTTL, ""},
		{"off for the service", map[string]any{"services.api.idempotency.enabled": false}, false, idemstore.DefaultTTL, ""},
		{"off from the environment's string", map[string]any{"services.api.idempotency.enabled": "false"}, false, idemstore.DefaultTTL, ""},
		{"shared ttl", map[string]any{"services.all.idempotency.ttl": "1h"}, true, time.Hour, ""},
		{"service ttl wins", map[string]any{
			"services.all.idempotency.ttl": "1h", "services.api.idempotency.ttl": "10m",
		}, true, 10 * time.Minute, ""},
		{"another service's key", map[string]any{"services.socket.idempotency.enabled": false}, true, idemstore.DefaultTTL, ""},
		{"zero ttl", map[string]any{"services.api.idempotency.ttl": "0s"}, false, 0, "services.api.idempotency.ttl"},
		{"bad ttl", map[string]any{"services.all.idempotency.ttl": "soon"}, false, 0, "services.all.idempotency.ttl"},
		{"bad enabled", map[string]any{"services.api.idempotency.enabled": "maybe"}, false, 0, "services.api.idempotency.enabled"},
		{"unknown key", map[string]any{"services.api.idempotency.window": "1h"}, false, 0, "services.api.idempotency.window"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := viper.New()
			for k, val := range c.set {
				v.Set(k, val)
			}
			got, err := serveIdempotency(v, APIServiceName)
			if c.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), c.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.enabled, got.enabled)
			assert.Equal(t, c.ttl, got.ttl)
		})
	}
}

// idemRoot is authRoot plus "mint", a write that counts its runs.
func idemRoot(t *testing.T, opts ...func(*Root)) (*Root, *atomic.Int32) {
	t.Helper()
	var runs atomic.Int32
	r := authRoot(t, append([]func(*Root){WithAPI(APIConfig{})}, opts...)...)
	r.Cmd.AddCommand(&cobra.Command{
		Use:         "mint",
		Short:       "mint a thing",
		Annotations: map[string]string{"kit/side-effect": "write"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Printf("minted %d", runs.Add(1))
			return nil
		},
	})
	t.Cleanup(func() { _ = r.closeServeIdempotency() })
	return r, &runs
}

func mint(t *testing.T, h http.Handler, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := loopbackRequest(http.MethodPost, "/v1/commands/mint", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(api.HeaderIdempotencyKey, key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// TestAPIServiceIdempotencyOnByDefault serves through the default
// store: the sqlite file in the tool's state directory, opened on the
// first keyed call.
func TestAPIServiceIdempotencyOnByDefault(t *testing.T) {
	isolateHome(t)
	r, runs := idemRoot(t)
	h := projectionHandler(t, r)
	db := filepath.Join(os.Getenv("XDG_STATE_HOME"), "tool", serveIdempotencyFile)

	w := mint(t, h, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	_, err := os.Stat(db)
	assert.True(t, os.IsNotExist(err), "a call without a key opens no store")

	first := mint(t, h, "k1")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	assert.Empty(t, first.Header().Get(api.HeaderIdempotentReplayed))
	second := mint(t, h, "k1")
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	assert.Equal(t, "true", second.Header().Get(api.HeaderIdempotentReplayed))
	assert.JSONEq(t, first.Body.String(), second.Body.String(), "the replay is the first answer")
	assert.EqualValues(t, 2, runs.Load(), "the unkeyed call and the first keyed call ran")

	_, err = os.Stat(db)
	assert.NoError(t, err, "the store lives in the tool's state directory")
}

func TestAPIServiceIdempotencyDisabled(t *testing.T) {
	isolateHome(t)
	r, runs := idemRoot(t, WithServeIdempotencyStore(idemstore.Memory()))
	r.Viper.Set("services.all.idempotency.enabled", false)
	h := projectionHandler(t, r)
	for range 2 {
		w := mint(t, h, "k1")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Empty(t, w.Header().Get(api.HeaderIdempotentReplayed))
	}
	assert.EqualValues(t, 2, runs.Load())
}

// TestServeIdempotencyLedgerIsShared: every service of the process
// answers from one ledger, so a record made on one transport replays
// on another for the same scope.
func TestServeIdempotencyLedgerIsShared(t *testing.T) {
	isolateHome(t)
	store := idemstore.Memory()
	r, _ := idemRoot(t, WithServeIdempotencyStore(store))
	a := r.serveIdempotencyLedger()
	b := r.serveIdempotencyLedger()
	assert.Same(t, a, b)
	require.NoError(t, r.closeServeIdempotency())
	_, hit, err := store.Lookup(t.Context(), "x")
	assert.NoError(t, err, "kit does not close an adopter's store")
	assert.False(t, hit)
}

func TestAPIServiceIdempotencyValidation(t *testing.T) {
	for k, v := range map[string]any{
		"services.api.idempotency.window": "1h",
		"services.all.idempotency.ttl":    "-1s",
	} {
		t.Run(k, func(t *testing.T) {
			isolateHome(t)
			r := authRoot(t, WithAPI(APIConfig{}))
			r.Viper.Set(k, v)
			err := apiSvc(t, r).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), k)
		})
	}
}

// Kit's own store purges what it deems expired, so it keeps records as
// long as the longest ttl any registered service replays them for; a
// service switched off does not count, and with none set it is the
// default.
func TestServeIdempotencyStoreTTLIsTheLongestServiceTTL(t *testing.T) {
	isolateHome(t)
	r, _ := idemRoot(t, WithSocket(SocketConfig{}))
	assert.Equal(t, idemstore.DefaultTTL, r.serveIdempotencyStoreTTL())

	r.Viper.Set("services.socket.idempotency.ttl", "72h")
	assert.Equal(t, 72*time.Hour, r.serveIdempotencyStoreTTL())

	r.Viper.Set("services.socket.idempotency.enabled", false)
	r.Viper.Set("services.all.idempotency.ttl", "2h")
	assert.Equal(t, 2*time.Hour, r.serveIdempotencyStoreTTL(), "only services that replay count")
}

// Kit's store is opened with that ttl: on open it purges a record
// older than the longest service ttl, and keeps one a service would
// still replay.
func TestServeIdempotencyStorePurgesPastTheServiceTTL(t *testing.T) {
	isolateHome(t)
	r, _ := idemRoot(t)
	r.Viper.Set("services.api.idempotency.ttl", "72h")
	db := filepath.Join(os.Getenv("XDG_STATE_HOME"), "tool", serveIdempotencyFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(db), 0o750))

	now := time.Now().UTC()
	seed, err := idemstore.OpenSQLite(db, 1000*time.Hour)
	require.NoError(t, err)
	require.NoError(t, seed.Record(t.Context(), "within", idemstore.Result{Output: []byte("w"), Recorded: now.Add(-48 * time.Hour)}))
	require.NoError(t, seed.Record(t.Context(), "past", idemstore.Result{Output: []byte("p"), Recorded: now.Add(-100 * time.Hour)}))
	require.NoError(t, seed.Close())

	h := projectionHandler(t, r)
	require.Equal(t, http.StatusOK, mint(t, h, "k1").Code, "a keyed call opens the store")
	require.NoError(t, r.closeServeIdempotency())

	check, err := idemstore.OpenSQLite(db, 1000*time.Hour)
	require.NoError(t, err)
	defer check.Close()
	_, hit, err := check.Lookup(t.Context(), "within")
	require.NoError(t, err)
	assert.True(t, hit, "a record within the service's 72h ttl is kept")
	_, hit, err = check.Lookup(t.Context(), "past")
	require.NoError(t, err)
	assert.False(t, hit, "a record past every service's ttl is purged")
}
