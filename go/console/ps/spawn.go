package ps

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
)

// StdioMode describes how SpawnDetached routes one of the child's
// standard streams. Unset (zero value) means "leave the *exec.Cmd
// field as-is", which is the safe default for callers who already
// configured cmd.Stdout / cmd.Stderr themselves.
type StdioMode int

const (
	// StdioInherit is the zero value: respect whatever the caller
	// configured on cmd. Pre-existing assignments are not touched.
	StdioInherit StdioMode = iota
	// StdioDiscard pipes the stream to io.Discard, so child output
	// silently disappears. Intended for daemons that already log to
	// their own files.
	StdioDiscard
	// StdioFile pipes the stream to a file path supplied via
	// SpawnOptions.StdoutPath / StderrPath. Existing content is
	// truncated; the file is opened with mode 0600.
	StdioFile
	// StdioBuffer captures the stream into an in-memory buffer
	// retrievable via Spawned.Stdout()/Stderr() after Wait. Useful
	// for tests; do NOT use for long-running children — the buffer
	// grows without bound.
	StdioBuffer
)

// SpawnOptions configures SpawnDetached.
//
// The zero SpawnOptions{} value is valid and means: use whatever the
// caller already configured on the *exec.Cmd, write a PID file at the
// supplied path (mandatory), detach the child into a new process group.
type SpawnOptions struct {
	// PIDFile is the path the child's PID is written to via
	// WritePIDFile after a successful Start. Required — the whole
	// point of SpawnDetached over plain cmd.Start() is the PID file.
	PIDFile string

	// Stdout / Stderr select stream routing. See StdioMode for modes.
	Stdout StdioMode
	Stderr StdioMode

	// StdoutPath / StderrPath are required when the corresponding
	// StdioMode is StdioFile.
	StdoutPath string
	StderrPath string
}

// Spawned is a handle to a process started via SpawnDetached.
//
// Callers typically don't need to keep this around — once SpawnDetached
// returns, the PID file on disk is the supervisory contract and ps.Stop
// can act on the entry without holding a Spawned. The handle is useful
// for short-lived in-process supervision (tests, fixtures) where access
// to captured stdout/stderr or the underlying *exec.Cmd is desirable.
type Spawned struct {
	Cmd *exec.Cmd
	PID int

	// stdout / stderr hold buffered output when the corresponding
	// StdioMode was StdioBuffer. nil otherwise.
	stdout *bytes.Buffer
	stderr *bytes.Buffer

	// closeOnExit accumulates files opened by SpawnDetached for
	// StdioFile redirection so they can be closed once the child
	// exits. Files are closed by the reaper goroutine.
	closeOnExit []io.Closer

	// waited is set after the reaper goroutine returns. Guarded by
	// mu so Wait can be called from multiple goroutines safely.
	mu      sync.Mutex
	waited  bool
	waitCh  chan struct{}
	waitErr error
}

// Stdout returns the captured stdout buffer when StdioBuffer was used.
// Returns nil for any other mode. Safe to read after Wait completes.
func (s *Spawned) Stdout() *bytes.Buffer { return s.stdout }

// Stderr returns the captured stderr buffer when StdioBuffer was used.
// Returns nil for any other mode. Safe to read after Wait completes.
func (s *Spawned) Stderr() *bytes.Buffer { return s.stderr }

// Wait blocks until the child exits and returns the result of
// cmd.Wait(). Safe to call multiple times: the second and subsequent
// calls return the cached result without re-waiting.
func (s *Spawned) Wait() error {
	s.mu.Lock()
	if s.waited {
		err := s.waitErr
		s.mu.Unlock()
		return err
	}
	ch := s.waitCh
	s.mu.Unlock()
	if ch != nil {
		<-ch
		s.mu.Lock()
		err := s.waitErr
		s.mu.Unlock()
		return err
	}
	return nil
}

// SpawnDetached starts cmd as a detached child process, writes its PID
// to opts.PIDFile, and returns a handle.
//
// "Detached" means:
//   - On POSIX: the child enters a new process group via Setpgid, so
//     signals delivered to the parent's pgrp (e.g. ^C in a terminal)
//     do not propagate to the child.
//   - On Windows: SysProcAttr is left at the runtime default; the
//     concept of process groups maps differently and detachment of a
//     supervised service is typically achieved via the service control
//     manager, which is out of scope for this primitive.
//
// SpawnDetached takes a pre-built *exec.Cmd (rather than wrapping
// exec.Command itself) so callers retain full control over args, env,
// working directory, and any platform-specific SysProcAttr fields they
// need to layer on top. The package only sets SysProcAttr.Setpgid
// (POSIX) and the requested stdio routing; existing SysProcAttr fields
// the caller set are preserved.
//
// The PID file is written AFTER cmd.Start() succeeds. If the write
// fails, the child is killed and the error returned, so a failed spawn
// never leaks a running process.
//
// SpawnDetached starts a background goroutine that calls cmd.Wait() to
// reap the child when it exits. Callers MAY also call Spawned.Wait()
// to be notified — the result is shared across all waiters.
//
// The supplied ctx is used only to abort the spawn itself before
// Start; it does NOT govern the child's lifetime. Use ps.Stop or the
// returned handle to terminate.
func SpawnDetached(ctx context.Context, cmd *exec.Cmd, opts SpawnOptions) (*Spawned, error) {
	if cmd == nil {
		return nil, fmt.Errorf("ps: spawn: nil cmd")
	}
	if opts.PIDFile == "" {
		return nil, fmt.Errorf("ps: spawn: opts.PIDFile is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("ps: spawn: %w", err)
	}

	applyDetachAttr(cmd)

	s := &Spawned{Cmd: cmd, waitCh: make(chan struct{})}

	// Configure stdio. Order matters: stdout / stderr setup may open
	// files we need to close on exit.
	if err := configureStdio(cmd, opts, s); err != nil {
		s.closeAllNow()
		return nil, fmt.Errorf("ps: spawn: configure stdio: %w", err)
	}

	if err := cmd.Start(); err != nil {
		s.closeAllNow()
		return nil, fmt.Errorf("ps: spawn: start: %w", err)
	}
	s.PID = cmd.Process.Pid

	if err := WritePIDFile(opts.PIDFile, Entry{ID: strconv.Itoa(s.PID)}); err != nil {
		// Clean up the orphan child rather than leaking it.
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		s.closeAllNow()
		return nil, fmt.Errorf("ps: spawn: write pid file: %w", err)
	}

	// Background reaper: lets the kernel release the child slot when
	// it exits. We do not remove the PID file here — supervision is
	// driven by ps.Stop / the on-disk file going forward.
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.waited = true
		s.waitErr = err
		closers := s.closeOnExit
		s.closeOnExit = nil
		s.mu.Unlock()
		for _, c := range closers {
			_ = c.Close()
		}
		close(s.waitCh)
	}()

	return s, nil
}

// configureStdio applies opts.Stdout / opts.Stderr to cmd. StdioInherit
// leaves the corresponding field untouched so the caller's pre-set
// values (e.g. cmd.Stdout = os.Stdout) win. Any opened files are
// recorded on s for close-on-exit.
func configureStdio(cmd *exec.Cmd, opts SpawnOptions, s *Spawned) error {
	stdout, closer, err := stdioWriter(opts.Stdout, opts.StdoutPath, &s.stdout)
	if err != nil {
		return fmt.Errorf("stdout: %w", err)
	}
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if closer != nil {
		s.closeOnExit = append(s.closeOnExit, closer)
	}

	stderr, closer, err := stdioWriter(opts.Stderr, opts.StderrPath, &s.stderr)
	if err != nil {
		return fmt.Errorf("stderr: %w", err)
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	if closer != nil {
		s.closeOnExit = append(s.closeOnExit, closer)
	}
	return nil
}

// stdioWriter resolves a StdioMode + path into the writer to assign and
// the closer to register for cleanup. Returns (nil, nil, nil) for
// StdioInherit so the caller's existing assignment is preserved.
//
// bufPtr is the field on Spawned where StdioBuffer parks its captured
// data; we pass a pointer because the buffer is allocated lazily here.
func stdioWriter(mode StdioMode, path string, bufPtr **bytes.Buffer) (io.Writer, io.Closer, error) {
	switch mode {
	case StdioInherit:
		return nil, nil, nil
	case StdioDiscard:
		return io.Discard, nil, nil
	case StdioFile:
		if path == "" {
			return nil, nil, fmt.Errorf("StdioFile requires a path")
		}
		// 0600: child output may contain secrets; constrain to owner.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("open %s: %w", path, err)
		}
		return f, f, nil
	case StdioBuffer:
		buf := &bytes.Buffer{}
		*bufPtr = buf
		return buf, nil, nil
	default:
		return nil, nil, fmt.Errorf("unknown StdioMode %d", mode)
	}
}

// closeAllNow closes any open StdioFile handles before they are
// transferred to the running child. Used on the spawn-failure path.
func (s *Spawned) closeAllNow() {
	s.mu.Lock()
	closers := s.closeOnExit
	s.closeOnExit = nil
	s.mu.Unlock()
	for _, c := range closers {
		_ = c.Close()
	}
}
