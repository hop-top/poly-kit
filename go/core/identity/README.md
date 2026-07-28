# identity

key management; JWT handling; encryption primitives.

## Key derivation is a cross-language contract

`Keypair.DeriveKey` is deterministic, so it binds every port: a store
encrypted by one implementation must be readable by another. The vectors
in [`contracts/identity-v1/derive-key.json`](../../../contracts/identity-v1/derive-key.json)
are the contract of record, and `derivekey_contract_test.go` asserts this
package against them.

The derivation is HKDF-SHA256 with an empty salt and the info string
`kit-identity-encryption-v1`, producing 32 bytes. The input keying material
is the **Ed25519 private key seed** — the 32-byte seed alone, never the
expanded private key. A port that feeds the expanded key instead derives a
different key and silently fails to interoperate.

Each vector carries the public key alongside the seed and derived key, so a
port can confirm it constructs the keypair correctly before blaming the
derivation.

Encryption itself is NaCl secretbox with a random nonce and so is not
deterministic; it is covered by round-trip tests rather than fixed vectors.
