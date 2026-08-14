package recorder

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sort"
	"time"

	"hop.top/kit/go/console/cli/conformance/harness"
)

// ExecInvoker runs the target binary as a real subprocess. It
// implements harness.Invoker so the recorder shares the harness's
// execution seam: args in, stdout/stderr/exit out.
type ExecInvoker struct {
	// Path is the target binary. Required.
	Path string
	// Dir is the working directory every invocation runs in.
	// Empty means the current process's working directory.
	Dir string
	// Timeout bounds one invocation; the subprocess is killed when
	// exceeded and Invoke returns the context error. Zero = no bound.
	Timeout time.Duration
	// BaseEnv is the environment the subprocess starts from before
	// per-invocation overrides apply. Nil = os.Environ().
	BaseEnv []string
}

var _ harness.Invoker = (*ExecInvoker)(nil)

// Invoke executes Path with args in Dir, streaming stdin/stdout/
// stderr, with env layered over BaseEnv. A non-zero exit is a
// successful capture (exitCode, nil); anything preventing execution
// (missing binary, timeout, signal) returns a non-nil error.
func (e *ExecInvoker) Invoke(args []string, stdin io.Reader, stdout, stderr io.Writer, env map[string]string) (int, error) {
	ctx := context.Background()
	cancel := func() {}
	if e.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(ctx, e.Path, args...)
	cmd.Dir = e.Dir
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	base := e.BaseEnv
	if base == nil {
		base = os.Environ()
	}
	cmd.Env = mergeEnv(base, env)

	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	// Timeout dominates: an ExitError caused by the kill signal must
	// not masquerade as a captured exit code.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return -1, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() >= 0 {
		return exitErr.ExitCode(), nil
	}
	return -1, err
}

// mergeEnv layers overrides onto base (last wins per exec semantics)
// with overrides applied in sorted key order for determinism.
func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(base)+len(keys))
	out = append(out, base...)
	for _, k := range keys {
		out = append(out, k+"="+overrides[k])
	}
	return out
}
