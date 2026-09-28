//go:build darwin

package socket

import "golang.org/x/sys/unix"

const peerCredSupported = true

// readPeerCred reads LOCAL_PEERCRED, whose first group is the peer's
// effective gid, and LOCAL_PEERPID.
func readPeerCred(fd uintptr) (PeerCred, error) {
	x, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return PeerCred{}, err
	}
	cred := PeerCred{UID: x.Uid}
	if x.Ngroups > 0 {
		cred.GID = x.Groups[0]
	}
	// The pid is audit detail; a kernel that will not report it
	// leaves the peer identified by its uid all the same.
	if pid, err := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID); err == nil {
		cred.PID = pid
	}
	return cred, nil
}
