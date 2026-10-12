package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/output"
)

// scoped gives a root the fixed control scope s.
func scoped(s string) func(*Root) {
	return WithServiceControlScope(func(*cobra.Command) (string, error) { return s, nil })
}

// ownScope gives a server-only test instance a control scope no other
// instance in this binary uses, so tests that run two at once do not
// refuse each other's start.
func ownScope() func(*Root) {
	return scoped(fmt.Sprintf("t%d", scopeSeq.Add(1)))
}

var scopeSeq atomic.Int64

// client builds a root for one client invocation in scope s.
func client(t *testing.T, s string) *Root {
	t.Helper()
	return authRoot(t, WithAPI(APIConfig{}), scoped(s))
}

// startRefused runs `service start api` on r, bounded: a start that is
// not refused serves until the deadline and comes back without an
// error instead of hanging the test.
func startRefused(t *testing.T, r *Root) (int, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	r.SetArgs([]string{"service", "start", "api"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := r.Execute(ctx)
	var ce *output.Error
	if errors.As(err, &ce) {
		return ce.ExitCode, err
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

// Two instances of one tool in two scopes each own a control socket:
// status, stop and restart from one scope reach that scope's instance
// only.
func TestServiceScope_InstancesAreControlledIndependently(t *testing.T) {
	isolateRuntime(t)
	a := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), scoped("alpha"))
	_, stopA := serveAPI(t, a)
	defer stopA()
	b := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), scoped("beta"))
	_, stopB := serveAPI(t, b)
	defer stopB()
	require.NotEqual(t, controlPathOf(t, a), controlPathOf(t, b))
	assert.Equal(t, "control-alpha.sock", filepath.Base(controlPathOf(t, a)))

	rowsA := instanceStatusRows(t, scoped("alpha"))
	require.Len(t, rowsA, 1)
	assert.Equal(t, apiSvc(t, a).Addr(), rowsA[0].Addr, "alpha's status answers from alpha")
	rowsB := instanceStatusRows(t, scoped("beta"))
	require.Len(t, rowsB, 1)
	assert.Equal(t, apiSvc(t, b).Addr(), rowsB[0].Addr, "beta's status answers from beta")

	// The unscoped instance is a third one, and is not running.
	_, _, code, err := run(t, client(t, ""), "service", "status")
	require.Error(t, err)
	assert.Equal(t, output.ExitNotFound, code)

	out, _, code, err := run(t, client(t, "alpha"), "service", "stop", "--format", "json")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.JSONEq(t, `{"result":"stopped"}`, out)
	require.Eventually(t, func() bool { return !apiSvc(t, a).Ready() }, 5*time.Second, 10*time.Millisecond,
		"alpha drained")
	assert.True(t, apiSvc(t, b).Ready(), "beta untouched by alpha's stop")

	_, _, code, err = run(t, client(t, "alpha"), "service", "status")
	require.Error(t, err)
	assert.Equal(t, output.ExitNotFound, code)
	assert.Contains(t, err.Error(), "tool is not running in control scope alpha")
	rowsB = instanceStatusRows(t, scoped("beta"))
	require.Len(t, rowsB, 1)
	assert.Equal(t, apiSvc(t, b).Addr(), rowsB[0].Addr)
}

// One scope, two starts: the second is refused with CONFLICT naming
// the path, before it starts a service, and the first keeps it.
func TestServiceScope_SameScopeSecondStartRefused(t *testing.T) {
	isolateRuntime(t)
	first := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), scoped("work"))
	_, stop := serveAPI(t, first)
	defer stop()

	second := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), scoped("work"))
	code, err := startRefused(t, second)
	require.Error(t, err)
	assert.Equal(t, output.ExitConflict, code)
	assert.Contains(t, err.Error(), controlPathOf(t, first))
	assert.Empty(t, apiSvc(t, second).Addr(), "nothing bound")

	rows := instanceStatusRows(t, scoped("work"))
	require.Len(t, rows, 1)
	assert.Equal(t, apiSvc(t, first).Addr(), rows[0].Addr)

	// Another scope still starts.
	other := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), scoped("home"))
	_, stopOther := serveAPI(t, other)
	defer stopOther()
}

// The scope is resolved from the invoked command, after flag parsing:
// a tool may derive it from a flag of its own.
func TestServiceScope_ResolvedAtCommandTime(t *testing.T) {
	isolateRuntime(t)
	var mu sync.Mutex
	var seenList []string
	seen := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seenList...) }
	profileScope := func(r *Root) {
		r.Cmd.PersistentFlags().String("profile", "", "profile")
		WithServiceControlScope(func(cmd *cobra.Command) (string, error) {
			p, err := cmd.Flags().GetString("profile")
			mu.Lock()
			seenList = append(seenList, cmd.Name()+":"+p)
			mu.Unlock()
			return p, err
		})(r)
	}
	d := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), profileScope)
	_, stop := serveAPI(t, d, "--profile", "staging")
	defer stop()
	assert.Contains(t, seen(), "start:staging")

	r := authRoot(t, WithAPI(APIConfig{}), profileScope)
	out, _, code, err := run(t, r, "--profile", "staging", "service", "status", "--format", "json")
	require.NoError(t, err, out)
	assert.Equal(t, 0, code)
	assert.Contains(t, seen(), "status:staging")

	r = authRoot(t, WithAPI(APIConfig{}), profileScope)
	_, _, code, _ = run(t, r, "--profile", "prod", "service", "status")
	assert.Equal(t, output.ExitNotFound, code, "another profile's instance is not running")
}

// A scope that is not one plain path segment is a usage error on every
// leaf; the resolver's own error passes through as it is.
func TestServiceScope_RefusedScopes(t *testing.T) {
	isolateRuntime(t)
	for _, bad := range []string{"../x", "a/b", "..", "with space", "ü"} {
		for _, args := range [][]string{
			{"service", "start", "api", "--dry-run"},
			{"service", "status"},
			{"service", "stop"},
			{"service", "restart"},
		} {
			r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), scoped(bad))
			_, _, code, err := run(t, r, args...)
			require.Error(t, err, "%q %v", bad, args)
			assert.Equal(t, output.ExitUsage, code, "%q %v", bad, args)
			assert.Contains(t, err.Error(), "control scope")
		}
	}

	boom := output.UnauthorizedError("no profile selected")
	r := authRoot(t, WithAPI(APIConfig{}),
		WithServiceControlScope(func(*cobra.Command) (string, error) { return "", boom }))
	_, _, code, err := run(t, r, "service", "status")
	require.ErrorIs(t, err, boom)
	assert.Equal(t, output.ExitUnauthorized, code)
}

// staleSocket leaves a socket file nobody listens on at path.
func staleSocket(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, ln.Close())
	_, statErr := os.Lstat(path)
	require.NoError(t, statErr, "a stale socket file is left behind")
}

// A scoped socket an instance left behind is not running, and the next
// start in that scope takes it over.
func TestServiceScope_StaleSocketRecovered(t *testing.T) {
	isolateRuntime(t)
	d := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), scoped("work"))
	staleSocket(t, controlPathOf(t, d))

	_, _, code, _ := run(t, client(t, "work"), "service", "status")
	assert.Equal(t, output.ExitNotFound, code)

	_, stop := serveAPI(t, d)
	defer stop()
	rows := instanceStatusRows(t, scoped("work"))
	require.Len(t, rows, 1)
	assert.Equal(t, apiSvc(t, d).Addr(), rows[0].Addr)
}

// A socket something still answers on is never unlinked, lock or no
// lock: an instance of a release that took no lock is live too.
func TestService_LiveSocketIsNeverRemoved(t *testing.T) {
	isolateRuntime(t)
	d := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	path := controlPathOf(t, d)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	code, err := startRefused(t, d)
	require.Error(t, err)
	assert.Equal(t, output.ExitConflict, code)
	assert.Contains(t, err.Error(), path)
	_, statErr := os.Lstat(path)
	assert.NoError(t, statErr, "the live socket is still there")
	conn, err := net.Dial("unix", path)
	require.NoError(t, err, "and still answers")
	_ = conn.Close()
}

// The lock beside the socket is what a live instance holds: held by
// anyone else, the start is refused even with no socket file yet, and
// nothing is created at the path.
func TestService_HeldLockIsRefused(t *testing.T) {
	isolateRuntime(t)
	d := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	path := controlPathOf(t, d)
	other := flock.New(path + ".lock")
	held, err := other.TryLock()
	require.NoError(t, err)
	require.True(t, held)
	defer func() { _ = other.Unlock() }()

	code, err := startRefused(t, d)
	require.Error(t, err)
	assert.Equal(t, output.ExitConflict, code)
	assert.Contains(t, err.Error(), path)
	_, statErr := os.Lstat(path)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "no socket created")
}

// The lock file is owner-only, beside the socket in the owner-only
// runtime directory.
func TestService_ControlLockIsOwnerOnly(t *testing.T) {
	isolateRuntime(t)
	d := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), scoped("work"))
	_, stop := serveAPI(t, d)
	defer stop()
	info, err := os.Stat(controlPathOf(t, d) + ".lock")
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	dir, err := os.Stat(filepath.Dir(controlPathOf(t, d)))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dir.Mode().Perm())
}

// Whatever else sits at the path is not removed either.
func TestService_NonSocketAtControlPathIsRefused(t *testing.T) {
	isolateRuntime(t)
	d := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	path := controlPathOf(t, d)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("keep me"), 0o600))

	code, err := startRefused(t, d)
	require.Error(t, err)
	assert.Equal(t, output.ExitConflict, code)
	assert.Contains(t, err.Error(), "is not a socket")
	got, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, "keep me", string(got))
}

// A path over the unix socket limit is a usage error, not a run
// without a control channel.
func TestServiceScope_OverlongPathIsUsage(t *testing.T) {
	isolateRuntime(t)
	long := make([]byte, 120)
	for i := range long {
		long[i] = 'x'
	}
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), scoped(string(long)))
	_, _, code, err := run(t, r, "service", "start", "api")
	require.Error(t, err)
	assert.Equal(t, output.ExitUsage, code)
	assert.Contains(t, err.Error(), "byte limit")
}

// The control socket is released when the run ends, so the same scope
// starts again at once.
func TestServiceScope_ReleasedOnStop(t *testing.T) {
	isolateRuntime(t)
	for range 2 {
		d := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), scoped("work"))
		_, stop := serveAPI(t, d)
		stop()
	}
	var ce *output.Error
	_, _, code, err := run(t, client(t, "work"), "service", "status")
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, output.ExitNotFound, code)
}

// The json shape `service status` declares carries both halves.
func TestServiceStatus_ShapeWithoutSections(t *testing.T) {
	isolateRuntime(t)
	d := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	_, stop := serveAPI(t, d)
	defer stop()
	out, _, _, err := run(t, client(t, ""), "service", "status", "--format", "json")
	require.NoError(t, err)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &raw), out)
	assert.JSONEq(t, `[]`, string(raw["sections"]))
	var rows []serviceStatusRow
	require.NoError(t, json.Unmarshal(raw["services"], &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, APIServiceName, rows[0].Service)
}
