//go:build unix

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file run two instances of the built binary, one
// per profile, under one home: the control scope is what keeps them
// apart.

func profile(name string) []string { return []string{profileEnv + "=" + name} }

// statusOf runs `service status --format json` for a profile.
func statusOf(t *testing.T, home, prof string) []statusRow {
	t.Helper()
	out, stderr, code := binClientEnv(t, home, profile(prof), "service", "status", "--format", "json")
	require.Equal(t, 0, code, "stdout: %s\nstderr: %s", out, stderr)
	var rows []statusRow
	require.NoError(t, json.Unmarshal([]byte(out), &rows), out)
	return rows
}

func apiURL(r *binRun) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.apiURL
}

func TestBinaryServiceScopesAreIndependent(t *testing.T) {
	home := newHome(t, "")
	alpha := runBinary(t, home, profile("alpha"), "service", "start", "api", "--addr", "127.0.0.1:0")
	require.True(t, alpha.ready, alpha.output())
	beta := runBinary(t, home, profile("beta"), "service", "start", "api", "--addr", "127.0.0.1:0")
	require.True(t, beta.ready, beta.output())

	a := statusOf(t, home, "alpha")
	require.Len(t, a, 1)
	assert.Equal(t, alpha.cmd.Process.Pid, a[0].PID)
	assert.Equal(t, "http://"+a[0].Addr, apiURL(alpha))
	b := statusOf(t, home, "beta")
	require.Len(t, b, 1)
	assert.Equal(t, beta.cmd.Process.Pid, b[0].PID)
	assert.Equal(t, "http://"+b[0].Addr, apiURL(beta))

	// No profile is a third, unscoped instance: not running.
	_, stderr, code := binClient(t, home, "service", "status")
	assert.Equal(t, 3, code, stderr)

	// Restart beta: same pid, alpha untouched.
	out, stderr, code := binClientEnv(t, home, profile("beta"), "service", "restart", "--format", "json", "--wait", "30s")
	require.Equal(t, 0, code, "stdout: %s\nstderr: %s\nbeta:\n%s", out, stderr, beta.output())
	var restarted []statusRow
	require.NoError(t, json.Unmarshal([]byte(out), &restarted), out)
	require.Len(t, restarted, 1)
	assert.Equal(t, beta.cmd.Process.Pid, restarted[0].PID)
	assert.Equal(t, 1, strings.Count(alpha.output(), "ready_reported object=supervisor"), "alpha not restarted")
	assert.Equal(t, alpha.cmd.Process.Pid, statusOf(t, home, "alpha")[0].PID, "alpha still answers")

	// Stop alpha: alpha exits, beta keeps serving.
	out, stderr, code = binClientEnv(t, home, profile("alpha"), "service", "stop", "--format", "json")
	require.Equal(t, 0, code, "stdout: %s\nstderr: %s", out, stderr)
	assert.JSONEq(t, `{"result":"stopped"}`, out)
	select {
	case <-alpha.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("alpha did not stop:\n%s", alpha.output())
	}
	assert.NoError(t, alpha.err, alpha.output())
	select {
	case <-beta.done:
		t.Fatalf("beta stopped with alpha:\n%s", beta.output())
	default:
	}
	b = statusOf(t, home, "beta")
	require.Len(t, b, 1)
	assert.Equal(t, beta.cmd.Process.Pid, b[0].PID)
	_, _, code = binClientEnv(t, home, profile("alpha"), "service", "status")
	assert.Equal(t, 3, code)
}

// A second instance in a scope a live one serves exits 4 (CONFLICT)
// naming the socket, without starting a service.
func TestBinaryServiceScopeTakenIsConflict(t *testing.T) {
	home := newHome(t, "")
	first := runBinary(t, home, profile("alpha"), "service", "start", "api", "--addr", "127.0.0.1:0")
	require.True(t, first.ready, first.output())

	second := runBinary(t, home, profile("alpha"), "service", "start", "api", "--addr", "127.0.0.1:0")
	require.False(t, second.ready, second.output())
	assert.Equal(t, 4, second.exitCode(), second.output())
	assert.Contains(t, second.output(), "control-alpha.sock")
	assert.NotContains(t, second.output(), "started object=service")

	st := statusOf(t, home, "alpha")
	require.Len(t, st, 1)
	assert.Equal(t, first.cmd.Process.Pid, st[0].PID)
}
