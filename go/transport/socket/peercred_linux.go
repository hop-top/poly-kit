//go:build linux

package socket

import "golang.org/x/sys/unix"

const peerCredSupported = true

// readPeerCred reads SO_PEERCRED: the peer's pid, effective uid and
// effective gid as of connect.
func readPeerCred(fd uintptr) (PeerCred, error) {
	u, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return PeerCred{}, err
	}
	return PeerCred{UID: u.Uid, GID: u.Gid, PID: int(u.Pid)}, nil
}
