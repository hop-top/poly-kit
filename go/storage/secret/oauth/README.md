# oauth

## What it answers

How a host turns a long-lived credential in a `secret` store into a
short-lived OAuth 2 access token, cached and refreshed. Wrong package for
serving consent (`go/transport/cmdsurface`) or for storing the credential
itself (`go/storage/secret`).

## Use it when

- a client secret, certificate, service account key or refresh token sits in a store and a provider wants a bearer token
- a rotating refresh token must survive restarts and concurrent processes

## Quick start

```go
src, err := oauth.TokenSource(ctx, store, "app/graph")
if err != nil {
	return err
}
tok, err := src.Token(ctx) // refreshed when under the skew remains
```

One runnable example per grant: `example_*_test.go`.

## Contract

- Grants: `client_credentials`, `client_assertion`, `jwt_bearer`, `refresh_token`.
- Errors match `ErrCredentialRejected`, `ErrNeedsReconsent`, `ErrNotWritable`, `ErrTransient`.
- Rotating grants need a writable store (`keyring`, `file`, `openbao`).
- Formats: [`contracts/oauth-token-v1`](../../../../contracts/oauth-token-v1/README.md).
- Tests: embedded, xrr cassettes per grant.

## Neighbours

- `hop.top/kit/go/transport/cmdsurface`: `LoopbackPKCE`, `MountOAuthPKCE` produce the refresh grant.
- `hop.top/kit/go/console/cli`: `NewOAuthIntrospector` reports a `Source`.

## See also

- [OAuth token source](../../../../docs/adopters/concepts/oauth-token-source.md)
- [`secret/README.md`](../README.md)
