package wizard

import (
	"context"
	"errors"
	"io"
	"os"
)

// TUIFrontend is a function that runs the wizard with a TUI.
// Provided by the wizardtui package; kept as a func type so the
// wizard root has no bubbletea dependency.
type TUIFrontend func(ctx context.Context, w *Wizard) error

// RunOption configures Run behavior.
type RunOption func(*runConfig)

type runConfig struct {
	in         io.Reader
	out        io.Writer
	answers    map[string]any
	tuiFn      TUIFrontend
	forceTUI   bool
	forceLine  bool
	onComplete func(map[string]any) error
	dryRun     bool
}

// WithInput sets the reader for line-mode input (default: os.Stdin).
func WithInput(r io.Reader) RunOption {
	return func(c *runConfig) { c.in = r }
}

// WithOutput sets the writer for line-mode output (default: os.Stdout).
func WithOutput(w io.Writer) RunOption {
	return func(c *runConfig) { c.out = w }
}

// WithAnswers supplies a pre-filled answer map, selecting headless mode.
func WithAnswers(a map[string]any) RunOption {
	return func(c *runConfig) { c.answers = a }
}

// WithTUI registers a TUI frontend function.
func WithTUI(fn TUIFrontend) RunOption {
	return func(c *runConfig) { c.tuiFn = fn }
}

// ForceLine forces the line-oriented frontend regardless of terminal state.
func ForceLine() RunOption {
	return func(c *runConfig) { c.forceLine = true }
}

// ForceTUI forces the TUI frontend.
func ForceTUI() RunOption {
	return func(c *runConfig) { c.forceTUI = true }
}

// OnComplete registers a callback invoked when the wizard finishes.
func OnComplete(fn func(map[string]any) error) RunOption {
	return func(c *runConfig) { c.onComplete = fn }
}

// WithDryRun enables dry-run mode (Complete callback is skipped).
func WithDryRun() RunOption {
	return func(c *runConfig) { c.dryRun = true }
}

// Run drives a wizard to completion, auto-selecting the best frontend.
//
// Selection priority:
//  1. ForceLine → line frontend
//  2. ForceTUI  → TUI (error if no TUIFrontend provided)
//  3. WithAnswers → headless
//  4. stdin is a pipe → headless with empty answers
//  5. TUIFrontend registered → TUI
//  6. Fallback → line frontend
func Run(ctx context.Context, w *Wizard, opts ...RunOption) error {
	cfg := &runConfig{
		in:  os.Stdin,
		out: os.Stdout,
	}
	for _, o := range opts {
		o(cfg)
	}

	if cfg.onComplete != nil {
		w.SetOnComplete(cfg.onComplete)
	}
	if cfg.dryRun {
		w.SetDryRun(true)
	}

	switch {
	case cfg.forceLine:
		if err := RunLine(ctx, w, cfg.in, cfg.out); err != nil {
			return err
		}
		return w.Complete()

	case cfg.forceTUI:
		if cfg.tuiFn == nil {
			return errors.New(
				"TUI frontend not provided; use WithTUI()",
			)
		}
		if err := cfg.tuiFn(ctx, w); err != nil {
			return err
		}
		return w.Complete()

	case cfg.answers != nil:
		_, err := RunHeadless(ctx, w, cfg.answers)
		return err

	case isPipe(cfg.in):
		_, err := RunHeadless(ctx, w, map[string]any{})
		return err

	case cfg.tuiFn != nil:
		if err := cfg.tuiFn(ctx, w); err != nil {
			return err
		}
		return w.Complete()

	default:
		if err := RunLine(ctx, w, cfg.in, cfg.out); err != nil {
			return err
		}
		return w.Complete()
	}
}

// isPipe returns true when r is an *os.File backed by a pipe (not a
// terminal). Returns false for any other reader type.
func isPipe(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice == 0
}
