package mcpserve_test

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/internal/testhome"
	"hop.top/kit/go/console/cli/mcpserve"
	"hop.top/kit/go/console/cli/service"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/runtime/bus"
)

// coreService is the daemon's core: up at once, until canceled.
type coreService struct {
	mu    sync.Mutex
	ready bool
}

func (c *coreService) Name() string { return "core" }

func (c *coreService) Start(ctx context.Context, report func()) error {
	c.mu.Lock()
	c.ready = true
	c.mu.Unlock()
	report()
	<-ctx.Done()
	return nil
}

func (c *coreService) Ready() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ready
}

func (c *coreService) Stop(context.Context) error {
	c.mu.Lock()
	c.ready = false
	c.mu.Unlock()
	return nil
}

// takenAddr returns a loopback address something already listens on.
func takenAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

// coreAndMCPRun is what one [core, mcp] run produced.
type coreAndMCPRun struct {
	err    error                // what serve returned
	failed []serve.EventPayload // the mcp service's failed events
	coreUp bool                 // the core was up just before the cancel
}

// coreAndMCP runs `serve` over [core, mcp] with mcp's port taken, and
// cancels once the supervisor reports ready: the start sequence, the
// mcp failure included, is over by then.
func coreAndMCP(t *testing.T, cfg mcpserve.Config, keys map[string]any) coreAndMCPRun {
	t.Helper()
	return coreAndMCPWhile(t, cfg, keys, nil)
}

// coreAndMCPWhile is coreAndMCP calling during, when set, after the
// supervisor reported ready and before the cancel: while the daemon
// still runs.
func coreAndMCPWhile(t *testing.T, cfg mcpserve.Config, keys map[string]any, during func()) coreAndMCPRun {
	t.Helper()
	testhome.Isolate(t)
	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	var mu sync.Mutex
	var failed []serve.EventPayload
	supReady := make(chan struct{}, 1)
	b.Subscribe("kit.serve.service.failed", func(_ context.Context, e bus.Event) error {
		if p, ok := e.Payload.(serve.EventPayload); ok && p.Service == mcpserve.ServiceName {
			mu.Lock()
			failed = append(failed, p)
			mu.Unlock()
		}
		return nil
	})
	b.Subscribe("kit.serve.supervisor.ready_reported", func(context.Context, bus.Event) error {
		select {
		case supReady <- struct{}{}:
		default:
		}
		return nil
	})

	core := &coreService{}
	r := newServeRoot(t, service.WithService(core), mcpserve.With(cfg), service.WithServiceBus(b))
	r.Cmd.SetErr(&lockedWriter{w: &strings.Builder{}})
	r.Viper.Set("services.core.enabled", true)
	r.Viper.Set("services.mcp.enabled", true)
	r.Viper.Set("services.mcp.addr", takenAddr(t))
	for k, v := range keys {
		r.Viper.Set(k, v)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.SetArgs([]string{"serve"})
	errCh := make(chan error, 1)
	go func() { errCh <- r.Execute(ctx) }()

	coreUp := false
	select {
	case <-supReady:
		coreUp = core.Ready()
		if during != nil {
			during()
		}
		cancel()
	case err := <-errCh:
		mu.Lock()
		defer mu.Unlock()
		return coreAndMCPRun{err: err, failed: append([]serve.EventPayload(nil), failed...)}
	case <-time.After(15 * time.Second):
		t.Fatal("serve neither became ready nor returned")
	}
	select {
	case err := <-errCh:
		mu.Lock()
		defer mu.Unlock()
		return coreAndMCPRun{err: err, failed: append([]serve.EventPayload(nil), failed...), coreUp: coreUp}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after cancellation")
		return coreAndMCPRun{}
	}
}

// TestMCPOptionalByDefaultSurvivesItsPortBeingTaken pins the adopter
// case: a daemon registering mcp optional keeps its core serving when
// the mcp port is in use, reports mcp failed with the bind error, and
// exits 0 on a signal.
func TestMCPOptionalByDefaultSurvivesItsPortBeingTaken(t *testing.T) {
	run := coreAndMCP(t, mcpserve.Config{Optional: true}, nil)
	require.NoError(t, run.err, "an optional mcp failing to bind is not the daemon failing")
	assert.True(t, run.coreUp, "the core kept serving")
	require.Len(t, run.failed, 1)
	assert.True(t, run.failed[0].Optional)
	assert.Equal(t, "start", run.failed[0].Reason)
	assert.Contains(t, run.failed[0].Error, "bind")
}

// TestMCPRequiredStillFailsTheRunWhenItsPortIsTaken pins the default:
// without the marker, mcp failing to bind fails the whole run, as it
// did before.
func TestMCPRequiredStillFailsTheRunWhenItsPortIsTaken(t *testing.T) {
	run := coreAndMCP(t, mcpserve.Config{}, nil)
	var oe *output.Error
	require.ErrorAs(t, run.err, &oe)
	assert.Equal(t, 1, oe.ExitCode)
	require.Len(t, run.failed, 1)
	assert.False(t, run.failed[0].Optional)
}

// TestMCPOptionalKeyOverridesTheRegistrationDefault pins that an
// operator can make the adopter's optional mcp required again.
func TestMCPOptionalKeyOverridesTheRegistrationDefault(t *testing.T) {
	run := coreAndMCP(t, mcpserve.Config{Optional: true},
		map[string]any{"services.mcp.optional": false})
	var oe *output.Error
	require.ErrorAs(t, run.err, &oe)
	assert.Equal(t, 1, oe.ExitCode)
}

// serviceStatusOf runs `service status <args>` against the running
// daemon from a client root and returns stdout.
func serviceStatusOf(t *testing.T, args ...string) string {
	t.Helper()
	var out strings.Builder
	c := newServeRoot(t, service.WithService(&coreService{}), mcpserve.With(mcpserve.Config{}))
	c.Cmd.SetOut(&out)
	c.Cmd.SetErr(&strings.Builder{})
	c.SetArgs(append([]string{"service", "status"}, args...))
	require.NoError(t, c.Execute(t.Context()))
	return out.String()
}

// TestServiceStatusReportsTheOptionalMCPFailure pins what an operator
// of a daemon running an optional MCP service reads when that
// service's port is taken: `service status` reports the core ready and
// mcp failed, optional, with the failure's kind and the bind error —
// in json, and in the table, whose earlier columns keep their places.
func TestServiceStatusReportsTheOptionalMCPFailure(t *testing.T) {
	var jsonOut, tableOut string
	run := coreAndMCPWhile(t, mcpserve.Config{Optional: true}, nil, func() {
		jsonOut = serviceStatusOf(t, "--format", "json")
		tableOut = serviceStatusOf(t, "--format", "table")
	})
	require.NoError(t, run.err)

	var got struct {
		Services []map[string]any `json:"services"`
	}
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &got), jsonOut)
	require.Len(t, got.Services, 2, jsonOut)
	core, mcp := got.Services[0], got.Services[1]
	assert.Equal(t, "core", core["service"])
	assert.Equal(t, true, core["ready"])
	assert.Equal(t, "ready", core["state"])
	assert.Equal(t, false, core["optional"])
	assert.NotContains(t, core, "reason", "a running service has no failure detail")
	assert.NotContains(t, core, "error")
	for _, k := range []string{"addr", "pid"} {
		assert.Contains(t, core, k, "the existing fields stay")
	}

	assert.Equal(t, mcpserve.ServiceName, mcp["service"])
	assert.Equal(t, false, mcp["ready"])
	assert.Equal(t, "failed", mcp["state"])
	assert.Equal(t, true, mcp["optional"])
	assert.Equal(t, "start", mcp["reason"])
	require.Len(t, run.failed, 1)
	assert.Equal(t, run.failed[0].Error, mcp["error"], "the text the failed event carried")
	assert.Contains(t, mcp["error"], "bind")

	lines := strings.Split(strings.TrimSpace(tableOut), "\n")
	require.Len(t, lines, 3, tableOut)
	assert.Equal(t, []string{"SERVICE", "READY", "ADDR", "PID", "STATE", "OPTIONAL", "REASON", "ERROR"},
		strings.Fields(lines[0]))
	mcpRow := lines[2]
	for _, want := range []string{"mcp", "failed", "start", "bind: address already in use"} {
		assert.Contains(t, mcpRow, want, tableOut)
	}
}
