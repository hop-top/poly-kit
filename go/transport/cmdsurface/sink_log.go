package cmdsurface

import (
	"context"
	"log/slog"
	"time"
)

// LogSink emits a structured log record for each invocation outcome
// via the configured slog.Handler. If Handler is nil, the handler
// from slog.Default() is used.
//
// Each record carries these attrs:
//
//   - path:      space-joined Invocation.Path
//   - surface:   Meta.Surface (string)
//   - exit_code: Result.ExitCode (int)
//   - error:     err.Error() when err != nil
//   - trace_id:  Meta.TraceID when non-empty
//   - caller:    Meta.Caller when non-empty
//
// When Level <= slog.LevelDebug, the record additionally carries
// stdout / stderr attributes copied from Result.
type LogSink struct {
	// Handler is the slog backend; nil falls back to slog.Default().
	Handler slog.Handler
	// Level controls record level; zero value is slog.LevelInfo.
	Level slog.Level
}

// Emit writes a single log record describing the outcome. The
// returned error is always nil — slog handlers do not surface
// failures through this path, and downstream sinks must remain
// best-effort.
func (l *LogSink) Emit(ctx context.Context, inv Invocation, res Result, err error) error {
	h := l.Handler
	if h == nil {
		h = slog.Default().Handler()
	}
	level := l.Level
	if !h.Enabled(ctx, level) {
		return nil
	}

	attrs := []slog.Attr{
		slog.String("path", joinPath(inv.Path)),
		slog.String("surface", string(inv.Meta.Surface)),
		slog.Int("exit_code", res.ExitCode),
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	if inv.Meta.TraceID != "" {
		attrs = append(attrs, slog.String("trace_id", inv.Meta.TraceID))
	}
	if inv.Meta.Caller != "" {
		attrs = append(attrs, slog.String("caller", inv.Meta.Caller))
	}
	if level <= slog.LevelDebug {
		if res.Stdout != "" {
			attrs = append(attrs, slog.String("stdout", res.Stdout))
		}
		if res.Stderr != "" {
			attrs = append(attrs, slog.String("stderr", res.Stderr))
		}
	}

	rec := slog.NewRecord(time.Now(), level, "cmdsurface invocation", 0)
	rec.AddAttrs(attrs...)
	_ = h.Handle(ctx, rec)
	return nil
}
