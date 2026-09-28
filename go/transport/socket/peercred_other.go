//go:build !linux && !darwin && !freebsd

package socket

const peerCredSupported = false

func readPeerCred(uintptr) (PeerCred, error) { return PeerCred{}, ErrPeerCredUnsupported }
