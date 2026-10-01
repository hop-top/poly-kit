package etcd

import (
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
)

// Option configures how New and NewContext connect.
type Option func(*options)

type options struct {
	username string
	password string
	tls      *tls.Config
}

// WithAuth authenticates to etcd as username with password: the etcd
// client's own Username and Password, the only credentials it sends. Both
// must be non-empty. The client authenticates before the open returns, so
// a bad password or an unreachable cluster fails the open (bounded by the
// context given to NewContext). Neither value ever appears in an error.
func WithAuth(username, password string) Option {
	return func(o *options) {
		o.username, o.password = username, password
	}
}

// WithTLS secures the transport with cfg. It applies to https:// and
// unixs:// endpoints, and to host:port and unix:// ones; combining it with
// an http:// endpoint is rejected, since the client would drop it there
// silently. Without it, https:// and unixs:// use the system roots.
func WithTLS(cfg *tls.Config) Option {
	return func(o *options) {
		o.tls = cfg
	}
}

// validate rejects a lone username or password: the client authenticates
// only when both are set and silently ignores either alone.
func (o options) validate() error {
	if (o.username == "") != (o.password == "") {
		return errors.New("etcd kv: Username and Password must be set together; the etcd client ignores either alone")
	}
	return nil
}

// checkTransport rejects an endpoint list whose transport security the
// client would not honor as written.
//
// The client secures the whole connection as its FIRST endpoint asks
// (clientv3 dialWithBalancer): http:// is plaintext even when TLS is set,
// https:// and unixs:// are TLS (the system roots when none is set), and
// host:port and unix:// follow whether TLS is set. A list whose endpoints
// disagree would send traffic — credentials included — in plaintext to an
// https:// endpoint, or TLS to one meant for plaintext. The error names
// positions and schemes, never endpoints.
func checkTransport(endpoints []string, tlsSet bool) error {
	first := -1
	var firstTLS bool
	for i, ep := range endpoints {
		secure, explicit := endpointTLS(ep, tlsSet)
		if tlsSet && explicit && !secure {
			return fmt.Errorf("etcd kv: endpoint %d uses http:// but TLS is set; use https://", i+1)
		}
		if first < 0 {
			first, firstTLS = i, secure
			continue
		}
		if secure != firstTLS {
			return fmt.Errorf("etcd kv: endpoints %d and %d disagree on TLS; "+
				"the client secures every endpoint as the first one asks", first+1, i+1)
		}
	}
	return nil
}

// endpointTLS reports whether the client would secure ep with TLS, and
// whether its scheme decides that on its own (explicit) rather than
// following whether TLS is set. It mirrors clientv3 internal/endpoint.
func endpointTLS(ep string, tlsSet bool) (secure, explicit bool) {
	switch {
	case strings.HasPrefix(ep, "unixs:"):
		return true, true
	case strings.HasPrefix(ep, "unix:"):
		return tlsSet, false
	}
	scheme, _, ok := strings.Cut(ep, "://")
	switch {
	case ok && strings.EqualFold(scheme, "https"):
		return true, true
	case ok && strings.EqualFold(scheme, "http"):
		return false, true
	}
	return tlsSet, false
}
