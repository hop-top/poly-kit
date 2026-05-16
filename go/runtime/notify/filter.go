package notify

import (
	"context"

	"hop.top/kit/go/runtime/bus"
)

// FilterSink wraps an inner Sink, forwarding events only when every
// configured option allows them. With no options, it passes
// everything. Filter rejection is silent (Drain returns nil); errors
// from the inner Sink propagate unchanged.
//
// FilterSink composes with itself: wrapping an existing FilterSink
// in another FilterSink stacks the filters (logical AND across the
// chain). Order is irrelevant for correctness; choose whichever
// reads better at the call site.
//
// Compose with RetrySink, dead-letter sinks, and reference outbound
// sinks (webhook / email / osnotify) to build channel-specific
// pipelines without reaching into TeeBus internals.
type FilterSink struct {
	inner bus.Sink
	opts  filterOpts
}

type filterOpts struct {
	pattern    string
	hasPattern bool
	minSev     Severity
	hasMinSev  bool
	predicate  func(bus.Event) bool
}

// FilterOption configures a FilterSink. Multiple options compose via
// logical AND inside Drain.
type FilterOption func(*filterOpts)

// WithTopicPattern drops events whose topic does not match pattern.
// Pattern semantics follow bus.Topic.Match: exact, "*" segment
// wildcard, "#" trailing-segments wildcard.
func WithTopicPattern(pattern string) FilterOption {
	return func(o *filterOpts) {
		o.pattern = pattern
		o.hasPattern = true
	}
}

// WithMinSeverity drops events whose payload severity (per
// SeverityOf) is below s.
func WithMinSeverity(s Severity) FilterOption {
	return func(o *filterOpts) {
		o.minSev = s
		o.hasMinSev = true
	}
}

// WithPredicate drops events for which fn returns false. fn is
// called once per event after the topic and severity checks have
// passed; nil fn is ignored.
func WithPredicate(fn func(bus.Event) bool) FilterOption {
	return func(o *filterOpts) {
		if fn != nil {
			o.predicate = fn
		}
	}
}

// NewFilterSink returns a Sink that forwards events to inner only
// when every configured option allows them. With no options, it
// passes everything.
func NewFilterSink(inner bus.Sink, opts ...FilterOption) *FilterSink {
	f := &FilterSink{inner: inner}
	for _, opt := range opts {
		opt(&f.opts)
	}
	return f
}

// Drain forwards e to the inner sink iff every configured option
// allows it. Filter rejections are silent (return nil); errors from
// the inner sink propagate unchanged.
func (f *FilterSink) Drain(ctx context.Context, e bus.Event) error {
	if f.opts.hasPattern && !e.Topic.Match(f.opts.pattern) {
		return nil
	}
	if f.opts.hasMinSev && SeverityOf(e) < f.opts.minSev {
		return nil
	}
	if f.opts.predicate != nil && !f.opts.predicate(e) {
		return nil
	}
	return f.inner.Drain(ctx, e)
}

// Close closes the inner sink.
func (f *FilterSink) Close() error {
	return f.inner.Close()
}
