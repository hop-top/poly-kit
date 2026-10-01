package etcd

import (
	"strings"
	"testing"
)

// dialTarget must reduce an endpoint to exactly the authority the client
// dials: clientv3 takes url.Host for http(s) endpoints, and url.Host never
// carries userinfo. Leaving it in both leaks credentials into the refusal
// and hides a loopback host behind a "user@" prefix.
func TestDialTarget(t *testing.T) {
	cases := []struct {
		ep, network, addr string
	}{
		// userinfo, with and without password
		{"http://u:p@127.0.0.1:2379", "tcp", "127.0.0.1:2379"},
		{"https://u:p@etcd.example:2379", "tcp", "etcd.example:2379"},
		{"http://u@127.0.0.1:2379", "tcp", "127.0.0.1:2379"},
		{"http://u:@localhost:2379", "tcp", "localhost:2379"},
		{"http://u:p@[::1]:2379", "tcp", "[::1]:2379"},
		// An unescaped "@" in the password is malformed, but must still
		// never leak: the authority splits on its LAST "@", as net/url does.
		{"http://u:p@ss@127.0.0.1:2379", "tcp", "127.0.0.1:2379"},
		{"http://u:p@127.0.0.1:2379/v3/kv?x=1#frag", "tcp", "127.0.0.1:2379"},
		// An "@" past the authority is path, not userinfo.
		{"http://etcd.example:2379/a@127.0.0.1:1", "tcp", "etcd.example:2379"},
		{"http://etcd.example:2379?q=a@127.0.0.1:1", "tcp", "etcd.example:2379"},
		{"http://etcd.example:2379#a@127.0.0.1:1", "tcp", "etcd.example:2379"},

		// no userinfo: unchanged behavior
		{"http://127.0.0.1:2379", "tcp", "127.0.0.1:2379"},
		{"https://[::1]:2379", "tcp", "[::1]:2379"},
		{"http://etcd.example", "tcp", "etcd.example"},
		{"http://127.0.0.1:2379/path", "tcp", "127.0.0.1:2379"},
		{"http://127.0.0.1:2379?q=1", "tcp", "127.0.0.1:2379"},
		{"http://127.0.0.1:2379#frag", "tcp", "127.0.0.1:2379"},
		{"127.0.0.1:2379", "tcp", "127.0.0.1:2379"},
		{"[::1]:2379", "tcp", "[::1]:2379"},
		{"localhost:2379", "tcp", "localhost:2379"},
		{"etcd.example", "tcp", "etcd.example"},
		{"unix:///tmp/etcd.sock", "unix", "/tmp/etcd.sock"},
		{"unixs://localhost:2379", "unix", "localhost:2379"},
		{"unix:etcd.sock", "unix", "etcd.sock"},

		// The client parses http(s) endpoints with net/url, which folds
		// the scheme to lower case: "HTTP://" is dialed like "http://".
		{"HTTP://127.0.0.1:2379", "tcp", "127.0.0.1:2379"},
		{"Https://u:p@etcd.example:2379/v3", "tcp", "etcd.example:2379"},
	}
	for _, tc := range cases {
		t.Run(tc.ep, func(t *testing.T) {
			network, addr := dialTarget(tc.ep)
			if network != tc.network || addr != tc.addr {
				t.Fatalf("dialTarget(%q) = (%q, %q), want (%q, %q)",
					tc.ep, network, addr, tc.network, tc.addr)
			}
		})
	}
}

// checkEndpoint admits exactly the forms the client interprets
// (clientv3 internal/endpoint): bare host:port, http(s)://, and the
// unix(s) socket forms. Any other scheme is passed to the dialer verbatim
// as a TCP address, which can never connect, so it is rejected at open —
// and the error never echoes the endpoint past what is safe to print.
func TestCheckEndpoint(t *testing.T) {
	accepted := []string{
		"127.0.0.1:2379",
		"[::1]:2379",
		"localhost:2379",
		"etcd.example",
		"http://127.0.0.1:2379",
		"https://etcd.example:2379/v3?x=1#f",
		"HTTP://127.0.0.1:2379",
		"HTTPS://etcd.example:2379",
		// An "@" past the authority is path, query or fragment.
		"http://etcd.example:2379/a@b",
		"https://etcd.example:2379?q=a@b",
		"http://etcd.example:2379#a@b",
		// A socket path is a file or abstract name, not an authority:
		// an "@" in it is not userinfo, and unix dials are never refused.
		"unix:///tmp/etcd.sock",
		"unixs://localhost:2379",
		"unix:etcd.sock",
		"unix:@etcd-abstract",
		"unix:///tmp/a@b.sock",
	}
	for _, ep := range accepted {
		t.Run("accept/"+ep, func(t *testing.T) {
			if err := checkEndpoint(ep); err != nil {
				t.Fatalf("checkEndpoint(%q) = %v, want nil", ep, err)
			}
		})
	}

	rejected := []struct {
		ep   string
		want string // a substring the error must carry
	}{
		{"kit-user:kit-secret@etcd.example:2379", "etcd.example:2379"},
		{"kit-user@127.0.0.1:2379", "127.0.0.1:2379"},
		{"grpc://etcd.example:2379", `"grpc"`},
		{"grpc://kit-user:kit-secret@etcd.example:2379", `"grpc"`},
		{"dns:///etcd.example:2379", `"dns"`},
		{"tcp://kit-user@127.0.0.1:2379", `"tcp"`},
		{"UNIX://kit-user:kit-secret@sock", `"UNIX"`},
		// Not a valid URL scheme at all: nothing before "://" is echoed.
		{"kit-user:kit-secret@etcd.example://x", "scheme"},
		{"kit user://kit-secret", "scheme"},
	}
	for _, tc := range rejected {
		t.Run("reject/"+tc.ep, func(t *testing.T) {
			err := checkEndpoint(tc.ep)
			if err == nil {
				t.Fatalf("checkEndpoint(%q) = nil, want an error", tc.ep)
			}
			msg := err.Error()
			for _, secret := range []string{"kit-user", "kit-secret", "@"} {
				if strings.Contains(msg, secret) {
					t.Fatalf("error leaks %q: %s", secret, msg)
				}
			}
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("error %q does not carry %q", msg, tc.want)
			}
		})
	}
}
