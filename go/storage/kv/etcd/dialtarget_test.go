package etcd

import "testing"

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
