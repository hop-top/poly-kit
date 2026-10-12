//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file run the built binary because `service
// restart` replaces the process image: only a real process can show
// that the pid is kept, the services drain before it, and the new run
// reads the configuration afresh.

// binClient runs one client invocation of the binary under home and
// returns its stdout, stderr and exit code.
func binClient(t *testing.T, home string, args ...string) (string, string, int) {
	t.Helper()
	return binClientEnv(t, home, nil, args...)
}

// binClientEnv is binClient with extra environment.
func binClientEnv(t *testing.T, home string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(servedBinary(t), args...)
	cmd.Env = binaryEnv(home, env)
	cmd.Dir = home
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else {
		require.NoError(t, err)
	}
	return stdout.String(), stderr.String(), code
}

type statusRow struct {
	Service string `json:"service"`
	Ready   bool   `json:"ready"`
	Addr    string `json:"addr"`
	PID     int    `json:"pid"`
}

// indexAfter is the index of needle in s at or after from, or -1.
func indexAfter(s, needle string, from int) int {
	if from < 0 || from > len(s) {
		return -1
	}
	i := strings.Index(s[from:], needle)
	if i < 0 {
		return -1
	}
	return from + i
}

// lineAt is the rest of the line s holds at i.
func lineAt(s string, i int) string {
	if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
		return s[i : i+j]
	}
	return s[i:]
}

func TestBinaryServiceRestart(t *testing.T) {
	home := newHome(t, "")
	r := runBinary(t, home, nil,
		"service", "start", "--enable", "heartbeat", "--addr", "127.0.0.1:0")
	require.True(t, r.ready, r.output())
	pid := r.cmd.Process.Pid
	firstURL := func() string { r.mu.Lock(); defer r.mu.Unlock(); return r.apiURL }()

	body := `{"args":["` + strings.Repeat("x", 200) + `"]}`
	status, got := httpDo(t, http.MethodPost, firstURL+"/v1/commands/item/add", body)
	require.Equal(t, http.StatusOK, status, string(got))

	// The configuration changes under the running instance; only a new
	// run reads it.
	dir := filepath.Join(home, ".config", "served")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"),
		[]byte("services:\n  api:\n    body_limit:\n      max_bytes: 64\n"), 0o600))

	out, stderr, code := binClient(t, home, "service", "restart", "--format", "json", "--wait", "30s")
	require.Equal(t, 0, code, "stdout: %s\nstderr: %s\ninstance:\n%s", out, stderr, r.output())

	var rows []statusRow
	require.NoError(t, json.Unmarshal([]byte(out), &rows), out)
	require.Len(t, rows, 2)
	for _, row := range rows {
		assert.True(t, row.Ready, "%s ready when restart returns", row.Service)
		assert.Equal(t, pid, row.PID, "%s answers from the same pid", row.Service)
	}
	select {
	case <-r.done:
		t.Fatalf("the process exited instead of re-executing:\n%s", r.output())
	default:
	}

	// The log: the drain in reverse start order, then the re-exec,
	// then a second run starting both services.
	require.Eventually(t, func() bool {
		return strings.Count(r.output(), "ready_reported object=supervisor") == 2
	}, 10*time.Second, 10*time.Millisecond, r.output())
	log := r.output()
	hbStop := strings.Index(log, "stopped object=service")
	require.GreaterOrEqual(t, hbStop, 0, log)
	assert.Contains(t, lineAt(log, hbStop), "service=heartbeat", "heartbeat started last, stops first")
	apiStop := indexAfter(log, "stopped object=service", hbStop+1)
	require.Greater(t, apiStop, hbStop, log)
	assert.Contains(t, lineAt(log, apiStop), "service=api")
	supStop := indexAfter(log, "stopped object=supervisor", apiStop)
	require.Greater(t, supStop, apiStop, log)
	assert.Contains(t, log[supStop:], "reason=clean-stop")
	reexec := indexAfter(log, "serve: restarting", supStop)
	require.Greater(t, reexec, supStop, "re-executed after the drain\n%s", log)
	restarted := indexAfter(log, "started object=service", reexec)
	require.Greater(t, restarted, reexec, "the new run starts its services\n%s", log)

	// The new run read the changed configuration.
	secondURL := func() string { r.mu.Lock(); defer r.mu.Unlock(); return r.apiURL }()
	status, got = httpDo(t, http.MethodPost, secondURL+"/v1/commands/item/add", body)
	assert.Equal(t, http.StatusRequestEntityTooLarge, status, string(got))

	// The new image handles signals: the first SIGINT drains it cleanly.
	require.NoError(t, r.cmd.Process.Signal(os.Interrupt))
	select {
	case <-r.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("the re-executed instance ignored SIGINT:\n%s", r.output())
	}
	assert.NoError(t, r.err, r.output())
}

// Nothing running: restart answers like `service status`, NOT_FOUND at
// exit 3.
func TestBinaryServiceRestartNothingRunning(t *testing.T) {
	home := newHome(t, "")
	_, stderr, code := binClient(t, home, "service", "restart")
	assert.Equal(t, 3, code, stderr)
	assert.Contains(t, stderr, "served is not running")
}

// A configuration change the new run refuses: the instance drains,
// re-executes, and the new run exits at 2. The client says so as soon
// as the process is gone instead of waiting out --wait.
func TestBinaryServiceRestartIntoABadConfig(t *testing.T) {
	home := newHome(t, "")
	r := runBinary(t, home, nil, "service", "start", "api", "--addr", "127.0.0.1:0")
	require.True(t, r.ready, r.output())

	dir := filepath.Join(home, ".config", "served")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"),
		[]byte("services:\n  api:\n    body_limit:\n      maxbytes: 64\n"), 0o600))

	begin := time.Now()
	_, stderr, code := binClient(t, home, "service", "restart", "--wait", "60s")
	assert.Less(t, time.Since(begin), 30*time.Second, "reported without waiting out --wait")
	assert.Equal(t, 1, code, stderr)
	assert.Contains(t, stderr, "the new run exited without coming up")

	select {
	case <-r.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("the new run kept running:\n%s", r.output())
	}
	assert.Equal(t, 2, r.exitCode(), r.output())
	assert.Contains(t, r.output(), `unknown key "maxbytes"`)
}
