package cli_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/transport/api"
)

func TestWithAPI_AddsServeCommand(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "test", Version: "0.1.0",
		DisableValidate: true,
	}, cli.WithAPI(cli.APIConfig{}))

	found := false
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "serve" {
			found = true
			break
		}
	}
	assert.True(t, found, "serve command must be registered")
}

func TestWithAPI_WithAuth_AddsTokenCommand(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "test", Version: "0.1.0",
		DisableValidate: true,
	}, cli.WithAPI(cli.APIConfig{
		Auth: func(r *http.Request) (any, error) { return nil, nil },
	}))

	found := false
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "token" {
			found = true
			// verify subcommands
			names := make(map[string]bool)
			for _, sub := range c.Commands() {
				names[sub.Name()] = true
			}
			assert.True(t, names["claims"], "token claims must exist")
			assert.True(t, names["decode"], "token decode must exist")
			break
		}
	}
	assert.True(t, found, "token command must be registered when Auth is set")
}

// TestWithAPI_WithAuth_ValidatingRootStarts pins that setting Auth on
// a validating root — every root by default — does not make the root
// refuse to start: the token leaves WithAPI mounts carry the
// annotations the validator requires of every leaf.
func TestWithAPI_WithAuth_ValidatingRootStarts(t *testing.T) {
	r := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t"},
		cli.WithStatus(cli.StatusConfig{}),
		cli.WithAPI(cli.APIConfig{
			Auth: func(r *http.Request) (any, error) { return nil, nil },
		}))

	err := r.Validate()
	require.NoError(t, err, "a root that authenticates its api must still validate")

	for _, c := range r.Cmd.Commands() {
		if c.Name() != "token" {
			continue
		}
		for _, leaf := range c.Commands() {
			s, ok := cli.GetSideEffect(leaf)
			assert.True(t, ok, "%s declares kit/side-effect", leaf.CommandPath())
			assert.Equal(t, cli.SideEffectRead, s, "%s is a read", leaf.CommandPath())
			_, ok = cli.GetIdempotency(leaf)
			assert.True(t, ok, "%s declares kit/idempotent", leaf.CommandPath())
			assert.NotEmpty(t, leaf.Long, "%s has a Long", leaf.CommandPath())
		}
	}
}

func TestWithAPI_WithoutAuth_SkipsTokenCommand(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "test", Version: "0.1.0",
		DisableValidate: true,
	}, cli.WithAPI(cli.APIConfig{}))

	for _, c := range r.Cmd.Commands() {
		assert.NotEqual(t, "token", c.Name(),
			"token command must not be registered without Auth")
	}
}

func TestWithAPI_ServeHasAddrFlag(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "test", Version: "0.1.0",
		DisableValidate: true,
	}, cli.WithAPI(cli.APIConfig{Addr: ":9090"}))

	for _, c := range r.Cmd.Commands() {
		if c.Name() == "serve" {
			f := c.Flags().Lookup("addr")
			require.NotNil(t, f, "--addr flag must exist on serve")
			assert.Equal(t, ":9090", f.DefValue,
				"--addr default must come from APIConfig")
			return
		}
	}
	t.Fatal("serve command not found")
}

func TestNew_WithoutWithAPI_NoServeCommand(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "test", Version: "0.1.0",
		DisableValidate: true,
	})

	for _, c := range r.Cmd.Commands() {
		assert.NotEqual(t, "serve", c.Name(),
			"serve must not exist without WithAPI")
	}
}

func TestServe_StartsAndStops(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "test", Version: "0.1.0",
		DisableValidate: true,
	}, cli.WithAPI(cli.APIConfig{
		Addr: ":0",
		Handlers: func(router *api.Router) {
			router.Handle("GET", "/health", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
		},
	}))

	// The serve goroutine writes this buffer while the poll loop below
	// reads it, so it must be synchronized: an unguarded bytes.Buffer
	// shared across goroutines is a data race the -race detector
	// rightly rejects.
	buf := &syncBuffer{}
	r.Cmd.SetErr(buf)

	ctx, cancel := context.WithCancel(t.Context())

	errCh := make(chan error, 1)
	r.SetArgs([]string{"serve", "--addr", "127.0.0.1:0"})
	go func() {
		errCh <- r.Execute(ctx)
	}()

	// Justification for the changed expectation: the leaf command
	// printed a bespoke "Listening on <addr>" line to stderr. The api
	// service reports readiness through the supervisor's lifecycle
	// trace instead, which carries the same resolved address under a
	// structured key. The behavior asserted — the server comes up on
	// `serve`, and canceling the context exits cleanly — is unchanged.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			cancel()
			t.Fatal("timed out waiting for server to start")
		default:
			if strings.Contains(buf.String(), "ready_reported") {
				cancel()
				err := <-errCh
				assert.NoError(t, err, "serve must exit cleanly on cancel")
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestServe_NoAuthFlag(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "test", Version: "0.1.0",
		DisableValidate: true,
	}, cli.WithAPI(cli.APIConfig{
		Auth: func(r *http.Request) (any, error) { return nil, nil },
	}))

	for _, c := range r.Cmd.Commands() {
		if c.Name() == "serve" {
			f := c.Flags().Lookup("no-auth")
			require.NotNil(t, f, "--no-auth flag must exist when Auth is configured")
			return
		}
	}
	t.Fatal("serve command not found")
}

func TestServe_OpenAPIConfigured(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "test", Version: "0.1.0",
		DisableValidate: true,
	}, cli.WithAPI(cli.APIConfig{
		OpenAPI: &api.OpenAPIConfig{
			Title:   "Test API",
			Version: "1.0.0",
		},
	}))

	found := false
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "serve" {
			found = true
			break
		}
	}
	assert.True(t, found, "serve command must exist with OpenAPI config")
}

// syncBuffer is a bytes.Buffer safe for concurrent Write and String.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
