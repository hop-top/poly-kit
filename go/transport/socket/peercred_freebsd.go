//go:build freebsd

package socket

import "golang.org/x/sys/unix"

const peerCredSupported = true

// readPeerCred reads LOCAL_PEERCRED, whose first group is the peer's
// effective gid. FreeBSD reports no pid through it.
func readPeerCred(fd uintptr) (PeerCred, error) {
	x, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return PeerCred{}, err
	}
	cred := PeerCred{UID: x.Uid}
	if x.Ngroups > 0 {
		cred.GID = x.Groups[0]
	}
	return cred, nil
}
