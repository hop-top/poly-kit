# authn/remote

## What it answers

How does a verifier get its signing keys from a URL: a JWKS document or an OpenID Connect issuer's discovery, cached, refreshed and rotated? Remote half of `hop_top_kit::authn` (feature `authn-remote`, native only), a port of the remote half of Go's `transport/authn`. Wrong place for verifying tokens: that is `authn::Verifier`.

## Use it when

- the issuer publishes a JWKS → `new_jwks(url, Remote::default(), opts)`
- the issuer publishes OIDC discovery → `new_oidc(issuer, ..)`
- tests or an adopter transport must supply the documents → implement `Fetch`; `HttpFetch` is the default

## Contract

- The set is fetched on first use, again once older than `Remote::refresh`, and when a token names an unknown `kid`, at most once per `Remote::min_refresh`.
- A failed refetch keeps the last good set; a set with no signing key is a failure.
- URLs are `https`, or `http` to a loopback host. Fetches go through the guarded client, so `--offline` and allowlists apply.
- The cache and refresh rules do no I/O; `http.rs` is the only module naming an HTTP crate.

## Neighbours

- [`authn`](../README.md): verifier, keys, claims, API keys
- `hop_top_kit::netpolicy`: the guard every fetch passes through
