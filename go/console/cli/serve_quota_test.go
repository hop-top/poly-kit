package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/memory"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
)

func TestServeQuotaResolution(t *testing.T) {
	v := viper.New()
	_, on, err := serveQuota(v, "api")
	require.NoError(t, err)
	assert.False(t, on, "off by default, loopback or not")

	v.Set("services.all.quota.ops", 100)
	v.Set("services.api.quota.window", "24h")
	v.Set("services.api.quota.per", "tenant")
	q, on, err := serveQuota(v, "api")
	require.NoError(t, err)
	assert.True(t, on, "a limit turns it on")
	assert.Equal(t, cmdsurface.Quota{Scope: "api", Per: cmdsurface.QuotaPerTenant, Window: 24 * time.Hour, Ops: 100}, q)

	q, _, err = serveQuota(v, "mcp")
	require.NoError(t, err)
	assert.Equal(t, time.Hour, q.Window, "the service's window key is its own")

	v.Set("services.api.quota.enabled", false)
	_, on, err = serveQuota(v, "api")
	require.NoError(t, err)
	assert.False(t, on)

	for key, val := range map[string]any{
		"services.api.quota.per":     "planet",
		"services.api.quota.window":  "10ms",
		"services.api.quota.ops":     -1,
		"services.api.quota.bytes":   "lots",
		"services.api.quota.surplus": 1,
	} {
		t.Run(key, func(t *testing.T) {
			v := viper.New()
			v.Set(key, val)
			_, _, err := serveQuota(v, "api")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "services.api.quota")
		})
	}

	v = viper.New()
	v.Set("services.api.quota.enabled", true)
	_, _, err = serveQuota(v, "api")
	require.ErrorContains(t, err, "true with no limit")
}

// quotaRoot serves the api and the socket on loopback with a quota of
// two calls an hour, counted in store.
func quotaRoot(t *testing.T, store kv.Store, sock string) *Root {
	t.Helper()
	opts := []func(*Root){
		WithAPI(APIConfig{Addr: "127.0.0.1:0"}),
		WithUsageStore(store),
		WithQuotaCommand(),
	}
	if sock != "" {
		opts = append(opts, WithSocket(SocketConfig{Path: sock}))
	}
	r := authRoot(t, opts...)
	r.Viper.Set("services.all.quota.ops", 2)
	return r
}

func TestQuotaOverRESTPersistsAcrossRestarts(t *testing.T) {
	store := memory.New()
	r := quotaRoot(t, store, "")
	base, stop := serveAPI(t, r)
	for range 2 {
		resp, body := get(t, base+"/v1/commands/list", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	}
	resp, body := get(t, base+"/v1/commands/list", nil)
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode, string(body))
	var ae api.APIError
	require.NoError(t, json.Unmarshal(body, &ae))
	assert.Equal(t, cmdsurface.CodeQuotaExceeded, ae.Code)
	secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	require.NoError(t, err)
	reset := time.Until(time.Now().Truncate(time.Hour).Add(time.Hour))
	assert.InDelta(t, reset.Seconds(), float64(secs), 2, "Retry-After is the window reset")
	stop()

	r = quotaRoot(t, store, "")
	base, stop = serveAPI(t, r)
	defer stop()
	resp, body = get(t, base+"/v1/commands/list", nil)
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode, "a restart resets nothing: %s", body)
}

func TestQuotaOverTheSocket(t *testing.T) {
	path := tmpSocket(t)
	r := quotaRoot(t, memory.New(), path)
	stop := serveSocket(t, r, path)
	defer stop()
	for range 2 {
		resp := socketCall(t, path, socket.Request{Path: []string{"list"}})
		require.True(t, resp.Ok, "%+v", resp.Error)
	}
	resp := socketCall(t, path, socket.Request{Path: []string{"list"}})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeQuotaExceeded, resp.Error.Code)
	assert.Positive(t, resp.Error.RetryAfterMs)
}

func TestQuotaShowAndReset(t *testing.T) {
	store := memory.New()
	r := quotaRoot(t, store, "")
	base, stop := serveAPI(t, r)
	for range 2 {
		resp, _ := get(t, base+"/v1/commands/list", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
	resp, _ := get(t, base+"/v1/commands/list", nil)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)

	// The management verbs are withheld from the served surface.
	resp, body := get(t, base+"/v1/commands", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), `"name":"quota show","summary":"Show each caller's usage in the current window"`)
	assert.Regexp(t, `"name":"quota show"[^}]*"invocable":false,"reason":"management-only"`, string(body))
	stop()

	run := func(args ...string) (string, error) {
		cli := quotaRoot(t, store, "")
		var out bytes.Buffer
		cli.Cmd.SetOut(&out)
		cli.SetArgs(args)
		err := cli.Execute(t.Context())
		return out.String(), err
	}
	out, err := run("quota", "show", "--format", "json")
	require.NoError(t, err)
	var rows []QuotaRow
	require.NoError(t, json.Unmarshal([]byte(out), &rows), out)
	require.Len(t, rows, 1)
	assert.Equal(t, "api", rows[0].Service)
	assert.Equal(t, "address/127.0.0.1", rows[0].Caller)
	assert.EqualValues(t, 2, rows[0].Ops)
	assert.EqualValues(t, 2, rows[0].MaxOps)

	_, err = run("quota", "reset")
	require.Error(t, err, "a caller or --all is required")

	out, err = run("quota", "reset", "address/127.0.0.1")
	require.NoError(t, err)
	assert.Contains(t, out, "reset 1")

	_, err = run("quota", "reset", "address/127.0.0.1")
	require.Error(t, err, "nothing left to reset")

	r = quotaRoot(t, store, "")
	base, stop = serveAPI(t, r)
	defer stop()
	resp, body = get(t, base+"/v1/commands/list", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the reset quota starts over: %s", body)
}

func TestQuotaMisconfigurationIsRefusedAtValidation(t *testing.T) {
	for _, svc := range []string{"api", "socket"} {
		t.Run(svc, func(t *testing.T) {
			opts := []func(*Root){WithAPI(APIConfig{Addr: "127.0.0.1:0"})}
			if svc == "socket" {
				opts = []func(*Root){WithSocket(SocketConfig{Path: tmpSocket(t)})}
			}
			r := authRoot(t, opts...)
			r.Viper.Set("services."+svc+".quota.per", "planet")
			oe := usageErr(t, runServeExpect(t, r, []string{"serve", svc}, 2*time.Second))
			assert.Contains(t, oe.Message, "services."+svc+".quota.per")
		})
	}
}
