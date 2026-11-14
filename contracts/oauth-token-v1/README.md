# oauth-token-v1

## What it answers

How an OAuth grant and its cached access token are stored in a secret store, so that every SDK reads what another wrote, derives the same cache key and reports the same failure classes. Wrong place for the uma `secret` / `client_cert` envelopes the grant points at, or for token-endpoint HTTP behaviour beyond the error mapping below.

## Use it when

- you add or change a grant kind, a grant field or a validation rule in `go/storage/secret/oauth`: add corpus cases first, then make every port pass
- you build an OAuth token source in another SDK: replay this corpus before anything else

## Quick start

```sh
make test-parity-oauth-token
```

Runs `TestContract*` in `go/storage/secret/oauth`. Go is the reference; a corpus case that disagrees with Go is a Go bug to fix, not a case to drop.

## Contract

| File | Pins |
|------|------|
| `grant.schema.json` | grant envelope: kinds `client_credentials`, `client_assertion`, `jwt_bearer`, `refresh_token`; per-kind required/forbidden refs; `client_secret_ref` on `refresh_token` only |
| `token.schema.json` | cached token = uma `oauth2` envelope |
| `grant-corpus.json` | valid and invalid grant documents; invalid ones name the offending `field` (`""` = whole-document fault: not an object, trailing data, unknown field) |
| `token-corpus.json` | same, for the cached token; valid ones give the UTC instant |
| `digest.json` | grant -> canonical JSON -> 16-hex digest -> token key `<ref>.token.<digest>` |
| `uma-envelope.json` | logical token <-> stored uma `oauth2` envelope pairs |
| `errors.json` | error classes and the OAuth error code mapping |

Rules the corpus pins beyond the schema: `token_url` is absolute https without userinfo or fragment, http only for loopback hosts; `client_id` required except `jwt_bearer`; scopes are non-empty, free of Unicode white space and control characters, unique; refs and `client_id`/`subject` carry no control characters; `v` must be the number 1; decode rejects unknown fields and trailing data. Cases marked `schema_gap` fail in code but are not expressible in the schema; every other invalid object document MUST fail the schema.

Digest: first 16 lowercase hex chars of SHA-256 over `{"client_cert_ref","client_id","client_secret_ref","kind","scopes"(sorted),"secret_ref","subject","token_url"}` in that (alphabetical) key order, no whitespace, scopes always an array, absent strings `""`, no HTML escaping, U+2028/U+2029 written as `\u2028`/`\u2029`. Kind and every ref are part of it, so re-pointing key material or changing the kind changes the cache key.

Error classes: `credential_rejected`, `needs_reconsent`, `not_writable`, `transient`. `invalid_client` and `unauthorized_client` map to `credential_rejected`; `invalid_grant`, `interaction_required`, `consent_required` and `login_required` map to `needs_reconsent` for `refresh_token` and to `credential_rejected` otherwise; every other 4xx maps to `credential_rejected`; 5xx, 429, 408, network failure, timeout, cancellation and a secret store read failure (other than not found) map to `transient`; a rotating grant on a non-writable backend is `not_writable`. Unclassified (no class, plain error): a 3xx reply (redirects are never followed), a TLS certificate verification failure and a 2xx reply missing `access_token` or `expires_in`. A secret ref not found in the store is a validation error naming the ref field. Messages carry the status and an allowlisted OAuth error code only. Messages never contain credential values.

Not pinned: JSON `null` for an optional field (Go treats it as absent; do not emit it).

All fixture values are fake and runtime-shaped (`fake-access-token-1`); never add provider-format keys or tokens.

## Parity claim

A port claims oauth-token-v1 parity when its test suite:

1. decodes every `grant-corpus.json` and `token-corpus.json` case: valid ones succeed and re-encode to the recorded document (or `encoded`); invalid ones fail with exactly the recorded `field`
2. validates every object document against both schemas: valid pass, invalid fail unless `schema_gap`
3. reproduces every `digest.json` vector exactly: canonical bytes, digest and token key
4. encodes and decodes every `uma-envelope.json` pair
5. exposes the four error classes as distinct matchable errors and applies the `errors.json` mapping

The fixtures are edited by hand. `make lint-config` checks they parse; `make test-parity-oauth-token` checks the Go reference still agrees.

## Neighbours

- `go/storage/secret/oauth/contract_test.go`: Go replayer
- `go/storage/secret/README.md`: design of the OAuth token source
- `../kv-v1/`: the store the envelopes live in
