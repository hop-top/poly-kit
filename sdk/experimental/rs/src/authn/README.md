# authn

## What it answers

Who is calling, from the credential a request carries: a signed bearer token (JWT) checked against trusted public keys, or a kit-issued opaque API key held hashed in a kv store. Wrong place for serving, routing or the 401 answer: that is the transport that calls an `AuthFunc`.

## Use it when

- a service must accept tokens from an issuer or from another kit tool → `Verifier::new(keys, Options { .. })`, then `verify(token)` or `auth_func()`
- the issuer publishes a JWKS → `Verifier::from_jwks(bytes, opts)`, or `keys_from_jwks` and rotate the set yourself
- a key file is on disk → `Key::from_public_key_pem`
- a tool issues its own long-lived credentials → `ApiKeys::new(store)`, `create`, `revoke`, `auth_func` (feature `authn-apikeys`)
- a deny list must be consulted → `Options::check`, `ApiKeys::with_check`
- the tokens are RS256 or PS256 (Google, Entra, Okta) → feature `authn-rsa`

## Quick start

```rust
use hop_top_kit::authn::{Credentials, Key, Options, Verifier};

fn authenticate(pem: &[u8], authorization: Option<&str>) {
    let key = Key::from_public_key_pem(pem).expect("a PUBLIC KEY block");
    let verifier = Verifier::new(
        vec![key],
        Options {
            issuer: Some("https://issuer.example".into()),
            audience: vec!["my-api".into()],
            ..Options::default()
        },
    )
    .expect("at least one key");

    let auth = verifier.auth_func();
    match auth(&Credentials { authorization, api_key: None }) {
        Ok(claims) => println!("{} acts for {:?}", claims.subject, claims.tenant),
        Err(refusal) => println!("401: {refusal}"),
    }
}
```

## Contract

- Go is canonical: signed tokens per algorithm, PEM key ids and API key hashes are pinned by [`contracts/authn-v1`](../../../../../contracts/authn-v1/README.md), run by both ports.
- Accepted algorithms: `EdDSA`, `ES256/384/512`, and with `authn-rsa` `RS256/384/512`, `PS256/384/512`. `none`, HMAC and anything unlisted are refused before any key is consulted, as is a `crit` header.
- Checks, in order: signature, `exp` present, issuer, `nbf`, `exp`, `iat` (each within `Options::clock_skew`, default one minute), audience (a trailing slash is ignored), `sub` present, then `Options::check`.
- The `kid` selects the key; a `kid` no key has, or none, tries every key and the signature decides. A JWK `alg` pins its key to that algorithm.
- Claims: `sub`, the tenant from `tenant` (or `Options::tenant_claim`), scopes from `scopes`, else `scope` (space-delimited), else `scp`.
- Refusals are `AuthnError`: `NoToken`, `InvalidToken`, `KeySetUnavailable` (no verdict; retry may succeed), `ApiKeyNotFound`, `Config`. Texts match Go's.
- `Key::from_identity` and an Ed25519 PEM yield the kid kit identity keypairs embed (first 16 hex digits of the SHA-256 of the raw key), so a kit tool's own tokens verify; other PEM keys use the RFC 7638 thumbprint.
- API keys are `kit_<id>_<secret>`; the store record is the JSON Go writes, so one store serves both ports. `with_prefix` and `with_hash_domain` keep a branded deployment's existing keys. `X-API-Key` wins over `Authorization: Bearer`.
- The public API names no signature crate: keys are `VerifyKey` bytes and the crates live in `provider/`. `authn` is wasm-safe; with no clock (wasm32) `Options::now` is required and a verifier without it refuses every token.
- RSA keys under 2048 or over 8192 bits are refused (ring's range). Without `authn-rsa` an RSA key joins a set but verifies nothing.
- Not ported: remote JWKS and OIDC discovery (fetch and cache). Back a `KeySource` with a guarded fetch.
- `ApiKeys` over a `kv::Store` serialises on one lock; last-used tracking (`with_last_used`) is a non-atomic read-modify-write and is off by default.

## Neighbours

- `hop_top_kit::kv`: the store behind `ApiKeys`
- `hop_top_kit::api`: the guarded client a remote key set fetches through
- `go/transport/authn`: the Go implementation
