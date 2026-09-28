package socket

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// ErrPeerCredUnsupported is returned by [NewPeerAuthenticator] and
// [PeerCredentials] on a platform whose kernel kit cannot ask for a
// Unix socket peer's credentials. Linux, macOS and FreeBSD are
// supported; Windows and the other BSDs are not.
var ErrPeerCredUnsupported = errors.New("socket: peer credentials are not supported")

// Meta.Extra keys the peer authenticator records on every request it
// decides, admitted or refused, so the audit trail carries the kernel's
// account of the caller beside the principal.
const (
	ExtraPeerUID = "peer_uid"
	ExtraPeerGID = "peer_gid"
	ExtraPeerPID = "peer_pid"
)

// PeerCred is what the kernel reports about the process on the other
// end of a Unix socket connection, as of when it connected.
type PeerCred struct {
	// UID is the peer's effective user id.
	UID uint32
	// GID is the peer's effective group id.
	GID uint32
	// PID is the peer's process id, 0 where the platform does not
	// report it (FreeBSD). A process id is recorded for the audit
	// trail only: it can be reused once the process exits.
	PID int
}

// extra is the Meta.Extra entries recording c.
func (c PeerCred) extra() map[string]string {
	m := map[string]string{
		ExtraPeerUID: strconv.FormatUint(uint64(c.UID), 10),
		ExtraPeerGID: strconv.FormatUint(uint64(c.GID), 10),
	}
	if c.PID > 0 {
		m[ExtraPeerPID] = strconv.Itoa(c.PID)
	}
	return m
}

// PeerCredentials asks the kernel who is on the other end of conn,
// which must be a Unix domain socket connection: SO_PEERCRED on
// Linux, LOCAL_PEERCRED (and LOCAL_PEERPID on macOS) on macOS and
// FreeBSD. The answer describes the peer as of connect, and a client
// cannot forge it.
func PeerCredentials(conn net.Conn) (PeerCred, error) {
	if !peerCredSupported {
		return PeerCred{}, fmt.Errorf("%w on %s", ErrPeerCredUnsupported, runtime.GOOS)
	}
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return PeerCred{}, fmt.Errorf("socket: peer credentials: %T is not an operating-system connection", conn)
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return PeerCred{}, fmt.Errorf("socket: peer credentials: %w", err)
	}
	var cred PeerCred
	var credErr error
	if err := raw.Control(func(fd uintptr) { cred, credErr = readPeerCred(fd) }); err != nil {
		return PeerCred{}, fmt.Errorf("socket: peer credentials: %w", err)
	}
	if credErr != nil {
		return PeerCred{}, fmt.Errorf("socket: peer credentials: %w", credErr)
	}
	return cred, nil
}

// PeerAuthConfig configures [NewPeerAuthenticator].
type PeerAuthConfig struct {
	// RequireSameUID refuses a peer whose uid is not the server
	// process's. The owner-only socket file already confines callers
	// to that uid and root; this turns root away too, and any caller
	// a deliberately widened socket would otherwise let in.
	RequireSameUID bool
	// ResolveNames makes the principal the peer's user name rather
	// than uid:<n>. A uid with no user entry keeps the uid:<n> form,
	// which no user name can take, since a name never contains ':'.
	ResolveNames bool
	// Scopes returns the scopes an admitted peer holds, from its
	// credentials. They become [Identity.Scopes], which reach the
	// permission gate as a verified credential's scopes, so a leaf
	// declaring kit/permissions runs for a peer holding every scope it
	// names. It is not asked about a refused peer. Nil grants none:
	// such a leaf is refused as insufficient scope.
	Scopes func(PeerCred) []string
}

// NewPeerAuthenticator returns an [Authenticator] that identifies each
// request by its connection's peer credentials ([PeerCredentials]):
// the principal is uid:<n>, or the user name with
// [PeerAuthConfig.ResolveNames], the tenant is empty, and the uid, gid
// and pid are recorded in Meta.Extra ([ExtraPeerUID], [ExtraPeerGID],
// [ExtraPeerPID]). The transport marks the identity verified: the
// kernel, not the caller, said who it is.
//
// A peer the kernel cannot describe, or one [PeerAuthConfig.RequireSameUID]
// turns away, is refused as UNAUTHENTICATED, and the refusal is
// audited with the peer's credentials when they were read.
//
// On a platform without peer credentials it returns
// [ErrPeerCredUnsupported], so a tool refuses the configuration at
// start rather than serving with an authenticator that refuses
// everything.
func NewPeerAuthenticator(cfg PeerAuthConfig) (Authenticator, error) {
	return newPeerAuthenticator(cfg, PeerCredentials)
}

func newPeerAuthenticator(cfg PeerAuthConfig, creds func(net.Conn) (PeerCred, error)) (Authenticator, error) {
	if !peerCredSupported {
		return nil, fmt.Errorf("%w on %s", ErrPeerCredUnsupported, runtime.GOOS)
	}
	serverUID := os.Getuid()
	return func(_ context.Context, conn net.Conn, _ Request) (Identity, error) {
		cred, err := creds(conn)
		if err != nil {
			return Identity{}, err
		}
		extra := cred.extra()
		if cfg.RequireSameUID && int64(cred.UID) != int64(serverUID) {
			return Identity{Extra: extra}, fmt.Errorf(
				"peer uid %d is not the server's uid %d", cred.UID, serverUID)
		}
		uid := strconv.FormatUint(uint64(cred.UID), 10)
		principal := "uid:" + uid
		if cfg.ResolveNames {
			if u, err := user.LookupId(uid); err == nil && u.Username != "" {
				principal = u.Username
			}
		}
		var scopes []string
		if cfg.Scopes != nil {
			scopes = cleanScopes(cfg.Scopes(cred))
		}
		return Identity{Principal: principal, Scopes: scopes, Extra: extra}, nil
	}, nil
}

// cleanScopes returns scopes trimmed, without empty entries and
// duplicates, in first-seen order, in a slice of its own.
func cleanScopes(scopes []string) []string {
	var out []string
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
