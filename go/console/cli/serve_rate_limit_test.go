package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
)

func TestServeRateLimitDefaultsFollowExposure(t *testing.T) {
	for _, v := range []*viper.Viper{nil, viper.New()} {
		_, on, err := serveRateLimit(v, "api", true)
		require.NoError(t, err)
		assert.False(t, on, "off on loopback")
		cfg, on, err := serveRateLimit(v, "api", false)
		require.NoError(t, err)
		assert.True(t, on, "on beyond loopback")
		assert.Equal(t, cmdsurface.RateLimit{}, cfg, "nothing set: every rule takes the kit default")
	}
}

func TestServeRateLimitEnabledOverridesExposure(t *testing.T) {
	v := viper.New()
	v.Set("services.all.rate_limit.enabled", "true") // as the environment carries it
	_, on, err := serveRateLimit(v, "socket", true)
	require.NoError(t, err)
	assert.True(t, on, "services.all turns it on for a loopback service")

	v.Set("services.api.rate_limit.enabled", false)
	_, on, err = serveRateLimit(v, "api", false)
	require.NoError(t, err)
	assert.False(t, on, "the service's key beats services.all")
}

func TestServeRateLimitMergesKeyByKey(t *testing.T) {
	v := viper.New()
	v.Set("services.all.rate_limit.read.per_minute", 10)
	v.Set("services.all.rate_limit.read.burst", 7)
	v.Set("services.api.rate_limit.read.burst", "2")
	v.Set("services.api.rate_limit.destructive.per_minute", 1)

	cfg, _, err := serveRateLimit(v, "api", false)
	require.NoError(t, err)
	assert.Equal(t, cmdsurface.RateRule{PerMinute: 10, Burst: 2}, cfg.Read)
	assert.Equal(t, cmdsurface.RateRule{PerMinute: 1}, cfg.Destructive)
	assert.Equal(t, cmdsurface.RateRule{}, cfg.Write)

	cfg, _, err = serveRateLimit(v, "socket", true)
	require.NoError(t, err)
	assert.Equal(t, cmdsurface.RateRule{PerMinute: 10, Burst: 7}, cfg.Read, "api's keys stay api's")
}

func TestServeRateLimitRefusesBadValues(t *testing.T) {
	cases := map[string]struct {
		key  string
		val  any
		want string
	}{
		"zero rate":       {"services.api.rate_limit.read.per_minute", 0, "services.api.rate_limit.read.per_minute: 0 is out of range; want 1 to 2147483647 (set enabled: false to lift the limit)"},
		"negative burst":  {"services.all.rate_limit.write.burst", -3, "services.all.rate_limit.write.burst: -3 is out of range"},
		"fraction":        {"services.api.rate_limit.write.burst", 1.5, "1.5 is not a whole number"},
		"word":            {"services.api.rate_limit.destructive.per_minute", "lots", `"lots" is not a whole number`},
		"not a boolean":   {"services.api.rate_limit.enabled", "maybe", `services.api.rate_limit.enabled: "maybe" is not a boolean`},
		"unknown key":     {"services.api.rate_limit.read.rate", 5, `unknown key "rate"; rate_limit.read accepts per_minute, burst`},
		"unknown tier":    {"services.all.rate_limit.reads.burst", 5, `unknown key "reads"`},
		"refused on loop": {"services.api.rate_limit.read.burst", 0, "out of range"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			v := viper.New()
			v.Set(c.key, c.val)
			_, _, err := serveRateLimit(v, "api", name == "refused on loop")
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

// getN issues n GETs and returns their statuses and the last response.
func getN(t *testing.T, url string, n int) ([]int, *http.Response) {
	t.Helper()
	var statuses []int
	var last *http.Response
	for range n {
		resp, _ := get(t, url, nil)
		statuses = append(statuses, resp.StatusCode)
		last = resp
	}
	return statuses, last
}

// TestAPIServiceRateLimitByExposure drives the api service through the
// real serve path: beyond loopback the limit is on without being named,
// on loopback it is off until enabled.
func TestAPIServiceRateLimitByExposure(t *testing.T) {
	cases := []struct {
		name    string
		addr    string
		enabled any
		limited bool
	}{
		{"beyond loopback: on by default", "0.0.0.0:0", nil, true},
		{"beyond loopback: switched off", "0.0.0.0:0", false, false},
		{"loopback: off by default", "127.0.0.1:0", nil, false},
		{"loopback: switched on", "127.0.0.1:0", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateHome(t)
			rec := &auditRecorder{}
			r := authRoot(t, WithAPI(APIConfig{Addr: c.addr, InsecureRemote: true, InsecureNoPolicy: true}),
				WithAuditSinks(rec.spec()))
			r.Viper.Set("services.api.rate_limit.read.per_minute", 1)
			r.Viper.Set("services.api.rate_limit.read.burst", 2)
			if c.enabled != nil {
				r.Viper.Set("services.all.rate_limit.enabled", c.enabled)
			}
			base, stop := serveAPI(t, r)
			defer stop()
			url := strings.Replace(base, "0.0.0.0", "127.0.0.1", 1) + "/v1/commands/list"

			statuses, last := getN(t, url, 3)
			if !c.limited {
				assert.Equal(t, []int{200, 200, 200}, statuses)
				return
			}
			assert.Equal(t, []int{200, 200, http.StatusTooManyRequests}, statuses)
			assert.Equal(t, "60", last.Header.Get("Retry-After"), "one token a minute")
			_, _, err := rec.last(t)
			assert.ErrorIs(t, err, cmdsurface.ErrRateLimited, "the refusal is audited")
		})
	}
}

func TestAPIServiceRefusesABadRateLimitAtValidate(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAPI(APIConfig{}))
	r.Viper.Set("services.all.rate_limit.write.per_minute", 0)
	oe := usageErr(t, runServeExpect(t, r, []string{"serve", "api"}, 2*time.Second))
	assert.Contains(t, oe.Message, "services.all.rate_limit.write.per_minute")
}

// The socket takes the loopback column: no limit until enabled.
func TestSocketServiceRateLimit(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := map[bool]string{false: "off by default", true: "switched on"}[enabled]
		t.Run(name, func(t *testing.T) {
			isolateHome(t)
			path := tmpSocket(t)
			r := authRoot(t, WithSocket(SocketConfig{Path: path}))
			r.Viper.Set("services.socket.rate_limit.read.burst", 1)
			if enabled {
				r.Viper.Set("services.socket.rate_limit.enabled", true)
			}
			stop := serveSocket(t, r, path)
			defer stop()

			first := socketCall(t, path, socket.Request{Path: []string{"list"}})
			require.True(t, first.Ok, "%+v", first.Error)
			second := socketCall(t, path, socket.Request{Path: []string{"list"}})
			if !enabled {
				assert.True(t, second.Ok, "%+v", second.Error)
				return
			}
			require.False(t, second.Ok)
			assert.Equal(t, socket.CodeRateLimited, second.Error.Code)
			assert.Positive(t, second.Error.RetryAfterMs)
		})
	}
}

func TestServeBridgeOptionsForSelectsTheColumn(t *testing.T) {
	isolateHome(t)
	r := authRoot(t)
	r.Viper.Set("services.heartbeat.rate_limit.read.burst", 1)
	invoke := func(opts []cmdsurface.Option) error {
		b := cmdsurface.New(r.Cmd, opts...)
		b.Expose("*", cmdsurface.SurfaceBus)
		var err error
		for range 2 {
			_, err = b.Invoke(t.Context(), cmdsurface.Invocation{Path: []string{"list"},
				Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceBus}})
		}
		return err
	}

	remote, err := ServeBridgeOptions(r, "heartbeat")
	require.NoError(t, err)
	assert.ErrorIs(t, invoke(remote), cmdsurface.ErrRateLimited,
		"an unstated exposure takes the beyond-loopback default")

	local, err := ServeBridgeOptionsFor(r, "heartbeat", true)
	require.NoError(t, err)
	assert.NoError(t, invoke(local))

	r.Viper.Set("services.heartbeat.rate_limit.read.burst", 0)
	refusing, err := ServeBridgeOptionsFor(r, "heartbeat", true)
	require.Error(t, err)
	assert.ErrorIs(t, invoke(refusing), cmdsurface.ErrPermissionDenied,
		"a configuration that does not resolve refuses every call")
	assert.Error(t, ValidateServeBridge(r, "heartbeat"))
}

// WithServeRateLimit is the code default under the rate_limit keys:
// each tier key it sets applies when no configuration source sets
// that key, and enablement still follows exposure and enabled.
func TestWithServeRateLimitIsTheCodeDefault(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithServeRateLimit(cmdsurface.RateLimit{
		Read: cmdsurface.RateRule{PerMinute: 1, Burst: 1},
	}))
	invokeN := func(n int) error {
		opts, err := ServeBridgeOptions(r, "heartbeat")
		require.NoError(t, err)
		b := cmdsurface.New(r.Cmd, opts...)
		b.Expose("*", cmdsurface.SurfaceBus)
		for range n {
			if _, err := b.Invoke(t.Context(), cmdsurface.Invocation{Path: []string{"list"},
				Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceBus}}); err != nil {
				return err
			}
		}
		return nil
	}

	assert.ErrorIs(t, invokeN(2), cmdsurface.ErrRateLimited, "the code's burst of one applies")

	r.Viper.Set("services.all.rate_limit.read.burst", 2)
	assert.NoError(t, invokeN(2), "a configured key beats the code default")
	assert.ErrorIs(t, invokeN(3), cmdsurface.ErrRateLimited)

	r.Viper.Set("services.heartbeat.rate_limit.enabled", false)
	assert.NoError(t, invokeN(3), "enabled: false lifts a code-set limit too")
}
