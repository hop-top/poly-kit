package api

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// CodeHostRejected is the stable refusal code [HostCheck] writes.
const CodeHostRejected = "host_rejected"

// LoopbackHosts are the hosts a loopback listener answers to: the
// names a local client uses to reach it, none of which an attacker
// can make resolve elsewhere ("localhost" by RFC 6761 §6.3).
var LoopbackHosts = []string{"localhost", "127.0.0.1", "::1"}

// RefusalWriter writes a refusal decided on the HTTP plane. The
// default, used when a config leaves it nil, writes e with [Error];
// a transport whose protocol puts errors elsewhere (a JSON-RPC body,
// a Connect error) supplies its own.
type RefusalWriter func(w http.ResponseWriter, r *http.Request, e *APIError)

func (rw RefusalWriter) write(w http.ResponseWriter, r *http.Request, e *APIError) {
	if rw == nil {
		Error(w, e.Status, e)
		return
	}
	rw(w, r, e)
}

// HostCheckConfig configures [HostCheck].
type HostCheckConfig struct {
	// Allow lists the hosts accepted in the Host header: a name, an
	// IP literal, or either with a port ("api.example.com",
	// "10.0.0.5", "tool.internal:8443", "[::1]:8080"). An entry with
	// no port matches any port. Matching ignores case and a trailing
	// dot. The single entry "*" accepts every host.
	//
	// An empty Allow accepts no host: derive one from the listen
	// address with [ListenerHosts].
	Allow []string
	// Refuse writes the refusal; nil writes an [APIError].
	Refuse RefusalWriter
}

// HostCheck returns a middleware that refuses a request whose Host
// header names a host outside cfg.Allow, with 403 and the code
// [CodeHostRejected].
//
// It is the defense against DNS rebinding: a page on attacker.example
// that re-resolves its own name to 127.0.0.1 reaches a loopback
// server over a well-formed connection, and only the Host header
// still says attacker.example.
//
// The port is not compared unless an entry names one: rebinding
// changes the name, never the port, and pinning it would refuse an
// ssh port forward or a container port mapping for no protection
// gained. A request with no Host at all (HTTP/1.0) passes; no browser
// sends one.
//
// 403 rather than 421 Misdirected Request: HTTP/2 clients retry a 421
// on a fresh connection, which only doubles the refused traffic.
func HostCheck(cfg HostCheckConfig) Middleware {
	allow := newHostAllowlist(cfg.Allow)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !allow.permits(r.Host) {
				cfg.Refuse.write(w, r, &APIError{
					Status:  http.StatusForbidden,
					Code:    CodeHostRejected,
					Message: fmt.Sprintf("host %q is not served here", r.Host),
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ListenerHosts derives the Host allowlist a listen address implies.
//
// A loopback address ("127.0.0.1:8080", "[::1]:8080",
// "localhost:8080") answers to [LoopbackHosts] and its own host; any
// other explicit host, a name or an IP, answers to itself. A wildcard
// address (":8080", "0.0.0.0:8080", "[::]:8080") answers on every
// interface under names the process cannot know, so it derives no
// restriction: wildcard is true and hosts is nil.
func ListenerHosts(addr string) (hosts []string, wildcard bool) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	name, _ := splitHost(host)
	if name == "" {
		return nil, true
	}
	ip := net.ParseIP(stripZone(name))
	if ip != nil && ip.IsUnspecified() {
		return nil, true
	}
	if name == "localhost" || (ip != nil && ip.IsLoopback()) {
		return appendUnique(append([]string(nil), LoopbackHosts...), name), false
	}
	return []string{name}, false
}

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}

// hostAllowlist is the parsed form of HostCheckConfig.Allow.
type hostAllowlist struct {
	any      bool
	names    map[string]struct{} // any port
	hostPort map[string]struct{} // "name:port"
}

func newHostAllowlist(entries []string) hostAllowlist {
	a := hostAllowlist{
		names:    map[string]struct{}{},
		hostPort: map[string]struct{}{},
	}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "*" {
			a.any = true
			continue
		}
		name, port := splitHost(e)
		if name == "" {
			continue
		}
		if port == "" {
			a.names[name] = struct{}{}
		} else {
			a.hostPort[name+":"+port] = struct{}{}
		}
	}
	return a
}

func (a hostAllowlist) permits(host string) bool {
	if a.any || host == "" {
		return true
	}
	name, port := splitHost(host)
	if _, ok := a.names[name]; ok {
		return true
	}
	_, ok := a.hostPort[name+":"+port]
	return ok
}

// splitHost separates a host or host:port into a normalized name
// (lowercase, no brackets, no trailing dot, IP literals in canonical
// form) and its port, which is empty when absent.
func splitHost(host string) (name, port string) {
	name = host
	if h, p, err := net.SplitHostPort(host); err == nil {
		name, port = h, p
	}
	name = strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if ip := net.ParseIP(name); ip != nil {
		name = ip.String()
	}
	return name, port
}

// stripZone drops an IPv6 zone ("fe80::1%en0"), which net.ParseIP
// does not accept.
func stripZone(host string) string {
	if i := strings.IndexByte(host, '%'); i >= 0 {
		return host[:i]
	}
	return host
}
