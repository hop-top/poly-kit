// Package peer provides decentralized peer discovery and trust mesh.
//
// # Discovery
//
// [PeerInfo] describes a peer (ID, Name, Addrs, PublicKey, Metadata).
// [Discoverer] is the pluggable interface for finding peers:
//
//   - [MDNSDiscoverer]: LAN discovery via mDNS/DNS-SD
//   - StaticDiscoverer: fixed peer list (testing/static configs)
//
// # Registry
//
// [Registry] persists [PeerRecord] entries in a sqlstore. Each record
// extends PeerInfo with [TrustLevel], FirstSeen, and LastSeen timestamps.
//
// # Trust
//
// [TrustManager] implements Trust-On-First-Use (TOFU) with explicit
// promotion. Trust levels:
//
//   - Unknown: never seen
//   - PendingTOFU: first encounter; key recorded but not yet trusted
//   - Trusted: explicitly promoted via [TrustManager.Trust]
//   - Blocked: explicitly blocked; mesh will not connect
//
// Challenge-response verification uses Ed25519 signatures to confirm
// that a peer holds the private key matching their announced public key.
//
// # Mesh
//
// [Mesh] ties discovery and trust into an auto-connect loop:
//
//	disc := peer.NewMDNSDiscoverer("_myapp._tcp", 5353)
//	reg := peer.NewRegistry(store)
//	tm := peer.NewTrustManager(reg, keypair)
//	m := peer.NewMesh(self, tm, disc)
//	go m.Start(ctx)
//
// Flow: discover → AcceptTOFU → PendingTOFU → Trust() → mesh connects.
// OnConnect/OnDisconnect callbacks fire as peers join/leave.
package peer
