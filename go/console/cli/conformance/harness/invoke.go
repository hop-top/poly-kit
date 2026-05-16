package harness

import (
	"bytes"
	"errors"
	"os"

	"github.com/spf13/cobra"
)

// runResult captures one invocation's externally visible result.
type runResult struct {
	stdout   *bytes.Buffer
	stderr   *bytes.Buffer
	exitCode int
	runErr   error
}

// invoke runs cmd (or the configured Invoker) with the per-config
// argv/stdin/env. The cassette dir, when set, is exported via
// XRR_CASSETTE_DIR and XRR_MODE so adopter code wrapping its
// adapters in xrr.SessionFromEnv picks the right substrate.
//
// Returns a runResult with stdout, stderr, and the observed exit
// code. cobra's RunE return is interpreted as exit-1 (kit's
// global error-render middleware normally maps codes; we only
// need a coarse OK/non-OK signal in the harness layer).
func (c *config) invoke(cmd *cobra.Command) *runResult {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	res := &runResult{stdout: stdout, stderr: stderr}

	// Env restoration.
	restoreEnv := c.installEnv()
	defer restoreEnv()

	// Config snapshot (viper) restoration, mutex-guarded.
	restoreCfg, err := c.installConfigSnapshot()
	if err != nil {
		res.runErr = err
		res.exitCode = 1
		return res
	}
	defer restoreCfg()

	// TTY installation (kit-level probe seam or pty fallback).
	restoreTTY := c.installTTY()
	defer restoreTTY()

	if c.invoker != nil {
		exit, runErr := c.invoker.Invoke(c.args, c.stdin, stdout, stderr, c.env)
		res.exitCode, res.runErr = exit, runErr
		return res
	}

	// Cobra path. SetArgs replaces argv; SetIn/Out/Err redirects
	// the I/O streams. cmd.Execute() runs the leaf inside the
	// caller's process.
	cmd.SetArgs(c.args)
	cmd.SetIn(c.stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	runErr := cmd.Execute()
	res.runErr = runErr
	if runErr != nil {
		res.exitCode = exitCodeFromError(runErr)
	}
	return res
}

// installEnv applies c.env to os.Environ and returns a restore fn.
// XRR_MODE / XRR_CASSETTE_DIR are appended here too so child
// invocations that call xrr.SessionFromEnv share the parent's
// recording substrate.
func (c *config) installEnv() func() {
	var saved []struct{ k, v string }
	type setEnv struct{ k, v string }
	var pending []setEnv

	if c.cassetteDir != "" && c.mode != "" {
		pending = append(pending,
			setEnv{"XRR_CASSETTE_DIR", c.cassetteDir},
			setEnv{"XRR_MODE", string(c.mode)},
		)
	}
	for k, v := range c.env {
		pending = append(pending, setEnv{k, v})
	}
	for _, e := range pending {
		prev, ok := os.LookupEnv(e.k)
		if ok {
			saved = append(saved, struct{ k, v string }{e.k, prev})
		} else {
			saved = append(saved, struct{ k, v string }{e.k, ""})
		}
		_ = os.Setenv(e.k, e.v)
	}
	return func() {
		for i := len(saved) - 1; i >= 0; i-- {
			s := saved[i]
			if s.v == "" {
				if _, ok := os.LookupEnv(s.k); ok {
					_ = os.Unsetenv(s.k)
				}
			} else {
				_ = os.Setenv(s.k, s.v)
			}
		}
	}
}

// exitCodeFromError reproduces a minimal subset of kit's error-to-
// exit mapping so the harness can render "exit code 3 (NOT_FOUND)"
// in failure messages without depending on the error-render pipe.
//
// The unwrap chain looks for any error whose Error() prefix matches
// a known kit error code; absence falls through to 1 (GENERIC).
func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	type coded interface{ Code() string }
	var c coded
	if errors.As(err, &c) {
		return ClassToExitCode(c.Code())
	}
	return 1
}

// runCaptured invokes cmd and returns the bundled buffers; thin
// wrapper used by primitives that don't care about the cassette
// side-effects.
func runCaptured(c *config, cmd *cobra.Command) *runResult {
	return c.invoke(cmd)
}
