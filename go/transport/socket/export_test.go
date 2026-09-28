package socket

import "net"

// NewPeerAuthenticatorWithCreds is [NewPeerAuthenticator] reading
// credentials through creds, so a test can present a peer of another
// uid without switching users.
func NewPeerAuthenticatorWithCreds(cfg PeerAuthConfig, creds func(net.Conn) (PeerCred, error)) (Authenticator, error) {
	return newPeerAuthenticator(cfg, creds)
}
