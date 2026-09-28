package cmdsurface

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// AnnotationTimeout is the cobra annotation that bounds one served
// invocation of a command: a Go duration ("30s", "2m"). It is the
// per-command deadline, and wins over the bridge's default
// ([WithCommandTimeout]). A caller may shorten it per call with a
// context deadline of its own; nothing lengthens it.
const AnnotationTimeout = "kit/timeout"

// ErrDeadlineExceeded is returned when an invocation ran past its
// per-command deadline and the runner reported the cancellation. It
// wraps [context.DeadlineExceeded], so a transport that predates the
// class still answers it as a deadline rather than as internal.
// Transports map it to 504, Connect DeadlineExceeded, an MCP isError
// result and the socket's DEADLINE_EXCEEDED, with the stable code
// deadline_exceeded.
var ErrDeadlineExceeded error = deadlineExceeded{}

// deadlineExceeded is [ErrDeadlineExceeded]: its own message, and
// [context.DeadlineExceeded] to errors.Is.
type deadlineExceeded struct{}

func (deadlineExceeded) Error() string { return "cmdsurface: deadline exceeded" }

// Is reports context.DeadlineExceeded as the class this one narrows.
func (deadlineExceeded) Is(target error) bool { return target == context.DeadlineExceeded }

// Timeout reports true, as context.DeadlineExceeded does.
func (deadlineExceeded) Timeout() bool { return true }

// CodeDeadlineExceeded is the stable refusal code of
// [ErrDeadlineExceeded] on every surface.
const CodeDeadlineExceeded = "deadline_exceeded"

// WithCommandTimeout sets the deadline of every invocation whose leaf
// declares no [AnnotationTimeout]. Zero or negative means none, the
// default. The kit-shipped services set it from
// services.<svc>.timeouts.command.
func WithCommandTimeout(d time.Duration) Option {
	return func(c *bridgeConfig) { c.commandTimeout = max(d, 0) }
}

// CommandTimeout reads cmd's [AnnotationTimeout]: zero when the
// annotation is absent, an error naming the command when it is not a
// positive duration.
func CommandTimeout(cmd *cobra.Command) (time.Duration, error) {
	if cmd == nil {
		return 0, nil
	}
	raw, ok := cmd.Annotations[AnnotationTimeout]
	if !ok {
		return 0, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err == nil && d <= 0 {
		err = errors.New("not positive")
	}
	if err != nil {
		return 0, fmt.Errorf("%s: annotation %s=%q: want a positive duration such as 30s (%v)",
			cmd.CommandPath(), AnnotationTimeout, raw, err)
	}
	return d, nil
}

// ValidateCommandTimeouts reports every command under root whose
// [AnnotationTimeout] does not parse, one per line. The bridge
// ignores a malformed annotation, falling back to its default; a
// service refuses it at validation instead, so a bound the author
// wrote is never silently dropped.
func ValidateCommandTimeouts(root *cobra.Command) error {
	var errs []error
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if _, err := CommandTimeout(c); err != nil {
			errs = append(errs, err)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	if root != nil {
		walk(root)
	}
	return errors.Join(errs...)
}

// deadline is the per-command deadline of leaf: its annotation, else
// the bridge default; zero means none.
func (b *Bridge) deadline(leaf *Leaf) time.Duration {
	if leaf != nil && leaf.Timeout > 0 {
		return leaf.Timeout
	}
	return b.cfg.commandTimeout
}

// armDeadline bounds ctx by leaf's per-command deadline. It is armed
// when the admitted call is about to run — the capacity slot — so it
// covers queue wait and execution and never the time a person spends
// answering a confirmation. A caller whose ctx already carries an
// earlier deadline keeps it: a call can shorten the bound, never
// lengthen it. bound is the deadline armed here, zero when none was
// or the caller's own is earlier.
func (b *Bridge) armDeadline(ctx context.Context, leaf *Leaf) (_ context.Context, cancel context.CancelFunc, bound time.Duration) {
	d := b.deadline(leaf)
	if d <= 0 {
		return ctx, func() {}, 0
	}
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) <= d {
		return ctx, func() {}, 0
	}
	ctx, cancel = context.WithTimeout(ctx, d)
	return ctx, cancel, d
}

// deadlineError reports a run that ended because ctx's deadline
// passed as [ErrDeadlineExceeded], naming the command and, when the
// bridge armed it, the bound. Only a runner error that is itself the
// deadline qualifies: a command that completed despite the deadline
// completed, and one that failed on its own keeps its error.
func deadlineError(ctx context.Context, err error, leaf *Leaf, bound time.Duration) error {
	if err == nil || errors.Is(err, ErrDeadlineExceeded) {
		return err
	}
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return err
	}
	name := "the command"
	if leaf != nil {
		name = leaf.PathKey()
	}
	if bound > 0 {
		return fmt.Errorf("%w: %s ran past its %s deadline", ErrDeadlineExceeded, name, bound)
	}
	return fmt.Errorf("%w: %s ran past the caller's deadline", ErrDeadlineExceeded, name)
}
