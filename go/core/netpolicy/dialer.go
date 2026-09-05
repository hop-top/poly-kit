package netpolicy

import (
	"context"
	"fmt"
	"net"
)

// DialFunc is the dial signature shared by net.Dialer.DialContext and by
// every library hook that accepts an injected dialer (websocket
// DialOptions, database drivers, gRPC).
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// isLocalNetwork reports whether network names a transport that cannot
// leave the machine. unix/unixgram/unixpacket sockets are filesystem
// objects: --offline means "do not talk to the network", and a unix
// socket is not the network.
func isLocalNetwork(network string) bool {
	switch network {
	case "unix", "unixgram", "unixpacket":
		return true
	}
	return false
}

// CheckDial reports whether a dial to addr on network is permitted under
// ctx. It returns nil when the dial may proceed and an error wrapping
// ErrOffline when the policy refuses it.
//
// It applies the same exemptions as the HTTP guard: the policy only bites
// on an offline-marked context, loopback stays reachable, and unix
// sockets are not network at all. Use it when a library gives you a hook
// that is not a dialer — a connection callback, a driver Connector — and
// you need the decision without a net.Conn to return.
func CheckDial(ctx context.Context, network, addr string) error {
	if !IsOffline(ctx) || isLocalNetwork(network) || isLoopback(addr) {
		return nil
	}
	return fmt.Errorf("dial %s %s: %w", network, addr, ErrOffline)
}

// GuardDial wraps base so a dial on an offline-marked context fails with
// ErrOffline instead of opening a socket. A nil base dials with a zero
// net.Dialer.
//
// This is the seam for every egress path net/http does not own: SMTP,
// WebSocket, database drivers, gRPC and raw TLS all accept a dial
// function, and routing that function through GuardDial puts them under
// the same policy as HTTP. Loopback and unix sockets stay reachable, so
// local workflows are unaffected.
//
// Unlike Install, this cannot be applied process-wide: Go has no hook
// beneath net.Dial itself (see the package doc, "Scope"). A client is
// covered only once its dialer is routed through here.
func GuardDial(base DialFunc) DialFunc {
	if base == nil {
		base = (&net.Dialer{}).DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if err := CheckDial(ctx, network, addr); err != nil {
			return nil, err
		}
		return base(ctx, network, addr)
	}
}
