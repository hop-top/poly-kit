package api

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UnixAddrPrefix marks a listen address as a unix domain socket path:
// "unix:/run/tool/api.sock" (the form "unix:///run/..." is accepted
// too). Anything else is a TCP host:port.
const UnixAddrPrefix = "unix:"

// UnixSocketMode is the mode of a unix listener's socket file: owner
// only, so access to the file is the access control.
const UnixSocketMode = 0o600

// IsUnixAddr reports whether addr names a unix socket.
func IsUnixAddr(addr string) bool { return strings.HasPrefix(addr, UnixAddrPrefix) }

// UnixPath is the socket path of a unix address: addr without its
// prefix and without a leading "//".
func UnixPath(addr string) string {
	return strings.TrimPrefix(strings.TrimPrefix(addr, UnixAddrPrefix), "//")
}

// ValidateListenAddr reports whether addr is a usable listen address:
// host:port, or a unix address with an absolute socket path. It binds
// nothing.
func ValidateListenAddr(addr string) error {
	if addr == "" {
		return errors.New("empty listen address")
	}
	if IsUnixAddr(addr) {
		p := UnixPath(addr)
		if p == "" || !filepath.IsAbs(p) {
			return fmt.Errorf("unix address %q needs an absolute socket path", addr)
		}
		return nil
	}
	_, _, err := net.SplitHostPort(addr)
	return err
}

// Listen binds addr: TCP for host:port, a unix socket for a
// [UnixAddrPrefix] address.
//
// A unix listener makes the socket file mode [UnixSocketMode] and
// removes it on close. A stale socket file, one no process answers
// on, is replaced; a live one is an error, since taking its path
// would steal a running server's traffic; a path that is not a socket
// is an error and is left alone. The parent directory is created
// (owner only) when missing.
func Listen(addr string) (net.Listener, error) {
	if !IsUnixAddr(addr) {
		return net.Listen("tcp", addr)
	}
	if err := ValidateListenAddr(addr); err != nil {
		return nil, err
	}
	path := UnixPath(addr)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}
	if err := clearStaleSocket(path); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	// Between Listen and Chmod the file carries the process umask;
	// narrowing before anything is served is the smallest window
	// available without changing the umask of a running process.
	if err := os.Chmod(path, UnixSocketMode); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}
	return ln, nil
}

func clearStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("listen: %s exists and is not a socket", path)
	}
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		_ = c.Close()
		return fmt.Errorf("listen: %s is already in use", path)
	}
	return os.Remove(path)
}
