//go:build unix

package main

import (
	"encoding/json"
	"net/http"
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
func statusOf(t *testing.T, home, prof string) serviceStatus {
	t.Helper()
	out, stderr, code := binClientEnv(t, home, profile(prof), "service", "status", "--format", "json")
	require.Equal(t, 0, code, "stdout: %s\nstderr: %s", out, stderr)
	var got serviceStatus
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	return got
}

func apiURL(r *binRun) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.apiURL
}

func storeItems(t *testing.T, st serviceStatus) any {
	t.Helper()
	require.Len(t, st.Sections, 1)
	require.Equal(t, "store", st.Sections[0].Title)
	return st.Sections[0].Data["items"]
}

func TestBinaryServiceScopesAreIndependent(t *testing.T) {
	home := newHome(t, "")
	alpha := runBinary(t, home, profile("alpha"), "service", "start", "api", "--addr", "127.0.0.1:0")
	require.True(t, alpha.ready, alpha.output())
	beta := runBinary(t, home, profile("beta"), "service", "start", "api", "--addr", "127.0.0.1:0")
	require.True(t, beta.ready, beta.output())

	a := statusOf(t, home, "alpha")
	require.Len(t, a.Services, 1)
	assert.Equal(t, alpha.cmd.Process.Pid, a.Services[0].PID)
	assert.Equal(t, "http://"+a.Services[0].Addr, apiURL(alpha))
	b := statusOf(t, home, "beta")
	require.Len(t, b.Services, 1)
	assert.Equal(t, beta.cmd.Process.Pid, b.Services[0].PID)
	assert.Equal(t, "http://"+b.Services[0].Addr, apiURL(beta))

	// The section is the instance's state: an item added to alpha
	// shows in alpha's status and not in beta's.
	assert.EqualValues(t, 2, storeItems(t, a))
	status, body := httpDo(t, http.MethodPost, apiURL(alpha)+"/v1/commands/item/add", `{"args":["washer"]}`)
	require.Equal(t, http.StatusOK, status, string(body))
	assert.EqualValues(t, 3, storeItems(t, statusOf(t, home, "alpha")))
	assert.EqualValues(t, 2, storeItems(t, statusOf(t, home, "beta")))

	// The text form prints the rows, then the section.
	out, _, code := binClientEnv(t, home, profile("alpha"), "service", "status")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "SERVICE")
	assert.Contains(t, out, "SECTION")
	assert.Contains(t, out, "items=3")

	// No profile is a third, unscoped instance: not running.
	_, stderr, code := binClient(t, home, "service", "status")
	assert.Equal(t, 3, code, stderr)

	// Restart beta: same pid, alpha untouched.
	out, stderr, code = binClientEnv(t, home, profile("beta"), "service", "restart", "--format", "json", "--wait", "30s")
	require.Equal(t, 0, code, "stdout: %s\nstderr: %s\nbeta:\n%s", out, stderr, beta.output())
	var restarted serviceStatus
	require.NoError(t, json.Unmarshal([]byte(out), &restarted), out)
	require.Len(t, restarted.Services, 1)
	assert.Equal(t, beta.cmd.Process.Pid, restarted.Services[0].PID)
	assert.EqualValues(t, 2, storeItems(t, restarted))
	assert.Equal(t, 1, strings.Count(alpha.output(), "ready_reported object=supervisor"), "alpha not restarted")
	assert.EqualValues(t, 3, storeItems(t, statusOf(t, home, "alpha")), "alpha kept its state")

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
	require.Len(t, b.Services, 1)
	assert.Equal(t, beta.cmd.Process.Pid, b.Services[0].PID)
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
	require.Len(t, st.Services, 1)
	assert.Equal(t, first.cmd.Process.Pid, st.Services[0].PID)
}
