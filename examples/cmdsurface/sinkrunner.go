// sinkrunner.go is the idiomatic adapter that fans every completed
// invocation through a cmdsurface.SinkSet. Sinks are a fan-out
// primitive, not a middleware: the package does not call them
// automatically. The example wires this thin Runner wrapper around
// InProcessRunner so every Result the bridge produces — regardless of
// originating surface — reaches the configured sinks.
package main

import (
	"context"
	"log/slog"
	"strings"

	"hop.top/kit/go/transport/cmdsurface"
)

// sinkRunner wraps a Runner and emits each Run outcome through a
// SinkSet. Stream invocations bypass the sink fan-out — streaming
// sinks would need a different contract — and delegate straight to
// the inner Runner.
type sinkRunner struct {
	inner cmdsurface.Runner
	sinks cmdsurface.SinkSet
	log   *slog.Logger
}

// newSinkRunner returns a Runner that wraps inner with sink fan-out.
// log is used for non-fatal sink-error reporting; nil falls back to
// slog.Default.
func newSinkRunner(inner cmdsurface.Runner, sinks cmdsurface.SinkSet, log *slog.Logger) *sinkRunner {
	if log == nil {
		log = slog.Default()
	}
	return &sinkRunner{inner: inner, sinks: sinks, log: log}
}

// Run implements cmdsurface.Runner. It executes inv on the inner
// Runner and emits the (inv, res, err) tuple through every matching
// SinkSpec. Sink errors are logged at warn level and otherwise
// swallowed — the bridge sees only the inner Runner's return.
func (s *sinkRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	res, err := s.inner.Run(ctx, inv)
	if sinkErrs := s.sinks.Emit(ctx, inv, res, err); len(sinkErrs) > 0 {
		s.log.Warn("sink emit errors",
			"path", strings.Join(inv.Path, " "),
			"surface", string(inv.Meta.Surface),
			"errors", sinkErrs,
		)
	}
	return res, err
}

// Stream implements cmdsurface.Runner. It delegates to the inner
// Runner without sink fan-out — fanning streaming events through
// sinks-as-currently-shaped would require event-level emit contracts
// the package does not yet expose.
func (s *sinkRunner) Stream(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	return s.inner.Stream(ctx, inv, out)
}
