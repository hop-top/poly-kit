// Package progress emits per-phase progress events for long-running CLI
// commands. Adopters call Reporter.Emit; kit decides whether to render to
// stderr as human lines or JSONL based on the active --progress-format
// flag.
//
// # Render selection
//
// kit/cli wires the active Reporter into cmd.Context() so adopter RunE
// can do:
//
//	r := progress.FromContext(cmd.Context())
//	r.Emit(ctx, progress.Event{Phase: "resolve", Item: target})
//
// The selection rules applied by kit/cli are:
//
//  1. --quiet wins. When set, the active Reporter is Discard regardless
//     of any other flag.
//  2. --progress-format json forces JSONL.
//  3. --format json (the data-output flag) implies --progress-format json
//     unless the user passed --progress-format human explicitly.
//  4. Otherwise the default is Human (stderr lines).
//
// # Output stream
//
// Progress is metadata, not data: writers passed to JSONL/Human are
// expected to be os.Stderr (or a test buffer). Stdout is reserved for
// the data envelope.
//
// # Best-effort
//
// Emit never returns an error. Encoding failures and closed-pipe writes
// are dropped silently — progress events must not abort the work that
// triggered them.
package progress

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Event is one progress update emitted by an adopter.
//
// Phase names are lowercase nouns (e.g. "resolve", "download",
// "verify"). At is filled by Emit when zero so callers may leave it
// unset.
type Event struct {
	Phase string         `json:"phase" yaml:"phase"`
	At    time.Time      `json:"at" yaml:"at"`
	Item  string         `json:"item,omitempty" yaml:"item,omitempty"`
	Bytes int64          `json:"bytes,omitempty" yaml:"bytes,omitempty"`
	Total int64          `json:"total,omitempty" yaml:"total,omitempty"`
	OK    *bool          `json:"ok,omitempty" yaml:"ok,omitempty"`
	Extra map[string]any `json:"extra,omitempty" yaml:"extra,omitempty"`
}

// Reporter is the interface adopters call into. Implementations must be
// safe for concurrent use; the built-in JSONL/Human/Discard reporters
// are.
type Reporter interface {
	Emit(ctx context.Context, e Event)
}

// JSONL returns a Reporter writing one Event per line as JSON to w
// (typically os.Stderr). Each line ends in \n; timestamps are emitted
// as RFC 3339 in UTC. Encoding errors and closed-pipe writes are
// dropped silently.
func JSONL(w io.Writer) Reporter {
	return &jsonlReporter{w: w}
}

// Human returns a Reporter rendering events as human-readable stderr
// lines: "[phase] item (bytes/total)". When Bytes/Total are non-zero,
// they are formatted as KiB to keep lines compact.
func Human(w io.Writer) Reporter {
	return &humanReporter{w: w}
}

// Discard returns a Reporter that drops every event. Used by tests and
// when --quiet is set.
func Discard() Reporter { return discardReporter{} }

// ── implementations ─────────────────────────────────────────────────────────

type jsonlReporter struct {
	mu sync.Mutex
	w  io.Writer
}

func (r *jsonlReporter) Emit(_ context.Context, e Event) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	} else {
		e.At = e.At.UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// json.NewEncoder.Encode appends a trailing newline → one JSONL row.
	_ = json.NewEncoder(r.w).Encode(e)
}

type humanReporter struct {
	mu sync.Mutex
	w  io.Writer
}

func (r *humanReporter) Emit(_ context.Context, e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// "[phase] item (bytes/total)" — the parts after the phase are
	// optional. Bytes/Total render in KiB when non-zero to keep lines
	// compact for downloads.
	if _, err := fmt.Fprintf(r.w, "[%s]", e.Phase); err != nil {
		return
	}
	if e.Item != "" {
		if _, err := fmt.Fprintf(r.w, " %s", e.Item); err != nil {
			return
		}
	}
	if e.Bytes != 0 || e.Total != 0 {
		if e.Total != 0 {
			_, _ = fmt.Fprintf(r.w, " (%s/%s)",
				formatKiB(e.Bytes), formatKiB(e.Total))
		} else {
			_, _ = fmt.Fprintf(r.w, " (%s)", formatKiB(e.Bytes))
		}
	}
	if e.OK != nil {
		mark := "ok"
		if !*e.OK {
			mark = "fail"
		}
		_, _ = fmt.Fprintf(r.w, " %s", mark)
	}
	_, _ = fmt.Fprintln(r.w)
}

// formatKiB returns "<n> KiB" with one fractional digit, matching the
// resolution most progress UIs care about.
func formatKiB(b int64) string {
	const kib = 1024.0
	return fmt.Sprintf("%.1f KiB", float64(b)/kib)
}

type discardReporter struct{}

func (discardReporter) Emit(context.Context, Event) {}

// ── context plumbing ────────────────────────────────────────────────────────

type ctxKey struct{}

// FromContext extracts the active Reporter; returns Discard() when none
// is set.
func FromContext(ctx context.Context) Reporter {
	if ctx == nil {
		return Discard()
	}
	if r, ok := ctx.Value(ctxKey{}).(Reporter); ok && r != nil {
		return r
	}
	return Discard()
}

// WithReporter attaches r to ctx for downstream readers.
func WithReporter(ctx context.Context, r Reporter) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey{}, r)
}
