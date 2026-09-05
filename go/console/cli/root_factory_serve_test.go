package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
)

// invocationBarrier releases its callers only once n of them are
// waiting at the same time. A serialized runner never gets past the
// first caller, so a barrier crossed is parallelism proven.
type invocationBarrier struct {
	n       int
	mu      sync.Mutex
	arrived int
	release chan struct{}
}

func newBarrier(n int) *invocationBarrier {
	return &invocationBarrier{n: n, release: make(chan struct{})}
}

func (b *invocationBarrier) wait(ctx context.Context) error {
	b.mu.Lock()
	b.arrived++
	if b.arrived == b.n {
		close(b.release)
	}
	b.mu.Unlock()
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
		b.mu.Lock()
		got := b.arrived
		b.mu.Unlock()
		return fmt.Errorf("only %d of %d invocations in flight after 1s", got, b.n)
	}
}

// inFlightMeter records the highest number of concurrent callers.
type inFlightMeter struct {
	cur, max atomic.Int32
}

func (m *inFlightMeter) enter() {
	c := m.cur.Add(1)
	for {
		old := m.max.Load()
		if c <= old || m.max.CompareAndSwap(old, c) {
			return
		}
	}
}

func (m *inFlightMeter) leave() { m.cur.Add(-1) }

// factoryFixture builds the tool the factory tests serve: one
// construction, run once for the serving root and once per served
// invocation, exactly as an adopter's main would write it.
type factoryFixture struct {
	socket  string
	barrier *invocationBarrier
	meter   *inFlightMeter
	opts    []func(*cli.Root)
	built   atomic.Int32
}

func newFactoryFixture(t *testing.T, n int, opts ...func(*cli.Root)) *factoryFixture {
	t.Helper()
	return &factoryFixture{
		socket:  shortSocketPath(t),
		barrier: newBarrier(n),
		meter:   &inFlightMeter{},
		opts:    opts,
	}
}

// build is the factory. It names itself through the fixture, the way
// a main package's newRoot names itself.
func (f *factoryFixture) build() *cli.Root {
	f.built.Add(1)
	destructiveOn := func(s cmdsurface.Surface) cmdsurface.Policy {
		return cmdsurface.Policy{AllowDestructiveOn: []cmdsurface.Surface{s}}
	}
	opts := []func(*cli.Root){
		cli.WithSocket(cli.SocketConfig{Path: f.socket, Policy: destructiveOn(cmdsurface.SurfaceRPC)}),
		cli.WithAPI(cli.APIConfig{Addr: "127.0.0.1:0", Policy: destructiveOn(cmdsurface.SurfaceREST)}),
		cli.WithRootFactory(f.build),
	}
	opts = append(opts, f.opts...)
	r := cli.New(cli.Config{
		Name: "test", Version: "0.1.0", DisableValidate: true,
		Globals: []cli.Flag{{Name: "region", Default: "us", Usage: "region"}},
	}, opts...)
	mountFactoryLeaves(r, f)
	return r
}

func mountFactoryLeaves(r *cli.Root, f *factoryFixture) {
	read := map[string]string{"kit/side-effect": "read"}
	r.Cmd.AddCommand(
		&cobra.Command{
			Use: "gather", Short: "wait for the others", Annotations: read,
			RunE: func(cmd *cobra.Command, _ []string) error {
				if err := f.barrier.wait(cmd.Context()); err != nil {
					return err
				}
				cmd.Print("gathered")
				return nil
			},
		},
		&cobra.Command{
			Use: "linger", Short: "hold the meter", Annotations: read,
			RunE: func(cmd *cobra.Command, _ []string) error {
				f.meter.enter()
				defer f.meter.leave()
				time.Sleep(30 * time.Millisecond)
				cmd.Print("lingered")
				return nil
			},
		},
		&cobra.Command{
			Use: "where", Short: "print the region and overrides", Annotations: read,
			RunE: func(cmd *cobra.Command, _ []string) error {
				cmd.Print(r.Viper.GetString("region"))
				if v, ok := r.ConfigOverrides()["k"]; ok {
					cmd.Printf(" k=%v", v)
				}
				return nil
			},
		},
		&cobra.Command{
			Use: "nuke", Short: "destroy",
			Annotations: map[string]string{"kit/side-effect": "destructive"},
			RunE: func(cmd *cobra.Command, _ []string) error {
				cmd.Print("destroyed")
				return nil
			},
		},
		&cobra.Command{
			Use: "wipe", Short: "destroy with a typed token",
			Annotations: map[string]string{
				"kit/side-effect":       "destructive",
				"kit/destructive-token": "required",
			},
			RunE: func(cmd *cobra.Command, _ []string) error {
				cmd.Print("wiped")
				return nil
			},
		},
		&cobra.Command{
			Use: "shell", Short: "needs a terminal",
			Annotations: map[string]string{"kit/side-effect": "interactive"},
			RunE: func(cmd *cobra.Command, _ []string) error {
				cmd.Print("shelled")
				return nil
			},
		},
	)
	echo := &cobra.Command{
		Use: "echo", Short: "print hi", Annotations: read,
		RunE: func(cmd *cobra.Command, _ []string) error {
			loud, _ := cmd.Flags().GetBool("loud")
			if loud {
				cmd.Print("HI")
			} else {
				cmd.Print("hi")
			}
			return nil
		},
	}
	echo.Flags().Bool("loud", false, "shout")
	r.Cmd.AddCommand(echo)
}

// serveAPIInBackground starts the api service and returns its base
// URL once it is bound.
func serveAPIInBackground(t *testing.T, r *cli.Root, args []string) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r.SetArgs(args)
	errCh := make(chan error, 1)
	go func() { errCh <- r.Execute(ctx) }()

	svc, ok := r.ServeRegistry().Lookup(cli.APIServiceName)
	require.True(t, ok)
	addressed, ok := svc.(serve.Addressed)
	require.True(t, ok, "the api service must report its bound address")

	deadline := time.Now().Add(10 * time.Second)
	for !svc.Ready() || addressed.Addr() == "" {
		select {
		case err := <-errCh:
			cancel()
			t.Fatalf("serve returned before the api was bound: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("api never became ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return "http://" + addressed.Addr(), func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("serve did not return after cancellation")
		}
	}
}

func httpCall(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(b)
}

func resultOf(t *testing.T, body string) api.CommandResult {
	t.Helper()
	var res api.CommandResult
	require.NoError(t, json.Unmarshal([]byte(body), &res), body)
	return res
}

// tokenIn pulls the expected token out of a typed-token refusal, the
// way a caller that read the refusal would.
func tokenIn(t *testing.T, text string) string {
	t.Helper()
	const marker = "requires --confirm-token="
	i := strings.Index(text, marker)
	require.GreaterOrEqual(t, i, 0, "the refusal must name the token: %s", text)
	rest := text[i+len(marker):]
	if end := strings.IndexAny(rest, "\"\\ \n"); end >= 0 {
		rest = rest[:end]
	}
	require.NotEmpty(t, rest)
	return rest
}

func TestRootFactorySocketRunsInvocationsInParallel(t *testing.T) {
	const n = 4
	f := newFactoryFixture(t, n)
	r := f.build()
	stop := serveInBackground(t, r, []string{"serve", "socket"}, f.socket)
	defer stop()

	var wg sync.WaitGroup
	results := make(chan socket.Response, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- callSocket(t, f.socket, socket.Request{Path: []string{"gather"}})
		}()
	}
	wg.Wait()
	close(results)
	for resp := range results {
		require.True(t, resp.Ok, "%+v", resp.Error)
		require.NotNil(t, resp.Result)
		assert.Equal(t, 0, resp.Result.ExitCode, resp.Result.Stderr)
		assert.Equal(t, "gathered", resp.Result.Stdout)
	}
	// One tree for the serving root, one for validation, one per
	// invocation: the factory ran, and ran per call.
	assert.GreaterOrEqual(t, f.built.Load(), int32(n+2))
}

func TestRootFactoryRESTRunsInvocationsInParallel(t *testing.T) {
	const n = 4
	f := newFactoryFixture(t, n)
	r := f.build()
	base, stop := serveAPIInBackground(t, r, []string{"serve", "api"})
	defer stop()

	var wg sync.WaitGroup
	type reply struct {
		status int
		body   string
	}
	replies := make(chan reply, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, body := httpCall(t, http.MethodGet, base+"/v1/commands/gather", "")
			replies <- reply{status, body}
		}()
	}
	wg.Wait()
	close(replies)
	for rep := range replies {
		require.Equal(t, http.StatusOK, rep.status, rep.body)
		assert.Equal(t, "gathered", resultOf(t, rep.body).Stdout)
	}
}

func TestRootFactoryFlagsDoNotLeakBetweenInvocations(t *testing.T) {
	f := newFactoryFixture(t, 1)
	r := f.build()
	stop := serveInBackground(t, r, []string{"serve", "socket"}, f.socket)
	defer stop()

	resp := callSocket(t, f.socket, socket.Request{
		Path: []string{"echo"}, Flags: map[string]any{"loud": true},
	})
	require.True(t, resp.Ok)
	assert.Equal(t, "HI", resp.Result.Stdout)

	resp = callSocket(t, f.socket, socket.Request{Path: []string{"echo"}})
	require.True(t, resp.Ok)
	assert.Equal(t, "hi", resp.Result.Stdout,
		"a flag set by one invocation must not appear in the next")
}

func TestRootFactoryKeepsTheGatesOverTheSocket(t *testing.T) {
	deny := func(_ context.Context, meta cmdsurface.Meta, _ *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		if meta.Caller == "mallory" {
			return cmdsurface.PermissionDecision{Reason: "mallory is not welcome"}
		}
		return cmdsurface.PermissionDecision{Allowed: true}
	}
	f := newFactoryFixture(t, 1, cli.WithPermission(deny))
	r := f.build()
	stop := serveInBackground(t, r, []string{"serve", "socket"}, f.socket)
	defer stop()

	unauthorized := output.UnauthorizedError("").ExitCode

	// Destructive without confirmation: the command's own gate refuses
	// with UNAUTHORIZED; with it, the command runs.
	resp := callSocket(t, f.socket, socket.Request{Path: []string{"nuke"}})
	require.True(t, resp.Ok, "%+v", resp.Error)
	assert.Equal(t, unauthorized, resp.Result.ExitCode)
	assert.Contains(t, resp.Result.Stderr, "--confirm=no (or non-TTY default)")
	assert.Empty(t, resp.Result.Stdout)

	resp = callSocket(t, f.socket, socket.Request{
		Path: []string{"nuke"}, Flags: map[string]any{"confirm": "yes"},
	})
	require.True(t, resp.Ok)
	assert.Equal(t, 0, resp.Result.ExitCode)
	assert.Equal(t, "destroyed", resp.Result.Stdout)

	// Typed token: confirm=yes alone is refused and the refusal names
	// the token; echoing it back completes the command.
	resp = callSocket(t, f.socket, socket.Request{
		Path: []string{"wipe"}, Flags: map[string]any{"confirm": "yes"},
	})
	require.True(t, resp.Ok)
	assert.Equal(t, unauthorized, resp.Result.ExitCode)
	token := tokenIn(t, resp.Result.Stderr)
	resp = callSocket(t, f.socket, socket.Request{
		Path:  []string{"wipe"},
		Flags: map[string]any{"confirm": "yes", "confirm-token": token},
	})
	require.True(t, resp.Ok)
	assert.Equal(t, 0, resp.Result.ExitCode, resp.Result.Stderr)
	assert.Equal(t, "wiped", resp.Result.Stdout)

	// The permission gate answers before any tree is built.
	resp = callSocket(t, f.socket, socket.Request{Path: []string{"echo"}, Caller: "mallory"})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeDenied, resp.Error.Code)

	// Interactive and self-hosting commands never run.
	resp = callSocket(t, f.socket, socket.Request{Path: []string{"shell"}})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeNotInvocable, resp.Error.Code)

	resp = callSocket(t, f.socket, socket.Request{Path: []string{"serve", "socket"}})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeNotFound, resp.Error.Code)
}

func TestRootFactoryKeepsTheGatesOverREST(t *testing.T) {
	f := newFactoryFixture(t, 1)
	r := f.build()
	base, stop := serveAPIInBackground(t, r, []string{"serve", "api"})
	defer stop()

	status, body := httpCall(t, http.MethodPost, base+"/v1/commands/nuke", `{}`)
	assert.Equal(t, http.StatusForbidden, status, body)
	assert.Contains(t, body, "--confirm=no (or non-TTY default)")

	status, body = httpCall(t, http.MethodPost, base+"/v1/commands/nuke",
		`{"flags":{"confirm":"yes"}}`)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "destroyed", resultOf(t, body).Stdout)

	status, body = httpCall(t, http.MethodPost, base+"/v1/commands/wipe",
		`{"flags":{"confirm":"yes"}}`)
	require.Equal(t, http.StatusForbidden, status, body)
	token := tokenIn(t, body)
	status, body = httpCall(t, http.MethodPost, base+"/v1/commands/wipe",
		`{"flags":{"confirm":"yes","confirm-token":"`+token+`"}}`)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "wiped", resultOf(t, body).Stdout)

	// Interactive and self-hosting commands are not mounted at all.
	status, _ = httpCall(t, http.MethodPost, base+"/v1/commands/shell", `{}`)
	assert.Equal(t, http.StatusNotFound, status)
	status, _ = httpCall(t, http.MethodPost, base+"/v1/commands/serve/api", `{}`)
	assert.Equal(t, http.StatusNotFound, status)
}

func TestRootFactoryReplaysTheOperatorsGlobals(t *testing.T) {
	f := newFactoryFixture(t, 1)
	r := f.build()
	// The operator's own command line sets a scalar and a slice
	// global; every served invocation must start from them.
	stop := serveInBackground(t, r,
		[]string{"--region", "eu", "-c", "k=v", "serve", "socket"}, f.socket)
	defer stop()

	resp := callSocket(t, f.socket, socket.Request{Path: []string{"where"}})
	require.True(t, resp.Ok, "%+v", resp.Error)
	assert.Equal(t, "eu k=v", resp.Result.Stdout)
}

func TestRootFactoryIsValidatedBeforeAnythingBinds(t *testing.T) {
	path := shortSocketPath(t)

	t.Run("a tree that fails validation", func(t *testing.T) {
		r := newServeRoot(t,
			cli.WithSocket(cli.SocketConfig{Path: path}),
			cli.WithRootFactory(func() *cli.Root {
				// EnforceValidate is on here and the leaf carries no
				// annotations, so Prepare returns a ValidationError.
				bad := cli.New(cli.Config{Name: "test", Version: "0.1.0"})
				bad.Cmd.AddCommand(&cobra.Command{
					Use:  "bare",
					RunE: func(*cobra.Command, []string) error { return nil },
				})
				return bad
			}),
		)
		err := runServeArgs(t, r, []string{"serve", "socket"}, 2*time.Second)
		require.Error(t, err)
		var oe *output.Error
		require.ErrorAs(t, err, &oe)
		assert.Equal(t, 2, oe.ExitCode)
		assert.Contains(t, err.Error(), "root factory")
	})

	t.Run("the serving root itself", func(t *testing.T) {
		var r *cli.Root
		r = newServeRoot(t,
			cli.WithSocket(cli.SocketConfig{Path: path}),
			cli.WithRootFactory(func() *cli.Root { return r }),
		)
		err := runServeArgs(t, r, []string{"serve", "socket"}, 2*time.Second)
		require.Error(t, err)
		var oe *output.Error
		require.ErrorAs(t, err, &oe)
		assert.Equal(t, 2, oe.ExitCode)
		assert.Contains(t, err.Error(), "serving root")
	})
}

// meterOverSocket fires n concurrent linger calls and waits for them.
func meterOverSocket(t *testing.T, path string, n int) {
	t.Helper()
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := callSocket(t, path, socket.Request{Path: []string{"linger"}})
			assert.True(t, resp.Ok, "%+v", resp.Error)
		}()
	}
	wg.Wait()
}

func TestSharedTreeStillSerializesWithoutAFactory(t *testing.T) {
	path := shortSocketPath(t)
	meter := &inFlightMeter{}
	r := newServeRoot(t, cli.WithSocket(cli.SocketConfig{Path: path}))
	r.Cmd.AddCommand(&cobra.Command{
		Use: "linger", Short: "hold the meter",
		Annotations: map[string]string{"kit/side-effect": "read"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			meter.enter()
			defer meter.leave()
			time.Sleep(30 * time.Millisecond)
			cmd.Print("lingered")
			return nil
		},
	})
	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	meterOverSocket(t, path, 4)
	assert.Equal(t, int32(1), meter.max.Load(),
		"without a factory the shared tree must run one command at a time")
}

func TestRootFactoryLetsInvocationsOverlap(t *testing.T) {
	f := newFactoryFixture(t, 1)
	r := f.build()
	stop := serveInBackground(t, r, []string{"serve", "socket"}, f.socket)
	defer stop()

	meterOverSocket(t, f.socket, 4)
	assert.Greater(t, f.meter.max.Load(), int32(1),
		"with a factory, invocations must overlap")
}
