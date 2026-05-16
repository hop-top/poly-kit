// Lifecycle hook execution for template engine. Runs hook scripts via
// /bin/sh in declared order, piping HookContext as JSON on stdin and
// forwarding stdout/stderr with a "[hook:<basename>] " line prefix.
// Aborts on first non-zero exit via NewHookFailedError.
//
// See docs/superpowers/specs/2026-04-26-kit-init-design.md §16.
package template

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
)

// HookContext is the JSON payload piped to hook scripts on stdin.
type HookContext struct {
	Vars      map[string]any `json:"vars"`
	Mode      string         `json:"mode"` // "bootstrap" | "augment"
	Tier      int            `json:"tier"`
	TargetDir string         `json:"target_dir"`
}

// Run executes hookScripts (paths relative to templateRoot) in order
// via /bin/sh. Aborts on first failure with NewHookFailedError.
// Forwarded output is best-effort: write failures on out are logged
// but do not abort hook execution.
func Run(ctx context.Context, hookScripts []string, templateRoot string,
	hookCtx HookContext, out io.Writer,
) error {
	payload, err := json.Marshal(hookCtx)
	if err != nil {
		return fmt.Errorf("template: marshal hook context: %w", err)
	}

	for _, script := range hookScripts {
		name := filepath.Base(script)
		scriptPath := filepath.Join(templateRoot, script)

		cmd := exec.CommandContext(ctx, "/bin/sh", scriptPath)
		cmd.Stdin = bytes.NewReader(payload)
		pw := &prefixWriter{out: out, prefix: fmt.Sprintf("[hook:%s] ", name)}
		cmd.Stdout = pw
		cmd.Stderr = pw

		runErr := cmd.Run()
		// Flush any trailing un-newlined fragment for this script.
		pw.flush()

		if runErr != nil {
			exitCode := -1
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				exitCode = exitErr.ExitCode()
			}
			return NewHookFailedError(name, exitCode)
		}
	}
	return nil
}

// prefixWriter prefixes each complete line written to out. Trailing
// fragments without a newline are buffered until the next Write or
// flush. Always reports len(b) consumed so callers (cmd.Stdout/Stderr)
// don't see short writes; downstream errors are logged best-effort.
type prefixWriter struct {
	out     io.Writer
	prefix  string
	pending []byte
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	consumed := len(b)
	data := append(p.pending, b...)
	p.pending = nil

	for {
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			p.pending = append(p.pending[:0], data...)
			break
		}
		line := data[:idx+1]
		if _, err := io.WriteString(p.out, p.prefix); err != nil {
			slog.Warn("hook output forwarding failed", "err", err)
			return consumed, nil
		}
		if _, err := p.out.Write(line); err != nil {
			slog.Warn("hook output forwarding failed", "err", err)
			return consumed, nil
		}
		data = data[idx+1:]
	}
	return consumed, nil
}

// flush emits any buffered fragment as a final prefixed line (adding
// a trailing newline). Best-effort; write errors are logged.
func (p *prefixWriter) flush() {
	if len(p.pending) == 0 {
		return
	}
	if _, err := io.WriteString(p.out, p.prefix); err != nil {
		slog.Warn("hook output forwarding failed", "err", err)
		p.pending = nil
		return
	}
	if _, err := p.out.Write(p.pending); err != nil {
		slog.Warn("hook output forwarding failed", "err", err)
		p.pending = nil
		return
	}
	if _, err := p.out.Write([]byte{'\n'}); err != nil {
		slog.Warn("hook output forwarding failed", "err", err)
	}
	p.pending = nil
}
