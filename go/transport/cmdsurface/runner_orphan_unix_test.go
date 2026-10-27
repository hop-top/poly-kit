//go:build !windows

package cmdsurface

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSubprocessRunner_OrphanFloodingOutputIsBounded pins that an
// orphan writing without pause does not keep the drain going: its
// pipe never reads empty, so only a bound on what is read after the
// child exits ends the run. The run must return, with the captured
// output bounded, rather than read and buffer for ever.
func TestSubprocessRunner_OrphanFloodingOutputIsBounded(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Cleanup(func() {
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	inv := Invocation{Args: []string{"-c", `yes & echo $! > "$0"`, pidFile}, ownArgv: true}

	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := SubprocessRunner(sh).Run(context.Background(), inv)
		done <- outcome{res, err}
	}()
	select {
	case o := <-done:
		if !errors.Is(o.err, exec.ErrWaitDelay) {
			t.Fatalf("Run err=%v, want exec.ErrWaitDelay", o.err)
		}
		if n := len(o.res.Stdout); n > 64<<20 {
			t.Errorf("captured %d bytes of orphan output; want it bounded", n)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run still draining an orphan that never stops writing")
	}
}

// TestDrainReader_BoundsAFloodAfterExit drives the drain directly with
// a writer that keeps the pipe full, as an orphan writing without
// pause does: the pipe never reads empty, so the grace never arms.
// Reads after exit must still stop, at errOrphanCut, after a bounded
// number of bytes.
func TestDrainReader_BoundsAFloodAfterExit(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	d, err := newDrainReader(r)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		chunk := make([]byte, 64<<10)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}()
	d.exited()

	type outcome struct {
		n   int
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		// Small reads with a pause between them: the writer refills
		// the pipe each time, so no read finds it empty.
		buf := make([]byte, 4<<10)
		total := 0
		for {
			time.Sleep(time.Millisecond)
			n, err := d.Read(buf)
			total += n
			if err != nil || total > 256<<20 {
				done <- outcome{total, err}
				return
			}
		}
	}()
	select {
	case o := <-done:
		if !errors.Is(o.err, errOrphanCut) {
			t.Fatalf("drain ended with %v after %d bytes, want errOrphanCut", o.err, o.n)
		}
		if o.n > orphanByteCap {
			t.Errorf("read %d bytes after exit, want at most %d", o.n, orphanByteCap)
		}
		if !d.wasCut() {
			t.Error("wasCut=false after a cut")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("drain still reading a flood after exit")
	}
}
