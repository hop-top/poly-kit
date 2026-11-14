package oauth

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/storage/secret/file"
)

// TestCrossProcessNoLostRotation runs three real processes against one file
// store and one lock directory. The provider rotates on every call and
// rejects any refresh token but the newest, so a lost rotation shows up as
// invalid_grant. A forced-stale skew makes every Token call hit the provider.
func TestCrossProcessNoLostRotation(t *testing.T) {
	if os.Getenv("OAUTH_TEST_HELPER") == "1" {
		return
	}
	rs := newRotServer(t, "rt-fake-0", 15*time.Millisecond)
	dir, locks := t.TempDir(), t.TempDir()
	st := file.New(dir, nil)
	rtSetup(t, st, rs.URL, "rt-fake-0")

	const procs, loops = 3, 6
	var cmds []*exec.Cmd
	var outs []*strings.Builder
	for i := 0; i < procs; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcessRefresh$")
		cmd.Env = append(os.Environ(), "OAUTH_TEST_HELPER=1", "OAUTH_TEST_DIR="+dir, "OAUTH_TEST_LOCKS="+locks, fmt.Sprintf("OAUTH_TEST_LOOPS=%d", loops))
		out := &strings.Builder{}
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds, outs = append(cmds, cmd), append(outs, out)
	}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Errorf("process %d: %v\n%s", i, err, outs[i])
		}
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.bad != 0 {
		t.Errorf("provider rejected %d stale refresh tokens (lost rotation)", rs.bad)
	}
	if rs.calls != procs*loops {
		t.Errorf("provider calls = %d, want %d", rs.calls, procs*loops)
	}
	if got := storedRT(t, st); got != secretEnv(rs.cur) {
		t.Errorf("stored %s, provider current %s", got, rs.cur)
	}
}

// TestHelperProcessRefresh is the child of TestCrossProcessNoLostRotation.
func TestHelperProcessRefresh(t *testing.T) {
	if os.Getenv("OAUTH_TEST_HELPER") != "1" {
		t.Skip("helper process")
	}
	st := file.New(os.Getenv("OAUTH_TEST_DIR"), nil)
	var loops int
	fmt.Sscan(os.Getenv("OAUTH_TEST_LOOPS"), &loops)
	// The clock runs 45 minutes ahead of the provider's one-hour tokens and
	// the skew is 2 hours, so every token, ours or another process's, is
	// stale at once and each Token call hits the provider.
	src, err := TokenSource(context.Background(), st, "g", WithLockDir(os.Getenv("OAUTH_TEST_LOCKS")),
		WithSkew(2*time.Hour), WithClock(func() time.Time { return time.Now().Add(45 * time.Minute) }))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < loops; i++ {
		if _, err := src.Token(context.Background()); err != nil {
			t.Fatalf("loop %d: %v", i, err)
		}
	}
}
