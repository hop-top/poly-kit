package observability

import (
	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// RPCInterceptor returns the ConnectRPC interceptor that traces and
// measures RPC calls, for the tracing slot of the RPC server: pass it
// to rpc.WithInterceptors or cmdsurface.WithRPCInterceptors. It returns
// nil, and no error, when the Provider records nothing.
//
// The server span is the child of the caller's W3C traceparent (the
// remote parent is trusted, as the HTTP middleware trusts it), and the
// rpc.server.call.duration instrument is recorded when metrics are on.
// The span is current in the handler's context, so the invocation span
// the bridge starts is its child.
func (p *Provider) RPCInterceptor() (connect.Interceptor, error) {
	if !p.Enabled() {
		return nil, nil
	}
	opts := []otelconnect.Option{
		otelconnect.WithPropagator(p.prop),
		otelconnect.WithTrustRemote(),
	}
	if p.tp != nil {
		opts = append(opts, otelconnect.WithTracerProvider(p.tp))
	} else {
		opts = append(opts, otelconnect.WithTracerProvider(tracenoop.NewTracerProvider()))
	}
	if p.mp != nil {
		opts = append(opts, otelconnect.WithMeterProvider(p.mp))
	} else {
		opts = append(opts, otelconnect.WithoutMetrics())
	}
	return otelconnect.NewInterceptor(opts...)
}
