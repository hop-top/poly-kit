package llm

import (
	"context"
	"net"
)

// Test seams for LocalOnly: the DNS lookup and the dial.
var (
	LocalWithLookup = func(f func(context.Context, string) ([]net.IP, error)) LocalOption { return withLookup(f) }
	LocalWithDial   = func(f func(context.Context, string, string) (net.Conn, error)) LocalOption { return withDial(f) }
)

// LocalOwnTransportNoProxy reports whether the transport kit builds
// for LocalOnly ignores proxy environment variables.
func LocalOwnTransportNoProxy() bool {
	t := (&localConfig{allowPrivate: true}).ownTransport()
	return t.Proxy == nil && t.DialContext != nil && t.DialTLSContext == nil
}
