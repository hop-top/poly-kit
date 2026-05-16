// Package identity provides local-first cryptographic identity for CLI tools.
//
// Core primitives:
//   - Ed25519 keypair generation and PEM serialization
//   - Filesystem-backed Store with XDG-compliant default path
//   - EdDSA JWT signing and verification
//   - NaCl secretbox encryption with key derivation from keypair
//
// Typical usage:
//
//	store, _ := identity.DefaultStore()
//	kp, _ := store.LoadOrGenerate()
//
//	// Sign a JWT for service auth.
//	token, _ := kp.SignJWT(identity.Claims{Subject: kp.PublicKeyID()})
//
//	// Encrypt data at rest.
//	key := kp.DeriveKey()
//	cipher, _ := identity.Encrypt(key, plaintext)
//
// CLI integration via kit/cli:
//
//	root := cli.New(cfg, cli.WithIdentity(cli.IdentityConfig{}))
//	// root.Identity is ready to use.
//
// Encrypted storage via kit/sqlstore:
//
//	enc := sqlstore.NewEncryptedStore(store, kp)
//	enc.Put(ctx, "secret", value)
package identity
