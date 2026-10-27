package rpcserve_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/mcpserve"
	"hop.top/kit/go/console/cli/rpcserve"
	"hop.top/kit/go/console/cli/service"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/runtime/bus"
)

// Every kit transport over one shared tree — the api, the socket, mcp
// and rpc — starting in one run while REST calls arrive the moment the
// api is ready. A served call writes the tree's flag values and
// writers under the tree's invocation lock; a service that reflected
// the tree, collected its flags or read the configuration bound to
// them while starting would race with it, and so would two services
// reflecting it at once. Under -race, any such read is a failure.
func TestServicesOverOneTreeStartWhileCallsRun(t *testing.T) {
	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	apiReady := make(chan string, 1)
	allReady := make(chan struct{})
	var once sync.Once
	b.Subscribe("kit.serve.service.ready_reported", func(_ context.Context, e bus.Event) error {
		if p, ok := e.Payload.(serve.EventPayload); ok && p.Service == service.APIServiceName {
			apiReady <- p.Address
		}
		return nil
	})
	b.Subscribe("kit.serve.supervisor.ready_reported", func(context.Context, bus.Event) error {
		once.Do(func() { close(allReady) })
		return nil
	})

	root := newServeRoot(t,
		service.WithAPI(service.APIConfig{}),
		service.WithSocket(service.SocketConfig{}),
		mcpserve.With(mcpserve.Config{}),
		rpcserve.With(rpcserve.Config{}),
		service.WithServiceBus(b),
		ownScope(),
	)
	rpcCommands(root)

	dir, err := os.MkdirTemp("", "sr")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root.SetArgs([]string{"service", "start",
		"--enable", "socket", "--enable", "mcp", "--enable", "rpc",
		"--addr", "127.0.0.1:0", "--socket", filepath.Join(dir, "s.sock"),
		"--mcp-addr", "127.0.0.1:0", "--rpc-addr", "127.0.0.1:0"})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(10 * time.Second):
			t.Error("serve did not return after cancellation")
		}
	})

	var base string
	select {
	case base = <-apiReady:
	case err := <-errCh:
		errCh <- err
		t.Fatalf("serve returned before the api was ready: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("the api never reported ready")
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}

	// Calls run back to back from the api's readiness until every
	// service is up, and a few beyond.
	call := func() {
		resp, err := http.Get(base + "/v1/commands/ping")
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
	deadline := time.After(15 * time.Second)
	for up := false; !up; {
		select {
		case <-allReady:
			up = true
		case err := <-errCh:
			errCh <- err
			t.Fatalf("serve returned before every service was ready: %v", err)
		case <-deadline:
			t.Fatal("the services never all reported ready")
		default:
			call()
		}
	}
	for range 5 {
		call()
	}
}
