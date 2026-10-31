# identity

## What it answers

Who is this process, and can it prove it and protect data at rest? A local Ed25519 keypair, EdDSA JWTs
signed by it, and a symmetric key derived from it for NaCl secretbox. Port of Go `go/core/identity`,
held to `contracts/identity-v1`.

## Use it when

- a CLI needs a stable local identity → `default_store()?.load_or_generate()?`
- you hand a service a bearer token → `kp.sign_jwt(Claims { .. })`; the receiver calls
  `verify_jwt` / `verify_jwt_with_options` with your public key
- you encrypt data at rest under that identity → `kp.derive_key()`, then `encrypt` / `decrypt`
- you run on wasm32 → seed, nonce and clock come from you: `Keypair::from_seed`,
  `encrypt_with_nonce`, `verify_jwt_at`

## Quick start

```rust
use hop_top_kit::identity::{decrypt, default_store, encrypt, verify_jwt, Claims};

let kp = default_store()?.load_or_generate()?;
let token = kp.sign_jwt(Claims { subject: kp.public_key_id(), issued_at: now, ..Claims::default() });
let claims = verify_jwt(&token, &kp.public_key())?;

let key = kp.derive_key();
let sealed = encrypt(&key, b"secret")?;
assert_eq!(decrypt(&key, &sealed)?, b"secret");
```

## Contract

- Feature `identity`; `default = []` unchanged. Core is wasm32-safe. Off wasm32-unknown-unknown:
  `Keypair::generate`, `encrypt`, `verify_jwt`, `verify_jwt_with_options` (OS RNG, system clock).
  Off wasm32 entirely: `Store`, `default_store` (`<data home>/kit/identity`, `id_ed25519` 0600,
  `id_ed25519.pub` 0644, directory 0700, atomic write).
- Keys: Ed25519; PKCS#8 v1 and SPKI PEM, byte-identical to Go's x509 output. `public_key_id` is the
  first 16 hex of SHA-256 over the public key; it is the JWT `kid`.
- JWT: header `{"alg":"EdDSA","kid":..,"typ":"JWT"}`, base64url without padding. Verify order:
  3 parts, signature, payload, `exp` (expired only when `exp > 0 && now > exp`). `alg`, `typ`,
  `kid`, `iat` are not checked. Issuer/audience are checked only through `VerifyOptions`; an empty
  option is not checked. `aud` is a string or an array; one element serialises as a string.
- Encryption: `derive_key` = HKDF-SHA256(ikm = 32-byte seed, no salt, info
  `kit-identity-encryption-v1`) → 32 bytes. Frame = `nonce(24) || tag(16) || body`
  (XSalsa20-Poly1305). Under 40 bytes is `CiphertextTooShort`.
- Errors carry Go's `identity: ` wording (`IdentityError`).

## Differences from Go

- Public keys are `[u8; 32]`, so Go's wrong-length-key panic cannot occur; an off-curve key is an
  invalid signature.
- PEM parse takes the first `-----BEGIN` block and requires the standard label; Go ignores the label.
- Claims JSON strings escape like `serde_json`; Go also escapes `<`, `>`, `&`, U+2028/9. Both parse
  everywhere. Duplicate JSON keys differing only in case resolve exact-case first, but not in
  document order.
- The clock is injectable (`_at` functions) and `Keypair::from_seed` / `encrypt_with_nonce` take
  caller randomness; Go reads `time.Now` and `crypto/rand`.
- Not ported: the `cli.WithIdentity` option (Go CLI wiring); `sqlstore::EncryptedStore` keeps its
  own copy of the same derivation, asserted equal in tests.

## Dependencies

`ed25519-dalek` (exact pin; `pem`, `zeroize`), `crypto_secretbox` (no `getrandom`), `hkdf`, `sha2`,
`base64`, `serde`, `serde_json`, `thiserror`; `getrandom` on native targets only.

## Neighbours

- `contracts/identity-v1/`: the key-derivation vectors
- `../sqlstore/`: encrypted store over the same derived key
- `go/core/identity/`: Go implementation
