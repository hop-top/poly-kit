package cmdsurface

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

// ErrUnknownCommand is returned when an Invocation.Path cannot be
// resolved to a cobra command in the root tree. The Bridge maps
// this to a transport-appropriate "not found" response (REST 404,
// MCP error, etc.).
var ErrUnknownCommand = errors.New("cmdsurface: unknown command")

// Runner executes an Invocation against some backing implementation
// (in-process cobra, subprocess, sandboxed). The bridge holds one
// Runner; surfaces invoke through the bridge so policy applies
// uniformly regardless of which Runner is wired.
type Runner interface {
	// Run executes inv synchronously and returns a single Result.
	// The implementation MUST honor ctx for cancellation.
	Run(ctx context.Context, inv Invocation) (Result, error)
	// Stream executes inv and emits Events as they are produced
	// (stdout / stderr lines, optional progress payloads). The
	// final event MUST have Kind == "done" and Data == *Result
	// (or nil on error). out is closed by the Runner when streaming
	// completes; callers must not close it.
	Stream(ctx context.Context, inv Invocation, out chan<- Event) error
}

// InProcessRunner returns a Runner that invokes the cobra tree
// rooted at root in the current process. The returned Runner does
// not mutate root; it temporarily swaps the root's output writers
// and argv during Run/Stream and restores them on return.
//
// Concurrent calls into the same InProcessRunner are serialized on
// an internal mutex because cobra's argv/output state is held on
// the *Command itself — there is no per-invocation isolation in the
// upstream library. Surfaces that need true parallelism should wire
// a Subprocess- or sandboxed Runner instead.
func InProcessRunner(root *cobra.Command) Runner {
	return &inProcessRunner{root: root}
}

type inProcessRunner struct {
	root *cobra.Command
	mu   sync.Mutex
}

// Run implements Runner.
func (r *inProcessRunner) Run(ctx context.Context, inv Invocation) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.root == nil {
		return Result{}, errors.New("cmdsurface: nil cobra root")
	}
	leaf, err := resolveLeaf(r.root, inv.Path)
	if err != nil {
		return Result{}, err
	}
	_ = leaf // resolution validates the path; cobra dispatches via SetArgs

	var stdout, stderr bytes.Buffer
	restore := captureRoot(r.root, &stdout, &stderr, buildArgs(inv))
	defer restore()

	execErr := r.root.ExecuteContext(ctx)

	res := Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if execErr != nil {
		res.ExitCode = 1
		if res.Stderr == "" {
			res.Stderr = execErr.Error()
		}
	}
	return res, nil
}

// Stream implements Runner. It produces one Event per output line
// (Kind="stdout"/"stderr") followed by a terminal Event{Kind:"done"}.
// out is closed when streaming completes.
func (r *inProcessRunner) Stream(ctx context.Context, inv Invocation, out chan<- Event) error {
	if out == nil {
		return errors.New("cmdsurface: nil event channel")
	}
	defer close(out)

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.root == nil {
		return errors.New("cmdsurface: nil cobra root")
	}
	if _, err := resolveLeaf(r.root, inv.Path); err != nil {
		return err
	}

	outR, outW := io.Pipe()
	errR, errW := io.Pipe()

	var stdoutBuf, stderrBuf bytes.Buffer
	outTee := io.MultiWriter(outW, &stdoutBuf)
	errTee := io.MultiWriter(errW, &stderrBuf)

	restore := captureRootWriters(r.root, outTee, errTee, buildArgs(inv))
	defer restore()

	// Scanners drain each pipe and forward one Event per line.
	var wg sync.WaitGroup
	wg.Add(2)
	go scanLines(outR, "stdout", out, &wg)
	go scanLines(errR, "stderr", out, &wg)

	execErr := r.root.ExecuteContext(ctx)
	// Closing the writers tells the scanners to drain remaining
	// bytes and exit cleanly.
	_ = outW.Close()
	_ = errW.Close()
	wg.Wait()

	res := &Result{
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
	}
	if execErr != nil {
		res.ExitCode = 1
		if res.Stderr == "" {
			res.Stderr = execErr.Error()
		}
	}
	out <- Event{Kind: "done", Data: res, At: time.Now()}
	return nil
}

// scanLines reads r line-by-line and forwards each line as an Event
// of the named kind. Exits when r returns EOF or any read error.
func scanLines(r io.Reader, kind string, out chan<- Event, wg *sync.WaitGroup) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	// Allow long lines (cobra help text + JSON payloads).
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		out <- Event{Kind: kind, Data: sc.Text(), At: time.Now()}
	}
}

// resolveLeaf walks the cobra tree from root following path and
// returns the matching command. An empty path resolves to root.
// Returns ErrUnknownCommand when any path segment cannot be matched.
func resolveLeaf(root *cobra.Command, path []string) (*cobra.Command, error) {
	if len(path) == 0 {
		return root, nil
	}
	cmd, remaining, err := root.Find(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUnknownCommand, joinPath(path), err)
	}
	if len(remaining) != 0 {
		return nil, fmt.Errorf("%w: unresolved segments %v under %q",
			ErrUnknownCommand, remaining, cmd.CommandPath())
	}
	return cmd, nil
}

// buildArgs converts an Invocation into the cobra argv form:
//
//	[<path...>, --flag=<value>..., <args...>]
//
// Bool flags whose value is true are emitted as --flag (no value);
// false bool flags are omitted entirely. Other flag values are
// rendered with fmt %v.
func buildArgs(inv Invocation) []string {
	out := make([]string, 0, len(inv.Path)+len(inv.Flags)*2+len(inv.Args))
	out = append(out, inv.Path...)
	for _, k := range sortedKeys(inv.Flags) {
		v := inv.Flags[k]
		switch tv := v.(type) {
		case bool:
			if tv {
				out = append(out, "--"+k)
			}
		default:
			out = append(out, fmt.Sprintf("--%s=%v", k, v))
		}
	}
	out = append(out, inv.Args...)
	return out
}

// captureRoot installs stdout/stderr buffers and argv on root and
// returns a function that restores the prior values. Also flips
// SilenceUsage/SilenceErrors so cobra does not interleave its own
// help text into the captured streams on error.
func captureRoot(root *cobra.Command, stdout, stderr *bytes.Buffer, args []string) func() {
	return captureRootWriters(root, stdout, stderr, args)
}

// captureRootWriters is the generic form taking any io.Writer
// destinations, used by Stream to plug io.Pipe writers in.
func captureRootWriters(root *cobra.Command, outW, errW io.Writer, args []string) func() {
	prevOut := root.OutOrStdout()
	prevErr := root.ErrOrStderr()
	prevSilenceUsage := root.SilenceUsage
	prevSilenceErrors := root.SilenceErrors

	root.SetOut(outW)
	root.SetErr(errW)
	root.SetArgs(args)
	root.SilenceUsage = true
	root.SilenceErrors = true

	return func() {
		root.SetOut(prevOut)
		root.SetErr(prevErr)
		root.SetArgs(nil)
		root.SilenceUsage = prevSilenceUsage
		root.SilenceErrors = prevSilenceErrors
	}
}

// joinPath renders a command path slice as "a b c" for error
// messages.
func joinPath(path []string) string {
	if len(path) == 0 {
		return "(root)"
	}
	out := path[0]
	for _, p := range path[1:] {
		out += " " + p
	}
	return out
}

// SubprocessRunner returns a Runner that shells out to binaryPath
// for each Invocation. The child is spawned with exec.CommandContext
// so context cancellation propagates to the OS process. On Unix the
// child is placed in its own process group (Setpgid) and cancellation
// kills the entire group with SIGKILL, ensuring grandchildren do not
// leak. On Windows the cancellation path falls back to a best-effort
// cmd.Process.Kill() — see runner_windows.go.
//
// The binary is invoked as:
//
//	<binaryPath> <inv.Path...> [--flag=val ...] [inv.Args...]
//
// Each Invocation runs a fresh process; SubprocessRunner is safe for
// concurrent use.
func SubprocessRunner(binaryPath string) Runner {
	return &subprocessRunner{binary: binaryPath}
}

type subprocessRunner struct {
	binary string
}

// Run executes the binary with the Invocation's argv synthesized from
// Path + Flags + Args, buffers stdout/stderr, and returns a Result
// with the captured streams and exit code. If ctx is canceled while
// the process is running the process group is killed and the wrapped
// ctx.Err() is returned alongside the partial Result.
func (r *subprocessRunner) Run(ctx context.Context, inv Invocation) (Result, error) {
	if r.binary == "" {
		return Result{}, errors.New("cmdsurface: SubprocessRunner: empty binary path")
	}

	argv := buildArgs(inv)
	cmd := exec.CommandContext(ctx, r.binary, argv...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	applySubprocessAttrs(cmd)
	// Cancel callback: on ctx.Done, kill the whole process group so
	// any grandchildren the binary spawned do not leak. Falls back
	// to cmd.Process.Kill() on platforms without process groups.
	cmd.Cancel = func() error { return killProcessTree(cmd) }
	// WaitDelay gives the kernel a brief window between the cancel
	// signal and a hard SIGKILL on the parent, so stdio is drained.
	cmd.WaitDelay = 100 * time.Millisecond

	runErr := cmd.Run()

	res := Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if runErr != nil {
		res.ExitCode = exitCodeOf(runErr)
		// If ctx was canceled, surface that as the returned error so
		// callers can distinguish a genuine subprocess failure from a
		// caller-initiated abort.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return res, fmt.Errorf("cmdsurface: subprocess canceled: %w", ctxErr)
		}
		// A non-ExitError run failure (binary not found, permission
		// denied, etc.) is returned as an error; an ExitError with a
		// non-zero status is surfaced only via res.ExitCode.
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return res, fmt.Errorf("cmdsurface: subprocess run: %w", runErr)
		}
	}
	return res, nil
}

// Stream executes the binary and emits one Event per line of stdout
// or stderr, followed by a terminal Event{Kind:"done"} carrying a
// *Result. The output channel is closed by Stream when streaming
// completes; callers must not close it.
func (r *subprocessRunner) Stream(ctx context.Context, inv Invocation, out chan<- Event) error {
	if out == nil {
		return errors.New("cmdsurface: nil event channel")
	}
	defer close(out)

	if r.binary == "" {
		return errors.New("cmdsurface: SubprocessRunner: empty binary path")
	}

	argv := buildArgs(inv)
	cmd := exec.CommandContext(ctx, r.binary, argv...)

	// Use io.Pipe pairs rather than cmd.StdoutPipe()/StderrPipe() so
	// cmd.Wait() does NOT close the read end before scanLinesTee
	// goroutines have drained it. With StdoutPipe(), a race exists:
	// if the process exits before goroutines are scheduled, Wait
	// closes the pipe and the readers see EOF with zero events.
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	applySubprocessAttrs(cmd)
	cmd.Cancel = func() error { return killProcessTree(cmd) }
	cmd.WaitDelay = 100 * time.Millisecond

	if err := cmd.Start(); err != nil {
		_ = stdoutW.Close()
		_ = stderrW.Close()
		return fmt.Errorf("cmdsurface: subprocess start: %w", err)
	}

	// Tee each pipe so we keep a copy for the terminal Result while
	// emitting one Event per line live.
	var stdoutBuf, stderrBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go scanLinesTee(stdoutR, &stdoutBuf, "stdout", out, &wg)
	go scanLinesTee(stderrR, &stderrBuf, "stderr", out, &wg)

	waitErr := cmd.Wait()
	_ = stdoutW.Close()
	_ = stderrW.Close()
	wg.Wait()

	res := &Result{
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
	}
	if waitErr != nil {
		res.ExitCode = exitCodeOf(waitErr)
	}
	out <- Event{Kind: "done", Data: res, At: time.Now()}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("cmdsurface: subprocess canceled: %w", ctxErr)
	}
	// Same policy as Run: only surface non-ExitError failures.
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			return fmt.Errorf("cmdsurface: subprocess wait: %w", waitErr)
		}
	}
	return nil
}

// scanLinesTee is the streaming sibling of scanLines: it also copies
// every line into buf so the terminal "done" Event can carry the full
// captured stream. Newline is restored when teeing because the scanner
// strips it.
func scanLinesTee(r io.Reader, buf *bytes.Buffer, kind string, out chan<- Event, wg *sync.WaitGroup) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		buf.WriteString(line)
		buf.WriteByte('\n')
		out <- Event{Kind: kind, Data: line, At: time.Now()}
	}
}

// exitCodeOf extracts the process exit code from an *exec.ExitError,
// returning 1 for any other error type (Go's convention for "command
// failed but no status was reported").
func exitCodeOf(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}
