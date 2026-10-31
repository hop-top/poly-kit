# authn-v1

## What it answers

What a token verifier and an API-key store must agree on across ports: which signed tokens verify (one per accepted algorithm), which are refused and why, the key id a PEM public key gets, and the hash an API key's secret is stored under. Wrong place for claim-extraction rules and transport behavior; those are covered by each port's own tests.

## Use it when

- you port `kit/transport/authn` to a new SDK: verify every token and PEM id here, and the API key hashes
- you change an accepted algorithm, the kid derivation or the API key hash: this file changes in the same commit, or tokens and stores written by one port stop working in another

## Quick start

```sh
make test-parity-authn
```

Regenerate (new keys, new signatures; re-baselines every other port):

```sh
go test ./go/transport/authn/ -run '^TestAuthnContractVectors$' -update-authn-vectors
```

## Contract

`vectors.json` pins, at the fixed clock `now`:

- `tokens`: for each of `EdDSA`, `ES256/384/512`, `RS256/384/512`, `PS256/384/512` a public JWK, a signed compact token and the principal, tenant, scopes and kid it must yield; plus refusals (expired, not yet valid, `iat` in the future, no `exp`, no `sub`, wrong issuer, wrong audience, tampered signature, `alg: none`, HS256) each with a substring of the error
- `pem`: PKIX public keys and the kid they get (the identity fingerprint for Ed25519, the RFC 7638 thumbprint otherwise)
- `api_keys`: a full key and the hash the store holds for it under the default hash domain

`apikey-records.json` (hand-written, not regenerated with `vectors.json`) pins the API key store beyond the default hash:

- `hash_domains`: a key under a branded prefix and/or hash domain verifies only with that prefix and domain named
- `records`: stored JSON with `name`, `labels` and `last_used_at` keeps every field through a rewrite (revoke), and a record without them gains none
- `last_used`: with last-used tracking on, a successful verify stamps last use in a separate kv entry (`apikey-last/<id>`), never rewriting the record, so it cannot undo a concurrent revoke; `Get` reads the merged value

The keys are throwaway; their private halves are not kept.

Remote JWKS and OIDC discovery are not in the corpus: fetch, cache and refresh rules are pinned by each port's own tests, which share the recorded Google exchanges in `go/transport/authn/testdata/cassettes` (Rust: `tests/authn_remote.rs`).

Consumers:

| Port | Loader |
|------|--------|
| Go | `go/transport/authn/contract_test.go` (`TestAuthnContractVectors`), `apikey_meta_test.go` (`TestAuthnContractVectorsAPIKeyRecords`) |
| Rust | `sdk/experimental/rs/tests/authn.rs` (feature `authn`; RSA vectors need `authn-rsa`), `tests/authn_apikeys.rs` (feature `authn-apikeys`; also `apikey-records.json`), `tests/authn_wasm.rs` |

## Neighbours

- `../identity-v1/`: the Ed25519 identity keypair whose tokens and kid this verifies
- `../kv-v1/`: the store API keys live in

## See also

- [Rust SDK reference](../../docs/adopters/reference/rs-sdk.md)
