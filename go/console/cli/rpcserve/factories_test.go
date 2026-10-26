package rpcserve_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/internal/testhome"
	"hop.top/kit/go/console/cli/rpcserve"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/core/upgrade"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
)

// TestMigrateCommandIsNeverServedOverRPC pins that an adopter mounting
// kit's migrate tree after cli.New does not put it on rpc: every leaf
// answers not_found.
func TestMigrateCommandIsNeverServedOverRPC(t *testing.T) {
	testhome.Isolate(t)
	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	ready := make(chan string, 1)
	b.Subscribe("kit.serve.service.ready_reported", func(_ context.Context, e bus.Event) error {
		if p, ok := e.Payload.(serve.EventPayload); ok && p.Service == rpcserve.ServiceName {
			ready <- p.Address
		}
		return nil
	})
	r := newServeRoot(t, rpcserve.With(rpcserve.Config{}), cli.WithServiceBus(b))
	r.Cmd.AddCommand(upgrade.MigrateCommand(upgrade.NewMigrator("test", "1.0.0"), r.Viper))

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	r.SetArgs([]string{"serve", "rpc", "--rpc-addr", "127.0.0.1:0"})
	go func() { errCh <- r.Execute(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-errCh
	})
	var base string
	select {
	case base = <-ready:
	case err := <-errCh:
		t.Fatalf("serve returned before rpc was ready: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("rpc never reported ready")
	}

	c := client(base, protocols[0])
	for _, verb := range []string{"status", "run", "rollback", "history"} {
		_, err := c.Invoke(t.Context(), connect.NewRequest(&cmdsurfacev1.Invocation{Path: []string{"migrate", verb}}))
		require.Error(t, err, "migrate %s ran over rpc", verb)
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "migrate %s", verb)
	}
}
