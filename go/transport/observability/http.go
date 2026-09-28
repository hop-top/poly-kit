package observability

import (
	"context"
	"net"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"hop.top/kit/go/transport/api"
)

// HTTPMiddleware returns the tracing and metrics middleware of an HTTP
// service, for slot 5 of the HTTP chain: after the request id, client
// address, access log and recovery, before everything that can refuse.
// It returns nil when the Provider records nothing, so a caller
// installs nothing at all.
//
// Per request it extracts the caller's W3C trace context and starts
// the server span (otelhttp), records the http.server.* instruments
// and the kit.serve.http.requests.active gauge labeled kit.service,
// and counts a refusal recorded below it with [api.RecordRefusal] by
// its code. The request's span is current in the handler's context,
// so the invocation span the bridge starts is its child.
func (p *Provider) HTTPMiddleware(service string) func(http.Handler) http.Handler {
	if !p.Enabled() {
		return nil
	}
	tp := p.tp
	if tp == nil {
		// Metrics without tracing: otelhttp still needs a tracer, and
		// a no-op one keeps the caller's context flowing unrecorded.
		tp = tracenoop.NewTracerProvider()
	}
	opts := []otelhttp.Option{
		otelhttp.WithTracerProvider(tp),
		otelhttp.WithPropagators(p.prop),
		otelhttp.WithSpanNameFormatter(spanName),
		otelhttp.WithMetricAttributesFn(func(*http.Request) []attribute.KeyValue {
			return []attribute.KeyValue{AttrService.String(service)}
		}),
	}
	if p.mp != nil {
		opts = append(opts, otelhttp.WithMeterProvider(p.mp))
	} else {
		opts = append(opts, otelhttp.WithMeterProvider(metricnoop.NewMeterProvider()))
	}

	svc := attribute.NewSet(AttrService.String(service))
	return func(next http.Handler) http.Handler {
		counted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			restoreForwardedFor(r)
			r, refused := api.ObserveRefusal(r)
			r, route := api.ObserveRoute(r)
			if in := p.inst; in != nil {
				in.httpActive.Add(r.Context(), 1, metric.WithAttributeSet(svc))
				defer in.httpActive.Add(r.Context(), -1, metric.WithAttributeSet(svc))
			}
			next.ServeHTTP(w, r)
			// Slot 5 wraps the router, so the pattern it matched is
			// only known now; the span is still open.
			if pattern := route(); pattern != "" {
				trace.SpanFromContext(r.Context()).SetName(routeSpanName(r.Method, pattern))
			}
			code := refused()
			if code == "" {
				return
			}
			trace.SpanFromContext(r.Context()).AddEvent(refusalEventName,
				trace.WithAttributes(AttrRefusalReason.String(code)))
			if in := p.inst; in != nil {
				in.httpRefusals.Add(r.Context(), 1, metric.WithAttributes(
					AttrService.String(service), AttrRefusalReason.String(code)))
			}
		})
		instrumented := otelhttp.NewHandler(counted, service, opts...)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			instrumented.ServeHTTP(w, maskForwardedFor(r))
		})
	}
}

type forwardedForKey struct{}

// maskForwardedFor shows otelhttp the client address slot 2 resolved.
// otelhttp records the first X-Forwarded-For entry as client.address
// whoever sent it, and that entry is whatever the client wrote; kit
// believes forwarding headers only from a trusted proxy and has
// already made the client the request's RemoteAddr. The span sees an
// X-Forwarded-For of that address alone, and [restoreForwardedFor]
// puts the original back before the handler runs.
func maskForwardedFor(r *http.Request) *http.Request {
	orig, ok := r.Header[api.HeaderXForwardedFor]
	if !ok {
		return r
	}
	r = r.Clone(context.WithValue(r.Context(), forwardedForKey{}, orig))
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "" {
		r.Header.Del(api.HeaderXForwardedFor)
	} else {
		r.Header.Set(api.HeaderXForwardedFor, host)
	}
	return r
}

// restoreForwardedFor undoes [maskForwardedFor] on the request the
// handler receives.
func restoreForwardedFor(r *http.Request) {
	if orig, ok := r.Context().Value(forwardedForKey{}).([]string); ok {
		r.Header[api.HeaderXForwardedFor] = orig
	}
}

// spanName names an HTTP server span "<METHOD> <route>" when the mux
// has matched a route pattern, else "<METHOD>": a raw path would put
// caller-chosen text into the span name. In front of a router nothing
// has matched yet, so the span starts as "<METHOD>" and is renamed by
// route once the router reports its pattern (see [api.ObserveRoute]).
func spanName(_ string, r *http.Request) string {
	return routeSpanName(r.Method, r.Pattern)
}

// routeSpanName is "<METHOD> <pattern>", or "<METHOD>" with no pattern.
func routeSpanName(method, pattern string) string {
	if pattern == "" {
		return method
	}
	// A method-qualified pattern already starts with the method.
	if len(pattern) > len(method) && pattern[:len(method)+1] == method+" " {
		return pattern
	}
	return method + " " + pattern
}
