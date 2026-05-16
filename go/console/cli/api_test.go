package cli_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
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

	var buf bytes.Buffer
	r.Cmd.SetErr(&buf)

	ctx, cancel := context.WithCancel(t.Context())

	errCh := make(chan error, 1)
	r.SetArgs([]string{"serve", "--addr", ":0"})
	go func() {
		errCh <- r.Execute(ctx)
	}()

	// wait for "Listening" message
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			cancel()
			t.Fatal("timed out waiting for server to start")
		default:
			if strings.Contains(buf.String(), "Listening") {
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
