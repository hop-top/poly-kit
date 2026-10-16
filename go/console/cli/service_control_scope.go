package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"

	"github.com/gofrs/flock"
	"github.com/spf13/cobra"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/core/xdg"
)

// Control scopes.
//
// One tool can run more than one instance per user: one per profile,
// per workspace, per tenant. Each needs a control socket of its own,
// or `service stop` from one profile drains another profile's
// instance. The tool names the instance a `service` command addresses
// with [WithServiceControlScope]; kit turns the name into the socket
// path, the same way for `service start` (which listens on it) and
// `service stop`, `service restart` and `service status` (which dial
// it).

// ServiceControlScope resolves the scope of the instance a `service`
// command addresses. Kit calls it from the command itself — after
// flag parsing and every PersistentPreRunE, so the tool's own
// configuration and profile resolution have run — with that command.
// It returns "" for the default, unscoped instance.
//
// A scope is one path segment of letters, digits, '.', '_' and '-'.
// Anything else is refused as a usage error rather than rewritten, so
// two spellings can never meet on one socket.
type ServiceControlScope func(cmd *cobra.Command) (string, error)

// WithServiceControlScope gives the tool's instances control sockets
// of their own. With it, the control socket of scope s is
// <runtime dir>/<tool>/control-<s>.sock; the empty scope keeps the
// default, <runtime dir>/<tool>/control.sock.
//
// The scope must come from what the restarted instance sees again —
// its arguments, environment or configuration — because `service
// restart` re-executes the instance with exactly those, and the new
// run listens where the old one did only if it resolves the same
// scope.
func WithServiceControlScope(fn ServiceControlScope) func(*Root) {
	return func(r *Root) { stateOf(r).controlScope = fn }
}

// controlEndpoint is where one invocation finds its instance's
// control socket.
type controlEndpoint struct {
	path  string
	scope string
}

// controlEndpoint resolves the control socket for cmd's invocation:
// the tool's scope, then the path.
func (r *Root) controlEndpoint(cmd *cobra.Command) (controlEndpoint, error) {
	var scope string
	if st, ok := lookup(r); ok && st.controlScope != nil {
		s, err := st.controlScope(cmd)
		if err != nil {
			return controlEndpoint{}, err
		}
		if err := validateControlScope(s); err != nil {
			return controlEndpoint{}, err
		}
		scope = s
	}
	path, err := controlSocketPath(r, scope)
	if err != nil {
		return controlEndpoint{}, err
	}
	if len(path) > maxSocketPathLen {
		e := output.UsageError(fmt.Sprintf(
			"control socket path %q is %d bytes, over the %d-byte limit for a unix socket path",
			path, len(path), maxSocketPathLen))
		e.SuggestedFix = "use a shorter control scope, or point XDG_RUNTIME_DIR at a shorter directory"
		return controlEndpoint{}, e
	}
	return controlEndpoint{path: path, scope: scope}, nil
}

// validateControlScope refuses a scope that is not one plain path
// segment.
func validateControlScope(s string) error {
	if s == "" {
		return nil
	}
	ok := s != "." && s != ".."
	for _, c := range s {
		if !ok {
			break
		}
		ok = c == '.' || c == '_' || c == '-' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	}
	if ok {
		return nil
	}
	e := output.UsageError(fmt.Sprintf(
		"control scope %q: use letters, digits, '.', '_' and '-' only", s))
	e.SuggestedFix = "map the instance's name onto that set before returning it as the scope"
	return e
}

// controlSocketPath is the control socket of scope under the tool's
// runtime directory.
func controlSocketPath(r *Root, scope string) (string, error) {
	tool := "kit"
	if r != nil && r.Config.Name != "" {
		tool = r.Config.Name
	}
	name := controlSocketName
	if scope != "" {
		name = "control-" + scope + ".sock"
	}
	return xdg.RuntimeFile(tool, name)
}

// claimControlPath makes path this instance's: it takes the lock
// beside it, which a live instance holds for as long as it runs, and
// clears a stale socket file an instance that did not exit cleanly
// left behind.
//
// Liveness is decided by the lock, not by the file: a socket file is
// removed only while the lock is held, only when it is a socket, and
// only when nothing answers on it. An instance of a release that took
// no lock still answers, so it is refused rather than unlinked.
func claimControlPath(path, stopHint string) (*flock.Flock, error) {
	inUse := func() error {
		e := output.ConflictError(fmt.Sprintf(
			"another instance is serving the control socket %s", path))
		e.SuggestedFix = "stop it with `" + stopHint + "`, or give this instance a control scope of its own"
		return e
	}
	// Owner-only, like the socket, in the owner-only runtime directory.
	lock := flock.New(path+".lock", flock.SetPermissions(0o600))
	held, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("control socket lock %s: %w", lock.Path(), err)
	}
	if !held {
		return nil, inUse()
	}
	release := func(err error) (*flock.Flock, error) {
		_ = lock.Unlock()
		return nil, err
	}
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return lock, nil
	case err != nil:
		return release(err)
	case info.Mode()&fs.ModeSocket == 0:
		e := output.ConflictError(fmt.Sprintf(
			"control socket path %s exists and is not a socket; not removing it", path))
		e.SuggestedFix = "move the file away, or give this instance a control scope of its own"
		return release(e)
	}
	conn, dialErr := net.DialTimeout("unix", path, controlDialTimeout)
	if dialErr == nil {
		_ = conn.Close()
		return release(inUse())
	}
	// A listener too busy to accept in time is still a listener.
	var ne net.Error
	if errors.As(dialErr, &ne) && ne.Timeout() {
		return release(inUse())
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return release(err)
	}
	return lock, nil
}

// notRunningMessage names the instance that is not running.
func notRunningMessage(r *Root, ep controlEndpoint) string {
	if ep.scope == "" {
		return r.Config.Name + " is not running"
	}
	return r.Config.Name + " is not running in control scope " + ep.scope
}
