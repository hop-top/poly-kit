package api_test

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"hop.top/kit/go/transport/api"
)

// sockPath returns a short socket path: unix socket paths are capped
// near 100 bytes, which t.TempDir often exceeds.
func sockPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func TestIsUnixAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"unix:/tmp/a.sock":  true,
		"unix:///tmp/a.sk":  true,
		"127.0.0.1:8080":    false,
		":0":                false,
		"unixhost:80":       false,
		"localhost:unix:80": false,
	} {
		if got := api.IsUnixAddr(addr); got != want {
			t.Errorf("IsUnixAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestUnixPath(t *testing.T) {
	for addr, want := range map[string]string{
		"unix:/tmp/a.sock":   "/tmp/a.sock",
		"unix:///tmp/a.sock": "/tmp/a.sock",
	} {
		if got := api.UnixPath(addr); got != want {
			t.Errorf("UnixPath(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestValidateListenAddr(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:0", "localhost:8080", ":9", "unix:/tmp/a.sock"} {
		if err := api.ValidateListenAddr(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "nohostport", "unix:", "unix:relative.sock", "unix://"} {
		if err := api.ValidateListenAddr(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestListen_TCP(t *testing.T) {
	ln, err := api.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if ln.Addr().Network() != "tcp" {
		t.Fatalf("network %s", ln.Addr().Network())
	}
}

func TestListen_UnixModeAndCleanup(t *testing.T) {
	p := sockPath(t)
	ln, err := api.Listen("unix:" + p)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&os.ModeSocket == 0 {
		t.Fatalf("not a socket: %v", st.Mode())
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", st.Mode().Perm())
	}
	if ln.Addr().String() != p {
		t.Fatalf("addr %q, want %q", ln.Addr().String(), p)
	}
	_ = ln.Close()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("socket file must go on close: %v", err)
	}
}

func TestListen_UnixStaleReplaced(t *testing.T) {
	p := sockPath(t)
	// A socket file left by a process that died: bound, then abandoned
	// without unlinking.
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: p, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	_ = l.Close()
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("setup: %v", err)
	}
	ln, err := api.Listen("unix:" + p)
	if err != nil {
		t.Fatalf("stale socket must be replaced: %v", err)
	}
	_ = ln.Close()
}

func TestListen_UnixLiveRefused(t *testing.T) {
	p := sockPath(t)
	first, err := api.Listen("unix:" + p)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := api.Listen("unix:" + p); err == nil {
		_ = second.Close()
		t.Fatal("a live socket must not be taken over")
	}
	// The first is still serving.
	c, err := net.Dial("unix", p)
	if err != nil {
		t.Fatalf("first listener lost its socket: %v", err)
	}
	_ = c.Close()
}

func TestListen_UnixNonSocketRefused(t *testing.T) {
	p := sockPath(t)
	if err := os.WriteFile(p, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ln, err := api.Listen("unix:" + p); err == nil {
		_ = ln.Close()
		t.Fatal("a regular file must not be removed")
	}
	if b, _ := os.ReadFile(p); string(b) != "keep" {
		t.Fatal("file was touched")
	}
}

func TestListenerHosts_Unix(t *testing.T) {
	hosts, wildcard := api.ListenerHosts("unix:/tmp/a.sock")
	if wildcard {
		t.Fatal("a unix listener is not a wildcard bind")
	}
	want := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	for _, h := range hosts {
		delete(want, h)
	}
	if len(want) != 0 {
		t.Fatalf("hosts %v must include the loopback names", hosts)
	}
}
