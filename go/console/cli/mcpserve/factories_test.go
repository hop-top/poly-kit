package mcpserve_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/internal/testhome"
	"hop.top/kit/go/console/cli/mcpserve"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/core/upgrade"
	"hop.top/kit/go/runtime/bus"
)

// TestMigrateCommandIsNeverAnMCPTool pins that an adopter mounting
// kit's migrate tree after cli.New does not put it on MCP: no migrate
// tool is listed, and a call naming one is refused.
func TestMigrateCommandIsNeverAnMCPTool(t *testing.T) {
	testhome.Isolate(t)
	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	ready := make(chan string, 1)
	b.Subscribe("kit.serve.service.ready_reported", func(_ context.Context, e bus.Event) error {
		if p, ok := e.Payload.(serve.EventPayload); ok && p.Service == mcpserve.ServiceName {
			ready <- p.Address
		}
		return nil
	})
	r := newServeRoot(t, mcpserve.With(mcpserve.Config{}), cli.WithServiceBus(b))
	mcpCommands(r)
	r.Cmd.AddCommand(upgrade.MigrateCommand(upgrade.NewMigrator("test", "1.0.0"), r.Viper))

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	r.SetArgs([]string{"serve", "mcp", "--mcp-addr", "127.0.0.1:0"})
	go func() { errCh <- r.Execute(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-errCh
	})
	var endpoint string
	select {
	case endpoint = <-ready:
	case err := <-errCh:
		t.Fatalf("serve returned before mcp was ready: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("mcp never reported ready")
	}

	sess := dialMCP(t, endpoint, nil, nil)
	names := toolNames(t, sess)
	require.Contains(t, names, "ping", "the catalog is not empty")
	for _, n := range names {
		assert.False(t, strings.HasPrefix(n, "migrate."), "%q is a tool", n)
	}
	for _, verb := range []string{"status", "run", "rollback", "history"} {
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "migrate." + verb, Arguments: map[string]any{}})
		assert.True(t, err != nil || res.IsError, "migrate.%s must be refused", verb)
	}
}
