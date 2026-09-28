package cli

import (
	"net/http"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
)

func TestServeConcurrencyIsOnByDefault(t *testing.T) {
	for _, v := range []*viper.Viper{nil, viper.New()} {
		cfg, on, err := serveConcurrency(v, "socket")
		require.NoError(t, err)
		assert.True(t, on, "on wherever the service listens")
		assert.Equal(t, cmdsurface.DefaultConcurrency(), cfg)
	}
}

func TestServeConcurrencyMergesKeyByKey(t *testing.T) {
	v := viper.New()
	v.Set("services.all.concurrency.max_inflight", 8)
	v.Set("services.all.concurrency.max_queue", "16") // as the environment carries it
	v.Set("services.api.concurrency.max_queue", 0)
	v.Set("services.socket.concurrency.enabled", false)

	cfg, on, err := serveConcurrency(v, "api")
	require.NoError(t, err)
	assert.True(t, on)
	assert.Equal(t, cmdsurface.Concurrency{MaxInflight: 8, MaxQueue: 0}, cfg, "max_queue 0 is no queue")

	cfg, on, err = serveConcurrency(v, "mcp")
	require.NoError(t, err)
	assert.True(t, on)
	assert.Equal(t, cmdsurface.Concurrency{MaxInflight: 8, MaxQueue: 16}, cfg, "api's keys stay api's")

	_, on, err = serveConcurrency(v, "socket")
	require.NoError(t, err)
	assert.False(t, on, "the service's key switches it off")
}

func TestServeConcurrencyRefusesBadValues(t *testing.T) {
	cases := map[string]struct {
		key  string
		val  any
		want string
	}{
		"zero in flight": {"services.api.concurrency.max_inflight", 0, "services.api.concurrency.max_inflight: 0 is out of range; want 1 to 2147483647"},
		"negative queue": {"services.all.concurrency.max_queue", -1, "services.all.concurrency.max_queue: -1 is out of range; want 0 (no queue) to 2147483647"},
		"fraction":       {"services.api.concurrency.max_queue", 2.5, "2.5 is not a whole number"},
		"word":           {"services.api.concurrency.max_inflight", "many", `"many" is not a whole number`},
		"not a boolean":  {"services.api.concurrency.enabled", "sometimes", `services.api.concurrency.enabled: "sometimes" is not a boolean`},
		"unknown key":    {"services.api.concurrency.max_waiting", 5, `unknown key "max_waiting"; concurrency accepts enabled, max_inflight, max_queue`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			v := viper.New()
			v.Set(c.key, c.val)
			_, _, err := serveConcurrency(v, "api")
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

// addSlow adds a read command that signals started and then holds
// until release is closed.
func addSlow(r *Root) (started chan struct{}, release chan struct{}) {
	started, release = make(chan struct{}, 1), make(chan struct{})
	r.Cmd.AddCommand(&cobra.Command{
		Use:         "slow",
		Short:       "hold a slot",
		Annotations: map[string]string{"kit/side-effect": "read"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			started <- struct{}{}
			<-release
			cmd.Print("slow")
			return nil
		},
	})
	return started, release
}

// within runs call, failing the test — and letting the slow command
// go — if it waits on the busy slot instead of being refused.
func within[T any](t *testing.T, release chan struct{}, call func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- call() }()
	select {
	case v := <-done:
		return v
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the call waited for the busy slot instead of being refused")
		var zero T
		return zero
	}
}

// TestAPIServiceRefusesOverloaded drives the api service through the
// real serve path: with the one slot a shared tree allows taken and no
// queue, the next call is 503 overloaded with Retry-After, audited.
func TestAPIServiceRefusesOverloaded(t *testing.T) {
	isolateHome(t)
	rec := &auditRecorder{}
	r := authRoot(t, WithAPI(APIConfig{}), WithAuditSinks(rec.spec()))
	started, release := addSlow(r)
	r.Viper.Set("services.api.concurrency.max_queue", 0)
	base, stop := serveAPI(t, r)
	defer stop()

	slow := make(chan int, 1)
	go func() {
		resp, _ := get(t, base+"/v1/commands/slow", nil)
		slow <- resp.StatusCode
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("slow never started")
	}

	type answer struct {
		resp *http.Response
		body []byte
	}
	a := within(t, release, func() answer {
		resp, body := get(t, base+"/v1/commands/list", nil)
		return answer{resp, body}
	})
	resp, body := a.resp, a.body
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, string(body))
	assert.Equal(t, "1", resp.Header.Get("Retry-After"))
	assert.Contains(t, string(body), `"code":"overloaded"`)
	_, _, err := rec.last(t)
	assert.ErrorIs(t, err, cmdsurface.ErrOverloaded, "the refusal is audited")

	close(release)
	assert.Equal(t, http.StatusOK, <-slow)
	resp, _ = get(t, base+"/v1/commands/list", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the slot is free again")
}

func TestAPIServiceRefusesABadConcurrencyAtValidate(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAPI(APIConfig{}))
	r.Viper.Set("services.all.concurrency.max_inflight", 0)
	oe := usageErr(t, runServeExpect(t, r, []string{"serve", "api"}, 2*time.Second))
	assert.Contains(t, oe.Message, "services.all.concurrency.max_inflight")
}

func TestSocketServiceRefusesOverloaded(t *testing.T) {
	isolateHome(t)
	path := tmpSocket(t)
	r := authRoot(t, WithSocket(SocketConfig{Path: path}))
	started, release := addSlow(r)
	r.Viper.Set("services.socket.concurrency.max_queue", 0)
	stop := serveSocket(t, r, path)
	defer stop()

	slow := make(chan socket.Response, 1)
	go func() { slow <- socketCall(t, path, socket.Request{Path: []string{"slow"}}) }()
	<-started
	resp := within(t, release, func() socket.Response {
		return socketCall(t, path, socket.Request{Path: []string{"list"}})
	})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeOverloaded, resp.Error.Code)
	assert.Equal(t, int64(1000), resp.Error.RetryAfterMs)
	close(release)
	assert.True(t, (<-slow).Ok)
}

func TestSocketServiceRefusesABadConcurrencyAtValidate(t *testing.T) {
	isolateHome(t)
	path := tmpSocket(t)
	r := authRoot(t, WithSocket(SocketConfig{Path: path}))
	r.Viper.Set("services.socket.concurrency.max_queue", -2)
	oe := usageErr(t, runServeExpect(t, r, []string{"serve", "socket"}, 2*time.Second))
	assert.Contains(t, oe.Message, "services.socket.concurrency.max_queue")
}

// A service built from ServeBridgeOptions — mcp, rpc — gets the gate.
func TestServeBridgeOptionsInstallTheCapacityGate(t *testing.T) {
	isolateHome(t)
	r := authRoot(t)
	r.Viper.Set("services.rpc.concurrency.max_queue", 3)
	opts, err := ServeBridgeOptionsFor(r, "rpc", true)
	require.NoError(t, err)
	load, ok := cmdsurface.New(r.Cmd, opts...).Capacity()
	require.True(t, ok, "the gate is on without being named")
	assert.Equal(t, cmdsurface.CapacityLoad{MaxInflight: 1, MaxQueue: 3}, load,
		"a shared tree runs one at a time")

	r.Viper.Set("services.rpc.concurrency.enabled", false)
	opts, err = ServeBridgeOptions(r, "rpc")
	require.NoError(t, err)
	_, ok = cmdsurface.New(r.Cmd, opts...).Capacity()
	assert.False(t, ok, "enabled: false lifts it")

	r.Viper.Set("services.rpc.concurrency.max_inflight", 0)
	_, err = ServeBridgeOptions(r, "rpc")
	require.Error(t, err)
	assert.Error(t, ValidateServeBridge(r, "rpc"))
}
