package observability

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"hop.top/kit/go/transport/cmdsurface"
)

// Attribute keys on kit's spans and metrics.
const (
	AttrService       = attribute.Key("kit.service")
	AttrSurface       = attribute.Key("kit.surface")
	AttrCommand       = attribute.Key("kit.command")
	AttrOutcome       = attribute.Key("kit.outcome")
	AttrRefusalReason = attribute.Key("kit.refusal.reason")
	AttrRequestID     = attribute.Key("kit.request_id")
	AttrExitCode      = attribute.Key("kit.exit_code")
)

// Outcomes a verdict is counted under.
const (
	OutcomeOK      = "ok"
	OutcomeError   = "error"
	OutcomeRefused = "refused"
)

// Instrument names.
const (
	MetricRequests       = "kit.serve.requests"
	MetricDuration       = "kit.serve.request.duration"
	MetricActive         = "kit.serve.requests.active"
	MetricRefusals       = "kit.serve.refusals"
	MetricHTTPActive     = "kit.serve.http.requests.active"
	MetricHTTPRefusals   = "kit.serve.http.refusals"
	refusalEventName     = "kit.refusal"
	invocationSpanPrefix = "invoke "
)

// durationBuckets are the bucket boundaries of [MetricDuration], in
// seconds: the OpenTelemetry semantic conventions' recommendation for
// http.server.request.duration. The SDK's default boundaries (0 to
// 10000) suit milliseconds and would put nearly every sample in the
// first bucket. They are an advisory on the instrument, so a view on
// an adopter's own meter provider still overrides them.
var durationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10,
}

// instruments are the metric instruments kit records served commands
// with. One set serves every service a Provider instruments; the
// service is an attribute.
type instruments struct {
	requests     metric.Int64Counter
	duration     metric.Float64Histogram
	active       metric.Int64UpDownCounter
	refusals     metric.Int64Counter
	httpActive   metric.Int64UpDownCounter
	httpRefusals metric.Int64Counter
}

func newInstruments(m metric.Meter) (*instruments, error) {
	var in instruments
	var err, e error
	in.requests, e = m.Int64Counter(MetricRequests, metric.WithUnit("{request}"),
		metric.WithDescription("Served invocations by verdict: ran ok, ran and failed, or refused."))
	err = errors.Join(err, e)
	in.duration, e = m.Float64Histogram(MetricDuration, metric.WithUnit("s"),
		metric.WithDescription("Time from receipt to verdict of a served invocation, refusals included."),
		metric.WithExplicitBucketBoundaries(durationBuckets...))
	err = errors.Join(err, e)
	in.active, e = m.Int64UpDownCounter(MetricActive, metric.WithUnit("{request}"),
		metric.WithDescription("Served invocations running now, past every gate."))
	err = errors.Join(err, e)
	in.refusals, e = m.Int64Counter(MetricRefusals, metric.WithUnit("{refusal}"),
		metric.WithDescription("Served invocations refused, by refusal code."))
	err = errors.Join(err, e)
	in.httpActive, e = m.Int64UpDownCounter(MetricHTTPActive, metric.WithUnit("{request}"),
		metric.WithDescription("HTTP requests in progress on a served listener."))
	err = errors.Join(err, e)
	in.httpRefusals, e = m.Int64Counter(MetricHTTPRefusals, metric.WithUnit("{refusal}"),
		metric.WithDescription("HTTP requests refused by HTTP-plane middleware, by refusal code."))
	err = errors.Join(err, e)
	if err != nil {
		return nil, err
	}
	return &in, nil
}

// BridgeOptions returns the cmdsurface bridge options that instrument
// service's invocations: a runner middleware (a span per invocation,
// the in-flight gauge) and an audit sink (every verdict counted and
// timed, refusals by code). It returns nil when the Provider records
// nothing.
func (p *Provider) BridgeOptions(service string) []cmdsurface.Option {
	if !p.Enabled() {
		return nil
	}
	return []cmdsurface.Option{
		cmdsurface.WithRunnerMiddleware(p.RunnerMiddleware(service)),
		cmdsurface.WithSinks(p.Sink(service)),
	}
}

// RunnerMiddleware returns a runner middleware that records service's
// invocations: a span per invocation, the child of the request's span
// when the transport started one, else of the caller's propagated
// Meta.Traceparent; and the in-flight gauge.
//
// The invocation handed to the wrapped runner carries the invocation
// span's own trace context in Meta.Traceparent and Meta.Tracestate,
// so a subprocess runner hands the child a parent inside this trace.
func (p *Provider) RunnerMiddleware(service string) func(cmdsurface.Runner) cmdsurface.Runner {
	return func(next cmdsurface.Runner) cmdsurface.Runner {
		if !p.Enabled() {
			return next
		}
		return &observedRunner{next: next, p: p, service: service}
	}
}

type observedRunner struct {
	next    cmdsurface.Runner
	p       *Provider
	service string
}

// Run implements cmdsurface.Runner.
func (r *observedRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	ctx, span, done := r.begin(ctx, &inv)
	res, err := r.next.Run(ctx, inv)
	done()
	endSpan(span, res.ExitCode, err)
	return res, err
}

// Stream implements cmdsurface.Runner. Events pass through unchanged;
// the terminal event's exit code is read on the way past.
func (r *observedRunner) Stream(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	ctx, span, done := r.begin(ctx, &inv)

	in := make(chan cmdsurface.Event)
	exit := make(chan int, 1)
	go func() {
		code := 0
		for ev := range in {
			if ev.Kind == "done" {
				if res, ok := ev.Data.(*cmdsurface.Result); ok && res != nil {
					code = res.ExitCode
				}
			}
			out <- ev
		}
		close(out)
		exit <- code
	}()

	err := r.next.Stream(ctx, inv, in)
	code := <-exit
	done()
	endSpan(span, code, err)
	return err
}

// begin starts the invocation's span, when tracing is on, and counts
// the invocation in flight, when metrics are. It rewrites inv's trace
// context to the span's, and returns the span (nil when none was
// started) and the function that takes the invocation out of flight.
func (r *observedRunner) begin(ctx context.Context, inv *cmdsurface.Invocation) (context.Context, trace.Span, func()) {
	var span trace.Span
	if r.p.tracer != nil {
		ctx, span = r.p.startInvocation(ctx, r.service, inv)
	}

	done := func() {}
	if in := r.p.inst; in != nil {
		set := metric.WithAttributeSet(attribute.NewSet(
			AttrService.String(r.service),
			AttrSurface.String(string(inv.Meta.Surface)),
		))
		in.active.Add(ctx, 1, set)
		done = func() { in.active.Add(context.WithoutCancel(ctx), -1, set) }
	}
	return ctx, span, done
}

// startInvocation starts the invocation span under the request's span,
// or under the caller's propagated context when no span is current,
// and hands the new span's context down through inv.Meta.
func (p *Provider) startInvocation(ctx context.Context, service string, inv *cmdsurface.Invocation) (context.Context, trace.Span) {
	kind := trace.SpanKindInternal
	if !trace.SpanContextFromContext(ctx).IsValid() {
		// No local span: this is where the process first sees the
		// request, so the span is the server side of the caller's.
		kind = trace.SpanKindServer
		if inv.Meta.Traceparent != "" {
			ctx = p.prop.Extract(ctx, propagation.MapCarrier{
				"traceparent": inv.Meta.Traceparent,
				"tracestate":  inv.Meta.Tracestate,
			})
		}
	}

	path := strings.Join(inv.Path, " ")
	attrs := []attribute.KeyValue{
		AttrService.String(service),
		AttrSurface.String(string(inv.Meta.Surface)),
		AttrCommand.String(path),
	}
	if inv.Meta.RequestID != "" {
		attrs = append(attrs, AttrRequestID.String(inv.Meta.RequestID))
	}
	ctx, span := p.tracer.Start(ctx, invocationSpanPrefix+path,
		trace.WithSpanKind(kind), trace.WithAttributes(attrs...))

	carrier := propagation.MapCarrier{}
	p.prop.Inject(ctx, carrier)
	if tp := carrier.Get("traceparent"); tp != "" {
		inv.Meta.Traceparent = tp
		inv.Meta.Tracestate = carrier.Get("tracestate")
		if inv.Meta.TraceID == "" {
			inv.Meta.TraceID = span.SpanContext().TraceID().String()
		}
	}
	return ctx, span
}

// endSpan records the invocation's verdict on the span the runner
// middleware started and ends it; nil (tracing off) is a no-op.
func endSpan(span trace.Span, exitCode int, err error) {
	if span == nil {
		return
	}
	span.SetAttributes(AttrExitCode.Int(exitCode))
	switch {
	case err != nil:
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	case exitCode != 0:
		span.SetStatus(codes.Error, fmt.Sprintf("exit code %d", exitCode))
	}
	span.End()
}

// Sink returns the audit sink that counts and times service's
// verdicts. The bridge emits to it for every refusal and every
// execution on a remote surface, and a transport reports its own
// authentication refusals through it, so one place sees every verdict.
// A refusal also adds a kit.refusal event to the request's span.
func (p *Provider) Sink(service string) cmdsurface.SinkSpec {
	return cmdsurface.SinkSpec{Sink: &verdictSink{p: p, service: service}, OnError: true, OnOK: true}
}

type verdictSink struct {
	p       *Provider
	service string
}

// Emit implements cmdsurface.Sink.
func (s *verdictSink) Emit(ctx context.Context, inv cmdsurface.Invocation, res cmdsurface.Result, err error) error {
	outcome, code := verdict(res, err)
	attrs := []attribute.KeyValue{
		AttrService.String(s.service),
		AttrSurface.String(string(inv.Meta.Surface)),
		AttrOutcome.String(outcome),
	}
	// An unresolved path is whatever the caller sent: labeling a
	// metric with it would let any caller mint series.
	if code != RefusalUnknownCommand && len(inv.Path) > 0 {
		attrs = append(attrs, AttrCommand.String(strings.Join(inv.Path, " ")))
	}

	if code != "" {
		trace.SpanFromContext(ctx).AddEvent(refusalEventName,
			trace.WithAttributes(AttrRefusalReason.String(code)))
	}
	in := s.p.inst
	if in == nil {
		return nil
	}
	if code != "" {
		attrs = append(attrs, AttrRefusalReason.String(code))
		in.refusals.Add(ctx, 1, metric.WithAttributes(
			AttrService.String(s.service),
			AttrSurface.String(string(inv.Meta.Surface)),
			AttrRefusalReason.String(code)))
	}
	set := metric.WithAttributeSet(attribute.NewSet(attrs...))
	in.requests.Add(ctx, 1, set)
	if !inv.Meta.RequestedAt.IsZero() {
		in.duration.Record(ctx, time.Since(inv.Meta.RequestedAt).Seconds(), set)
	}
	return nil
}

// Refusal codes, the stable strings every surface reports a refusal
// with. Only the ones the bridge and the transport edge can produce
// today are classified here.
const (
	RefusalUnknownCommand     = "unknown_command"
	RefusalNotEnabled         = "not_enabled"
	RefusalNotInvocable       = "not_invocable"
	RefusalDestructiveBlocked = "destructive_blocked"
	RefusalPermissionDenied   = "permission_denied"
	RefusalUnauthenticated    = "unauthenticated"
	RefusalRateLimited        = "rate_limited"
	RefusalDeadlineExceeded   = "deadline_exceeded"

	RefusalIdempotencyConflict  = cmdsurface.CodeIdempotencyConflict
	RefusalIdempotencyKeyReused = cmdsurface.CodeIdempotencyKeyReused
)

// verdict classifies one emitted record: its outcome, and its refusal
// code when refused.
func verdict(res cmdsurface.Result, err error) (outcome, code string) {
	if code = RefusalCode(err); code != "" {
		return OutcomeRefused, code
	}
	if err != nil || res.ExitCode != 0 {
		return OutcomeError, ""
	}
	return OutcomeOK, ""
}

// RefusalCode maps an error the bridge or a transport edge returned to
// its refusal code, or "" when err is not a refusal. The more specific
// class is tested first, so a sentinel that wraps a broader one is
// reported as itself.
func RefusalCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, cmdsurface.ErrAuthRefused):
		return RefusalUnauthenticated
	case errors.Is(err, cmdsurface.ErrUnknownCommand):
		return RefusalUnknownCommand
	case errors.Is(err, cmdsurface.ErrSurfaceNotEnabled):
		return RefusalNotEnabled
	case errors.Is(err, cmdsurface.ErrNotInvocable):
		return RefusalNotInvocable
	case errors.Is(err, cmdsurface.ErrDestructiveBlocked):
		return RefusalDestructiveBlocked
	case errors.Is(err, cmdsurface.ErrPermissionDenied):
		return RefusalPermissionDenied
	case errors.Is(err, cmdsurface.ErrRateLimited):
		return RefusalRateLimited
	case errors.Is(err, context.DeadlineExceeded):
		return RefusalDeadlineExceeded
	}
	return cmdsurface.IdempotencyRefusalCode(err)
}
