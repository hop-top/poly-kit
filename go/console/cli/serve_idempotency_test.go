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
