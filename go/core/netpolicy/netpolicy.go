// Package netpolicy owns the process-wide network policy marker and the
// http.RoundTripper that enforces it.
//
// The --offline global (cli-parity-guide, "Global Flags") promises that
// network access is disabled. Stamping a context value alone cannot keep
// that promise: it is advisory, so any caller that forgets to consult it
// still reaches the wire. Guard closes that gap by refusing the request
// inside the transport, beneath every http.Client, where no caller can
// route around it.
//
// The marker lives here rather than in go/console/cli because cli imports
// go/core/*; transports enforcing the policy would otherwise import cli
// back and cycle. cli re-exports WithOffline/IsOffline as forwarders so
// the existing call sites keep working.
//
// Loopback is deliberately exempt. --offline means "do not talk to the
// network", not "do not talk to myself": a local `kit serve` peer, a dev
// backend on 127.0.0.1 and unix sockets stay reachable so offline
// workflows remain usable.
//
// # Scope
//
// Enforcement has two seams, because Go offers no single one.
//
// Guard sits in the http.RoundTripper chain, and Install puts it under
// http.DefaultTransport, so every client that does not set its own
// Transport is covered without a per-site change. That is all HTTP and
// HTTPS through net/http, and with it ConnectRPC, which rides an
// ordinary *http.Client.
//
// GuardDial is the seam for everything net/http does not own. Go has no
// hook beneath net.Dial: a library that opens its own socket cannot be
// intercepted from outside, so it is covered only once its dial function
// is routed through GuardDial. Libraries that accept an injected dialer
// — net/smtp via net.Dialer, coder/websocket via DialOptions.HTTPClient,
// go-sql-driver/mysql via Config.DialFunc or RegisterDialContext, gRPC
// via WithContextDialer, crypto/tls via tls.Client over a dialed conn —
// can therefore all be brought under the policy. CheckDial exposes the
// same decision for hooks that are not shaped like a dialer.
//
// # kit's own egress
//
// Every egress path kit itself owns is now routed through one of the two
// seams:
//
//   - SMTP (runtime/notify/sinks/email) dials through GuardDial. The
//     mailer still accepts a *net.Dialer, which cannot itself be made
//     policy-aware; the guard wraps its DialContext at the call site.
//   - WebSocket (transport/api/client and runtime/bus) hands
//     coder/websocket an *http.Client whose transport is Guard-wrapped.
//     Naming it explicitly rather than leaning on http.DefaultClient
//     keeps enforcement independent of whether Install has run. The bus
//     reconnect loop starts from its own root context and replays the
//     policy its last Connect saw, so a dropped peer is not re-dialed
//     on an offline run.
//   - TiDB/MySQL (storage/kv/tidb) sets Config.DialFunc to a guarded
//     dialer. The driver invokes it with the context of the query that
//     triggered the connection, so the marker survives sql.Open's
//     laziness and the refusal lands on first use.
//
// One gap remains inside kit, bounded by an API rather than by Go:
// kv.Open and kv.Opener carry no context, so a Store opened through the
// backend registry cannot police its own open-time ping. The guarded
// dial hook is installed regardless, so every query through that Store
// is covered; only the initial connect escapes. Callers with a context
// should use tidb.NewContext.
//
// What remains uncovered, and cannot be covered from this package:
//
//   - A dependency that calls net.Dial itself and exposes no dialer
//     hook. Nothing in Go can intercept it; the only fixes are upstream
//     or a sandbox outside the process.
//   - A client holding a *net.Dialer rather than a dial function. Only
//     DialContext carries a context, and DialContext is a method, not a
//     replaceable field, so a *net.Dialer cannot be made policy-aware.
//     Such a call site must call GuardDial's result directly.
//   - A caller that captured http.DefaultTransport before Install ran,
//     or that sets an explicit Transport without wrapping it in Guard.
//
// For anything in that list --offline stays advisory and the call site
// must consult IsOffline itself.
package netpolicy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
)

// ErrOffline is returned by Guard when a request is attempted on a
// context marked offline. Callers match it with errors.Is; it is wrapped
// in a *url.Error by net/http, which errors.Is unwraps.
var ErrOffline = errors.New("network disabled by --offline")

type offlineCtxKey struct{}

// WithOffline returns a context marked as offline. Passing false returns
// ctx unchanged so an untagged context stays clean.
func WithOffline(ctx context.Context, offline bool) context.Context {
	if !offline {
		return ctx
	}
	return context.WithValue(ctx, offlineCtxKey{}, true)
}

// IsOffline reports whether ctx carries the offline marker. nil-safe.
func IsOffline(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	on, _ := ctx.Value(offlineCtxKey{}).(bool)
	return on
}

// isLoopback reports whether host names a loopback address. Hosts that
// are not literal IPs (DNS names) are treated as remote: resolving them
// would itself be network access.
func isLoopback(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// guard is the RoundTripper returned by Guard.
type guard struct{ base http.RoundTripper }

// Guard wraps base so requests on an offline-marked context fail with
// ErrOffline instead of reaching the network. A nil base falls back to
// http.DefaultTransport. Wrapping is idempotent: guarding an already
// guarded transport returns it unchanged.
func Guard(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if g, ok := base.(*guard); ok {
		return g
	}
	return &guard{base: base}
}

// RoundTrip refuses the request when its context is marked offline and
// the destination is not loopback.
func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if IsOffline(req.Context()) && !isLoopback(req.URL.Host) {
		return nil, fmt.Errorf("%s %s: %w", req.Method, req.URL.Redacted(), ErrOffline)
	}
	return g.base.RoundTrip(req)
}

// Install wraps http.DefaultTransport with Guard, so every client that
// does not set an explicit Transport — the common case across kit and
// adopter code — enforces the policy without a per-site change.
//
// It is idempotent and safe to call more than once. Call it once during
// process start-up (cli.New does this) and never concurrently with
// in-flight requests: it mutates a process-global.
//
// Clients that DO set their own Transport must wrap it themselves with
// Guard; Install cannot reach them.
func Install() {
	http.DefaultTransport = Guard(http.DefaultTransport)
}

// # Observability carve-out
//
// --offline means "do not talk to the network on the user's behalf". It
// does not silence diagnostics: telemetry, and any future remote-logging
// or crash-reporting sink, are logging-class egress and stay exempt, the
// same way a remote syslog target would not be muted by an offline flag.
// Consent and the telemetry mode already govern whether those emit at
// all; --offline is not a second consent gate.
//
// Sinks opt out by building their client with ObservabilityTransport
// rather than relying on http.DefaultTransport, which Install guards.
// The exemption is therefore explicit at the call site and survives a
// sink changing which HTTP library it uses.

// ObservabilityTransport returns a transport that ignores the offline
// marker, for logging-class sinks (telemetry, remote logging, crash
// reporting). base may be nil, in which case the unguarded default is
// used.
//
// If base is already guarded, the guard is stripped: the caller is
// asserting this client carries diagnostics, not user-initiated traffic.
// Never use it for anything the user asked for — that is exactly the
// traffic --offline exists to stop.
func ObservabilityTransport(base http.RoundTripper) http.RoundTripper {
	if g, ok := base.(*guard); ok {
		return g.base
	}
	if base != nil {
		return base
	}
	if g, ok := http.DefaultTransport.(*guard); ok {
		return g.base
	}
	return http.DefaultTransport
}
