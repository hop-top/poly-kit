package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/output"
)

// gateService is a service whose Stop can be held open, so a test can
// act while the run is draining. It records the order of its calls.
type gateService struct {
	name    string
	ready   atomic.Bool
	stopped atomic.Bool
	// stopping is closed when Stop is entered; release, when set,
	// holds Stop until it is closed.
	stopping chan struct{}
	release  chan struct{}
	once     sync.Once
}

func newGateService(name string, hold bool) *gateService {
	g := &gateService{name: name, stopping: make(chan struct{})}
	if hold {
		g.release = make(chan struct{})
	}
	return g
}

func (g *gateService) Name() string { return g.name }

func (g *gateService) Start(ctx context.Context, ready func()) error {
	g.ready.Store(true)
	ready()
	<-ctx.Done()
	return nil
}

func (g *gateService) Ready() bool { return g.ready.Load() }

func (g *gateService) Stop(context.Context) error {
	g.once.Do(func() { close(g.stopping) })
	if g.release != nil {
		<-g.release
	}
	g.ready.Store(false)
	g.stopped.Store(true)
	return nil
}

// execCall is one call to the stand-in for exec.
type execCall struct {
	path, dir         string
	args, env         []string
	stoppedBeforeExec bool
}

// stubExec swaps the process-image replacement for fn.
func stubExec(t *testing.T, fn func(path string, args, env []string) error) {
	t.Helper()
	prev := execSelf
	execSelf = fn
	t.Cleanup(func() { execSelf = prev })
}

// startInstance runs `service start <name>` on r in the background and
// waits for svc to be ready. The returned channel yields Execute's
// result.
func startInstance(t *testing.T, r *Root, svc *gateService) (<-chan error, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r.SetArgs([]string{"service", "start", svc.name})
	errCh := make(chan error, 1)
	go func() { errCh <- r.Execute(ctx) }()
	require.Eventually(t, svc.Ready, 10*time.Second, 5*time.Millisecond, "instance never ready")
	// The control socket opens before the services start; wait for it
	// to answer too.
	path := controlPathOf(t, r)
	require.Eventually(t, func() bool { _, ok := pollStatus(path); return ok },
		10*time.Second, 5*time.Millisecond, "control socket never answered")
	return errCh, cancel
}

type clientResult struct {
	out  string
	code int
	err  error
}

// restartClient runs `service restart` on a fresh root in the
// background.
func restartClient(t *testing.T, args ...string) <-chan clientResult {
	t.Helper()
	c := authRoot(t, WithService(newGateService("svc", false)))
	ch := make(chan clientResult, 1)
	go func() {
		out, _, code, err := run(t, c, append([]string{"service", "restart", "--format", "json"}, args...)...)
		ch <- clientResult{out, code, err}
	}()
	return ch
}

func awaitClient(t *testing.T, ch <-chan clientResult) clientResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(20 * time.Second):
		t.Fatal("service restart did not return")
		return clientResult{}
	}
}

func awaitStopping(t *testing.T, svc *gateService) {
	t.Helper()
	select {
	case <-svc.stopping:
	case <-time.After(10 * time.Second):
		close(svc.release)
		t.Fatal("the instance never began draining")
	}
}

func awaitRun(t *testing.T, errCh <-chan error) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("the instance did not end its run")
		return nil
	}
}

func TestService_RestartLeafShape(t *testing.T) {
	r := authRoot(t, WithAPI(APIConfig{}))
	restart := child(r.ServiceCommand(), "restart")
	require.NotNil(t, restart)
	assert.Equal(t, "true", restart.Annotations["kit/self-hosting"])
	f := restart.Flags().Lookup("wait")
	require.NotNil(t, f)
	assert.Equal(t, defaultRestartWait.String(), f.DefValue)
}

// No instance: restart answers like status, NOT_FOUND at exit 3. Unlike
// stop, whose goal (nothing running) already holds, restart's goal (a
// fresh instance running) does not, and restart does not start one.
func TestService_RestartWithoutAnInstance(t *testing.T) {
	isolateRuntime(t)
	r := authRoot(t, WithAPI(APIConfig{}))
	_, _, code, err := run(t, r, "service", "restart")
	require.Error(t, err)
	assert.Equal(t, output.ExitNotFound, code)
	assert.Contains(t, err.Error(), "tool is not running")
}

func TestService_RestartDryRunIsAPlan(t *testing.T) {
	isolateRuntime(t)
	r := authRoot(t, WithAPI(APIConfig{}))
	out, _, code, err := run(t, r, "service", "restart", "--dry-run", "--format", "json")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "instance:tool")
	assert.Contains(t, out, `"restart"`)
}

// The instance drains, then replaces itself with the launch binary,
// argv, environment and directory; the client waits for the new
// instance and prints its status.
func TestService_RestartDrainsThenReexecs(t *testing.T) {
	isolateRuntime(t)

	// -C moves the working directory during a run; the new image must
	// start from the launch directory regardless.
	launch := t.TempDir()
	prevDir := launchDir
	launchDir = launch
	t.Cleanup(func() { launchDir = prevDir })
	t.Chdir(t.TempDir())

	svc := newGateService("svc", false)
	d := authRoot(t, WithService(svc))
	errCh, _ := startInstance(t, d, svc)

	calls := make(chan execCall, 1)
	stubExec(t, func(path string, args, env []string) error {
		dir, _ := os.Getwd()
		calls <- execCall{path: path, dir: dir, args: args, env: env, stoppedBeforeExec: svc.stopped.Load()}
		return nil
	})

	client := restartClient(t, "--wait", "15s")

	var call execCall
	select {
	case call = <-calls:
	case <-time.After(10 * time.Second):
		t.Fatal("the instance never re-executed")
	}
	require.NoError(t, awaitRun(t, errCh), "the drain was clean")
	assert.True(t, call.stoppedBeforeExec, "every service stopped before the exec")
	exe, err := os.Executable()
	require.NoError(t, err)
	assert.Equal(t, exe, call.path)
	assert.Equal(t, launchArgs, call.args)
	assert.Equal(t, os.Args, call.args)
	assert.Equal(t, launchEnv, call.env)
	gotDir, _ := filepath.EvalSymlinks(call.dir)
	wantDir, _ := filepath.EvalSymlinks(launch)
	assert.Equal(t, wantDir, gotDir, "the new image starts in the launch directory")

	// The stand-in for the new image: a fresh run of the same tool.
	next := newGateService("svc", false)
	n := authRoot(t, WithService(next))
	_, cancelNext := startInstance(t, n, next)
	defer cancelNext()

	res := awaitClient(t, client)
	require.NoError(t, res.err)
	assert.Equal(t, 0, res.code)
	var got serviceStatusOutput
	require.NoError(t, json.Unmarshal([]byte(res.out), &got), res.out)
	rows := got.Services
	require.Len(t, rows, 1)
	assert.Equal(t, "svc", rows[0].Service)
	assert.True(t, rows[0].Ready)
	assert.Equal(t, os.Getpid(), rows[0].PID)
}

// A failed exec after the drain ends the instance non-zero with the
// reason on stderr, and the client reports it.
func TestService_RestartExecFailureExitsNonZero(t *testing.T) {
	isolateRuntime(t)
	svc := newGateService("svc", false)
	d := authRoot(t, WithService(svc))
	stderr := &lockedStringWriter{b: &strings.Builder{}}
	d.Cmd.SetErr(stderr)
	errCh, _ := startInstance(t, d, svc)

	stubExec(t, func(string, []string, []string) error {
		return errors.New("exec format error")
	})
	res := awaitClient(t, restartClient(t, "--wait", "10s"))

	runErr := awaitRun(t, errCh)
	require.Error(t, runErr)
	var ce *output.Error
	require.ErrorAs(t, runErr, &ce)
	assert.Equal(t, output.ExitGeneric, ce.ExitCode)
	assert.Contains(t, runErr.Error(), "exec format error")
	assert.Contains(t, stderr.String(), "exec format error", "the reason is on the instance's stderr")
	assert.True(t, svc.stopped.Load(), "the drain ran first")

	require.Error(t, res.err)
	assert.Equal(t, output.ExitGeneric, res.code)
	assert.Contains(t, res.err.Error(), "exec format error")
}

// A shutdown signal (here the caller's context) during the restart's
// drain wins: the instance exits as a stop.
func TestService_RestartYieldsToASignalDuringTheDrain(t *testing.T) {
	isolateRuntime(t)
	svc := newGateService("svc", true)
	d := authRoot(t, WithService(svc))
	errCh, cancel := startInstance(t, d, svc)
	stubExec(t, func(string, []string, []string) error {
		t.Error("re-executed after a shutdown signal")
		return nil
	})

	client := restartClient(t, "--wait", "10s")
	awaitStopping(t, svc)
	cancel()
	close(svc.release)

	require.NoError(t, awaitRun(t, errCh))
	res := awaitClient(t, client)
	require.Error(t, res.err)
	assert.Contains(t, res.err.Error(), "not restarted: a shutdown signal arrived during the drain")
}

// A stop during the restart's drain wins too; a restart during a stop
// is refused.
func TestService_StopOutranksRestart(t *testing.T) {
	isolateRuntime(t)
	svc := newGateService("svc", true)
	d := authRoot(t, WithService(svc))
	errCh, _ := startInstance(t, d, svc)
	stubExec(t, func(string, []string, []string) error {
		t.Error("re-executed after a stop")
		return nil
	})

	client := restartClient(t, "--wait", "10s")
	awaitStopping(t, svc)

	stopper := authRoot(t, WithService(newGateService("svc", false)))
	stopped := make(chan clientResult, 1)
	go func() {
		out, _, code, err := run(t, stopper, "service", "stop", "--format", "json")
		stopped <- clientResult{out, code, err}
	}()
	require.Eventually(t, func() bool {
		d2 := authRoot(t, WithService(newGateService("svc", false)))
		// Short: a probe that lands before the stop joins the restart.
		_, _, code, _ := run(t, d2, "service", "restart", "--wait", "200ms")
		return code == output.ExitConflict
	}, 5*time.Second, 20*time.Millisecond, "a restart during a stop is a conflict")
	close(svc.release)

	require.NoError(t, awaitRun(t, errCh))
	st := awaitClient(t, stopped)
	require.NoError(t, st.err)
	assert.JSONEq(t, `{"result":"stopped"}`, st.out)
	res := awaitClient(t, client)
	require.Error(t, res.err)
	assert.Contains(t, res.err.Error(), "not restarted: a stop was requested during the drain")
}

// Without exec(2) both halves refuse, with the way to do it by hand.
func TestService_RestartUnsupportedPlatform(t *testing.T) {
	isolateRuntime(t)
	svc := newGateService("svc", false)
	d := authRoot(t, WithService(svc))
	_, cancel := startInstance(t, d, svc)
	defer cancel()

	prev := reexecSupported
	reexecSupported = false
	t.Cleanup(func() { reexecSupported = prev })

	r := authRoot(t, WithService(newGateService("svc", false)))
	_, _, code, err := run(t, r, "service", "restart")
	require.Error(t, err)
	assert.Equal(t, output.ExitUsage, code)
	assert.Contains(t, err.Error(), "not supported on this platform")

	// The instance refuses the op itself, and keeps running.
	conn, err := net.Dial("unix", controlPathOf(t, d))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, _, err = controlCall(conn, "restart")
	require.Error(t, err)
	var ce *output.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, output.ExitUsage, ce.ExitCode)
	assert.True(t, svc.Ready(), "still serving")
}

// heldService reports ready only once release is closed, so a test
// can settle what an earlier service did before the run is ready.
type heldService struct {
	name    string
	release chan struct{}
	ready   atomic.Bool
}

func (h *heldService) Name() string { return h.name }

func (h *heldService) Start(ctx context.Context, report func()) error {
	select {
	case <-h.release:
	case <-ctx.Done():
		return nil
	}
	h.ready.Store(true)
	report()
	<-ctx.Done()
	return nil
}

func (h *heldService) Ready() bool                { return h.ready.Load() }
func (h *heldService) Stop(context.Context) error { h.ready.Store(false); return nil }

// restartIntoCrashedAux runs `service restart` against an instance
// whose new run serves aux, which crashes after reporting ready
// without resetting its own Ready, and then svc. The new run's svc
// reports ready only once the supervisor recorded aux failed, so
// whatever restart reports, it read the failure. It returns the
// client's result and how long the client took after the new run
// was ready.
func restartIntoCrashedAux(t *testing.T, optional bool) (clientResult, time.Duration) {
	t.Helper()
	isolateRuntime(t)
	isolateHome(t)
	old := newGateService("svc", false)
	d := authRoot(t, WithService(old))
	errCh, _ := startInstance(t, d, old)
	execed := make(chan struct{}, 1)
	stubExec(t, func(string, []string, []string) error { execed <- struct{}{}; return nil })
	client := restartClient(t, "--wait", "10s")
	select {
	case <-execed:
	case <-time.After(10 * time.Second):
		t.Fatal("the instance never re-executed")
	}
	require.NoError(t, awaitRun(t, errCh))

	// The stand-in for the new image.
	aux := newToggle("aux")
	svc := &heldService{name: "svc", release: make(chan struct{})}
	n := authRoot(t, WithServices(aux, svc))
	n.Viper.Set("services.aux.enabled", true)
	n.Viper.Set("services.svc.enabled", true)
	n.Viper.Set("services.aux.optional", optional)
	// A required service's crash leaves the instance running only
	// under isolate; under fail-fast it would end the run.
	n.Viper.Set("services.failure_policy", "isolate")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	n.SetArgs([]string{"service", "start"})
	go func() { _ = n.Execute(ctx) }()

	require.Eventually(t, func() bool { return statusRowsByName(t)["aux"].State == "ready" },
		10*time.Second, 10*time.Millisecond)
	close(aux.crash)
	require.Eventually(t, func() bool { return statusRowsByName(t)["aux"].State == "failed" },
		10*time.Second, 10*time.Millisecond)
	require.True(t, aux.Ready(), "aux never reset its own Ready")
	close(svc.release)
	readyAt := time.Now()
	res := awaitClient(t, client)
	return res, time.Since(readyAt)
}

// TestService_RestartReportsAFailedOptionalService pins that restart
// waits for the required services only: a new run whose optional
// service failed is a successful restart, and the status it prints
// reports that service failed, optional, with its reason and error.
func TestService_RestartReportsAFailedOptionalService(t *testing.T) {
	res, _ := restartIntoCrashedAux(t, true)
	require.NoError(t, res.err)
	assert.Equal(t, 0, res.code)
	var got serviceStatusOutput
	require.NoError(t, json.Unmarshal([]byte(res.out), &got), res.out)
	rows := map[string]serviceStatusRow{}
	for _, r := range got.Services {
		rows[r.Service] = r
	}
	assert.True(t, rows["svc"].Ready)
	aux := rows["aux"]
	assert.Equal(t, "failed", aux.State)
	assert.True(t, aux.Optional)
	assert.False(t, aux.Ready)
	assert.Equal(t, "runtime", aux.Reason)
	assert.Equal(t, "crashed", aux.Error)
}

// TestService_RestartFailsAtOnceOnARequiredFailure pins the other
// side: a required service the new run recorded failed will not come
// up — the supervisor restarts nothing — so restart fails at once,
// exit 1, naming it with its reason, instead of waiting out --wait.
func TestService_RestartFailsAtOnceOnARequiredFailure(t *testing.T) {
	res, took := restartIntoCrashedAux(t, false)
	require.Error(t, res.err)
	assert.Equal(t, output.ExitGeneric, res.code)
	assert.Contains(t, res.err.Error(), "required service aux failed (runtime): crashed")
	assert.Less(t, took, 5*time.Second, "at once, not at --wait (10s)")
}

// TestRestartSettled pins how restart reads one answer of the new run.
func TestRestartSettled(t *testing.T) {
	row := func(ready bool, state string, optional bool) serviceStatusRow {
		return serviceStatusRow{Service: "s", Ready: ready, State: state, Optional: optional}
	}
	for name, c := range map[string]struct {
		rows    []serviceStatusRow
		settled bool
		failed  bool
	}{
		"every row ready":                 {[]serviceStatusRow{row(true, "ready", false), row(true, "ready", true)}, true, false},
		"a failed optional row":           {[]serviceStatusRow{row(true, "ready", false), row(false, "failed", true)}, true, false},
		"a failed required row":           {[]serviceStatusRow{row(true, "ready", false), row(false, "failed", false)}, false, true},
		"ready, but the state disagrees":  {[]serviceStatusRow{row(true, "starting", false)}, false, false},
		"a stopped row":                   {[]serviceStatusRow{row(false, "stopped", false)}, false, false},
		"not started yet":                 {[]serviceStatusRow{row(false, "not_in_run", true)}, false, false},
		"an answer without a state, read": {[]serviceStatusRow{row(true, "", false)}, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			settled, failed := restartSettled(c.rows)
			assert.Equal(t, c.settled, settled)
			assert.Equal(t, c.failed, failed != nil)
		})
	}
}
