package cli

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/serve"
)

// TestServiceStatus_ReportsTheSupervisorsRecord pins that `service
// status` reads the supervisor's record of the run, not only each
// service's own Ready: a service that crashed under the isolate
// failure policy without resetting Ready is reported failed and not
// ready, with the runtime reason and its error, while its sibling
// keeps its fields and reads ready.
func TestServiceStatus_ReportsTheSupervisorsRecord(t *testing.T) {
	isolateRuntime(t)
	isolateHome(t)
	crashy := newToggle("crashy")
	d := authRoot(t, WithServices(&toggleService{name: "core", crash: make(chan struct{})}, crashy))
	d.Viper.Set("services.core.enabled", true)
	d.Viper.Set("services.crashy.enabled", true)
	d.Viper.Set("services.failure_policy", "isolate")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.SetArgs([]string{"service", "start"})
	errCh := make(chan error, 1)
	go func() { errCh <- d.Execute(ctx) }()

	status := func() map[string]serviceStatusRow { return statusRowsByName(t) }
	require.Eventually(t, func() bool {
		rows := status()
		return rows["core"].State == string(serve.StateReady) && rows["crashy"].State == string(serve.StateReady)
	}, 10*time.Second, 10*time.Millisecond)

	close(crashy.crash)
	var rows map[string]serviceStatusRow
	require.Eventually(t, func() bool {
		rows = status()
		return rows["crashy"].State == string(serve.StateFailed)
	}, 10*time.Second, 10*time.Millisecond)
	assert.True(t, crashy.Ready(), "the service's own Ready was never reset")
	got := rows["crashy"]
	assert.False(t, got.Ready, "the supervisor's record wins over the service's Ready")
	assert.False(t, got.Optional)
	assert.Equal(t, "runtime", got.Reason)
	assert.Equal(t, "crashed", got.Error)
	core := rows["core"]
	assert.True(t, core.Ready)
	assert.Equal(t, string(serve.StateReady), core.State)
	assert.Empty(t, core.Reason)
	assert.Empty(t, core.Error)
	assert.NotZero(t, core.PID)

	cancel()
	select {
	case <-errCh:
	case <-time.After(10 * time.Second):
		t.Fatal("the instance did not stop")
	}
}

// statusRowsByName runs `service status --format json` against the
// running instance and returns its rows by service, nil when no
// instance answers.
func statusRowsByName(t *testing.T) map[string]serviceStatusRow {
	t.Helper()
	c := authRoot(t, WithService(newToggle("probe")))
	out, _, code, err := run(t, c, "service", "status", "--format", "json")
	if err != nil || code != 0 {
		return nil
	}
	var got serviceStatusOutput
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	rows := map[string]serviceStatusRow{}
	for _, r := range got.Services {
		rows[r.Service] = r
	}
	return rows
}

// TestServiceStatusRows_BeforeTheRunBegins pins the answer the control
// channel gives while the supervisor has not handed its record over
// yet: every selected service not in the run, with the optionality the
// run resolved.
func TestServiceStatusRows_BeforeTheRunBegins(t *testing.T) {
	reg := serve.NewRegistry()
	reg.Register(newToggle("core"))
	reg.Register(newToggle("aux"))
	configs := map[string]serve.Config{"core": {Enabled: true}, "aux": {Enabled: true, Optional: true}}
	rows := serviceStatusRows(reg, []string{"core", "aux"}, configs, &runRecord{})()
	require.Len(t, rows, 2)
	for _, r := range rows {
		assert.Equal(t, string(serve.StateNotInRun), r.State, r.Service)
	}
	assert.False(t, rows[0].Optional)
	assert.True(t, rows[1].Optional)
}

// quitter reports ready, then returns cleanly from Start once quit is
// closed, without resetting its own Ready.
type quitter struct {
	name  string
	quit  chan struct{}
	ready atomic.Bool
}

func (q *quitter) Name() string { return q.name }
func (q *quitter) Start(ctx context.Context, report func()) error {
	q.ready.Store(true)
	report()
	select {
	case <-q.quit:
	case <-ctx.Done():
	}
	return nil
}
func (q *quitter) Ready() bool                { return q.ready.Load() }
func (q *quitter) Stop(context.Context) error { return nil }

// TestServiceStatus_StoppedIsNotReady pins that a service whose Start
// returned on its own while the run goes on reads stopped and not
// ready, whatever its own Ready still says.
func TestServiceStatus_StoppedIsNotReady(t *testing.T) {
	isolateRuntime(t)
	isolateHome(t)
	q := &quitter{name: "oneshot", quit: make(chan struct{})}
	d := authRoot(t, WithServices(newToggle("core"), q))
	d.Viper.Set("services.core.enabled", true)
	d.Viper.Set("services.oneshot.enabled", true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.SetArgs([]string{"service", "start"})
	errCh := make(chan error, 1)
	go func() { errCh <- d.Execute(ctx) }()

	require.Eventually(t, func() bool { return statusRowsByName(t)["oneshot"].State == "ready" },
		10*time.Second, 10*time.Millisecond)
	close(q.quit)
	var rows map[string]serviceStatusRow
	require.Eventually(t, func() bool {
		rows = statusRowsByName(t)
		return rows["oneshot"].State == "stopped"
	}, 10*time.Second, 10*time.Millisecond)
	assert.True(t, q.Ready(), "its own Ready was never reset")
	assert.False(t, rows["oneshot"].Ready, "stopped is not ready")
	assert.Empty(t, rows["oneshot"].Reason, "a clean return is no failure")
	assert.True(t, rows["core"].Ready, "the run goes on")

	cancel()
	select {
	case <-errCh:
	case <-time.After(10 * time.Second):
		t.Fatal("the instance did not stop")
	}
}
