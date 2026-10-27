package cmdsurface

import (
	"bytes"
	"fmt"

	"github.com/spf13/cobra"
)

// DefaultOutputCap is how many bytes of each output stream, stdout and
// stderr separately, a runner keeps for the Result it returns, unless
// [WithOutputCap] sets another bound.
const DefaultOutputCap = 16 << 20

// WithOutputCap bounds how many bytes of each output stream, stdout
// and stderr separately, the runner keeps for a Result: the one Run
// returns and the one a Stream carries on its done Event. Output past
// the cap is read and discarded, so the command keeps running and is
// never blocked on a full pipe; the kept text ends with a truncation
// marker naming how many bytes were dropped, and [Result.Truncated]
// is set.
//
// A Stream's stdout and stderr Events are not capped: every line is
// delivered as it is produced. Only the copy kept for the done
// Event's Result is bounded.
//
// n <= 0 keeps [DefaultOutputCap].
func WithOutputCap(n int) RunnerOption {
	return func(c *runnerConfig) {
		if n > 0 {
			c.outputCap = n
		}
	}
}

// runnerConfig is what a [RunnerOption] sets, for either runner.
type runnerConfig struct {
	// newRoot is [WithRootFactory]'s factory; only InProcessRunner
	// reads it.
	newRoot func() *cobra.Command
	// outputCap is [WithOutputCap]'s bound; zero means the default.
	outputCap int
}

// newCapture returns an empty capture buffer bounded by c's cap.
func (c *runnerConfig) newCapture() *capturedOutput {
	limit := c.outputCap
	if limit <= 0 {
		limit = DefaultOutputCap
	}
	return &capturedOutput{limit: limit}
}

// capturedOutput keeps the first limit bytes written to it and counts
// the rest. Write never fails and always reports the whole of p as
// written, so a command writing past the cap neither errors nor
// blocks; the excess is discarded.
type capturedOutput struct {
	buf     bytes.Buffer
	limit   int
	dropped int64
}

func (c *capturedOutput) Write(p []byte) (int, error) {
	room := c.limit - c.buf.Len()
	switch {
	case room >= len(p):
		c.buf.Write(p)
	case room > 0:
		c.buf.Write(p[:room])
		c.dropped += int64(len(p) - room)
	default:
		c.dropped += int64(len(p))
	}
	return len(p), nil
}

// writeString writes s; see Write.
func (c *capturedOutput) writeString(s string) { _, _ = c.Write([]byte(s)) }

// empty reports whether nothing has been written.
func (c *capturedOutput) empty() bool { return c.buf.Len() == 0 && c.dropped == 0 }

// truncated reports whether any output was dropped.
func (c *capturedOutput) truncated() bool { return c.dropped > 0 }

// String returns the kept output, followed by the truncation marker
// when anything was dropped.
func (c *capturedOutput) String() string {
	s := c.buf.String()
	if c.dropped == 0 {
		return s
	}
	return s + truncationMarker(s, c.limit, c.dropped)
}

// truncationMarker is the line appended to a capped stream. It starts
// on a line of its own, so it never reads as part of the command's
// last line.
func truncationMarker(kept string, limit int, dropped int64) string {
	sep := ""
	if kept != "" && kept[len(kept)-1] != '\n' {
		sep = "\n"
	}
	return fmt.Sprintf("%s[cmdsurface: output truncated at %d bytes; %d bytes dropped]\n", sep, limit, dropped)
}
